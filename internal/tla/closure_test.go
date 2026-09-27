package tla

import (
	"reflect"
	"strings"
	"testing"
)

const spec = `---- MODULE Bank ----
CONSTANT Accounts
VARIABLES
    balance,   \* balance[a] is a's balance
    log

vars == <<balance, log>>

Amounts == 0..10

\* Pinned: every balance stays in range.
TypeOK == balance \in [Accounts -> Amounts]

Solvent == \A a \in Accounts : balance[a] >= 0

\* A known bug.
Overdraw == balance' = [a \in Accounts |-> -1] /\ UNCHANGED log

Init == balance = [a \in Accounts |-> 0] /\ log = <<>>

Deposit(a) == balance' = [balance EXCEPT ![a] = @ + 1] /\ UNCHANGED log

Next == \E a \in Accounts : Deposit(a)

Spec == Init /\ [][Next]_vars

FairSpec == Spec /\ WF_vars(Next)
====
`

var model = map[string]bool{"Init": true, "Next": true}

func TestClosure(t *testing.T) {
	cases := map[string][]string{
		"TypeOK":   {"Amounts", "TypeOK"},
		"Solvent":  {"Solvent"},
		"Spec":     {"Spec", "vars"},
		"FairSpec": {"FairSpec", "Spec", "vars"},
	}
	for name, want := range cases {
		got, err := Closure(spec, name, model)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("Closure(%s) = %v, %v; want %v", name, got, err, want)
		}
	}
}

func TestPinHashCoversDependencies(t *testing.T) {
	before, _ := PinHash(spec, "TypeOK", model)
	widened := strings.Replace(spec, "Amounts == 0..10", "Amounts == -5..10", 1)
	after, _ := PinHash(widened, "TypeOK", model)
	if before == after {
		t.Error("changing Amounts, which TypeOK depends on, didn't change TypeOK's pin")
	}
	solo, _ := PinHash(spec, "Solvent", model)
	def, _ := Definition(spec, "Solvent")
	if solo != Hash(def) {
		t.Error("a statement with no dependencies should pin exactly like Hash")
	}
	// The model is the factory's: changing it moves no pin.
	edited := strings.Replace(spec, "@ + 1", "@ + 2", 1)
	for _, name := range []string{"TypeOK", "Solvent", "Spec", "Overdraw"} {
		a, _ := PinHash(spec, name, model)
		b, _ := PinHash(edited, name, model)
		if a != b {
			t.Errorf("editing the model changed %s's pin", name)
		}
	}
}

func TestVariables(t *testing.T) {
	if got := Variables(spec); !reflect.DeepEqual(got, []string{"balance", "log"}) {
		t.Errorf("Variables = %v; want [balance log]", got)
	}
}

func TestSkeleton(t *testing.T) {
	keep := map[string]bool{"vars": true, "Amounts": true, "TypeOK": true, "Solvent": true, "Overdraw": true, "Spec": true}
	got, err := Skeleton(spec, keep, []string{"Spec"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"---- MODULE Bank ----", "CONSTANT Accounts", "    log", "\\* Pinned: every balance stays in range.",
		"TypeOK ==", "Overdraw ==", ModelMarker, "Spec == Init /\\ [][Next]_vars"} {
		if !strings.Contains(got, want) {
			t.Errorf("skeleton lacks %q:\n%s", want, got)
		}
	}
	for _, gone := range []string{"Init ==", "Deposit(a)", "Next =="} {
		if strings.Contains(got, gone) {
			t.Errorf("skeleton still has the model's %q", gone)
		}
	}
	if strings.Index(got, ModelMarker) > strings.Index(got, "Spec ==") {
		t.Error("Spec must come after the model's place, since it refers to Init and Next")
	}
	for name := range keep {
		a, _ := PinHash(spec, name, model)
		b, err := PinHash(got, name, model)
		if err != nil || a != b {
			t.Errorf("the skeleton changed %s's pin", name)
		}
	}
}

// A fairness statement may refer to the model, as WF_vars(Next) does, so it
// goes after the model's place too, after the spec.
func TestSkeletonWithFairness(t *testing.T) {
	src := `---- MODULE Tick ----
VARIABLE n
vars == <<n>>
TypeOK == n \in 0..3
Init == n = 0
Next == n < 3 /\ n' = n + 1
\* The clock, whenever it can tick, eventually does.
Ticks == WF_vars(Next)
Spec == Init /\ [][Next]_vars
====
`
	keep := map[string]bool{"vars": true, "TypeOK": true, "Ticks": true, "Spec": true}
	got, err := Skeleton(src, keep, []string{"Spec", "Ticks"})
	if err != nil {
		t.Fatal(err)
	}
	marker, spec, ticks := strings.Index(got, ModelMarker), strings.Index(got, "Spec =="), strings.Index(got, "\\* The clock")
	if marker < 0 || !(marker < spec && spec < ticks) || !strings.Contains(got, "Ticks == WF_vars(Next)") {
		t.Fatalf("want the marker, then Spec, then Ticks with its comment:\n%s", got)
	}
	for name := range keep {
		a, _ := PinHash(src, name, model)
		b, err := PinHash(got, name, model)
		if err != nil || a != b {
			t.Errorf("the skeleton changed %s's pin", name)
		}
	}
}
