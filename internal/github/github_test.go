package github

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

// A request GitHub refuses the App is ErrNoPermission, which waiting won't
// fix. A rate limit is also a 403, but it isn't, since waiting does fix it
// (copythis-ad#38).
func TestNoPermissionIsItsOwnError(t *testing.T) {
	for _, c := range []struct {
		stderr string
		want   error
	}{
		{"gh: Resource not accessible by integration (HTTP 403)", ErrNoPermission},
		{"gh: Not Found (HTTP 404)", ErrNotFound},
		{"gh: API rate limit exceeded for installation ID 1. (HTTP 403)", nil},
	} {
		gh := filepath.Join(t.TempDir(), "gh")
		if err := os.WriteFile(gh, []byte("#!/bin/sh\necho '"+c.stderr+"' >&2\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := Client{Repo: "o/r", GH: gh}.Job(context.Background(), 1)
		switch {
		case err == nil:
			t.Errorf("%s: no error", c.stderr)
		case c.want != nil && !errors.Is(err, c.want):
			t.Errorf("%s: %v; want %v", c.stderr, err, c.want)
		case c.want == nil && (errors.Is(err, ErrNoPermission) || errors.Is(err, ErrNotFound)):
			t.Errorf("%s: %v; a rate limit is neither", c.stderr, err)
		}
	}
}

// fakeGH is a gh that runs script, with sh, on the arguments it's given.
func fakeGH(t *testing.T, script string) string {
	t.Helper()
	gh := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return gh
}

// Workflows reads each workflow file under .github/workflows at a ref,
// through the contents API, and nothing else there. A ref with no such
// directory has none, which isn't an error.
func TestWorkflowsReadsEachWorkflowFileAtARef(t *testing.T) {
	gh := fakeGH(t, `case "$*" in
*"repos/acme/widgets/contents/.github/workflows?ref=main")
  echo '[{"type": "file", "path": ".github/workflows/gate.yml"}, {"type": "file", "path": ".github/workflows/checks.yaml"}, {"type": "file", "path": ".github/workflows/README.md"}, {"type": "dir", "path": ".github/workflows/old"}]' ;;
*"repos/acme/widgets/contents/.github/workflows/gate.yml?ref=main") printf 'jobs:\n  gate:\n    name: invariant/gate\n' ;;
*"repos/acme/widgets/contents/.github/workflows/checks.yaml?ref=main") printf 'jobs:\n  test:\n    name: test\n' ;;
*) echo 'gh: Not Found (HTTP 404)' >&2; exit 1 ;;
esac
`)
	c := Client{Repo: "acme/widgets", GH: gh}
	got, err := c.Workflows(context.Background(), "main")
	want := map[string]string{
		".github/workflows/gate.yml":    "jobs:\n  gate:\n    name: invariant/gate\n",
		".github/workflows/checks.yaml": "jobs:\n  test:\n    name: test\n",
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("the workflows on main = %q, %v; want %q", got, err, want)
	}
	if got, err := c.Workflows(context.Background(), "wip"); err != nil || len(got) > 0 {
		t.Errorf("wip has no workflows directory, but its workflows = %q, %v", got, err)
	}
}

// MergeCommits asks GraphQL, which tells anyone who can read the repository
// whether it allows merge commits. A repository GitHub doesn't find is an
// error, not one that refuses them.
func TestMergeCommitsAsksGraphQL(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		gh := fakeGH(t, `case "$*" in
"api graphql "*mergeCommitAllowed*" owner=acme "*" name=widgets") echo '{"data": {"repository": {"mergeCommitAllowed": `+strconv.FormatBool(allowed)+`}}}' ;;
*) echo '{"data": {"repository": null}}' ;;
esac
`)
		widgets, gadgets := Client{Repo: "acme/widgets", GH: gh}, Client{Repo: "acme/gadgets", GH: gh}
		if got, err := widgets.MergeCommits(context.Background()); err != nil || got != allowed {
			t.Errorf("MergeCommits = %v, %v; want %v", got, err, allowed)
		}
		if _, err := gadgets.MergeCommits(context.Background()); err == nil {
			t.Error("GitHub didn't find acme/gadgets, but MergeCommits answered for it without an error")
		}
	}
}
