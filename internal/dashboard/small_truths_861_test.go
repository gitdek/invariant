package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

// The page counts merged pull requests one way (#861): an issue closed as
// merged with no pull request, as the first one here was by hand, is in
// none of its counts, where "Who builds what" used to count it.
func TestMergedPullRequestsAreCountedOneWay861(t *testing.T) {
	bot := github.User{Login: "invariant-code-factory[bot]", Type: "Bot"}
	merged := []github.Label{{Name: factory.LabelTrigger}, {Name: factory.LabelMerged}}
	s := &Server{Cache: t.TempDir(), Log: t.Logf, Repos: []*Repo{{Name: "gitdek/invariant", FactoryLogin: bot.Login, src: sources{
		issues: []github.Issue{
			{Number: 1, Title: "Merged by hand", State: "closed", CreatedAt: "2026-11-12T08:00:00Z", ClosedAt: "2026-11-12T09:00:00Z", Labels: merged},
			{Number: 2, Title: "Merged by the factory", State: "closed", CreatedAt: "2026-11-12T08:00:00Z", ClosedAt: "2026-11-12T10:00:00Z", Labels: merged},
		},
		comments: map[int]cachedComments{2: {comments: []github.Comment{
			{User: bot, CreatedAt: "2026-11-12T09:30:00Z", Body: marker(t, factory.Marker{Kind: factory.KindPR, PR: 7})},
			{User: bot, CreatedAt: "2026-11-12T10:00:00Z", Body: marker(t, factory.Marker{Kind: factory.KindMerged, PR: 7})},
		}}},
	}}}}
	snap := s.assemble(time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC))
	stages := 0
	for _, is := range snap.Issues {
		if is.Stage == StageMerged {
			stages++
		}
	}
	if stages != 2 {
		t.Fatalf("the fixture should hold two merged issues, one with no pull request: %+v", snap.Issues)
	}
	if snap.Totals.Merged != 1 || snap.Repos[0].Totals.Merged != 1 || snap.Who.Merged != 1 {
		t.Fatalf("merged pull requests: totals %d, the repository's %d, who builds what %d; want 1 each",
			snap.Totals.Merged, snap.Repos[0].Totals.Merged, snap.Who.Merged)
	}
}

// An evidence page writes a count grouped in threes, as a receipt does, and
// a caught bug's line isn't drawn in a miss's color (#861).
func TestEvidencePageCountsAndCaughtBugs861(t *testing.T) {
	for n, want := range map[int64]string{1: "1 step", 5: "5 steps", 999: "999 steps", 1000: "1,000 steps", 132486: "132,486 steps", 1234567: "1,234,567 steps"} {
		if got := counted(n, "step"); got != want {
			t.Errorf("counted(%d) = %q, want %q", n, got, want)
		}
	}
	r := &verify.Report{Design: verify.Design{Passed: true, DistinctStates: 132486}, Bugs: []verify.Bug{{Name: "Lost", Caught: true, Violated: "Safe", Steps: 5}, {Name: "Slipped"}}}
	if found, missed := foundFor(project.Statement{Name: "Safe", Kind: project.Invariant}, r); found != "Held in all 132,486 checked states" || missed {
		t.Errorf("an invariant that held reads %q, missed %v", found, missed)
	}
	if found, missed := foundFor(project.Statement{Name: "Lost", Kind: project.Bug}, r); found != "Caught: breaks Safe in 5 steps" || missed {
		t.Errorf("a caught bug reads %q, missed %v", found, missed)
	}
	if found, missed := foundFor(project.Statement{Name: "Slipped", Kind: project.Bug}, r); found != "Not caught" || !missed {
		t.Errorf("a bug the model didn't catch reads %q, missed %v", found, missed)
	}
	// Only a miss takes the bug color. A bug rule's left edge still marks
	// what kind of rule it is.
	if strings.Contains(evidenceHead, ".rule.bug .found{") || !strings.Contains(evidenceHead, ".rule .found.missed{color:var(--bug)}") {
		t.Error("the evidence page's style colors a caught bug's line as a miss, or no longer colors a miss")
	}
}
