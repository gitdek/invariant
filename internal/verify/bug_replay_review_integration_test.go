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
	// for the choice, so a path through it can't be handed to the code. TLC
	// decides it at the step's own state, whatever the body's text looks
	// like: with the step's arguments fixed, could it have gone elsewhere?
	put := `Put(p) == n < 1 /\ n' = n + 1 /\ who' = who \cup {p}`
	next := `Next == (\E p \in People : Put(p)) \/ Done`
	bare := func(body string) string {
		return strings.Replace(strings.Replace(honest, put, body, 1), next, `Next == Put \/ Done`, 1)
	}
	for name, model := range map[string]string{
		"an \\E over the update":         bare(`Put == n < 1 /\ \E p \in People : n' = n + 1 /\ who' = who \cup {p}`),
		"\\exists, spelled out":          bare(`Put == n < 1 /\ \exists p \in People : n' = n + 1 /\ who' = who \cup {p}`),
		"a disjunction of updates":       bare(`Put == /\ n < 1 /\ n' = n + 1 /\ (\/ who' = who \cup {P1} \/ who' = who \cup {P2})`),
		"a choice behind another name":   bare("Pick == \\E p \\in People : who' = who \\cup {p}\nPut == n < 1 /\\ n' = n + 1 /\\ Pick"),
		"a primed variable from a set":   bare(`Put == n < 1 /\ n' = n + 1 /\ (who') \in {{P1}, {P2}}`),
		"a choice beside its argument's": strings.Replace(honest, put, `Put(p) == n < 1 /\ \E q \in People : n' = n + 1 /\ who' = who \cup {p, q}`, 1),
	} {
		t.Run("a path through a step that hides a choice: "+name, func(t *testing.T) {
			path, err := replayReviewExtract(t, ctx, runner, "Cap", map[string]string{"Cap.tla": model}, people, "Safe")
			// With a choice beside an argument, the step TLC took can also
			// be one two arguments explain. Either way it names no step.
			if err == nil || !strings.Contains(err.Error(), "step 1 of the counterexample, from its state 1 to its state 2,") ||
				!(strings.Contains(err.Error(), "replay-unsupported-action:") && strings.Contains(err.Error(), "could have reached another state with the same arguments") ||
					strings.Contains(name, "beside") && strings.Contains(err.Error(), "replay-ambiguous-edge:")) {
				t.Fatalf("want the path refused at its first step, got %+v, %v", path.Steps, err)
			}
		})
	}
	// An \E that only guards, and a primed variable that's only tested,
	// choose nothing: the step has one successor, and its path is taken.
	for name, body := range map[string]string{
		"an \\E that only guards":         `Put(p) == n < 1 /\ (\E q \in People : q \notin who) /\ n' = n + 1 /\ who' = who \cup {p}`,
		"a primed variable that's tested": `Put(p) == n < 1 /\ n' = n + 1 /\ n' \in Nat /\ who' = who \cup {p}`,
	} {
		t.Run("a path through a step with "+name, func(t *testing.T) {
			path, err := replayReviewExtract(t, ctx, runner, "Cap", map[string]string{"Cap.tla": strings.Replace(honest, put, body, 1)}, people, "Safe")
			if err != nil || len(path.Steps) != 2 || path.Steps[0].Action != "Put" || len(path.Steps[0].Args) != 1 || path.Steps[1].Action != "Break" || !path.Steps[1].Bug {
				t.Fatalf("want the path, got %+v, %v", path.Steps, err)
			}
		})
	}
	// A bug's operation that takes a step an action of Next takes too
	// gives that step two readings, and TLC can't say which one took it.
	t.Run("a bug's operation that takes a normal step too", func(t *testing.T) {
		overlapping := strings.Replace(honest, `Break(p) == n = 1 /\ n' = 2 /\ who' = who \cup {p}`, `Break(p) == n' = n + 1 /\ who' = who \cup {p}`, 1)
		path, err := replayReviewExtract(t, ctx, runner, "Cap", map[string]string{"Cap.tla": overlapping}, people, "Safe")
		if err == nil || !strings.Contains(err.Error(), "replay-ambiguous-edge: step 1 of the counterexample, from its state 1 to its state 2, has no single operation that takes it") {
			t.Fatalf("want the path refused at its first step, got %+v, %v", path.Steps, err)
		}
	})
}
