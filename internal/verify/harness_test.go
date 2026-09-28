package verify

import (
	"os"
	"path/filepath"
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
		if got := explores(&project.Project{Dir: dir, Manifest: c.manifest}); got != c.want {
			t.Errorf("explores(%+v, %q) = %v; want %v", c.manifest, c.driver, got, c.want)
		}
	}
}
