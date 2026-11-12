package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The dashboard says who made a decision as the journal means it (#843): a
// person's handle is that person, and anything else is an agent. This
// repository's earliest records name the agent that wrote them, and the
// page showed that name as a third kind of author.
func TestDecisionsNameRoles843(t *testing.T) {
	for who, want := range map[string]string{"@gitdek": "@gitdek", "agent": "agent", "Some Product": "agent", " @someone ": "@someone", "": ""} {
		if got := madeBy(who); got != want {
			t.Errorf("madeBy(%q) = %q, want %q", who, got, want)
		}
	}
	log := "| ID | Date | Door | Status | Decision | Who |\n| :-- | :-- | :-- | :-- | :-- | :-- |\n" +
		"| D-0001 | 2026-09-25 | two-way | decided | An early call. | Some Product |\n| D-0002 | 2026-09-26 | one-way | ratified | A person's call. | @gitdek |\n"
	parsed := ParseLog(log)
	if len(parsed) != 2 || parsed[0].Who != "agent" || parsed[1].Who != "@gitdek" {
		t.Fatalf("the log's rows read as %+v", parsed)
	}

	// The repository's own journal, which holds such records.
	dir := filepath.Join("..", "..", "decisions", "journal")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	journal, named := map[string][]byte{}, 0
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		journal[e.Name()] = b
		if strings.Contains(string(b), `"who":"`) && !strings.Contains(string(b), `"who":"agent"`) && !strings.Contains(string(b), `"who":"@`) {
			named++
		}
	}
	if named == 0 {
		t.Fatal("the journal no longer holds a record that names its author another way, so this checks nothing")
	}
	g, err := BuildDecisionGraph("invariant", journal)
	if err != nil {
		t.Fatal(err)
	}
	agents := 0
	for _, n := range g.Nodes {
		switch {
		case n.Who == "agent":
			agents++
		case n.Who != "" && !strings.HasPrefix(n.Who, "@"):
			t.Errorf("%s is served as made by %q", n.ID, n.Who)
		}
	}
	if agents < named {
		t.Errorf("%d decisions are served as an agent's, fewer than the %d early records", agents, named)
	}
	d, _ := journalDecisions(g)
	for name := range d.Who {
		if name != "agent" && name != "" && !strings.HasPrefix(name, "@") {
			t.Errorf("the decisions tile counts %q as an author", name)
		}
	}
}
