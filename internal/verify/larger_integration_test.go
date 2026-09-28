//go:build integration

package verify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// The log buffer's code has no bounds in it: it's proved at every size, and
// it agrees with the model one size past the bounds too (D-0068).
func TestTheLogBufferHoldsAtEverySize(t *testing.T) {
	r := run(t, copyProject(t, "testdata/examples/03-log-buffer", nil))
	if !r.Passed || !r.EverySize() {
		t.Fatalf("the log buffer should be proved at every size: passed %v, larger %+v", r.Passed, r.Larger)
	}
}

// Code that hardcodes a size fails, even when Gobra proves it: its explorer
// names its bounds, so it must agree one size larger, and it can't.
func TestAHardcodedSizeFails(t *testing.T) {
	dir := copyProject(t, "testdata/examples/03-log-buffer", nil)
	code := filepath.Join(dir, "logbuffer", "logbuffer.go")
	b, err := os.ReadFile(code)
	if err != nil {
		t.Fatal(err)
	}
	// A pool of two, whatever it's asked for, with a contract that says so.
	planted := strings.Replace(string(b), "// @ ensures len(b.Slots) == capacity && b.N == 0", "// @ ensures len(b.Slots) == 2 && b.N == 0", 1)
	planted = strings.Replace(planted, "Slots: make([]Line, capacity)", "Slots: make([]Line, 2)", 1)
	planted = strings.Replace(planted, "// @ requires 0 < capacity && capacity <= MaxCapacity", "// @ requires capacity == 2", 1)
	if planted == string(b) || strings.Count(planted, "make([]Line, 2)") != 1 {
		t.Fatal("couldn't plant the bound: the log buffer's New changed")
	}
	if err := os.WriteFile(code, []byte(planted), 0o644); err != nil {
		t.Fatal(err)
	}
	r := run(t, dir)
	if r.Code == nil || !r.Code.Passed {
		t.Fatalf("Gobra should still prove the planted code: %+v", r.Code)
	}
	if r.Agreement == nil || !r.Agreement.Passed {
		t.Fatalf("at the ratified bounds, the planted code still agrees: %+v", r.Agreement)
	}
	if r.Passed || r.Larger == nil || !r.Larger.Required || r.Larger.Passed {
		t.Fatalf("one size larger should catch the hardcoded size: passed %v, larger %+v", r.Passed, r.Larger)
	}
}

// tickets is a TypeScript pool of tickets whose driver names its bounds and
// counts (D-0076), built from testdata/tickets with its statements pinned.
func tickets(t *testing.T, edit func(code string) string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tickets")
	if err := copyTree(filepath.Join("testdata", "tickets"), dir); err != nil {
		t.Fatal(err)
	}
	specs := filepath.Join(dir, ".invariant", "specs")
	if err := os.MkdirAll(specs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "Tickets.tla"), filepath.Join(specs, "Tickets.tla")); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		code := filepath.Join(dir, "src", "tickets.ts")
		b, err := os.ReadFile(code)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(code, []byte(edit(string(b))), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := project.Manifest{Name: "tickets", Module: ".invariant/specs/Tickets.tla", Code: "src", Language: "typescript", Conformance: "conformance.ts", Exhaustive: true}
	lock := project.Lock{Decision: "a test", Bounds: map[string]string{"Capacity": "2", "Clients": "{c1, c2, c3}"}, Statements: []project.Statement{
		{Name: "Spec", Kind: project.Spec, Says: "Clients take and give back tickets."},
		{Name: "TypeOK", Kind: project.Invariant, Says: "Only clients hold tickets."},
		{Name: "AtMostCapacity", Kind: project.Invariant, Says: "No more tickets are out than the pool has."},
		{Name: "Full", Kind: project.Witness, Says: "Every ticket can be out."},
		{Name: "TakeWhenFull", Kind: project.Bug, Says: "A client takes a ticket when none is left.", Expect: "AtMostCapacity"},
	}}
	for file, v := range map[string]any{"invariant.json": manifest, "ratified.lock": lock} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".invariant", file), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Pin(); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A TypeScript driver that names its bounds counts one size larger, and
// code with no bounds in it reaches exactly what the model does there.
func TestATypeScriptDriverCountsOneSizeLarger(t *testing.T) {
	r := run(t, tickets(t, nil))
	if !r.Passed || r.Larger == nil || !r.Larger.Passed || r.Larger.States != 15 || r.Larger.Depth != 4 {
		t.Fatalf("passed %v, larger %+v, conformance %+v; want 15 states in 4 levels one size larger", r.Passed, r.Larger, r.Conformance)
	}
	if r.EverySize() {
		t.Error("TypeScript has no proof, so nothing is claimed at every size")
	}
}

// TypeScript code that hardcodes a size passes at the ratified bounds and
// fails one size larger.
func TestAHardcodedSizeFailsInTypeScript(t *testing.T) {
	r := run(t, tickets(t, func(code string) string {
		planted := strings.Replace(code, "this.holders.size >= this.capacity", "this.holders.size >= 2", 1)
		if planted == code {
			t.Fatal("couldn't plant the bound")
		}
		return planted
	}))
	if r.Conformance == nil || !r.Conformance.Passed {
		t.Fatalf("at the ratified bounds, the planted code still conforms: %+v", r.Conformance)
	}
	if r.Passed || r.Larger == nil || !r.Larger.Required || r.Larger.Passed || r.Larger.States != 11 {
		t.Fatalf("one size larger should catch the hardcoded size: passed %v, larger %+v", r.Passed, r.Larger)
	}
}
