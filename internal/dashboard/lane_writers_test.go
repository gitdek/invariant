package dashboard

import (
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/project"
)

// The factory obeys only people who can write to the repository, so on a
// public repository a stranger's command must neither answer a card nor
// move its lane, and /act must still take the owner's (#752).
func TestLaneCountsOnlyWritersCommands(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	at, later := now.Add(-time.Minute).Format(time.RFC3339), now.Add(-30*time.Second).Format(time.RFC3339)
	issue := github.Issue{Number: 33, Title: "Check leases", State: "open", CreatedAt: now.Add(-time.Hour).Format(time.RFC3339),
		Labels: []github.Label{{Name: factory.LabelTrigger}, {Name: factory.LabelProposal}}}
	proposal := &formalize.Proposal{Draft: formalize.Draft{Name: "leases", Statements: []project.Statement{{Name: "OneLeaseHolder", Kind: "invariant", Says: "One holder."}}},
		Hash: "sha256:6db634c64b0912345678"}
	posted := github.Comment{User: github.User{Login: "bot"}, CreatedAt: at, Body: marker(t, factory.Marker{Kind: factory.KindProposal, Proposal: proposal})}
	command := func(association string) github.Comment {
		return github.Comment{User: github.User{Login: "someone"}, AuthorAssociation: association, CreatedAt: later, Body: "/invariant ratify 6db634c64b09"}
	}
	for _, association := range []string{"NONE", "CONTRIBUTOR", "FIRST_TIME_CONTRIBUTOR", "FIRST_TIMER", "MANNEQUIN"} {
		l := Lane(issue, []github.Comment{posted, command(association)}, "", now)
		if l.Waiting == nil || l.Waiting.Hash != "6db634c64b09" || l.Stage == StageQueued {
			t.Errorf("%s's ratify answered the card: stage %q, waiting %+v", association, l.Stage, l.Waiting)
		}
		if l.PeopleComments != 0 {
			t.Errorf("%s's command counted as a person's: %d", association, l.PeopleComments)
		}
	}
	// A writer's command answers it, and so does one whose association a
	// cache never kept.
	for _, association := range []string{"OWNER", "MEMBER", "COLLABORATOR", ""} {
		if l := Lane(issue, []github.Comment{posted, command(association)}, "", now); l.Waiting != nil || l.Stage != StageQueued {
			t.Errorf("%q's ratify didn't answer the card: stage %q, waiting %+v", association, l.Stage, l.Waiting)
		}
	}
}

// Labels need write access to set, but anyone can open an issue that says
// /invariant solve. The factory takes that only from a writer, or from
// itself when it opens a plan's step, and the dashboard lists the same.
func TestDashboardTakesWhatTheFactoryTakes(t *testing.T) {
	solve := func(login, kind, association string, labels ...string) github.Issue {
		is := github.Issue{Number: 1, State: "open", Body: "Please build this.\n\n/invariant solve\n", User: github.User{Login: login, Type: kind}, AuthorAssociation: association}
		for _, l := range labels {
			is.Labels = append(is.Labels, github.Label{Name: l})
		}
		return is
	}
	for _, tc := range []struct {
		name  string
		issue github.Issue
		want  bool
	}{
		{"stranger", solve("someone", "User", "NONE"), false},
		{"past contributor", solve("someone", "User", "CONTRIBUTOR"), false},
		{"owner", solve("gitdek", "User", "OWNER"), true},
		{"collaborator", solve("someone", "User", "COLLABORATOR"), true},
		{"the factory's plan step", solve("invariant-code-factory[bot]", "Bot", "CONTRIBUTOR"), true},
		{"unknown association", solve("someone", "User", ""), true},
		{"labeled stranger's issue", solve("someone", "User", "NONE", factory.LabelTrigger), true},
		{"factory label", solve("someone", "User", "NONE", factory.LabelAsking), true},
		{"no command", github.Issue{Number: 2, State: "open", Body: "An idea.", User: github.User{Login: "gitdek"}, AuthorAssociation: "OWNER"}, false},
	} {
		if got := takes(tc.issue); got != tc.want {
			t.Errorf("%s: takes = %v, want %v", tc.name, got, tc.want)
		}
	}
}
