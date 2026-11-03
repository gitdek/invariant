package factory

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
)

// Leaving two forks open must be a valid protocol step, and each later
// choice set must get its own durable acknowledgment before a revise.
func TestPartialChoicesIncrementalFeedbackAndRevision(t *testing.T) {
	r, forks, original := partialChoices422Rig(t)
	f1 := r.gh.say(1, "gitdek", "/invariant choose F1 A")
	r.poll()
	first := r.expect(1, KindForks, LabelAsking)
	if len(first.Marker.Forks) != 2 || first.Marker.Forks[0].ID != "F2" || first.Marker.Forks[1].ID != "F3" {
		t.Fatalf("first partial choices left forks %+v; want F2 and F3", first.Marker.Forks)
	}
	for _, id := range []string{"F2", "F3"} {
		if !strings.Contains(first.Comment.Body, "/invariant choose "+id) {
			t.Errorf("partial-choice feedback doesn't explain how to answer %s", id)
		}
	}
	partialChoices422Answers(t, first.Marker.Answers, []formalize.Answer{partialChoices422Answer(forks[0], "A", f1)})
	partialChoices422NoPending(t, r)
	f3 := r.gh.say(1, "gitdek", "/invariant choose F3 B")
	r.poll()
	second := r.expect(1, KindForks, LabelAsking)
	if len(second.Marker.Forks) != 1 || second.Marker.Forks[0].ID != "F2" {
		t.Fatalf("second partial choices left forks %+v; want F2", second.Marker.Forks)
	}
	if second.Marker.Agent != "" || second.Marker.Spend != 0 {
		t.Errorf("recording choices reported agent usage again: %+v", second.Marker)
	}
	want := []formalize.Answer{partialChoices422Answer(forks[0], "A", f1), partialChoices422Answer(forks[2], "B", f3)}
	partialChoices422Answers(t, second.Marker.Answers, want)
	if got := len(r.gh.posts(1)); got != 3 || len(r.form.requests) != 1 {
		t.Fatalf("two partial sets gave %d posts and %d drafts; want 3 posts and 1 draft", got, len(r.form.requests))
	}
	old := r.f
	r.f = &Factory{Repository: old.Repository, GitHub: r.gh, Repo: old.Repo,
		Formalizer: r.form, Builder: r.build, Base: old.Base, Projects: old.Projects,
		Work: old.Work, Check: old.Check, Log: t.Logf}
	r.poll()
	if len(r.gh.posts(1)) != 3 || len(r.form.requests) != 1 {
		t.Fatal("restart repeated partial-choice feedback or drafted without a command")
	}
	r.form.forks = nil
	r.gh.say(1, "gitdek", "For F2, retry the original message.\n\n/invariant revise")
	r.poll()
	p := r.expect(1, KindProposal, LabelProposal)
	if len(r.form.requests) != 2 {
		t.Fatalf("revision gave %d drafts; want 2", len(r.form.requests))
	}
	partialChoices422Answers(t, p.Marker.Answers, want)
	if !reflect.DeepEqual(r.form.requests[1].Previous, original.Marker.Proposal) {
		t.Fatal("incremental choices replaced the original draft passed to revise")
	}
	partialChoices422NoPending(t, r)
}

// A choice and revise can share the same comment. Repeated choices for a
// fork in that batch count once, preserving the final command's answer.
func TestPartialChoicesAndReviseInOneComment(t *testing.T) {
	r, forks, original := partialChoices422Rig(t)
	c := r.gh.say(1, "gitdek", "Retain the message for F2.\n\n/invariant choose F1 B\n/invariant choose F1 A\n/invariant revise\n/invariant choose F3 B")
	r.form.forks = nil
	r.poll()
	p := r.expect(1, KindProposal, LabelProposal)
	if len(r.form.requests) != 2 {
		t.Fatalf("choice and revise gave %d drafts; want 2", len(r.form.requests))
	}
	want := []formalize.Answer{partialChoices422Answer(forks[0], "A", c), partialChoices422Answer(forks[2], "B", c)}
	partialChoices422Answers(t, r.form.requests[1].Answers, want)
	partialChoices422Answers(t, p.Marker.Answers, want)
	if !reflect.DeepEqual(r.form.requests[1].Previous, original.Marker.Proposal) {
		t.Fatal("revision lost the draft it revises")
	}
	partialChoices422NoPending(t, r)
	posts := len(r.gh.posts(1))
	r.poll()
	if len(r.gh.posts(1)) != posts || len(r.form.requests) != 2 {
		t.Fatal("the shared choice/revise comment replayed")
	}
}

// A malformed command sharing a comment with a valid choice cannot make
// the valid choice disappear when that comment is acknowledged.
func TestPartialChoicesKeepValidAnswersBesideInvalidChoices(t *testing.T) {
	r, forks, _ := partialChoices422Rig(t)
	c := r.gh.say(1, "gitdek", "/invariant choose F1 A\n/invariant choose F9 A\n/invariant choose F3 Z")
	r.poll()
	p := r.expect(1, KindForks, LabelAsking)
	want := []formalize.Answer{partialChoices422Answer(forks[0], "A", c)}
	partialChoices422Answers(t, p.Marker.Answers, want)
	for _, problem := range []string{"There's no open question F9.", "F3 has no option Z."} {
		if !strings.Contains(p.Comment.Body, problem) {
			t.Errorf("partial feedback omitted invalid choice: %s", problem)
		}
	}
	partialChoices422NoPending(t, r)
	r.form.forks = nil
	r.gh.say(1, "gitdek", "Keep the message and let the shipper choose.\n\n/invariant revise")
	r.poll()
	p = r.expect(1, KindProposal, LabelProposal)
	partialChoices422Answers(t, p.Marker.Answers, want)
	partialChoices422Answers(t, r.form.requests[1].Answers, want)
	partialChoices422NoPending(t, r)
}
