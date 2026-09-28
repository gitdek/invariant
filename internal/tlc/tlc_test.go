package tlc

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

// passed.txt is a real TLC 2.19 run of TwoPhase.tla with RM = {r1, r2, r3}.
func TestParsePassed(t *testing.T) {
	r := Parse(fixture(t, "passed.txt"), 0)
	if r.Outcome != Passed {
		t.Fatalf("Outcome = %s (%s); want passed", r.Outcome, r.Message)
	}
	if r.DistinctStates != 288 || r.StatesGenerated != 1146 || r.Depth != 11 {
		t.Errorf("stats = %d distinct, %d generated, depth %d; want 288, 1146, 11",
			r.DistinctStates, r.StatesGenerated, r.Depth)
	}
	if r.Version != "TLC2 Version 2.19 of 08 August 2024 (rev: 5a47802)" {
		t.Errorf("Version = %q", r.Version)
	}
}

// violated.txt is the same spec with the coordinator committing early.
func TestParseViolated(t *testing.T) {
	r := Parse(fixture(t, "violated.txt"), 12)
	if r.Outcome != Violated || r.Invariant != "TCConsistent" {
		t.Fatalf("Outcome = %s, Invariant = %q; want violated TCConsistent", r.Outcome, r.Invariant)
	}
	var actions []string
	for _, s := range r.Trace {
		actions = append(actions, s.Action)
	}
	want := []string{"Initial predicate", "RMPrepare", "TMRcvPrepared", "TMCommit", "RMRcvCommitMsg", "RMChooseToAbort"}
	if !reflect.DeepEqual(actions, want) {
		t.Fatalf("trace actions = %v; want %v", actions, want)
	}
	last := r.Trace[len(r.Trace)-1]
	if len(last.Vars) != 4 || last.Vars[1].Name != "rmState" {
		t.Fatalf("last state vars = %+v", last.Vars)
	}
	if got := last.Vars[1].Value; got != `(r1 :> "committed" @@ r2 :> "aborted" @@ r3 :> "working")` {
		t.Errorf("rmState = %s", got)
	}
}

func TestParseFailed(t *testing.T) {
	out := "@!@!@STARTMSG 1000:1 @!@!@\nCould not parse module TwoPhase\n@!@!@ENDMSG 1000 @!@!@\n"
	r := Parse(out, 150)
	if r.Outcome != Failed || r.Message != "Could not parse module TwoPhase" {
		t.Errorf("Outcome = %s, Message = %q; want error with the parse message", r.Outcome, r.Message)
	}
}

func TestParseValue(t *testing.T) {
	cases := map[string]any{
		`{}`:                                    []any{},
		`(r1 :> "working" @@ r2 :> "prepared")`: map[string]any{"r1": "working", "r2": "prepared"},
		`{[type |-> "Commit"], [type |-> "Prepared", rm |-> r1]}`: []any{
			map[string]any{"type": "Commit"},
			map[string]any{"type": "Prepared", "rm": "r1"},
		},
		`<<1, -2, TRUE>>`:  []any{int64(1), int64(-2), true},
		`"a \"quoted\" b"`: `a "quoted" b`,
		`r3`:               "r3",
	}
	for text, want := range cases {
		got, err := ParseValue(text)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("ParseValue(%s) = %#v, %v; want %#v", text, got, err, want)
		}
	}
	for _, bad := range []string{`{1, 2`, `[a |-> ]`, `(r1 :> 1 @@)`, `"open`} {
		if _, err := ParseValue(bad); err == nil {
			t.Errorf("ParseValue(%s) succeeded; want an error", bad)
		}
	}
}

func TestConfigRender(t *testing.T) {
	cfg := Config{Specification: "Spec", Constants: map[string]string{"RM": "{r1, r2}", "N": "3"}, Invariants: []string{"TypeOK", "Safe"}, Properties: []string{"Answered"}}
	want := "SPECIFICATION Spec\nCONSTANT N = 3\nCONSTANT RM = {r1, r2}\nINVARIANT TypeOK\nINVARIANT Safe\nPROPERTY Answered\n"
	if got := cfg.render(); got != want {
		t.Errorf("render =\n%s\nwant\n%s", got, want)
	}
}

// The property fixtures are real runs of the pinned TLC on testdata/Live.tla,
// where a watcher answers the commands people ask, under weak fairness.

// property-passed.txt checks EventuallyAnswered and the action property
// NeverUnanswered under FairSpec. Both hold.
func TestParsePropertiesPassed(t *testing.T) {
	r := Parse(fixture(t, "property-passed.txt"), 0)
	if r.Outcome != Passed || r.DistinctStates != 9 || r.Depth != 5 {
		t.Fatalf("Outcome = %s (%s), %d states, depth %d; want passed, 9, 5", r.Outcome, r.Message, r.DistinctStates, r.Depth)
	}
}

// property-loop.txt adds Forget, which drops a pending command. The
// counterexample asks and forgets command 1 forever: from state 6 it
// returns to state 5.
func TestParsePropertyLoop(t *testing.T) {
	r := Parse(fixture(t, "property-loop.txt"), 13)
	if r.Outcome != PropertyViolated || r.Property != "" || r.Loop != 5 || r.Stutters {
		t.Fatalf("Outcome = %s, Property = %q, Loop = %d, Stutters = %v; want property violated, unnamed, loop 5", r.Outcome, r.Property, r.Loop, r.Stutters)
	}
	if len(r.Trace) != 6 || r.Trace[2].Action != "Forget" {
		t.Fatalf("trace = %+v; want 6 states, the third after Forget", r.Trace)
	}
	f, err := r.TraceFile("Live", "property EventuallyAnswered")
	if err != nil || f.Loop != 5 || f.Stutters || len(f.States) != 6 {
		t.Fatalf("TraceFile = %+v, %v", f, err)
	}
}

// property-stutter.txt checks EventuallyAnswered with no fairness: the
// watcher answers nothing, and the behavior stops with a command pending.
func TestParsePropertyStutter(t *testing.T) {
	r := Parse(fixture(t, "property-stutter.txt"), 13)
	if r.Outcome != PropertyViolated || !r.Stutters || r.Loop != 0 {
		t.Fatalf("Outcome = %s, Loop = %d, Stutters = %v; want property violated by stuttering", r.Outcome, r.Loop, r.Stutters)
	}
	if last := r.Trace[len(r.Trace)-1]; last.Action == "" {
		t.Fatalf("last state = %+v", last)
	}
}

// action-property.txt adds an action outside Next and checks that its every
// step is a Next step, as the gate checks a fair action. TLC names it.
func TestParseActionProperty(t *testing.T) {
	r := Parse(fixture(t, "action-property.txt"), 13)
	if r.Outcome != PropertyViolated || r.Property != "OtherIsNext" || len(r.Trace) != 2 {
		t.Fatalf("Outcome = %s, Property = %q, trace %d states; want OtherIsNext violated in 2", r.Outcome, r.Property, len(r.Trace))
	}
}

// What a model prints with PrintT comes back beside TLC's own messages.
func TestParseKeepsWhatTheModelPrinted(t *testing.T) {
	out := "@!@!@STARTMSG 2189:0 @!@!@\nComputing initial states...\n@!@!@ENDMSG 2189 @!@!@\n" +
		"<<\"invariant-untried\", {<<\"MakeCall\", a1>>}>>\n" +
		"@!@!@STARTMSG 2107:1 @!@!@\nInvariant Invariant_EveryStepTried is violated by the initial state:\n" +
		"/\\ clock = 3\n/\\ invariant_i = 2\n\n@!@!@ENDMSG 2107 @!@!@\n"
	r := Parse(out, 12)
	if len(r.Trace) != 1 || len(r.Trace[0].Vars) != 2 || r.Trace[0].Vars[1] != (Var{Name: "invariant_i", Value: "2"}) {
		t.Errorf("trace = %+v; want the initial state the message carries", r.Trace)
	}
	if len(r.Printed) != 1 || r.Printed[0] != `<<"invariant-untried", {<<"MakeCall", a1>>}>>` {
		t.Errorf("Printed = %q", r.Printed)
	}
	if r.Outcome != Violated || r.Invariant != "Invariant_EveryStepTried" {
		t.Errorf("outcome %s, invariant %q", r.Outcome, r.Invariant)
	}
}
