package factory

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
)

func TestPartialChoicesAccumulateAndCanBeCorrected(t *testing.T) {
	r, forks, _ := partialChoices422Rig(t)
	r.gh.say(1, "gitdek", "/invariant choose F1 A")
	r.poll()
	r.expect(1, KindForks, LabelAsking)
	posts := len(r.gh.posts(1))
	f3 := r.gh.say(1, "gitdek", "/invariant choose F3 B")
	r.poll()
	if len(r.gh.posts(1)) != posts+1 {
		t.Fatal("the next partial choice needs exactly one reply")
	}
	p := r.expect(1, KindForks, LabelAsking)
	if len(p.Marker.Forks) != 1 || p.Marker.Forks[0].ID != "F2" {
		t.Fatalf("unanswered forks = %+v", p.Marker.Forks)
	}
	f1 := r.gh.say(1, "gitdek", "/invariant choose F1 B")
	r.poll()
	want := []formalize.Answer{
		partialChoices422Answer(forks[0], "B", f1),
		partialChoices422Answer(forks[2], "B", f3),
	}
	partialChoices422Answers(t, r.gh.last(1).Marker.Answers, want)
	// An invalid answer changes neither the saved choices nor what remains.
	r.gh.say(1, "gitdek", "/invariant choose F2 Z")
	r.poll()
	r.expect(1, KindNote, LabelAsking)
	r.gh.say(1, "gitdek", "Keep the original message for F2.\n\n/invariant revise")
	r.form.forks = nil
	r.poll()
	if len(r.form.requests) != 2 {
		t.Fatalf("drafts = %d; want the original and one revision", len(r.form.requests))
	}
	partialChoices422Answers(t, r.form.requests[1].Answers, want)
	partialChoices422NoPending(t, r)
}

func TestPartialChoicesStopAtTheFollowingRevision(t *testing.T) {
	r, forks, original := partialChoices422Rig(t)
	f1 := r.gh.say(1, "gitdek", "/invariant choose F1 A")
	r.gh.say(1, "gitdek", "Reconsider the remaining questions.\n\n/invariant revise")
	later := r.gh.say(1, "gitdek", "/invariant choose F3 B")
	r.form.forks = nil
	r.poll()
	if len(r.form.requests) != 2 {
		t.Fatalf("drafts = %d; want 2", len(r.form.requests))
	}
	req := r.form.requests[1]
	partialChoices422Answers(t, req.Answers, []formalize.Answer{partialChoices422Answer(forks[0], "A", f1)})
	if req.Previous == nil || req.Previous.Name != original.Marker.Proposal.Name || !strings.Contains(req.Markdown(), "Reconsider the remaining questions.") {
		t.Fatal("revision lost its previous draft or the writer's prose")
	}
	if partialChoices422Replied(r.gh.last(1).Marker.ReplyTo, later.ID) {
		t.Fatal("revision consumed a choice posted after it")
	}
}
