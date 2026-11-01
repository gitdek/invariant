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
		// The retry's build stopped, and a second retry builds again: it
		// closes nothing, since #700 is closed already.
		{ID: 4, User: bot, CreatedAt: at(4), Body: atOnceMarker(t, factory.Marker{Kind: factory.KindFailed, Failure: factory.FailStopped, Replaces: 700, Proposal: plan})},
		{ID: 5, User: github.User{Login: "gitdek"}, CreatedAt: at(5), Body: "/invariant retry"},
		{ID: 6, User: bot, CreatedAt: at(6), Body: atOnceMarker(t, factory.Marker{Kind: factory.KindRatified, ReplyTo: []int64{5}, Replaces: 700, Proposal: plan})},
		{ID: 7, User: bot, CreatedAt: at(7), Body: atOnceMarker(t, factory.Marker{Kind: factory.KindPR, PR: 701, Replaces: 700, Proposal: plan})},
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
	if len(asked) != 2 || !strings.Contains(asked[0], "build the plan again, replacing #700") || strings.Contains(asked[0], "won't review") {
		t.Errorf("the first retry's entry must say it asked to build the plan again, replacing #700: %q", asked)
	}
	if len(did) != 2 || !strings.Contains(did[0], "closed #700") || !strings.Contains(did[0], "building the plan again") {
		t.Errorf("the factory's first entry must say it closed #700 and built the plan again: %q", did)
	}
	if len(did) == 2 && (strings.Contains(did[1], "closed") || !strings.Contains(did[1], "building the plan again")) {
		t.Errorf("the second retry's build closes nothing, so its entry mustn't say it closed #700: %q", did[1])
	}
}

// A retry's build that couldn't reach the commit holding the plan's
// ratification says so for a person, and its card offers only drafting the
// plan again, and says why, as the factory's post does (D-0220, D-0238).
func TestAGoneRatificationsCardOffersOnlyDraftingAgain(t *testing.T) {
	plan := &formalize.Proposal{Draft: formalize.Draft{Name: "Rebuild"}, Hash: "sha256:11bff3f218d7aa", Plan: &plumbing.Plan{Name: "Rebuild"}}
	w := waitingFor(factory.Marker{Kind: factory.KindFailed, Failure: factory.FailStopped, Replaces: 700, From: "0123456789ab", Gone: true, Proposal: plan}, time.Now())
	if w == nil || !w.Gone || w.Retry != nil || len(w.Takes) != 1 || w.Takes[0] != factory.Revise {
		t.Fatalf("the card must offer only drafting the plan again: %+v", w)
	}
	issues := []Issue{{Repo: "o/r", Number: 271, Open: true, Waiting: w}}
	if err := (act{Repo: "o/r", Issue: 271, Body: "/invariant retry"}).check(issues, nil); err == nil {
		t.Error("/act posts a retry the card doesn't offer")
	}
	if err := (act{Repo: "o/r", Issue: 271, Body: "/invariant revise"}).check(issues, nil); err != nil {
		t.Errorf("/act refuses the revise the card offers: %v", err)
	}
	b, err := web.ReadFile("web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "if (w.gone) return") {
		t.Error("the page doesn't say why a card whose ratification is gone only drafts again")
	}
	// A build that stopped otherwise still builds again.
	if w := waitingFor(factory.Marker{Kind: factory.KindFailed, Failure: factory.FailStopped, Replaces: 700, Proposal: plan}, time.Now()); w.Gone || w.Retry == nil || len(w.Takes) != 2 {
		t.Errorf("a stopped build's card must still offer a retry: %+v", w)
	}
}

// The card for a plan's failed draft that a retry rebuilds says how to
// change the plan instead in the words the factory's post uses: close the
// draft, say what to change, revise, and ratify the new draft (D-0238).
func TestTheRebuildCardChangesThePlanAsThePostSays(t *testing.T) {
	w := waitingFor(factory.Marker{Kind: factory.KindFailed, Failure: factory.FailGate, PR: 700,
		Proposal: &formalize.Proposal{Hash: "sha256:11bff3f218d7aa", Plan: &plumbing.Plan{Name: "Rebuild"}}}, time.Now())
	if w == nil || w.Retry == nil {
		t.Fatalf("no rebuild card: %+v", w)
	}
	if !strings.Contains(w.Retry.Guidance, "To change the plan instead, close #700, say what to change") {
		t.Errorf("the card's way to change the plan isn't the post's: %q", w.Retry.Guidance)
	}
}
