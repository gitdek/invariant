package formalize

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/plumbing"
)

// The prompt is the interface to the planner. Pin the two instructions
// requested by #518, allowing different whitespace and surrounding prose.
func TestPlanningNamesExistingTestsForChangedBehavior(t *testing.T) {
	checkPlan518Instructions(t,
		"Before naming files, look for the existing tests and fakes that check the behavior the change alters, including command lines, flags and output a fake validates.",
		"Name every existing test file the change needs in the plan's files.",
	)
}

func TestPlanningKeepsAcceptanceConsistentWithPinnedBehavior(t *testing.T) {
	checkPlan518Instructions(t,
		"An acceptance test's expectations about behavior the plan keeps must agree with the existing tests that pin it.",
		"When the plan intentionally changes pinned behavior, say so in its summary and name those existing tests in its files.",
	)
}

func checkPlan518Instructions(t *testing.T, instructions ...string) {
	t.Helper()
	for _, tc := range []struct {
		name     string
		previous *Proposal
	}{
		{name: "draft"},
		{name: "plan revision", previous: &Proposal{Plan: &plumbing.Plan{Name: "Earlier plan"}}},
		{name: "issue plan revision", previous: &Proposal{IssuePlan: &IssuePlan{Name: "Earlier issue plan"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := Request{
				Repo:     "gitdek/invariant",
				Issue:    518,
				Title:    "Have the planner check its plan against existing tests",
				Author:   "gitdek",
				Previous: tc.previous,
			}
			got := strings.Join(strings.Fields(PlanPrompt(req, 4)), " ")
			for _, want := range instructions {
				if !strings.Contains(got, want) {
					t.Errorf("planning prompt lacks instruction %q", want)
				}
			}
		})
	}
}
