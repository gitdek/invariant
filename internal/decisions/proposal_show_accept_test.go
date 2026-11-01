package decisions

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const showProposal358 = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func proposalFixture358(t *testing.T) (*Store, Repo) {
	t.Helper()
	s, repo := fixture(t)
	for _, d := range []NewDecision{
		{Door: "one-way", Status: "proposed", Who: "agent", Text: "Approve the reviewed proposal.", Record: "review"},
		{Door: "two-way", Status: "decided", Who: "agent", Text: "Ratify without a proposal."},
		{Door: "two-way", Status: "ratified", Who: "@recorder", Text: "Recorded as ratified; text mentions " + showProposal358},
		{Door: "two-way", Status: "proposed", Who: "agent", Text: "Still proposed."},
		{Door: "two-way", Status: "decided", Who: "agent", Text: "Still decided."},
		{Door: "two-way", Status: "proposed", Who: "agent", Text: "Replaced after ratification."},
	} {
		if _, err := s.Decide(repo, d, "agent"); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"D-0001", "D-0006"} {
		if err := s.RatifyProposal(repo, id, "@reviewer", showProposal358); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Ratify(repo, "D-0002", "@reviewer"); err != nil {
		t.Fatal(err)
	}
	if err := s.Supersede(repo, "D-0006", "D-0005", "agent"); err != nil {
		t.Fatal(err)
	}
	if err := s.Link(repo, "D-0001", Edge{Type: Refines, To: "D-0005"}, "agent"); err != nil {
		t.Fatal(err)
	}
	if err := s.Link(repo, "D-0004", Edge{Type: Cites, To: "D-0001"}, "agent"); err != nil {
		t.Fatal(err)
	}
	return s, repo
}

func checkProposalNode358(t *testing.T, s *Store, id, want string) {
	t.Helper()
	n, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	if want == "" {
		if p, ok := fields["proposal"]; ok {
			t.Errorf("%s has an unexpected proposal field: %s", id, p)
		}
		return
	}
	var got string
	if raw, ok := fields["proposal"]; ok {
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
	}
	if got != want || n.Status != "ratified" || n.Who != "@reviewer" {
		t.Errorf("%s: proposal = %q, status = %q, who = %q; want %q, ratified, @reviewer", id, got, n.Status, n.Who, want)
	}
}

func TestRatifiedProposalSurvivesStoreAndCheckoutRebuilds(t *testing.T) {
	s, repo := proposalFixture358(t)
	check := func(store *Store) {
		checkProposalNode358(t, store, "demo/D-0001", showProposal358)
		for i := 2; i <= 6; i++ {
			checkProposalNode358(t, store, fmt.Sprintf("demo/D-%04d", i), "")
		}
	}
	check(s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	check(reopened)
	if err := reopened.Refresh(repo, "D-0001"); err != nil {
		t.Fatal(err)
	}
	check(reopened)
	fresh, err := Open(filepath.Join(t.TempDir(), "rebuilt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err := fresh.Rebuild([]Repo{repo}); err != nil {
		t.Fatal(err)
	}
	check(fresh)

	// The store keeps branch history, but its node must reflect this checkout.
	older := Repo{Name: repo.Name, Dir: t.TempDir()}
	line, err := os.ReadFile(filepath.Join(repo.JournalDir(), "D-0001.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(older.JournalDir(), "D-0001.jsonl"), strings.SplitN(string(line), "\n", 2)[0]+"\n")
	if err := fresh.Rebuild([]Repo{older}); err != nil {
		t.Fatal(err)
	}
	checkProposalNode358(t, fresh, "demo/D-0001", "")
	if err := fresh.Rebuild([]Repo{repo}); err != nil {
		t.Fatal(err)
	}
	check(fresh)
	// Reads still work from the persisted graph without the checkout's files.
	if err := os.RemoveAll(repo.JournalDir()); err != nil {
		t.Fatal(err)
	}
	check(fresh)
}

func TestVersionThreeStoreRebuildShowsRatifiedProposal(t *testing.T) {
	s, repo := proposalFixture358(t)
	// Construct the old schema explicitly so this remains an upgrade test.
	path := filepath.Join(t.TempDir(), "v3.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
CREATE TABLE node (id TEXT PRIMARY KEY, kind TEXT NOT NULL, project TEXT NOT NULL,
 date TEXT NOT NULL DEFAULT '', door TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT '',
 who TEXT NOT NULL DEFAULT '', text TEXT NOT NULL DEFAULT '', record TEXT NOT NULL DEFAULT '');
CREATE TABLE edge (src TEXT NOT NULL, type TEXT NOT NULL, dst TEXT NOT NULL, project TEXT NOT NULL,
 derived INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (src, type, dst));
CREATE TABLE repo (project TEXT PRIMARY KEY, dir TEXT NOT NULL, rebuilt TEXT NOT NULL);
CREATE TABLE journal (seq INTEGER PRIMARY KEY AUTOINCREMENT, project TEXT NOT NULL, id TEXT NOT NULL,
 hash TEXT NOT NULL, line TEXT NOT NULL, UNIQUE (project, hash));
CREATE TRIGGER journal_no_update BEFORE UPDATE ON journal BEGIN SELECT RAISE(ABORT, 'the journal only grows'); END;
CREATE TRIGGER journal_no_delete BEFORE DELETE ON journal BEGIN SELECT RAISE(ABORT, 'the journal only grows'); END;
CREATE TABLE taken (project TEXT NOT NULL, id TEXT NOT NULL, PRIMARY KEY (project, id));
CREATE TRIGGER taken_no_update BEFORE UPDATE ON taken BEGIN SELECT RAISE(ABORT, 'a taken ID stays taken'); END;
CREATE TRIGGER taken_no_delete BEFORE DELETE ON taken BEGIN SELECT RAISE(ABORT, 'a taken ID stays taken'); END;
INSERT INTO taken VALUES ('demo', 'D-0100');
PRAGMA user_version = 3;`)
	if err != nil {
		t.Fatal(err)
	}
	_, rows, err := s.Query("SELECT project, id, hash, line FROM journal ORDER BY seq", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if _, err := db.Exec("INSERT INTO journal (project, id, hash, line) VALUES (?, ?, ?, ?)", row...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("INSERT INTO repo VALUES ('demo', ?, '2026-10-31T00:00:00Z')", repo.Dir); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if err := upgraded.Rebuild([]Repo{repo}); err != nil {
		t.Fatal(err)
	}
	checkProposalNode358(t, upgraded, "demo/D-0001", showProposal358)
	_, after, err := upgraded.Query("SELECT project, id, hash, line FROM journal ORDER BY seq", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rows, after) {
		t.Error("migration or rebuild changed the retained journal")
	}
	id, err := upgraded.Decide(repo, NewDecision{Door: "two-way", Status: "decided", Who: "agent", Text: "Keep reserved IDs."}, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if id != "D-0101" {
		t.Errorf("reserved ID lost: next ID = %s", id)
	}
}

func TestDecisionToolShowsRatifiedProposalAndPreservesOtherAnswers(t *testing.T) {
	shared, repo := proposalFixture358(t)
	session, err := NewSession(shared, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("D-%04d", i)
		n, err := shared.Get("demo/" + id)
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("%s · %s door · %s · %s · %s", n.ID, n.Door, n.Status, n.Date, n.Who)
		if i == 1 {
			want += " · proposal " + showProposal358
		}
		want += "\n" + n.Text + "\n"
		if i == 1 {
			want += "Its full record: decisions/D-0001-review.md\n\nEdges from it:\n- refines demo/D-0005: Still decided.\n\nEdges to it:\n- cites demo/D-0004: Still proposed.\n"
		}
		if i == 4 {
			want += "\nEdges from it:\n- cites demo/D-0001: Approve the reviewed proposal.\n"
		}
		if i == 5 {
			want += "\nEdges from it:\n- supersedes demo/D-0006: Replaced after ratification.\n\nEdges to it:\n- refines demo/D-0001: Approve the reviewed proposal.\n"
		}
		if i == 6 {
			want += "\nEdges to it:\n- supersedes demo/D-0005: Still decided.\n"
		}
		for _, requested := range []string{id, "demo/" + id} {
			got, bad := call(t, session.Tools(false), "decision", map[string]string{"id": requested})
			if bad || got != want {
				t.Errorf("decision(%s): error=%v\ngot: %s\nwant: %s", requested, bad, got, want)
			}
		}
	}
}
