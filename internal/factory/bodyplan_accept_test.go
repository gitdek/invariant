package factory

import (
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
)

// An issue a writer opens with /invariant plan is a PRD, as one a writer
// comments it on is (#201): the factory takes it as its own, drafts its
// plan of issues, and it's still a PRD once the factory has posted, when a
// person answers its forks. The same line in the issue of someone who
// can't write to the repository does nothing.
func TestAnIssueOpenedWithAPlanIsAPRD(t *testing.T) {
	r := newRig(t)
	planner := &prdPlanner{t: t, plans: []*formalize.IssuePlan{graphPlan()}, forks: []formalize.Fork{{ID: "F1", Question: "Does the graph show superseded decisions?",
		Options: []formalize.Option{{ID: "A", Says: "Yes, greyed out."}, {ID: "B", Says: "No, only the ones in force."}}}}}
	r.f.Formalizer = planner
	body := prdBody + "\n\n/invariant plan\n"
	if !Takes(github.Issue{Body: body}) {
		t.Error("the factory's issues leave out one opened with /invariant plan")
	}
	r.gh.open(41, "mallory", "Draw the decision graph", body)
	r.gh.open(40, "gitdek", "Draw the decision graph", body)
	r.poll()
	if len(r.gh.posts(41)) != 0 {
		t.Fatal("the factory took /invariant plan from the issue of someone who can't write to the repository")
	}
	r.expect(40, KindForks, LabelAsking)

	r.gh.say(40, "gitdek", "/invariant choose F1 B")
	r.poll()
	proposed := r.expect(40, KindProposal, LabelProposal)
	if p := proposed.Marker.Proposal; p == nil || p.IssuePlan == nil || p.Hash != graphPlan().Hash() {
		t.Fatalf("answering the fork should draft the plan of issues, as the issue is still a PRD: %+v", p)
	}
	if len(planner.requests) != 2 {
		t.Fatalf("%d drafts; want the forks, then the plan", len(planner.requests))
	}
}
