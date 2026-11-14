package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/found"
	"github.com/gitdek/invariant/internal/setup"
)

// noGitHub is a runner that runs nothing: it notes each command a founding
// asks for and answers that the owner is a person with no such repository,
// then refuses to create one. A checkout it's told of is a fresh clone of
// the repository, with no commit and no git identity. No test here reaches
// gh, git or GitHub.
type noGitHub struct {
	calls    []string
	checkout string // a directory that is the repository's checkout
}

func (n *noGitHub) Run(_ context.Context, c found.Command) ([]byte, error) {
	n.calls = append(n.calls, c.String())
	switch args := strings.Join(c.Args, " "); {
	case c.Name == "gh" && args == "api users/acme --jq .type":
		return []byte("User\n"), nil
	case c.Name == "gh" && c.Args[1] == "view":
		return nil, errors.New("GraphQL: Could not resolve to a Repository with the name 'acme/widgets'. (repository)")
	case c.Name == "git" && c.Dir == n.checkout && (args == "remote get-url --all origin" || args == "remote get-url --push --all origin"):
		return []byte("https://github.com/acme/widgets.git\n"), nil
	case c.Name == "git" && c.Dir == n.checkout && args == "for-each-ref --count=1":
		return nil, nil
	}
	return nil, errors.New("this test creates nothing")
}

// new reads its flags after the repository, as its usage gives them, and
// before it too. The repository is private unless -public says otherwise,
// and -dir is where the checkout goes.
func TestNewReadsItsFlagsAfterTheRepository919(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "elsewhere")
	spec := filepath.Join(t.TempDir(), "spec.md")
	if err := os.WriteFile(spec, []byte("# The spec\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args       []string
		visibility string
	}{
		{[]string{"acme/widgets", "-dir", dir, "-public", "-language", "go", "-spec", spec, "-invariant", initPin}, "--public"},
		{[]string{"-dir", dir, "-public", "-invariant", initPin, "acme/widgets"}, "--public"},
		{[]string{"-invariant", initPin, "acme/widgets", "-dir", dir}, "--private"},
	} {
		var stdout, stderr bytes.Buffer
		runner := &noGitHub{}
		code := foundNew(context.Background(), c.args, runner, &stdout, &stderr)
		want := []string{"gh api users/acme --jq .type", "gh repo view acme/widgets --json isEmpty,mergeCommitAllowed,visibility", "gh repo create acme/widgets " + c.visibility}
		if !slices.Equal(runner.calls, want) {
			t.Errorf("new %s ran %q, want %q", strings.Join(c.args, " "), runner.calls, want)
		}
		if !strings.Contains(stdout.String(), "Repository: https://github.com/acme/widgets\nDirectory:  "+dir+"\n") {
			t.Errorf("new %s didn't name the repository and -dir's directory:\n%s", strings.Join(c.args, " "), stdout.String())
		}
		if code != 2 || !strings.Contains(stderr.String(), "invariant: gh repo create acme/widgets "+c.visibility+": this test creates nothing") {
			t.Errorf("new %s exited %d and said:\n%s", strings.Join(c.args, " "), code, stderr.String())
		}
		if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("new made %s though the repository couldn't be created: %v", dir, err)
		}
	}
}

// words splits a command as a shell would, for the quoting new prints: a
// word in single quotes is one word, and '\” inside it is a quote.
func words(t *testing.T, line string) []string {
	t.Helper()
	var out []string
	word, quoted, open := "", false, false
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\'' && quoted && strings.HasPrefix(line[i:], `'\''`):
			word, i = word+"'", i+3
		case c == '\'':
			quoted, open = !quoted, true
		case c == ' ' && !quoted:
			if word != "" || open {
				out = append(out, word)
			}
			word, open = "", false
		default:
			word += string(c)
		}
	}
	if quoted {
		t.Fatalf("an open quote in %s", line)
	}
	if word != "" || open {
		out = append(out, word)
	}
	return out
}

// Where new stops for something a person can put right, it gives the
// command to run again, with every flag that decides what it does. Pasted,
// that command is the same founding: it reads the same repository,
// directory, visibility, spec and pin, from wherever it's run.
func TestNewTakesTheCommandItGivesForRunningAgain919(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "the project's", "widgets")
	spec := filepath.Join(root, "the spec.md")
	if err := os.WriteFile(spec, []byte("# The spec\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A fresh clone of the repository, with no git identity, which stops
	// new before it creates anything.
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args []string) (string, []string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		runner := &noGitHub{checkout: dir}
		if code := foundNew(context.Background(), args, runner, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), dir+" has no git identity") {
			t.Fatalf("new %s exited %d and said:\n%s", strings.Join(args, " "), code, stderr.String())
		}
		for _, call := range runner.calls {
			if strings.Contains(call, "create") || strings.Contains(call, "clone") || strings.Contains(call, "push") && !strings.Contains(call, "get-url") {
				t.Errorf("new %s ran %q before it stopped", strings.Join(args, " "), call)
			}
		}
		return stderr.String(), runner.calls
	}
	said, calls := run([]string{"acme/widgets", "-public", "-dir", dir, "-spec", spec, "-invariant", initPin})
	lines := strings.Split(strings.TrimRight(said, "\n"), "\n")
	again := words(t, lines[len(lines)-1])
	want := []string{"invariant", "new", "acme/widgets", "-dir", dir, "-public", "-spec", spec, "-invariant", initPin}
	if !slices.Equal(again, want) {
		t.Fatalf("the command to run again reads as %q, want %q:\n%s", again, want, said)
	}
	// From another directory, it stops at the same place, having asked the
	// same things.
	t.Chdir(t.TempDir())
	saidAgain, callsAgain := run(again[2:])
	if saidAgain != said || !slices.Equal(callsAgain, calls) {
		t.Errorf("pasted, the command said:\n%s\nand ran %q; the first run said:\n%s\nand ran %q", saidAgain, callsAgain, said, calls)
	}
}

// new takes exactly one repository, and says how it's used otherwise. -h
// lists its flags.
func TestNewTakesOneRepository919(t *testing.T) {
	for _, args := range [][]string{nil, {"-invariant", initPin}, {"acme/widgets", "acme/gadgets", "-invariant", initPin}, {"acme/widgets", "-invariant", initPin, "acme/gadgets"}} {
		var stdout, stderr bytes.Buffer
		runner := &noGitHub{}
		if code := foundNew(context.Background(), args, runner, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "invariant new OWNER/NAME") {
			t.Errorf("new %s exited %d and said:\n%s", strings.Join(args, " "), code, stderr.String())
		}
		if len(runner.calls) != 0 || stdout.Len() != 0 {
			t.Errorf("new %s did something: %q, %q", strings.Join(args, " "), runner.calls, stdout.String())
		}
	}
	var stdout, stderr bytes.Buffer
	runner := &noGitHub{}
	if code := foundNew(context.Background(), []string{"-h"}, runner, &stdout, &stderr); code != 0 || len(runner.calls) != 0 {
		t.Errorf("new -h exited %d and ran %q", code, runner.calls)
	}
	for _, flag := range []string{"-dir", "-public", "-language", "-spec", "-invariant"} {
		if !strings.Contains(stderr.String(), flag) {
			t.Errorf("new -h doesn't list %s:\n%s", flag, stderr.String())
		}
	}
	if code := foundNew(context.Background(), []string{"acme/widgets", "-no-such-flag"}, runner, &stdout, &stderr); code != 2 || len(runner.calls) != 0 {
		t.Errorf("new with a flag it doesn't have exited %d and ran %q", code, runner.calls)
	}
}

// Built by go run, or in a checkout with changes, new knows no commit of
// Invariant to pin the gate to. It creates nothing, and says how to fix it,
// as init does.
func TestNewNamesTheFixWhenItDoesntKnowItsCommit919(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner := &noGitHub{}
	code := foundNew(context.Background(), []string{"acme/widgets", "-invariant", ""}, runner, &stdout, &stderr)
	if code != 2 || len(runner.calls) != 0 || stdout.Len() != 0 {
		t.Fatalf("new went ahead with no commit to pin the gate to: exit %d, %q, %q", code, runner.calls, stdout.String())
	}
	for _, want := range []string{"clean checkout", "-invariant"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("new's message lacks %q:\n%s", want, stderr.String())
		}
	}
}

// A language with no starter is refused in a sentence, before anything is
// said or run.
func TestNewRefusesALanguageWithoutAStarterInASentence919(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner := &noGitHub{}
	code := foundNew(context.Background(), []string{"acme/widgets", "-language", "rust", "-invariant", initPin}, runner, &stdout, &stderr)
	if code != 2 || len(runner.calls) != 0 || stdout.Len() != 0 {
		t.Fatalf("new -language rust exited %d, ran %q and said %q", code, runner.calls, stdout.String())
	}
	if want := "invariant: there's a starter for Go only, so invariant new can't start a rust project: -language takes go\n"; stderr.String() != want {
		t.Errorf("new -language rust said %q, want %q", stderr.String(), want)
	}
}

// The requirement new makes is the one init prints for a person to make:
// the same body, to the same place.
func TestInitPrintsTheRequirementNewMakes919(t *testing.T) {
	dir := repoWithOrigin(t, "https://github.com/acme/widgets.git", "go.mod", "module example.com/widgets\n\ngo 1.27.1\n")
	code, printed := initPrints(t, "-invariant", initPin, dir)
	if code != 0 {
		t.Fatalf("init exited %d:\n%s", code, printed)
	}
	want := "gh api -X PUT repos/acme/widgets/branches/main/protection --input - <<'EOF'\n" + setup.Protection + "\nEOF\ngh api -X PATCH repos/acme/widgets -F allow_merge_commit=true\n"
	if !strings.Contains(printed, want) {
		t.Errorf("init's steps lack the requirement as new makes it:\n%s\nin:\n%s", want, printed)
	}
	for _, field := range []string{`"context":"invariant/gate"`, `"app_id":15368`, `"enforce_admins":true`, `"required_approving_review_count":0`} {
		if !strings.Contains(setup.Protection, field) {
			t.Errorf("the requirement lacks %s: %s", field, setup.Protection)
		}
	}
}
