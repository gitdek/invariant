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
// asks for and answers that GitHub has no such repository, then refuses to
// create one. No test here reaches gh, git or GitHub.
type noGitHub struct{ calls []string }

func (n *noGitHub) Run(_ context.Context, c found.Command) ([]byte, error) {
	n.calls = append(n.calls, c.String())
	if len(c.Args) > 1 && c.Args[1] == "view" {
		return nil, errors.New("GraphQL: Could not resolve to a Repository with the name 'acme/widgets'. (repository)")
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
		want := []string{"gh repo view acme/widgets --json isEmpty,mergeCommitAllowed,visibility", "gh repo create acme/widgets " + c.visibility}
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
