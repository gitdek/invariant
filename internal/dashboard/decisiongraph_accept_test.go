package dashboard

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/decisions"
	"github.com/gitdek/invariant/internal/github"
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

// acceptSorted is a graph as JSON, with its edges and each decision's
// dependents sorted, so two graphs compare whatever order each was built in.
func acceptSorted(t *testing.T, g *DecisionGraph) string {
	t.Helper()
	b, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	var c DecisionGraph
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	sort.Slice(c.Edges, func(i, j int) bool { return fmt.Sprint(c.Edges[i]) < fmt.Sprint(c.Edges[j]) })
	for i := range c.Nodes {
		sort.Strings(c.Nodes[i].Dependents)
	}
	if b, err = json.Marshal(c); err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// acceptBlob is a file's blob SHA, as git and GitHub's trees name it.
func acceptBlob(b []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(b))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

// acceptGH is a gh that answers the dashboard's reads of a repository from
// a directory: head names the branch's newest commit, <sha>/tree.json is that
// commit's tree, and <sha>/<path> is each file at it. It logs every path it's
// asked for to calls.
const acceptGH = `#!/bin/sh
d='@DIR@'
for last; do :; done
echo "$last" >> "$d/calls"
head=$(cat "$d/head")
missing() { echo "gh: Not Found (HTTP 404)" >&2; exit 1; }
case "$last" in
*/issues\?*) echo '[]' ;;
*/commits\?*) printf '[{"sha":"%s","commit":{"message":"Record decisions","author":{"date":"2026-09-29T00:00:00Z"},"committer":{"date":"2026-09-29T00:00:00Z"}},"author":{"login":"gitdek"}}]\n' "$head" ;;
*/actions/runs\?*) echo '{"workflow_runs":[]}' ;;
*/git/trees/*)
	ref=${last#*/git/trees/}
	ref=${ref%%\?*}
	if [ "$ref" = main ]; then ref=$head; fi
	if [ -f "$d/$ref/tree.json" ]; then cat "$d/$ref/tree.json"; else missing; fi ;;
*/contents/*)
	p=${last#*/contents/}
	ref=${p#*\?ref=}
	p=${p%%\?*}
	if [ "$ref" = main ]; then ref=$head; fi
	if [ -f "$d/$ref/$p" ]; then cat "$d/$ref/$p"; else missing; fi ;;
*) missing ;;
esac
`

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

// The server reads the journal through GitHub, at the branch's newest
// commit, and the page's graph is the one built from it. When the branch
// moves, the server reads again only the journal files that changed or were
// added, and the graph follows.
func TestTheServerReadsAJournalFileAgainOnlyWhenItChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	dir := t.TempDir()
	repo := decisions.Repo{Name: "r", Dir: filepath.Join(dir, "r")}
	if err := os.MkdirAll(repo.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := decisions.Open(filepath.Join(dir, "decisions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, d := range []decisions.NewDecision{
		{Door: "two-way", Status: "decided", Who: "agent", Text: "The store is SQLite."},
		{Door: "two-way", Status: "proposed", Who: "agent", Text: "Journal every write.", Edges: []decisions.Edge{{Type: decisions.Refines, To: "D-0001"}}},
		{Door: "two-way", Status: "decided", Who: "agent", Text: "Check the journal in CI, as D-0002 says."},
	} {
		if _, err := store.Decide(repo, d, "agent"); err != nil {
			t.Fatal(err)
		}
	}
	first := acceptFiles(t, repo)
	if err := store.Ratify(repo, "D-0002", "@gitdek"); err != nil {
		t.Fatal(err)
	}
	reopen := decisions.NewDecision{Door: "one-way", Status: "proposed", Who: "agent", Text: "Reconsider SQLite.", Record: "reconsider-sqlite", Edges: []decisions.Edge{{Type: decisions.Reopens, To: "D-0001"}}}
	if _, err := store.Decide(repo, reopen, "agent"); err != nil {
		t.Fatal(err)
	}
	second := acceptFiles(t, repo)
	for _, name := range []string{"D-0001.jsonl", "D-0003.jsonl"} {
		if string(first[name]) != string(second[name]) {
			t.Fatalf("%s changed; the fixture needs it unchanged", name)
		}
	}

	fake, err := os.MkdirTemp("", "invariant-fake-gh-")
	if err != nil {
		t.Fatal(err)
	}
	const shaA, shaB = "1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222"
	commit := func(sha string, journal map[string][]byte) {
		files := map[string][]byte{"README.md": []byte("# r\n"), "decisions/log.md": []byte("# Decision log\n")}
		for name, b := range journal {
			files["decisions/journal/"+name] = b
		}
		tree := []github.TreeEntry{{Path: "decisions", Type: "tree", SHA: acceptBlob([]byte(sha + "/decisions"))}, {Path: "decisions/journal", Type: "tree", SHA: acceptBlob([]byte(sha + "/decisions/journal"))}}
		for p, b := range files {
			full := filepath.Join(fake, sha, filepath.FromSlash(p))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, b, 0o644); err != nil {
				t.Fatal(err)
			}
			tree = append(tree, github.TreeEntry{Path: p, Type: "blob", SHA: acceptBlob(b), Size: int64(len(b))})
		}
		sort.Slice(tree, func(i, j int) bool { return tree[i].Path < tree[j].Path })
		js, err := json.Marshal(map[string]any{"sha": sha, "tree": tree, "truncated": false})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fake, sha, "tree.json"), js, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit(shaA, first)
	commit(shaB, second)
	head := func(sha string) {
		tmp := filepath.Join(fake, "head.tmp")
		if err := os.WriteFile(tmp, []byte(sha), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, filepath.Join(fake, "head")); err != nil {
			t.Fatal(err)
		}
	}
	head(shaA)
	gh := filepath.Join(fake, "gh")
	if err := os.WriteFile(gh, []byte(strings.ReplaceAll(acceptGH, "@DIR@", fake)), 0o755); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var logged []string
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, fmt.Sprintf(format, args...))
	}
	s := &Server{Repos: []*Repo{{Name: "o/r", GitHub: github.Client{Repo: "o/r", GH: gh}}}, Branch: "main", Every: 50 * time.Millisecond, Log: logf}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		time.Sleep(300 * time.Millisecond)
		os.RemoveAll(fake)
	})
	s.Start(ctx)

	// shows waits for the page to show n decisions at a commit, and returns
	// its graph.
	shows := func(sha string, n int) *DecisionGraph {
		deadline := time.Now().Add(time.Minute)
		for {
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/state.json", nil))
			var snap Snapshot
			if rec.Code == http.StatusOK {
				if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
					t.Fatal(err)
				}
			}
			if g := snap.Decisions.Graph; snap.Main != nil && snap.Main.SHA == sha && g != nil && len(g.Nodes) == n {
				return g
			}
			if time.Now().After(deadline) {
				mu.Lock()
				defer mu.Unlock()
				t.Fatalf("the page never showed %d decisions at %s; the server logged:\n%s", n, sha[:7], strings.Join(logged, "\n"))
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	// reads is every journal file the server read from the from-th call to gh
	// on, and how many calls there have been.
	reads := func(from int) ([]string, int) {
		b, err := os.ReadFile(filepath.Join(fake, "calls"))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
		var files []string
		for _, l := range lines[from:] {
			if _, file, ok := strings.Cut(l, "/contents/decisions/journal/"); ok {
				files = append(files, file)
			}
		}
		sort.Strings(files)
		return files, len(lines)
	}
	built := func(journal map[string][]byte) string {
		g, err := BuildDecisionGraph("r", journal)
		if err != nil {
			t.Fatal(err)
		}
		return acceptSorted(t, g)
	}

	if got, want := acceptSorted(t, shows(shaA, 3)), built(first); got != want {
		t.Errorf("at %s the page's graph is\n%s\nand the journal's is\n%s", shaA[:7], got, want)
	}
	read, calls := reads(0)
	if want := []string{"D-0001.jsonl?ref=" + shaA, "D-0002.jsonl?ref=" + shaA, "D-0003.jsonl?ref=" + shaA}; strings.Join(read, " ") != strings.Join(want, " ") {
		t.Errorf("the server read the journal files %v; want each once, at %s: %v", read, shaA[:7], want)
	}
	head(shaB)
	if got, want := acceptSorted(t, shows(shaB, 4)), built(second); got != want {
		t.Errorf("at %s the page's graph is\n%s\nand the journal's is\n%s", shaB[:7], got, want)
	}
	if read, _ = reads(calls); strings.Join(read, " ") != "D-0002.jsonl?ref="+shaB+" D-0004.jsonl?ref="+shaB {
		t.Errorf("after the branch moved to %s, the server read %v; want only the changed D-0002.jsonl and the new D-0004.jsonl, once each, at that commit", shaB[:7], read)
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
