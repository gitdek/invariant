package factory

import (
	"context"
	"fmt"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
)

type uncheckedStepPlanner struct{ t *testing.T }

func (p uncheckedStepPlanner) Formalize(_ context.Context, req formalize.Request, _ string) (*formalize.Result, error) {
	if req.ParentPlan != 10 || req.PRD != "" {
		p.t.Errorf("step lost its parent or became a PRD: parent=%d, PRD=%q", req.ParentPlan, req.PRD)
	}
	plan := nestedIssuePlan("an unchecked split", "Store the data", "Display the data")
	return &formalize.Result{Proposal: &formalize.Proposal{
		Draft: formalize.Draft{Name: plan.Name, Forks: []formalize.Fork{{
			ID: "F1", Question: "Which part comes first?",
			Options: []formalize.Option{{ID: "A", Says: "Storage."}, {ID: "B", Says: "Display."}},
		}}},
		IssuePlan: plan, Hash: plan.Hash(),
	}}, nil
}

func TestStepRefusesUncheckedIssuePlanBeforePostingForks(t *testing.T) {
	for _, modeled := range []bool{false, true} {
		t.Run(fmt.Sprint(modeled), func(t *testing.T) {
			r, hub, parent, child := stepRig560(t, modeled)
			r.f.Formalizer = uncheckedStepPlanner{t: t}
			r.poll()
			r.expect(child, KindStuck, LabelHumanReview)
			splitRefused560(t, r, hub, child, parent.Issue, 1)
		})
	}
}
