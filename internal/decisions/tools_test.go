package decisions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/mcp"
)

// call runs one of a session's tools by name, as an agent's MCP call would.
func call(t *testing.T, tools []mcp.Tool, name string, args any) (string, bool) {
	t.Helper()
	b, _ := json.Marshal(args)
	for _, tool := range tools {
		if tool.Name == name {
			return tool.Call(context.Background(), b)
		}
	}
	t.Fatalf("no tool %q", name)
	return "", true
}

func names(tools []mcp.Tool) string {
	var out []string
	for _, tool := range tools {
		out = append(out, tool.Name)
	}
	return strings.Join(out, ",")
}

// An agent's tools answer from its own checkout, with citations, even after
// another checkout rebuilds the machine's store. Its writes take their IDs
// from the machine's store, and it can't ratify.
func TestAnAgentsToolsAnswerFromItsCheckout(t *testing.T) {
	shared, repo := fixture(t)
	write(t, filepath.Join(repo.Dir, "decisions", "log.md"), "# Decision log\n\n"+tableHead+"\nThe end.\n")
	for _, d := range []NewDecision{
		{Text: "The store is SQLite."},
		{Text: "Journal every write.", Edges: []Edge{{Refines, "D-0001"}}},
	} {
		d.Door, d.Status, d.Who = "two-way", "decided", "agent"
		if _, err := shared.Decide(repo, d, "agent"); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(repo.Dir, "store", "store.go"), "package store\n\n// append writes a line (D-0002).\n")
	if _, err := WriteLog(repo); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(shared, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if got := names(s.Tools(false)); got != "decision,dependents,implementers,grounds,search_decisions,query_decisions" {
		t.Errorf("read-only tools = %s", got)
	}
	tools := s.Tools(true)
	if strings.Contains(names(tools), "ratify") {
		t.Error("an agent's tools must not ratify")
	}

	// Another checkout of the project, without D-0002, rebuilds the machine's store.
	other := Repo{Name: "demo", Dir: filepath.Join(filepath.Dir(repo.Dir), "demo-other")}
	if err := os.MkdirAll(other.JournalDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(repo.JournalDir(), "D-0001.jsonl"))
	write(t, filepath.Join(other.JournalDir(), "D-0001.jsonl"), string(b))
	if err := shared.Rebuild([]Repo{other}); err != nil {
		t.Fatal(err)
	}

	text, bad := call(t, tools, "dependents", map[string]string{"id": "D-0001"})
	if bad || !strings.Contains(text, "- demo/D-0002 [decision, decided] Journal every write.") || !strings.Contains(text, "- demo/store/store.go:3 [code] // append writes a line (D-0002).") {
		t.Errorf("dependents:\n%s", text)
	}
	if text, bad := call(t, tools, "decision", map[string]string{"id": "D-0002"}); bad || !strings.Contains(text, "- refines demo/D-0001: The store is SQLite.") {
		t.Errorf("decision:\n%s", text)
	}
	if text, bad := call(t, tools, "query_decisions", map[string]string{"sql": "DELETE FROM node"}); !bad {
		t.Errorf("a query deleted: %s", text)
	}
	if text, bad := call(t, tools, "query_decisions", map[string]string{"sql": "SELECT count(*) AS n FROM node WHERE kind = 'decision'"}); bad || text != "n\n2\n" {
		t.Errorf("query: %q", text)
	}

	if text, bad := call(t, tools, "decide", map[string]any{"door": "two-way", "status": "ratified", "text": "Mine."}); !bad {
		t.Errorf("an agent ratified: %s", text)
	}
	// This checkout holds D-0002, so the next is D-0003, whatever the other
	// checkout's rebuild left in the machine's store.
	text, bad = call(t, tools, "decide", map[string]any{"door": "two-way", "status": "decided", "text": "Name the tools plainly.", "refines": []string{"D-0002"}})
	if bad || !strings.HasPrefix(text, "Recorded D-0003 ") {
		t.Fatalf("decide: %s", text)
	}
	if text, _ := call(t, tools, "grounds", map[string]string{"id": "D-0003"}); !strings.Contains(text, "demo/D-0001") || !strings.Contains(text, "demo/D-0002") {
		t.Errorf("grounds of the new decision:\n%s", text)
	}
	log, _ := os.ReadFile(filepath.Join(repo.Dir, "decisions", "log.md"))
	if !strings.Contains(string(log), "| D-0003 | ") {
		t.Errorf("decisions/log.md wasn't brought up to date:\n%s", log)
	}
	if text, bad := call(t, tools, "link_decision", map[string]string{"id": "D-0003", "type": "cites", "to": "D-0001"}); bad {
		t.Errorf("link: %s", text)
	}
}
