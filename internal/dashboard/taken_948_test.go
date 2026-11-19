package dashboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
)

// taken948GH is a gh that answers a repository's reads from a directory:
// issues.json for its issues and pull requests, comments-<n>.json, or none,
// pull-<n>.json, and runs.json for any commit's check runs. Every other read
// is Not Found. It logs each path it's asked for to calls.
const taken948GH = `#!/bin/sh
d='@DIR@'
for last; do :; done
echo "$last" >> "$d/calls"
case "$last" in
*/issues\?*) cat "$d/issues.json" ;;
*/issues/*/comments\?*)
	n=${last#*/issues/}
	n=${n%%/*}
	if [ -f "$d/comments-$n.json" ]; then cat "$d/comments-$n.json"; else echo '[]'; fi ;;
*/pulls/*)
	n=${last##*/pulls/}
	cat "$d/pull-$n.json" ;;
*/check-runs\?*) cat "$d/runs.json" ;;
*) echo "gh: Not Found (HTTP 404)" >&2; exit 1 ;;
esac
`

type taken948 struct {
	dir string
	t   *testing.T
}

func newTaken948(t *testing.T) taken948 {
	t.Helper()
	f := taken948{dir: t.TempDir(), t: t}
	if err := os.WriteFile(filepath.Join(f.dir, "gh"), []byte(strings.ReplaceAll(taken948GH, "@DIR@", f.dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	f.write("runs.json", map[string][]github.CheckRun{"check_runs": {{Name: "invariant/gate", Status: "completed", Conclusion: "success"}}})
	return f
}

func (f taken948) write(name string, v any) {
	f.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, name), b, 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// calls is how many reads asked for paths holding what.
func (f taken948) calls(what string) int {
	b, _ := os.ReadFile(filepath.Join(f.dir, "calls"))
	return strings.Count(string(b), what)
}

func (f taken948) server(repo string) *Server {
	s := &Server{Repos: []*Repo{{Name: repo, GitHub: github.Client{Repo: repo, GH: filepath.Join(f.dir, "gh")}, FactoryLogin: bot948.Login}}, Branch: "main", Cache: f.t.TempDir(), Log: f.t.Logf}
	s.prepare()
	return s
}

var (
	bot948    = github.User{Login: "invariant-code-factory[bot]", Type: "Bot"}
	person948 = github.User{Login: "gitdek", Type: "User"}
)

func say948(t *testing.T, id int64, at string, m factory.Marker) github.Comment {
	return github.Comment{ID: id, User: bot948, CreatedAt: at, Body: marker(t, m), URL: fmt.Sprintf("https://github.com/o/r/issues/1#issuecomment-%d", id)}
}

func command948(id int64, at, body string) github.Comment {
	return github.Comment{ID: id, User: person948, CreatedAt: at, Body: body, AuthorAssociation: "OWNER"}
}

func pull948(n int, merged, body string) github.Issue {
	return github.Issue{Number: n, State: "closed", Body: body, User: person948, CreatedAt: merged, UpdatedAt: merged, ClosedAt: merged, PullRequest: &github.PullLink{MergedAt: merged}}
}

// snap948 is what the page reads of a snapshot's issues and counts.
type snap948 struct {
	Issues []struct {
		Number int    `json:"number"`
		Stage  string `json:"stage"`
		PR     int    `json:"pr"`
		ByHand bool   `json:"byHand"`
		Doing  string `json:"doing"`
		Waits  string `json:"waits"`
		Plan   *struct {
			Step    int    `json:"step"`
			Steps   int    `json:"steps"`
			Issue   int    `json:"issue"`
			Stage   string `json:"stage"`
			Waiting string `json:"waiting"`
		} `json:"plan"`
	} `json:"issues"`
	Totals struct {
		Merged        int `json:"merged"`
		ByHand        int `json:"byHand"`
		MergesChecked int `json:"mergesChecked"`
		BadMerges     int `json:"badMerges"`
	} `json:"totals"`
	Tiles []Tile `json:"tiles"`
	Who   struct {
		Merged int `json:"merged"`
	} `json:"who"`
	Now struct {
		Headline string `json:"headline"`
	} `json:"now"`
	Activity []Event `json:"activity"`
}

func read948(t *testing.T, snap Snapshot) snap948 {
	t.Helper()
	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	var s snap948
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (s snap948) issue(n int) int {
	for i, is := range s.Issues {
		if is.Number == n {
			return i
		}
	}
	return -1
}

// An issue the factory built and a person finished counts as merged once
// its change merges through the gate, whoever merged it (#948). On Air's #8
// was handed over by a comment, ratified and built, its build failed and
// its pull request closed, and a person finished it in that pull request,
// #21, and merged it. The factory took its labels off as the issue closed.
// A pull request a person opened for an issue the factory never took
// doesn't count, nor one that merged into another branch.
func TestAnIssueFinishedByHandCountsAsMerged948(t *testing.T) {
	f := newTaken948(t)
	const repo = "gitdek/onair"
	issue := func(n int, closed string, comments int, labels ...string) github.Issue {
		is := github.Issue{Number: n, Title: fmt.Sprintf("Issue %d", n), State: "closed", User: person948, AuthorAssociation: "OWNER", Comments: comments,
			CreatedAt: "2026-11-14T13:00:00Z", UpdatedAt: closed, ClosedAt: closed, StateReason: "completed"}
		for _, l := range labels {
			is.Labels = append(is.Labels, github.Label{Name: l})
		}
		return is
	}
	f.write("issues.json", []github.Issue{
		issue(8, "2026-11-15T15:48:09Z", 4),
		pull948(21, "2026-11-15T15:48:07Z", "Finished by hand: the factory's build failed only on Gobra.\n\nCloses #8.\n"),
		issue(5, "2026-11-14T13:19:14Z", 1),
		pull948(6, "2026-11-14T13:19:13Z", "Closes #5."),
		issue(10, "2026-11-14T00:00:00Z", 2, factory.LabelTrigger, factory.LabelMerged),
		pull948(11, "2026-11-13T23:59:58Z", "Closes #10."),
		issue(30, "2026-11-16T10:00:01Z", 2),
		pull948(31, "2026-11-16T10:00:00Z", "Fixes gitdek/onair#30"),
	})
	built := factory.Marker{Kind: factory.KindRatified, Project: "core/session", Branch: "invariant/issue-8-recording-sessions"}
	f.write("comments-8.json", []github.Comment{
		command948(1, "2026-11-14T13:58:26Z", "/invariant solve"),
		say948(t, 2, "2026-11-14T17:38:51Z", built),
		say948(t, 3, "2026-11-14T19:07:08Z", factory.Marker{Kind: factory.KindFailed, Project: "core/session", PR: 21}),
		say948(t, 4, "2026-11-14T19:31:33Z", factory.Marker{Kind: factory.KindClosed, Project: "core/session", PR: 21}),
	})
	f.write("comments-5.json", []github.Comment{command948(5, "2026-11-14T13:10:00Z", "Looks right.")})
	f.write("comments-10.json", []github.Comment{
		say948(t, 6, "2026-11-13T23:00:00Z", factory.Marker{Kind: factory.KindRatified, Project: "core/handover"}),
		say948(t, 7, "2026-11-14T00:00:00Z", factory.Marker{Kind: factory.KindMerged, Project: "core/handover", PR: 11}),
	})
	f.write("comments-30.json", []github.Comment{
		say948(t, 8, "2026-11-16T09:00:00Z", factory.Marker{Kind: factory.KindRatified, Project: "core/pieces"}),
		say948(t, 9, "2026-11-16T09:30:00Z", factory.Marker{Kind: factory.KindFailed, Project: "core/pieces"}),
	})
	for n, base := range map[int]string{21: "main", 11: "main", 31: "release"} {
		f.write(fmt.Sprintf("pull-%d.json", n), github.PullRequest{Number: n, Merged: true, Head: github.Ref{SHA: strings.Repeat(fmt.Sprint(n%10), 40)}, Base: github.Ref{Ref: base}})
	}
	s := f.server(repo)
	if err := s.refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	snap := read948(t, s.assemble(time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)))
	i := snap.issue(8)
	if i < 0 {
		t.Fatalf("onair#8, which the factory built and a person finished, isn't among the issues: %+v", snap.Issues)
	}
	if is := snap.Issues[i]; is.Stage != StageMerged || is.PR != 21 || !is.ByHand {
		t.Errorf("onair#8 is %+v; want it merged in #21, finished by hand", is)
	}
	if snap.issue(5) >= 0 {
		t.Error("an issue the factory never took is among its issues, for a person's pull request")
	}
	if i := snap.issue(30); i < 0 || snap.Issues[i].Stage != StageClosed || snap.Issues[i].ByHand {
		t.Errorf("onair#30, whose pull request merged into another branch, should show closed, not merged: %+v", snap.Issues)
	}
	if snap.Totals.Merged != 2 || snap.Totals.ByHand != 1 || snap.Totals.MergesChecked != 2 || snap.Totals.BadMerges != 0 || snap.Who.Merged != 2 {
		t.Errorf("merged %d (%d by hand, %d checked, %d bad), who builds what %d; want #8 and #10, one by hand, both checked green", snap.Totals.Merged, snap.Totals.ByHand, snap.Totals.MergesChecked, snap.Totals.BadMerges, snap.Who.Merged)
	}
	if !strings.HasPrefix(snap.Tiles[0].Small, "CI's gate passed on the exact head of all 2 pull requests that merged the factory's issues, 1 of them finished by hand. ") {
		t.Errorf("the merges tile says %q; want it to count the one finished by hand", snap.Tiles[0].Small)
	}
}

// A refresh reads at most a few closed issues' comments to learn whether
// the factory built them, so a dashboard with no kept reads asks GitHub for
// them over several refreshes, not at once (#948).
func TestClosedIssuesAreReadAFewAtATime948(t *testing.T) {
	f := newTaken948(t)
	var all []github.Issue
	for n := 1; n <= 25; n++ {
		closed := fmt.Sprintf("2026-11-%02dT10:00:01Z", 1+n%28)
		all = append(all, github.Issue{Number: n, Title: "Built", State: "closed", User: person948, AuthorAssociation: "OWNER", Comments: 1, CreatedAt: closed, UpdatedAt: closed, ClosedAt: closed},
			pull948(100+n, strings.Replace(closed, ":01Z", ":00Z", 1), fmt.Sprintf("Closes #%d.", n)))
		f.write(fmt.Sprintf("comments-%d.json", n), []github.Comment{say948(t, int64(n), closed, factory.Marker{Kind: factory.KindRatified, Project: "core/x"})})
		f.write(fmt.Sprintf("pull-%d.json", 100+n), github.PullRequest{Number: 100 + n, Merged: true, Head: github.Ref{SHA: strings.Repeat("a", 40)}, Base: github.Ref{Ref: "main"}})
	}
	f.write("issues.json", all)
	s := f.server("o/r")
	read := 0
	for round := 1; round <= 3; round++ {
		if err := s.refresh(t.Context()); err != nil {
			t.Fatal(err)
		}
		n := f.calls("/comments?")
		if n-read > closedReads {
			t.Fatalf("refresh %d read %d issues' comments; want at most %d", round, n-read, closedReads)
		}
		read = n
	}
	if snap := read948(t, s.assemble(time.Now())); len(snap.Issues) != 25 || snap.Totals.Merged != 25 || snap.Totals.ByHand != 25 {
		t.Errorf("after three refreshes the page shows %d issues and %d merged (%d by hand); want all 25, each finished by hand", len(snap.Issues), snap.Totals.Merged, snap.Totals.ByHand)
	}
}

// An issue appears once a writer's command hands it to the factory, with
// what the factory is doing with it, before the factory has posted on it,
// and the activity shows the command (#948). A stranger's command hands
// nothing over, and a writer's issue nobody handed over stays for /act.
func TestAnIssueHandedOverShowsAtOnce948(t *testing.T) {
	f := newTaken948(t)
	const repo = "gitdek/invariant"
	work := t.TempDir()
	at := time.Date(2026, 11, 19, 12, 15, 0, 0, time.UTC)
	draft := Step{Issue: 946, Doing: "formalizing", Since: at, Agent: "codex", Run: "formalize-946"}
	// #947's step waits for one of the factory's agent slots: its agent
	// hasn't started, so the watcher has named no run for it.
	queuedDraft := Step{Issue: 947, Doing: "formalizing", Since: at.Add(time.Minute)}
	st := Status{PID: os.Getpid(), Repo: repo, Login: bot948.Login, Started: at, Heartbeat: at, Every: 30, Limit: 9000}
	st.Update([]Step{draft, queuedDraft}, at)
	if err := WriteStatus(StatusPath(work, repo), st); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(stepDir(work, repo, draft), "transcript.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	open := func(n int, by github.User, association, body string, comments int) github.Issue {
		return github.Issue{Number: n, Title: fmt.Sprintf("Issue %d", n), State: "open", Body: body, User: by, AuthorAssociation: association, Comments: comments,
			CreatedAt: "2026-11-19T11:00:00Z", UpdatedAt: "2026-11-19T12:15:00Z"}
	}
	stranger := github.User{Login: "stranger", Type: "User"}
	f.write("issues.json", []github.Issue{
		open(946, person948, "OWNER", "Kind: plumbing\n\nDo one thing.", 1),
		open(947, person948, "OWNER", "A whole PRD.", 1),
		open(950, stranger, "NONE", "Please look at this.", 1),
		open(951, person948, "OWNER", "Kind: plumbing\n\nNot handed over yet.", 0),
		open(952, stranger, "NONE", "Kind: plumbing\n\nMine.", 1),
	})
	f.write("comments-946.json", []github.Comment{command948(1, "2026-11-19T12:15:00Z", "/invariant solve")})
	f.write("comments-947.json", []github.Comment{command948(2, "2026-11-19T12:15:30Z", "/invariant plan")})
	f.write("comments-950.json", []github.Comment{command948(3, "2026-11-19T12:16:00Z", "/invariant solve")})
	f.write("comments-952.json", []github.Comment{{ID: 4, User: stranger, AuthorAssociation: "NONE", CreatedAt: "2026-11-19T12:16:30Z", Body: "/invariant solve"}})
	s := f.server(repo)
	s.Work, s.Repos[0].Status = work, StatusPath(work, repo)
	if err := s.refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	snap := read948(t, s.assemble(at.Add(5*time.Minute)))
	for _, n := range []int{946, 947, 950} {
		if snap.issue(n) < 0 {
			t.Errorf("#%d, handed over by a writer's command, isn't among the factory's issues", n)
		}
	}
	for _, n := range []int{951, 952} {
		if snap.issue(n) >= 0 {
			t.Errorf("#%d, which no writer handed over, is among the factory's issues", n)
		}
	}
	if i := snap.issue(946); i >= 0 && (snap.Issues[i].Stage != StageQueued || snap.Issues[i].Doing != "formalizing" || snap.Issues[i].Waits == "") {
		t.Errorf("#946 is %+v; want it queued, with the draft that waits for Codex's turn", snap.Issues[i])
	}
	if i := snap.issue(947); i >= 0 && (snap.Issues[i].Stage != StageQueued || snap.Issues[i].Doing != "" || snap.Issues[i].Waits != "") {
		t.Errorf("#947 is %+v; want it queued, since its draft's agent hasn't started", snap.Issues[i])
	}
	said := map[int]string{}
	for _, e := range snap.Activity {
		if e.Who == WhoPerson && e.Kind != "opened" {
			said[e.Issue] = e.Text
		}
	}
	if said[946] != "asked the factory to take it" || said[947] != "asked the factory to plan it" || said[950] != "asked the factory to take it" {
		t.Errorf("the activity shows people's commands as %v; want the three that handed issues over", said)
	}
	var untaken []int
	for _, is := range s.Repos[0].src.untaken {
		untaken = append(untaken, is.Number)
	}
	if fmt.Sprint(untaken) != "[951]" {
		t.Errorf("/act offers %v to hand over; want only #951, a writer's issue nobody handed over", untaken)
	}
}

// A plan's issue says what it waits on: its open step and that step's
// state, not that it's queued, and the headline doesn't take it for an
// issue the factory is about to pick up (#948).
func TestAPlanWaitsOnItsOpenStep948(t *testing.T) {
	steps := make([]formalize.PlannedIssue, 10)
	for i := range steps {
		steps[i] = formalize.PlannedIssue{Title: fmt.Sprintf("Step %d", i+1)}
	}
	plan := &formalize.Proposal{IssuePlan: &formalize.IssuePlan{Name: "Reader statecharts", Issues: steps}}
	at := "2026-11-19T11:50:00Z"
	s := &Server{Cache: t.TempDir(), Log: t.Logf, Branch: "main", Repos: []*Repo{{Name: "gitdek/invariant", FactoryLogin: bot948.Login, src: sources{
		issues: []github.Issue{
			{Number: 571, Title: "Show a reader's statechart in every modeled proposal", State: "open", Body: "/invariant plan", User: person948, AuthorAssociation: "OWNER", CreatedAt: "2026-11-04T21:00:00Z"},
			{Number: 669, Title: "Draw each known bug as a numbered TLC path", State: "open", Body: "/invariant solve", User: bot948, CreatedAt: "2026-11-06T14:19:36Z",
				Labels: []github.Label{{Name: factory.LabelTrigger}, {Name: factory.LabelHumanReview}}},
		},
		comments: map[int]cachedComments{
			571: {comments: []github.Comment{
				say948(t, 1, "2026-11-04T22:04:09Z", factory.Marker{Kind: factory.KindRatified, Proposal: plan}),
				say948(t, 2, "2026-11-06T06:15:57Z", factory.Marker{Kind: factory.KindPlanOpened, Step: 8, Opened: 648}),
				say948(t, 3, at, factory.Marker{Kind: factory.KindPlanOpened, Step: 9, Opened: 669}),
			}},
			669: {comments: []github.Comment{
				say948(t, 4, "2026-11-06T15:00:00Z", factory.Marker{Kind: factory.KindRatified, Project: "plumbing/669"}),
				say948(t, 5, "2026-11-06T18:00:00Z", factory.Marker{Kind: factory.KindFailed, Project: "plumbing/669", Failure: factory.FailGate, PR: 700}),
			}},
		},
	}}}}
	snap := read948(t, s.assemble(time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)))
	i := snap.issue(571)
	if i < 0 {
		t.Fatal("#571 isn't among the issues")
	}
	p := snap.Issues[i].Plan
	if p == nil || p.Step != 9 || p.Steps != 10 || p.Issue != 669 || p.Stage != StageReview || p.Waiting != factory.KindFailed {
		t.Errorf("#571's plan reads %+v; want step 9 of 10, #669, which needs a person", p)
	}
	if strings.Contains(snap.Now.Headline, "#571") {
		t.Errorf("the headline %q takes the plan's issue for one the factory is picking up", snap.Now.Headline)
	}
	opened := false
	for _, e := range snap.Activity {
		opened = opened || e.Issue == 571 && e.Text == "opened #669 for step 9 of its plan"
	}
	if !opened {
		t.Error("the activity doesn't say which issue the plan opened for its step")
	}
}
