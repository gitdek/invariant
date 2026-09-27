package receipt

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/verify"
)

func TestLivenessRows(t *testing.T) {
	r := &verify.Report{
		Project:   "answering",
		ModelOnly: true,
		Bounds:    map[string]string{"Commands": "{c1, c2}"},
		Design:    verify.Design{Passed: true, Outcome: "passed", DistinctStates: 9, StatesGenerated: 14, Depth: 5},
		Properties: []verify.Property{
			{Name: "EventuallyAnswered", Holds: true},
			{Name: "AnswersStay", Steps: 3, Loop: 2, Message: "TLC found a behavior that breaks it"},
		},
		Fairness: []verify.Fair{{Name: "WatcherIsFair", Action: "Watch", InNext: true}},
		Bugs:     []verify.Bug{{Name: "Drop", Label: "drop", Caught: true, Violated: "EventuallyAnswered", Steps: 5}},
	}
	md := Markdown(r)
	for _, want := range []string{
		"| Liveness · TLC | ❌ 1 of 2 properties hold | `EventuallyAnswered` holds, `AnswersStay` broken: loops back to state 2 after 3 steps, under `WatcherIsFair` |",
		"| Fairness | ✅ 1 of 1 on steps the model takes | `WatcherIsFair`: weak, on `Watch` |",
		"- Property `AnswersStay`: TLC found a behavior that breaks it",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("receipt lacks %q:\n%s", want, md)
		}
	}

	// A project with no properties shows neither row, as before.
	r.Properties, r.Fairness = nil, nil
	if md := Markdown(r); strings.Contains(md, "Liveness") || strings.Contains(md, "Fairness") {
		t.Errorf("a receipt without properties shows liveness rows:\n%s", md)
	}
}
