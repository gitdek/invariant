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
