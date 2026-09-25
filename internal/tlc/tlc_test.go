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

// passed.out is a real TLC 2.19 run of TwoPhase.tla with RM = {r1, r2, r3}.
func TestParsePassed(t *testing.T) {
	r := Parse(fixture(t, "passed.out"), 0)
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

// violated.out is the same spec with the coordinator committing early.
func TestParseViolated(t *testing.T) {
	r := Parse(fixture(t, "violated.out"), 12)
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
	cfg := Config{Specification: "Spec", Constants: map[string]string{"RM": "{r1, r2}", "N": "3"}, Invariants: []string{"TypeOK", "Safe"}}
	want := "SPECIFICATION Spec\nCONSTANT N = 3\nCONSTANT RM = {r1, r2}\nINVARIANT TypeOK\nINVARIANT Safe\n"
	if got := cfg.render(); got != want {
		t.Errorf("render =\n%s\nwant\n%s", got, want)
	}
}
