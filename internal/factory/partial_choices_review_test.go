package factory

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
)

func TestPartialChoicesKeepEarlierQuestionWhenDraftReusesForkID(t *testing.T) {
	r, forks, _ := partialChoices422Rig(t)
	first := r.gh.say(1, "gitdek", "/invariant choose F1 A")
	r.gh.say(1, "gitdek", "/invariant revise")
	next := []formalize.Fork{
		{ID: "F1", Question: "Who may close the buffer?", Options: []formalize.Option{{ID: "A", Says: "Only the producer."}, {ID: "B", Says: "Any caller."}}},
		forks[1],
	}
	r.form.forks = next
	r.poll()
	r.expect(1, KindForks, LabelAsking)
	second := r.gh.say(1, "gitdek", "/invariant choose F1 B")
	r.poll()
	want := []formalize.Answer{
		partialChoices422Answer(forks[0], "A", first),
		partialChoices422Answer(next[0], "B", second),
	}
	note := r.expect(1, KindNote, LabelAsking)
	if !reflect.DeepEqual(note.Marker.Answers, want) {
		t.Fatalf("reusing a fork ID lost an earlier question or its provenance: got %+v, want %+v", note.Marker.Answers, want)
	}
	// Replacing the newer question must still leave the older answer alone.
	replacement := r.gh.say(1, "gitdek", "/invariant choose f1 a")
	r.poll()
	want[1] = partialChoices422Answer(next[0], "A", replacement)
	note = r.expect(1, KindNote, LabelAsking)
	if !reflect.DeepEqual(note.Marker.Answers, want) {
		t.Fatalf("replacing the new answer changed the earlier one: got %+v, want %+v", note.Marker.Answers, want)
	}
	last := r.gh.say(1, "gitdek", "/invariant choose F2 A")
	want = append(want, partialChoices422Answer(next[1], "A", last))
	r.form.forks = nil
	r.poll()
	if len(r.form.requests) != 3 {
		t.Fatalf("expected three successive drafts, got %d", len(r.form.requests))
	}
	if got := r.form.requests[2].Answers; !reflect.DeepEqual(got, want) {
		t.Errorf("later draft lost a decided question: got %+v, want %+v", got, want)
	}
	if got := r.expect(1, KindProposal, LabelProposal).Marker.Answers; !reflect.DeepEqual(got, want) {
		t.Errorf("proposal lost a decided question: got %+v, want %+v", got, want)
	}
	// A prose revision also refers to the most recently asked question,
	// without changing the older decision that used the same fork ID.
	r.form.revised = []formalize.Revision{{Fork: "F1", Option: "B", By: "gitdek"}}
	changed := r.gh.say(1, "gitdek", "Let any caller close the buffer.\n/invariant revise")
	r.poll()
	want[1] = partialChoices422Answer(next[0], "B", changed)
	if got := r.expect(1, KindProposal, LabelProposal).Marker.Answers; !reflect.DeepEqual(got, want) {
		t.Errorf("revising the new question changed the older decision or its provenance: got %+v, want %+v", got, want)
	}
	partialChoices422NoPending(t, r)
}

func TestPartialChoicesReadWholeRevisionComment(t *testing.T) {
	for _, commands := range []string{
		"/invariant choose F1 A\n/invariant revise\n/invariant choose F3 B",
		"/invariant revise\n/invariant choose F1 A\n/invariant choose F3 B",
	} {
		t.Run(strings.Split(commands, "\n")[0], func(t *testing.T) {
			r, forks, original := partialChoices422Rig(t)
			comment := r.gh.say(1, "gitdek", commands)
			r.form.forks = nil
			r.poll()
			if len(r.form.requests) != 2 {
				t.Fatalf("revision comment must draft once: got %d drafts", len(r.form.requests))
			}
			want := []formalize.Answer{
				partialChoices422Answer(forks[0], "A", comment),
				partialChoices422Answer(forks[2], "B", comment),
			}
			partialChoices422Answers(t, r.form.requests[1].Answers, want)
			if !reflect.DeepEqual(r.form.requests[1].Previous, original.Marker.Proposal) {
				t.Error("revision did not receive the previous draft")
			}
			p := r.expect(1, KindProposal, LabelProposal)
			partialChoices422Answers(t, p.Marker.Answers, want)
			if !slices.Equal(p.Marker.ReplyTo, []int64{comment.ID}) {
				t.Errorf("revision should acknowledge the whole comment once: %v", p.Marker.ReplyTo)
			}
			partialChoices422NoPending(t, r)
			posts := len(r.gh.posts(1))
			r.poll()
			if len(r.form.requests) != 2 || len(r.gh.posts(1)) != posts {
				t.Fatal("revision or its following choices were replayed")
			}
		})
	}
}

func TestPartialChoicesReadPastInvalidChoiceInSameComment(t *testing.T) {
	for _, invalid := range []struct {
		command string
		problem string
	}{
		{"/invariant choose F2 Z", "F2 has no option Z."},
		{"/invariant choose F9 A", "There's no open question F9."},
		{"/invariant choose F2", "To answer a question"},
	} {
		t.Run(invalid.command, func(t *testing.T) {
			r, forks, _ := partialChoices422Rig(t)
			comment := r.gh.say(1, "gitdek", "/invariant choose F1 A\n"+invalid.command+"\n/invariant choose F3 B")
			r.poll()
			want := []formalize.Answer{
				partialChoices422Answer(forks[0], "A", comment),
				partialChoices422Answer(forks[2], "B", comment),
			}
			note := r.expect(1, KindNote, LabelAsking)
			partialChoices422Answers(t, note.Marker.Answers, want)
			if !strings.Contains(note.Comment.Body, invalid.problem) {
				t.Errorf("invalid choice was not explained: %s", note.Comment.Body)
			}
			if !slices.Equal(note.Marker.ReplyTo, []int64{comment.ID}) {
				t.Errorf("choice note should acknowledge the whole comment once: %v", note.Marker.ReplyTo)
			}
			partialChoices422NoPending(t, r)
			posts := len(r.gh.posts(1))
			r.poll()
			if len(r.form.requests) != 1 || len(r.gh.posts(1)) != posts {
				t.Fatal("invalid choice comment was replayed")
			}
			r.form.forks = nil
			r.gh.say(1, "gitdek", "/invariant revise")
			r.poll()
			if len(r.form.requests) != 2 {
				t.Fatalf("saved valid choices should resume in a revision: got %d drafts", len(r.form.requests))
			}
			partialChoices422Answers(t, r.form.requests[1].Answers, want)
		})
	}
}

func TestPartialChoicesInvalidChoiceDoesNotConsumeSameCommentRevision(t *testing.T) {
	r, forks, _ := partialChoices422Rig(t)
	comment := r.gh.say(1, "gitdek", "/invariant choose F1 A\n/invariant choose F2 Z\n/invariant revise\n/invariant choose F3 B")
	r.form.forks = nil
	before := len(r.gh.posts(1))
	r.poll()
	if len(r.form.requests) != 2 {
		t.Fatalf("invalid choice must not discard a later revision: got %d drafts", len(r.form.requests))
	}
	want := []formalize.Answer{
		partialChoices422Answer(forks[0], "A", comment),
		partialChoices422Answer(forks[2], "B", comment),
	}
	partialChoices422Answers(t, r.form.requests[1].Answers, want)
	posts := r.gh.posts(1)
	if len(posts) != before+2 {
		t.Fatalf("expected validation feedback and a draft, got %d posts", len(posts)-before)
	}
	note := posts[before]
	if note.Marker.Kind != KindNote || !strings.Contains(note.Comment.Body, "F2 has no option Z.") || len(note.Marker.ReplyTo) != 0 {
		t.Fatalf("validation feedback must leave the revision pending until its draft: %+v", note)
	}
	partialChoices422Answers(t, note.Marker.Answers, want)
	p := r.expect(1, KindProposal, LabelProposal)
	partialChoices422Answers(t, p.Marker.Answers, want)
	partialChoices422NoPending(t, r)
	r.poll()
	if len(r.form.requests) != 2 || len(r.gh.posts(1)) != len(posts) {
		t.Fatal("mixed validation and revision comment was replayed")
	}
}
