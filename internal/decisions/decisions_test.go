package decisions

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fixture is a store and one project's repository, both new.
func fixture(t *testing.T) (*Store, Repo) {
	t.Helper()
	dir := t.TempDir()
	repo := Repo{Name: "demo", Dir: filepath.Join(dir, "demo")}
	if err := os.MkdirAll(repo.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, "decisions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, repo
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ids(nodes []Node) []string {
	var out []string
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
}

// A decision takes the next ID, lands in its own journal file and in the
// graph, and ratifying and superseding it are lines in the same file.
func TestDecideRatifySupersede(t *testing.T) {
	s, repo := fixture(t)
	first, err := s.Decide(repo, NewDecision{Door: "two-way", Status: "decided", Who: "agent", Text: "Keep the store in one file."}, "agent")
	if err != nil || first != "D-0001" {
		t.Fatalf("first = %q, %v", first, err)
	}
	second, err := s.Decide(repo, NewDecision{Door: "one-way", Status: "proposed", Who: "agent", Text: "Journal every write, for D-0001.",
		Edges: []Edge{{Refines, "D-0001"}}}, "agent")
	if err != nil || second != "D-0002" {
		t.Fatalf("second = %q, %v", second, err)
	}
	if _, err := s.Decide(repo, NewDecision{Door: "one-way", Status: "decided", Text: "x"}, "agent"); err == nil {
		t.Error("an agent can't decide a one-way door; it proposes one")
	}
	if err := s.Ratify(repo, "D-0002", "@gitdek"); err != nil {
		t.Fatal(err)
	}
	if err := s.Ratify(repo, "D-0002", "@gitdek"); err == nil {
		t.Error("ratifying twice should be refused")
	}
	third, err := s.Decide(repo, NewDecision{Door: "two-way", Status: "decided", Who: "agent", Text: "Two files after all."}, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Supersede(repo, "D-0001", third, "agent"); err != nil {
		t.Fatal(err)
	}
	d1, _ := s.Get("demo/D-0001")
	d2, _ := s.Get("demo/D-0002")
	if d1.Status != "superseded" || d2.Status != "ratified" || d2.Who != "@gitdek" {
		t.Errorf("D-0001 %+v, D-0002 %+v", d1, d2)
	}
	events, err := ReadJournal(filepath.Join(repo.JournalDir(), "D-0002.jsonl"))
	if err != nil || len(events) != 2 || events[1].Op != OpRatify || events[1].Prev != events[0].Hash {
		t.Fatalf("D-0002's journal: %+v, %v", events, err)
	}
	// What rests on D-0001: D-0002 refines it; D-0003 supersedes it, which
	// isn't resting on it.
	deps, err := s.Dependents("demo/D-0001")
	if err != nil || strings.Join(ids(deps), ",") != "demo/D-0002" {
		t.Errorf("dependents = %v, %v", ids(deps), err)
	}
}

// The journal is the record: rebuilding from it gives the same graph, and
// an edited, dropped or reordered line is caught.
func TestRebuildAndTamper(t *testing.T) {
	s, repo := fixture(t)
	for i := 0; i < 3; i++ {
		if _, err := s.Decide(repo, NewDecision{Door: "two-way", Status: "decided", Who: "agent", Text: fmt.Sprintf("Step %d.", i)}, "agent"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Ratify(repo, "D-0002", "@gitdek"); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		cols, rows, err := s.Query(`SELECT id, status, who, text FROM node UNION ALL SELECT src || ' ' || type, dst, derived, '' FROM edge ORDER BY 1, 2`, time.Second)
		if err != nil || len(cols) != 4 {
			t.Fatal(err)
		}
		b, _ := json.Marshal(rows)
		return string(b)
	}
	before := snapshot()
	if err := s.Rebuild([]Repo{repo}); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(); after != before {
		t.Errorf("a rebuild changed the graph:\n%s\n%s", before, after)
	}

	path := filepath.Join(repo.JournalDir(), "D-0002.jsonl")
	original, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(original)), "\n")
	for name, text := range map[string]string{
		"edited":    strings.Replace(string(original), "Step 1.", "Step one.", 1),
		"dropped":   lines[1] + "\n",
		"reordered": lines[1] + "\n" + lines[0] + "\n",
	} {
		write(t, path, text)
		if err := s.Rebuild([]Repo{repo}); err == nil {
			t.Errorf("a %s journal line wasn't caught", name)
		}
	}
	write(t, path, string(original))
	if err := s.Rebuild([]Repo{repo}); err != nil {
		t.Fatal(err)
	}
}

// The journal table only grows, and SQL run read-only can't change a thing.
func TestJournalOnlyGrowsAndQueriesOnlyRead(t *testing.T) {
	s, repo := fixture(t)
	if _, err := s.Decide(repo, NewDecision{Door: "two-way", Status: "decided", Who: "agent", Text: "One."}, "agent"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`UPDATE journal SET line = 'x'`, `DELETE FROM journal`} {
		if _, err := s.db.Exec(q); err == nil || !strings.Contains(err.Error(), "only grows") {
			t.Errorf("%s: %v", q, err)
		}
	}
	ro, err := OpenReadOnly(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if _, _, err := ro.Query(`DELETE FROM node`, time.Second); err == nil {
		t.Error("a read-only store ran a DELETE")
	}
	cols, rows, err := ro.Query(`SELECT id, status FROM node`, time.Second)
	if err != nil || len(cols) != 2 || len(rows) != 1 {
		t.Errorf("cols %v, rows %v, %v", cols, rows, err)
	}
	// A query that would never end is stopped.
	start := time.Now()
	_, _, err = ro.Query(`WITH RECURSIVE forever(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM forever) SELECT count(*) FROM forever`, 200*time.Millisecond)
	if err == nil || time.Since(start) > 5*time.Second {
		t.Errorf("a runaway query ran %v, err %v", time.Since(start), err)
	}
}

// Walking what rests on a decision ends even where the graph loops, and
// reaches the SPEC, the docs and the code that name it.
func TestDependentsThroughTextAndLoops(t *testing.T) {
	s, repo := fixture(t)
	for _, text := range []string{"The store is SQLite.", "Journal every write, for D-0001.", "Check it in CI, for D-0002, as D-0003 says."} {
		if _, err := s.Decide(repo, NewDecision{Door: "two-way", Status: "decided", Who: "agent", Text: text}, "agent"); err != nil {
			t.Fatal(err)
		}
	}
	// D-0001 cites D-0003 through its record, which closes a loop.
	write(t, filepath.Join(repo.Dir, "decisions", "D-0001-store.md"), "# D-0001\n\nSee D-0003.\n")
	write(t, filepath.Join(repo.Dir, "SPEC.md"), "# Spec\n\n- The store journals every write. `D-0002`\n")
	write(t, filepath.Join(repo.Dir, "store", "store.go"), "package store\n\n// append writes a line (D-0003).\nfunc append() {}\n")
	write(t, filepath.Join(repo.Dir, "testdata", "copy.go"), "// D-0001 in a test copy is left out.\n")
	if err := s.Rebuild([]Repo{repo}); err != nil {
		t.Fatal(err)
	}
	deps, err := s.Dependents("demo/D-0001")
	if err != nil {
		t.Fatal(err)
	}
	want := "demo/store/store.go:3,demo/D-0002,demo/D-0003,demo/SPEC.md:3"
	got := ids(deps)
	sort.Slice(got, func(i, j int) bool { return deps[i].Kind+got[i] < deps[j].Kind+got[j] })
	if strings.Join(got, ",") != want {
		t.Errorf("dependents = %v; want %s", got, want)
	}
	code, _ := s.Implementers("demo/D-0001")
	if len(code) != 1 || code[0].ID != "demo/store/store.go:3" || code[0].Text != "// append writes a line (D-0003)." {
		t.Errorf("implementers = %+v", code)
	}
	grounds, _ := s.Grounds("demo/D-0003")
	if strings.Join(ids(grounds), ",") != "demo/D-0001,demo/D-0002" {
		t.Errorf("grounds = %v", ids(grounds))
	}
	found, _ := s.Search("journal WRITE")
	if len(found) != 1 || found[0].ID != "demo/D-0002" {
		t.Errorf("search = %v", ids(found))
	}
}

// The log's table carries over exactly: its rows become import lines, and
// the table the store renders from them is the table it came from.
func TestTheLogCarriesOverExactly(t *testing.T) {
	log, err := os.ReadFile(filepath.Join("..", "..", "decisions", "log.md"))
	if err != nil {
		t.Fatal(err)
	}
	events, err := ParseLog(string(log))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 97 || events[0].ID != "D-0000" || events[1].Record != "D-0001-factory-in-go.md" {
		t.Fatalf("%d events, first %+v", len(events), events[0])
	}
	var refines int
	for _, e := range events {
		for _, edge := range e.Edges {
			if edge.Type == Refines {
				refines++
			}
		}
	}
	if refines < 5 {
		t.Errorf("only %d refines edges; decisions 'for D-0082' refine it", refines)
	}
	s, repo := fixture(t)
	if err := Import(repo, events); err != nil {
		t.Fatal(err)
	}
	if err := s.Rebuild([]Repo{repo}); err != nil {
		t.Fatal(err)
	}
	decisions, err := s.Decisions("demo")
	if err != nil {
		t.Fatal(err)
	}
	marked, err := WithTable(string(log), Table(decisions, "demo"))
	if err != nil {
		t.Fatal(err)
	}
	strip := func(s string) string {
		s = strings.Replace(s, TableStart+"\n\n", "", 1)
		return strings.Replace(s, "\n"+TableEnd+"\n", "", 1)
	}
	if strip(marked) != string(log) {
		t.Error("the table rendered from the journal isn't the log's table")
	}
	again, err := WithTable(marked, Table(decisions, "demo"))
	if err != nil || again != marked {
		t.Errorf("rendering twice changed the log: %v", err)
	}
}

// Every SPEC bullet here traces to a decision, itself or through the bullet
// it sits under.
func TestTheSpecTracesToDecisions(t *testing.T) {
	spec, err := os.ReadFile(filepath.Join("..", "..", "SPEC.md"))
	if err != nil {
		t.Fatal(err)
	}
	if missing := Untraced(string(spec)); len(missing) > 0 {
		t.Errorf("%d bullets trace to no decision:\n%s", len(missing), strings.Join(missing, "\n"))
	}
	if got := Untraced("# A\n\n- One `D-0001`\n  - under it\n- alone\n"); len(got) != 1 || got[0] != "- alone" {
		t.Errorf("untraced = %v", got)
	}
}

// Several processes writing at once never take the same ID, and every
// journal stays whole. Each writer is this test binary, run again.
func TestProcessesWriteAtOnce(t *testing.T) {
	if os.Getenv("DECISIONS_WRITER") != "" {
		return
	}
	s, repo := fixture(t)
	const writers, each = 4, 15
	var cmds []*exec.Cmd
	for w := 0; w < writers; w++ {
		cmd := exec.Command(os.Args[0], "-test.run", "^TestWriterProcess$")
		cmd.Env = append(os.Environ(), "DECISIONS_WRITER="+strconv.Itoa(w), "DECISIONS_STORE="+s.Path,
			"DECISIONS_REPO="+repo.Dir, "DECISIONS_EACH="+strconv.Itoa(each))
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, cmd)
	}
	ro, err := OpenReadOnly(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	for _, cmd := range cmds {
		for cmd.ProcessState == nil {
			if _, _, err := ro.Query(`SELECT count(*) FROM node`, time.Second); err != nil {
				t.Errorf("a read while others wrote: %v", err)
				break
			}
			if err := cmd.Wait(); err != nil {
				t.Fatalf("a writer failed: %v", err)
			}
		}
	}
	decisions, err := s.Decisions("demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != writers*each {
		t.Fatalf("%d decisions; want %d", len(decisions), writers*each)
	}
	for i, d := range decisions {
		if want := fmt.Sprintf("demo/D-%04d", i+1); d.ID != want {
			t.Fatalf("decision %d is %s; want %s, with no gaps or repeats", i, d.ID, want)
		}
	}
	if err := s.Rebuild([]Repo{repo}); err != nil {
		t.Fatal(err)
	}
}

// TestWriterProcess is one writer for TestProcessesWriteAtOnce.
func TestWriterProcess(t *testing.T) {
	w := os.Getenv("DECISIONS_WRITER")
	if w == "" {
		t.Skip("run by TestProcessesWriteAtOnce")
	}
	s, err := Open(os.Getenv("DECISIONS_STORE"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	each, _ := strconv.Atoi(os.Getenv("DECISIONS_EACH"))
	repo := Repo{Name: "demo", Dir: os.Getenv("DECISIONS_REPO")}
	for i := 0; i < each; i++ {
		if _, err := s.Decide(repo, NewDecision{Door: "two-way", Status: "decided", Who: "agent", Text: "Writer " + w + ", decision " + strconv.Itoa(i) + "."}, "agent"); err != nil {
			t.Fatal(err)
		}
	}
}
