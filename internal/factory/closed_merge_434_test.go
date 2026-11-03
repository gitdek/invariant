package factory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/github"
)

// A person may merge the pull request after the issue's close was recorded.
// The close isn't recorded again, and the later merge is, once, without the
// factory merging anything itself (#434's review).
func TestClosedPoll434RecordsAMergeAfterTheClose(t *testing.T) {
	r, opened := closedPoll434PR(t, false)
	r.gh.issues[1].State = "closed"
	r.poll()
	if closing := r.gh.last(1); !closing.Marker.IssueClosed {
		t.Fatalf("the close wasn't recorded: %+v\n%s", closing.Marker, closing.Comment.Body)
	}
	before := len(r.gh.posts(1))
	r.poll()
	if len(r.gh.posts(1)) != before {
		t.Fatal("another poll recorded the close again")
	}
	pr := r.gh.prs[opened.Marker.PR]
	pr.Merged, pr.State, pr.MergeCommitSHA = true, "closed", "human-merge-434"
	r.poll()
	last := r.gh.last(1)
	if last.Marker.Kind != KindMerged || last.Marker.PR != pr.Number || !strings.Contains(last.Comment.Body, "was merged") {
		t.Fatalf("the merge after the close wasn't recorded: %+v\n%s", last.Marker, last.Comment.Body)
	}
	if len(r.gh.posts(1)) != before+1 || len(r.gh.merged) != 0 {
		t.Fatalf("want one merge record and no merge by the factory: posts=%d, merged=%v", len(r.gh.posts(1))-before, r.gh.merged)
	}
	if !r.gh.issues[1].HasLabel(LabelMerged) {
		t.Errorf("the merged issue has labels %v; want %s", r.gh.labelsOf(1), LabelMerged)
	}
	closedPoll434NoActiveLabels(t, r, 1)
	r.poll()
	if len(r.gh.posts(1)) != before+1 {
		t.Fatal("another poll repeated the merge record")
	}
}

// readCounter434 counts the issues whose comments a poll reads.
type readCounter434 struct {
	*closedPoll434GitHub
	read map[int]int
}

func (g *readCounter434) Comments(ctx context.Context, n int) ([]github.Comment, error) {
	g.read[n]++
	return g.closedPoll434GitHub.Comments(ctx, n)
}

// A poll reads a closed issue only while something is left to record on it.
// A finished or untouched one isn't read at all, so a repository's closed
// issues cost nothing per poll however many there are.
func TestClosedPoll434ReadsOnlyClosedIssuesLeftToRecord(t *testing.T) {
	r := newRig(t)
	g := &readCounter434{closedPoll434GitHub: closedPoll434Hub(r), read: map[int]int{}}
	r.f.GitHub = g
	r.gh.open(1, "gitdek", "Merged long ago", "", LabelTrigger, LabelMerged)
	r.gh.say(1, "gitdek", post("previous work", "Previous work.", Marker{Kind: KindRatified}))
	r.gh.say(1, "gitdek", post("merged", "Merged.", Marker{Kind: KindMerged}))
	r.gh.open(2, "gitdek", "Never taken", "/invariant solve", LabelTrigger)
	r.gh.open(3, "gitdek", "Merged, with a stale label", "", LabelTrigger, LabelPR, LabelMerged)
	r.gh.say(3, "gitdek", post("previous work", "Previous work.", Marker{Kind: KindRatified}))
	r.gh.say(3, "gitdek", post("merged", "Merged.", Marker{Kind: KindMerged}))
	for n := 1; n <= 3; n++ {
		r.gh.issues[n].State = "closed"
	}
	r.gh.open(4, "gitdek", "Open", "")
	r.poll()
	r.poll()
	if g.read[1] != 0 || g.read[2] != 0 {
		t.Errorf("polls read finished closed issues: #1 %d times, #2 %d times; want neither", g.read[1], g.read[2])
	}
	if g.read[3] != 1 {
		t.Errorf("polls read #3, with its stale label, %d times; want once", g.read[3])
	}
	if g.read[4] != 2 {
		t.Errorf("polls read the open #4 %d times; want each time", g.read[4])
	}
	if r.gh.issues[3].HasLabel(LabelPR) || !r.gh.issues[3].HasLabel(LabelMerged) {
		t.Errorf("#3 has labels %v; want its stale label gone and %s kept", r.gh.labelsOf(3), LabelMerged)
	}
	if len(r.gh.posts(1)) != 2 || len(r.gh.posts(2)) != 0 || len(r.gh.posts(3)) != 2 {
		t.Fatal("polling closed issues posted on them")
	}
}

// The issue a factory branch names is the one its merge is recorded on.
func TestBranchIssue434(t *testing.T) {
	for branch, want := range map[string]int{
		"invariant/issue-434-record-closed-work":            434,
		"invariant/issue-434-record-closed-work-retry-6595": 434,
		"invariant/issue-7-x":                               7,
		"hand/issue-452-direction":                          0,
		"invariant/issue-434":                               0,
		"invariant/issue-0-x":                               0,
		"invariant/issue-x-y":                               0,
	} {
		n, ok := branchIssue(branch)
		if want == 0 && ok || want != 0 && n != want {
			t.Errorf("branchIssue(%q) = %d, %v; want %d", branch, n, ok, want)
		}
	}
}

// A merge's issue is read again after GitHub failed for a moment, and not
// after it refused, as for an issue since deleted, nor after it succeeded.
func TestSawMergeRetriesOnlyTransientFailures434(t *testing.T) {
	f := &Factory{seenMerges: map[int]bool{}}
	f.sawMerge(5, errors.New("gh: Bad Gateway (HTTP 502)"))
	f.sawMerge(6, errors.New("gh: This issue was deleted (HTTP 410)"))
	f.sawMerge(7, nil)
	f.sawMerge(0, nil)
	if f.seenMerges[5] || !f.seenMerges[6] || !f.seenMerges[7] || f.seenMerges[0] {
		t.Fatalf("seen merges %v; want #6 and #7 only", f.seenMerges)
	}
}
