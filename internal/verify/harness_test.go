package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// A driver built on the harness explores every state, whatever its manifest
// says, and the gate holds it to that (D-0088).
func TestExplores(t *testing.T) {
	for _, c := range []struct {
		manifest project.Manifest
		driver   string
		want     bool
	}{
		{project.Manifest{Language: "typescript", Conformance: "conformance.ts", Exhaustive: true}, "// runs", true},
		{project.Manifest{Language: "typescript", Conformance: "conformance.ts"}, `import { explore } from "./invariant-explore.ts";`, true},
		{project.Manifest{Language: "python", Conformance: "conformance.py"}, "from invariant_explore import Step, explore\n", true},
		{project.Manifest{Language: "typescript", Conformance: "conformance.ts"}, "// samples runs at random", false},
		{project.Manifest{Language: "typescript", Conformance: "conformance.ts"}, `// not "./invariant-explore.ts"`, false},
		{project.Manifest{Language: "typescript", Conformance: "conformance.ts", Existing: []string{"src/lease"}}, `import { explore } from "./invariant-explore.ts";`, false},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, c.manifest.Conformance), []byte(c.driver), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := Explores(&project.Project{Dir: dir, Manifest: c.manifest}); got != c.want {
			t.Errorf("Explores(%+v, %q) = %v; want %v", c.manifest, c.driver, got, c.want)
		}
	}
}

// A Go explorer with Try reports every step it tries, so the gate explores it
// with its own harness and checks every attempt; one with only Successors
// keeps the agreement check (D-0090).
func TestAGoExplorerWithTryExplores(t *testing.T) {
	for _, c := range []struct {
		file, src string
		want      bool
	}{
		{"explore.go", "package pool\n\nfunc Try(s State, tried func(string, []any, State)) {}\n", true},
		{"environment.go", "package pool\n\nfunc Try(s State, tried func(string, []any, State)) {}\n", true},
		{"explore.go", "package pool\n\nfunc Successors(s State) []State { return nil }\n", false},
		{"explore_test.go", "package pool\n\nfunc Try(s State, tried func(string, []any, State)) {}\n", false},
		{"explore.go", "package pool\n\n// Try isn't here: func Try(s State)\n", false},
	} {
		dir := t.TempDir()
		pkg := filepath.Join(dir, "pool")
		if err := os.MkdirAll(pkg, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkg, c.file), []byte(c.src), 0o644); err != nil {
			t.Fatal(err)
		}
		p := &project.Project{Dir: dir, Manifest: project.Manifest{Language: "go", Code: "pool"}}
		if got := Explores(p); got != c.want {
			t.Errorf("%s holding %q: explores = %v; want %v", c.file, c.src, got, c.want)
		}
		test := explorerTest(pkg, "pool")
		if tries := strings.Contains(test, "Try(nodes[i]"); tries != c.want {
			t.Errorf("%s holding %q: the gate's test uses Try: %v; want %v", c.file, c.src, tries, c.want)
		}
		if !strings.HasPrefix(test, "package pool\n") || strings.Contains(test, "%%") {
			t.Errorf("the gate's test is malformed:\n%s", test)
		}
	}
}
