package scope

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/repository"
)

func TestNativeAcceptanceScopePinsIdentityAndOriginalBaseline(t *testing.T) {
	for _, mode := range []string{"allowed", "changed source", "removed source", "linked source", "changed selector", "unnamed Python regression", "existing Go test", "new Go regression"} {
		t.Run(mode, func(t *testing.T) {
			root := repo(t)
			commands := map[string]repository.Command{}
			for _, phase := range []string{"install", "format", "lint", "build", "test"} {
				commands[phase] = repository.Command{NotApplicable: "Not used in scope tests."}
			}
			commands["test_one"] = repository.Command{Argv: []string{"pytest", "{test}"}}
			config := repository.Config{Version: 1, Parts: []repository.Part{{Name: "app", Directory: "app", Language: "python", Image: "python@sha256:" + strings.Repeat("a", 64), Lockfiles: []string{}, Inputs: []string{}, Commands: commands}}, AgentRules: []string{}, CurrentState: []string{}, TrustedPaths: []string{}, SharedPaths: []repository.SharedPath{}}
			data, _ := json.Marshal(config)
			write(t, root, map[string]string{repository.Path: string(data), "app/main.py": "value = 1\n", "app/embed.go": "package app\n", "app/existing_test.go": "package app\n"})
			commit(t, root, "configured baseline")
			gitRun(t, root, "branch", "native-base")
			snapshot, err := repository.Load(context.Background(), root, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			ws := t.TempDir()
			const file = "app/test_accept.py"
			const source = "def test_ready():\r\n    assert False\r\n"
			write(t, ws, map[string]string{"plan.json": `{"name":"native scope","summary":"Pin the source and selector.","files":["app/main.py"],"tests":[{"name":"ready","part":"app","framework":"pytest","file":"app/test_accept.py","selector":"test_accept.py::test_ready","says":"It is ready."}]}`, "tests/" + file: source})
			p, err := plumbing.ReadRepositoryDraft(context.Background(), ws, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			lock := plumbing.Lock{Plan: *p, Ratified: plumbing.Ratification{Issue: 7, Proposal: p.Hash()}}
			if err := lock.Write(root); err != nil {
				t.Fatal(err)
			}
			write(t, root, map[string]string{"app/main.py": "value = 2\n"})
			switch mode {
			case "changed source":
				write(t, root, map[string]string{file: "assert True\n"})
			case "removed source":
				if err := os.Remove(filepath.Join(root, file)); err != nil {
					t.Fatal(err)
				}
			case "linked source":
				if err := os.Remove(filepath.Join(root, file)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("main.py", filepath.Join(root, file)); err != nil {
					t.Fatal(err)
				}
			case "changed selector":
				lock.Plan.Tests[0].Selector = "test_accept.py::test_other"
				data, _ := json.Marshal(lock)
				write(t, root, map[string]string{plumbing.LockPath(7): string(data)})
			case "unnamed Python regression":
				write(t, root, map[string]string{"app/test_unplanned.py": "assert True\n"})
			case "existing Go test":
				write(t, root, map[string]string{"app/existing_test.go": "package app // changed\n"})
			case "new Go regression":
				write(t, root, map[string]string{"app/new_test.go": "package app\n"})
			}
			commit(t, root, "candidate")
			result, err := Check(context.Background(), root, "native-base", "HEAD", 7)
			if err != nil {
				t.Fatal(err)
			}
			allowed := mode == "allowed" || mode == "new Go regression"
			if result.OK() != allowed {
				t.Fatalf("scope %s: allowed=%v, want %v: %+v", mode, result.OK(), allowed, result)
			}
		})
	}
}
