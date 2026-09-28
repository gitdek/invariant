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

const (
	example           = "../../examples/02-twophase-commit"
	typescriptExample = "../../examples/02-twophase-commit-ts"
	pythonExample     = "../../examples/02-twophase-commit-py"
	provedPython      = "../../examples/02-twophase-commit-py-proved"
)

// copyExample copies the Go example to a temporary directory, applying each
// edit to the file whose path ends with its key.
func copyExample(t *testing.T, edits map[string]func(string) string) string {
	t.Helper()
	return copyProject(t, example, edits)
}

func copyProject(t *testing.T, example string, edits map[string]func(string) string) string {
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

// Conformance: the TypeScript and Python implementations are tested against
// the same model. TypeScript has no proof, and the gate says so rather than
// claiming one. The Python example's core is proved with Nagini too, since
// its rebuild on #59.
func TestConformingImplementationsPass(t *testing.T) {
	for dir, assurance := range map[string]string{typescriptExample: "tested against the model", pythonExample: "proved"} {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			t.Parallel()
			r := run(t, copyProject(t, dir, nil))
			if !r.Passed || r.Conformance == nil || (r.Code != nil) != (assurance == "proved") || r.Assurance != assurance {
				t.Fatalf("passed = %v, assurance = %q, failed = %v\n%s", r.Passed, r.Assurance, Failed(r), Feedback(r))
			}
			if r.Conformance.States < 250 {
				t.Errorf("the driver visited only %d of %d model states", r.Conformance.States, r.Conformance.ModelStates)
			}
		})
	}
}

// The same early-commit bug, planted in ordinary TypeScript and Python code,
// must be caught: the coordinator commits after a single vote, which no
// action of the model allows.
func TestEarlyCommitInCodeFailsConformance(t *testing.T) {
	cases := map[string]map[string]func(string) string{
		typescriptExample: {"src/transaction.ts": replace(t, "this.#votes.size < this.participants.length", "this.#votes.size === 0")},
		pythonExample:     {"twophase/core.py": replace(t, "        if not ok:\n            return False\n        self.decided = True\n", "        if not any(votes):\n            return False\n        self.decided = True\n")},
	}
	for dir, edits := range cases {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			t.Parallel()
			r := run(t, copyProject(t, dir, edits))
			if r.Passed || !contains(Failed(r), "conformance") || r.Conformance.BadStep == nil {
				t.Fatalf("failed = %v; want conformance to catch the early commit", Failed(r))
			}
			if !strings.Contains(r.Conformance.BadStep.To, `tmState = "done"`) {
				t.Errorf("the bad step should be the commit: %+v", r.Conformance.BadStep)
			}
		})
	}
}

// Nagini proves the Python core, and its driver explores the core
// completely, so the receipt says "proved" and every model state is visited.
func TestProvedPythonPasses(t *testing.T) {
	t.Parallel()
	r := run(t, copyProject(t, provedPython, nil))
	if !r.Passed || r.Assurance != "proved" || r.Code == nil || r.Code.Verifier != "Nagini" {
		t.Fatalf("passed = %v, assurance = %q, failed = %v\n%s", r.Passed, r.Assurance, Failed(r), Feedback(r))
	}
	if int64(r.Conformance.States) != r.Conformance.ModelStates {
		t.Errorf("visited %d of %d model states; the driver should cover them all", r.Conformance.States, r.Conformance.ModelStates)
	}
}

// A proof covers every state a contract allows, not only the states a run
// reaches. This tm_commit is right in every reachable state, where the
// coordinator can't have aborted yet, so the tests and conformance pass. Its
// precondition doesn't rule an abort out, and Nagini rejects it.
func TestNaginiSeesPastTheReachableStates(t *testing.T) {
	t.Parallel()
	r := run(t, copyProject(t, provedPython, map[string]func(string) string{
		"twophase/core.py": replace(t, "    s.commit_msg = True\n", "    s.commit_msg = not s.abort_msg\n"),
	}))
	if r.Passed || !contains(Failed(r), "code") || !strings.Contains(strings.Join(r.Code.Errors, "\n"), "tm_commit") {
		t.Fatalf("failed = %v, errors = %v; want Nagini to reject tm_commit", Failed(r), r.Code.Errors)
	}
	if !r.Build.Passed || !r.Conformance.Passed {
		t.Error("the tests and conformance should pass; only the proof can see this")
	}
}
