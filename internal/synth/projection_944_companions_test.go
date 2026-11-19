package synth

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

func TestProjection944SynthesisKeepsOriginalCompanions(t *testing.T) {
	dir, dst := t.TempDir(), t.TempDir()
	p := &project.Project{Dir: dir, Manifest: project.Manifest{Module: ".invariant/specs/M.tla"}}
	if err := os.MkdirAll(filepath.Join(dir, ".invariant/specs"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"comparison.json", "reader-baseline.json", "reader-candidate.json"} {
		want := []byte(" \n{\"original\":\"" + name + "\"}\n")
		if err := os.WriteFile(filepath.Join(dir, ".invariant", name), want, 0600); err != nil {
			t.Fatal(err)
		}
		if !readerInput(p.Manifest, ".invariant/"+name) {
			t.Fatal("companion can be replaced during synthesis", name)
		}
	}
	if err := copyReaderInputs(p, dst); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"comparison.json", "reader-baseline.json", "reader-candidate.json"} {
		a, _ := os.ReadFile(filepath.Join(dir, ".invariant", name))
		b, err := os.ReadFile(filepath.Join(dst, ".invariant", name))
		if err != nil || !bytes.Equal(a, b) {
			t.Fatal("changed original companion bytes", name, err)
		}
	}
	file := filepath.Join(dir, ".invariant/reader-candidate.json")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := copyReaderInputs(p, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".invariant/reader-candidate.json")); !os.IsNotExist(err) {
		t.Fatal("stale companion retained")
	}
	if err := os.Symlink(filepath.Join(dir, ".invariant/reader-baseline.json"), file); err != nil {
		t.Fatal(err)
	}
	if err := copyReaderInputs(p, dst); err == nil {
		t.Fatal("linked companion accepted")
	}
}
