package dashboard

import (
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
)

// A marker drives the page only when the account the repository's factory
// posts as posted it, as the factory reads only its own (#666): the same
// proposal reposted by anyone else, a person or another App, shows nothing,
// and with no known account none does.
func TestOnlyTheFactorysOwnMarkersDriveThePage(t *testing.T) {
	const at = "2026-11-06T12:00:00Z"
	now := time.Date(2026, 11, 6, 13, 0, 0, 0, time.UTC)
	m := marker(t, factory.Marker{Kind: factory.KindProposal, Proposal: &formalize.Proposal{Draft: formalize.Draft{Name: "leases"}, Hash: "645f2d6b79c8"}})
	is := github.Issue{Number: 7, Title: "Hold one lease", State: "open", CreatedAt: at, Labels: []github.Label{{Name: factory.LabelTrigger}}}
	repo := func(login string, by ...github.User) *Repo {
		var comments []github.Comment
		for i, u := range by {
			comments = append(comments, github.Comment{ID: int64(i + 1), User: u, CreatedAt: at, Body: m})
		}
		return &Repo{Name: "o/r", FactoryLogin: login, src: sources{issues: []github.Issue{is}, comments: map[int]cachedComments{7: {comments: comments}}}}
	}
	lane := func(s *Server) Issue {
		t.Helper()
		for _, l := range s.assemble(now).Issues {
			if l.Number == 7 {
				return l
			}
		}
		t.Fatal("the issue left the page")
		return Issue{}
	}
	factoryState := func(l Issue) bool {
		for _, e := range l.Events {
			if e.Who == WhoFactory {
				return true
			}
		}
		return l.Waiting != nil || l.Statements != 0 || l.Project != ""
	}
	bot := github.User{Login: "factory[bot]", Type: "Bot"}
	person := github.User{Login: "mallory", Type: "User"}
	other := github.User{Login: "other-app[bot]", Type: "Bot"}

	asks := func(l Issue) bool {
		return l.Waiting != nil && l.Waiting.Kind == factory.KindProposal && l.Waiting.Hash == "645f2d6b79c8"
	}
	if l := lane(&Server{Repos: []*Repo{repo("factory[bot]", bot)}}); !asks(l) {
		t.Fatalf("the factory's own proposal doesn't ask for ratification: %+v", l.Waiting)
	}
	for _, by := range []github.User{person, other} {
		if l := lane(&Server{Repos: []*Repo{repo("factory[bot]", by)}}); factoryState(l) {
			t.Errorf("a marker %s reposted drives the card: %+v", by.Login, l)
		}
	}
	if l := lane(&Server{Repos: []*Repo{repo("", bot)}}); factoryState(l) {
		t.Errorf("with no known factory account, a marker drives the card: %+v", l)
	}

	// The account comes from a watcher's status file, a stopped watcher's
	// too, and a marker anyone else posts still counts as nothing.
	work := t.TempDir()
	if err := WriteStatus(StatusPath(work, "o/r"), Status{Repo: "o/r", Login: "factory[bot]"}); err != nil {
		t.Fatal(err)
	}
	if l := lane(&Server{Works: []string{work}, Repos: []*Repo{repo("", bot)}}); !asks(l) {
		t.Fatalf("the watcher's account isn't the factory's: %+v", l.Waiting)
	}
	if l := lane(&Server{Works: []string{work}, Repos: []*Repo{repo("", person)}}); factoryState(l) {
		t.Errorf("with the watcher's account known, another's marker drives the card: %+v", l)
	}
}
