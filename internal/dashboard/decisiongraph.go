package dashboard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gitdek/invariant/internal/decisions"
)

// DecisionGraph is the decision graph the page draws under the newest
// decisions: every decision in the primary repository's journal, and every
// edge between two of them, as internal/decisions reads the journal. Only the
// journal is read, so what records, docs and code cite isn't on it.
type DecisionGraph struct {
	Nodes []DecisionNode `json:"nodes"` // oldest first
	Edges []DecisionEdge `json:"edges"`
}

// DecisionNode is one decision on the graph.
type DecisionNode struct {
	ID     string `json:"id"` // short, as D-0096
	Date   string `json:"date"`
	Door   string `json:"door"`
	Status string `json:"status"`
	Who    string `json:"who"`  // who made the call: a person, such as @gitdek, or agent
	Says   string `json:"says"` // its first sentence
	Text   string `json:"text"` // all of it, as plain text, as the list shows it
	// Dependents are the decisions that rest on it, as `invariant decisions
	// dependents` lists them.
	Dependents []string `json:"dependents"`
}

// DecisionEdge is one edge between two decisions: From refines, cites,
// supersedes or reopens To.
type DecisionEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

// BuildDecisionGraph builds the graph of a project's journal, given its files
// by name, such as D-0096.jsonl. It writes them into a checkout of its own,
// rebuilds a store of its own from it and asks that store, so the graph is
// what `invariant decisions` answers for that journal.
func BuildDecisionGraph(project string, journal map[string][]byte) (*DecisionGraph, error) {
	dir, err := os.MkdirTemp("", "invariant-decision-graph-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	repo := decisions.Repo{Name: project, Dir: filepath.Join(dir, "checkout")}
	if err := os.MkdirAll(repo.JournalDir(), 0o755); err != nil {
		return nil, err
	}
	for name, b := range journal {
		if name != filepath.Base(name) || !strings.HasSuffix(name, ".jsonl") {
			return nil, fmt.Errorf("%q isn't a journal file, such as D-0096.jsonl", name)
		}
		if err := os.WriteFile(filepath.Join(repo.JournalDir(), name), b, 0o644); err != nil {
			return nil, err
		}
	}
	store, err := decisions.Open(filepath.Join(dir, "decisions.db"))
	if err != nil {
		return nil, err
	}
	defer store.Close()
	if err := store.Rebuild([]decisions.Repo{repo}); err != nil {
		// Its errors name files in the checkout of its own, which are the
		// journal's own files.
		return nil, errors.New(strings.ReplaceAll(err.Error(), repo.Dir+string(filepath.Separator), ""))
	}
	all, err := store.Decisions(project)
	if err != nil {
		return nil, err
	}
	short := func(id string) string { return strings.TrimPrefix(id, project+"/") }
	g := &DecisionGraph{Nodes: []DecisionNode{}, Edges: []DecisionEdge{}}
	for _, d := range all {
		n := DecisionNode{ID: short(d.ID), Date: d.Date, Door: d.Door, Status: d.Status, Who: d.Who, Text: plain(d.Text), Dependents: []string{}}
		n.Says = firstSentence(n.Text)
		deps, err := store.Dependents(d.ID)
		if err != nil {
			return nil, err
		}
		for _, dep := range deps {
			n.Dependents = append(n.Dependents, short(dep.ID))
		}
		near, err := store.Neighbors(d.ID)
		if err != nil {
			return nil, err
		}
		for _, nb := range near {
			if nb.Out && nb.Node.Kind == "decision" {
				g.Edges = append(g.Edges, DecisionEdge{From: n.ID, To: short(nb.Node.ID), Kind: nb.Type})
			}
		}
		g.Nodes = append(g.Nodes, n)
	}
	return g, nil
}
