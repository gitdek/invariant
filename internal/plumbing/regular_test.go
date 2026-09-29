package plumbing

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/regular"
)

// A plan's draft and its build take only regular files from the agent's
// workspace (#153). An acceptance test, or a file the plan lets the build
// write, left as a link to a file on the host, standing in for the
// factory's key, is refused by name, and the file's text never reaches the
// plan or the repository.
func TestAPlanTakesNoLinkFromItsWorkspace(t *testing.T) {
	const secret = "-----BEGIN PRIVATE KEY----- stand-in"
	for _, link := range []func(string, string) error{os.Symlink, os.Link} {
		ws := t.TempDir()
		key := filepath.Join(t.TempDir(), "factory.pem")
		if err := os.WriteFile(key, []byte(graphTest+"// "+secret+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		plant := func(rel string) {
			t.Helper()
			at := filepath.Join(ws, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
				t.Fatal(err)
			}
			os.Remove(at)
			if err := link(key, at); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(ws, "plan.json"), []byte(`{"name":"the graph","summary":"Draw the graph.","files":["internal/dashboard/graph.go"],"tests":[{"name":"TestGraphShowsEveryDecision","file":"internal/dashboard/graph_accept_test.go","says":"Every decision is a node."}]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		plant("tests/internal/dashboard/graph_accept_test.go")
		p, err := ReadDraft(ws)
		var refused *regular.Refused
		if !errors.As(err, &refused) || refused.File != "tests/internal/dashboard/graph_accept_test.go" {
			t.Errorf("a linked acceptance test: %v; want it refused", err)
		}
		if p != nil && strings.Contains(strings.Join(mapValues(p.Sources), ""), secret) {
			t.Error("the key's text reached the plan")
		}

		root := t.TempDir()
		plant("internal/dashboard/graph.go")
		err = apply(root, ws, &Plan{Files: []string{"internal/dashboard/graph.go"}})
		if !errors.As(err, &refused) || refused.File != "internal/dashboard/graph.go" {
			t.Errorf("a linked file the plan names: %v; want it refused", err)
		}
		if b, _ := os.ReadFile(filepath.Join(root, "internal", "dashboard", "graph.go")); strings.Contains(string(b), secret) {
			t.Error("the key's text reached the repository")
		}
	}
}

func mapValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
