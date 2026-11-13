package tlc

import (
	"reflect"
	"strings"
	"testing"
)

// When a step fails the path's check, the refusal says what the check found
// there (#857). The check prints each step's readings before it judges
// them, and these read that row back: none, one, or each of several, with
// its arguments, and nothing when the check never printed the step.
func TestAFailedStepsReadingsAreReadBack857(t *testing.T) {
	printed := []string{`<<"invariant-replay-v1", 0, {<<"Put", <<p1>>, FALSE>>}>>` + "\n" +
		`<<"invariant-replay-v1", 1, {<<"Break", <<p1, 3>>, TRUE>>, <<"Put", <<p2>>, FALSE>>}>>` + "\n" +
		`<<"invariant-replay-v1", 2, {}>>` + "\n" + `<<"invariant-replay-v1", 3, {<<"Done", <<>>, FALSE>>}>>`}
	for index, want := range map[int][]string{0: {"Put(p1)"}, 1: {"Break(p1, 3)", "Put(p2)"}, 2: nil, 3: {"Done()"}} {
		got, ok := replayReadings(printed, index)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("step %d reads as %v, printed %v; want %v", index, got, ok, want)
		}
	}
	if got, ok := replayReadings(printed, 4); ok || got != nil {
		t.Errorf("a step the check never printed reads as %v, printed %v", got, ok)
	}
	if got, ok := replayReadings([]string{`<<"invariant-replay-v1", 0, {<<"Put", "p1">>}>>`}, 0); ok || got != nil {
		t.Errorf("a malformed row reads as %v, printed %v", got, ok)
	}
}

// What TLC said goes into a refusal on one line, without the frames its
// tool mode puts around each message.
func TestAFailedChecksWordsAreOneLine857(t *testing.T) {
	e := &replayCheckError{module: "Invariant_ReplayPath", res: Result{Outcome: Failed,
		Message: "@!@!@STARTMSG 2280:0 @!@!@\nIn evaluation, the identifier who is either undefined\nor not an operator.\n@!@!@ENDMSG 2280 @!@!@\n"}}
	const want = "replay-check-failed: Invariant_ReplayPath: error In evaluation, the identifier who is either undefined or not an operator."
	if got := e.Error(); got != want || strings.ContainsAny(got, "\n@") {
		t.Errorf("the refusal says %q; want %q", got, want)
	}
	// A violation names its step from the state TLC stopped in.
	e = &replayCheckError{module: "Invariant_ReplayPath", res: Result{Outcome: Violated, Trace: []State{{Index: 1, Vars: []Var{{Name: "n", Value: "0"}, {Name: "invariant_replay_i", Value: "3"}}}}}}
	if got := e.step(); got != 3 {
		t.Errorf("the failing step is read as %d; want 3", got)
	}
	if got := (&replayCheckError{res: Result{Outcome: Failed}}).step(); got != 0 {
		t.Errorf("an error with no state names step %d", got)
	}
}

// The check's configuration writes the ratified bounds first and the model
// values it declares itself after them: TLC orders model values as its
// configuration introduces them, and a model's CHOOSE follows that order.
func TestTheChecksConfigurationWritesTheBoundsFirst857(t *testing.T) {
	header, bounds, values, err := replayHeader("Cap", map[string]string{"People": "{B, A}", "Limit": "2"}, []ReplayState{{"who": {Kind: "set", Elements: []ReplayValue{{Kind: "model", Atom: "B"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bounds, map[string]string{"People": "{B, A}", "Limit": "2"}) || !reflect.DeepEqual(values, map[string]string{"A": "A", "B": "B"}) || !strings.Contains(header, "CONSTANTS A, B\n") {
		t.Fatalf("bounds %v, values %v, header %q", bounds, values, header)
	}
	got := Config{Specification: "S", Constants: bounds, Later: values, Invariants: []string{"I"}}.render()
	const want = "SPECIFICATION S\nCONSTANT Limit = 2\nCONSTANT People = {B, A}\nCONSTANT A = A\nCONSTANT B = B\nINVARIANT I\n"
	if got != want {
		t.Errorf("the configuration is\n%s\nwant\n%s", got, want)
	}
}
