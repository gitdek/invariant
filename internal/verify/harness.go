package verify

import (
	"embed"
	"os"
	"path/filepath"
	"regexp"

	"github.com/gitdek/invariant/internal/project"
)

// harness is Invariant's exploration harness for conformance drivers
// (D-0082, D-0085). It tries every step the model's Next names, with every
// argument, in every state a driver reaches, and records each attempt. The
// gate writes it in before a driver runs, over any copy the project holds,
// so the harness that records attempts is always Invariant's own.
//
//go:embed harness/invariant-explore.ts harness/invariant_explore.py
var harness embed.FS

// The harness's file for each language: a TypeScript driver imports it from
// beside itself, and a Python driver from its path.
const (
	HarnessTypeScript = "invariant-explore.ts"
	HarnessPython     = "invariant_explore.py"
)

// Harness is the harness's source for a language, and the name a driver
// imports it by, or empty for a language whose drivers don't use it.
func Harness(language string) (name string, source []byte) {
	switch language {
	case "typescript":
		name = HarnessTypeScript
	case "python":
		name = HarnessPython
	default:
		return "", nil
	}
	source, _ = harness.ReadFile("harness/" + name)
	return name, source
}

// writeHarness puts the harness where a driver imports it: beside a
// TypeScript driver in src, and in runtime, which is on a Python driver's
// path.
func writeHarness(src, runtime string, p *project.Project) error {
	if name, source := Harness("python"); name != "" {
		if err := os.WriteFile(filepath.Join(runtime, name), source, 0o644); err != nil {
			return err
		}
	}
	if p.Manifest.Language == "typescript" && p.Manifest.Conformance != "" {
		name, source := Harness("typescript")
		if err := os.WriteFile(filepath.Join(src, filepath.Dir(p.Manifest.Conformance), name), source, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// harnessImport is how a driver built on the harness imports it.
var harnessImport = regexp.MustCompile(`(?m)^\s*(import\s[^\n]*["']\./invariant-explore\.ts["']|from\s+invariant_explore\s+import\s)`)

// Explores says whether a project's driver explores every state the code
// can reach: its manifest says so, the driver is built on the harness, which
// always does (D-0088), or it's a Go explorer with Try, which the gate's own
// harness explores (D-0090). Either way the gate holds it to that: the code
// must reach every state the model does, and the driver must have tried
// every step.
func Explores(p *project.Project) bool {
	if p.Manifest.Exhaustive {
		return true
	}
	if l := p.Manifest.Language; l == "" || l == "go" {
		return triesSteps(p.CodeDir())
	}
	if p.Manifest.Conformance == "" || len(p.Manifest.Existing) > 0 {
		return false
	}
	src, err := os.ReadFile(filepath.Join(p.Dir, p.Manifest.Conformance))
	return err == nil && harnessImport.Match(src)
}
