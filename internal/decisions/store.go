package decisions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // a pure-Go SQLite, so Invariant needs no cgo (D-0096)
)

// schema is the graph: nodes, typed edges between them, and a journal that
// mirrors every repository's journal lines and refuses edits.
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
CREATE TABLE IF NOT EXISTS edge (
	src     TEXT NOT NULL,
	type    TEXT NOT NULL,
	dst     TEXT NOT NULL,
	derived INTEGER NOT NULL DEFAULT 0, -- read from text on a rebuild, rather than written to a journal
	PRIMARY KEY (src, type, dst)
);
CREATE INDEX IF NOT EXISTS edge_to ON edge (dst, type);
CREATE TABLE IF NOT EXISTS journal (
	seq     INTEGER PRIMARY KEY AUTOINCREMENT,
	project TEXT NOT NULL,
	id      TEXT NOT NULL,
	hash    TEXT NOT NULL UNIQUE,
	line    TEXT NOT NULL
);
CREATE TRIGGER IF NOT EXISTS journal_no_update BEFORE UPDATE ON journal
BEGIN SELECT RAISE(ABORT, 'the journal only grows'); END;
CREATE TRIGGER IF NOT EXISTS journal_no_delete BEFORE DELETE ON journal
BEGIN SELECT RAISE(ABORT, 'the journal only grows'); END;
`

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
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("the decision store at %s: %w", path, err)
	}
	return &Store{db: db, Path: path}, nil
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

// apply puts one journal line into the graph, within tx.
func apply(tx *sql.Tx, project string, e Event, line []byte) error {
	id := project + "/" + e.ID
	if _, err := tx.Exec(`INSERT OR IGNORE INTO journal (project, id, hash, line) VALUES (?, ?, ?, ?)`, project, id, e.Hash, string(line)); err != nil {
		return err
	}
	switch e.Op {
	case OpImport, OpDecide:
		if _, err := tx.Exec(`INSERT INTO node (id, kind, project, date, door, status, who, text, record) VALUES (?, 'decision', ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET date = excluded.date, door = excluded.door, status = excluded.status, who = excluded.who, text = excluded.text, record = excluded.record`,
			id, project, e.Date, e.Door, e.Status, e.Who, e.Text, e.Record); err != nil {
			return err
		}
	case OpRatify:
		if _, err := tx.Exec(`UPDATE node SET status = 'ratified', who = ? WHERE id = ?`, e.By, id); err != nil {
			return err
		}
	case OpSupersede:
		with, err := Qualify(project, e.With)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE node SET status = 'superseded' WHERE id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO edge (src, type, dst) VALUES (?, ?, ?)`, with, Supersedes, id); err != nil {
			return err
		}
	case OpLink:
	default:
		return fmt.Errorf("%s: unknown op %q", id, e.Op)
	}
	for _, edge := range e.Edges {
		to, err := Qualify(project, edge.To)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO edge (src, type, dst) VALUES (?, ?, ?)`, id, edge.Type, to); err != nil {
			return err
		}
	}
	return nil
}

// Rebuild replays every repository's journal into the store, from nothing,
// then reads what cites each decision in each repository's text: the
// decisions' own words, the docs and the code. The journal table keeps what
// it already had, since it only grows.
func (s *Store) Rebuild(repos []Repo) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM edge; DELETE FROM node`); err != nil {
		return err
	}
	for _, repo := range repos {
		journals, ids, err := Journals(repo)
		if err != nil {
			return err
		}
		for _, id := range ids {
			for _, e := range journals[id] {
				line, err := jsonLine(e)
				if err != nil {
					return err
				}
				if err := apply(tx, repo.Name, e, line); err != nil {
					return err
				}
			}
		}
	}
	for _, repo := range repos {
		if err := deriveCitations(tx, repo); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// NewDecision is a decision to write.
type NewDecision struct {
	Door   string // one-way or two-way
	Status string // decided, proposed or ratified
	Who    string // @gitdek, or agent
	Text   string
	Record string
	Edges  []Edge
}

// Decide writes a new decision: it takes the next ID in the repository's
// project, writes the decision's journal, then puts it in the graph. The ID
// is taken under the store's write lock, so two processes never take the
// same one.
func (s *Store) Decide(repo Repo, d NewDecision, by string) (string, error) {
	if d.Door != "one-way" && d.Door != "two-way" {
		return "", fmt.Errorf("a decision's door is one-way or two-way, not %q", d.Door)
	}
	if d.Status != "decided" && d.Status != "proposed" && d.Status != "ratified" {
		return "", fmt.Errorf("a new decision is decided, proposed or ratified, not %q", d.Status)
	}
	if d.Door == "one-way" && d.Status == "decided" {
		return "", errors.New("a one-way door is proposed until @gitdek ratifies it, never decided by an agent (AGENTS.md)")
	}
	if strings.TrimSpace(d.Text) == "" {
		return "", errors.New("a decision says what was decided")
	}
	for _, e := range d.Edges {
		if !edgeTypes[e.Type] || e.Type == Implements {
			return "", fmt.Errorf("a decision refines, supersedes, reopens or cites another, not %q", e.Type)
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var last sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(CAST(substr(id, length(?) + 4) AS INTEGER)) FROM node WHERE kind = 'decision' AND project = ?`, repo.Name, repo.Name).Scan(&last); err != nil {
		return "", err
	}
	id := fmt.Sprintf("D-%04d", last.Int64+1)
	if !last.Valid {
		id = "D-0001"
	}
	now := time.Now().UTC()
	e := Event{At: now.Format(time.RFC3339), By: by, Op: OpDecide, ID: id, Date: now.Format("2006-01-02"),
		Door: d.Door, Status: d.Status, Who: d.Who, Text: strings.TrimSpace(d.Text), Record: d.Record, Edges: d.Edges}
	if err := appendLine(repo, &e, true); err != nil {
		return "", err
	}
	if err := s.applyNew(tx, repo, e); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

// Ratify records @gitdek's ratification of a proposed or decided decision.
func (s *Store) Ratify(repo Repo, id, by string) error {
	return s.update(repo, id, by, func(status string) (Event, error) {
		if status == "ratified" || status == "superseded" {
			return Event{}, fmt.Errorf("%s is %s already", id, status)
		}
		return Event{Op: OpRatify}, nil
	})
}

// Supersede records that a later decision replaces id.
func (s *Store) Supersede(repo Repo, id, with, by string) error {
	if _, err := Qualify(repo.Name, with); err != nil {
		return err
	}
	return s.update(repo, id, by, func(status string) (Event, error) {
		if status == "superseded" {
			return Event{}, fmt.Errorf("%s is superseded already", id)
		}
		return Event{Op: OpSupersede, With: with}, nil
	})
}

// Link records a typed edge from id to another decision.
func (s *Store) Link(repo Repo, id string, edge Edge, by string) error {
	if !edgeTypes[edge.Type] || edge.Type == Implements {
		return fmt.Errorf("a decision refines, supersedes, reopens or cites another, not %q", edge.Type)
	}
	if _, err := Qualify(repo.Name, edge.To); err != nil {
		return err
	}
	return s.update(repo, id, by, func(string) (Event, error) {
		return Event{Op: OpLink, Edges: []Edge{edge}}, nil
	})
}

// update appends one line to an existing decision's journal, and applies it,
// under the store's write lock.
func (s *Store) update(repo Repo, id, by string, next func(status string) (Event, error)) error {
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
	var status string
	if err := tx.QueryRow(`SELECT status FROM node WHERE id = ? AND kind = 'decision'`, full).Scan(&status); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("there's no decision %s", full)
	} else if err != nil {
		return err
	}
	e, err := next(status)
	if err != nil {
		return err
	}
	e.At, e.By, e.ID = time.Now().UTC().Format(time.RFC3339), by, short
	if err := appendLine(repo, &e, false); err != nil {
		return err
	}
	if err := s.applyNew(tx, repo, e); err != nil {
		return err
	}
	return tx.Commit()
}

// applyNew puts a line that was just written into the graph, with the
// citations its words make.
func (s *Store) applyNew(tx *sql.Tx, repo Repo, e Event) error {
	line, err := jsonLine(e)
	if err != nil {
		return err
	}
	if err := apply(tx, repo.Name, e, line); err != nil {
		return err
	}
	if e.Text != "" {
		return citeFrom(tx, repo.Name+"/"+e.ID, repo.Name, e.Text)
	}
	return nil
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
