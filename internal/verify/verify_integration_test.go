//go:build integration

// These tests run the real verifiers in Docker. They show the gate passes the
// honest example and rejects the cheapest ways to game it:
//
//	go test -tags integration ./internal/verify/
package verify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/toolchain"
)

const example = "../../examples/02-twophase-commit"

// copyExample copies the example to a temporary directory and applies edit
// to its TLA+ module.
func copyExample(t *testing.T, edit func(spec string) string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(example, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(example, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, "TwoPhase.tla") {
			b = []byte(edit(string(b)))
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func run(t *testing.T, dir string) *Report {
	t.Helper()
	ctx := context.Background()
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Run(ctx, dir, "", tc)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func replace(t *testing.T, from, to string) func(string) string {
	return func(spec string) string {
		if strings.Count(spec, from) != 1 {
			t.Fatalf("%q must occur exactly once in the spec", from)
		}
		return strings.Replace(spec, from, to, 1)
	}
}

func TestHonestExamplePasses(t *testing.T) {
	t.Parallel()
	r := run(t, copyExample(t, func(s string) string { return s }))
	if !r.Passed {
		t.Fatalf("the unmodified example failed: %+v", r)
	}
}

// Weakening a ratified invariant must fail on its pin, even though TLC
// would happily confirm the weaker statement.
func TestWeakenedInvariantFails(t *testing.T) {
	t.Parallel()
	weaken := replace(t, `~(rmState[r1] = "aborted" /\ rmState[r2] = "committed")`, `TRUE`)
	r := run(t, copyExample(t, weaken))
	if r.Passed {
		t.Fatal("the gate passed a weakened TCConsistent")
	}
	for _, p := range r.Pins {
		if p.Name == "TCConsistent" && p.Match {
			t.Error("TCConsistent's pin still matches after weakening it")
		}
	}
	if !r.Design.Passed {
		t.Error("TLC should pass the weakened spec; the pin is what must catch it")
	}
}

// A model that can never commit satisfies every invariant vacuously. The
// AllCommitted witness must catch that.
func TestVacuousModelFails(t *testing.T) {
	t.Parallel()
	neverCommit := replace(t, "    \\/ TMCommit\n", "")
	r := run(t, copyExample(t, neverCommit))
	if r.Passed {
		t.Fatal("the gate passed a model that can never commit")
	}
	for _, w := range r.Witnesses {
		if w.Name == "AllCommitted" && w.Reached {
			t.Error("AllCommitted was reached in a model without TMCommit")
		}
	}
}

// Removing the text the known bug patches must fail the mutant check, so
// the planted bug can't quietly stop being tested.
func TestUnappliableMutantFails(t *testing.T) {
	t.Parallel()
	rename := replace(t, "/\\ tmPrepared = RM\n", "/\\ RM \\subseteq tmPrepared\n")
	r := run(t, copyExample(t, rename))
	if r.Passed {
		t.Fatal("the gate passed although the known bug could no longer be applied")
	}
	if len(r.Mutants) != 1 || r.Mutants[0].Caught {
		t.Errorf("mutants = %+v; want the early-commit mutant reported as not caught", r.Mutants)
	}
}
