package gobra

import (
	"os"
	"reflect"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// passed.txt is a real Gobra run over examples/02-twophase-commit/twophase
// with --overflow.
func TestParsePassed(t *testing.T) {
	r := Parse(fixture(t, "passed.txt"), 0)
	if !r.Passed || len(r.Errors) != 0 {
		t.Fatalf("Passed = %v, Errors = %v; want a clean pass", r.Passed, r.Errors)
	}
}

// failed.txt is the same package with RMPrepare no longer sending its
// Prepared message.
func TestParseFailed(t *testing.T) {
	r := Parse(fixture(t, "failed.txt"), 1)
	want := []string{"twophase.go:97:9: Postcondition might not hold."}
	if r.Passed || !reflect.DeepEqual(r.Errors, want) {
		t.Fatalf("Passed = %v, Errors = %q; want %q", r.Passed, r.Errors, want)
	}
}

func TestParseNoSummary(t *testing.T) {
	r := Parse("Exception in thread main", 1)
	if r.Passed || len(r.Errors) != 1 {
		t.Fatalf("Passed = %v, Errors = %v; want one error explaining there was no result", r.Passed, r.Errors)
	}
}

func TestFunctions(t *testing.T) {
	dir := "../../examples/02-twophase-commit/twophase"
	files, err := sources(dir)
	if err != nil {
		t.Fatal(err)
	}
	all, withContract, err := functions(dir, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 10 || !reflect.DeepEqual(all, withContract) {
		t.Errorf("functions = %v, with contracts = %v; want all 10 to carry a contract", all, withContract)
	}
}
