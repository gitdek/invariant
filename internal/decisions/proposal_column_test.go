package decisions

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

const columnProposal = "sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"

// version is a store file's schema version, read as any binary reads it.
func version(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// ratifiedFixture is a store whose demo/D-0001 is ratified for
// columnProposal, closed so another binary can open its file.
func ratifiedFixture(t *testing.T) (string, Repo) {
	t.Helper()
	s, repo := fixture(t)
	if _, err := s.Decide(repo, NewDecision{Door: "two-way", Status: "proposed", Who: "agent", Text: "Ratify this exact proposal."}, "agent"); err != nil {
		t.Fatal(err)
	}
	if err := s.RatifyProposal(repo, "D-0001", "@reviewer", columnProposal); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return s.Path, repo
}

// A store with the proposal column keeps schema version 3, so a binary from
// before the column opens it as it always did, and writes and reads nodes
// with the columns it knows. What such a binary writes never makes a newer
// one show a proposal that's no longer true (#361).
func TestAStoreWithTheProposalColumnStaysOpenToOlderBinaries(t *testing.T) {
	path, repo := ratifiedFixture(t)
	if v := version(t, path); v != 3 {
		t.Fatalf("schema version = %d, want 3, which binaries from before the proposal column open", v)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// What a binary from before the column runs: its node write, with the
	// columns it knows, superseding the ratified decision, and its reads.
	if _, err := db.Exec(`INSERT INTO node (id, kind, project, date, door, status, who, text, record) VALUES ('demo/D-0001', 'decision', 'demo', '2026-10-31', 'two-way', 'superseded', '@reviewer', 'Ratify this exact proposal.', '')
		ON CONFLICT (id) DO UPDATE SET date = excluded.date, door = excluded.door, status = excluded.status, who = excluded.who, text = excluded.text, record = excluded.record`); err != nil {
		t.Fatalf("an older binary's node write: %v", err)
	}
	var status string
	if err := db.QueryRow(`SELECT n.id, n.kind, n.project, n.date, n.door, n.status, n.who, n.text, n.record FROM node n WHERE n.id = 'demo/D-0001'`).Scan(new(string), new(string), new(string), new(string), new(string), &status, new(string), new(string), new(string)); err != nil || status != "superseded" {
		t.Fatalf("an older binary's node read: status %q, %v", status, err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.Get("demo/D-0001")
	if err != nil {
		t.Fatal(err)
	}
	if n.Proposal != "" {
		t.Errorf("a decision an older binary marked superseded still shows proposal %s", n.Proposal)
	}
	// An older binary's rebuild re-inserts the project's nodes without the
	// column, leaving the proposal empty until a newer binary rebuilds.
	if _, err := db.Exec(`DELETE FROM edge WHERE project = 'demo'; DELETE FROM node WHERE project = 'demo';
		INSERT INTO node (id, kind, project, date, door, status, who, text, record) VALUES ('demo/D-0001', 'decision', 'demo', '2026-10-31', 'two-way', 'ratified', '@reviewer', 'Ratify this exact proposal.', '')`); err != nil {
		t.Fatalf("an older binary's rebuild: %v", err)
	}
	if n, err := s.Get("demo/D-0001"); err != nil || n.Proposal != "" {
		t.Errorf("after an older binary's rebuild: %+v, %v; want no proposal yet", n, err)
	}
	if err := s.Rebuild([]Repo{repo}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Get("demo/D-0001"); err != nil || n.Proposal != columnProposal {
		t.Errorf("after a newer binary's rebuild: %+v, %v; want proposal %s", n, err, columnProposal)
	}
	if v := version(t, path); v != 3 {
		t.Errorf("schema version = %d after reopening and rebuilding, want 3", v)
	}
}

// A store a newer binary wrote, with a higher schema version, is refused,
// and left exactly as it was, instead of dropped and remade (#361).
func TestAStoreFromANewerBinaryIsRefusedAndLeftAlone(t *testing.T) {
	path, _ := ratifiedFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA user_version = 4`); err != nil {
		t.Fatal(err)
	}
	var nodes int
	if err := db.QueryRow(`SELECT count(*) FROM node`).Scan(&nodes); err != nil || nodes == 0 {
		t.Fatalf("fixture: %d nodes, %v", nodes, err)
	}
	s, err := Open(path)
	if err == nil {
		s.Close()
		t.Fatal("a store of schema version 4 opened")
	}
	for _, want := range []string{"version 4", "version 3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q doesn't name %q", err, want)
		}
	}
	var after int
	if err := db.QueryRow(`SELECT count(*) FROM node`).Scan(&after); err != nil || after != nodes {
		t.Errorf("refusing the store changed it: %d nodes, was %d (%v)", after, nodes, err)
	}
	if v := version(t, path); v != 4 {
		t.Errorf("schema version = %d after the refusal, want 4", v)
	}
}

// Opening a version-3 store made before the proposal column adds it in
// place and rebuilds each project the store holds from its checkout, so
// the proposal shows with no rebuild by hand. A checkout that's gone is
// skipped (#361).
func TestOpeningAStoreFromBeforeTheColumnFillsProposals(t *testing.T) {
	_, repo := ratifiedFixture(t)
	path := filepath.Join(t.TempDir(), "before.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
CREATE TABLE node (id TEXT PRIMARY KEY, kind TEXT NOT NULL, project TEXT NOT NULL,
 date TEXT NOT NULL DEFAULT '', door TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT '',
 who TEXT NOT NULL DEFAULT '', text TEXT NOT NULL DEFAULT '', record TEXT NOT NULL DEFAULT '');
CREATE TABLE edge (src TEXT NOT NULL, type TEXT NOT NULL, dst TEXT NOT NULL, project TEXT NOT NULL,
 derived INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (src, type, dst));
CREATE TABLE repo (project TEXT PRIMARY KEY, dir TEXT NOT NULL, rebuilt TEXT NOT NULL);
CREATE TABLE journal (seq INTEGER PRIMARY KEY AUTOINCREMENT, project TEXT NOT NULL, id TEXT NOT NULL,
 hash TEXT NOT NULL, line TEXT NOT NULL, UNIQUE (project, hash));
CREATE TABLE taken (project TEXT NOT NULL, id TEXT NOT NULL, PRIMARY KEY (project, id));
INSERT INTO node (id, kind, project, status, who, text) VALUES ('demo/D-0001', 'decision', 'demo', 'ratified', '@reviewer', 'Ratify this exact proposal.');
INSERT INTO taken VALUES ('demo', 'D-0001');
INSERT INTO repo VALUES ('gone', '/nonexistent/checkout', '2026-10-31T00:00:00Z');
PRAGMA user_version = 3;`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO repo VALUES ('demo', ?, '2026-10-31T00:00:00Z')`, repo.Dir); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, err := s.Get("demo/D-0001"); err != nil || n.Proposal != columnProposal {
		t.Errorf("after opening: %+v, %v; want proposal %s without a rebuild by hand", n, err, columnProposal)
	}
	held, err := s.Checkouts()
	if err != nil || len(held) != 2 {
		t.Errorf("held checkouts = %+v, %v; want both kept, the gone one included", held, err)
	}
	if v := version(t, path); v != 3 {
		t.Errorf("schema version = %d, want 3", v)
	}
}
