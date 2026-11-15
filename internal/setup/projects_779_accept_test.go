package setup_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gitdek/invariant/internal/setup"
)

func TestDraftSeparatesModeledProjects779(t *testing.T) {
	root := t.TempDir()
	node727(t, root, "vitest", 3)
	node727(t, filepath.Join(root, "invariant", "01-compile-once"), "vitest", 3)
	node727(t, filepath.Join(root, "invariant-tools"), "vitest", 3)
	write727(t, root, "invariant/01-compile-once/.invariant/invariant.json", `{"name":"compile once","module":"spec.tla","language":"typescript","code":"conformance.ts"}`)
	write727(t, root, "invariant/01-compile-once/conformance.ts", "import {value} from '../../src/app.ts';\nvoid value;\n")
	write727(t, root, "invariant/02-go/go.mod", "module example.invalid/model\n\ngo 1.27.1\n")
	write727(t, root, "invariant/02-go/model.go", "package model\n")
	// Drafting from held bytes and from disk must continue to agree, and
	// neither operation may rewrite the driver's Node-compatible imports.
	held := map[string][]byte{}
	if err := filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		held[filepath.ToSlash(rel)], err = os.ReadFile(name)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	b, c, issues := draft727(t, root)
	var dirs []string
	for _, part := range c.Parts {
		dirs = append(dirs, part.Directory)
	}
	if !reflect.DeepEqual(dirs, []string{".", "invariant-tools"}) {
		t.Errorf("modeled projects became application parts: %q", dirs)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["projects_directory"]) != `"invariant"` {
		t.Errorf("draft does not declare the default projects directory: %s", b)
	}
	of, ofIssues, err := setup.DraftRepositoryOf(held)
	if err != nil || !bytes.Equal(of, b) || !reflect.DeepEqual(ofIssues, issues) {
		t.Errorf("held evidence and disk evidence disagree: %v\n%s\n%s", err, of, b)
	}
	for name, want := range held {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("drafting changed %s: %v", name, err)
		}
	}
}
