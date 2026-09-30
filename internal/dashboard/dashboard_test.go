package dashboard

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tlc"
)

// A state graph as TLC 2.19 writes it with -dump dot,actionlabels.
const dump = `strict digraph DiskGraph {
nodesep=0.35;
subgraph cluster_graph {
color="white";
-39061 [label="/\\ buf = <<>>\n/\\ n = 0",style = filled]
-39061 -> 1090 [label="Write",color="black",fontcolor="black"];
1090 [label="/\\ buf = <<\"a\">>\n/\\ n = 1"];
1090 -> 2207 [label="Write",color="black",fontcolor="black"];
2207 [label="/\\ buf = <<\"a\", \"b\">>\n/\\ n = 2"];
2207 -> 2207 [label="Stay",color="black",fontcolor="black"];
1090 -> -39061 [label="Ship",color="black",fontcolor="black"];
}
}`

func TestParseDot(t *testing.T) {
	g, err := ParseDot(dump)
	if err != nil {
		t.Fatal(err)
	}
	if g.States != 3 || len(g.Init) != 1 || g.Init[0] != 0 {
		t.Fatalf("states %d, init %v; want 3 states, init [0]", g.States, g.Init)
	}
	if got := strings.Join(g.Actions, ","); got != "Write,Stay,Ship" {
		t.Errorf("actions %s", got)
	}
	if len(g.Edges) != 4*3 {
		t.Errorf("%d edge ints, want 12", len(g.Edges))
	}
	if g.vars[1]["buf"] != `<<"a">>` || g.vars[2]["n"] != "2" {
		t.Errorf("vars %v", g.vars)
	}
	if !strings.Contains(g.Labels[2], `<<"a", "b">>`) {
		t.Errorf("label %q", g.Labels[2])
	}
}

// A known bug's counterexample runs on the model's states until the bug
// acts; the state it breaks the invariant in isn't in the graph.
func TestTraceFindsTheCounterexample(t *testing.T) {
	g, err := ParseDot(dump)
	if err != nil {
		t.Fatal(err)
	}
	st := func(buf, n, action string) tlc.TraceState {
		return tlc.TraceState{Action: action, TLA: map[string]string{"buf": buf, "n": n}}
	}
	trace := tlc.TraceFile{States: []tlc.TraceState{
		st("<<>>", "0", "Initial predicate"),
		st(`<<"a">>`, "1", "Write"),
		st(`<<"a",   "b">>`, "2", "Write"), // TLC may wrap and space values differently
		st(`<<"a", "b", "c">>`, "3", "WriteOverCapacity"),
	}}
	path, action, escape := g.Trace(trace)
	if fmt.Sprint(path) != "[0 1 2]" || action != "WriteOverCapacity" {
		t.Fatalf("path %v, action %q", path, action)
	}
	if !strings.Contains(escape, `n = 3`) {
		t.Errorf("escape %q", escape)
	}
}

func marker(t *testing.T, m factory.Marker) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return "◉ **Invariant** · update\n\n<!-- invariant:" + base64.StdEncoding.EncodeToString(b) + " -->\n"
}

func TestLaneSplitsTheFactorysTimeFromPeoples(t *testing.T) {
	at := func(min int) string {
		return time.Date(2026, 9, 25, 18, 0, 0, 0, time.UTC).Add(time.Duration(min) * time.Minute).Format(time.RFC3339)
	}
	bot := github.User{Login: "invariant-code-factory[bot]", Type: "Bot"}
	me := github.User{Login: "gitdek", Type: "User"}
	proposal := &formalize.Proposal{Draft: formalize.Draft{Statements: make([]project.Statement, 12), Language: "typescript"}}
	issue := github.Issue{Number: 3, Title: "Add a rate limiter", State: "closed", User: me, CreatedAt: at(0), ClosedAt: at(40),
		Labels: []github.Label{{Name: factory.LabelTrigger}, {Name: factory.LabelMerged}}}
	comments := []github.Comment{
		{User: bot, CreatedAt: at(1), Body: marker(t, factory.Marker{Kind: factory.KindForks, Forks: make([]formalize.Fork, 2)})},
		{User: me, CreatedAt: at(5), Body: "/invariant choose F1 B\n/invariant choose F2 A"},
		{User: bot, CreatedAt: at(7), Body: marker(t, factory.Marker{Kind: factory.KindProposal, Proposal: proposal})},
		{User: me, CreatedAt: at(20), Body: "Looks right.\n\n`/invariant ratify 645f2d6b79c8`"},
		{User: bot, CreatedAt: at(21), Body: marker(t, factory.Marker{Kind: factory.KindRatified, Project: "examples/04-api-rate-limiter"})},
		{User: bot, CreatedAt: at(23), Body: marker(t, factory.Marker{Kind: factory.KindPR, PR: 4})},
		{User: me, CreatedAt: at(30), Body: "Nice work."},
		{User: bot, CreatedAt: at(33), Body: marker(t, factory.Marker{Kind: factory.KindMerged, PR: 4})},
		{User: github.User{Login: "someone-else[bot]", Type: "Bot"}, CreatedAt: at(34), Body: "/invariant retry"},
	}
	l := Lane(issue, comments, bot.Login, time.Now())
	if l.Stage != StageMerged || l.PR != 4 || l.Project != "examples/04-api-rate-limiter" || l.Language != "typescript" {
		t.Fatalf("stage %s, pr %d, project %q, language %q", l.Stage, l.PR, l.Project, l.Language)
	}
	if l.PeopleComments != 2 || l.Answers != 2 || l.Questions != 2 || l.Statements != 12 {
		t.Errorf("comments %d, answers %d, questions %d, statements %d", l.PeopleComments, l.Answers, l.Questions, l.Statements)
	}
	// People: 1→5 and 7→20. CI: 23→33. The factory: the rest.
	if l.PeopleSeconds != 17*60 || l.FactorySeconds != 16*60 {
		t.Errorf("people %ds, factory %ds; want 1020s and 960s", l.PeopleSeconds, l.FactorySeconds)
	}
	var who []string
	for _, s := range l.Spans {
		who = append(who, s.Who)
	}
	if got := strings.Join(who, " "); got != "factory people factory people factory factory ci" {
		t.Errorf("spans %s", got)
	}
	if l.Events[4].Text != "ratified 645f2d6b79c8" || l.Events[2].Text != "chose F1 B, F2 A" {
		t.Errorf("events %+v", l.Events)
	}
}

func TestAnOpenIssueIsWaitingOnSomeone(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	issue := github.Issue{Number: 7, Title: "Refuse duplicates", State: "open", CreatedAt: now.Add(-time.Hour).Format(time.RFC3339),
		Labels: []github.Label{{Name: factory.LabelTrigger}, {Name: factory.LabelProposal}}}
	comments := []github.Comment{{User: github.User{Login: "gitdek"}, CreatedAt: now.Add(-50 * time.Minute).Format(time.RFC3339),
		Body: marker(t, factory.Marker{Kind: factory.KindProposal})}}
	l := Lane(issue, comments, "", now)
	last := l.Spans[len(l.Spans)-1]
	if l.Stage != StageRatifying || !last.Open || last.Who != WhoPeople || l.PeopleSeconds != 50*60 {
		t.Fatalf("stage %s, last span %+v, people %ds", l.Stage, last, l.PeopleSeconds)
	}
	// Waiting on a person is Needs you's to show, not the headline's
	// (D-0098).
	n := nowLine([]namedWatcher{{repo: "gitdek/invariant", w: Watcher{Running: true}}}, []Issue{l}, "gitdek/invariant")
	if n.Stage != "idle" || n.Headline != "Idle, and watching for issues" {
		t.Errorf("now %+v", n)
	}
	held := Issue{Repo: "gitdek/copythis-ad", Number: 36, Title: "Check retries", Open: true, Stage: StageReview}
	if n := nowLine([]namedWatcher{{repo: "gitdek/invariant", w: Watcher{Running: true}}}, []Issue{held}, "gitdek/invariant"); n.Stage != "idle" {
		t.Errorf("an issue that needs a person took the headline: %+v", n)
	}
	n = nowLine([]namedWatcher{{repo: "gitdek/invariant", w: Watcher{Running: true, Issue: 7, Doing: "building"}}}, []Issue{l}, "gitdek/invariant")
	if n.Headline != "Writing the code for #7" || n.Detail != "Refuse duplicates" || n.Running != 1 {
		t.Errorf("now %+v", n)
	}
	if n := nowLine(nil, nil, "gitdek/invariant"); n.Stage != "idle" || !strings.Contains(n.Headline, "switched off") {
		t.Errorf("now %+v", n)
	}
}

// With several steps doing something, in one repository or more, the
// headline names each one, grouped by what each is doing: the groups in the
// order of their longest-running step, and each group's issues
// longest-running first. It counts them, and no one issue's title is its
// detail. With one step, it's that step's headline, with its issue's title.
func TestTheHeadlineNamesEveryStep(t *testing.T) {
	at := func(min int) time.Time { return time.Date(2026, 9, 29, 10, min, 0, 0, time.UTC) }
	issues := []Issue{{Repo: "gitdek/invariant", Number: 148, Title: "Show every step", Open: true, Stage: StageBuilding}}
	ws := []namedWatcher{
		{repo: "gitdek/invariant", w: Watcher{Running: true, Steps: []Step{
			{Issue: 150, Doing: "building", Since: at(2)}, {Issue: 148, Doing: "building", Since: at(0)}, {Issue: 152, Doing: "formalizing", Since: at(5)},
			{Issue: 154, Doing: "ratifying", Since: at(8)}, {Issue: 156, Doing: "building", Since: at(9)}}}},
		{repo: "gitdek/copythis-ad", w: Watcher{Running: true, Steps: []Step{{Issue: 3, Doing: "answering", Since: at(1)}}}},
		{repo: "gitdek/stopped", w: Watcher{Steps: []Step{{Issue: 4, Doing: "building", Since: at(0)}}}},
	}
	n := nowLine(ws, issues, "gitdek/invariant")
	want := "Building #148, #150 and #156 · answering copythis-ad#3 · drafting #152 · committing the ratification on #154"
	if n.Headline != want || n.Running != 6 || n.Detail != "" || n.WaitingOn != WhoFactory || n.Since == nil || !n.Since.Equal(at(0)) {
		t.Errorf("now %+v; want %q, 6 steps running, no detail, and the factory at work since 10:00", n, want)
	}

	since := at(0)
	one := []namedWatcher{{repo: "gitdek/invariant", w: Watcher{Running: true, Issue: 148, Doing: "building", Since: &since,
		Steps: []Step{{Issue: 148, Doing: "building", Since: since}}}}}
	if n := nowLine(one, issues, "gitdek/invariant"); n.Headline != "Writing the code for #148" || n.Detail != "Show every step" || n.Running != 1 {
		t.Errorf("one step: now %+v", n)
	}

	// The snapshot's watcher lists the steps it's running that are doing
	// something, longest-running first.
	work := t.TempDir()
	path := StatusPath(work, "gitdek/invariant")
	st := Status{PID: os.Getpid(), Repo: "gitdek/invariant", Started: since, Heartbeat: since, Issue: 148, Doing: "building", Since: &since,
		Steps: []Step{{Issue: 150, Doing: "building", Since: at(2)}, {Issue: 155, Since: at(3)}, {Issue: 148, Doing: "building", Since: since}}}
	if err := WriteStatus(path, st); err != nil {
		t.Fatal(err)
	}
	s := &Server{Work: work, Repos: []*Repo{{Name: "gitdek/invariant", Status: path}}}
	snap := s.assemble(at(10))
	var steps []string
	for _, step := range snap.Repos[0].Factory.Steps {
		steps = append(steps, fmt.Sprintf("#%d %s", step.Issue, step.Doing))
	}
	if got := strings.Join(steps, ", "); got != "#148 building, #150 building" || snap.Now.Headline != "Building #148 and #150" || snap.Now.Running != 2 {
		t.Errorf("the snapshot's watcher lists [%s], headlined %q with %d running; want [#148 building, #150 building], %q with 2",
			got, snap.Now.Headline, snap.Now.Running, "Building #148 and #150")
	}
}

func TestParseLog(t *testing.T) {
	log := "| ID | Date | Door | Status | Decision | Who |\n| :-- | :-- | :-- | :-- | :-- | :-- |\n" +
		"| D-0000 | 2026-09-25 | — | ratified | The [kickoff brief](sources/brief.md) is the baseline. | @gitdek |\n" +
		"| [D-0045](D-0045-slice-6-plan.md) | 2026-09-25 | two-way | ratified | Slice 6 plan, with **bold** and `code`. | @gitdek |\n" +
		"| D-0048 | 2026-09-26 | two-way | proposed | Product requirements. | Claude |\n"
	d := ParseLog(log)
	if len(d) != 3 || d[1].ID != "D-0045" || d[1].Text != "Slice 6 plan, with bold and code." || d[0].Text != "The kickoff brief is the baseline." {
		t.Fatalf("%+v", d)
	}
	if d[2].Status != "proposed" || d[2].Who != "Claude" {
		t.Errorf("%+v", d[2])
	}
}

func TestParseSlices(t *testing.T) {
	readme := "- [x] **Slice 1 · The gate.** TLC, Gobra, and receipts.\n" +
		"- [x] **Slice 3b · Nagini spike.** Nagini proves a core.\n" +
		"- [ ] **Slice 6 · Changing existing projects.** Amendments work. Next, more.\n"
	prd := "## Roadmap\n\n| Slice | What | Status |\n| :-- | :-- | :-- |\n" +
		"| 1 to 5 | Everything so far | Done |\n| 6 | Amendments (4.1, 4.2) | Part A done |\n" +
		"| 7 | Code you can ship (3.7) | Proposed |\n" +
		"| 8 | The factory survives crashes and concurrent work, proved, which brings liveness (3.8, 4.3) | Proposed |\n" +
		"| 9 | Ready to go public: GitHub enforces the gate (1.9) | Proposed. The switch itself is ratified. `D-0007` |\n" +
		"| Later | Codex, type checking | Later |\n\n## Risks\n\n| 10 | Not a slice | Proposed |\n"
	var got []string
	for _, s := range ParseSlices(readme, prd) {
		got = append(got, s.ID+" "+s.Title+" "+s.Status)
	}
	want := []string{
		"1 The gate done", "3b Nagini spike done", "6 Changing existing projects now", "7 Code you can ship proposed",
		"8 The factory survives crashes and concurrent work proposed", "9 Ready to go public proposed", "Later Codex, type checking later",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The server only reads: anything but GET or HEAD is refused, and nothing
// is served that isn't one of the page's own files.
func TestHandlerServesOnlyThePage(t *testing.T) {
	s := &Server{}
	h := s.Handler()
	get := func(method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w
	}
	if w := get("GET", "/api/state.json"); w.Code != http.StatusServiceUnavailable {
		t.Errorf("state before the first read: %d", w.Code)
	}
	if w := get("POST", "/api/state.json"); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", w.Code)
	}
	for _, p := range []string{"/../go.mod", "/web/index.html", "/dashboard.go", "/api/graph/xyz.json", "/api/graph/0123456789abcdef.json"} {
		if w := get("GET", p); w.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d", p, w.Code)
		}
	}
	w := get("GET", "/")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "/app.js?v="+version) || strings.Contains(w.Body.String(), "{{version}}") {
		t.Errorf("page: %d", w.Code)
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP %q", csp)
	}
	if w := get("GET", "/app.js?v="+version); w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("app.js: %d %q", w.Code, w.Header().Get("Cache-Control"))
	}
}

func TestAnotherRepositorysIssuesAreNamed(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	l := Issue{Repo: "gitdek/copythis-ad", Number: 1, Title: "Check leases", Open: true, Stage: StageAsking,
		Spans: []Span{{From: now.Add(-time.Minute), To: now, Who: WhoPeople, Open: true}}}
	l.Stage = StageQueued
	if n := nowLine(nil, []Issue{l}, "gitdek/invariant"); n.Headline != "Picking up copythis-ad#1" || n.Repo != "gitdek/copythis-ad" {
		t.Errorf("now %+v", n)
	}
	w := []namedWatcher{{repo: "gitdek/invariant", w: Watcher{Running: true}}, {repo: "gitdek/copythis-ad", w: Watcher{Running: true, Issue: 1, Doing: "formalizing"}}}
	if n := nowLine(w, []Issue{l}, "gitdek/invariant"); n.Headline != "Drafting what must be true for copythis-ad#1" {
		t.Errorf("now %+v", n)
	}
	if got := combined([]RepoState{{Factory: Watcher{Running: true}}, {Factory: Watcher{Running: true, Issue: 1, Doing: "building"}}}); got.Doing != "building" {
		t.Errorf("the top bar should show the watcher that's working: %+v", got)
	}
	// A page showing one repository has that repository's headline alone.
	by := nowBy(w, []Issue{l}, []RepoState{{Name: "gitdek/invariant"}, {Name: "gitdek/copythis-ad"}}, "gitdek/invariant")
	if by["gitdek/invariant"].Stage != "idle" || by["gitdek/copythis-ad"].Headline != "Drafting what must be true for copythis-ad#1" {
		t.Errorf("nowBy %+v", by)
	}
}

// An open issue says what it needs from a person: the questions still
// open, or the proposal's short hash to ratify.
func TestWaitingSaysWhatAPersonMustDo(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	at := now.Add(-time.Minute).Format(time.RFC3339)
	issue := github.Issue{Number: 33, Title: "Check leases", State: "open", CreatedAt: now.Add(-time.Hour).Format(time.RFC3339),
		Labels: []github.Label{{Name: factory.LabelTrigger}, {Name: factory.LabelAsking}}}
	forks := factory.Marker{Kind: factory.KindForks,
		Forks:   []formalize.Fork{{ID: "F1", Question: "Which rule?", Options: []formalize.Option{{ID: "A", Says: "a"}, {ID: "C", Says: "c"}}}, {ID: "F2", Question: "When?", Options: []formalize.Option{{ID: "A", Says: "now"}}}},
		Answers: []formalize.Answer{{Fork: "F2", Option: "A"}}}
	l := Lane(issue, []github.Comment{{User: github.User{Login: "bot"}, CreatedAt: at, Body: marker(t, forks)}}, "", now)
	if w := l.Waiting; w == nil || w.Kind != factory.KindForks || len(w.Forks) != 1 || w.Forks[0].ID != "F1" || len(w.Forks[0].Options) != 2 {
		t.Fatalf("waiting %+v", l.Waiting)
	}
	proposal := &formalize.Proposal{Draft: formalize.Draft{Name: "leases", Statements: []project.Statement{{Name: "OneLeaseHolder", Kind: "invariant", Says: "One holder."}}},
		Hash: "sha256:6db634c64b0912345678"}
	issue.Labels = []github.Label{{Name: factory.LabelTrigger}, {Name: factory.LabelProposal}}
	l = Lane(issue, []github.Comment{{User: github.User{Login: "bot"}, CreatedAt: at, Body: marker(t, factory.Marker{Kind: factory.KindProposal, Proposal: proposal})}}, "", now)
	if w := l.Waiting; w == nil || w.Hash != "6db634c64b09" || len(w.Statements) != 1 || w.Statements[0].Name != "OneLeaseHolder" || w.Unchanged {
		t.Fatalf("waiting %+v", l.Waiting)
	}
	// An amendment at the hash of the lock it amends changes no statement,
	// so only its code changes (D-0093).
	for _, c := range []struct {
		amends string
		want   bool
	}{{proposal.Hash, true}, {"sha256:0000", false}, {"", false}} {
		proposal.Target = &formalize.Target{Previous: "D-0027", Amends: c.amends}
		l = Lane(issue, []github.Comment{{User: github.User{Login: "bot"}, CreatedAt: at, Body: marker(t, factory.Marker{Kind: factory.KindProposal, Proposal: proposal})}}, "", now)
		if w := l.Waiting; w == nil || w.Unchanged != c.want || w.Amends != "D-0027" {
			t.Errorf("amending %q: waiting %+v", c.amends, l.Waiting)
		}
	}
	// A pull request closed without merging, on an open issue, waits for a
	// person to draft again, and a revise answers it.
	closed := marker(t, factory.Marker{Kind: factory.KindClosed, PR: 37})
	l = Lane(issue, []github.Comment{{User: github.User{Login: "bot"}, CreatedAt: at, Body: closed}}, "", now)
	if w := l.Waiting; w == nil || w.Kind != factory.KindClosed || w.PR != 37 {
		t.Fatalf("waiting %+v", l.Waiting)
	}
	later := now.Add(-30 * time.Second).Format(time.RFC3339)
	l = Lane(issue, []github.Comment{{User: github.User{Login: "bot"}, CreatedAt: at, Body: closed}, {User: github.User{Login: "gitdek"}, CreatedAt: later, Body: "/invariant revise"}}, "", now)
	if l.Waiting != nil {
		t.Errorf("a revise answers a closed pull request: waiting %+v", l.Waiting)
	}
	// A ratification the factory hasn't reached yet, while it builds another
	// issue, is the factory's move: the issue is queued, not waiting on
	// anyone.
	proposal.Target = nil
	l = Lane(issue, []github.Comment{{User: github.User{Login: "bot"}, CreatedAt: at, Body: marker(t, factory.Marker{Kind: factory.KindProposal, Proposal: proposal})},
		{User: github.User{Login: "gitdek"}, CreatedAt: later, Body: "/invariant ratify 6db634c64b09"}}, "", now)
	if l.Waiting != nil || l.Stage != StageQueued {
		t.Errorf("ratified and not yet taken: stage %q, waiting %+v", l.Stage, l.Waiting)
	}
	issue.State = "closed"
	if l = Lane(issue, nil, "", now); l.Waiting != nil {
		t.Error("a closed issue waits on no one")
	}
}

// Once every slice in the README is done, the PRD's next slice is the one
// in progress.
func TestTheNextSliceIsNow(t *testing.T) {
	readme := "- [x] **Slice 6 · Changing existing projects.** Done.\n"
	prd := "## Roadmap\n\n| Slice | What | Status |\n| :-- | :-- | :-- |\n| 7 | Invariant builds itself: the issue protocol (4.1) | Next `D-0058` |\n| 8 | Code you can ship (3.7) | Planned |\n"
	var got []string
	for _, s := range ParseSlices(readme, prd) {
		got = append(got, s.ID+" "+s.Status)
	}
	if strings.Join(got, ",") != "6 done,7 now,8 planned" {
		t.Errorf("slices %v", got)
	}
}

// The hero shows the checks the current step has run, read from the
// watcher's own log for that step.
func TestRunsOfTheCurrentStep(t *testing.T) {
	work := t.TempDir()
	write := func(dir, file, text string) {
		p := filepath.Join(work, "gitdek", "app", "issues", "issue-4", dir)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, file), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("build-20260926-230000", "gate-runs.jsonl", `{"run":1,"passed":false}`+"\n")
	write("build-20260927-010000", "gate-runs.jsonl", `{"run":1,"passed":false,"at":"x"}`+"\n"+`{"run":2,"passed":true}`+"\n")
	write("formalize-20260926-220000", "check-runs.jsonl", `{"run":1,"passed":true}`+"\n")
	runs, kind := runsOf(work, "gitdek/app", Watcher{Running: true, Issue: 4, Doing: "building"})
	if kind != "gate" || len(runs) != 2 || runs[0].Passed || !runs[1].Passed {
		t.Errorf("building: %s %+v; want the newest build's two gate runs", kind, runs)
	}
	runs, kind = runsOf(work, "gitdek/app", Watcher{Running: true, Issue: 4, Doing: "formalizing"})
	if kind != "check" || len(runs) != 1 {
		t.Errorf("formalizing: %s %+v", kind, runs)
	}
	for _, w := range []Watcher{{Running: true, Issue: 4, Doing: "ratifying"}, {Issue: 4, Doing: "building"}, {Running: true, Doing: "building"}} {
		if runs, _ := runsOf(work, "gitdek/app", w); runs != nil {
			t.Errorf("%+v shows runs %+v", w, runs)
		}
	}
}

// The dashboard shows every issue the factory takes, however it was asked.
func TestTheFactorysIssues(t *testing.T) {
	for _, c := range []struct {
		is   github.Issue
		want bool
	}{
		{github.Issue{Labels: []github.Label{{Name: "invariant"}}}, true},
		{github.Issue{Labels: []github.Label{{Name: "invariant:asking"}}}, true},
		{github.Issue{Body: "Check the leases.\n\n/invariant solve\n"}, true},
		{github.Issue{Body: "Mentions `invariant` in passing.", Labels: []github.Label{{Name: "bug"}}}, false},
		{github.Issue{Body: "> /invariant solve\nquoted, so not a command"}, false},
	} {
		if got := factory.Takes(c.is); got != c.want {
			t.Errorf("%+v: %v, want %v", c.is, got, c.want)
		}
	}
}

// Once people give what the factory asked for, the issue no longer needs
// anyone: the next move is the factory's.
func TestAnsweredIssuesNeedNoOne(t *testing.T) {
	now := time.Date(2026, 9, 27, 4, 30, 0, 0, time.UTC)
	asked, later := now.Add(-10*time.Minute).Format(time.RFC3339), now.Add(-time.Minute).Format(time.RFC3339)
	issue := github.Issue{Number: 13, Title: "Add failure steps", State: "open", CreatedAt: now.Add(-time.Hour).Format(time.RFC3339),
		Labels: []github.Label{{Name: factory.LabelAsking}}}
	forks := factory.Marker{Kind: factory.KindForks, Forks: []formalize.Fork{
		{ID: "F1", Question: "What can a writer do?", Options: []formalize.Option{{ID: "A"}, {ID: "C"}}},
		{ID: "F2", Question: "Does retry reset the limit?", Options: []formalize.Option{{ID: "B"}}}}}
	post := github.Comment{User: github.User{Login: "bot"}, CreatedAt: asked, Body: marker(t, forks)}
	person := func(body string) github.Comment {
		return github.Comment{User: github.User{Login: "gitdek", Type: "User"}, CreatedAt: later, Body: body}
	}
	for body, waiting := range map[string]bool{
		"/invariant choose F1 C\n/invariant choose F2 B": false,
		"/invariant choose F1 C":                         true,
		"/invariant revise":                              false,
		"Thinking about it.":                             true,
	} {
		if l := Lane(issue, []github.Comment{post, person(body)}, "", now); (l.Waiting != nil) != waiting {
			t.Errorf("after %q: waiting %+v", body, l.Waiting)
		}
	}
	proposal := factory.Marker{Kind: factory.KindProposal, Proposal: &formalize.Proposal{Hash: "sha256:11bff3f218d7d1da4c62"}}
	post.Body = marker(t, proposal)
	for body, waiting := range map[string]bool{
		"/invariant ratify 11bff3f218d7":         false,
		"/invariant ratify 11bff3f218d7d1da4c62": false,
		"/invariant ratify 000000000000":         true,
	} {
		if l := Lane(issue, []github.Comment{post, person(body)}, "", now); (l.Waiting != nil) != waiting {
			t.Errorf("after %q: waiting %+v", body, l.Waiting)
		}
	}
}

// A huge graph keeps every state's first step from Init, so its rings stay
// exact, and every step of a bug's path, within the page's budget.
func TestCompactKeepsRingsAndBugPaths(t *testing.T) {
	// A complete graph on 400 states: 160,000 steps, over the budget.
	const n = 400
	g := &Graph{States: n, Init: []int{0}, Actions: []string{"Go"}}
	for f := 0; f < n; f++ {
		for to := 0; to < n; to++ {
			g.Edges = append(g.Edges, f, to, 0)
		}
	}
	g.Bugs = []BugPath{{Name: "Bug", Path: []int{0, 399, 7, 398}}}
	g.Compact()
	if g.AllEdges != n*n || len(g.Edges)/3 > maxEdges || len(g.Edges)/3 < maxEdges-n {
		t.Fatalf("kept %d of %d steps", len(g.Edges)/3, g.AllEdges)
	}
	reached := map[int]bool{0: true}
	has := map[[2]int]bool{}
	for e := 0; e < len(g.Edges)/3; e++ {
		f, to := g.Edges[3*e], g.Edges[3*e+1]
		has[[2]int{f, to}] = true
		if f == 0 {
			reached[to] = true
		}
	}
	if len(reached) != n {
		t.Errorf("only %d states keep their first step from Init", len(reached))
	}
	for _, step := range [][2]int{{0, 399}, {399, 7}, {7, 398}} {
		if !has[step] {
			t.Errorf("the bug's step %v was dropped", step)
		}
	}
}

// A failure says why, and a build that stopped can be drafted again.
func TestFailuresSayWhy(t *testing.T) {
	now := time.Date(2026, 9, 27, 8, 0, 0, 0, time.UTC)
	at := now.Add(-time.Minute).Format(time.RFC3339)
	issue := github.Issue{Number: 20, Title: "A pool", State: "open", CreatedAt: now.Add(-time.Hour).Format(time.RFC3339),
		Labels: []github.Label{{Name: factory.LabelHumanReview}}}
	stopped := factory.Marker{Kind: factory.KindFailed, Failure: factory.FailStopped}
	l := Lane(issue, []github.Comment{{User: github.User{Login: "bot"}, CreatedAt: at, Body: marker(t, stopped)}}, "", now)
	if w := l.Waiting; w == nil || w.Kind != factory.KindFailed || w.Failure != factory.FailStopped {
		t.Fatalf("waiting %+v", l.Waiting)
	}
	c := act{Repo: "o/r", Issue: 20, Body: "/invariant revise"}
	l.Repo, l.Open = "o/r", true
	if err := c.check([]Issue{l}, nil); err != nil {
		t.Errorf("a stopped build can be drafted again: %v", err)
	}
	ci := factory.Marker{Kind: factory.KindFailed, Failure: factory.FailCI, PR: 7}
	l = Lane(issue, []github.Comment{{User: github.User{Login: "bot"}, CreatedAt: at, Body: marker(t, ci)}}, "", now)
	l.Repo, l.Open = "o/r", true
	if err := c.check([]Issue{l}, nil); err == nil {
		t.Error("a pull request that failed CI can't be drafted again, only retried")
	}
}

// A gate run GitHub never started, as when the account's Actions minutes
// run out, didn't fail: the gate didn't run at all. Nor did one that a docs
// change skipped, and neither may stand for a gate that passed (D-0083).
func TestAGateThatDidntRunIsNeverAPass(t *testing.T) {
	ran := github.Job{Name: "invariant/gate", RunnerID: 5, Conclusion: "success"}
	ran.Steps = append(ran.Steps, struct {
		Name string `json:"name"`
	}{Name: "Verify every project"})
	changes := github.Job{Name: "What changed", RunnerID: 4, Conclusion: "success"}
	changes.Steps = ran.Steps
	for _, c := range []struct {
		jobs []github.Job
		want string
	}{
		{[]github.Job{ran}, gateRan},
		{[]github.Job{changes, ran}, gateRan},
		{[]github.Job{changes, {Name: "invariant/gate", Conclusion: "skipped"}}, gateSkipped},
		{[]github.Job{{Name: "invariant/gate", Conclusion: "failure"}}, gateRefused},
		{[]github.Job{{Name: "What changed", Conclusion: "failure"}, {Name: "invariant/gate", Conclusion: "failure"}}, gateRefused},
	} {
		if got := gateState(c.jobs); got != c.want {
			t.Errorf("gateState(%+v) = %q; want %q", c.jobs, got, c.want)
		}
	}
	if refused(nil) || !refused([]github.Job{{Name: "invariant/gate"}}) || refused([]github.Job{changes}) {
		t.Error("refused misreads a run's jobs")
	}

	run := github.Run{ID: 9, Name: "gate", HeadSHA: "9121057", Status: "completed", Conclusion: "failure"}
	r := &Repo{Name: "o/r"}
	r.src.gates = map[int64]string{9: gateRefused}
	if got := runWords(run, gateRefused); got != "didn't start" {
		t.Errorf("runWords = %q", got)
	}
	if ref := r.runRef(run); !ref.NotStarted || ref.Skipped {
		t.Errorf("runRef = %+v; want NotStarted", ref)
	}
	r.src.gates[9] = gateRan
	if got := runWords(run, gateRan); got != "failed" {
		t.Errorf("runWords = %q", got)
	}
	if ref := r.runRef(run); ref.NotStarted || ref.Skipped {
		t.Errorf("runRef = %+v; a gate that ran and failed started", ref)
	}
	run.Conclusion = "success"
	r.src.gates[9] = gateSkipped
	if got := runWords(run, gateSkipped); got != "skipped" {
		t.Errorf("runWords = %q; a skipped gate didn't pass", got)
	}
	if ref := r.runRef(run); !ref.Skipped || ref.NotStarted {
		t.Errorf("runRef = %+v; want Skipped", ref)
	}
	run.Conclusion = "cancelled"
	if got := runWords(run, ""); got != "cancelled" {
		t.Errorf("runWords = %q", got)
	}
	run.Conclusion = "timed_out"
	if got := runWords(run, gateRan); got != "timed out" {
		t.Errorf("runWords = %q", got)
	}
}

// A dashboard that restarts serves the last snapshot it kept at once, until
// its first read of GitHub replaces it, and only for the same repository.
func TestARestartServesTheLastSnapshot(t *testing.T) {
	cache := t.TempDir()
	js, err := json.Marshal(Snapshot{Repo: "gitdek/app", Issues: []Issue{{Number: 7, Title: "a buffer"}}})
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzipped(js)
	if err != nil {
		t.Fatal(err)
	}
	(&Server{Cache: cache}).saveSnapshot(gz)

	s := &Server{Cache: cache, Repos: []*Repo{{Name: "gitdek/app"}}}
	s.loadSnapshot()
	if !bytes.Equal(s.state, gz) || len(s.issues) != 1 || s.issues[0].Number != 7 {
		t.Errorf("a restarted dashboard serves %d bytes and issues %+v, want the kept snapshot", len(s.state), s.issues)
	}
	other := &Server{Cache: cache, Repos: []*Repo{{Name: "gitdek/other"}}}
	other.loadSnapshot()
	if other.state != nil {
		t.Error("a dashboard for another repository served the kept snapshot")
	}
}

// A graph the kept snapshot names is served from the cache on disk, before
// the restarted dashboard has read it into memory.
func TestAGraphIsServedFromTheCache(t *testing.T) {
	s := &Server{Cache: t.TempDir()}
	key := "0123456789abcdef"
	gz, err := gzipped([]byte(`{"nodes":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(s.graphFile(key)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.graphFile(key), gz, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{key, "fedcba9876543210"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/graph/"+k+".json", nil))
		if want := map[string]int{key: 200}[k]; (want == 200) != (rec.Code == 200) {
			t.Errorf("graph %s: %d", k, rec.Code)
		}
	}
}
