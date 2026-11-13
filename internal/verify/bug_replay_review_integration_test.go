//go:build integration

package verify

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

// What two independent reviews of #857 found in the path's extraction, each
// against the pinned TLC. The plan's own tests answer with canned rows
// whatever module is checked, so none of these could fail there.

const replayReviewCap = `CONSTANT People
VARIABLE n, who
vars == <<n, who>>
P1 == CHOOSE p \in People : TRUE
P2 == CHOOSE p \in People : p # P1
INIT
Put(p) == n < 1 /\ n' = n + 1 /\ who' = who \cup {p}
Done == n = 1 /\ UNCHANGED vars
Next == (\E p \in People : Put(p)) \/ Done
Spec == Init /\ [][Next]_vars
Safe == n <= 1
Break(p) == n = 1 /\ n' = 2 /\ who' = who \cup {p}
Bad == \E p \in People : Break(p)
====
`

func replayReviewExtract(t *testing.T, ctx context.Context, runner tlc.Runner, module string, files map[string]string, bounds map[string]string, expect string) (tlc.ReplayPath, error) {
	t.Helper()
	dir, check := t.TempDir(), t.TempDir()
	for name, body := range files {
		writeReplayIntegration(t, dir, name, body)
		writeReplayIntegration(t, check, name, body)
	}
	writeReplayIntegration(t, check, "ReplayBug.tla", fmt.Sprintf("---- MODULE ReplayBug ----\nEXTENDS %s\nBugSpec == Init /\\ [][Next \\/ Bad]_vars\n====\n", module))
	cfg := tlc.Config{Specification: "BugSpec", Constants: bounds, Invariants: []string{expect}}
	trace, err := runner.Check(ctx, check, "ReplayBug", cfg)
	if err != nil || trace.Outcome != tlc.Violated || trace.Invariant != expect {
		t.Fatalf("the fixture's bug isn't caught as it should be: %+v, %v", trace, err)
	}
	cfg.Specification = "Spec"
	return runner.ExtractReplay(ctx, dir, tlc.ReplaySpec{Module: module, Bug: "Bad", Expect: expect, ExpectKind: project.Invariant, Config: cfg}, trace)
}

func TestPinnedTLCBugReplayReview857(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runner := tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar}
	people := map[string]string{"People": "{p1, p2}"}
	capModel := func(extends, init string) string {
		return "---- MODULE Cap ----\nEXTENDS " + extends + "\n" + strings.Replace(replayReviewCap, "INIT\n", init+"\n", 1)
	}
	honest := capModel("Naturals", `Init == n = 0 /\ who = {}`)

	t.Run("an honest model's path is TLC's", func(t *testing.T) {
		path, err := replayReviewExtract(t, ctx, runner, "Cap", map[string]string{"Cap.tla": honest}, people, "Safe")
		if err != nil {
			t.Fatal(err)
		}
		if len(path.Steps) != 2 || path.Offending != 1 || path.Steps[0].Action != "Put" || path.Steps[0].Bug || path.Steps[1].Action != "Break" || !path.Steps[1].Bug {
			t.Fatalf("the path: %+v", path.Steps)
		}
	})

	// The check reads its answer through PrintT, Cardinality and Len. A
	// model with one of its own, in its module or beside it, had TLC
	// evaluate the model's, and the extracted path was no longer TLC's.
	for _, tc := range []struct{ name, extends, define string }{
		{"PrintT, in a module beside it", "Naturals, Shadow", ""},
		{"Cardinality, in the model", "Naturals", "Cardinality(s) == 1\n"},
		{"Len, in the model", "Naturals", "Len(s) == 2\n"},
	} {
		t.Run("a model that defines "+tc.name, func(t *testing.T) {
			files := map[string]string{"Cap.tla": capModel(tc.extends, tc.define+`Init == n = 0 /\ who = {}`)}
			if strings.Contains(tc.extends, "Shadow") {
				files["Shadow.tla"] = "---- MODULE Shadow ----\nPrintT(x) == TRUE\n====\n"
			}
			path, err := replayReviewExtract(t, ctx, runner, "Cap", files, people, "Safe")
			if err == nil || !strings.Contains(err.Error(), "replay-unsupported-model: the model defines "+strings.SplitN(tc.name, ",", 2)[0]) {
				t.Fatalf("want the model refused, got %+v, %v", path.Steps, err)
			}
		})
	}

	// A model can print rows of its own through another name for PrintT.
	// The check's own row for the edge is printed too, and two are refused.
	t.Run("a model that prints rows of its own", func(t *testing.T) {
		forged := capModel("Naturals, Helper", `Init == n = 0 /\ who = {} /\ Forge(<<"invariant-replay-v1", 0, {<<"Break", <<P2>>, TRUE>>}>>)`)
		files := map[string]string{"Cap.tla": forged, "Helper.tla": "---- MODULE Helper ----\nEXTENDS TLC\nForge(x) == PrintT(x)\n====\n"}
		path, err := replayReviewExtract(t, ctx, runner, "Cap", files, people, "Safe")
		if err == nil {
			t.Fatalf("a path was extracted beside the model's own rows: %+v", path.Steps)
		}
	})

	// The check counts with .., + and -, and a model needn't extend them.
	t.Run("a model that extends no arithmetic", func(t *testing.T) {
		model := `---- MODULE Flags ----
CONSTANT People
VARIABLE on, done
vars == <<on, done>>
Init == on = {} /\ done = FALSE
Turn(p) == ~done /\ p \notin on /\ on' = on \cup {p} /\ UNCHANGED done
Finish == on = People /\ done' = TRUE /\ UNCHANGED on
Stay == done /\ UNCHANGED vars
Next == (\E p \in People : Turn(p)) \/ Finish \/ Stay
Spec == Init /\ [][Next]_vars
Safe == done => on = People
Early(p) == ~done /\ p \notin on /\ on' = on \cup {p} /\ on' # People /\ done' = TRUE
Bad == \E p \in People : Early(p)
====
`
		path, err := replayReviewExtract(t, ctx, runner, "Flags", map[string]string{"Flags.tla": model}, people, "Safe")
		if err != nil {
			t.Fatal(err)
		}
		last := path.Steps[len(path.Steps)-1]
		if path.Offending != len(path.Steps)-1 || last.Action != "Early" || !last.Bug || len(last.Args) != 1 {
			t.Fatalf("the path: %+v", path.Steps)
		}
	})

	// The check declares each model value as a constant, so a step can't
	// bind a variable of the same name in it.
	t.Run("a step that binds a model value's name", func(t *testing.T) {
		path, err := replayReviewExtract(t, ctx, runner, "Cap", map[string]string{"Cap.tla": honest}, map[string]string{"People": "{p, q}"}, "Safe")
		if err == nil || !strings.Contains(err.Error(), "replay-unsupported-action: Put binds p, which is also a constant or a model value") {
			t.Fatalf("want the binder refused, got %+v, %v", path.Steps, err)
		}
	})

	// A step whose body chooses part of its next state names no argument
	// for the choice, so a path through it can't be handed to the code.
	t.Run("a path through a step that hides a choice", func(t *testing.T) {
		hidden := strings.Replace(honest, `Put(p) == n < 1 /\ n' = n + 1 /\ who' = who \cup {p}`, `Put == n < 1 /\ \E p \in People : n' = n + 1 /\ who' = who \cup {p}`, 1)
		hidden = strings.Replace(hidden, `Next == (\E p \in People : Put(p)) \/ Done`, `Next == Put \/ Done`, 1)
		path, err := replayReviewExtract(t, ctx, runner, "Cap", map[string]string{"Cap.tla": hidden}, people, "Safe")
		if err == nil || !strings.Contains(err.Error(), "replay-unsupported-action: edge 0 takes Put, whose body chooses part of its next state") {
			t.Fatalf("want the path refused, got %+v, %v", path.Steps, err)
		}
	})
}
