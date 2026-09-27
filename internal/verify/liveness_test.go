package verify

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/tlc"
)

func TestFairnessOf(t *testing.T) {
	good := map[string]condition{
		`Fair == WF_vars(Watch)`:                          {Action: "Watch"},
		`Fair == SF_vars(Watch)`:                          {Strong: true, Action: "Watch"},
		"Fair ==\n    (WF_vars(Watch))":                   {Action: "Watch"},
		`Fair == \A p \in Procs : WF_vars(Step(p))`:       {Bound: "p", Set: "Procs", Action: "Step(p)"},
		`Fair == WF_vars(\E c \in pending : Answer(c))`:   {Action: `\E c \in pending : Answer(c)`},
		`Fair == \A w \in {w1, w2} : SF_vars(Poll(w, 1))`: {Strong: true, Bound: "w", Set: "{w1, w2}", Action: "Poll(w, 1)"},
	}
	for def, want := range good {
		got, err := fairnessOf("---- MODULE M ----\n"+def+"\n====\n", "Fair")
		if err != nil || got != want {
			t.Errorf("fairnessOf(%s) = %+v, %v; want %+v", def, got, err, want)
		}
	}
	for _, def := range []string{
		`Fair == WF_vars(A) /\ WF_vars(B)`,
		`Fair == WF_<<x, y>>(A)`,
		`Fair(p) == WF_vars(Step(p))`,
		`Fair == []<>(x = 1)`,
		`Fair == WF_vars(A)) /\ (WF_vars(B)`,
	} {
		if got, err := fairnessOf("---- MODULE M ----\n"+def+"\n====\n", "Fair"); err == nil {
			t.Errorf("fairnessOf(%s) = %+v; want an error", def, got)
		}
	}
}

func TestFairnessWrappers(t *testing.T) {
	c := condition{Bound: "p", Set: "Procs", Action: "Step(p)"}
	if got := c.steps(); got != `(\E p \in Procs : Step(p))` {
		t.Errorf("steps = %s", got)
	}
	if got := c.inNext(); got != `[][\A p \in Procs : ((Step(p)) => Next)]_vars` {
		t.Errorf("inNext = %s", got)
	}
	if got := fairSpec("S", "Spec", []string{"F1", "F2"}); got != `S == Spec /\ F1 /\ F2` {
		t.Errorf("fairSpec = %s", got)
	}
	if got := fairSpec("S", "Spec", nil); !strings.HasSuffix(got, "== Spec") {
		t.Errorf("fairSpec with no fairness = %s", got)
	}
}

// An agent repairing a failed gate run is told what failed and shown the
// behavior, for properties and for the check one size larger alike.
func TestFeedbackExplainsPropertiesAndSizes(t *testing.T) {
	r := &Report{
		ModelOnly: true,
		Design:    Design{Passed: true, Outcome: "passed"},
		Properties: []Property{{Name: "EventuallyAnswered", Says: "Every command is answered", Stutters: true, Steps: 1,
			Behavior: []tlc.State{{Index: 1, Action: "Initial predicate"}, {Index: 2, Action: "Ask"}}}},
		Fairness: []Fair{{Name: "WatcherIsFair", Says: "The watcher answers", Message: "some step of Watch isn't a Next step"}},
		Larger:   &Larger{Required: true, Message: "the code reaches 87 states in 9 levels; the model reaches 111 in 9"},
	}
	fb := Feedback(r)
	for _, want := range []string{"What to fix: Property EventuallyAnswered; Fairness WatcherIsFair; One size larger.", "stays there forever", "2. Ask", "Something in the code stops at the bounds"} {
		if !strings.Contains(fb, want) {
			t.Errorf("feedback lacks %q:\n%s", want, fb)
		}
	}
	if got := strings.Join(Failed(r), ", "); got != "property EventuallyAnswered, fairness WatcherIsFair, one size larger" {
		t.Errorf("Failed = %s", got)
	}
}
