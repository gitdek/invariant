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

// copyExample copies the example to a temporary directory, applying each edit
// to the file whose path ends with its key.
func copyExample(t *testing.T, edits map[string]func(string) string) string {
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
		for suffix, edit := range edits {
			if strings.HasSuffix(path, suffix) {
				b = []byte(edit(string(b)))
			}
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
	return func(src string) string {
		if strings.Count(src, from) != 1 {
			t.Fatalf("%q must occur exactly once", from)
		}
		return strings.Replace(src, from, to, 1)
	}
}

func TestHonestExamplePasses(t *testing.T) {
	t.Parallel()
	r := run(t, copyExample(t, nil))
	if !r.Passed {
		t.Fatalf("the unmodified example failed: %v\n%s", Failed(r), Feedback(r))
	}
}

// Weakening a ratified invariant must fail on its pin, even though TLC
// would happily confirm the weaker statement.
func TestWeakenedInvariantFails(t *testing.T) {
	t.Parallel()
	r := run(t, copyExample(t, map[string]func(string) string{
		"TwoPhase.tla": replace(t, `~(rmState[r1] = "aborted" /\ rmState[r2] = "committed")`, `TRUE`),
	}))
	if r.Passed || !contains(Failed(r), "pin TCConsistent") {
		t.Fatalf("failed = %v; want TCConsistent's pin to fail", Failed(r))
	}
	if !r.Design.Passed {
		t.Error("TLC should pass the weakened spec; the pin is what must catch it")
	}
}

// Redefining something a statement depends on must fail its pin too:
// TypeOK is only as strong as Messages.
func TestChangedDependencyFails(t *testing.T) {
	t.Parallel()
	r := run(t, copyExample(t, map[string]func(string) string{
		"TwoPhase.tla": replace(t, `[type : {"Prepared"}, rm : RM]`, `[type : {"Prepared", "Commit", "Abort"}, rm : RM]`),
	}))
	if r.Passed || !contains(Failed(r), "pin TypeOK") {
		t.Fatalf("failed = %v; want TypeOK's pin to fail", Failed(r))
	}
}

// A model that can never commit satisfies every invariant vacuously. The
// AllCommitted witness must catch that.
func TestVacuousModelFails(t *testing.T) {
	t.Parallel()
	r := run(t, copyExample(t, map[string]func(string) string{"TwoPhase.tla": replace(t, "    \\/ TMCommit\n", "")}))
	if r.Passed || !contains(Failed(r), "witness AllCommitted") {
		t.Fatalf("failed = %v; want the AllCommitted witness to fail", Failed(r))
	}
}

// A model too narrow to show the known bug's damage must fail the bug
// check: here resource managers can no longer abort on their own.
func TestModelTooNarrowForTheBugFails(t *testing.T) {
	t.Parallel()
	r := run(t, copyExample(t, map[string]func(string) string{"TwoPhase.tla": replace(t, "        \\/ RMChooseToAbort(r)\n", "")}))
	if r.Passed || !contains(Failed(r), "bug EarlyCommit") {
		t.Fatalf("failed = %v; want the EarlyCommit bug check to fail", Failed(r))
	}
}

// Code that reaches fewer states than the model must fail agreement, even
// when Gobra and the tests are happy: here the coordinator never aborts.
func TestCodeThatDisagreesWithTheModelFails(t *testing.T) {
	t.Parallel()
	r := run(t, copyExample(t, map[string]func(string) string{
		"explore.go": replace(t, "\tif s.TM == TMInit {\n\t\tnext = append(next, TMAbort(s))\n\t}\n", ""),
	}))
	if r.Passed || !contains(Failed(r), "agreement") {
		t.Fatalf("failed = %v; want agreement to fail", Failed(r))
	}
	if !r.Code.Passed {
		t.Error("Gobra should still pass; agreement is what must catch it")
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
