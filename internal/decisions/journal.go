// Package decisions keeps every project's decisions in one graph (D-0096).
// The graph lives in a SQLite file on the machine that runs the factory, and
// its record is a journal in each project's repository: one file per
// decision, one line per write to it (D-0097). The store can always be
// rebuilt from the journals, which is how CI checks it.
package decisions

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// A Repo is one project's repository checkout. Its name prefixes its
// decisions' IDs in the store, such as invariant/D-0096.
type Repo struct {
	Name string
	Dir  string
}

// JournalDir is where a repository keeps its journal.
func (r Repo) JournalDir() string { return filepath.Join(r.Dir, "decisions", "journal") }

// Ops a journal line can record.
const (
	OpImport    = "import"    // a decision carried over from decisions/log.md
	OpDecide    = "decide"    // a new decision
	OpRatify    = "ratify"    // @gitdek ratifies a proposed or decided one
	OpSupersede = "supersede" // a later decision replaces this one
	OpLink      = "link"      // a typed edge from this decision
)

// Edge types. A decision refines, supersedes or reopens another, or cites
// it; code implements one. What depends on a decision is everything with a
// path to it over refines, cites and implements.
const (
	Refines    = "refines"
	Supersedes = "supersedes"
	Reopens    = "reopens"
	Cites      = "cites"
	Implements = "implements"
)

var edgeTypes = map[string]bool{Refines: true, Supersedes: true, Reopens: true, Cites: true, Implements: true}

// Statuses a decision can have, as decisions/log.md defines them.
var statuses = map[string]bool{"ratified": true, "decided": true, "proposed": true, "open": true, "superseded": true}

// Edge is a typed edge from a decision. To is another decision's ID, short
// within the same project (D-0082) or qualified by its project
// (copythis-ad/D-0001).
type Edge struct {
	Type string `json:"type"`
	To   string `json:"to"`
}

// Event is one line of a decision's journal. Prev is the hash of the line
// before it in the same file, and Hash covers every other field, so a line
// can't be edited, dropped or reordered without the chain showing it.
type Event struct {
	At     string `json:"at"` // when it was written, RFC 3339 in UTC
	By     string `json:"by"` // who wrote it: @gitdek, or agent
	Op     string `json:"op"`
	ID     string `json:"id"` // the decision, as D-0097
	Date   string `json:"date,omitempty"`
	Door   string `json:"door,omitempty"`
	Status string `json:"status,omitempty"`
	Who    string `json:"who,omitempty"`    // who made the decision, as the log's Who column
	Text   string `json:"text,omitempty"`   // the decision in plain words
	Record string `json:"record,omitempty"` // a one-way door's full record, relative to decisions/
	Edges  []Edge `json:"edges,omitempty"`
	With   string `json:"with,omitempty"` // the decision that supersedes this one
	Prev   string `json:"prev"`
	Hash   string `json:"hash"`
}

// seal sets the event's hash over its canonical encoding, which is its JSON
// with the hash empty.
func (e *Event) seal() error {
	e.Hash = ""
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	e.Hash = "sha256:" + hex.EncodeToString(sum[:])
	return nil
}

// sealed says whether the event's hash is its own.
func (e Event) sealed() bool {
	want := e.Hash
	if err := e.seal(); err != nil {
		return false
	}
	return e.Hash == want
}

// idPattern is a decision ID, alone or qualified by its project.
var (
	idPattern   = regexp.MustCompile(`^(?:([A-Za-z0-9][A-Za-z0-9._-]*)/)?(D-\d{4,})$`)
	mentionedID = regexp.MustCompile(`\bD-\d{4,}\b`)
)

// Qualify turns a short ID into its store ID within project, and leaves a
// qualified one as it is.
func Qualify(project, id string) (string, error) {
	m := idPattern.FindStringSubmatch(strings.TrimSpace(id))
	if m == nil {
		return "", fmt.Errorf("%q isn't a decision ID, such as D-0096 or copythis-ad/D-0001", id)
	}
	if m[1] != "" {
		return m[1] + "/" + m[2], nil
	}
	return project + "/" + m[2], nil
}

// number is the numeric part of a short ID.
func number(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "D-"))
	return n
}

// ReadJournal reads one decision's journal file and checks its chain: each
// line's hash is its own, each points at the line before it, and every line
// is about the file's decision.
func ReadJournal(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	var events []Event
	prev := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e Event
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("%s:%d: %v", path, n, err)
		}
		switch {
		case e.ID != id:
			return nil, fmt.Errorf("%s:%d: the line is about %s, not %s", path, n, e.ID, id)
		case e.Prev != prev:
			return nil, fmt.Errorf("%s:%d: the line doesn't follow the one before it, so the journal was edited", path, n)
		case !e.sealed():
			return nil, fmt.Errorf("%s:%d: the line's hash isn't its own, so it was edited", path, n)
		}
		events = append(events, e)
		prev = e.Hash
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("%s is empty", path)
	}
	return events, nil
}

// Journals reads every decision's journal in a repository, in the order of
// their IDs.
func Journals(repo Repo) (map[string][]Event, []string, error) {
	entries, err := os.ReadDir(repo.JournalDir())
	if errors.Is(err, os.ErrNotExist) {
		return map[string][]Event{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	out := map[string][]Event{}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(name, ".jsonl")
		if m := idPattern.FindStringSubmatch(id); m == nil || m[1] != "" {
			return nil, nil, fmt.Errorf("%s: a journal file is named after its decision, such as D-0096.jsonl", filepath.Join(repo.JournalDir(), name))
		}
		events, err := ReadJournal(filepath.Join(repo.JournalDir(), name))
		if err != nil {
			return nil, nil, err
		}
		out[id] = events
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return number(ids[i]) < number(ids[j]) })
	return out, ids, nil
}

// appendLine writes one event to the end of a decision's journal, chained to
// the line before it. A new decision's file must not exist yet.
func appendLine(repo Repo, e *Event, create bool) error {
	path := filepath.Join(repo.JournalDir(), e.ID+".jsonl")
	if err := os.MkdirAll(repo.JournalDir(), 0o755); err != nil {
		return err
	}
	e.Prev = ""
	if !create {
		events, err := ReadJournal(path)
		if err != nil {
			return err
		}
		e.Prev = events[len(events)-1].Hash
	}
	if err := e.seal(); err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	flag := os.O_WRONLY | os.O_APPEND
	if create {
		flag |= os.O_CREATE | os.O_EXCL
	}
	f, err := os.OpenFile(path, flag, 0o644)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s already has a journal", e.ID)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
