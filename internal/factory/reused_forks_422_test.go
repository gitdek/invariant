package factory

import (
	"reflect"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
)

// IDs belong to a draft. A new question that reuses F1 must keep the
// earlier question's answer, including when a later comment revises F1.
func TestChoicesKeepDistinctQuestionsWithReusedIDs422(t *testing.T) {
	for _, sameQuestion := range []bool{false, true} {
		name := "different question"
		if sameQuestion {
			name = "same question asked again"
		}
		t.Run(name, func(t *testing.T) {
			r, forks, _ := partialChoices422Rig(t)
			next := formalize.Fork{ID: "F1", Question: "What happens to buffered messages on close?", Options: []formalize.Option{
				{ID: "A", Says: "Flush before closing."}, {ID: "B", Says: "Discard on close."},
			}}
			if sameQuestion {
				next = forks[0]
			}
			r.form.forks = []formalize.Fork{next, forks[1]}
			first := r.gh.say(1, "gitdek", "/invariant choose F1 A\n/invariant revise")
			r.poll()
			asked := r.expect(1, KindForks, LabelAsking)
			if !reflect.DeepEqual(asked.Marker.Forks, r.form.forks) {
				t.Fatalf("revised draft's questions = %+v; want %+v", asked.Marker.Forks, r.form.forks)
			}
			earlier := partialChoices422Answer(forks[0], "A", first)
			if !reflect.DeepEqual(asked.Marker.Answers, []formalize.Answer{earlier}) {
				t.Fatalf("revised draft lost the earlier choice: %+v", asked.Marker.Answers)
			}
			second := r.gh.say(1, "gitdek", "/invariant choose F1 B")
			r.poll()
			partial := r.expect(1, KindForks, LabelAsking)
			want := []formalize.Answer{partialChoices422Answer(next, "B", second)}
			if !sameQuestion {
				want = append([]formalize.Answer{earlier}, want...)
			}
			if !reflect.DeepEqual(partial.Marker.Answers, want) {
				t.Fatalf("partial choices = %+v; want %+v", partial.Marker.Answers, want)
			}
			old := r.f
			r.f = &Factory{Repository: old.Repository, GitHub: r.gh, Repo: old.Repo,
				Formalizer: r.form, Builder: r.build, Base: old.Base, Projects: old.Projects,
				Work: old.Work, Check: old.Check, Log: t.Logf}
			posts := len(r.gh.posts(1))
			r.poll()
			if len(r.gh.posts(1)) != posts || len(r.form.requests) != 2 {
				t.Fatal("restart replayed the earlier commands")
			}
			r.form.forks = nil
			last := r.gh.say(1, "gitdek", "/invariant choose F2 A")
			r.poll()
			want = append(want, partialChoices422Answer(forks[1], "A", last))
			proposal := r.expect(1, KindProposal, LabelProposal)
			if len(r.form.requests) != 3 || !reflect.DeepEqual(r.form.requests[2].Answers, want) || !reflect.DeepEqual(proposal.Marker.Answers, want) {
				t.Fatalf("completed choices lost an answer: requests %+v, proposal %+v; want %+v", r.form.requests, proposal.Marker.Answers, want)
			}
			r.form.revised = []formalize.Revision{{Fork: "f1", Option: "A", By: "gitdek"}}
			changed := r.gh.say(1, "gitdek", "Use option A for the latest F1 question.\n\n/invariant revise")
			r.poll()
			want[len(want)-2] = partialChoices422Answer(next, "A", changed)
			proposal = r.expect(1, KindProposal, LabelProposal)
			if !reflect.DeepEqual(proposal.Marker.Answers, want) {
				t.Fatalf("revision changed the wrong question or lost provenance: %+v; want %+v", proposal.Marker.Answers, want)
			}
			partialChoices422NoPending(t, r)
		})
	}
}
