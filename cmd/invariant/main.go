// Command invariant is the Invariant CLI.
//
//	invariant verify [-out DIR] DIR      run every gate check on a project
//	invariant synthesize [-out DIR] DIR  have a coding agent write the model and code, then gate them
//	invariant pin DIR                    record the current statement text as ratified
//	invariant trace FILE                 replay a counterexample trace
//	invariant mcp ...                    serve the gate as an MCP tool (synthesize starts it)
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/mcp"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

const usage = `Invariant proves code against statements people ratified.

Usage:
  invariant verify [-out DIR] PROJECT       run every gate check and print the receipt
  invariant synthesize [-out DIR] PROJECT   have a coding agent write the model and code, then gate them
  invariant pin PROJECT                     record the statements' current text as ratified
  invariant trace FILE                      replay a counterexample trace

Not built yet: init (see decisions/D-0013-slice-plan.md).
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
	case "mcp":
		code = mcpCmd(ctx, args)
	case "init":
		fmt.Fprintf(os.Stderr, "invariant %s isn't built yet (see decisions/D-0013-slice-plan.md)\n", cmd)
		code = 2
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
		Project: fs.Arg(0), Out: *out, Binary: self, GateRuns: *runs, Timeout: *timeout, Toolchain: tc,
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
	maxRuns := fs.Int("max-runs", 4, "the most gate runs to allow")
	logPath := fs.String("log", "", "append a line per gate run to this file")
	fs.Parse(args)
	if fs.NArg() != 1 || *ratified == "" {
		fmt.Fprintln(os.Stderr, "usage: invariant mcp -ratified PROJECT [-max-runs N] [-log FILE] WORKSPACE")
		return 2
	}
	ws := fs.Arg(0)
	p, err := project.Load(*ratified)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	tc, err := toolchain.Ensure(ctx)
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
			if err := synth.Assemble(p, ws, dir); err != nil {
				return "The gate couldn't assemble your project: " + err.Error(), true
			}
			r, err := verify.Run(ctx, dir, "", tc)
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
