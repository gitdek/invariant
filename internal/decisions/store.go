package decisions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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
	hash    TEXT NOT NULL UNIQUE,
	line    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS journal_id ON journal (project, id);
CREATE TRIGGER IF NOT EXISTS journal_no_update BEFORE UPDATE ON journal
BEGIN SELECT RAISE(ABORT, 'the journal only grows'); END;
CREATE TRIGGER IF NOT EXISTS journal_no_delete BEFORE DELETE ON journal
BEGIN SELECT RAISE(ABORT, 'the journal only grows'); END;
CREATE TABLE IF NOT EXISTS repo (
	project TEXT PRIMARY KEY,
	dir     TEXT NOT NULL, -- the checkout it was last rebuilt from
	rebuilt TEXT NOT NULL  -- when, in RFC 3339
);
`

// schemaVersion is the schema's version. A store with another has its
// nodes, edges and checkouts dropped, since a rebuild brings them back, and
// keeps its journal, which only grows.
const schemaVersion = 1

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
	if _, err := tx.Exec(schema); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
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
	for _, e := range events {
		line, err := jsonLine(e)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO journal (project, id, hash, line) VALUES (?, ?, ?, ?)`, project, d.ID, e.Hash, string(line)); err != nil {
			return err
		}
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
		if _, err := tx.Exec(`DELETE FROM edge WHERE project = ?1; DELETE FROM node WHERE project = ?1`, name); err != nil {
			return err
		}
		for _, id := range l.ids {
			if err := put(tx, name, l.journals[id], l.decisions[id]); err != nil {
				return err
			}
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

// Decide writes a new decision: it takes the project's next ID, writes the
// decision's journal in the checkout, then puts it in the graph. The ID is
// taken under the store's write lock, past every ID the store has ever
// journaled for the project and every one in the checkout, so processes and
// checkouts on one machine never take the same one. A branch that's
// abandoned leaves a gap in the numbers.
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
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var journaled sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(CAST(substr(id, length(?1) + 4) AS INTEGER)) FROM journal WHERE project = ?1`, repo.Name).Scan(&journaled); err != nil {
		return "", err
	}
	next, err := highest(repo)
	if err != nil {
		return "", err
	}
	if journaled.Valid && int(journaled.Int64) > next {
		next = int(journaled.Int64)
	}
	id := fmt.Sprintf("D-%04d", max(next+1, 1))
	now := time.Now().UTC()
	e := Event{At: now.Format(time.RFC3339), By: by, Op: OpDecide, ID: id, Date: now.Format("2006-01-02"),
		Door: d.Door, Status: d.Status, Who: d.Who, Text: text, Edges: d.Edges}
	if d.Record != "" {
		e.Record = id + "-" + d.Record + ".md"
	}
	if _, err := fold(repo.Name, []Event{e}); err != nil {
		return "", err
	}
	if err := appendLine(repo, &e, true); err != nil {
		return "", err
	}
	if err := putFile(tx, repo, id); err != nil {
		return "", err
	}
	return id, tx.Commit()
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
func (s *Store) update(repo Repo, id, by string, next func(Decision) (Event, error)) error {
	full, err := Qualify(repo.Name, id)
	if err != nil {
		return err
	}
	short := full[strings.LastIndex(full, "/")+1:]
	if full != repo.Name+"/"+short {
		return fmt.Errorf("%s belongs to another project, so it's changed in that project's repository", full)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
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
	if err := appendLine(repo, &e, false); err != nil {
		return err
	}
	if err := putFile(tx, repo, short); err != nil {
		return err
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

// Dependents is everything that rests on a decision, transitively: the
// decisions that refine or cite it, and the SPEC lines, docs and code that
// cite or implement any of them. It's what reopening the decision touches.
// UNION keeps each node once, so the walk always ends, however the graph
// loops.
func (s *Store) Dependents(id string) ([]Node, error) {
	return s.nodes(`WITH RECURSIVE dep(id) AS (
			SELECT ?1
			UNION
			SELECT e.src FROM edge e JOIN dep d ON e.dst = d.id WHERE e.type IN ('refines', 'cites', 'implements')
		)
		SELECT `+nodeColumns+` FROM dep JOIN node n ON n.id = dep.id WHERE dep.id != ?1
		ORDER BY n.kind, n.id`, id)
}

// Implementers is the code that implements a decision, or a decision that
// rests on it.
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

// Grounds is what a decision rests on, transitively: the decisions it
// refines or cites. It says why the decision exists.
func (s *Store) Grounds(id string) ([]Node, error) {
	return s.nodes(`WITH RECURSIVE g(id) AS (
			SELECT ?1
			UNION
			SELECT e.dst FROM edge e JOIN g ON e.src = g.id WHERE e.type IN ('refines', 'cites')
		)
		SELECT `+nodeColumns+` FROM g JOIN node n ON n.id = g.id WHERE g.id != ?1 AND n.kind = 'decision'
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
