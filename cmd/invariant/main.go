// Command invariant is the Invariant CLI.
//
//	invariant verify [-out DIR] DIR      run every gate check on a project
//	invariant synthesize [-out DIR] DIR  have a coding agent write the model and code, then gate them
//	invariant formalize [-out DIR] FILE  have a coding agent draft statements for a request
//	invariant watch -repo OWNER/NAME     turn the repository's issues into merged pull requests
//	invariant scope [-base REF] [HEAD]   check that a factory pull request stays in bounds
//	invariant ratification -repo R DIR   check a factory project's ratification on GitHub
//	invariant pin DIR                    record the current statement text as ratified
//	invariant trace FILE                 replay a counterexample trace
//	invariant decisions ...              record decisions and ask what rests on them
//	invariant mcp ...                    serve the gate or the check to an agent (synthesize and formalize start it)
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/dashboard"
	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/mcp"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/scope"
	"github.com/gitdek/invariant/internal/setup"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

const usage = `Invariant proves code against statements people ratified.

Usage:
  invariant verify [-out DIR] PROJECT          run every gate check and print the receipt
  invariant synthesize [-out DIR] PROJECT      have a coding agent write the model and code, then gate them
  invariant formalize [-out DIR] REQUEST.md    have a coding agent draft statements for a request
  invariant watch -repo OWNER/NAME [-once] [-app-id ID] [-agent A] [-language L] [-lease D] [-parallel N]
                                               turn the repository's issues into merged pull requests
  invariant scope [-base REF] [HEAD]           check that a factory pull request stays in bounds
  invariant ratification -repo OWNER/NAME PROJECT|PLAN...
                                               check factory projects' and plans' ratifications on GitHub
  invariant decisions COMMAND ...              record decisions and ask what rests on them
  invariant pin PROJECT                        record the statements' current text as ratified
  invariant trace FILE                         replay a counterexample trace
  invariant dashboard -repo OWNER/NAME [-repo OWNER/NAME]... [-work DIR]... [-addr HOST:PORT]
                                               serve a live view of the factory and its evidence

  invariant ledger -repo OWNER/NAME [-json]      what each of the factory's issues took: time, comments, spend
  invariant init [-repo OWNER/NAME] [-invariant COMMIT] [DIR]
                                               set up another repository's gate
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var code int
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "verify":
		code = verifyCmd(ctx, args)
	case "pin":
		code = pinCmd(args)
	case "trace":
		code = traceCmd(args)
	case "synthesize":
		code = synthesizeCmd(ctx, args)
	case "formalize":
		code = formalizeCmd(ctx, args)
	case "watch":
		code = watchCmd(ctx, args)
	case "scope":
		code = scopeCmd(ctx, args)
	case "ratification":
		code = ratificationCmd(ctx, args)
	case "mcp":
		code = mcpCmd(ctx, args)
	case "dashboard":
		code = dashboardCmd(ctx, args)
	case "init":
		code = initCmd(args)
	case "ledger":
		code = ledgerCmd(ctx, args)
	case "decisions":
		code = decisionsCmd(ctx, args)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "invariant: unknown command %q\n\n%s", cmd, usage)
		code = 2
	}
	os.Exit(code)
}

func verifyCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	out := fs.String("out", "", "write receipt.md, receipt.json and traces/ to this directory")
	fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	r, err := verify.Run(ctx, fs.Arg(0), *out, tc)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	md := receipt.Markdown(r)
	fmt.Print(md)
	if *out != "" {
		js, err := json.MarshalIndent(r, "", "  ")
		if err == nil {
			err = os.MkdirAll(*out, 0o755)
		}
		if err == nil {
			err = os.WriteFile(filepath.Join(*out, "receipt.json"), append(js, '\n'), 0o644)
		}
		if err == nil {
			err = os.WriteFile(filepath.Join(*out, "receipt.md"), []byte(md), 0o644)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "invariant:", err)
			return 2
		}
	}
	if !r.Passed {
		return 1
	}
	return 0
}

func pinCmd(args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	p, err := project.Load(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	changed, err := p.Pin()
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	if len(changed) == 0 {
		fmt.Println("Every pin already matches the statements' text.")
		return 0
	}
	fmt.Printf("Pinned the current text of: %s.\n", strings.Join(changed, ", "))
	fmt.Println("Pinning records a ratification. Log it in decisions/log.md.")
	return 0
}

func traceCmd(args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	b, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	var f tlc.TraceFile
	if err := json.Unmarshal(b, &f); err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	fmt.Printf("%s · %s", f.Module, f.Check)
	if f.Violated != "" {
		fmt.Printf(" · %s violated", f.Violated)
	}
	fmt.Println()
	for _, s := range f.States {
		fmt.Printf("\n%d  %s\n", s.Index, s.Action)
		names := make([]string, 0, len(s.TLA))
		for name := range s.TLA {
			names = append(names, name)
		}
		sort.Strings(names)
		changed := map[string]bool{}
		for _, c := range s.Changed {
			changed[c] = true
		}
		for _, name := range names {
			marker := "   "
			if changed[name] {
				marker = " * "
			}
			fmt.Printf("%s%s = %s\n", marker, name, s.TLA[name])
		}
	}
	switch {
	case f.Loop > 0:
		fmt.Printf("\nThen back to %d, forever.\n", f.Loop)
	case f.Stutters:
		fmt.Printf("\nThen nothing more happens, forever.\n")
	}
	return 0
}

func synthesizeCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("synthesize", flag.ExitOnError)
	out := fs.String("out", "out/synthesis", "where the result, its receipt and the logs go")
	agent := fs.String("agent", "claude-code", "the coding agent that writes the model and code: claude-code, the only one Invariant can run yet")
	model := fs.String("model", "opus", "the model the agent uses")
	effort := fs.String("effort", "max", "how hard the agents think: low, medium, high, xhigh or max")
	fallback := fs.String("fallback-effort", "xhigh", "the effort the agent runs at once more when the loop guard stops its first run, if it's below -effort; empty never runs it again")
	budget := fs.Float64("budget", 5, "cap on the agent's estimated cost for the run, in USD (claude --max-budget-usd)")
	turns := fs.Int("max-turns", 80, "cap on the agent's turns")
	runs := fs.Int("gate-runs", 4, "the most gate runs the agent gets: one attempt and three repairs")
	timeout := fs.Duration("timeout", 40*time.Minute, "wall-clock cap on the agent's run")
	claude := fs.String("claude", "claude", "the Claude Code CLI")
	draft := fs.Bool("draft", false, "start from the module's drafted model instead of a skeleton of the pinned definitions")
	fs.Parse(args)
	if !validAgent(*agent) {
		fmt.Fprintf(os.Stderr, "invariant: -agent is claude-code, the only coding agent Invariant can run yet, not %q\n", *agent)
		return 2
	}
	if !validEffort(*effort) {
		fmt.Fprintf(os.Stderr, "invariant: -effort is low, medium, high, xhigh or max, not %q\n", *effort)
		return 2
	}
	if *fallback != "" && !validEffort(*fallback) {
		fmt.Fprintf(os.Stderr, "invariant: -fallback-effort is empty, low, medium, high, xhigh or max, not %q\n", *fallback)
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	r, err := synth.Synthesize(ctx, synth.Options{
		Project: fs.Arg(0), Out: *out, Binary: self, GateRuns: *runs, Timeout: *timeout, Toolchain: tc, KeepModel: *draft,
		Backend:        synth.ClaudeCode{Binary: *claude, Model: *model, BudgetUSD: *budget, MaxTurns: *turns, Effort: *effort},
		FallbackEffort: fallbackEffort(*effort, *fallback),
	})
	if r != nil && r.Final != nil {
		md := receipt.Markdown(r.Final)
		summary := synth.Summary(r) + md
		fmt.Print(summary)
		js, _ := json.MarshalIndent(r.Final, "", "  ")
		for name, text := range map[string]string{"synthesis.md": summary, "gate/receipt.md": md, "gate/receipt.json": string(js) + "\n"} {
			path := filepath.Join(*out, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
				os.WriteFile(path, []byte(text), 0o644)
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		if r == nil || r.Final == nil {
			return 2
		}
	}
	if !r.Final.Passed {
		return 1
	}
	return 0
}

// mcpCmd serves the gate as an MCP tool for a synthesis agent. Every run
// checks a project assembled from the ratified project's protected files and
// the workspace's model and code, so the agent can't move the goalposts.
func mcpCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	ratified := fs.String("ratified", "", "the original project, whose protected files every gate run uses")
	formal := fs.Bool("formalize", false, "serve the check tool to a formalizing agent, instead of the gate")
	maxRuns := fs.Int("max-runs", 4, "the most gate runs to allow")
	logPath := fs.String("log", "", "append a line per gate run to this file")
	graph := fs.Bool("decisions", false, "serve the decision graph's tools for the checkout at -C")
	dir := fs.String("C", ".", "with -decisions: the checkout")
	storePath := fs.String("store", "", "with -decisions: the store (default: the one on this machine)")
	write := fs.Bool("write", false, "with -decisions: let the agent record decisions and links; it never ratifies")
	plan := fs.Bool("plan", false, "serve the check tool to an agent drafting a plumbing plan, with the repository under WORKSPACE/repo")
	pipes := fs.Bool("plumbing", false, "serve the test tool to an agent building issue -issue's ratified plan, from the checkout at -ratified")
	issue := fs.Int("issue", 0, "with -plumbing: the issue whose plan is built")
	cache := fs.String("cache", "", "with -plan or -plumbing: a Go build cache to keep between test runs")
	issues := fs.Bool("issues", false, "serve the check tool to an agent drafting a PRD's plan of issues in WORKSPACE")
	fs.Parse(args)
	if *graph {
		return decisionsMCP(ctx, *dir, *storePath, *write)
	}
	if *issues && fs.NArg() == 1 {
		server := mcp.Server{Name: "invariant", Version: "0.6", Tools: []mcp.Tool{issuesTool(fs.Arg(0), *maxRuns, *logPath)}}
		if err := server.Serve(ctx, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "invariant:", err)
			return 1
		}
		return 0
	}
	if (*plan || *pipes) && fs.NArg() == 1 {
		sb, err := sandbox(ctx, *cache)
		if err != nil {
			fmt.Fprintln(os.Stderr, "invariant:", err)
			return 2
		}
		tool := planTool(fs.Arg(0), sb, *maxRuns, *logPath)
		if *pipes {
			if *ratified == "" || *issue == 0 {
				fmt.Fprintln(os.Stderr, "usage: invariant mcp -plumbing -ratified CHECKOUT -issue N [-max-runs N] [-log FILE] [-cache DIR] WORKSPACE")
				return 2
			}
			tool = testTool(*ratified, *issue, fs.Arg(0), sb, *maxRuns, *logPath)
		}
		server := mcp.Server{Name: "invariant", Version: "0.5", Tools: []mcp.Tool{tool}}
		if err := server.Serve(ctx, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "invariant:", err)
			return 1
		}
		return 0
	}
	if fs.NArg() != 1 || (*ratified == "") == !*formal {
		fmt.Fprintln(os.Stderr, "usage: invariant mcp (-ratified PROJECT | -formalize) [-max-runs N] [-log FILE] WORKSPACE\n       invariant mcp -decisions [-C DIR] [-store FILE] [-write]")
		return 2
	}
	ws := fs.Arg(0)
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	if *formal {
		server := mcp.Server{Name: "invariant", Version: "0.4", Tools: []mcp.Tool{checkTool(ws, tc, *maxRuns, *logPath)}}
		if err := server.Serve(ctx, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "invariant:", err)
			return 1
		}
		return 0
	}
	p, err := project.Load(*ratified)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	runs := 0
	gate := mcp.Tool{
		Name: "gate",
		Description: fmt.Sprintf("Run every check of Invariant's gate on your model and code. It reports what failed, "+
			"with TLC counterexamples and verifier errors. You have %d runs in total.", *maxRuns),
		Schema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		Call: func(ctx context.Context, _ json.RawMessage) (string, bool) {
			if runs >= *maxRuns {
				return fmt.Sprintf("No gate runs left: you've used all %d.", *maxRuns), true
			}
			runs++
			dir, err := os.MkdirTemp("", "invariant-gate-")
			if err != nil {
				return "The gate couldn't start: " + err.Error(), true
			}
			defer os.RemoveAll(dir)
			proj, err := synth.Stage(p, ws, dir)
			if err != nil {
				return "The gate couldn't assemble your project: " + err.Error(), true
			}
			r, err := verify.Run(ctx, proj, "", tc)
			if err != nil {
				if *logPath != "" {
					synth.LogGateRun(*logPath, synth.GateRun{Run: runs, Failed: []string{"couldn't run: " + err.Error()}, At: time.Now().UTC().Format(time.RFC3339)})
				}
				return fmt.Sprintf("The gate couldn't run: %v\n\n(Gate run %d of %d.)", err, runs, *maxRuns), true
			}
			if *logPath != "" {
				synth.LogGateRun(*logPath, synth.GateRun{Run: runs, Passed: r.Passed, Failed: verify.Failed(r), At: time.Now().UTC().Format(time.RFC3339)})
			}
			return verify.Feedback(r) + fmt.Sprintf("\n\n(Gate run %d of %d.)", runs, *maxRuns), false
		},
	}
	server := mcp.Server{Name: "invariant", Version: "0.2", Tools: []mcp.Tool{gate}}
	if err := server.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 1
	}
	return 0
}

// sandbox is where plumbing's tests run: the pinned Go image, with this
// machine's module cache read-only.
func sandbox(ctx context.Context, cache string) (plumbing.Sandbox, error) {
	out, err := exec.CommandContext(ctx, "go", "env", "GOMODCACHE").Output()
	if err != nil {
		return plumbing.Sandbox{}, fmt.Errorf("finding the module cache: %w", err)
	}
	image, err := toolchain.PlumbingImage(ctx)
	if err != nil {
		return plumbing.Sandbox{}, err
	}
	return plumbing.Sandbox{Image: image, ModCache: strings.TrimSpace(string(out)), Cache: cache}, nil
}

// planTool checks the plan an agent drafts for a plumbing issue (D-0105):
// it holds together, and its acceptance tests fail on the code as it is.
func planTool(ws string, sb plumbing.Sandbox, maxRuns int, logPath string) mcp.Tool {
	runs := 0
	return mcp.Tool{
		Name: "check",
		Description: fmt.Sprintf("Check the plan in plan.json and its acceptance tests under tests/ against the repository in repo/: "+
			"the plan holds together, and every acceptance test fails on the code as it is. You have %d checks in total.", maxRuns),
		Schema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		Call: func(ctx context.Context, _ json.RawMessage) (string, bool) {
			if runs >= maxRuns {
				return fmt.Sprintf("No checks left: you've used all %d.", maxRuns), true
			}
			runs++
			if _, err := os.Stat(filepath.Join(ws, "plan.json")); err != nil {
				if _, err := os.Stat(filepath.Join(ws, "proposal.json")); err == nil {
					return fmt.Sprintf("There's no plan.json, and proposal.json asks questions or says the issue is unsupported, so there's nothing to check. "+
						"If that's what you mean to send, you're done.\n\n(Check %d of %d.)", runs, maxRuns), false
				}
			}
			c, err := plumbing.Check(ctx, ws, filepath.Join(ws, "repo"), sb)
			if err != nil {
				return fmt.Sprintf("The plan couldn't be checked: %v\n\n(Check %d of %d.)", err, runs, maxRuns), true
			}
			if logPath != "" {
				run := synth.GateRun{Run: runs, Passed: c.Problem == "", At: time.Now().UTC().Format(time.RFC3339)}
				if c.Problem != "" {
					run.Failed = []string{c.Problem}
				}
				synth.LogGateRun(logPath, run)
			}
			return c.Feedback() + fmt.Sprintf("\n\n(Check %d of %d.)", runs, maxRuns), false
		},
	}
}

// issuesTool checks the plan of issues an agent drafts for a PRD (#112): it
// holds together as a plan a person can ratify.
func issuesTool(ws string, maxRuns int, logPath string) mcp.Tool {
	runs := 0
	return mcp.Tool{
		Name: "check",
		Description: fmt.Sprintf("Check the plan of issues in issues.json: it has a name, a summary and 1 to %d issues, each with a title and a whole body that's "+
			"modeled, with a Project: line, or plumbing, with a Kind: plumbing line, and carries no /invariant command. You have %d checks in total.", formalize.MaxIssues, maxRuns),
		Schema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		Call: func(_ context.Context, _ json.RawMessage) (string, bool) {
			if runs >= maxRuns {
				return fmt.Sprintf("No checks left: you've used all %d.", maxRuns), true
			}
			runs++
			if _, err := os.Stat(filepath.Join(ws, "issues.json")); err != nil {
				if _, err := os.Stat(filepath.Join(ws, "proposal.json")); err == nil {
					return fmt.Sprintf("There's no issues.json, and proposal.json asks questions or says the issue is unsupported, so there's nothing to check. "+
						"If that's what you mean to send, you're done.\n\n(Check %d of %d.)", runs, maxRuns), false
				}
			}
			plan, err := formalize.ReadIssues(ws)
			if logPath != "" {
				run := synth.GateRun{Run: runs, Passed: err == nil, At: time.Now().UTC().Format(time.RFC3339)}
				if err != nil {
					run.Failed = []string{err.Error()}
				}
				synth.LogGateRun(logPath, run)
			}
			if err != nil {
				return fmt.Sprintf("The plan can't be proposed yet: %v.\n\n(Check %d of %d.)", err, runs, maxRuns), false
			}
			return fmt.Sprintf("The plan holds together: each of its %d issues has a title and a body that's modeled or plumbing, and none carries a command.\n\n(Check %d of %d.)",
				len(plan.Issues), runs, maxRuns), false
		},
	}
}

// testTool runs a plumbing build's checks for its agent: the ratified plan
// and its acceptance tests from the checkout at root, and the agent's
// changes to the files the plan names, in the sandbox.
func testTool(root string, n int, ws string, sb plumbing.Sandbox, maxRuns int, logPath string) mcp.Tool {
	runs := 0
	return mcp.Tool{
		Name: "test",
		Description: fmt.Sprintf("Run gofmt on the files the change touches, go vet ./..., the tests of every package it touches, and the acceptance tests, on your changes to the plan's files, "+
			"in a sandbox with no network. You have %d runs in total.", maxRuns),
		Schema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		Call: func(ctx context.Context, _ json.RawMessage) (string, bool) {
			if runs >= maxRuns {
				return fmt.Sprintf("No test runs left: you've used all %d.", maxRuns), true
			}
			runs++
			lock, err := plumbing.ReadLockFile(root, n)
			if err != nil {
				return "The ratified plan can't be read: " + err.Error(), true
			}
			staged, err := os.MkdirTemp("", "invariant-plumbing-test-")
			if err != nil {
				return "The test run couldn't start: " + err.Error(), true
			}
			defer os.RemoveAll(staged)
			if err := plumbing.Stage(ctx, root, ws, &lock.Plan, staged); err != nil {
				return "Your changes couldn't be staged: " + err.Error(), true
			}
			r, err := sb.Run(ctx, staged, &lock.Plan)
			if err != nil {
				return fmt.Sprintf("The tests couldn't run: %v\n\n(Test run %d of %d.)", err, runs, maxRuns), true
			}
			if logPath != "" {
				synth.LogGateRun(logPath, synth.GateRun{Run: runs, Passed: r.Passed(), Failed: r.Failing(), At: time.Now().UTC().Format(time.RFC3339)})
			}
			return r.Summary() + "\n" + r.Tail(8000) + fmt.Sprintf("\n\n(Test run %d of %d.)", runs, maxRuns), false
		},
	}
}

// checkTool runs the gate's model checks on a formalizing agent's draft.
func checkTool(ws string, tc toolchain.Toolchain, maxRuns int, logPath string) mcp.Tool {
	runs := 0
	return mcp.Tool{
		Name: "check",
		Description: fmt.Sprintf("Pin the statements in proposal.json and run the gate's model checks on your draft: TLC, "+
			"the witnesses and the known bugs. It reports what failed, with counterexamples. You have %d checks in total.", maxRuns),
		Schema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		Call: func(ctx context.Context, _ json.RawMessage) (string, bool) {
			if runs >= maxRuns {
				return fmt.Sprintf("No checks left: you've used all %d.", maxRuns), true
			}
			runs++
			record := func(passed bool, failed ...string) {
				if logPath != "" {
					synth.LogGateRun(logPath, synth.GateRun{Run: runs, Passed: passed, Failed: failed, At: time.Now().UTC().Format(time.RFC3339)})
				}
			}
			p, r, err := formalize.Check(ctx, ws, tc)
			switch {
			case err != nil:
				record(false, "draft: "+err.Error())
				return fmt.Sprintf("The draft can't be checked: %v\n\n(Check %d of %d.)", err, runs, maxRuns), true
			case r == nil:
				record(true)
				return fmt.Sprintf("proposal.json asks questions or says the issue is unsupported (%d forks), so there's nothing "+
					"to model-check. If that's what you mean to send, you're done.\n\n(Check %d of %d.)", len(p.Forks), runs, maxRuns), false
			}
			record(r.Passed, verify.Failed(r)...)
			return verify.Feedback(r) + fmt.Sprintf("\n\n(Check %d of %d.)", runs, maxRuns), false
		},
	}
}

func formalizeCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("formalize", flag.ExitOnError)
	out := fs.String("out", "out/formalize", "where the draft, the transcript and the check log go")
	agent := fs.String("agent", "claude-code", "the coding agent that drafts the statements: claude-code, the only one Invariant can run yet")
	model := fs.String("model", "opus", "the model the agent uses")
	effort := fs.String("effort", "max", "how hard the agents think: low, medium, high, xhigh or max")
	budget := fs.Float64("budget", 3, "cap on the agent's estimated cost for the run, in USD (claude --max-budget-usd)")
	turns := fs.Int("max-turns", 60, "cap on the agent's turns")
	checks := fs.Int("checks", 4, "the most model checks the agent gets")
	timeout := fs.Duration("timeout", 25*time.Minute, "wall-clock cap on the agent's run")
	claude := fs.String("claude", "claude", "the Claude Code CLI")
	fs.Parse(args)
	if !validAgent(*agent) {
		fmt.Fprintf(os.Stderr, "invariant: -agent is claude-code, the only coding agent Invariant can run yet, not %q\n", *agent)
		return 2
	}
	if !validEffort(*effort) {
		fmt.Fprintf(os.Stderr, "invariant: -effort is low, medium, high, xhigh or max, not %q\n", *effort)
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	text, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	title, body, _ := strings.Cut(strings.TrimSpace(string(text)), "\n")
	req := formalize.Request{Repo: "local", Issue: 0, Title: strings.TrimLeft(title, "# "), Body: body, Author: "you"}
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	f := formalize.Formalizer{Backend: synth.ClaudeCode{Binary: *claude, Model: *model, BudgetUSD: *budget, MaxTurns: *turns, Effort: *effort},
		Binary: self, CheckRuns: *checks, Timeout: *timeout, Toolchain: tc}
	r, err := f.Formalize(ctx, req, *out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	fmt.Printf("%s, %d turns, about $%.2f by the agent's own estimate. Checks: %d.\n\n", r.Usage.Model, r.Usage.Turns, r.Usage.CostUSD, len(r.CheckRuns))
	switch p := r.Proposal; {
	case r.Problem != "":
		fmt.Println("No usable draft:", r.Problem)
		return 1
	case p.Unsupported != "":
		fmt.Println("Unsupported:", p.Unsupported)
	case len(p.Forks) > 0:
		for _, fork := range p.Forks {
			fmt.Printf("%s. %s\n", fork.ID, fork.Question)
			for _, o := range fork.Options {
				fmt.Printf("   %s. %s\n", o.ID, o.Says)
			}
		}
	default:
		fmt.Printf("Proposal %s for %s, within %v:\n", p.Hash, p.Name, p.Bounds)
		for _, s := range p.Statements {
			fmt.Printf("  %-12s %-9s %s\n", s.Name, s.Kind, s.Says)
		}
		fmt.Printf("\nChecked: TLC explored %d states; witnesses and known bugs as above. Draft in %s.\n", r.Report.Design.DistinctStates, *out)
	}
	return 0
}

func watchCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	repo := fs.String("repo", "", "the repository to watch, as owner/name")
	every := fs.Duration("every", time.Minute, "how often to poll GitHub")
	once := fs.Bool("once", false, "poll once and exit")
	base := fs.String("base", "main", "the branch pull requests merge into")
	projects := fs.String("projects", "examples", "the directory new projects go in")
	language := fs.String("language", "go", "the code's language when an issue has no language label: go, typescript or python")
	cache, _ := os.UserCacheDir()
	work := fs.String("work", filepath.Join(cache, "invariant", "watch"), "where the clone, transcripts and logs go")
	agent := fs.String("agent", "claude-code", "the coding agent that drafts and builds an issue when neither it nor its project picks one: claude-code, the only one Invariant can run yet")
	model := fs.String("model", "opus", "the model the agents use")
	effort := fs.String("effort", "max", "how hard the agents think: low, medium, high, xhigh or max")
	fallback := fs.String("fallback-effort", "xhigh", "the effort a build's agent runs at once more when the loop guard stops its first run, if it's below -effort; empty never runs it again")
	fbudget := fs.Float64("formalize-budget", 3, "cap on a formalization's estimated cost, in USD")
	budget := fs.Float64("budget", 5, "cap on a synthesis's estimated cost, in USD")
	turns := fs.Int("max-turns", 80, "cap on an agent's turns")
	runs := fs.Int("gate-runs", 4, "the most gate runs a synthesis gets")
	timeout := fs.Duration("timeout", 40*time.Minute, "wall-clock cap on an agent's run")
	claude := fs.String("claude", "claude", "the Claude Code CLI")
	appID := fs.Int64("app-id", 0, "the factory's GitHub App; without one, the factory acts as whoever gh is logged in as")
	home, _ := os.UserHomeDir()
	appKey := fs.String("app-key", filepath.Join(home, ".config", "invariant", "factory.pem"), "the App's private key")
	leaseFor := fs.Duration("lease", 5*time.Minute, "how long the watcher's lease on the repository lasts, renewed every poll: over 2m and at least three polls; 0 watches without one")
	parallel := fs.Int("parallel", 3, "how many issues the watcher takes steps on at once, one step per issue; 1 takes one step at a time")
	fs.Parse(args)
	if !validAgent(*agent) {
		fmt.Fprintf(os.Stderr, "invariant: -agent is claude-code, the only coding agent Invariant can run yet, not %q\n", *agent)
		return 2
	}
	if !validEffort(*effort) {
		fmt.Fprintf(os.Stderr, "invariant: -effort is low, medium, high, xhigh or max, not %q\n", *effort)
		return 2
	}
	if *fallback != "" && !validEffort(*fallback) {
		fmt.Fprintf(os.Stderr, "invariant: -fallback-effort is empty, low, medium, high, xhigh or max, not %q\n", *fallback)
		return 2
	}
	if *repo == "" || fs.NArg() != 0 || *parallel < 1 || formalize.Languages[*language] == "" || (*leaseFor > 0 && (*leaseFor < 3*(*every) || *leaseFor <= 2*time.Minute)) {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	logger := log.New(os.Stderr, "invariant: ", log.LstdFlags)
	fail := func(err error) int { logger.Print(err); return 2 }

	gh := github.Client{Repo: *repo}
	dir := filepath.Join(*work, filepath.FromSlash(*repo))
	clone := factory.Clone{Dir: filepath.Join(dir, "clone"), Remote: "https://github.com/" + *repo + ".git"}
	var bot, actor string
	if *appID != 0 {
		// The factory acts as its App's bot (D-0041): it comments, pushes and
		// merges with the App's installation token. Reading the repository
		// still uses your git credentials.
		app := &github.App{ID: *appID, KeyPath: *appKey, Repo: *repo}
		id, err := app.Identity(ctx)
		if err != nil {
			return fail(fmt.Errorf("the factory's App: %w", err))
		}
		if _, err := app.Token(ctx); err != nil {
			return fail(err)
		}
		gh.Token, clone.Token = app.Token, app.Token
		bot, actor = id.Login, id.Login
		clone.Name, clone.Email = id.Login, id.Email()
	} else {
		me, err := gh.Viewer(ctx)
		if err != nil {
			return fail(fmt.Errorf("gh must be logged in: %w", err))
		}
		actor = me.Login
		clone.Name, clone.Email = me.Name, fmt.Sprintf("%d+%s@users.noreply.github.com", me.ID, me.Login)
		if clone.Name == "" {
			clone.Name = me.Login
		}
	}
	// The factory merges only once invariant/gate passes, and with a merge
	// commit, so it doesn't start on a repository where its pull requests
	// never could merge.
	if err := setup.Check(ctx, gh, *repo, *base); err != nil {
		return fail(err)
	}
	if err := clone.Ensure(ctx); err != nil {
		return fail(err)
	}
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		return fail(err)
	}
	self, err := os.Executable()
	if err != nil {
		return fail(err)
	}
	sb, err := sandbox(ctx, "")
	if err != nil {
		return fail(err)
	}
	f := &factory.Factory{
		Repository: *repo, GitHub: gh, Repo: clone, Base: *base, Projects: *projects, Check: "invariant/gate", Language: *language, Self: bot,
		Work: filepath.Join(dir, "issues"), Log: logger.Printf,
		// Claude Code is the watcher's own agent, and the only one Invariant
		// can run yet, so it drafts, builds and reviews every issue (#173).
		Agent: *agent,
		Formalizer: formalize.Formalizer{Backend: synth.ClaudeCode{Binary: *claude, Model: *model, BudgetUSD: *fbudget, MaxTurns: *turns, Effort: *effort},
			Binary: self, CheckRuns: 4, Timeout: *timeout, Toolchain: tc, Sandbox: sb},
		Builder: factory.Synthesis{Options: synth.Options{Backend: synth.ClaudeCode{Binary: *claude, Model: *model, BudgetUSD: *budget, MaxTurns: *turns, Effort: *effort},
			Binary: self, GateRuns: *runs, Timeout: *timeout, Toolchain: tc, FallbackEffort: fallbackEffort(*effort, *fallback)}},
		// A plumbing issue's plan is built with tests, not proofs, and a
		// second agent reviews it (D-0105). Only builds fall back (D-0125).
		Plumbing: plumbing.Builder{Backend: synth.ClaudeCode{Binary: *claude, Model: *model, BudgetUSD: *budget, MaxTurns: *turns, Effort: *effort},
			Reviewer: synth.ClaudeCode{Binary: *claude, Model: *model, BudgetUSD: *fbudget, MaxTurns: *turns, Effort: *effort},
			Binary:   self, TestRuns: *runs + 2, Timeout: *timeout, Sandbox: sb, FallbackEffort: fallbackEffort(*effort, *fallback)},
		Holder: factory.NewHolder(), LeaseFor: *leaseFor, Parallel: *parallel,
	}
	if err := f.Prepare(ctx); err != nil {
		return fail(err)
	}
	// What the factory is doing goes in a status file for the dashboard
	// (D-0049). It holds no secrets: the process, the repository, the
	// watcher's name on its lease, which the page follows, and the steps in
	// hand, each of which the page shows (D-0113). Issue, doing and since
	// name the longest-running step that's doing something.
	statusPath := dashboard.StatusPath(*work, *repo)
	status := dashboard.Status{PID: os.Getpid(), Repo: *repo, Holder: f.Holder, Started: time.Now().UTC(), Every: every.Seconds(), Limit: timeout.Seconds()}
	f.Activity = func(steps []factory.Step) {
		status.Heartbeat = time.Now().UTC()
		status.Issue, status.Doing, status.Since, status.Steps = 0, "", nil, nil
		for _, s := range steps {
			since := s.Since.UTC()
			status.Steps = append(status.Steps, dashboard.Step{Issue: s.Issue, Doing: s.Doing, Since: since})
			if s.Doing != "" && (status.Since == nil || since.Before(*status.Since)) {
				status.Issue, status.Doing, status.Since = s.Issue, s.Doing, &since
			}
		}
		if err := dashboard.WriteStatus(statusPath, status); err != nil {
			logger.Printf("status: %v", err)
		}
	}
	f.Activity(nil)
	defer os.Remove(statusPath)
	logger.Printf("watching %s as @%s; commits by %s <%s>", *repo, actor, clone.Name, clone.Email)
	if *once {
		// The poll's steps can run on after it returns, so it waits for them.
		err := f.Poll(ctx)
		f.Wait()
		if err != nil {
			return fail(err)
		}
		return 0
	}
	if err := f.Watch(ctx, *every); err != nil && ctx.Err() == nil {
		return fail(err)
	}
	return 0
}

func scopeCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("scope", flag.ExitOnError)
	base := fs.String("base", "origin/main", "the ref the pull request merges into")
	dir := fs.String("C", ".", "the git repository")
	issue := fs.Int("issue", 0, "the issue the pull request answers; only its ratification may amend an existing project")
	fs.Parse(args)
	head := "HEAD"
	if fs.NArg() == 1 {
		head = fs.Arg(0)
	}
	r, err := scope.Check(ctx, *dir, *base, head, *issue)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	if !r.OK() {
		fmt.Printf("❌ Out of scope:\n- %s\n", strings.Join(r.Problems, "\n- "))
		return 1
	}
	kind := "changes"
	if r.New {
		kind = "adds"
	}
	fmt.Printf("✅ In scope: it %s one project, %s, in %d files.\n", kind, r.Project, len(r.Files))
	return 0
}

func ratificationCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("ratification", flag.ExitOnError)
	repo := fs.String("repo", os.Getenv("GITHUB_REPOSITORY"), "the repository ratifying comments must be in, as owner/name")
	fs.Parse(args)
	if fs.NArg() == 0 || *repo == "" {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	gh := github.Client{Repo: *repo}
	code := 0
	for _, dir := range fs.Args() {
		// A plumbing plan's record is checked the same way (D-0105).
		if plumbing.LockIssue(filepath.ToSlash(dir)) != 0 {
			b, err := os.ReadFile(dir)
			var lock *plumbing.Lock
			if err == nil {
				lock, err = plumbing.ReadLock(b)
			}
			if err == nil {
				err = factory.VerifyPlan(ctx, gh, *repo, *lock)
			}
			if err != nil {
				fmt.Printf("❌ %s: %v\n", dir, err)
				code = 1
				continue
			}
			fmt.Printf("✅ %s: ratified by @%s on #%d (%s)\n", dir, lock.Ratified.By, lock.Ratified.Issue, lock.Ratified.Comment)
			continue
		}
		p, err := project.Load(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invariant: %s: %v\n", dir, err)
			code = 2
			continue
		}
		r := p.Lock.Ratified
		switch {
		case r == nil:
			fmt.Printf("✅ %s: ratified by hand, recorded in %s\n", dir, p.Lock.Decision)
		case factory.VerifyRatification(ctx, gh, *repo, p.Lock) == nil:
			fmt.Printf("✅ %s: ratified by @%s on #%d (%s)\n", dir, r.By, r.Issue, r.Comment)
		default:
			fmt.Printf("❌ %s: %v\n", dir, factory.VerifyRatification(ctx, gh, *repo, p.Lock))
			code = 1
		}
	}
	return code
}

// agentDoor serves the agent's door on a loopback address (D-0066), with a
// new token written where only this account can read it.
func agentDoor(addr, dir string, s *dashboard.Server) (*http.Server, error) {
	host, _, err := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); err != nil || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
		return nil, fmt.Errorf("the agent's door must be on a loopback address, not %q", addr)
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(b)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "agent.token")
	os.Remove(path)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return nil, err
	}
	log.Printf("invariant: the agent's door is on %s, and its token is in %s", addr, path)
	return &http.Server{Addr: addr, Handler: s.AgentHandler(token), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: time.Minute, MaxHeaderBytes: 16 << 10}, nil
}

func dashboardCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("dashboard", flag.ExitOnError)
	var repos []string
	fs.Func("repo", "a repository to show, as owner/name; repeat it to show more. The first is Invariant's own", func(v string) error {
		repos = append(repos, v)
		return nil
	})
	addr := fs.String("addr", "127.0.0.1:8484", "where to serve the page. Keep it on localhost, and share it through a tunnel")
	every := fs.Duration("every", 30*time.Second, "how often to read GitHub")
	base := fs.String("base", "main", "the branch the factory merges into")
	cache, _ := os.UserCacheDir()
	var works []string
	fs.Func("work", "a watcher's work directory, where it writes what it's doing; repeat it for each watcher, and the page follows the one that holds the lease, or else the one that's working (default "+filepath.Join(cache, "invariant", "watch")+")", func(v string) error {
		works = append(works, v)
		return nil
	})
	team := fs.String("access-team", "", "the Cloudflare Access team domain in front of /act, such as puglisij.cloudflareaccess.com. Without it, there's no /act")
	aud := fs.String("access-aud", "", "the AUD tag of the Access application that protects /act")
	agentAddr := fs.String("agent-addr", "", "serve the agent's door on this loopback address, such as 127.0.0.1:8485: a coding agent acting for @gitdek posts through the same narrow check as /act. The tunnel never publishes it. Empty turns it off")
	var emails []string
	fs.Func("access-email", "an email that may post commands from /act; repeat it for more", func(v string) error {
		emails = append(emails, v)
		return nil
	})
	fs.Parse(args)
	if len(repos) == 0 || fs.NArg() != 0 || (*team != "") != (*aud != "") || (*team != "" && len(emails) == 0) {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	if len(works) == 0 {
		works = []string{filepath.Join(cache, "invariant", "watch")}
	}
	logger := log.New(os.Stderr, "invariant: ", log.LstdFlags)
	s := &dashboard.Server{Branch: *base, Cache: filepath.Join(cache, "invariant", "dashboard"), Work: works[0], Works: works, Every: *every, Log: logger.Printf}
	if *team != "" {
		s.Access = &dashboard.Access{Team: *team, Audience: *aud, Emails: emails}
		logger.Printf("/act lets %s post commands, behind Cloudflare Access", strings.Join(emails, ", "))
	}
	if *agentAddr != "" {
		door, err := agentDoor(*agentAddr, filepath.Join(cache, "invariant", "dashboard"), s)
		if err != nil {
			logger.Print(err)
			return 1
		}
		go door.ListenAndServe()
		defer door.Close()
	}
	for _, r := range repos {
		s.Repos = append(s.Repos, &dashboard.Repo{Name: r, GitHub: github.Client{Repo: r}, Status: dashboard.StatusPath(works[0], r)})
	}
	// State graphs come from TLC, so they need Docker. Without it, the page
	// still shows everything else.
	if tc, err := toolchain.Ensure(ctx); err != nil {
		logger.Printf("no state graphs: %v", err)
	} else {
		s.Runner = &tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar}
	}
	s.Start(ctx)
	srv := &http.Server{Addr: *addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: time.Minute, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 16 << 10}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shut)
	}()
	logger.Printf("serving the dashboard for %s at http://%s", strings.Join(repos, " and "), *addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Print(err)
		return 2
	}
	return 0
}

// initCmd sets up another repository for the factory (D-0054): it writes the
// gate workflow, pinned to one commit of Invariant, and prints the steps only
// a person can take, filled in for the repository.
func initCmd(args []string) int {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	repo := fs.String("repo", "", "the repository, as owner/name, for the steps this prints (default: the one its origin remote names on GitHub)")
	ref := fs.String("invariant", buildCommit(), "the full commit of Invariant the repository's gate builds")
	force := fs.Bool("force", false, "replace a different gate workflow")
	fs.Parse(args)
	dir := "."
	switch fs.NArg() {
	case 0:
	case 1:
		dir = fs.Arg(0)
	default:
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	if *ref == "" {
		fmt.Fprintln(os.Stderr, "invariant: this binary doesn't know which commit of Invariant it was built from, so there's no commit to pin the gate to, and it wrote nothing. "+
			"go run records none, and neither does a build from a checkout with changes. "+
			"Build invariant from a clean checkout of a commit that's on GitHub, or pass -invariant with a full commit hash.")
		return 2
	}
	path, err := setup.Write(dir, *ref, *force)
	if err != nil {
		if path != "" {
			err = fmt.Errorf("%s: %w", path, err)
		}
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	if *repo == "" {
		*repo = originRepo(dir)
	}
	if *repo == "" {
		*repo = "OWNER/NAME"
	}
	language := ""
	for _, f := range projectFiles {
		if _, err := os.Stat(filepath.Join(dir, f.name)); err == nil {
			language = " -language " + f.language
			break
		}
	}
	// The commands aren't indented, so the heredoc still ends when it's
	// pasted.
	fmt.Printf(`Wrote %s. CI will build Invariant at %s and run its gate on every pull request.

Next:

1. Commit the workflow and push it. The factory can't change CI, so a person does. The projects directory needn't exist: the factory makes it with the first project.

2. Require invariant/gate on main, and allow merge commits. GitHub then refuses any merge the gate didn't pass, yours included. app_id 15368 is GitHub Actions, so only the workflow's own check counts. The factory merges with a merge commit:

gh api -X PUT repos/%[3]s/branches/main/protection --input - <<'EOF'
{"required_status_checks":{"strict":false,"checks":[{"context":"invariant/gate","app_id":15368}]},"enforce_admins":true,"required_pull_request_reviews":{"required_approving_review_count":0},"restrictions":null}
EOF
gh api -X PATCH repos/%[3]s -F allow_merge_commit=true

3. Run the factory. It acts as you, through gh:

invariant watch -repo %[3]s -projects invariant%[4]s

4. Optionally, have it act as its own bot, through a GitHub App: set one up as https://github.com/gitdek/invariant/blob/main/docs/factory-app.md says, add %[3]s to its installation, and add -app-id with the App's ID to the command above.
`, setup.WorkflowPath, (*ref)[:12], *repo, language)
	return 0
}

// projectFiles tell a repository's language by the file at its root. The
// first one there decides.
var projectFiles = []struct{ name, language string }{
	{"go.mod", "go"}, {"package.json", "typescript"}, {"pyproject.toml", "python"},
}

// githubRemote is a GitHub repository's remote, over https or ssh.
var githubRemote = regexp.MustCompile(`^(?:https://github\.com/|git@github\.com:|ssh://git@github\.com/)([^/]+/[^/]+?)(?:\.git)?/?$`)

// originRepo is the repository on GitHub that dir's origin remote names, as
// owner/name, or "" when it names none there.
func originRepo(dir string) string {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	m := githubRemote.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return ""
	}
	return m[1]
}

// buildCommit is the commit this binary was built from, when go build
// recorded one from a clean checkout.
func buildCommit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var rev string
	for _, s := range info.Settings {
		switch {
		case s.Key == "vcs.revision":
			rev = s.Value
		case s.Key == "vcs.modified" && s.Value == "true":
			return ""
		}
	}
	return rev
}

// ledgerCmd prints what each of the factory's issues took (D-0048, 5.4):
// its factory time, the time it waited on people, their comments, the
// agents' estimated spend and the gate runs synthesis used. It reads them
// from the factory's own posts, so older issues show what was recorded.
func ledgerCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("ledger", flag.ExitOnError)
	repo := fs.String("repo", "", "the repository, as owner/name")
	asJSON := fs.Bool("json", false, "print JSON instead of a table")
	fs.Parse(args)
	if *repo == "" || fs.NArg() != 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	gh := github.Client{Repo: *repo}
	all, err := gh.Issues(ctx, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	var issues []github.Issue
	for _, is := range all {
		if factory.Takes(is) {
			issues = append(issues, is)
		}
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	type row struct {
		Issue  int             `json:"issue"`
		Title  string          `json:"title"`
		State  string          `json:"state"`
		Merged bool            `json:"merged"`
		Took   factory.Numbers `json:"numbers"`
	}
	var rows []row
	for _, is := range issues {
		comments, err := gh.Comments(ctx, is.Number)
		if err != nil {
			fmt.Fprintln(os.Stderr, "invariant:", err)
			return 2
		}
		t := factory.ThreadFrom(is, comments, "")
		post, ok := t.State()
		if !ok {
			continue
		}
		end := time.Now()
		if is.State == "closed" {
			end, _ = time.Parse(time.RFC3339, post.Comment.CreatedAt)
		}
		n := factory.NumbersOf(t, end)
		if post.Marker.Numbers != nil {
			n = *post.Marker.Numbers
		}
		rows = append(rows, row{Issue: is.Number, Title: is.Title, State: post.Marker.Kind, Merged: post.Marker.Kind == factory.KindMerged, Took: n})
	}
	if *asJSON {
		b, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	fmt.Printf("%-6s %-10s %9s %9s %8s %8s %6s  %s\n", "ISSUE", "STATE", "FACTORY", "PEOPLE", "COMMENTS", "SPEND", "GATE", "TITLE")
	for _, r := range rows {
		spend := "-"
		if r.Took.SpendUSD > 0 {
			spend = fmt.Sprintf("$%.2f", r.Took.SpendUSD)
		}
		fmt.Printf("#%-5d %-10s %9s %9s %8d %8s %6d  %s\n", r.Issue, r.State, clock(r.Took.FactorySeconds), clock(r.Took.PeopleSeconds), r.Took.PeopleComments, spend, r.Took.GateRuns, r.Title)
	}
	return 0
}

// clock is a duration as hours and minutes, or minutes and seconds.
func clock(secs int) string {
	if secs >= 3600 {
		return fmt.Sprintf("%dh%02dm", secs/3600, secs%3600/60)
	}
	return fmt.Sprintf("%dm%02ds", secs/60, secs%60)
}

// efforts are the efforts Claude Code takes, from the least to the most.
var efforts = []string{"low", "medium", "high", "xhigh", "max"}

// validEffort says whether e is an effort Claude Code takes.
func validEffort(e string) bool { return slices.Contains(efforts, e) }

// agents are the coding agents Invariant can run, by the names -agent
// takes. Codex comes in #153's later issues.
var agents = []string{"claude-code"}

// validAgent says whether a is a coding agent Invariant can run.
func validAgent(a string) bool { return slices.Contains(agents, a) }

// fallbackEffort is the effort a build runs its agent at once more when the
// loop guard stops its first run, at effort (D-0125): fallback, when it's
// below effort, and otherwise none.
func fallbackEffort(effort, fallback string) string {
	if fallback == "" || slices.Index(efforts, fallback) >= slices.Index(efforts, effort) {
		return ""
	}
	return fallback
}
