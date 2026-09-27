package dashboard

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	n := nowLine([]namedWatcher{{repo: "gitdek/invariant"}}, []Issue{l}, "gitdek/invariant")
	if n.Headline != "Waiting for a person to ratify #7" || n.WaitingOn != WhoPeople || n.Since == nil {
		t.Errorf("now %+v", n)
	}
	n = nowLine([]namedWatcher{{repo: "gitdek/invariant", w: Watcher{Running: true, Issue: 7, Doing: "building"}}}, []Issue{l}, "gitdek/invariant")
	if n.Headline != "Writing the code for #7" || n.Detail != "Refuse duplicates" {
		t.Errorf("now %+v", n)
	}
	if n := nowLine(nil, nil, "gitdek/invariant"); n.Stage != "idle" || !strings.Contains(n.Headline, "switched off") {
		t.Errorf("now %+v", n)
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
	if n := nowLine(nil, []Issue{l}, "gitdek/invariant"); n.Headline != "Waiting for answers on copythis-ad#1" || n.Repo != "gitdek/copythis-ad" {
		t.Errorf("now %+v", n)
	}
	w := []namedWatcher{{repo: "gitdek/invariant", w: Watcher{Running: true}}, {repo: "gitdek/copythis-ad", w: Watcher{Running: true, Issue: 1, Doing: "formalizing"}}}
	if n := nowLine(w, []Issue{l}, "gitdek/invariant"); n.Headline != "Drafting what must be true for copythis-ad#1" {
		t.Errorf("now %+v", n)
	}
	if got := combined([]RepoState{{Factory: Watcher{Running: true}}, {Factory: Watcher{Running: true, Issue: 1, Doing: "building"}}}); got.Doing != "building" {
		t.Errorf("the top bar should show the watcher that's working: %+v", got)
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
	if w := l.Waiting; w == nil || w.Hash != "6db634c64b09" || len(w.Statements) != 1 || w.Statements[0].Name != "OneLeaseHolder" {
		t.Fatalf("waiting %+v", l.Waiting)
	}
	issue.State = "closed"
	if l = Lane(issue, nil, "", now); l.Waiting != nil {
		t.Error("a closed issue waits on no one")
	}
}
