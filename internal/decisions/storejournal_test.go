package decisions

import (
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// lines is how many of a decision's journal lines the store holds.
func lines(t *testing.T, s *Store, id string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM journal WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The store journals a decision's line before the checkout writes it, so a
// write that stops partway leaves the line in the store alone, which keeps
// it, and never a line in a checkout that the store doesn't hold (#195).
func TestTheStoreJournalsALineBeforeTheCheckoutWritesIt(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes where a file's mode says it can't")
	}
	s, repo := fixture(t)
	decide := func() (string, error) {
		return s.Decide(repo, NewDecision{Door: "two-way", Status: "proposed", Who: "agent", Text: "One."}, "agent")
	}
	if err := os.MkdirAll(repo.JournalDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(repo.JournalDir(), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(repo.JournalDir(), 0o755) })
	if _, err := decide(); err == nil {
		t.Fatal("a decide wrote to a journal directory it can't write")
	}
	if n := lines(t, s, "demo/D-0001"); n != 1 {
		t.Errorf("the store holds %d of D-0001's lines after a decide the checkout didn't write, want 1", n)
	}
	if err := os.Chmod(repo.JournalDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if id, err := decide(); err != nil || id != "D-0002" {
		t.Fatalf("decided %q, %v; want D-0002, past the gap", id, err)
	}
	path := filepath.Join(repo.JournalDir(), "D-0002.jsonl")
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := s.Ratify(repo, "D-0002", "@gitdek"); err == nil {
		t.Fatal("a ratify wrote to a journal file it can't write")
	}
	if n := lines(t, s, "demo/D-0002"); n != 2 {
		t.Errorf("the store holds %d of D-0002's lines after a ratify the checkout didn't write, want 2", n)
	}
	if events, err := ReadJournal(path); err != nil || len(events) != 1 {
		t.Errorf("the checkout holds %d of D-0002's lines, %v; want 1", len(events), err)
	}
	if err := s.Rebuild([]Repo{repo}); err != nil {
		t.Fatal(err)
	}
	if one, two := lines(t, s, "demo/D-0001"), lines(t, s, "demo/D-0002"); one != 1 || two != 2 {
		t.Errorf("after a rebuild, the store holds %d of D-0001's lines and %d of D-0002's, want 1 and 2", one, two)
	}
}

// A rebuild adds a checkout's lines and drops none: the store keeps the lines
// of a branch that was never merged, and of one that's abandoned (#195).
func TestARebuildKeepsTheLinesOnlyOtherBranchesHold(t *testing.T) {
	s, a, b := twoCheckouts(t)
	if _, err := s.Decide(a, NewDecision{Door: "two-way", Status: "decided", Who: "agent", Text: "On a."}, "agent"); err != nil {
		t.Fatal(err)
	}
	id, err := s.Decide(b, NewDecision{Door: "two-way", Status: "proposed", Who: "agent", Text: "On b."}, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Ratify(b, id, "@gitdek"); err != nil {
		t.Fatal(err)
	}
	if err := s.Rebuild([]Repo{a}); err != nil {
		t.Fatal(err)
	}
	if n := lines(t, s, "demo/"+id); n != 2 {
		t.Errorf("a rebuild from a left the store %d of %s's lines, which only b holds; want 2", n, id)
	}
	if err := os.RemoveAll(b.Dir); err != nil {
		t.Fatal(err)
	}
	if err := s.Rebuild([]Repo{a}); err != nil {
		t.Fatal(err)
	}
	if n := lines(t, s, "demo/"+id); n != 2 {
		t.Errorf("once b is abandoned, the store holds %d of %s's lines, want 2", n, id)
	}
}

// Two projects can write the same line, word for word and in the same
// second, and the store journals it for each (#195).
func TestTwoProjectsCanJournalTheSameLine(t *testing.T) {
	s, demo := fixture(t)
	other := Repo{Name: "other", Dir: filepath.Join(filepath.Dir(demo.Dir), "other")}
	for _, r := range []Repo{demo, other} {
		e := Event{At: "2026-09-29T12:00:00Z", By: "agent", Op: OpDecide, ID: "D-0001", Date: "2026-09-29",
			Door: "two-way", Status: "decided", Who: "agent", Text: "One."}
		if err := appendLine(r, &e, true); err != nil {
			t.Fatal(err)
		}
		if err := s.Rebuild([]Repo{r}); err != nil {
			t.Fatalf("rebuilding %s: %v", r.Name, err)
		}
	}
	for _, id := range []string{"demo/D-0001", "other/D-0001"} {
		if n := lines(t, s, id); n != 1 {
			t.Errorf("the store holds %d of %s's lines, want 1", n, id)
		}
	}
}

// A store from before lines were journaled per project keeps every line it
// holds, in order, and its journal still refuses edits (#195).
func TestAnOlderStoreKeepsEveryLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
CREATE TABLE journal (
	seq     INTEGER PRIMARY KEY AUTOINCREMENT,
	project TEXT NOT NULL,
	id      TEXT NOT NULL,
	hash    TEXT NOT NULL UNIQUE,
	line    TEXT NOT NULL
);
CREATE TRIGGER journal_no_update BEFORE UPDATE ON journal
BEGIN SELECT RAISE(ABORT, 'the journal only grows'); END;
CREATE TRIGGER journal_no_delete BEFORE DELETE ON journal
BEGIN SELECT RAISE(ABORT, 'the journal only grows'); END;
INSERT INTO journal (project, id, hash, line) VALUES
	('demo', 'demo/D-0001', 'sha256:a', '{}'),
	('other', 'other/D-0001', 'sha256:b', '{}'),
	('demo', 'demo/D-0001', 'sha256:c', '{}');
PRAGMA user_version = 2;`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.db.Query(`SELECT seq, project, hash FROM journal ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var seq int
		var project, hash string
		if err := rows.Scan(&seq, &project, &hash); err != nil {
			t.Fatal(err)
		}
		got = append(got, string(rune('0'+seq))+" "+project+" "+hash)
	}
	rows.Close()
	if want := []string{"1 demo sha256:a", "2 other sha256:b", "3 demo sha256:c"}; !slices.Equal(got, want) {
		t.Errorf("the journal holds %q, want %q", got, want)
	}
	if _, err := s.db.Exec(`INSERT INTO journal (project, id, hash, line) VALUES ('other', 'other/D-0002', 'sha256:a', '{}')`); err != nil {
		t.Errorf("another project can't journal a line demo holds: %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM journal`); err == nil {
		t.Error("the journal let a line be deleted")
	}
	if _, err := s.db.Exec(`UPDATE journal SET line = 'x'`); err == nil {
		t.Error("the journal let a line be changed")
	}
}
