package github

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// closeGH is a fake gh for pull request #7 of acme/widgets, which starts as
// state says. It writes down each call, and answers a close as close says:
// "ok" closes it, "merged" is GitHub refusing because someone merged it
// just then, and "refused" is GitHub refusing for no such reason.
func closeGH(t *testing.T, state, close string) (gh, calls string) {
	t.Helper()
	dir := t.TempDir()
	calls, now := filepath.Join(dir, "calls"), filepath.Join(dir, "now")
	if err := os.WriteFile(now, []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	answer := `echo '{"number": 7, "state": "closed", "merged": false}'; echo closed > '` + now + `'`
	switch close {
	case "merged":
		answer = `echo merged > '` + now + `'; echo 'gh: Pull Request is not mergeable (HTTP 422)' >&2; exit 1`
	case "refused":
		answer = `echo 'gh: Resource not accessible by integration (HTTP 403)' >&2; exit 1`
	}
	gh = fakeGH(t, `input=
case "$*" in *--input*) input=$(cat) ;; esac
printf '%s %s\n' "$*" "$input" >> '`+calls+`'
case "$*" in
*"-X PATCH"*"repos/acme/widgets/pulls/7"*) `+answer+` ;;
*"-X GET"*"repos/acme/widgets/pulls/7"*)
  case $(cat '`+now+`') in
  open) echo '{"number": 7, "state": "open", "merged": false, "draft": true}' ;;
  closed) echo '{"number": 7, "state": "closed", "merged": false}' ;;
  merged) echo '{"number": 7, "state": "closed", "merged": true}' ;;
  esac ;;
*) echo 'gh: Not Found (HTTP 404)' >&2; exit 1 ;;
esac
`)
	return gh, calls
}

func closeCalls(t *testing.T, calls string) []string {
	t.Helper()
	b, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	var patches []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.Contains(line, "-X PATCH") {
			patches = append(patches, line)
		}
	}
	return patches
}

// A retry that replaces a failed draft closes it without merging it (#271).
// The close sets the pull request's state to closed through the REST API,
// once. Closing it again, or closing one someone already closed, changes
// nothing, and one someone merged, before or just as the factory closes it,
// comes back merged instead of as an error, so the factory never replaces a
// merged draft. Any other refusal is an error that says what GitHub said.
func TestClosePullRequestClosesADraftOnceAndSaysWhenItMerged(t *testing.T) {
	ctx := context.Background()

	gh, calls := closeGH(t, "open", "ok")
	pr, err := Client{Repo: "acme/widgets", GH: gh}.ClosePullRequest(ctx, 7)
	if err != nil || pr.State != "closed" || pr.Merged {
		t.Fatalf("closing open #7 = %+v, %v; want it closed and not merged", pr, err)
	}
	patches := closeCalls(t, calls)
	if len(patches) != 1 || !strings.Contains(patches[0], `{"state":"closed"}`) {
		t.Fatalf("closing #7 sent %q; want one PATCH setting its state to closed", patches)
	}
	if pr, err = (Client{Repo: "acme/widgets", GH: gh}).ClosePullRequest(ctx, 7); err != nil || pr.State != "closed" {
		t.Fatalf("closing #7 again = %+v, %v; want it closed, without an error", pr, err)
	}
	if patches := closeCalls(t, calls); len(patches) != 1 {
		t.Errorf("closing #7 again sent another PATCH: %q", patches)
	}

	for _, state := range []string{"closed", "merged"} {
		gh, calls := closeGH(t, state, "ok")
		pr, err := Client{Repo: "acme/widgets", GH: gh}.ClosePullRequest(ctx, 7)
		if err != nil || pr.State != "closed" || pr.Merged != (state == "merged") || len(closeCalls(t, calls)) != 0 {
			t.Errorf("closing #7 when it's %s = %+v, %v, with PATCHes %q; want it as it is, with no PATCH", state, pr, err, closeCalls(t, calls))
		}
	}

	gh, _ = closeGH(t, "open", "merged")
	if pr, err := (Client{Repo: "acme/widgets", GH: gh}).ClosePullRequest(ctx, 7); err != nil || !pr.Merged {
		t.Errorf("closing #7 as someone merged it = %+v, %v; want it merged, without an error", pr, err)
	}

	gh, _ = closeGH(t, "open", "refused")
	if _, err := (Client{Repo: "acme/widgets", GH: gh}).ClosePullRequest(ctx, 7); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("GitHub refused to close #7, but ClosePullRequest(7) = %v; want an error that says what GitHub said", err)
	}
}

// A replacement pull request is found by the branch its attempt builds on,
// whatever became of it: open, closed or merged, so a crash after GitHub
// opened it never opens a second (#271).
func TestPullRequestFromFindsTheNewestPullRequestInAnyState(t *testing.T) {
	ctx := context.Background()
	calls := filepath.Join(t.TempDir(), "calls")
	gh := fakeGH(t, `printf '%s\n' "$*" >> '`+calls+`'
case "$*" in
*"head=acme%3Ainvariant%2Fissue-7-retry-9"*) echo '[{"number": 8, "state": "closed", "merged": true}, {"number": 12, "state": "closed"}]' ;;
*"head=acme%3Ainvariant%2Fissue-7-none"*) echo '[]' ;;
*) echo 'gh: Not Found (HTTP 404)' >&2; exit 1 ;;
esac
`)
	c := Client{Repo: "acme/widgets", GH: gh}
	pr, ok, err := c.PullRequestFrom(ctx, "invariant/issue-7-retry-9")
	if err != nil || !ok || pr.Number != 12 || pr.State != "closed" {
		t.Fatalf("PullRequestFrom = %+v, %v, %v; want #12, the newest, closed", pr, ok, err)
	}
	b, _ := os.ReadFile(calls)
	if !strings.Contains(string(b), "state=all") {
		t.Errorf("PullRequestFrom must ask for pull requests in every state: %s", b)
	}
	if _, ok, err := c.PullRequestFrom(ctx, "invariant/issue-7-none"); ok || err != nil {
		t.Errorf("PullRequestFrom a branch with none = %v, %v; want none, without an error", ok, err)
	}
}
