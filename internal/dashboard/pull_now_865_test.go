package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/github"
)

// A failed build's card says what its pull request is now (#865). The
// factory's record says how it was when the factory stopped, and the pull
// request moves on: here GitHub answers for it through a gh that reads a
// directory, as #674's test has it. A card with no pull request costs no
// read, and one whose pull request can't be read says nothing of now.
func TestAFailedCardReadsItsPullRequestNow865(t *testing.T) {
	yes, no := true, false
	head := strings.Repeat("b", 40)
	runs := func(states ...string) map[string][]github.CheckRun {
		out := []github.CheckRun{}
		for _, s := range states {
			run := github.CheckRun{Name: "invariant/gate", Status: "completed", Conclusion: s}
			if s == "in_progress" {
				run.Status, run.Conclusion = s, ""
			}
			out = append(out, run)
		}
		return map[string][]github.CheckRun{"check_runs": out}
	}
	for _, tc := range []struct {
		name string
		pull *github.PullRequest
		runs map[string][]github.CheckRun
		want *PullNow
	}{
		{"ready, mergeable and past the gate", &github.PullRequest{Number: 7, State: "open", Mergeable: &yes, Head: github.Ref{SHA: head}}, runs("success"), &PullNow{State: "open", Gate: "passed"}},
		{"a rerun that passed after a failure", &github.PullRequest{Number: 7, State: "open", Mergeable: &yes, Head: github.Ref{SHA: head}}, runs("failure", "success"), &PullNow{State: "open", Gate: "passed"}},
		{"conflicting, and failed the gate", &github.PullRequest{Number: 7, State: "open", Mergeable: &no, Head: github.Ref{SHA: head}}, runs("failure"), &PullNow{State: "open", Conflicts: true, Gate: "failed"}},
		{"a draft whose gate is running again", &github.PullRequest{Number: 7, State: "open", Draft: true, Mergeable: &yes, Head: github.Ref{SHA: head}}, runs("failure", "in_progress"), &PullNow{State: "open", Draft: true, Gate: "running"}},
		{"just pushed: no gate yet, mergeability unsettled", &github.PullRequest{Number: 7, State: "open", Head: github.Ref{SHA: head}}, runs(), &PullNow{State: "open", Unsettled: true}},
		{"merged by a person", &github.PullRequest{Number: 7, State: "closed", Merged: true, Head: github.Ref{SHA: head}}, nil, &PullNow{State: "merged"}},
		{"closed", &github.PullRequest{Number: 7, State: "closed", Head: github.Ref{SHA: head}}, nil, &PullNow{State: "closed"}},
		{"GitHub can't read it", nil, nil, nil},
		{"its gate can't be read", &github.PullRequest{Number: 7, State: "open", Mergeable: &yes, Head: github.Ref{SHA: head}}, nil, nil},
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
			labels := []github.Label{{Name: factory.LabelTrigger}, {Name: factory.LabelHumanReview}}
			write("issues.json", []github.Issue{
				{Number: 5, Title: "Hold one lease", State: "open", User: github.User{Login: "gitdek"}, CreatedAt: "2026-11-06T10:00:00Z", UpdatedAt: "2026-11-06T11:00:00Z", Labels: labels},
				{Number: 6, Title: "Stopped before a pull request", State: "open", User: github.User{Login: "gitdek"}, CreatedAt: "2026-11-06T10:00:00Z", UpdatedAt: "2026-11-06T11:00:00Z", Labels: labels},
			})
			write("comments-5.json", []github.Comment{{ID: 1, User: bot, CreatedAt: "2026-11-06T10:30:00Z", Body: marker(t, factory.Marker{Kind: factory.KindFailed, Failure: factory.FailCI, PR: 7})}})
			write("comments-6.json", []github.Comment{{ID: 2, User: bot, CreatedAt: "2026-11-06T10:30:00Z", Body: marker(t, factory.Marker{Kind: factory.KindFailed, Failure: factory.FailStopped})}})
			if tc.pull != nil {
				write("pull-7.json", tc.pull)
			}
			if tc.runs != nil {
				write("runs.json", tc.runs)
			}
			s := &Server{Repos: []*Repo{{Name: "o/r", GitHub: github.Client{Repo: "o/r", GH: filepath.Join(dir, "gh")}, FactoryLogin: bot.Login}}, Branch: "main", Cache: t.TempDir(), Log: t.Logf}
			s.prepare()
			if err := s.refresh(t.Context()); err != nil {
				t.Fatal(err)
			}
			snap := s.assemble(time.Now())
			cards := map[int]*Waiting{}
			for _, is := range snap.Issues {
				cards[is.Number] = is.Waiting
			}
			if cards[5] == nil || cards[5].PR != 7 || cards[6] == nil || cards[6].PR != 0 {
				t.Fatalf("the fixture's cards are %+v and %+v", cards[5], cards[6])
			}
			if !reflect.DeepEqual(cards[5].Now, tc.want) {
				t.Errorf("the card says its pull request is now %+v, want %+v", cards[5].Now, tc.want)
			}
			if cards[6].Now != nil {
				t.Errorf("a card with no pull request says %+v of one", cards[6].Now)
			}
			// Two reads for an open pull request, one for one that isn't, and
			// none for the card without one.
			b, _ := os.ReadFile(filepath.Join(dir, "calls"))
			pulls, checks := 0, 0
			for _, c := range strings.Fields(string(b)) {
				if strings.Contains(c, "/pulls/") {
					pulls++
				}
				if strings.Contains(c, "/check-runs") {
					checks++
				}
			}
			wantChecks := 0
			if tc.pull != nil && tc.pull.State == "open" {
				wantChecks = 1
			}
			if pulls != 1 || checks != wantChecks {
				t.Errorf("the refresh read %d pull requests and %d heads' check runs, want 1 and %d: %s", pulls, checks, wantChecks, b)
			}
		})
	}
}
