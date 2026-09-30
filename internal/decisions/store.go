package decisions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	idcore "github.com/gitdek/invariant/factory/ids/ids"
	"github.com/gitdek/invariant/factory/store-journal/storejournal"
	_ "modernc.org/sqlite" // a pure-Go SQLite, so Invariant needs no cgo (D-0096)
)

// schema is the graph: nodes, typed edges between them, a journal that
// mirrors every repository's journal lines and refuses edits, and the
// checkout each project was last rebuilt from. An edge's project is the one
// whose journal or text records it, so rebuilding a project replaces its
// nodes and edges and leaves every other project's alone.
const schema = `
CREATE TABLE IF NOT EXISTS node (
	id      TEXT PRIMARY KEY,       -- invariant/D-0096, or invariant/SPEC.md:92 for a line that cites one
	kind    TEXT NOT NULL,          -- decision, spec, doc or code
	project TEXT NOT NULL,
	date    TEXT NOT NULL DEFAULT '',
	door    TEXT NOT NULL DEFAULT '',
	status  TEXT NOT NULL DEFAULT '',
	who     TEXT NOT NULL DEFAULT '',
	text    TEXT NOT NULL DEFAULT '',
	record  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS node_project ON node (project, kind);
CREATE TABLE IF NOT EXISTS edge (
	src     TEXT NOT NULL,
	type    TEXT NOT NULL,
	dst     TEXT NOT NULL,
	project TEXT NOT NULL,
	derived INTEGER NOT NULL DEFAULT 0, -- read from text on a rebuild, rather than written to a journal
	PRIMARY KEY (src, type, dst)
);
CREATE INDEX IF NOT EXISTS edge_to ON edge (dst, type);
CREATE INDEX IF NOT EXISTS edge_project ON edge (project);
CREATE TABLE IF NOT EXISTS journal (
	seq     INTEGER PRIMARY KEY AUTOINCREMENT,
	project TEXT NOT NULL,
	id      TEXT NOT NULL,
	hash    TEXT NOT NULL,
	line    TEXT NOT NULL,
	UNIQUE (project, hash) -- a line's hash doesn't cover its project, so two projects can write the same line
);
CREATE INDEX IF NOT EXISTS journal_id ON journal (project, id);
CREATE TRIGGER IF NOT EXISTS journal_no_update BEFORE UPDATE ON journal
BEGIN SELECT RAISE(ABORT, 'the journal only grows'); END;
CREATE TRIGGER IF NOT EXISTS journal_no_delete BEFORE DELETE ON journal
BEGIN SELECT RAISE(ABORT, 'the journal only grows'); END;
-- The IDs decides have taken, each recorded before its journal file is
-- written, so a decide that stops partway leaves a gap, never an ID another
-- decide takes again (#192). An ID once taken stays taken.
CREATE TABLE IF NOT EXISTS taken (
	project TEXT NOT NULL,
	id      TEXT NOT NULL,
	PRIMARY KEY (project, id)
);
CREATE TRIGGER IF NOT EXISTS taken_no_update BEFORE UPDATE ON taken
BEGIN SELECT RAISE(ABORT, 'a taken ID stays taken'); END;
CREATE TRIGGER IF NOT EXISTS taken_no_delete BEFORE DELETE ON taken
BEGIN SELECT RAISE(ABORT, 'a taken ID stays taken'); END;
CREATE TABLE IF NOT EXISTS repo (
	project TEXT PRIMARY KEY,
	dir     TEXT NOT NULL, -- the checkout it was last rebuilt from
	rebuilt TEXT NOT NULL  -- when, in RFC 3339
);
`

// schemaVersion is the schema's version. A store with another has its
// nodes, edges and checkouts dropped, since a rebuild brings them back, and
// keeps its journal, which only grows.
const schemaVersion = 3

// Store is the decision graph in one SQLite file. Every process that needs
// it opens it directly: SQLite in WAL mode lets many read while one writes,
// and a writer that finds another waits (D-0096).
type Store struct {
	db   *sql.DB
	Path string
}

// DefaultPath is the store on this machine.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "invariant", "decisions.db"), nil
}

// Open opens the store at path for reading and writing, creating it if it
// doesn't exist.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("the decision store at %s: %w", path, err)
	}
	return &Store{db: db, Path: path}, nil
}

// migrate brings the store to this schema, in one transaction, so two
// processes opening it at once don't both change it.
func migrate(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version == schemaVersion {
		return nil
	}
	if _, err := tx.Exec(`DROP TABLE IF EXISTS edge; DROP TABLE IF EXISTS node; DROP TABLE IF EXISTS repo`); err != nil {
		return err
	}
	if err := perProject(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(schema); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// perProject moves a journal whose lines were unique by hash alone, as
// schema versions 1 and 2 had it, to lines unique within their project,
// copying every line it holds, in order. Under the old key, a project's line
// that another project had written word for word was never journaled (#195).
func perProject(tx *sql.Tx) error {
	var table string
	err := tx.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'journal'`).Scan(&table)
	if errors.Is(err, sql.ErrNoRows) || err == nil && strings.Contains(table, "UNIQUE (project, hash)") {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
CREATE TABLE journal_by_project (
	seq     INTEGER PRIMARY KEY AUTOINCREMENT,
	project TEXT NOT NULL,
	id      TEXT NOT NULL,
	hash    TEXT NOT NULL,
	line    TEXT NOT NULL,
	UNIQUE (project, hash)
);
INSERT INTO journal_by_project (seq, project, id, hash, line) SELECT seq, project, id, hash, line FROM journal ORDER BY seq;
DROP TABLE journal;
ALTER TABLE journal_by_project RENAME TO journal;`)
	return err
}

// OpenReadOnly opens the store for queries only: nothing written through it
// can change it, whatever SQL is sent.
func OpenReadOnly(path string) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(30000)&_pragma=query_only(1)")
	if err != nil {
		return nil, err
	}
	return &Store{db: db, Path: path}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// put writes a decision into the graph within tx: its journal lines, its
// node, the edges its journal records, and the citations its words make. It
// replaces what the graph had for the decision, so putting it again after a
// new line is the same as rebuilding.
func put(tx *sql.Tx, project string, events []Event, d Decision) error {
	if err := journalLines(tx, project, d.ID, events); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO node (id, kind, project, date, door, status, who, text, record) VALUES (?, 'decision', ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET date = excluded.date, door = excluded.door, status = excluded.status, who = excluded.who, text = excluded.text, record = excluded.record`,
		d.ID, project, d.Date, d.Door, d.Status, d.Who, d.Text, d.Record); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM edge WHERE src = ? AND project = ? AND derived = 0`, d.ID, project); err != nil {
		return err
	}
	for _, edge := range d.Edges {
		if err := addEdge(tx, d.ID, edge.Type, edge.To, project); err != nil {
			return err
		}
	}
	if d.SupersededBy != "" {
		if err := addEdge(tx, d.SupersededBy, Supersedes, d.ID, project); err != nil {
			return err
		}
	}
	// A citation read from words gives way to an edge the journal records.
	if _, err := tx.Exec(`DELETE FROM edge WHERE src = ?1 AND derived = 1 AND dst IN (SELECT dst FROM edge WHERE src = ?1 AND derived = 0)`, d.ID); err != nil {
		return err
	}
	return citeFrom(tx, d.ID, project, d.Text)
}

// journalLines has the store journal a decision's lines, keeping every line
// it holds already.
func journalLines(tx *sql.Tx, project, id string, events []Event) error {
	for _, e := range events {
		line, err := jsonLine(e)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO journal (project, id, hash, line) VALUES (?, ?, ?, ?)`, project, id, e.Hash, string(line)); err != nil {
			return err
		}
	}
	return nil
}

// addEdge records an edge a journal holds, in place of a citation that was
// only read from words.
func addEdge(tx *sql.Tx, src, typ, dst, project string) error {
	_, err := tx.Exec(`INSERT INTO edge (src, type, dst, project) VALUES (?, ?, ?, ?)
		ON CONFLICT (src, type, dst) DO UPDATE SET derived = 0`, src, typ, dst, project)
	return err
}

// Rebuild replaces each repository's project in the store with what its
// checkout holds: every decision in its journal, then what cites each one in
// its text, which is the decisions' own words, the docs and the code. Every
// other project stays as it was, and the journal table keeps every line it
// ever had, since it only grows.
func (s *Store) Rebuild(repos []Repo) error {
	type loaded struct {
		repo      Repo
		journals  map[string][]Event
		ids       []string
		decisions map[string]Decision
	}
	var all []loaded
	seen := map[string]bool{}
	for _, repo := range repos {
		if seen[repo.Name] {
			return fmt.Errorf("two checkouts of %s", repo.Name)
		}
		seen[repo.Name] = true
		journals, ids, err := Journals(repo)
		if err != nil {
			return err
		}
		l := loaded{repo, journals, ids, map[string]Decision{}}
		for _, id := range ids {
			if l.decisions[id], err = fold(repo.Name, journals[id]); err != nil {
				return err
			}
		}
		all = append(all, l)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	for _, l := range all {
		name := l.repo.Name
		// The store's journal only grows, which factory/store-journal proves
		// (#195): what the store must hold after the rebuild is the core's
		// Rebuild step from what it holds now and what the checkout holds.
		floor, err := journalFloor(tx, name, l.journals)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM edge WHERE project = ?1; DELETE FROM node WHERE project = ?1`, name); err != nil {
			return err
		}
		for _, id := range l.ids {
			if err := put(tx, name, l.journals[id], l.decisions[id]); err != nil {
				return err
			}
		}
		if err := holdsFloor(tx, name, floor); err != nil {
			return err
		}
		if err := deriveCitations(tx, l.repo); err != nil {
			return err
		}
		dir, err := filepath.Abs(l.repo.Dir)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO repo (project, dir, rebuilt) VALUES (?, ?, ?)
			ON CONFLICT (project) DO UPDATE SET dir = excluded.dir, rebuilt = excluded.rebuilt`, name, dir, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// journalFloor is how many lines of each of a project's decision files the
// store must hold once it's rebuilt from a checkout, by factory/store-journal,
// its proved core (#195). Each decision file is one of the core's origins,
// whose lines the store and the checkout hold as far as a count: the core's
// Rebuild takes the larger of the two, so the store never holds fewer lines
// than it did, or than the checkout does.
func journalFloor(tx *sql.Tx, project string, journals map[string][]Event) (map[string]int, error) {
	counts, err := journalCounts(tx, project)
	if err != nil {
		return nil, err
	}
	var files []string
	seen := map[string]bool{}
	for id := range counts {
		seen[id] = true
		files = append(files, id)
	}
	for id := range journals {
		if full := project + "/" + id; !seen[full] {
			seen[full] = true
			files = append(files, full)
		}
	}
	if len(files) == 0 {
		return nil, nil
	}
	sort.Strings(files)
	capacity := 0
	for _, f := range files {
		capacity = max(capacity, counts[f], len(journals[f[len(project)+1:]]))
	}
	j := storejournal.New(len(files), capacity)
	for p, f := range files {
		j.Store[p], j.Files[0][p] = counts[f], len(journals[f[len(project)+1:]])
	}
	j.Rebuild(0)
	floor := map[string]int{}
	for p, f := range files {
		floor[f] = j.Store[p]
	}
	return floor, nil
}

// holdsFloor refuses a rebuild after which the store holds fewer lines of a
// decision file than journalFloor says it must.
func holdsFloor(tx *sql.Tx, project string, floor map[string]int) error {
	counts, err := journalCounts(tx, project)
	if err != nil {
		return err
	}
	for f, least := range floor {
		if counts[f] < least {
			return fmt.Errorf("the rebuild would leave the store with %d of %s's journal lines, fewer than the %d factory/store-journal says it keeps", counts[f], f, least)
		}
	}
	return nil
}

// journalCounts is how many lines of each of a project's decision files the
// store's journal holds.
func journalCounts(tx *sql.Tx, project string) (map[string]int, error) {
	rows, err := tx.Query(`SELECT id, COUNT(*) FROM journal WHERE project = ? GROUP BY id`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		counts[id] = n
	}
	return counts, rows.Err()
}

// Count is how many decisions a project has in the store, and how many
// edges its journal and its text record.
func (s *Store) Count(project string) (decisions, edges int, err error) {
	if err = s.db.QueryRow(`SELECT count(*) FROM node WHERE project = ? AND kind = 'decision'`, project).Scan(&decisions); err != nil {
		return 0, 0, err
	}
	err = s.db.QueryRow(`SELECT count(*) FROM edge WHERE project = ?`, project).Scan(&edges)
	return decisions, edges, err
}

// Checkouts is every project the store holds, and the checkout each was
// last rebuilt from.
func (s *Store) Checkouts() ([]Repo, error) {
	rows, err := s.db.Query(`SELECT project, dir FROM repo ORDER BY project`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Repo
	for rows.Next() {
		var r Repo
		if err := rows.Scan(&r.Name, &r.Dir); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// NewDecision is a decision to write.
type NewDecision struct {
	Door   string // one-way or two-way
	Status string // decided, proposed or ratified
	Who    string // who made it: a person, such as @gitdek, or agent
	Text   string
	Record string // a one-way door's record, by its slug: decisions/D-NNNN-slug.md
	Edges  []Edge
}

var slug = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Decide writes a new decision, in the order factory/ids proves (#192):
// holding the store's write lock from start to finish, it takes one more
// than the highest ID the store has taken or journaled for the project, or
// the checkout holds, has the store record it, writes the decision's journal
// in the checkout, and puts it in the graph. So processes and checkouts on
// one machine never take the same ID: a decide that stops after the store
// records its ID leaves a gap, and so does a branch that's abandoned. The
// store journals the decision's first line before the checkout writes it,
// as factory/store-journal proves (#195).
func (s *Store) Decide(repo Repo, d NewDecision, by string) (string, error) {
	text := strings.Join(strings.Fields(d.Text), " ")
	switch {
	case d.Door != "one-way" && d.Door != "two-way":
		return "", fmt.Errorf("a decision's door is one-way or two-way, not %q", d.Door)
	case d.Status != "decided" && d.Status != "proposed" && d.Status != "ratified":
		return "", fmt.Errorf("a new decision is decided, proposed or ratified, not %q", d.Status)
	case d.Door == "one-way" && d.Status == "decided":
		return "", errors.New("a one-way door is proposed until @gitdek ratifies it, never decided by an agent (AGENTS.md)")
	case d.Door == "one-way" && d.Record == "":
		return "", errors.New("a one-way door has a full record: name it with its slug, for decisions/D-NNNN-slug.md")
	case d.Record != "" && !slug.MatchString(d.Record):
		return "", fmt.Errorf("a record's slug is lowercase words joined by hyphens, not %q", d.Record)
	case d.Status == "ratified" && !strings.HasPrefix(d.Who, "@"):
		return "", errors.New("only a person ratifies, so a ratified decision's who is a person, such as @gitdek")
	case text == "":
		return "", errors.New("a decision says what was decided")
	case strings.Contains(text, "|"):
		return "", errors.New("a decision's words can't hold |, which would split its row in decisions/log.md")
	}
	for _, e := range d.Edges {
		if err := s.checkEdge(repo, e); err != nil {
			return "", err
		}
	}
	now := time.Now().UTC()
	e := Event{At: now.Format(time.RFC3339), By: by, Op: OpDecide, Date: now.Format("2006-01-02"),
		Door: d.Door, Status: d.Status, Who: d.Who, Text: text, Edges: d.Edges}
	// A decision the fold would refuse takes no ID.
	if _, err := fold(repo.Name, []Event{e}); err != nil {
		return "", err
	}
	unlock, err := s.lockWrites()
	if err != nil {
		return "", err
	}
	defer unlock()
	id, c, err := s.take(repo)
	if err != nil {
		return "", err
	}
	e.ID = id
	if d.Record != "" {
		e.Record = id + "-" + d.Record + ".md"
	}
	if err := e.chain(nil); err != nil {
		return "", err
	}
	if err := s.journalFirst(repo.Name, nil, e); err != nil {
		return "", err
	}
	if !c.core.Write(c.checkout, idcore.Decision{By: 1, Number: 1}) {
		return "", errOffCore("write the journal file of " + id)
	}
	if err := writeLine(repo, e, true); err != nil {
		return "", err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err := putFile(tx, repo, id); err != nil {
		return "", err
	}
	if !c.core.Finish(c.checkout) {
		return "", errOffCore("finish deciding " + id)
	}
	return id, tx.Commit()
}

// onCore is a decide as factory/ids, its proved core, sees it (#192): the IDs
// the store has taken, the IDs the checkout holds, and how far the decide
// has come. Each of the decide's steps is the core's first, and a step the
// core refuses isn't taken.
type onCore struct {
	core     *idcore.Core
	checkout *idcore.Checkout
}

// errOffCore is a step factory/ids refused. It never should be: the steps
// come in the core's order.
func errOffCore(step string) error {
	return fmt.Errorf("the decision store can't %s: factory/ids, its proved core, refuses the step", step)
}

// take takes a decide's ID on factory/ids, and has the store record it, in
// a transaction of its own, so the ID is the store's before its journal file
// is written. The core's Take chooses it: one more than the highest ID the
// store has taken or journaled for the project, or the checkout holds.
func (s *Store) take(repo Repo) (string, onCore, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", onCore{}, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT CAST(substr(id, length(?1) + 4) AS INTEGER) FROM journal WHERE project = ?1
		UNION SELECT CAST(substr(id, 3) AS INTEGER) FROM taken WHERE project = ?1`, repo.Name)
	if err != nil {
		return "", onCore{}, err
	}
	var taken []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return "", onCore{}, err
		}
		taken = append(taken, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", onCore{}, err
	}
	held, err := heldIDs(repo)
	if err != nil {
		return "", onCore{}, err
	}
	// The core holds room for one more ID than any taken or held.
	top := 0
	for _, n := range append(append([]int(nil), taken...), held...) {
		top = max(top, n)
	}
	c := onCore{core: idcore.New(top + 1), checkout: idcore.NewCheckout(top + 1)}
	for _, n := range taken {
		if n > 0 {
			c.core.Store[n-1] = true
		}
	}
	for _, n := range held {
		if n > 0 {
			c.checkout.Files[n-1] = idcore.Decision{By: 1, Number: 1}
		}
	}
	if !c.core.Take(c.checkout, 1) {
		return "", onCore{}, errOffCore("take an ID")
	}
	id := fmt.Sprintf("D-%04d", c.checkout.Next)
	if !c.core.Reserve(c.checkout) {
		return "", onCore{}, errOffCore("record " + id)
	}
	if _, err := tx.Exec(`INSERT INTO taken (project, id) VALUES (?, ?)`, repo.Name, id); err != nil {
		return "", onCore{}, err
	}
	return id, c, tx.Commit()
}

// lockWrites holds the store's write lock, a lock on a file beside it, until
// the function it returns is called, so one decide or change at a time
// writes a journal line. A write that stops lets it go with its process.
func (s *Store) lockWrites() (func(), error) {
	f, err := os.OpenFile(s.Path+".decide.lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// checkEdge refuses an edge a decision can't have: an unknown type, code's
// implements, or a decision in the same project that doesn't exist.
func (s *Store) checkEdge(repo Repo, e Edge) error {
	if !edgeTypes[e.Type] || e.Type == Implements {
		return fmt.Errorf("a decision refines, supersedes, reopens or cites another, not %q", e.Type)
	}
	return exists(repo, e.To)
}

// exists refuses an ID in the repository's own project that its checkout
// has no journal for. Another project's decisions are checked against its
// own journal, where both are loaded.
func exists(repo Repo, id string) error {
	full, err := Qualify(repo.Name, id)
	if err != nil {
		return err
	}
	short := full[strings.LastIndex(full, "/")+1:]
	if full != repo.Name+"/"+short {
		return nil
	}
	if _, err := os.Stat(filepath.Join(repo.JournalDir(), short+".jsonl")); err != nil {
		return fmt.Errorf("there's no decision %s in %s", short, repo.Dir)
	}
	return nil
}

// Ratify records a person's ratification of a proposed or decided decision.
func (s *Store) Ratify(repo Repo, id, by string) error {
	return s.update(repo, id, by, func(d Decision) (Event, error) {
		if d.Status == "ratified" || d.Status == "superseded" {
			return Event{}, fmt.Errorf("%s is %s already", id, d.Status)
		}
		return Event{Op: OpRatify}, nil
	})
}

// Supersede records that a later decision replaces id.
func (s *Store) Supersede(repo Repo, id, with, by string) error {
	if err := exists(repo, with); err != nil {
		return err
	}
	return s.update(repo, id, by, func(d Decision) (Event, error) {
		if d.Status == "superseded" {
			return Event{}, fmt.Errorf("%s is superseded already", id)
		}
		return Event{Op: OpSupersede, With: with}, nil
	})
}

// Link records a typed edge from id to another decision.
func (s *Store) Link(repo Repo, id string, edge Edge, by string) error {
	if err := s.checkEdge(repo, edge); err != nil {
		return err
	}
	return s.update(repo, id, by, func(Decision) (Event, error) {
		return Event{Op: OpLink, Edges: []Edge{edge}}, nil
	})
}

// update appends one line to a decision's journal in the checkout, and puts
// the decision in the graph again, under the store's write lock. What the
// line may say depends on the decision as the checkout's journal has it.
// The store journals the line before the checkout writes it, as
// factory/store-journal proves (#195).
func (s *Store) update(repo Repo, id, by string, next func(Decision) (Event, error)) error {
	full, err := Qualify(repo.Name, id)
	if err != nil {
		return err
	}
	short := full[strings.LastIndex(full, "/")+1:]
	if full != repo.Name+"/"+short {
		return fmt.Errorf("%s belongs to another project, so it's changed in that project's repository", full)
	}
	unlock, err := s.lockWrites()
	if err != nil {
		return err
	}
	defer unlock()
	events, err := ReadJournal(filepath.Join(repo.JournalDir(), short+".jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("there's no decision %s in %s", short, repo.Dir)
	}
	if err != nil {
		return err
	}
	d, err := fold(repo.Name, events)
	if err != nil {
		return err
	}
	e, err := next(d)
	if err != nil {
		return err
	}
	e.At, e.By, e.ID = time.Now().UTC().Format(time.RFC3339), by, short
	if _, err := fold(repo.Name, append(events, e)); err != nil {
		return err
	}
	if err := e.chain(events); err != nil {
		return err
	}
	if err := s.journalFirst(repo.Name, events, e); err != nil {
		return err
	}
	if err := writeLine(repo, e, false); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := putFile(tx, repo, short); err != nil {
		return err
	}
	return tx.Commit()
}

// journalFirst has the store journal a decision's next line, and every line
// before it the checkout holds, before the checkout writes it: the Write
// step of factory/store-journal, its proved core (#195), taken first. So a
// checkout never holds a line the store doesn't. A write that stops before
// the checkout's file has its line leaves the line in the store alone,
// which keeps it, as it keeps the lines of a branch that's abandoned.
func (s *Store) journalFirst(project string, held []Event, next Event) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	id := project + "/" + next.ID
	counts, err := journalCounts(tx, project)
	if err != nil {
		return err
	}
	j := storejournal.New(1, max(counts[id], len(held)+1))
	j.Store[0], j.Files[0][0] = counts[id], len(held)
	if !j.Write(0) {
		return fmt.Errorf("the decision store can't journal %s's next line: factory/store-journal, its proved core, refuses the step", id)
	}
	if err := journalLines(tx, project, id, append(held[:len(held):len(held)], next)); err != nil {
		return err
	}
	if counts, err = journalCounts(tx, project); err != nil {
		return err
	}
	if counts[id] < j.Store[0] {
		return fmt.Errorf("the store would hold %d of %s's journal lines, fewer than the %d factory/store-journal says it journals", counts[id], id, j.Store[0])
	}
	return tx.Commit()
}

// putFile puts one decision into the graph, as its journal file in the
// checkout has it.
func putFile(tx *sql.Tx, repo Repo, short string) error {
	events, err := ReadJournal(filepath.Join(repo.JournalDir(), short+".jsonl"))
	if err != nil {
		return err
	}
	d, err := fold(repo.Name, events)
	if err != nil {
		return err
	}
	return put(tx, repo.Name, events, d)
}

// Node is one node of the graph.
type Node struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Project string `json:"project"`
	Date    string `json:"date,omitempty"`
	Door    string `json:"door,omitempty"`
	Status  string `json:"status,omitempty"`
	Who     string `json:"who,omitempty"`
	Text    string `json:"text,omitempty"`
	Record  string `json:"record,omitempty"`
}

const nodeColumns = `n.id, n.kind, n.project, n.date, n.door, n.status, n.who, n.text, n.record`

func scanNodes(rows *sql.Rows) ([]Node, error) {
	defer rows.Close()
	var out []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Kind, &n.Project, &n.Date, &n.Door, &n.Status, &n.Who, &n.Text, &n.Record); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Get is one node.
func (s *Store) Get(id string) (Node, error) {
	nodes, err := s.nodes(`SELECT `+nodeColumns+` FROM node n WHERE n.id = ?`, id)
	if err != nil {
		return Node{}, err
	}
	if len(nodes) == 0 {
		return Node{}, fmt.Errorf("there's no %s", id)
	}
	return nodes[0], nil
}

// A Neighbor is a node at the other end of an edge: the edge's type, and
// whether it points out from the node asked about or in to it. A node whose
// project isn't loaded has only its ID.
type Neighbor struct {
	Type string `json:"type"`
	Out  bool   `json:"out"`
	Node Node   `json:"node"`
}

// Neighbors is every edge at a node, out from it first.
func (s *Store) Neighbors(id string) ([]Neighbor, error) {
	cols := `COALESCE(n.kind, ''), COALESCE(n.project, ''), COALESCE(n.date, ''), COALESCE(n.door, ''), COALESCE(n.status, ''), COALESCE(n.who, ''), COALESCE(n.text, ''), COALESCE(n.record, '')`
	rows, err := s.db.Query(`SELECT e.type, 1, e.dst, `+cols+` FROM edge e LEFT JOIN node n ON n.id = e.dst WHERE e.src = ?1
		UNION ALL
		SELECT e.type, 0, e.src, `+cols+` FROM edge e LEFT JOIN node n ON n.id = e.src WHERE e.dst = ?1
		ORDER BY 2 DESC, 1, 3`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Neighbor
	for rows.Next() {
		var nb Neighbor
		n := &nb.Node
		if err := rows.Scan(&nb.Type, &nb.Out, &n.ID, &n.Kind, &n.Project, &n.Date, &n.Door, &n.Status, &n.Who, &n.Text, &n.Record); err != nil {
			return nil, err
		}
		out = append(out, nb)
	}
	return out, rows.Err()
}

func (s *Store) nodes(query string, args ...any) ([]Node, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	return scanNodes(rows)
}

// Dependents is everything that rests on a decision: every decision that
// refines it, directly or through others, and every decision, SPEC line,
// doc or piece of code that mentions or implements any of them. It's what
// reopening the decision touches. A mention counts one step and no further,
// since a citation is often only an example, and following citations on
// would make every decision rest on nearly every other one. UNION keeps
// each node once, so the walk ends however the graph loops.
func (s *Store) Dependents(id string) ([]Node, error) {
	return s.nodes(`WITH RECURSIVE chain(id) AS (
			SELECT ?1
			UNION
			SELECT e.src FROM edge e JOIN chain c ON e.dst = c.id WHERE e.type = 'refines'
		)
		SELECT `+nodeColumns+` FROM node n WHERE n.id != ?1 AND n.id IN (
			SELECT id FROM chain
			UNION
			SELECT e.src FROM edge e JOIN chain c ON e.dst = c.id WHERE e.type IN ('cites', 'implements', 'reopens')
		)
		ORDER BY n.kind, n.id`, id)
}

// Implementers is the code that implements a decision, or a decision that
// refines it.
func (s *Store) Implementers(id string) ([]Node, error) {
	all, err := s.Dependents(id)
	if err != nil {
		return nil, err
	}
	var out []Node
	for _, n := range all {
		if n.Kind == "code" {
			out = append(out, n)
		}
	}
	return out, nil
}

// Grounds is what a decision rests on: the decisions it refines, directly
// or through others, and the ones any of them cites or reopens. It says why
// the decision exists.
func (s *Store) Grounds(id string) ([]Node, error) {
	return s.nodes(`WITH RECURSIVE chain(id) AS (
			SELECT ?1
			UNION
			SELECT e.dst FROM edge e JOIN chain c ON e.src = c.id WHERE e.type = 'refines'
		)
		SELECT `+nodeColumns+` FROM node n WHERE n.kind = 'decision' AND n.id != ?1 AND n.id IN (
			SELECT id FROM chain
			UNION
			SELECT e.dst FROM edge e JOIN chain c ON e.src = c.id WHERE e.type IN ('cites', 'reopens')
		)
		ORDER BY n.id`, id)
}

// Search finds decisions whose words contain every one of the given words.
func (s *Store) Search(words string) ([]Node, error) {
	q := `SELECT ` + nodeColumns + ` FROM node n WHERE n.kind = 'decision'`
	var args []any
	for _, w := range strings.Fields(words) {
		q += ` AND n.text LIKE ? ESCAPE '\'`
		args = append(args, "%"+strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(w)+"%")
	}
	return s.nodes(q+` ORDER BY n.id`, args...)
}

// Decisions is every decision in a project, in the order of their IDs.
func (s *Store) Decisions(project string) ([]Node, error) {
	return s.nodes(`SELECT `+nodeColumns+` FROM node n WHERE n.kind = 'decision' AND n.project = ?
		ORDER BY CAST(substr(n.id, length(?) + 4) AS INTEGER)`, project, project)
}

// Query runs SQL someone wrote, such as an agent, and returns its rows. It
// stops after timeout. Run it on a store opened with OpenReadOnly, so the SQL
// can't change anything.
func (s *Store) Query(query string, timeout time.Duration) ([]string, [][]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	var out [][]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		out = append(out, vals)
		if len(out) >= 10000 {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return cols, out, ctx.Err()
}
