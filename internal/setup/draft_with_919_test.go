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

// listing is every path under dir, in the order a walk meets them.
func listing(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	err := filepath.WalkDir(dir, func(path string, _ os.DirEntry, err error) error {
		names = append(names, path)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// The checks a checkout would have once files are written to it are the
// checks it has afterwards: the draft with files added is, byte for byte,
// the draft of the checkout that holds them, whatever was there before and
// however the new names sort among the old. Nothing is written to find out.
func TestADraftWithFilesAddedIsTheDraftOnceTheyAreWritten919(t *testing.T) {
	gate, err := Workflow(ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name         string
		there, added map[string]string
	}{
		{"a checkout that isn't there yet", nil, map[string]string{
			"go.mod": "module github.com/acme/widgets\n\ngo 1.27\n", "doc.go": "// Package widgets is a start.\npackage widgets\n",
			"AGENTS.md": "# Rules\n\n- `SPEC.md` is the current state.\n", "SPEC.md": "# What is built\n", "README.md": "# widgets\n",
			"tools/check-format": "#!/bin/sh\n", WorkflowPath: gate,
		}},
		{"an empty checkout", map[string]string{}, map[string]string{
			"go.mod": "module github.com/acme/widgets\n\ngo 1.27\n", "AGENTS.md": "# Rules\n", WorkflowPath: gate,
		}},
		{"a checkout with files of its own, some among the new ones' names", map[string]string{
			"LICENSE": "A license.\n", "a.txt": "a\n", "z/AGENTS.md": "# z's rules\n", "CLAUDE.md": "# Rules\n\nThe source of truth is `docs/PRD.md`.\n",
			"web/package.json": "{\"name\": \"web\", \"private\": true}\n", "go.mod": "module github.com/acme/widgets\n\ngo 1.27\n",
		}, map[string]string{
			"go.mod": "module github.com/acme/widgets\n\ngo 1.27\n", "doc.go": "// Package widgets is a start.\npackage widgets\n",
			"a/AGENTS.md": "# a's rules\n", "a-b/AGENTS.md": "# a-b's rules\n", "a.b/AGENTS.md": "# a.b's rules\n",
			"docs/PRD.md": "# The spec\n", "cmd/tool/go.mod": "module github.com/acme/widgets/cmd/tool\n\ngo 1.27\n", WorkflowPath: gate,
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			base, before := "", []string(nil)
			if c.there != nil {
				base = treeOf(t, c.there)
				before = listing(t, base)
			}
			added := map[string][]byte{}
			whole := map[string]string{}
			for name, text := range c.there {
				whole[name] = text
			}
			for name, text := range c.added {
				added[name], whole[name] = []byte(text), text
			}
			with, unresolvedWith, err := DraftRepositoryWith(base, added)
			if err != nil {
				t.Fatal(err)
			}
			after, unresolvedAfter, err := DraftRepository(treeOf(t, whole))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(with, after) || !slices.Equal(unresolvedWith, unresolvedAfter) {
				t.Errorf("the draft with the files added:\n%s\nthe draft once they're written:\n%s", with, after)
			}
			if base != "" && !slices.Equal(listing(t, base), before) {
				t.Errorf("drafting with files added changed the checkout: %q, was %q", listing(t, base), before)
			}
		})
	}
	// With nothing to add, it is DraftRepository.
	dir := treeOf(t, map[string]string{"go.mod": "module example.com/x\n\ngo 1.27\n", "b/AGENTS.md": "# b\n", "a/AGENTS.md": "# a\n"})
	plain, _, err := DraftRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	if same, _, err := DraftRepositoryWith(dir, nil); err != nil || !bytes.Equal(same, plain) {
		t.Errorf("with nothing added the draft differs from DraftRepository's: %v", err)
	}
}

// A file can't be added where the draft couldn't have found it written: at
// an unsafe path, under a directory discovery skips, under a file, or where
// the checkout holds a directory or a link.
func TestADraftRefusesAFileItCouldntHaveFound919(t *testing.T) {
	dir := treeOf(t, map[string]string{"go.mod": "module example.com/x\n\ngo 1.27\n", "docs/notes.md": "notes\n", "plain": "a file\n"})
	if err := os.Symlink("go.mod", filepath.Join(dir, "linked.mod")); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"../outside.md":            "isn't a path a checkout can hold",
		"/abs.md":                  "isn't a path a checkout can hold",
		".git/config":              "isn't a path a checkout can hold",
		"node_modules/x/README.md": "discovery reads nothing under node_modules",
		"plain/under.md":           "discovery reads nothing under plain",
		"docs":                     "has a directory there",
		"linked.mod":               "isn't a regular file",
	} {
		_, _, err := DraftRepositoryWith(dir, map[string][]byte{name: []byte("x\n")})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("adding %s: %v, want a refusal saying %q", name, err, want)
		}
	}
	if _, _, err := DraftRepositoryWith(filepath.Join(dir, "missing"), map[string][]byte{"go.mod": []byte("module example.com/x\n")}); err == nil {
		t.Error("a directory that isn't there was taken for a checkout")
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
