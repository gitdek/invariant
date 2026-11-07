package scope

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/repository"
)

// A plan may change what a trusted link in the base names, but scope marks
// that change trusted, so it waits for a person. With no link, the same
// change is ordinary (#705).
func TestScopeTrustsWhatTrustedLinksName705(t *testing.T) {
	for _, linked := range []bool{true, false} {
		t.Run(fmt.Sprintf("linked=%v", linked), func(t *testing.T) {
			root := repo(t)
			commands := map[string]repository.Command{}
			for _, phase := range []string{"install", "format", "lint", "build", "test"} {
				commands[phase] = repository.Command{NotApplicable: "Not used in scope tests."}
			}
			commands["test_one"] = repository.Command{Argv: []string{"pytest", "{test}"}}
			commands["test"] = repository.Command{Argv: []string{"pytest"}}
			config := repository.Config{Version: 1, Parts: []repository.Part{{Name: "app", Directory: "app", Language: "python", Image: "python@sha256:" + strings.Repeat("a", 64), Lockfiles: []string{}, Inputs: []string{}, Commands: commands}}, AgentRules: []string{}, CurrentState: []string{}, TrustedPaths: []string{}, SharedPaths: []repository.SharedPath{}}
			data, _ := json.Marshal(config)
			write(t, root, map[string]string{repository.Path: string(data), "app/main.py": "value = 1\n", "app/notes.md": "Shared notes.\n"})
			if linked {
				if err := os.Symlink("app/notes.md", filepath.Join(root, "CLAUDE.md")); err != nil {
					t.Fatal(err)
				}
			} else {
				write(t, root, map[string]string{"CLAUDE.md": "Own notes.\n"})
			}
			commit(t, root, "configured baseline")
			gitRun(t, root, "branch", "link-base")
			snapshot, err := repository.Load(context.Background(), root, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			ws := t.TempDir()
			write(t, ws, map[string]string{"plan.json": `{"name":"linked notes","summary":"Change the notes and the code.","files":["app/main.py","app/notes.md"],"tests":[{"name":"ready","part":"app","framework":"pytest","file":"app/test_accept.py","selector":"test_accept.py::test_ready","says":"It is ready."}]}`, "tests/app/test_accept.py": "def test_ready():\n    assert False\n"})
			p, err := plumbing.ReadRepositoryDraft(context.Background(), ws, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			lock := plumbing.Lock{Plan: *p, Ratified: plumbing.Ratification{Issue: 7, Proposal: p.Hash()}}
			if err := lock.Write(root); err != nil {
				t.Fatal(err)
			}
			write(t, root, map[string]string{"app/main.py": "value = 2\n", "app/notes.md": "Changed notes.\n"})
			commit(t, root, "candidate")
			result, err := Check(context.Background(), root, "link-base", "HEAD", 7)
			if err != nil {
				t.Fatal(err)
			}
			if !result.OK() {
				t.Fatalf("scope refused the planned change: %+v", result)
			}
			if got := slices.Contains(result.Trusted, "app/notes.md"); got != linked {
				t.Errorf("app/notes.md trusted = %v, want %v: %v", got, linked, result.Trusted)
			}
			if slices.Contains(result.Trusted, "app/main.py") {
				t.Errorf("ordinary code became trusted: %v", result.Trusted)
			}
		})
	}
}
