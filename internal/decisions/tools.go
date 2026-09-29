package decisions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/mcp"
)

// A Session is the decision graph as one checkout has it, for a coding
// agent's tools over MCP (D-0096). It reads a store of its own, rebuilt from
// the checkout and from every other project the machine's store holds, so
// another agent's rebuild of the machine's store never changes what it
// answers. Its writes go through the machine's store, which takes every new
// ID (D-0102), then into its own.
type Session struct {
	Repo    Repo
	Skipped []string // other projects that couldn't be read, and why
	shared  *Store
	view    *Store
	dir     string
}

// NewSession reads repo's checkout, and every other project shared holds,
// into a store of the session's own.
func NewSession(shared *Store, repo Repo) (*Session, error) {
	dir, err := os.MkdirTemp("", "invariant-decisions-session-")
	if err != nil {
		return nil, err
	}
	view, err := Open(filepath.Join(dir, "view.db"))
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	s := &Session{Repo: repo, shared: shared, view: view, dir: dir}
	if err := view.Rebuild([]Repo{repo}); err != nil {
		s.Close()
		return nil, err
	}
	held, err := shared.Checkouts()
	if err != nil {
		s.Close()
		return nil, err
	}
	for _, r := range held {
		if r.Name == repo.Name {
			continue
		}
		if err := view.Rebuild([]Repo{r}); err != nil {
			s.Skipped = append(s.Skipped, fmt.Sprintf("%s, from %s: %v", r.Name, r.Dir, err))
		}
	}
	return s, nil
}

// Close removes the session's own store.
func (s *Session) Close() error {
	err := s.view.Close()
	os.RemoveAll(s.dir)
	return err
}

// Refresh puts one decision into the store again, as the checkout's journal
// has it.
func (s *Store) Refresh(repo Repo, short string) error {
	events, err := ReadJournal(filepath.Join(repo.JournalDir(), short+".jsonl"))
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	floor, err := journalFloor(tx, repo.Name, map[string][]Event{short: events})
	if err != nil {
		return err
	}
	if err := putFile(tx, repo, short); err != nil {
		return err
	}
	if err := holdsFloor(tx, repo.Name, floor); err != nil {
		return err
	}
	return tx.Commit()
}

// Tools are the graph's tools. Every one reads, except decide and link,
// which are offered only when write is true. None ratifies: that's a
// person's (D-0101).
func (s *Session) Tools(write bool) []mcp.Tool {
	id := map[string]any{"type": "string", "description": "a decision's ID: D-0096 within this project, or qualified by its project, as copythis-ad/D-0001"}
	byID := map[string]any{"type": "object", "properties": map[string]any{"id": id}, "required": []string{"id"}, "additionalProperties": false}
	tools := []mcp.Tool{
		{Name: "decision", Schema: byID, Call: s.show,
			Description: "A decision: what it says, its door, status, date and who made it, its record, and every edge to and from it, with the text at each end."},
		{Name: "dependents", Schema: byID, Call: s.walk("dependents"),
			Description: "Everything that rests on a decision: what reopening it would touch. That's every decision that refines it, directly or through others, and every decision, SPEC line, doc or piece of code that mentions or implements any of them. Each line cites the node and its text."},
		{Name: "implementers", Schema: byID, Call: s.walk("implementers"),
			Description: "The code that implements a decision, or a decision that refines it, as file:line with the line's text."},
		{Name: "grounds", Schema: byID, Call: s.walk("grounds"),
			Description: "What a decision rests on: the decisions it refines, all the way, and the ones they cite. It says why the decision exists."},
		{Name: "search_decisions", Call: s.search,
			Schema: map[string]any{"type": "object", "properties": map[string]any{
				"words": map[string]any{"type": "string", "description": "words every decision found must contain, in any case"}},
				"required": []string{"words"}, "additionalProperties": false},
			Description: "Decisions whose words contain every one of the given words."},
		{Name: "query_decisions", Call: s.query,
			Schema: map[string]any{"type": "object", "properties": map[string]any{
				"sql": map[string]any{"type": "string", "description": "one SQL query over node(id, kind, project, date, door, status, who, text, record) and edge(src, type, dst, project, derived)"}},
				"required": []string{"sql"}, "additionalProperties": false},
			Description: "Run your own read-only SQL over the graph, for what the other tools don't answer. It stops after 10 seconds and returns at most 200 rows. Prefer the named tools: they're tested, and a traversal you write may not end."},
	}
	if !write {
		return tools
	}
	ids := map[string]any{"type": "array", "items": id}
	return append(tools,
		mcp.Tool{Name: "decide", Call: s.decide,
			Schema: map[string]any{"type": "object", "properties": map[string]any{
				"door":    map[string]any{"type": "string", "enum": []string{"two-way", "one-way"}, "description": "two-way if it's cheap to reverse, one-way if undoing it has a real cost"},
				"status":  map[string]any{"type": "string", "enum": []string{"decided", "proposed"}, "description": "decided for a two-way call made while carrying out ratified work; proposed for anything the owner would want a say in, and every one-way door"},
				"text":    map[string]any{"type": "string", "description": "the decision in plain words, and why, in a sentence or a few; no | characters"},
				"record":  map[string]any{"type": "string", "description": "for a one-way door, the slug of its full record, which you then write as decisions/D-NNNN-slug.md"},
				"refines": ids, "cites": ids, "reopens": ids},
				"required": []string{"door", "status", "text"}, "additionalProperties": false},
			Description: "Record a decision you made or propose, in this checkout's journal and decisions/log.md, and get its ID. It's recorded as yours: who is agent. Only a person ratifies."},
		mcp.Tool{Name: "link_decision", Call: s.link,
			Schema: map[string]any{"type": "object", "properties": map[string]any{
				"id":   id,
				"type": map[string]any{"type": "string", "enum": []string{Refines, Cites, Reopens, Supersedes}},
				"to":   id},
				"required": []string{"id", "type", "to"}, "additionalProperties": false},
			Description: "Record that one decision refines, cites, reopens or supersedes another."},
	)
}

// qualify reads a tool's ID argument.
func (s *Session) qualify(args json.RawMessage) (string, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	return Qualify(s.Repo.Name, a.ID)
}

func fail(err error) (string, bool) { return err.Error(), true }

func (s *Session) show(_ context.Context, args json.RawMessage) (string, bool) {
	id, err := s.qualify(args)
	if err != nil {
		return fail(err)
	}
	n, err := s.view.Get(id)
	if err != nil {
		return fail(err)
	}
	near, err := s.view.Neighbors(id)
	if err != nil {
		return fail(err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %s door · %s · %s · %s\n%s\n", n.ID, n.Door, n.Status, n.Date, n.Who, n.Text)
	if n.Record != "" {
		fmt.Fprintf(&b, "Its full record: decisions/%s\n", n.Record)
	}
	for _, out := range []bool{true, false} {
		first := true
		for _, nb := range near {
			if nb.Out != out {
				continue
			}
			if first {
				b.WriteString(map[bool]string{true: "\nEdges from it:\n", false: "\nEdges to it:\n"}[out])
				first = false
			}
			fmt.Fprintf(&b, "- %s %s: %s\n", nb.Type, nb.Node.ID, clip(nb.Node.Text, 240))
		}
	}
	return b.String(), false
}

func (s *Session) walk(which string) func(context.Context, json.RawMessage) (string, bool) {
	return func(_ context.Context, args json.RawMessage) (string, bool) {
		id, err := s.qualify(args)
		if err != nil {
			return fail(err)
		}
		if _, err := s.view.Get(id); err != nil {
			return fail(err)
		}
		var nodes []Node
		switch which {
		case "dependents":
			nodes, err = s.view.Dependents(id)
		case "implementers":
			nodes, err = s.view.Implementers(id)
		default:
			nodes, err = s.view.Grounds(id)
		}
		if err != nil {
			return fail(err)
		}
		return listNodes(fmt.Sprintf("%s of %s: %d", which, id, len(nodes)), nodes), false
	}
}

func (s *Session) search(_ context.Context, args json.RawMessage) (string, bool) {
	var a struct {
		Words string `json:"words"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return fail(err)
	}
	nodes, err := s.view.Search(a.Words)
	if err != nil {
		return fail(err)
	}
	return listNodes(fmt.Sprintf("decisions with %q: %d", a.Words, len(nodes)), nodes), false
}

func (s *Session) query(_ context.Context, args json.RawMessage) (string, bool) {
	var a struct {
		SQL string `json:"sql"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return fail(err)
	}
	ro, err := OpenReadOnly(s.view.Path)
	if err != nil {
		return fail(err)
	}
	defer ro.Close()
	cols, rows, err := ro.Query(a.SQL, 10*time.Second)
	if err != nil {
		return fail(err)
	}
	var b strings.Builder
	b.WriteString(strings.Join(cols, "\t") + "\n")
	for i, row := range rows {
		if i == 200 {
			fmt.Fprintf(&b, "…and %d more rows\n", len(rows)-200)
			break
		}
		cells := make([]string, len(row))
		for j, v := range row {
			cells[j] = fmt.Sprint(v)
		}
		b.WriteString(strings.Join(cells, "\t") + "\n")
	}
	return b.String(), false
}

func (s *Session) decide(_ context.Context, args json.RawMessage) (string, bool) {
	var a struct {
		Door, Status, Text, Record string
		Refines, Cites, Reopens    []string
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return fail(err)
	}
	if a.Status != "decided" && a.Status != "proposed" {
		return fail(errors.New("an agent records a decision as decided or proposed; only a person ratifies"))
	}
	var edges []Edge
	for _, set := range []struct {
		typ string
		ids []string
	}{{Refines, a.Refines}, {Cites, a.Cites}, {Reopens, a.Reopens}} {
		for _, to := range set.ids {
			edges = append(edges, Edge{Type: set.typ, To: to})
		}
	}
	id, err := s.shared.Decide(s.Repo, NewDecision{Door: a.Door, Status: a.Status, Who: "agent", Text: a.Text, Record: a.Record, Edges: edges}, "agent")
	if err != nil {
		return fail(err)
	}
	if err := s.afterWrite(id); err != nil {
		return fmt.Sprintf("Recorded %s, but: %v", id, err), true
	}
	msg := fmt.Sprintf("Recorded %s in decisions/journal/%s.jsonl and decisions/log.md.", id, id)
	if a.Record != "" {
		msg += fmt.Sprintf(" Write its full record in decisions/%s-%s.md: the options considered, why, and what would reopen it.", id, a.Record)
	}
	return msg, false
}

func (s *Session) link(_ context.Context, args json.RawMessage) (string, bool) {
	var a struct {
		ID, Type, To string
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return fail(err)
	}
	full, err := Qualify(s.Repo.Name, a.ID)
	if err != nil {
		return fail(err)
	}
	if err := s.shared.Link(s.Repo, a.ID, Edge{Type: a.Type, To: a.To}, "agent"); err != nil {
		return fail(err)
	}
	short := full[strings.LastIndex(full, "/")+1:]
	if err := s.afterWrite(short); err != nil {
		return fmt.Sprintf("Recorded the link, but: %v", err), true
	}
	return fmt.Sprintf("Recorded that %s %s %s.", full, a.Type, a.To), false
}

// afterWrite puts a decision just written into the session's store, and
// brings decisions/log.md up to date where the checkout has one.
func (s *Session) afterWrite(short string) error {
	if err := s.view.Refresh(s.Repo, short); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(s.Repo.Dir, "decisions", "log.md")); err != nil {
		return nil
	}
	_, err := WriteLog(s.Repo)
	return err
}

func listNodes(head string, nodes []Node) string {
	var b strings.Builder
	b.WriteString(head + "\n")
	for _, n := range nodes {
		label := n.Kind
		if n.Kind == "decision" {
			label = "decision, " + n.Status
		}
		fmt.Fprintf(&b, "- %s [%s] %s\n", n.ID, label, clip(n.Text, 300))
	}
	return b.String()
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
