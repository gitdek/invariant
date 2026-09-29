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
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/decisions"
	"github.com/gitdek/invariant/internal/github"
)

// queryGH is a gh that answers the dashboard's reads of a repository from a
// directory. head names the branch's newest commit, and each commit's
// directory holds its files, its tree.json for the trees API, and
// journal.json, GitHub's answer to a GraphQL query of decisions/journal at
// that commit, which a file named fail there makes fail. It logs each call to
// calls, as a REST call's path or as graphql and the <commit>:<path> a query
// names, and keeps the last query's arguments in query.
const queryGH = `#!/bin/sh
d='@DIR@'
gql=
for last; do [ "$last" = graphql ] && gql=1; done
tip=$(cat "$d/head")
missing() { echo "gh: Not Found (HTTP 404)" >&2; exit 1; }
if [ -n "$gql" ]; then
	all=$( { printf '%s\n' "$@"; cat; } )
	printf '%s\n' "$all" > "$d/query"
	expr=$(printf '%s\n' "$all" | grep -o '[0-9a-f]\{40\}:[A-Za-z0-9/._-]*' | head -n 1)
	expr=${expr%/}
	echo "graphql $expr" >> "$d/calls"
	sha=${expr%%:*}
	if [ "$expr" != "$sha:decisions/journal" ] || [ ! -f "$d/$sha/journal.json" ]; then
		echo '{"data":{"repository":{"object":null}}}'
	elif [ -f "$d/$sha/fail" ]; then
		echo 'gh: Something went wrong while executing your query. (HTTP 502)' >&2
		exit 1
	else
		cat "$d/$sha/journal.json"
	fi
	exit 0
fi
echo "$last" >> "$d/calls"
case "$last" in
*/issues\?*) echo '[]' ;;
*/commits\?*) printf '[{"sha":"%s","commit":{"message":"Record decisions","author":{"date":"2026-09-29T00:00:00Z"},"committer":{"date":"2026-09-29T00:00:00Z"}},"author":{"login":"gitdek"}}]\n' "$tip" ;;
*/actions/runs\?*) echo '{"workflow_runs":[]}' ;;
*/git/trees/*)
	ref=${last#*/git/trees/}
	ref=${ref%%\?*}
	if [ -f "$d/$ref/tree.json" ]; then cat "$d/$ref/tree.json"; else missing; fi ;;
*/contents/*)
	p=${last#*/contents/}
	ref=${p#*\?ref=}
	p=${p%%\?*}
	if [ -f "$d/$ref/$p" ]; then cat "$d/$ref/$p"; else missing; fi ;;
*) missing ;;
esac
`

// queryBlob is a file's blob ID, as git and GitHub name it.
func queryBlob(b []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(b))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

// queryJournal is a checkout whose journal internal/decisions wrote, with n
// decisions: every even one proposed, and some refining the one before or
// citing an earlier one in their words. Its store writes more to it.
func queryJournal(t *testing.T, n int) (decisions.Repo, *decisions.Store) {
	t.Helper()
	dir := t.TempDir()
	repo := decisions.Repo{Name: "r", Dir: filepath.Join(dir, "r")}
	if err := os.MkdirAll(repo.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := decisions.Open(filepath.Join(dir, "decisions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	for i := 1; i <= n; i++ {
		d := decisions.NewDecision{Door: "two-way", Status: "decided", Who: "agent", Text: fmt.Sprintf("Decision %d stands.", i)}
		if i%2 == 0 {
			d.Status = "proposed"
		}
		switch {
		case i%3 == 2:
			d.Edges = []decisions.Edge{{Type: decisions.Refines, To: fmt.Sprintf("D-%04d", i-1)}}
		case i%3 == 0:
			d.Text = fmt.Sprintf("Decision %d stands, as D-%04d says.", i, i-2)
		}
		if _, err := store.Decide(repo, d, "agent"); err != nil {
			t.Fatal(err)
		}
	}
	return repo, store
}

// queryFiles reads a checkout's journal one file at a time, by name.
func queryFiles(t *testing.T, repo decisions.Repo) map[string][]byte {
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

// queryFake is a directory for a fake GitHub, with queryGH in it as gh. It's
// removed once the server reading it has stopped.
func queryFake(t *testing.T) string {
	t.Helper()
	fake, err := os.MkdirTemp("", "invariant-query-gh-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(fake) })
	if err := os.WriteFile(filepath.Join(fake, "gh"), []byte(strings.ReplaceAll(queryGH, "@DIR@", fake)), 0o755); err != nil {
		t.Fatal(err)
	}
	return fake
}

// queryCommit puts a commit on the fake GitHub, with a README and a journal:
// its files, its tree, and its answer to a query of decisions/journal, which
// lists a file that isn't a decision and a subdirectory too. answer, when
// set, changes a blob's object in that answer, given the blob's name.
func queryCommit(t *testing.T, fake, sha, readme string, journal map[string][]byte, answer func(name string, object map[string]any)) {
	t.Helper()
	files := map[string][]byte{"README.md": []byte(readme), "decisions/log.md": []byte("# Decision log\n"), "decisions/journal/README.md": []byte("One file per decision.\n"), "decisions/journal/old/notes.md": []byte("Notes.\n")}
	for name, b := range journal {
		files["decisions/journal/"+name] = b
	}
	tree := []github.TreeEntry{{Path: "decisions", Type: "tree", SHA: queryBlob([]byte("decisions"))}, {Path: "decisions/journal", Type: "tree", SHA: queryBlob([]byte(sha))}, {Path: "decisions/journal/old", Type: "tree", SHA: queryBlob([]byte("old"))}}
	entries := []map[string]any{{"name": "old", "path": "decisions/journal/old", "type": "tree", "mode": 16384, "oid": queryBlob([]byte("old")), "object": map[string]any{}}}
	for p, b := range files {
		full := filepath.Join(fake, sha, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, b, 0o644); err != nil {
			t.Fatal(err)
		}
		tree = append(tree, github.TreeEntry{Path: p, Type: "blob", SHA: queryBlob(b), Size: int64(len(b))})
		name, ok := strings.CutPrefix(p, "decisions/journal/")
		if !ok || strings.Contains(name, "/") {
			continue
		}
		object := map[string]any{"oid": queryBlob(b), "text": string(b), "byteSize": len(b), "isBinary": false, "isTruncated": false}
		if answer != nil {
			answer(name, object)
		}
		entries = append(entries, map[string]any{"name": name, "path": p, "type": "blob", "mode": 33188, "oid": queryBlob(b), "object": object})
	}
	sort.Slice(tree, func(i, j int) bool { return tree[i].Path < tree[j].Path })
	sort.Slice(entries, func(i, j int) bool { return entries[i]["name"].(string) < entries[j]["name"].(string) })
	queryWrite(t, filepath.Join(fake, sha, "tree.json"), map[string]any{"sha": sha, "tree": tree, "truncated": false})
	queryWrite(t, filepath.Join(fake, sha, "journal.json"), map[string]any{"data": map[string]any{"repository": map[string]any{"object": map[string]any{"entries": entries}}}})
}

// queryWrite writes v to a file as JSON.
func queryWrite(t *testing.T, file string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// queryHead moves the fake's branch to sha.
func queryHead(t *testing.T, fake, sha string) {
	t.Helper()
	tmp := filepath.Join(fake, "head.tmp")
	if err := os.WriteFile(tmp, []byte(sha), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(fake, "head")); err != nil {
		t.Fatal(err)
	}
}

// queryServer starts a dashboard server that reads the fake GitHub, with its
// branch at sha, and returns it with the last few things it logged.
func queryServer(t *testing.T, fake, sha string) (*Server, func() string) {
	t.Helper()
	queryHead(t, fake, sha)
	var mu sync.Mutex
	var logged []string
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, fmt.Sprintf(format, args...))
	}
	gh := github.Client{Repo: "o/r", GH: filepath.Join(fake, "gh")}
	s := &Server{Repos: []*Repo{{Name: "o/r", GitHub: gh}}, Branch: "main", Every: 50 * time.Millisecond, Log: logf}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		time.Sleep(300 * time.Millisecond)
	})
	s.Start(ctx)
	last := func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(logged[max(len(logged)-4, 0):], "\n")
	}
	return s, last
}

// querySnapshot is what the page shows now, or nothing before the server's
// first refresh.
func querySnapshot(t *testing.T, s *Server) Snapshot {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/state.json", nil))
	var snap Snapshot
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
			t.Fatal(err)
		}
	}
	return snap
}

// queryShows waits for the page to show the branch at sha, with what ok
// wants, and returns what it shows.
func queryShows(t *testing.T, s *Server, sha, what string, ok func(Snapshot) bool, logged func() string) Snapshot {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		snap := querySnapshot(t, s)
		if snap.Main != nil && snap.Main.SHA == sha && ok(snap) {
			return snap
		}
		if time.Now().After(deadline) {
			t.Fatalf("the page never showed %s at %s; the server last logged:\n%s", what, sha[:7], logged())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// queryAny wants nothing of the page but the commit it shows.
func queryAny(Snapshot) bool { return true }

// queryGraph wants the page's graph to be want, as querySorted writes it.
func queryGraph(t *testing.T, want string) func(Snapshot) bool {
	return func(snap Snapshot) bool { return querySorted(t, snap.Decisions.Graph) == want }
}

// querySorted is a graph as JSON, with its edges and each decision's
// dependents sorted, so two graphs compare whatever order each was built in.
func querySorted(t *testing.T, g *DecisionGraph) string {
	t.Helper()
	if g == nil {
		return "no graph"
	}
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

// queryBuilt is the graph BuildDecisionGraph builds from a journal's files,
// as querySorted writes it.
func queryBuilt(t *testing.T, journal map[string][]byte) string {
	t.Helper()
	g, err := BuildDecisionGraph("r", journal)
	if err != nil {
		t.Fatal(err)
	}
	return querySorted(t, g)
}

// queryCalls is every call to gh the fake has logged, in whole lines.
func queryCalls(t *testing.T, fake string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fake, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if s = s[:strings.LastIndex(s, "\n")+1]; s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// queryRefreshes waits for the server to refresh n times after the from-th
// call to gh, each refresh reading the branch's commits once, so whatever it
// reads at the commit it's at has been read.
func queryRefreshes(t *testing.T, fake string, from, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		seen := 0
		for _, c := range queryCalls(t, fake)[from:] {
			if strings.Contains(c, "/commits?") {
				seen++
			}
		}
		if seen >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server refreshed %d times in a minute; want %d", seen, n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// queryReads checks that the only reads of the journal among calls are one
// GraphQL query of decisions/journal at sha: no tree listed, and no file read
// one at a time.
func queryReads(t *testing.T, calls []string, sha, when string) {
	t.Helper()
	var reads []string
	for _, c := range calls {
		if strings.HasPrefix(c, "graphql") || strings.Contains(c, "/git/trees/") || strings.Contains(c, "/git/blobs/") || strings.Contains(c, "decisions/journal") {
			reads = append(reads, c)
		}
	}
	want := "graphql " + sha + ":decisions/journal"
	if len(reads) != 1 || reads[0] != want {
		t.Errorf("%s, the server read the journal in %d calls, starting %q; want one query of %s:decisions/journal", when, len(reads), reads[:min(len(reads), 2)], sha[:7])
	}
}

// With every journal file new, as after a restart, a refresh reads
// decisions/journal at the branch's newest commit in one GraphQL query,
// however many files the journal holds, and reads no file one at a time. The
// query asks for what the fake answers, since GitHub answers only what's
// asked, and the page's graph is the one built from the files read one at a
// time.
func TestARefreshReadsTheJournalInOneQuery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	sha := strings.Repeat("a", 40)
	for _, n := range []int{3, 40} {
		repo, _ := queryJournal(t, n)
		files := queryFiles(t, repo)
		fake := queryFake(t)
		queryCommit(t, fake, sha, "# r\n", files, nil)
		s, logged := queryServer(t, fake, sha)
		what := fmt.Sprintf("the graph of its %d journal files, as built from them read one at a time", n)
		queryShows(t, s, sha, what, queryGraph(t, queryBuilt(t, files)), logged)
		queryRefreshes(t, fake, len(queryCalls(t, fake)), 3)
		queryReads(t, queryCalls(t, fake), sha, fmt.Sprintf("with %d journal files new", n))
		query, err := os.ReadFile(filepath.Join(fake, "query"))
		if err != nil {
			t.Fatalf("with %d journal files new, the server sent no GraphQL query", n)
		}
		for _, field := range []string{"entries", "type", "oid", "text", "isTruncated"} {
			if !strings.Contains(string(query), field) {
				t.Errorf("the query doesn't ask for %s, which the fake answers as GitHub would only when asked", field)
			}
		}
	}
}

// When the branch moves to a commit that changes no journal file, the server
// reads nothing more than its one query there, and the page keeps the graph
// it had without building it again. The fake answers that commit with every
// blob ID as it was, but with D-0002's text as it reads once ratified, which
// git never does, so a graph built again would show D-0002 ratified.
func TestACommitThatChangesNoJournalFileKeepsTheGraph(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	repo, store := queryJournal(t, 5)
	journal := queryFiles(t, repo)
	if err := store.Ratify(repo, "D-0002", "@gitdek"); err != nil {
		t.Fatal(err)
	}
	ratified := string(queryFiles(t, repo)["D-0002.jsonl"])
	shaA, shaB := strings.Repeat("a", 40), strings.Repeat("b", 40)
	fake := queryFake(t)
	queryCommit(t, fake, shaA, "# r\n", journal, nil)
	queryCommit(t, fake, shaB, "# r\n\nThe README changed.\n", journal, func(name string, object map[string]any) {
		if name == "D-0002.jsonl" {
			object["text"] = ratified
		}
	})
	s, logged := queryServer(t, fake, shaA)
	want := queryBuilt(t, journal)
	queryShows(t, s, shaA, "the journal's graph", queryGraph(t, want), logged)
	queryRefreshes(t, fake, len(queryCalls(t, fake)), 2)
	from := len(queryCalls(t, fake))
	queryHead(t, fake, shaB)
	queryShows(t, s, shaB, "the branch's new commit", queryAny, logged)
	queryRefreshes(t, fake, len(queryCalls(t, fake)), 3)
	if got := querySorted(t, querySnapshot(t, s).Decisions.Graph); got != want {
		t.Errorf("at %s, which changes no journal file, the page's graph changed: it was built again", shaB[:7])
	}
	queryReads(t, queryCalls(t, fake)[from:], shaB, "after the branch moved to a commit that changes no journal file")
}

// When the branch moves to a commit that changes a journal file, adds one and
// removes one, the server reads decisions/journal there in one query, and the
// page's graph becomes the one built from that commit's files.
func TestTheGraphFollowsTheJournalInOneQuery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	repo, store := queryJournal(t, 5)
	first := queryFiles(t, repo)
	if err := store.Ratify(repo, "D-0002", "@gitdek"); err != nil {
		t.Fatal(err)
	}
	d := decisions.NewDecision{Door: "two-way", Status: "decided", Who: "agent", Text: "Decision 6 stands.", Edges: []decisions.Edge{{Type: decisions.Refines, To: "D-0001"}}}
	if _, err := store.Decide(repo, d, "agent"); err != nil {
		t.Fatal(err)
	}
	second := queryFiles(t, repo)
	delete(second, "D-0005.jsonl")
	shaA, shaB := strings.Repeat("a", 40), strings.Repeat("b", 40)
	fake := queryFake(t)
	queryCommit(t, fake, shaA, "# r\n", first, nil)
	queryCommit(t, fake, shaB, "# r\n", second, nil)
	s, logged := queryServer(t, fake, shaA)
	queryShows(t, s, shaA, "the journal's graph", queryGraph(t, queryBuilt(t, first)), logged)
	queryRefreshes(t, fake, len(queryCalls(t, fake)), 2)
	from := len(queryCalls(t, fake))
	queryHead(t, fake, shaB)
	queryShows(t, s, shaB, "its journal's graph, with D-0002 ratified, D-0006 added and D-0005 gone", queryGraph(t, queryBuilt(t, second)), logged)
	queryRefreshes(t, fake, len(queryCalls(t, fake)), 3)
	queryReads(t, queryCalls(t, fake)[from:], shaB, "after the branch moved to a commit that changes, adds and removes journal files")
}

// When the query fails, or GitHub cuts a journal file's text short, the page
// keeps the last graph that built and lists the journal as stale, and the
// next commit whose journal reads whole brings the graph up to date. The
// short text is D-0002's first line, a journal of its own, so a graph built
// from it would show the new D-0005 without D-0002's ratification.
func TestAFailedJournalQueryKeepsTheLastGoodGraph(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	repo, store := queryJournal(t, 4)
	first := queryFiles(t, repo)
	if err := store.Ratify(repo, "D-0002", "@gitdek"); err != nil {
		t.Fatal(err)
	}
	d := decisions.NewDecision{Door: "two-way", Status: "decided", Who: "agent", Text: "Decision 5 stands."}
	if _, err := store.Decide(repo, d, "agent"); err != nil {
		t.Fatal(err)
	}
	second := queryFiles(t, repo)
	line, _, _ := strings.Cut(string(second["D-0002.jsonl"]), "\n")
	shaA, shaB, shaC, shaD := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40), strings.Repeat("d", 40)
	fake := queryFake(t)
	queryCommit(t, fake, shaA, "# r\n", first, nil)
	queryCommit(t, fake, shaB, "# r\n", second, nil)
	if err := os.WriteFile(filepath.Join(fake, shaB, "fail"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	queryCommit(t, fake, shaC, "# r\n", second, func(name string, object map[string]any) {
		if name == "D-0002.jsonl" {
			object["text"], object["isTruncated"] = line+"\n", true
		}
	})
	queryCommit(t, fake, shaD, "# r\n", second, nil)
	s, logged := queryServer(t, fake, shaA)
	want := queryBuilt(t, first)
	queryShows(t, s, shaA, "the journal's graph", queryGraph(t, want), logged)
	for _, sha := range []string{shaB, shaC} {
		queryHead(t, fake, sha)
		snap := queryShows(t, s, sha, "the branch's new commit", queryAny, logged)
		if querySorted(t, snap.Decisions.Graph) != want {
			t.Fatalf("at %s, whose journal couldn't be read whole, the page's graph isn't the last one that built", sha[:7])
		}
		if !strings.Contains(strings.Join(snap.Stale, "; "), "journal") {
			t.Errorf("at %s, whose journal couldn't be read whole, the page doesn't list the journal as stale: %q", sha[:7], snap.Stale)
		}
	}
	queryHead(t, fake, shaD)
	queryShows(t, s, shaD, "its journal's graph, with D-0002 ratified and D-0005 added", queryGraph(t, queryBuilt(t, second)), logged)
}
