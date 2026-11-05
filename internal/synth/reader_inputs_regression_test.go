package synth

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

func TestReaderInputsBesideOwnedCode(t *testing.T) {
	p := &project.Project{Dir: t.TempDir(), Manifest: project.Manifest{
		Module: "machine/Main.tla", Code: "machine", Language: "go",
	}}
	for name, text := range map[string]string{
		"machine/Main.tla":       "original model",
		"machine/Helper.tla":     "original include",
		".invariant/view.json":   "original view",
		".invariant/reader.json": "obsolete proposal",
	} {
		if err := writeFile(filepath.Join(p.Dir, name), text); err != nil {
			t.Fatal(err)
		}
	}
	ws := t.TempDir()
	if err := Prepare(p, "skeleton", ws); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".invariant/reader.json")); !os.IsNotExist(err) {
		t.Fatal("preparation copied derived evidence")
	}
	for _, name := range []string{"machine/Helper.tla", ".invariant/view.json"} {
		file := filepath.Join(ws, name)
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(p.Dir, "machine/Main.tla"), file); err != nil {
			t.Fatal(err)
		}
	}
	for name, text := range map[string]string{
		"machine/Main.tla":       "final model",
		"machine/core.go":        "final code",
		"machine/Extra.tla":      "agent include",
		".invariant/reader.json": "forged reader",
		".invariant/reader.svg":  "forged SVG",
	} {
		if err := writeFile(filepath.Join(ws, name), text); err != nil {
			t.Fatal(err)
		}
	}
	dst := filepath.Join(t.TempDir(), "result")
	if err := Assemble(p, ws, dst); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"machine/Main.tla":     "final model",
		"machine/core.go":      "final code",
		"machine/Helper.tla":   "original include",
		".invariant/view.json": "original view",
	} {
		got, err := os.ReadFile(filepath.Join(dst, name))
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v", name, got, err)
		}
	}
	for _, name := range []string{"machine/Extra.tla", ".invariant/reader.json", ".invariant/reader.svg"} {
		if _, err := os.Stat(filepath.Join(dst, name)); !os.IsNotExist(err) {
			t.Fatalf("assembly accepted %s", name)
		}
	}
}
