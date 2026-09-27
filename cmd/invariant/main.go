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
//	invariant mcp ...                    serve the gate or the check to an agent (synthesize and formalize start it)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/dashboard"
	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/mcp"
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
  invariant watch -repo OWNER/NAME [-once] [-app-id ID] [-language L]
                                               turn the repository's issues into merged pull requests
  invariant scope [-base REF] [HEAD]           check that a factory pull request stays in bounds
  invariant ratification -repo OWNER/NAME PROJECT...
                                               check factory projects' ratifications on GitHub
  invariant pin PROJECT                        record the statements' current text as ratified
  invariant trace FILE                         replay a counterexample trace
  invariant dashboard -repo OWNER/NAME [-repo OWNER/NAME]... [-addr HOST:PORT]
                                               serve a live view of the factory and its evidence

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
	return 0
}

func synthesizeCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("synthesize", flag.ExitOnError)
	out := fs.String("out", "out/synthesis", "where the result, its receipt and the logs go")
	model := fs.String("model", "opus", "the model the agent uses")
	budget := fs.Float64("budget", 5, "cap on the agent's estimated cost for the run, in USD (claude --max-budget-usd)")
	turns := fs.Int("max-turns", 80, "cap on the agent's turns")
	runs := fs.Int("gate-runs", 4, "the most gate runs the agent gets: one attempt and three repairs")
	timeout := fs.Duration("timeout", 40*time.Minute, "wall-clock cap on the agent's run")
	claude := fs.String("claude", "claude", "the Claude Code CLI")
	draft := fs.Bool("draft", false, "start from the module's drafted model instead of a skeleton of the pinned definitions")
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
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	r, err := synth.Synthesize(ctx, synth.Options{
		Project: fs.Arg(0), Out: *out, Binary: self, GateRuns: *runs, Timeout: *timeout, Toolchain: tc, KeepModel: *draft,
		Backend: synth.ClaudeCode{Binary: *claude, Model: *model, BudgetUSD: *budget, MaxTurns: *turns},
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
	fs.Parse(args)
	if fs.NArg() != 1 || (*ratified == "") == !*formal {
		fmt.Fprintln(os.Stderr, "usage: invariant mcp (-ratified PROJECT | -formalize) [-max-runs N] [-log FILE] WORKSPACE")
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
	model := fs.String("model", "opus", "the model the agent uses")
	budget := fs.Float64("budget", 3, "cap on the agent's estimated cost for the run, in USD (claude --max-budget-usd)")
	turns := fs.Int("max-turns", 60, "cap on the agent's turns")
	checks := fs.Int("checks", 4, "the most model checks the agent gets")
	timeout := fs.Duration("timeout", 25*time.Minute, "wall-clock cap on the agent's run")
	claude := fs.String("claude", "claude", "the Claude Code CLI")
	fs.Parse(args)
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
	f := formalize.Formalizer{Backend: synth.ClaudeCode{Binary: *claude, Model: *model, BudgetUSD: *budget, MaxTurns: *turns},
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
	model := fs.String("model", "opus", "the model the agents use")
	fbudget := fs.Float64("formalize-budget", 3, "cap on a formalization's estimated cost, in USD")
	budget := fs.Float64("budget", 5, "cap on a synthesis's estimated cost, in USD")
	turns := fs.Int("max-turns", 80, "cap on an agent's turns")
	runs := fs.Int("gate-runs", 4, "the most gate runs a synthesis gets")
	timeout := fs.Duration("timeout", 40*time.Minute, "wall-clock cap on an agent's run")
	claude := fs.String("claude", "claude", "the Claude Code CLI")
	appID := fs.Int64("app-id", 0, "the factory's GitHub App; without one, the factory acts as whoever gh is logged in as")
	home, _ := os.UserHomeDir()
	appKey := fs.String("app-key", filepath.Join(home, ".config", "invariant", "factory.pem"), "the App's private key")
	fs.Parse(args)
	if *repo == "" || fs.NArg() != 0 || formalize.Languages[*language] == "" {
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
	f := &factory.Factory{
		Repository: *repo, GitHub: gh, Repo: clone, Base: *base, Projects: *projects, Check: "invariant/gate", Language: *language, Self: bot,
		Work: filepath.Join(dir, "issues"), Log: logger.Printf,
		Formalizer: formalize.Formalizer{Backend: synth.ClaudeCode{Binary: *claude, Model: *model, BudgetUSD: *fbudget, MaxTurns: *turns},
			Binary: self, CheckRuns: 4, Timeout: *timeout, Toolchain: tc},
		Builder: factory.Synthesis{Options: synth.Options{Backend: synth.ClaudeCode{Binary: *claude, Model: *model, BudgetUSD: *budget, MaxTurns: *turns},
			Binary: self, GateRuns: *runs, Timeout: *timeout, Toolchain: tc}},
	}
	if err := f.Prepare(ctx); err != nil {
		return fail(err)
	}
	// What the factory is doing goes in a status file for the dashboard
	// (D-0049). It holds no secrets: the process, the repository, and the
	// issue and step in hand.
	statusPath := dashboard.StatusPath(*work, *repo)
	status := dashboard.Status{PID: os.Getpid(), Repo: *repo, Started: time.Now().UTC(), Every: every.Seconds()}
	f.Activity = func(issue int, doing string) {
		now := time.Now().UTC()
		status.Heartbeat = now
		if issue != status.Issue || doing != status.Doing {
			status.Issue, status.Doing, status.Since = issue, doing, &now
		}
		if err := dashboard.WriteStatus(statusPath, status); err != nil {
			logger.Printf("status: %v", err)
		}
	}
	f.Activity(0, "")
	defer os.Remove(statusPath)
	logger.Printf("watching %s as @%s; commits by %s <%s>", *repo, actor, clone.Name, clone.Email)
	if *once {
		if err := f.Poll(ctx); err != nil {
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
	work := fs.String("work", filepath.Join(cache, "invariant", "watch"), "the watchers' work directory, where they write what they're doing")
	fs.Parse(args)
	if len(repos) == 0 || fs.NArg() != 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	logger := log.New(os.Stderr, "invariant: ", log.LstdFlags)
	s := &dashboard.Server{Branch: *base, Cache: filepath.Join(cache, "invariant", "dashboard"), Every: *every, Log: logger.Printf}
	for _, r := range repos {
		s.Repos = append(s.Repos, &dashboard.Repo{Name: r, GitHub: github.Client{Repo: r}, Status: dashboard.StatusPath(*work, r)})
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
// a person can take.
func initCmd(args []string) int {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	repo := fs.String("repo", "OWNER/NAME", "the repository, for the steps this prints")
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
	path, err := setup.Write(dir, *ref, *force)
	if err != nil {
		if path != "" {
			err = fmt.Errorf("%s: %w", path, err)
		}
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	key := filepath.Join("~/.config/invariant", strings.ReplaceAll(*repo, "/", "-")+"-deploy")
	fmt.Printf(`Wrote %s. CI will build Invariant at %s and run its gate on every pull request.

Next:
1. Commit the workflow. The factory's App can't change CI, so a person does.
2. Let CI read Invariant with a read-only deploy key:
     ssh-keygen -t ed25519 -N "" -C "%s CI reads invariant" -f %s
     gh repo deploy-key add %s.pub -R gitdek/invariant -t "%s CI (read-only)"
     gh secret set INVARIANT_DEPLOY_KEY -R %s < %s
     rm %s
3. Add %s to the factory's App installation, under Repository access.
4. Run the factory there:
     invariant watch -repo %s -app-id APP_ID -projects invariant -language typescript
`, setup.WorkflowPath, (*ref)[:12], *repo, key, key, *repo, *repo, key, key, *repo, *repo)
	return 0
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
