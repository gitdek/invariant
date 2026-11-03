package github

import (
	"context"
	"testing"
)

// A branch that's already gone is deleted, whichever way GitHub says so:
// GitHub may have deleted it on merge, or a step deleted it before the
// factory's last post was interrupted (#434's review). Any other refusal
// stays an error.
func TestDeleteBranchTakesAGoneBranchAsDeleted434(t *testing.T) {
	for _, tc := range []struct {
		stderr string
		gone   bool
	}{
		{"gh: Not Found (HTTP 404)", true},
		{"gh: Reference does not exist (HTTP 422)", true},
		{"gh: Validation Failed (HTTP 422)", false},
		{"gh: Resource not accessible by integration (HTTP 403)", false},
	} {
		gh := fakeGH(t, "printf '%s\\n' '"+tc.stderr+"' >&2\nexit 1\n")
		err := (Client{Repo: "o/r", GH: gh}).DeleteBranch(context.Background(), "invariant/issue-1-work")
		if tc.gone && err != nil {
			t.Errorf("%s: a branch that's gone wasn't taken as deleted: %v", tc.stderr, err)
		}
		if !tc.gone && err == nil {
			t.Errorf("%s: GitHub's refusal was taken as a deletion", tc.stderr)
		}
	}
}

// A list of pull requests says when each merged, not whether it did, so
// MergedPullRequests reads that, and leaves out one closed without merging.
func TestMergedPullRequestsReadsWhenEachMerged434(t *testing.T) {
	gh := fakeGH(t, `printf '%s\n' '[{"number":3,"state":"closed","merged_at":"2026-11-03T09:00:00Z","head":{"ref":"invariant/issue-7-work"}},{"number":2,"state":"closed","head":{"ref":"invariant/issue-6-work"}}]'`+"\n")
	prs, err := (Client{Repo: "o/r", GH: gh}).MergedPullRequests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Number != 3 || !prs[0].Merged || prs[0].Head.Ref != "invariant/issue-7-work" {
		t.Fatalf("got %+v; want only #3, merged", prs)
	}
}
