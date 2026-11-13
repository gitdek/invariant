package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
)

// A card's pull request says when it last changed (#902): the later of its
// last update and its gate's latest run on its head. The page counts a
// card's wait from then, so one whose gate passed after the factory's post
// isn't folded away with what has waited for days. GitHub answers here
// through a gh that reads a directory, as #865's test has it.
func TestACardsPullRequestSaysWhenItLastChanged902(t *testing.T) {
	yes := true
	head := strings.Repeat("c", 40)
	at := func(s string) *time.Time {
		t.Helper()
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return &v
	}
	passed := github.CheckRun{Name: "invariant/gate", Status: "completed", Conclusion: "success", StartedAt: "2026-11-13T00:40:40Z", CompletedAt: "2026-11-13T00:46:10Z"}
	open := func(updated string) *github.PullRequest {
		return &github.PullRequest{Number: 7, State: "open", Draft: true, Mergeable: &yes, Head: github.Ref{SHA: head}, UpdatedAt: updated}
	}
	for _, tc := range []struct {
		name string
		pull *github.PullRequest
		runs []github.CheckRun
		want *time.Time
	}{
		{"its gate ended after its last push", open("2026-11-13T00:40:34Z"), []github.CheckRun{passed}, at("2026-11-13T00:46:10Z")},
		{"it was updated after its gate ended", open("2026-11-13T09:00:00Z"), []github.CheckRun{passed}, at("2026-11-13T09:00:00Z")},
		{"an earlier run ended later than the latest began", open("2026-11-10T02:35:00Z"),
			[]github.CheckRun{{Name: "invariant/gate", Status: "completed", Conclusion: "failure", StartedAt: "2026-11-10T02:36:00Z", CompletedAt: "2026-11-10T02:52:00Z"}, passed}, at("2026-11-13T00:46:10Z")},
		{"its gate is running", open("2026-11-13T00:40:34Z"),
			[]github.CheckRun{{Name: "invariant/gate", Status: "in_progress", StartedAt: "2026-11-13T00:41:00Z"}}, at("2026-11-13T00:41:00Z")},
		{"GitHub gives no times", open(""), []github.CheckRun{{Name: "invariant/gate", Status: "completed", Conclusion: "success"}}, nil},
		{"times that aren't times", open("yesterday"), []github.CheckRun{{Name: "invariant/gate", Status: "completed", Conclusion: "success", StartedAt: "soon", CompletedAt: "later"}}, nil},
		{"merged", &github.PullRequest{Number: 7, State: "closed", Merged: true, Head: github.Ref{SHA: head}, UpdatedAt: "2026-11-13T12:49:24Z"}, nil, at("2026-11-13T12:49:24Z")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name string, v any) {
				t.Helper()
				b, err := json.Marshal(v)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(strings.ReplaceAll(keptGH, "@DIR@", dir)), 0o755); err != nil {
				t.Fatal(err)
			}
			bot := github.User{Login: "factory[bot]", Type: "Bot"}
			write("issues.json", []github.Issue{{Number: 5, Title: "Hold one lease", State: "open", User: github.User{Login: "gitdek"},
				CreatedAt: "2026-11-10T02:00:00Z", UpdatedAt: "2026-11-10T02:52:36Z", Labels: []github.Label{{Name: factory.LabelTrigger}, {Name: factory.LabelHumanReview}}}})
			write("comments-5.json", []github.Comment{{ID: 1, User: bot, CreatedAt: "2026-11-10T02:52:36Z", Body: marker(t, factory.Marker{Kind: factory.KindFailed, Failure: factory.FailCI, PR: 7})}})
			write("pull-7.json", tc.pull)
			if tc.runs != nil {
				write("runs.json", map[string][]github.CheckRun{"check_runs": tc.runs})
			}
			s := &Server{Repos: []*Repo{{Name: "o/r", GitHub: github.Client{Repo: "o/r", GH: filepath.Join(dir, "gh")}, FactoryLogin: bot.Login}}, Branch: "main", Cache: t.TempDir(), Log: t.Logf}
			s.prepare()
			if err := s.refresh(t.Context()); err != nil {
				t.Fatal(err)
			}
			var card *Waiting
			for _, is := range s.assemble(time.Now()).Issues {
				if is.Number == 5 {
					card = is.Waiting
				}
			}
			if card == nil || card.Now == nil {
				t.Fatalf("the card says nothing of its pull request now: %+v", card)
			}
			switch got := card.Now.Changed; {
			case tc.want == nil && got != nil:
				t.Errorf("the pull request last changed at %v, and GitHub gave no time", got)
			case tc.want != nil && (got == nil || !got.Equal(*tc.want)):
				t.Errorf("the pull request last changed at %v, want %v", got, tc.want)
			}
			// The page reads it as JSON, under this name.
			b, err := json.Marshal(card.Now)
			if err != nil {
				t.Fatal(err)
			}
			if has := strings.Contains(string(b), `"changed":"`); has != (tc.want != nil) {
				t.Errorf("the page is sent %s", b)
			}
		})
	}
}

// A retry that waits on CI's gate has a second label for a gate that has
// passed, which doesn't speak of one still to pass (#902). A retry that
// builds again has none.
func TestARetryHasALabelForAGateThatHasPassed902(t *testing.T) {
	for _, tc := range []struct {
		name          string
		m             factory.Marker
		label, passed string
	}{
		{"the factory merges it", factory.Marker{Kind: factory.KindFailed, Failure: factory.FailCI, PR: 7}, "Merge #7 once CI passes", "Merge #7"},
		{"a person merges it", factory.Marker{Kind: factory.KindFailed, Failure: factory.FailTrusted, PR: 7}, "Have a person merge #7 once CI passes", "Have a person merge #7"},
		{"it builds the ratified proposal again", factory.Marker{Kind: factory.KindFailed, Failure: factory.FailStopped}, "Build ratified proposal again", ""},
		{"it builds a plan's failed draft again", factory.Marker{Kind: factory.KindFailed, Failure: factory.FailGate, PR: 7, Project: plumbing.LockPath(5)}, "Build the plan again, replacing #7", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := retryCopy(tc.m)
			if r.Label != tc.label || r.LabelPassed != tc.passed {
				t.Errorf("the retry is labeled %q, and %q once the gate has passed; want %q and %q", r.Label, r.LabelPassed, tc.label, tc.passed)
			}
		})
	}
}
