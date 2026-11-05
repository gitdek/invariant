package formalize

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gitdek/invariant/internal/statechart"
)

func TestReaderWriteReplacesIncludeSet(t *testing.T) {
	p, err := Read(workspace(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	// Only persistence is under test here; computation and validation through
	// TLC are covered by the check tests. Seal a saved limitation fixture.
	p.Reader = &Reader{Version: 1, Limitation: &ReaderLimitation{Code: "too_large", Actual: 501, Limit: statechart.MaxStates}}
	p.Includes = map[string]string{"Old.tla": "---- MODULE Old ----\nOld == 1\n====\n"}
	p.Reader.Fingerprint, err = p.readerFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := p.Write(dir, "first draft", nil); err != nil {
		t.Fatal(err)
	}
	modelDir := filepath.Join(dir, filepath.Dir(p.Manifest().Module))
	if err := os.WriteFile(filepath.Join(modelDir, "notes.md"), []byte("Keep notes."), 0o600); err != nil {
		t.Fatal(err)
	}
	p.Includes = map[string]string{"New.tla": "---- MODULE New ----\nNew == 2\n====\n"}
	p.Reader.Fingerprint, err = p.readerFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Write(dir, "replacement draft", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(modelDir, "Old.tla")); !os.IsNotExist(err) {
		t.Fatalf("removed include remains in the project: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(modelDir, "notes.md")); err != nil || string(b) != "Keep notes." {
		t.Fatalf("writing includes changed another file: %q %v", b, err)
	}
	c := &Current{Manifest: p.Manifest(), ModuleText: p.ModuleText}
	if err := c.ReadReader(dir); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Includes, p.Includes) || c.View != p.View || !reflect.DeepEqual(c.Reader, p.Reader) {
		t.Fatal("the written project differs from the checked snapshot")
	}
	// Check the on-disk artifact independently of Current's loader too.
	b, err := os.ReadFile(filepath.Join(dir, ".invariant/reader.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved Reader
	if err := json.Unmarshal(b, &saved); err != nil || !reflect.DeepEqual(&saved, p.Reader) {
		t.Fatalf("saved reader differs: %v", err)
	}
}
