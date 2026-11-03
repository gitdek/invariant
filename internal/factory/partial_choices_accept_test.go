package factory

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
)

// A revise queued after a subset of the choices must reach the formalizer.
// In #417, F1 and F3 were decided and F2 was answered in prose, but choose
// kept waiting for an explicit F2 choice and never reached the revise.
func TestPartialChoices422ReviseUsesChosenForks(t *testing.T) {
	r, forks, original := partialChoices422Rig(t)
	f1 := r.gh.say(1, "gitdek", "/invariant choose F1 A")
	f3 := r.gh.say(1, "gitdek", "/invariant choose F3 B")
	const clarification = "For F2, retain the original message and retry it on the next send."
	revision := r.gh.say(1, "gitdek", clarification+"\n\n/invariant revise")
	// Let the scripted formalizer propose once the factory actually calls
	// it. Deciding whether the prose resolves F2 belongs to that formalizer.
	r.form.forks = nil
	r.poll()
	if len(r.form.requests) != 2 {
		t.Fatalf("a revise after F1 and F3 must draft without an F2 choice: got %d drafts, want 2", len(r.form.requests))
	}
	req := r.form.requests[1]
	want := []formalize.Answer{
		partialChoices422Answer(forks[0], "A", f1),
		partialChoices422Answer(forks[2], "B", f3),
	}
	partialChoices422Answers(t, req.Answers, want)
	if !reflect.DeepEqual(req.Previous, original.Marker.Proposal) {
		t.Errorf("revise lost the draft it revises: previous = %+v, want %+v", req.Previous, original.Marker.Proposal)
	}
	if !strings.Contains(req.Markdown(), clarification) {
		t.Error("the formalizer did not receive the writer's explanation of F2")
	}
	p := r.expect(1, KindProposal, LabelProposal)
	partialChoices422Answers(t, p.Marker.Answers, want)
	for _, id := range []int64{f1.ID, f3.ID, revision.ID} {
		if !partialChoices422Replied(p.Marker.ReplyTo, id) {
			t.Errorf("draft reply_to = %v; missing consumed command %d", p.Marker.ReplyTo, id)
		}
	}
	partialChoices422NoPending(t, r)
	posts := len(r.gh.posts(1))
	r.poll()
	if len(r.form.requests) != 2 || len(r.gh.posts(1)) != posts {
		t.Fatal("consumed choices or revise were processed again on the next poll")
	}
}

// An incomplete choice set gets one actionable answer. The chosen values
// must survive that answer, whichever command later resumes the work.
func TestPartialChoices422NamesMissingForkAndRetainsAnswers(t *testing.T) {
	for _, resume := range []string{"choose", "revise"} {
		t.Run(resume, func(t *testing.T) {
			r, forks, _ := partialChoices422Rig(t)
			f1 := r.gh.say(1, "gitdek", "/invariant choose F1 A")
			f3 := r.gh.say(1, "gitdek", "/invariant choose F3 B")
			before := len(r.gh.posts(1))
			r.poll()
			if got := len(r.gh.posts(1)); got != before+1 {
				t.Fatalf("partial choices need one actionable reply naming F2: got %d new replies", got-before)
			}
			note := r.gh.last(1)
			if !strings.Contains(note.Comment.Body, "/invariant choose F2") {
				t.Errorf("partial-choice reply must say how to answer the missing F2:\n%s", note.Comment.Body)
			}
			for _, answered := range []string{"F1", "F3"} {
				if strings.Contains(note.Comment.Body, "/invariant choose "+answered) {
					t.Errorf("partial-choice reply asks for the already chosen %s again:\n%s", answered, note.Comment.Body)
				}
			}
			if !sameSet(r.gh.labelsOf(1), []string{LabelAsking}) {
				t.Errorf("partial choices must still wait for a person: labels = %v", r.gh.labelsOf(1))
			}
			r.poll()
			r.poll()
			if len(r.gh.posts(1)) != before+1 || len(r.form.requests) != 1 {
				t.Fatal("unchanged partial choices must neither repeat the reply nor start a draft")
			}
			// A new watcher must reconstruct the accepted choices from the
			// issue, rather than relying on the previous watcher's memory.
			old := r.f
			r.f = &Factory{Repository: old.Repository, GitHub: r.gh, Repo: old.Repo,
				Formalizer: r.form, Builder: r.build, Base: old.Base, Projects: old.Projects,
				Work: old.Work, Check: old.Check, Log: t.Logf}
			r.poll()
			if len(r.gh.posts(1)) != before+1 || len(r.form.requests) != 1 {
				t.Fatal("a restarted watcher must not repeat the partial-choice reply or start a draft")
			}

			want := []formalize.Answer{
				partialChoices422Answer(forks[0], "A", f1),
				partialChoices422Answer(forks[2], "B", f3),
			}
			if resume == "choose" {
				f2 := r.gh.say(1, "gitdek", "/invariant choose F2 A")
				want = append(want, partialChoices422Answer(forks[1], "A", f2))
			} else {
				r.gh.say(1, "gitdek", "For F2, retain the original message for the next send.\n\n/invariant revise")
			}
			r.form.forks = nil
			r.poll()
			if len(r.form.requests) != 2 {
				t.Fatalf("%s after the partial-choice reply must draft once: got %d drafts", resume, len(r.form.requests))
			}
			partialChoices422Answers(t, r.form.requests[1].Answers, want)
			p := r.expect(1, KindProposal, LabelProposal)
			partialChoices422Answers(t, p.Marker.Answers, want)
			partialChoices422NoPending(t, r)
			posts := len(r.gh.posts(1))
			r.poll()
			if len(r.form.requests) != 2 || len(r.gh.posts(1)) != posts {
				t.Fatal("completing partial choices must consume their commands exactly once")
			}
		})
	}
}

func partialChoices422Rig(t *testing.T) (*rig, []formalize.Fork, Post) {
	t.Helper()
	r := newRig(t)
	forks := []formalize.Fork{
		{ID: "F1", Question: "What happens when the buffer is full?", Options: []formalize.Option{{ID: "A", Says: "Wait for room."}, {ID: "B", Says: "Return an error."}}},
		{ID: "F2", Question: "What happens when sending fails?", Options: []formalize.Option{{ID: "A", Says: "Retry the original message."}, {ID: "B", Says: "Discard the message."}}},
		{ID: "F3", Question: "Who chooses when to send?", Options: []formalize.Option{{ID: "A", Says: "The producer chooses."}, {ID: "B", Says: "The shipper chooses."}}},
	}
	r.form.forks = forks
	r.gh.open(1, "gitdek", "Add a bounded buffer", "A buffer for log messages.\n\n/invariant solve")
	r.poll()
	return r, forks, r.expect(1, KindForks, LabelAsking)
}

func partialChoices422Answer(fork formalize.Fork, option string, comment github.Comment) formalize.Answer {
	o, _ := fork.Option(option)
	return formalize.Answer{Fork: fork.ID, Question: fork.Question, Option: o.ID, Says: o.Says, By: comment.User.Login, Comment: comment.URL}
}

func partialChoices422Answers(t *testing.T, got, want []formalize.Answer) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("decided answers = %+v; want exactly %+v", got, want)
	}
	seen := map[string]formalize.Answer{}
	for _, a := range got {
		if _, duplicate := seen[a.Fork]; duplicate {
			t.Fatalf("fork %s was recorded twice: %+v", a.Fork, got)
		}
		seen[a.Fork] = a
	}
	for _, a := range want {
		if seen[a.Fork] != a {
			t.Errorf("answer for %s = %+v; want %+v, including the writer and original comment", a.Fork, seen[a.Fork], a)
		}
	}
}

func partialChoices422Replied(ids []int64, want int64) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func partialChoices422NoPending(t *testing.T, r *rig) {
	t.Helper()
	thread, err := r.f.read(context.Background(), *r.gh.issues[1])
	if err != nil {
		t.Fatal(err)
	}
	if pending := thread.Pending(); len(pending) != 0 {
		t.Errorf("consumed choices or revise remain pending: %+v", pending)
	}
}
