package formalize

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// current is the testdata buffer, ratified.
func current(t *testing.T) *Current {
	t.Helper()
	p, err := Read(workspace(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	return &Current{Dir: "examples/03-bounded-buffer", ModuleText: p.ModuleText,
		Manifest: project.Manifest{Name: "bounded buffer", Module: ".invariant/specs/BoundedBuffer.tla", Code: "buffer", Language: "go"},
		Lock:     project.Lock{Ratified: &project.Ratification{By: "gitdek", Issue: 1}, Bounds: p.Bounds, Statements: p.Statements}}
}

func TestDiff(t *testing.T) {
	c := current(t)
	// Drop the witness, reword and retext WithinCap, add an invariant, and
	// widen a bound.
	ws := workspace(t, map[string]func(string) string{
		"BoundedBuffer.tla": func(s string) string {
			s = strings.Replace(s, "WithinCap == Len(buf) <= Cap", "WithinCap == Len(buf) < Cap + 1", 1)
			return strings.Replace(s, "CanFill == Len(buf) = Cap", "CanFill == Len(buf) = Cap\n\nNeverNegative == Len(buf) >= 0", 1)
		},
		"proposal.json": func(s string) string {
			s = strings.Replace(s, `{"name": "CanFill", "kind": "witness", "says": "The buffer can fill up."},`,
				`{"name": "NeverNegative", "kind": "invariant", "says": "The buffer's length is never negative."},`, 1)
			s = strings.Replace(s, "The buffer never holds more than its capacity.", "The buffer never holds more than its capacity, strictly counted.", 1)
			return strings.Replace(s, `"Cap": "2"`, `"Cap": "3"`, 1)
		},
	})
	p, err := Read(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Amend(c); err != nil {
		t.Fatal(err)
	}
	d := Diff(c, p)
	got := map[string]string{}
	for _, ch := range d.Statements {
		got[ch.Name] = ch.How
	}
	want := map[string]string{"Spec": Unchanged, "WithinCap": Changed, "TypeOK": Unchanged, "NeverNegative": Added, "PutWhenFull": Unchanged, "CanFill": Removed}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("changes = %v", got)
	}
	if !reflect.DeepEqual(d.Bounds, []BoundChange{{Name: "Cap", From: "2", To: "3"}}) {
		t.Errorf("bounds = %+v", d.Bounds)
	}
	for _, ch := range d.Of(Changed) {
		if !strings.Contains(ch.OldText, "<= Cap") || !strings.Contains(ch.NewText, "< Cap + 1") {
			t.Errorf("a changed statement carries its old and new text: %+v", ch)
		}
	}
	if p.Target == nil || p.Target.Dir != c.Dir || p.Target.Previous != "#1" || p.Target.Amends != project.ProposalHash(c.Lock.Bounds, c.Lock.Statements) {
		t.Errorf("target = %+v", p.Target)
	}
	if m := p.Manifest(); !reflect.DeepEqual(m, c.Manifest) {
		t.Errorf("an amendment keeps the project's manifest: %+v", m)
	}
}

func TestAmendKeepsTheModule(t *testing.T) {
	c := current(t)
	p, err := Read(workspace(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	p.Module = "Buffer"
	if err := p.Amend(c); err == nil || !strings.Contains(err.Error(), "keeps its module") {
		t.Errorf("err = %v", err)
	}
}

// An amendment's agent starts from the project's module and its ratified
// statements.
func TestSeedAmendment(t *testing.T) {
	c := current(t)
	ws := t.TempDir()
	if err := seedAmendment(ws, c); err != nil {
		t.Fatal(err)
	}
	p, err := Read(ws)
	if err != nil {
		t.Fatal(err)
	}
	if p.Hash != project.ProposalHash(c.Lock.Bounds, c.Lock.Statements) {
		t.Error("an untouched amendment's draft is exactly what's ratified")
	}
	if !strings.Contains(Prompt(Request{Repo: "o/r", Issue: 5, Title: "t", Current: c}, 4), "This issue changes an existing project, `examples/03-bounded-buffer`") {
		t.Error("the prompt should say this is an amendment")
	}
}

// A statement whose own text stays the same can still change, through a
// definition it depends on: Spec changes when vars gains a variable.
func TestDiffThroughAHelper(t *testing.T) {
	c := current(t)
	p, err := Read(workspace(t, map[string]func(string) string{
		"BoundedBuffer.tla": func(s string) string { return strings.Replace(s, "vars == <<buf>>", "vars == <<buf, dropped>>", 1) },
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Amend(c); err != nil {
		t.Fatal(err)
	}
	d := Diff(c, p)
	for _, ch := range d.Statements {
		if ch.Name != "Spec" {
			continue
		}
		if ch.How != Changed || !ch.OwnTextSame() || !reflect.DeepEqual(ch.Through, []string{"vars"}) {
			t.Errorf("Spec should change only through vars: %+v", ch)
		}
	}
	if len(d.Definitions) != 1 || d.Definitions[0].Name != "vars" || !strings.Contains(d.Definitions[0].NewText, "dropped") {
		t.Errorf("definitions = %+v", d.Definitions)
	}
}
