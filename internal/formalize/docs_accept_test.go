package formalize

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/plumbing"
)

// planDocsPhrases is what a plumbing issue's planning prompt tells its agent
// about SPEC.md and the README (#147, D-0122): they describe Invariant as it
// is, so a plan whose change makes either one wrong names that file and says
// in its summary what changes there, and a SPEC bullet cites the decisions it
// rests on or the plan itself, which is its own decision record once it's
// ratified (#809). A decision the plan doesn't state is one the plan says
// whoever merges it records, since a plumbing build can't write decisions/.
var planDocsPhrases = []string{
	"SPEC.md and the README describe Invariant as it is",
	"a change that makes either one wrong fixes it in the same change",
	"name that file among the plan's files",
	"say in the summary what changes there",
	"The README is the front page, at most 400 lines",
	"detail belongs in the page under docs/ it links",
	"A ratified plan is its own decision record (#809)",
	"A new or changed SPEC bullet cites what it rests on, as `decisions check` requires",
	"the decisions it rests on, or this plan, by a link to its file, `[#147's ratified plan](.invariant/plans/issue-147.json)`",
	"A plumbing build can't write decisions/",
	"the change needs a decision this plan doesn't state, or makes a one-way door",
	"whoever merges it records the decision",
}

// #134, #135 and #139 each changed what SPEC.md says, and none of their
// plans named it, so SPEC and the README called all three next until #146
// caught them up by hand. The planning prompt says how a plan keeps them
// right, whether it drafts a plan or revises one, and still gives the agent
// its checks.
func TestPlanningTellsTheAgentToKeepTheSpecAndReadmeRight(t *testing.T) {
	req := Request{Repo: "gitdek/invariant", Issue: 147, Title: "Have plumbing plans keep SPEC.md and the README up to date", Author: "gitdek"}
	revise := req
	revise.Previous = &Proposal{Plan: &plumbing.Plan{
		Name:    "the plan prompt",
		Summary: "Change the plan prompt.",
		Files:   []string{"internal/formalize/plan.go"},
		Tests:   []plumbing.Test{{Name: "TestPlanPrompt", File: "internal/formalize/plan_accept_test.go", Says: "The prompt changed."}},
	}}
	for name, r := range map[string]Request{"draft": req, "revision": revise} {
		got := PlanPrompt(r, 4)
		for _, want := range append([]string{"You have 4 checks"}, planDocsPhrases...) {
			if !strings.Contains(got, want) {
				t.Errorf("the planning prompt for a %s lacks %q", name, want)
			}
		}
	}
}
