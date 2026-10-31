package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
)

// An issue's timeline says what a retry of a plan's failed draft asked for
// and what the factory did: it built the plan again, closing the draft, and
// the new pull request replaces it (#271).
func TestTheTimelineNamesTheDraftARebuildReplaces(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	at := func(min int) string { return now.Add(time.Duration(min-10) * time.Minute).Format(time.RFC3339) }
	plan := &formalize.Proposal{Draft: formalize.Draft{Name: "Rebuild"}, Hash: "sha256:11bff3f218d7aa", Plan: &plumbing.Plan{Name: "Rebuild"}}
	bot := github.User{Login: "invariant-code-factory[bot]", Type: "Bot"}
	is := github.Issue{Number: 271, State: "open", CreatedAt: at(0)}
	comments := []github.Comment{
		{ID: 1, User: bot, CreatedAt: at(1), Body: atOnceMarker(t, factory.Marker{Kind: factory.KindFailed, Failure: factory.FailGate, PR: 700, Proposal: plan})},
		{ID: 2, User: github.User{Login: "gitdek"}, CreatedAt: at(2), Body: "/invariant retry"},
		{ID: 3, User: bot, CreatedAt: at(3), Body: atOnceMarker(t, factory.Marker{Kind: factory.KindRatified, ReplyTo: []int64{2}, Replaces: 700, Proposal: plan})},
		{ID: 4, User: bot, CreatedAt: at(4), Body: atOnceMarker(t, factory.Marker{Kind: factory.KindPR, PR: 701, Replaces: 700, Proposal: plan})},
	}
	lane := Lane(is, comments, bot.Login, now)
	var asked, did []string
	for _, e := range lane.Events {
		switch {
		case e.Kind == factory.Retry && e.Who == WhoPerson:
			asked = append(asked, strings.ToLower(e.Text))
		case e.Who == WhoFactory && e.Kind == factory.KindRatified:
			did = append(did, strings.ToLower(e.Text))
		}
	}
	if len(asked) != 1 || !strings.Contains(asked[0], "build the plan again, replacing #700") || strings.Contains(asked[0], "won't review") {
		t.Errorf("the retry's entry must say it asked to build the plan again, replacing #700: %q", asked)
	}
	if len(did) != 1 || !strings.Contains(did[0], "closed #700") || !strings.Contains(did[0], "building the plan again") {
		t.Errorf("the factory's entry must say it closed #700 and built the plan again: %q", did)
	}
}
