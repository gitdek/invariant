package formalize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workspace copies testdata/buffer into a temporary workspace, applying
// edits to its files.
func workspace(t *testing.T, edits map[string]func(string) string) string {
	t.Helper()
	ws := t.TempDir()
	for _, name := range []string{"BoundedBuffer.tla", "proposal.json"} {
		b, err := os.ReadFile(filepath.Join("testdata", "buffer", name))
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		if edit, ok := edits[name]; ok {
			text = edit(text)
		}
		if err := os.WriteFile(filepath.Join(ws, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}

func replace(from, to string) func(string) string {
	return func(s string) string { return strings.Replace(s, from, to, 1) }
}

func TestReadPinsTheDraft(t *testing.T) {
	p, err := Read(workspace(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Ratifiable() || !strings.HasPrefix(p.Hash, "sha256:") {
		t.Fatalf("proposal = %+v", p)
	}
	for _, s := range p.Statements {
		if !strings.HasPrefix(s.SHA256, "sha256:") {
			t.Errorf("%s isn't pinned", s.Name)
		}
	}
	pinned, err := p.Pinned()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"WithinCap ==", "PutWhenFull ==", "Spec ==", "vars =="} {
		if !strings.Contains(pinned, want) {
			t.Errorf("the pinned text lacks %q", want)
		}
	}
	for _, model := range []string{"Init ==", "Next ==", "Put(m) ==", "Take =="} {
		if strings.Contains(pinned, model) {
			t.Errorf("the pinned text includes the model's %q", model)
		}
	}
	// The hash covers the plain-language meaning as well as the TLA+.
	q, _ := Read(workspace(t, map[string]func(string) string{"proposal.json": replace("never holds more than", "rarely holds more than")}))
	if q.Hash == p.Hash {
		t.Error("changing what a statement says must change the proposal's hash")
	}
}

func TestReadRejectsBadDrafts(t *testing.T) {
	for name, tc := range map[string]struct {
		edits map[string]func(string) string
		want  string
	}{
		"slug":         {map[string]func(string) string{"proposal.json": replace(`"bounded-buffer"`, `"Bounded Buffer"`)}, "kebab case"},
		"module name":  {map[string]func(string) string{"BoundedBuffer.tla": replace("MODULE BoundedBuffer", "MODULE Buffer")}, "must begin with ---- MODULE BoundedBuffer"},
		"no module":    {map[string]func(string) string{"proposal.json": replace(`"module": "BoundedBuffer"`, `"module": "Missing"`)}, "Missing.tla can't be read"},
		"undefined":    {map[string]func(string) string{"proposal.json": replace(`"name": "CanFill"`, `"name": "CanOverflow"`)}, "CanOverflow"},
		"bad expect":   {map[string]func(string) string{"proposal.json": replace(`"expect": "WithinCap"`, `"expect": "Nope"`)}, "isn't a ratified invariant"},
		"no spec":      {map[string]func(string) string{"proposal.json": replace(`"kind": "spec"`, `"kind": "invariant"`)}, `want exactly one statement of kind "spec"`},
		"lonely fork":  {map[string]func(string) string{"proposal.json": replace(`"forks": []`, `"forks": [{"id": "F1", "question": "Which?", "options": [{"id": "A", "says": "This."}]}]`)}, "at least two options"},
		"model is not": {map[string]func(string) string{"proposal.json": replace(`"name": "CanFill"`, `"name": "Next"`)}, "belongs to the factory's model"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Read(workspace(t, tc.edits))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v; want %q", err, tc.want)
			}
		})
	}
}

func TestForksNeedNoModule(t *testing.T) {
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "proposal.json"), []byte(`{"forks": [{"id": "F1", "question": "When the buffer is full, what happens?",
		"options": [{"id": "A", "says": "The producer waits."}, {"id": "B", "says": "The oldest item is dropped."}]}]}`), 0o644)
	p, err := Read(ws)
	if err != nil || p.Ratifiable() || len(p.Forks) != 1 {
		t.Fatalf("p = %+v, err = %v", p, err)
	}
	if o, ok := p.Forks[0].Option("b"); !ok || o.Says != "The oldest item is dropped." {
		t.Error("options are found by id, in either case")
	}
}

func TestWriteLaysOutAProject(t *testing.T) {
	p, err := Read(workspace(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := p.Write(dir, "# Add a bounded buffer\n", nil); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{".invariant/invariant.json", ".invariant/ratified.lock", ".invariant/specs/BoundedBuffer.tla", ".invariant/request.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Error(err)
		}
	}
}

func TestRequestMarkdown(t *testing.T) {
	r := Request{Repo: "o/r", Issue: 7, Title: "Add a bounded buffer", Body: "Producers and a shipper.", Author: "gitdek",
		Thread:  []Message{{By: "gitdek", Body: "Keep it small."}},
		Answers: []Answer{{Fork: "F1", Question: "When it's full?", Option: "A", Says: "The producer waits.", By: "gitdek", Comment: "https://c"}}}
	got := r.Markdown()
	for _, want := range []string{"# Add a bounded buffer", "Issue #7 in o/r, opened by @gitdek.", "## Decided", "**F1. When it's full?** A. The producer waits. (decided by @gitdek: https://c)", "**@gitdek:**\n\nKeep it small."} {
		if !strings.Contains(got, want) {
			t.Errorf("markdown lacks %q:\n%s", want, got)
		}
	}
}
