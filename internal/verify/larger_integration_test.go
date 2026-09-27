//go:build integration

package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The log buffer's code has no bounds in it: it's proved at every size, and
// it agrees with the model one size past the bounds too (D-0068).
func TestTheLogBufferHoldsAtEverySize(t *testing.T) {
	r := run(t, filepath.Join("..", "..", "examples", "03-log-buffer"))
	if !r.Passed || !r.EverySize() {
		t.Fatalf("the log buffer should be proved at every size: passed %v, larger %+v", r.Passed, r.Larger)
	}
}

// Code that hardcodes a size fails, even when Gobra proves it: its explorer
// names its bounds, so it must agree one size larger, and it can't.
func TestAHardcodedSizeFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "03-log-buffer")
	if err := copyTree(filepath.Join("..", "..", "examples", "03-log-buffer"), dir); err != nil {
		t.Fatal(err)
	}
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
