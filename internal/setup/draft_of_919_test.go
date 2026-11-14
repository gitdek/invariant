package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// treeOf writes files into a new directory and returns it.
func treeOf(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The checks a checkout would have, holding given files and no others, are
// the checks it has once they're written: the draft of the files is, byte
// for byte, the draft of a directory that holds them, however their names
// sort. Nothing is written to find out.
func TestADraftOfFilesIsTheDraftOnceTheyAreWritten919(t *testing.T) {
	gate, err := Workflow(ref)
	if err != nil {
		t.Fatal(err)
	}
	for name, files := range map[string]map[string]string{
		"no files": {},
		"a Go starter": {
			"go.mod": "module github.com/acme/widgets\n\ngo 1.27\n", "doc.go": "// Package widgets is a start.\npackage widgets\n",
			"AGENTS.md": "# Rules\n\n- `SPEC.md` is the current state.\n", "SPEC.md": "# What is built\n", "README.md": "# widgets\n",
			"tools/check-format": "#!/bin/sh\n", WorkflowPath: gate,
		},
		// A walk meets a/ before a-b/ and a.b/, where their paths as plain
		// strings sort the other way, and the rules are drafted in a
		// walk's order.
		"rules in directories whose names sort apart from their paths": {
			"go.mod": "module github.com/acme/widgets\n\ngo 1.27\n", "a.txt": "a\n", "LICENSE": "A license.\n",
			"a/AGENTS.md": "# a's rules\n", "a-b/AGENTS.md": "# a-b's rules\n", "a.b/AGENTS.md": "# a.b's rules\n", "z/AGENTS.md": "# z's rules\n",
			"CLAUDE.md": "# Rules\n\nThe source of truth is `docs/PRD.md`.\n", "docs/PRD.md": "# The spec\n",
			"web/package.json": "{\"name\": \"web\", \"private\": true}\n", "cmd/tool/go.mod": "module github.com/acme/widgets/cmd/tool\n\ngo 1.27\n",
			WorkflowPath: gate,
		},
	} {
		t.Run(name, func(t *testing.T) {
			held := map[string][]byte{}
			for name, text := range files {
				held[name] = []byte(text)
			}
			of, unresolvedOf, err := DraftRepositoryOf(held)
			if err != nil {
				t.Fatal(err)
			}
			written, unresolvedWritten, err := DraftRepository(treeOf(t, files))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(of, written) || !slices.Equal(unresolvedOf, unresolvedWritten) {
				t.Errorf("the draft of the files:\n%s\nthe draft once they're written:\n%s", of, written)
			}
		})
	}
}

// A file can't be held where a walk of a checkout couldn't have met one: at
// an unsafe path, under a directory discovery skips, or where the checkout
// would have a directory, or a file on the way.
func TestADraftRefusesAFileItCouldntHaveFound919(t *testing.T) {
	for name, want := range map[string]string{
		"../outside.md":            "isn't a path a checkout can hold",
		"/abs.md":                  "isn't a path a checkout can hold",
		".git/config":              "isn't a path a checkout can hold",
		"node_modules/x/README.md": "discovery reads nothing under node_modules",
	} {
		_, _, err := DraftRepositoryOf(map[string][]byte{"go.mod": []byte("module example.com/x\n\ngo 1.27\n"), name: []byte("x\n")})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("holding %s: %v, want a refusal saying %q", name, err, want)
		}
	}
	// A path can't be a file and a directory, whichever is met first.
	for range 20 {
		_, _, err := DraftRepositoryOf(map[string][]byte{"docs": []byte("a file\n"), "docs/notes.md": []byte("notes\n")})
		if err == nil || !strings.Contains(err.Error(), "discovery reads nothing under docs") && !strings.Contains(err.Error(), "docs is a directory of the checkout") {
			t.Fatalf("holding a file under a file: %v", err)
		}
	}
}

// The commit a gate builds Invariant at is read from the workflow init and
// new write, and from nothing that only looks like it.
func TestTheGatesPinIsReadFromItsWorkflow919(t *testing.T) {
	gate, err := Workflow(ref)
	if err != nil {
		t.Fatal(err)
	}
	if got := Pinned(gate); got != ref {
		t.Errorf("the gate's pin reads as %q, want %s", got, ref)
	}
	for _, other := range []string{
		"",
		"name: ci\non: push\njobs: {}\n",
		strings.Replace(gate, "repository: gitdek/invariant", "repository: someone/else", 1),
		strings.Replace(gate, "ref: "+ref, "ref: main", 1),
		"# ref: " + ref + "\n",
	} {
		if got := Pinned(other); got != "" {
			t.Errorf("a pin %q was read from:\n%s", got, other)
		}
	}
}
