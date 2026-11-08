package factory

import (
	"context"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/setup"
)

// questions742 asks one question in place of the draft its at-th request
// would have made, and drafts as the factory's formalizer does otherwise.
type questions742 struct {
	Formalizer
	at, n int
}

func (q *questions742) Formalize(ctx context.Context, req formalize.Request, out string) (*formalize.Result, error) {
	q.n++
	if q.n == q.at {
		forks := []formalize.Fork{{ID: "F1", Question: "Keep the behavior as it is?", Options: []formalize.Option{{ID: "A", Says: "Keep it."}, {ID: "B", Says: "Change it."}}}}
		return &formalize.Result{Proposal: &formalize.Proposal{Draft: formalize.Draft{Forks: forks}}}, nil
	}
	return q.Formalizer.Formalize(ctx, req, out)
}

// A revision of a configured repository's native plan keeps the authority
// that plan captured, through a question it asks, the question's answer and
// a restart. A question carries no plan, so the answer once captured the
// current base and silently adopted a gate pin changed while the question
// waited (#762's review). It now finds the issue's latest native plan and
// holds the changed authority for a person, drafting nothing.
func TestConfiguredQuestionsKeepCapturedAuthority742(t *testing.T) {
	x := new742(t, "pytest", false, t.TempDir())
	x.start()
	q := &questions742{Formalizer: x.r.f.Formalizer, at: 1}
	x.r.f.Formalizer = q
	x.r.gh.say(1, "gitdek", "Please ask before changing anything.\n\n/invariant revise")
	x.r.poll()
	x.r.expect(1, KindForks, LabelAsking)

	x.changeBranch("main", func(root string) {
		workflow, err := setup.Workflow(strings.Repeat("b", 40))
		if err != nil {
			t.Fatal(err)
		}
		write742(t, root, setup.WorkflowPath, workflow)
	})
	x.restart()
	x.r.gh.say(1, "gitdek", "/invariant choose F1 A")
	_ = x.r.f.Poll(t.Context())
	last := x.r.gh.last(1)
	if last.Marker.Kind == KindProposal {
		t.Fatalf("the answer was drafted against the changed gate authority:\n%s", last.Comment.Body)
	}
	if last.Marker.Kind != KindStuck || !strings.Contains(strings.ToLower(last.Comment.Body), "gate") {
		t.Fatalf("the changed gate authority wasn't held for a person: %s\n%s", last.Marker.Kind, last.Comment.Body)
	}
	if q.n != 1 {
		t.Fatalf("the factory drafted %d times after the first proposal; want the revision's question alone", q.n)
	}
}

// upgradeGate742 commits the gate an owner upgrades to, pinning another
// Invariant commit, on the base.
func upgradeGate742(t *testing.T, x *fixture742) {
	t.Helper()
	x.changeBranch("main", func(root string) {
		workflow, err := setup.Workflow(strings.Repeat("b", 40))
		if err != nil {
			t.Fatal(err)
		}
		write742(t, root, setup.WorkflowPath, workflow)
	})
}

// A writer's /invariant solve drafts afresh and captures the base as it is
// now, so an owner who upgraded the gate, as the setup guidance asks, can
// draft the issue again, though a revision of the old plan is held. A
// question that fresh draft asks is answered against the upgraded base too,
// not the plan from before it (#762's review).
func TestConfiguredSolveCapturesTheBaseAgain742(t *testing.T) {
	for _, asks := range []bool{false, true} {
		t.Run(map[bool]string{false: "proposal", true: "question"}[asks], func(t *testing.T) {
			x := new742(t, "pytest", false, t.TempDir())
			old := x.start().Marker.Proposal.Plan.Execution.Base
			upgradeGate742(t, x)
			x.r.gh.say(1, "gitdek", "/invariant revise")
			_ = x.r.f.Poll(t.Context())
			if last := x.r.gh.last(1); last.Marker.Kind != KindStuck {
				t.Fatalf("a revision of the old plan wasn't held for the changed gate: %s\n%s", last.Marker.Kind, last.Comment.Body)
			}
			if asks {
				x.r.f.Formalizer = &questions742{Formalizer: x.r.f.Formalizer, at: 1}
			}
			x.r.gh.say(1, "gitdek", "/invariant solve")
			_ = x.r.f.Poll(t.Context())
			if asks {
				x.r.expect(1, KindForks, LabelAsking)
				x.r.gh.say(1, "gitdek", "/invariant choose F1 A")
				_ = x.r.f.Poll(t.Context())
			}
			last := x.r.gh.last(1)
			if last.Marker.Kind != KindProposal || last.Marker.Proposal == nil || last.Marker.Proposal.Plan == nil || last.Marker.Proposal.Plan.Execution == nil {
				t.Fatalf("a fresh solve was held instead of drafted: %s\n%s", last.Marker.Kind, last.Comment.Body)
			}
			if last.Marker.Proposal.Plan.Execution.Base == old {
				t.Fatal("a fresh solve kept the base captured before the gate upgrade")
			}
		})
	}
}
