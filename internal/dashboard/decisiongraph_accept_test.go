package dashboard

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/decisions"
)

// acceptJournal is a project's checkout whose journal internal/decisions
// wrote: a chain of refinements, a citation read from a decision's words, a
// reopening, a supersession and a link, with every status but open.
func acceptJournal(t *testing.T) decisions.Repo {
	t.Helper()
	dir := t.TempDir()
	repo := decisions.Repo{Name: "demo", Dir: filepath.Join(dir, "demo")}
	if err := os.MkdirAll(repo.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := decisions.Open(filepath.Join(dir, "decisions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, d := range []decisions.NewDecision{
		{Door: "two-way", Status: "decided", Text: "The store is SQLite. It keeps the whole graph in one file."},
		{Door: "two-way", Status: "proposed", Text: "Journal every write.", Edges: []decisions.Edge{{Type: decisions.Refines, To: "D-0001"}}},
		{Door: "two-way", Status: "decided", Text: "Check the journal in CI.", Edges: []decisions.Edge{{Type: decisions.Refines, To: "D-0002"}}},
		{Door: "two-way", Status: "decided", Text: "Keep one journal file, as D-0003 checks it."},
		{Door: "one-way", Status: "proposed", Text: "Reconsider SQLite for the graph.", Record: "reconsider-sqlite", Edges: []decisions.Edge{{Type: decisions.Reopens, To: "D-0001"}}},
		{Door: "two-way", Status: "decided", Text: "One journal file per decision, in place of D-0004. Each file is its own chain.", Edges: []decisions.Edge{{Type: decisions.Supersedes, To: "D-0004"}}},
	} {
		d.Who = "agent"
		if _, err := s.Decide(repo, d, "agent"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Supersede(repo, "D-0004", "D-0006", "agent"); err != nil {
		t.Fatal(err)
	}
	if err := s.Ratify(repo, "D-0002", "@gitdek"); err != nil {
		t.Fatal(err)
	}
	if err := s.Link(repo, "D-0006", decisions.Edge{Type: decisions.Cites, To: "D-0002"}, "agent"); err != nil {
		t.Fatal(err)
	}
	return repo
}

// acceptFiles reads a checkout's journal as the server gets it from GitHub:
// each file's bytes, by its name.
func acceptFiles(t *testing.T, repo decisions.Repo) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(repo.JournalDir())
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(repo.JournalDir(), e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = b
	}
	return files
}

// acceptRebuilt is a store of the test's own, rebuilt from the checkout
// alone: what internal/decisions finds in its journal.
func acceptRebuilt(t *testing.T, repo decisions.Repo) *decisions.Store {
	t.Helper()
	s, err := decisions.Open(filepath.Join(t.TempDir(), "rebuilt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Rebuild([]decisions.Repo{repo}); err != nil {
		t.Fatal(err)
	}
	return s
}

// The graph the page draws from a journal has every decision in it, oldest
// first, as internal/decisions reads it, and every edge internal/decisions
// finds between them, with its kind.
func TestTheDecisionGraphHasEveryDecisionAndEdge(t *testing.T) {
	repo := acceptJournal(t)
	g, err := BuildDecisionGraph(repo.Name, acceptFiles(t, repo))
	if err != nil {
		t.Fatal(err)
	}
	s := acceptRebuilt(t, repo)
	want, err := s.Decisions(repo.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != len(want) {
		t.Fatalf("the graph has %d decisions; the journal has %d", len(g.Nodes), len(want))
	}
	statuses := map[string]bool{}
	var wantEdges, gotEdges []string
	for i, w := range want {
		n := g.Nodes[i]
		short := strings.TrimPrefix(w.ID, repo.Name+"/")
		if n.ID != short || n.Date != w.Date || n.Door != w.Door || n.Status != w.Status || n.Who != w.Who || n.Text != w.Text {
			t.Errorf("decision %d on the graph is %+v; internal/decisions has %+v", i, n, w)
		}
		statuses[n.Status] = true
		near, err := s.Neighbors(w.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, nb := range near {
			if nb.Out && nb.Node.Kind == "decision" {
				wantEdges = append(wantEdges, short+" "+nb.Type+" "+strings.TrimPrefix(nb.Node.ID, repo.Name+"/"))
			}
		}
	}
	for _, st := range []string{"ratified", "proposed", "decided", "superseded"} {
		if !statuses[st] {
			t.Errorf("no %s decision on the graph; the journal has one", st)
		}
	}
	says := map[string]string{"D-0001": "The store is SQLite.", "D-0003": "Check the journal in CI.", "D-0006": "One journal file per decision, in place of D-0004."}
	for _, n := range g.Nodes {
		if w, ok := says[n.ID]; ok && n.Says != w {
			t.Errorf("%s's first sentence is %q; want %q", n.ID, n.Says, w)
		}
	}
	kinds := map[string]bool{}
	for _, e := range g.Edges {
		gotEdges = append(gotEdges, e.From+" "+e.Kind+" "+e.To)
		kinds[e.Kind] = true
	}
	sort.Strings(wantEdges)
	sort.Strings(gotEdges)
	if strings.Join(gotEdges, "\n") != strings.Join(wantEdges, "\n") {
		t.Errorf("the graph's edges are\n%s\nand internal/decisions finds\n%s", strings.Join(gotEdges, "\n"), strings.Join(wantEdges, "\n"))
	}
	for _, k := range []string{decisions.Refines, decisions.Cites, decisions.Supersedes, decisions.Reopens} {
		if !kinds[k] {
			t.Errorf("no %s edge on the graph; the journal has one", k)
		}
	}
}

// Choosing a decision lights up what rests on it: exactly what
// `invariant decisions dependents` answers for it.
func TestTheDecisionGraphsDependentsAreWhatTheStoreAnswers(t *testing.T) {
	repo := acceptJournal(t)
	g, err := BuildDecisionGraph(repo.Name, acceptFiles(t, repo))
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]DecisionNode{}
	for _, n := range g.Nodes {
		nodes[n.ID] = n
	}
	s := acceptRebuilt(t, repo)
	all, err := s.Decisions(repo.Name)
	if err != nil {
		t.Fatal(err)
	}
	most := 0
	for _, d := range all {
		short := strings.TrimPrefix(d.ID, repo.Name+"/")
		n, ok := nodes[short]
		if !ok {
			t.Errorf("%s isn't on the graph", short)
			continue
		}
		deps, err := s.Dependents(d.ID)
		if err != nil {
			t.Fatal(err)
		}
		var want []string
		for _, dep := range deps {
			want = append(want, strings.TrimPrefix(dep.ID, repo.Name+"/"))
		}
		got := append([]string(nil), n.Dependents...)
		sort.Strings(want)
		sort.Strings(got)
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("choosing %s lights up [%s]; `invariant decisions dependents` lists [%s]", short, strings.Join(got, " "), strings.Join(want, " "))
		}
		most = max(most, len(want))
	}
	if most < 3 {
		t.Errorf("the most dependents any decision has is %d; the fixture gives D-0001 five", most)
	}
}

// The page reads every field of the decision graph the server sends it.
func TestThePageReadsEveryDecisionGraphField(t *testing.T) {
	js, err := web.ReadFile("web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []any{DecisionGraph{}, DecisionNode{}, DecisionEdge{}} {
		rt := reflect.TypeOf(v)
		for i := 0; i < rt.NumField(); i++ {
			tag := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
			if tag != "" && tag != "-" && !strings.Contains(string(js), "."+tag) {
				t.Errorf("the page never reads %s.%s", rt.Name(), tag)
			}
		}
	}
}
