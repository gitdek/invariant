package tla

import (
	"strings"
	"testing"
)

// A replay names each step's operation and its arguments (#857). An
// operation whose own body chooses part of its next state, where no
// argument names the choice, is marked, so a path through it is refused
// where it's extracted. A review found such a choice was refused only when
// it was the first thing in the body.
func TestReplayActionsMarkAChoiceABodyHides857(t *testing.T) {
	m, err := Read(`---- MODULE Hidden ----
EXTENDS Naturals
CONSTANT People
VARIABLE n, who
vars == <<n, who>>
Init == n = 0 /\ who = {}
Put(p) == n < 2 /\ n' = n + 1 /\ who' = who \cup {p}
Plain == n = 2 /\ n' = 0 /\ who' = {}
Nested == n = 1 /\ \E p \in People : n' = n + 1 /\ who' = who \cup {p}
Picked == n = 0 /\ n' = 1 /\ who' \in {{p} : p \in People}
Guarded == (\E p \in People : p \in who) /\ n' = 0 /\ UNCHANGED who
Reads == (\E p \in People : p \in who) /\ UNCHANGED vars
Next == (\E p \in People : Put(p)) \/ Plain \/ Nested \/ Picked \/ Guarded \/ Reads
Spec == Init /\ [][Next]_vars
Safe == n <= 2
Break(p) == n = 2 /\ n' = 3 /\ who' = who \cup {p}
Bad == \E p \in People : Break(p)
Worse == n = 2 /\ \E p \in People : n' = 3 /\ who' = {p}
Inline == \E p \in People : n' = 3 /\ who' = {p}
====
`, nil)
	if err != nil {
		t.Fatal(err)
	}
	normal, buggy, err := m.ReplayActions("Bad")
	if err != nil {
		t.Fatal(err)
	}
	hidden := map[string]bool{}
	for _, s := range append(normal, buggy...) {
		hidden[s.Name] = s.Hidden
	}
	// Guarded only reads under its \E, and still counts: the rule errs
	// toward refusing. Reads changes nothing, so there is nothing to choose.
	want := map[string]bool{"Put": false, "Plain": false, "Nested": true, "Picked": true, "Guarded": true, "Reads": false, "Break": false}
	for name, w := range want {
		if got, ok := hidden[name]; !ok || got != w {
			t.Errorf("%s: hides a choice %v (found %v), want %v", name, got, ok, w)
		}
	}
	// A bug that hides its choice is marked, and one that is an \E over a
	// body with no operation to name is refused outright, as before.
	_, worse, err := m.ReplayActions("Worse")
	if err != nil || len(worse) != 1 || !worse[0].Hidden {
		t.Errorf("a bug with a choice inside its body: %+v, %v", worse, err)
	}
	if _, _, err := m.ReplayActions("Inline"); err == nil || !strings.Contains(err.Error(), "replay-unsupported-action: Inline must name its parameterized operations") {
		t.Errorf("a bug that is a bare choice over a body: %v", err)
	}
}
