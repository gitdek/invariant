package decisions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
		Record: "journal-every-write", Edges: []Edge{{Refines, "D-0001"}}}, "agent")
	if err != nil || second != "D-0002" {
		t.Fatalf("second = %q, %v", second, err)
	}
	if d, _ := s.Get("demo/D-0002"); d.Record != "D-0002-journal-every-write.md" {
		t.Errorf("the record is %q", d.Record)
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

// What rests on a decision follows refines all the way, and a mention one
// step: it reaches the SPEC, the docs and the code that name any decision
// in the chain, and ends even where the graph loops.
func TestDependentsFollowRefinesAndOneMention(t *testing.T) {
	s, repo := fixture(t)
	for _, d := range []NewDecision{
		{Text: "The store is SQLite."},
		{Text: "Journal every write.", Edges: []Edge{{Refines, "D-0001"}}},
		{Text: "Check it in CI, as D-0003 says.", Edges: []Edge{{Refines, "D-0002"}}},
		{Text: "Name the tools as D-0001 does."},
	} {
		d.Door, d.Status, d.Who = "two-way", "decided", "agent"
		if _, err := s.Decide(repo, d, "agent"); err != nil {
			t.Fatal(err)
		}
	}
	// D-0001 cites D-0003 through its record, which closes a loop.
	write(t, filepath.Join(repo.Dir, "decisions", "D-0001-store.md"), "# D-0001\n\nSee D-0003.\n")
	write(t, filepath.Join(repo.Dir, "SPEC.md"), "# Spec\n\n- The store journals every write. `D-0002`\n")
	write(t, filepath.Join(repo.Dir, "store", "store.go"), "package store\n\n// append writes a line (D-0003).\nfunc append() {}\n")
	// D-0004 only mentions D-0001, so what implements D-0004 doesn't rest on D-0001.
	write(t, filepath.Join(repo.Dir, "tools", "tools.go"), "package tools\n\n// Names, as D-0004 says.\n")
	write(t, filepath.Join(repo.Dir, "testdata", "copy.go"), "// D-0001 in a test copy is left out.\n")
	if err := s.Rebuild([]Repo{repo}); err != nil {
		t.Fatal(err)
	}
	deps, err := s.Dependents("demo/D-0001")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(ids(deps), ","), "demo/store/store.go:3,demo/D-0002,demo/D-0003,demo/D-0004,demo/SPEC.md:3"; got != want {
		t.Errorf("dependents = %s; want %s", got, want)
	}
	code, _ := s.Implementers("demo/D-0001")
	if len(code) != 1 || code[0].ID != "demo/store/store.go:3" || code[0].Text != "// append writes a line (D-0003)." {
		t.Errorf("implementers = %+v", code)
	}
	grounds, _ := s.Grounds("demo/D-0003")
	if got := strings.Join(ids(grounds), ","); got != "demo/D-0001,demo/D-0002" {
		t.Errorf("grounds = %s", got)
	}
	found, _ := s.Search("journal WRITE")
	if len(found) != 1 || found[0].ID != "demo/D-0002" {
		t.Errorf("search = %v", ids(found))
	}
}

// The log's table carries over exactly: its rows become import lines, and
// the table the store renders from them is the table it came from.
func TestTheLogCarriesOverExactly(t *testing.T) {
	marked, err := os.ReadFile(filepath.Join("..", "..", "decisions", "log.md"))
	if err != nil {
		t.Fatal(err)
	}
	strip := func(s string) string {
		s = strings.Replace(s, TableStart+"\n\n", "", 1)
		return strings.Replace(s, "\n"+TableEnd+"\n", "", 1)
	}
	log := strip(string(marked))
	events, err := ParseLog(log)
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
	rendered, err := WithTable(log, Table(decisions, "demo"))
	if err != nil {
		t.Fatal(err)
	}
	if strip(rendered) != log {
		t.Error("the table rendered from the journal isn't the log's table")
	}
	again, err := WithTable(rendered, Table(decisions, "demo"))
	if err != nil || again != rendered {
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

// Two checkouts of one project share the store's numbering: a decision on
// a branch that isn't merged still holds its ID, even after the store is
// rebuilt from another checkout.
func TestCheckoutsNeverShareAnID(t *testing.T) {
	s, a := fixture(t)
	b := Repo{Name: "demo", Dir: filepath.Join(filepath.Dir(a.Dir), "demo-branch")}
	decide := func(repo Repo, want string) {
		t.Helper()
		id, err := s.Decide(repo, NewDecision{Door: "two-way", Status: "decided", Who: "agent", Text: "A call on " + filepath.Base(repo.Dir) + "."}, "agent")
		if err != nil || id != want {
			t.Fatalf("%s decided %q, %v; want %s", filepath.Base(repo.Dir), id, err, want)
		}
	}
	decide(a, "D-0001")
	decide(b, "D-0002")
	if err := s.Rebuild([]Repo{a}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("demo/D-0002"); err == nil {
		t.Error("a rebuild from one checkout keeps the project as that checkout has it")
	}
	decide(b, "D-0003")
	decide(a, "D-0004")
}

// Rebuilding one project leaves every other project in the store as it was.
func TestRebuildLeavesOtherProjects(t *testing.T) {
	s, demo := fixture(t)
	other := Repo{Name: "other", Dir: filepath.Join(filepath.Dir(demo.Dir), "other")}
	for _, r := range []Repo{demo, other} {
		if _, err := s.Decide(r, NewDecision{Door: "two-way", Status: "decided", Who: "agent", Text: "One."}, "agent"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Rebuild([]Repo{demo}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("other/D-0001"); err != nil {
		t.Errorf("rebuilding demo dropped other: %v", err)
	}
	held, err := s.Checkouts()
	if err != nil || len(held) != 1 || held[0].Name != "demo" {
		t.Errorf("checkouts = %+v, %v", held, err)
	}
}

// A journal line that breaks the rules is refused when the store is
// rebuilt, however it got there: an agent's ratification, a first line that
// doesn't record the decision, and an agent deciding a one-way door.
func TestTheJournalsRulesHoldOnARebuild(t *testing.T) {
	for name, lines := range map[string][]Event{
		"an agent ratifies": {
			{Op: OpDecide, ID: "D-0001", Door: "two-way", Status: "proposed", Who: "agent", Text: "One."},
			{Op: OpRatify, ID: "D-0001", By: "agent"},
		},
		"no decision first": {{Op: OpLink, ID: "D-0001", Edges: []Edge{{Cites, "D-0002"}}}},
		"a one-way door decided": {
			{Op: OpDecide, ID: "D-0001", Door: "one-way", Status: "decided", Who: "agent", Text: "One."},
		},
		"decided twice": {
			{Op: OpDecide, ID: "D-0001", Door: "two-way", Status: "decided", Who: "agent", Text: "One."},
			{Op: OpDecide, ID: "D-0001", Door: "two-way", Status: "decided", Who: "agent", Text: "Two."},
		},
	} {
		s, repo := fixture(t)
		for i := range lines {
			if err := appendLine(repo, &lines[i], i == 0); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Rebuild([]Repo{repo}); err == nil {
			t.Errorf("%s: the rebuild took it", name)
		}
	}
}

// An edge the journal records takes the place of a citation read from a
// decision's words, the same way after the write as after a rebuild.
func TestALinkReplacesACitation(t *testing.T) {
	s, repo := fixture(t)
	for _, text := range []string{"The store is SQLite.", "It journals every write, as D-0001 allows."} {
		if _, err := s.Decide(repo, NewDecision{Door: "two-way", Status: "decided", Who: "agent", Text: text}, "agent"); err != nil {
			t.Fatal(err)
		}
	}
	edges := func() string {
		_, rows, err := s.Query(`SELECT type || ' ' || dst || ' ' || derived FROM edge WHERE src = 'demo/D-0002' ORDER BY 1`, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(rows)
		return string(b)
	}
	if got := edges(); got != `[["cites demo/D-0001 1"]]` {
		t.Fatalf("before the link: %s", got)
	}
	if err := s.Link(repo, "D-0002", Edge{Refines, "D-0001"}, "agent"); err != nil {
		t.Fatal(err)
	}
	after := edges()
	if after != `[["refines demo/D-0001 0"]]` {
		t.Errorf("after the link: %s", after)
	}
	if err := s.Rebuild([]Repo{repo}); err != nil {
		t.Fatal(err)
	}
	if got := edges(); got != after {
		t.Errorf("the rebuild has %s, the write had %s", got, after)
	}
	near, err := s.Neighbors("demo/D-0001")
	if err != nil || len(near) != 1 || near[0].Out || near[0].Type != Refines || near[0].Node.ID != "demo/D-0002" {
		t.Errorf("neighbors = %+v, %v", near, err)
	}
	if err := s.Link(repo, "D-0002", Edge{Cites, "D-0009"}, "agent"); err == nil {
		t.Error("a link to a decision that doesn't exist was taken")
	}
}

// Against the base branch, the journal only grows: a new line or a new file
// passes, and a changed line or a removed file doesn't.
func TestTheJournalOnlyGrowsSinceBase(t *testing.T) {
	s, repo := fixture(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo.Dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	for _, text := range []string{"One.", "Two."} {
		if _, err := s.Decide(repo, NewDecision{Door: "two-way", Status: "proposed", Who: "agent", Text: text}, "agent"); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	git("tag", "base")
	grown := func() []string {
		t.Helper()
		problems, err := Grown(context.Background(), repo, "base")
		if err != nil {
			t.Fatal(err)
		}
		return problems
	}
	if err := s.Ratify(repo, "D-0001", "@gitdek"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(repo, NewDecision{Door: "two-way", Status: "decided", Who: "agent", Text: "Three."}, "agent"); err != nil {
		t.Fatal(err)
	}
	if p := grown(); len(p) != 0 {
		t.Errorf("lines and files added: %v", p)
	}
	one := filepath.Join(repo.JournalDir(), "D-0001.jsonl")
	b, _ := os.ReadFile(one)
	write(t, one, strings.Replace(string(b), "One.", "Uno.", 1))
	os.Remove(filepath.Join(repo.JournalDir(), "D-0002.jsonl"))
	p := grown()
	if len(p) != 2 || !strings.Contains(p[0], "demo/D-0001's journal changed") || !strings.Contains(p[1], "demo/D-0002's journal is gone") {
		t.Errorf("problems = %v", p)
	}
}

// A line whose old text is the start of its new text has still changed: a
// file at base whose last line had no newline after it can't have that line
// extended, which a check of the files' bytes let through (#194).
func TestAJournalLineExtendedHasChanged(t *testing.T) {
	_, repo := fixture(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo.Dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	one := filepath.Join(repo.JournalDir(), "D-0001.jsonl")
	write(t, one, "one\ntwo")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	git("tag", "base")
	write(t, one, "one\ntwofold\n")
	problems, err := Grown(context.Background(), repo, "base")
	if err != nil || len(problems) != 1 || !strings.Contains(problems[0], "demo/D-0001's journal changed") {
		t.Errorf("problems = %v, %v; want D-0001's changed line", problems, err)
	}
	write(t, one, "one\ntwo\nthree\n")
	if problems, err := Grown(context.Background(), repo, "base"); err != nil || len(problems) != 0 {
		t.Errorf("a line added after the last one: %v, %v", problems, err)
	}
}

// The graph check finds a citation of a decision that doesn't exist, a
// SPEC line resting on a superseded decision, a missing record and a log
// that isn't the journal's view, and passes once they're fixed.
func TestCheckFindsWhatsWrong(t *testing.T) {
	s, repo := fixture(t)
	write(t, filepath.Join(repo.Dir, "decisions", "log.md"), "# Decision log\n\n"+tableHead+"\nThe end.\n")
	for _, d := range []NewDecision{
		{Door: "two-way", Status: "decided", Who: "agent", Text: "Keep one file."},
		{Door: "one-way", Status: "proposed", Who: "agent", Text: "Journal every write.", Record: "journal"},
		{Door: "two-way", Status: "decided", Who: "agent", Text: "Two files."},
	} {
		if _, err := s.Decide(repo, d, "agent"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Supersede(repo, "D-0001", "D-0003", "agent"); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(repo.Dir, "SPEC.md"), "# Spec\n\n- One file. `D-0001`\n")
	write(t, filepath.Join(repo.Dir, "docs", "notes.md"), "As D-0009 says.\n")
	check := func() []string {
		t.Helper()
		if err := s.Rebuild([]Repo{repo}); err != nil {
			t.Fatal(err)
		}
		problems, err := s.Check([]Repo{repo})
		if err != nil {
			t.Fatal(err)
		}
		return problems
	}
	got := strings.Join(check(), "\n")
	for _, want := range []string{"cites demo/D-0009, which isn't a decision", "demo/SPEC.md:3 rests on a superseded decision",
		"demo/D-0002's record, decisions/D-0002-journal.md, isn't there", "decisions/log.md isn't the journal's view"} {
		if !strings.Contains(got, want) {
			t.Errorf("the check missed %q in:\n%s", want, got)
		}
	}
	write(t, filepath.Join(repo.Dir, "SPEC.md"), "# Spec\n\n- Two files. `D-0003`\n")
	write(t, filepath.Join(repo.Dir, "docs", "notes.md"), "As D-0003 says.\n")
	write(t, filepath.Join(repo.Dir, "decisions", "D-0002-journal.md"), "# D-0002\n")
	if _, err := WriteLog(repo); err != nil {
		t.Fatal(err)
	}
	if p := check(); len(p) != 0 {
		t.Errorf("after the fixes: %v", p)
	}
}
