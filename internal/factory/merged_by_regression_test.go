package factory

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/github"
)

// stopAtMerge is the GitHub of a watcher that stops just as it merges: the
// merge is recorded, and it never reaches GitHub.
type stopAtMerge struct{ *fakeGitHub }

func (g stopAtMerge) Merge(context.Context, int, string, string) (string, error) { panic(errCrash) }

// toMerge polls issue 1 through its proposal and pull request until the
// watcher stops at the merge, ratifying and passing CI as a person and CI
// would, and returns the pull request.
func toMerge(t *testing.T, r *rig) int {
	t.Helper()
	said := map[string]bool{}
	for i := 0; i < 30; i++ {
		if err := r.f.Poll(context.Background()); err != nil {
			if strings.Contains(err.Error(), errCrash.Error()) {
				return r.gh.last(1).Marker.PR
			}
			t.Fatal(err)
		}
		switch last := r.gh.last(1); {
		case last.Marker.Kind == KindProposal && !said["ratify"]:
			said["ratify"] = true
			r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(last.Marker.Proposal.Hash, "sha256:")[:hashChars])
		case last.Marker.Kind == KindPR && !said["ci"]:
			said["ci"] = true
			r.gh.ci(last.Marker.PR, "success")
		}
	}
	t.Fatalf("the watcher never reached the merge: its last post is %q", r.gh.last(1).Marker.Kind)
	return 0
}

// A watcher that stops between recording a merge and making it leaves a
// record, and a person's later merge of that head isn't the factory's: a
// fresh watcher says the pull request was merged, without deleting its
// branch or reporting the merge as its own. When the factory's own login
// merged it, the factory's report stands (#756).
func TestAPersonsMergeAfterTheRecordIsntTheFactorys(t *testing.T) {
	const bot = "invariant-factory[bot]"
	for _, tc := range []struct {
		by       string
		factorys bool
	}{{"gitdek", false}, {bot, true}} {
		t.Run(tc.by, func(t *testing.T) {
			r := newRig(t)
			r.form.forks = nil
			r.gh.me, r.f.Self = bot, bot
			gh := r.gh
			r.f.GitHub = stopAtMerge{gh}
			gh.open(1, "gitdek", "Add a bounded buffer", "Producers put log lines in a buffer; a shipper takes them out in order.\n\n/invariant solve")
			n := toMerge(t, r)
			pr := gh.prs[n]
			pr.Merged, pr.State, pr.MergeCommitSHA = true, "closed", "mergedelsewhere"
			pr.MergedBy = &github.User{Login: tc.by}

			r.f.GitHub = gh
			for i := 0; i < 5 && gh.last(1).Marker.Kind != KindMerged; i++ {
				if err := r.f.Poll(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			last := gh.last(1)
			if last.Marker.Kind != KindMerged {
				t.Fatalf("the merge was never reported: the last post is %q", last.Marker.Kind)
			}
			claimed := len(gh.deleted) != 0 || !strings.Contains(last.Comment.Body, fmt.Sprintf("#%d was merged.", n))
			if claimed != tc.factorys {
				t.Errorf("merged by %s: the factory reported it as its own: %v (deleted %v); want %v:\n%s", tc.by, claimed, gh.deleted, tc.factorys, last.Comment.Body)
			}
			if len(gh.merged) != 0 {
				t.Errorf("the factory merged %v itself after stopping", gh.merged)
			}
		})
	}
}
