package verify

import (
	"context"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/tlc"
)

func TestBugReplayFeedbackPreservesModelOnlyAndIndependentEvidence(t *testing.T) {
	r := Report{Passed: true, ModelOnly: true, Design: Design{Passed: true, Outcome: "passed"},
		Bugs: []Bug{
			{Name: "Old", Caught: true},
			{Name: "Possible", Caught: true, CodeBehavior: "hypothetical"},
			{Name: "Actual", Caught: true, CodeBehavior: "current", Replay: &BugReplayEvidence{Required: true, Status: "pending"}},
		}}
	before := Fingerprint(r)
	feedback := Feedback(&r)
	for _, want := range []string{"The model checks out", "Old: caught in the model; not replayed: not classified", "Possible: caught in the model; not replayed: hypothetical", "Actual: caught in the model; current; pending"} {
		if !strings.Contains(feedback, want) {
			t.Errorf("feedback omits %q: %s", want, feedback)
		}
	}
	if len(Failed(&r)) != 0 || Fingerprint(r) != before {
		t.Fatal("pending replay changed model-only success or its evidence")
	}
	r.ModelOnly, r.Build.Passed = false, true
	r.Code = &Code{Verifier: "Gobra", Passed: true, Functions: []string{"KeepSafe"}}
	r.Passed = activateBugReplays(&r)
	feedback = Feedback(&r)
	if r.Passed || !strings.Contains(feedback, "replay-unavailable") || !strings.Contains(feedback, "caught in the model") || r.CodeAssurance() != "proved" {
		t.Fatalf("replay failure hides independent evidence: %s", feedback)
	}
}

func TestBugReplayRefusesLossyObservedStrings(t *testing.T) {
	state := tlc.ReplayState{"text": {Kind: "string", Atom: string([]byte{0xff})}}
	evidence, err := CheckBugReplay(context.Background(), tlc.Runner{}, "", BugReplayRequest{}, BugReplayResult{Initial: state}, BugReplayObservation{Initial: state})
	if err == nil || evidence.Passed || !strings.Contains(err.Error(), "malformed replay string") {
		t.Fatalf("lossy observation reached replay checking: %+v %v", evidence, err)
	}
}
