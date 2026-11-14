package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/gitdek/invariant/internal/found"
)

// newCmd founds a new project (#919): its repository on GitHub, its
// checkout, a starter, its checks and its gate, and prints the steps only a
// person can take. It's the one place that hands a founding the runner that
// really runs gh and git.
func newCmd(ctx context.Context, args []string) int {
	return foundNew(ctx, args, found.Exec{}, os.Stdout, os.Stderr)
}

// foundNew reads new's arguments and founds the project through runner.
func foundNew(ctx context.Context, args []string, runner found.Runner, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "the checkout's directory (default: NAME beside the current directory's checkout)")
	public := fs.Bool("public", false, "create the repository public; it's private otherwise")
	language := fs.String("language", "go", "the starter's language; only go has one")
	spec := fs.String("spec", "", "a file to copy to docs/PRD.md, the project's spec")
	ref := fs.String("invariant", buildCommit(), "the full commit of Invariant the repository's gate builds")
	// The repository comes first, as the usage gives it, and the flag
	// package stops at the first argument that isn't a flag, so the flags
	// after it are read in a second pass.
	repo := ""
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		if repo != "" {
			fmt.Fprint(stderr, usage)
			return 2
		}
		repo, rest = fs.Arg(0), fs.Args()[1:]
	}
	if repo == "" {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if *ref == "" {
		fmt.Fprintln(stderr, noCommitToPin)
		return 2
	}
	err := found.Project(ctx, found.Options{
		Repo: repo, Dir: *dir, Public: *public, Language: *language, Spec: *spec, Invariant: *ref,
		Runner: runner, Out: stdout,
	})
	if err != nil {
		fmt.Fprintln(stderr, "invariant:", err)
		return 2
	}
	return 0
}
