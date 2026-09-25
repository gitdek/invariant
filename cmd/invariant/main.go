// Command invariant is the Invariant CLI.
//
//	invariant verify [-out DIR] DIR   run every gate check on a project
//	invariant pin DIR                 record the current statement text as ratified
//	invariant trace FILE              replay a counterexample trace
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

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

const usage = `Invariant proves code against statements people ratified.

Usage:
  invariant verify [-out DIR] PROJECT   run every gate check and print the receipt
  invariant pin PROJECT                 record the statements' current text as ratified
  invariant trace FILE                  replay a counterexample trace

Not built yet: init, synthesize (see decisions/D-0013-slice-plan.md).
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
	case "init", "synthesize":
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
