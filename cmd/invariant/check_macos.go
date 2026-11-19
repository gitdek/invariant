package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/setup"
)

// checkMacOSCmd is the gate's macOS job (#929): it runs the checks of the
// parts a repository's contract says build on macOS, on the macOS it runs
// on, since Linux skips the files only macOS builds.
func checkMacOSCmd(ctx context.Context, args []string) int {
	return runMacOSCheck(ctx, args, runtime.GOOS, os.Stdout)
}

// runMacOSCheck takes the host's operating system so a test can name it.
// The checks are the contract's at -base, never the candidate's, so a pull
// request can't change what it's checked by: a part's build, vet and test,
// then its other checks, each run as its plain argument array, without a
// shell, in the part's directory of a private copy of the candidate commit.
// It runs every check of every such part, and fails if any fails. Off
// macOS, it fails rather than skip: a skipped required check counts as
// passed.
func runMacOSCheck(ctx context.Context, args []string, host string, out io.Writer) (code int) {
	fs := flag.NewFlagSet("check-macos", flag.ContinueOnError)
	fs.SetOutput(out)
	dir := fs.String("C", ".", "the git repository")
	base := fs.String("base", "HEAD", "the commit whose contract names the checks: a pull request's base")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(out, "usage: invariant check-macos [-C DIR] [-base REF] [HEAD]")
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(out, "invariant:", err)
		return 1
	}
	if host != "darwin" {
		return fail(fmt.Errorf("check-macos checks the parts that build on macOS, so it runs on macOS only, and this is %s. The gate's %s job runs it on a hosted macOS runner", host, setup.MacOSCheck))
	}
	checkout, err := filepath.Abs(*dir)
	if err != nil {
		return fail(err)
	}
	headRef := "HEAD"
	if fs.NArg() == 1 {
		headRef = fs.Arg(0)
	}
	head, err := repositoryCommit(ctx, checkout, headRef)
	if err != nil {
		return fail(err)
	}
	snapshot, err := repository.Load(ctx, checkout, *base)
	if errors.Is(err, repository.ErrMissing) {
		fmt.Fprintf(out, "No repository contract is committed at %s, so no part builds on macOS, and nothing was checked here.\n", *base)
		return 0
	}
	if err != nil {
		return fail(err)
	}
	config := snapshot.Config()
	parts := config.MacOSParts()
	if len(parts) == 0 {
		fmt.Fprintf(out, "The contract committed at %s declares no part that builds on macOS, so nothing was checked here.\n", snapshot.Commit())
		return 0
	}
	work, err := os.MkdirTemp("", "invariant-macos-")
	if err != nil {
		return fail(err)
	}
	defer func() {
		if err := os.RemoveAll(work); err != nil {
			code = fail(fmt.Errorf("macOS checks cleanup: %w", err))
		}
	}()
	candidate := filepath.Join(work, "candidate")
	if err := stageRepositoryCommit(ctx, checkout, head, candidate); err != nil {
		return fail(err)
	}
	// The modeled projects are the gate's, and the Linux job leaves them
	// out of a part's checks too.
	if err := os.RemoveAll(filepath.Join(candidate, filepath.FromSlash(config.Projects()))); err != nil {
		return fail(fmt.Errorf("remove projects_directory from the macOS checks: %w", err))
	}
	env := macOSEnvironment(os.Environ())
	fmt.Fprintf(out, "macOS checks of %s, as the contract committed at %s declares them, on %s.\n", head, snapshot.Commit(), macOSHost(ctx, env))
	grouped := os.Getenv("GITHUB_ACTIONS") == "true"
	type outcome struct{ line, failed string }
	var outcomes []outcome
	for _, part := range parts {
		for _, step := range part.MacOS.Steps() {
			label := part.Name + "/" + step.Name
			if grouped {
				fmt.Fprintf(out, "::group::%s: %s\n", label, macOSArgv(step.Argv))
			} else {
				fmt.Fprintf(out, "%s: %s\n", label, macOSArgv(step.Argv))
			}
			cmd := exec.CommandContext(ctx, step.Argv[0], step.Argv[1:]...)
			cmd.Dir = filepath.Join(candidate, filepath.FromSlash(part.Directory))
			cmd.Env = env
			cmd.Stdout, cmd.Stderr = out, out
			started := time.Now()
			err := cmd.Run()
			took := time.Since(started).Round(time.Second / 10)
			if grouped {
				fmt.Fprintln(out, "::endgroup::")
			}
			o := outcome{line: fmt.Sprintf("%s: passed in %s (%s)", label, took, macOSArgv(step.Argv))}
			if err != nil {
				o = outcome{line: fmt.Sprintf("%s: failed in %s: %v (%s)", label, took, err, macOSArgv(step.Argv)), failed: label}
			}
			fmt.Fprintln(out, o.line)
			outcomes = append(outcomes, o)
		}
	}
	var failed []string
	fmt.Fprintln(out, "\nmacOS checks:")
	for _, o := range outcomes {
		fmt.Fprintln(out, o.line)
		if o.failed != "" {
			failed = append(failed, o.failed)
		}
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if len(failed) > 0 {
		fmt.Fprintf(out, "Failed on macOS: %s.\n", strings.Join(failed, ", "))
		return 1
	}
	names := make([]string, len(parts))
	for i, p := range parts {
		names[i] = p.Name
	}
	fmt.Fprintf(out, "Everything passed on macOS: every check the contract declares for the parts that build on macOS (%s).\n", strings.Join(names, ", "))
	return 0
}

// macOSEnvironment is the job's environment for a part's checks, without
// any variable whose name holds TOKEN, and with GOTOOLCHAIN=local: the
// checks run with the Go the job set up, as the Linux job's run with the
// Go its image holds, and never download another.
func macOSEnvironment(environ []string) []string {
	var env []string
	for _, variable := range environ {
		name, _, _ := strings.Cut(variable, "=")
		if strings.Contains(strings.ToUpper(name), "TOKEN") || name == "GOTOOLCHAIN" {
			continue
		}
		env = append(env, variable)
	}
	return append(env, "GOTOOLCHAIN=local")
}

// macOSHost says what the checks run on: the macOS and its SDK, the hosted
// runner's image, which GitHub updates under its label, and the Go.
func macOSHost(ctx context.Context, env []string) string {
	ask := func(name string, args ...string) string {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Env = env
		b, err := cmd.Output()
		if text := strings.TrimSpace(string(b)); err == nil && text != "" {
			return text
		}
		return "unknown"
	}
	image := "not a hosted runner"
	if name, version := os.Getenv("ImageOS"), os.Getenv("ImageVersion"); name != "" || version != "" {
		image = "runner image " + strings.TrimSpace(name+" "+version)
	}
	return fmt.Sprintf("macOS %s with SDK %s, %s, and %s", ask("sw_vers", "-productVersion"), ask("xcrun", "--show-sdk-version"), image, ask("go", "version"))
}

var plainWord = regexp.MustCompile(`^[A-Za-z0-9_./:=+,@%-]+$`)

// macOSArgv shows an argument array as a person would type it.
func macOSArgv(argv []string) string {
	words := make([]string, len(argv))
	for i, arg := range argv {
		words[i] = arg
		if !plainWord.MatchString(arg) {
			words[i] = fmt.Sprintf("%q", arg)
		}
	}
	return strings.Join(words, " ")
}
