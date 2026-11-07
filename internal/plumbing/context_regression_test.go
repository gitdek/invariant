package plumbing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func TestRepositoryContextKeepsCapturedDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name, language, file, source, want string
		argv                               []string
	}{
		{"pytest", "python", "pyproject.toml", "[tool.pytest.ini_options]\ntestpaths = ['checks']\npython_files = ['*_test.py']\n", `suite targets/roots ["checks"]`, []string{"pytest"}},
		{"vitest", "typescript", "vitest.config.ts", "export default {test: {include: ['checks/**/*.spec.ts'], exclude: ['checks/ignored/**']}};", `include ["checks/**/*.spec.ts"]`, []string{"vitest", "run"}},
		{"jest", "typescript", "jest.config.cjs", "module.exports = {roots: ['<rootDir>/checks'], testMatch: ['**/*.spec.ts']};", `suite targets/roots ["checks"]`, []string{"jest", "--runInBand"}},
		{"executable", "typescript", "vitest.config.ts", "export default getSettings();", "no implicit non-Go regression allowance", []string{"vitest", "run"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			part := part484(tc.language, "app")
			part.Commands["test"] = repository.Command{Argv: tc.argv}
			root, _, _ := fixture484(t, []repository.Part{part}, nil)
			write468(t, root, "app/"+tc.file, tc.source)
			git468(t, root, "add", "--all")
			git468(t, root, "commit", "-qm", "captured discovery")
			snapshot, err := repository.Load(t.Context(), root, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			write468(t, root, "app/"+tc.file, "candidate discovery replacement")
			write468(t, root, "AGENTS.md", "candidate rule replacement")
			sandbox := Sandbox{Repository: &RepositoryChecks{Snapshot: snapshot}}
			captured, err := sandbox.RepositoryContext(t.Context(), &Plan{Files: []string{"app/main.ts"}})
			if err != nil {
				t.Fatal(err)
			}
			ws := t.TempDir()
			for round := range 2 {
				prompt, release, err := captured.Stage(ws)
				if err != nil || !strings.Contains(prompt, tc.want) || !strings.Contains(prompt, "fixture rules") || strings.Contains(prompt, "candidate rule replacement") || strings.Contains(prompt, "candidate context replacement") {
					t.Fatalf("round %d lost captured guidance: %v\n%s", round, err, prompt)
				}
				if err := filepath.WalkDir(ws, func(file string, entry os.DirEntry, err error) error {
					if err != nil || entry.IsDir() {
						return err
					}
					return os.WriteFile(file, []byte("candidate context replacement"), 0o644)
				}); err != nil {
					t.Fatal(err)
				}
				release()
				if entries, err := os.ReadDir(ws); err != nil || len(entries) != 0 {
					t.Fatalf("context copies leaked into candidate changes: %v, %v", entries, err)
				}
			}
		})
	}
}
