package synth

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

func TestComparison648SynthesisPreservesCapturedProvenance(t *testing.T) {
	// This tests byte transport only, like TestReaderInputsBesideOwnedCode.
	// Comparison validity belongs to formalization, verification and rendering.
	p := &project.Project{Dir: t.TempDir(), Manifest: project.Manifest{Module: "machine/Main.tla", Code: "machine", Language: "go"}}
	const provenance = `{"version":1,"transport_fixture":"captured proposal comparison"}`
	for name, text := range map[string]string{"machine/Main.tla": "original model", ".invariant/view.json": "original view", ".invariant/comparison.json": provenance} {
		if err := writeFile(filepath.Join(p.Dir, name), text); err != nil {
			t.Fatal(err)
		}
	}
	ws := t.TempDir()
	if err := Prepare(p, "skeleton", ws); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(ws, ".invariant/comparison.json")); err != nil || string(b) != provenance {
		t.Fatal("synthesis did not retain captured comparison inputs")
	}
	if err := writeFile(filepath.Join(ws, ".invariant/comparison.json"), `{"forged":"latest branch"}`); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(ws, "machine/Main.tla"), "synthesized model"); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "result")
	if err := Assemble(p, ws, dst); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, ".invariant/comparison.json")); err != nil || string(b) != provenance {
		t.Fatal("agent-written comparison replaced captured provenance")
	}
	if b, err := os.ReadFile(filepath.Join(dst, "machine/Main.tla")); err != nil || string(b) != "synthesized model" {
		t.Fatal("preserving proposal provenance prevented final model assembly")
	}
	if err := os.Remove(filepath.Join(p.Dir, ".invariant/comparison.json")); err != nil {
		t.Fatal(err)
	}
	if err := Assemble(p, ws, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".invariant/comparison.json")); !os.IsNotExist(err) {
		t.Fatal("agent created comparison provenance for a project with none")
	}
}
