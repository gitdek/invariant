package factory

import (
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
)

func TestPartialChoicesAccumulateReplacementsAndSurviveDeletedComments(t *testing.T) {
	r, forks, _ := partialChoices422Rig(t)
	first := r.gh.say(1, "gitdek", "/invariant choose F1 A")
	r.poll()
	note := r.expect(1, KindNote, LabelAsking)
	for _, id := range []string{"F2", "F3"} {
		if !strings.Contains(note.Comment.Body, "/invariant choose "+id) {
			t.Errorf("partial choices did not name unanswered %s: %s", id, note.Comment.Body)
		}
	}

	replacement := r.gh.say(1, "gitdek", "/invariant choose f1 b")
	f3 := r.gh.say(1, "gitdek", "/invariant choose F3 A")
	before := len(r.gh.posts(1))
	r.poll()
	if len(r.gh.posts(1)) != before+1 || len(r.form.requests) != 1 {
		t.Fatal("an updated partial set must get exactly one answer without drafting")
	}
	note = r.expect(1, KindNote, LabelAsking)
	if !strings.Contains(note.Comment.Body, "/invariant choose F2") ||
		strings.Contains(note.Comment.Body, "/invariant choose F1") || strings.Contains(note.Comment.Body, "/invariant choose F3") {
		t.Fatalf("updated partial-choice response asks the wrong questions: %s", note.Comment.Body)
	}
	// The durable answer records retain the writer and original comment URL
	// even if those comments disappear before the remaining fork is chosen.
	r.gh.comments[1] = slices.DeleteFunc(r.gh.comments[1], func(c github.Comment) bool {
		return c.ID == first.ID || c.ID == replacement.ID || c.ID == f3.ID
	})
	// An unrelated note must not hide the saved partial answers.
	r.gh.say(1, "gitdek", "/invariant ratify 0123456789ab")
	r.poll()
	r.expect(1, KindNote, LabelAsking)
	r.form.forks = nil
	f2 := r.gh.say(1, "gitdek", "/invariant choose F2 A")
	r.poll()
	want := []formalize.Answer{
		partialChoices422Answer(forks[0], "B", replacement),
		partialChoices422Answer(forks[1], "A", f2),
		partialChoices422Answer(forks[2], "A", f3),
	}
	if len(r.form.requests) != 2 {
		t.Fatalf("completing saved choices drafted %d times; want 2 total", len(r.form.requests))
	}
	partialChoices422Answers(t, r.form.requests[1].Answers, want)
	partialChoices422Answers(t, r.expect(1, KindProposal, LabelProposal).Marker.Answers, want)
	partialChoices422NoPending(t, r)
}

func TestPartialChoicesNotesCannotReplaceALaterDraftsAnswers(t *testing.T) {
	r, forks, _ := partialChoices422Rig(t)
	r.gh.say(1, "gitdek", "/invariant choose F1 A")
	r.poll()
	r.form.forks = nil
	r.form.revised = []formalize.Revision{{Fork: "F1", Option: "B", By: "gitdek"}}
	changed := r.gh.say(1, "gitdek", "Change F1 to B: return an error.\n\n/invariant revise")
	r.poll()
	want := []formalize.Answer{partialChoices422Answer(forks[0], "B", changed)}
	partialChoices422Answers(t, r.expect(1, KindProposal, LabelProposal).Marker.Answers, want)
	r.form.revised = nil
	r.gh.say(1, "gitdek", "/invariant revise")
	r.poll()
	if len(r.form.requests) != 3 {
		t.Fatalf("expected a second revision, got %d drafts", len(r.form.requests))
	}
	partialChoices422Answers(t, r.form.requests[2].Answers, want)
	partialChoices422Answers(t, r.expect(1, KindProposal, LabelProposal).Marker.Answers, want)
}

func TestPartialChoicesSurviveARevisionRefusedBeforeDrafting(t *testing.T) {
	r, forks, _ := partialChoices422Rig(t)
	f1 := r.gh.say(1, "gitdek", "/invariant choose F1 A")
	r.poll()
	want := []formalize.Answer{partialChoices422Answer(forks[0], "A", f1)}
	body := r.gh.issues[1].Body
	r.gh.issues[1].Body += "\n\nProject: ../outside"
	r.gh.say(1, "gitdek", "/invariant revise")
	r.poll()
	partialChoices422Answers(t, r.expect(1, KindStuck, LabelHumanReview).Marker.Answers, want)
	if len(r.form.requests) != 1 {
		t.Fatal("an invalid project directory should be refused before drafting")
	}
	r.gh.issues[1].Body = body
	r.form.forks = nil
	r.gh.say(1, "gitdek", "/invariant revise")
	r.poll()
	if len(r.form.requests) != 2 {
		t.Fatalf("expected a draft after fixing the directory, got %d", len(r.form.requests))
	}
	partialChoices422Answers(t, r.form.requests[1].Answers, want)
	partialChoices422Answers(t, r.expect(1, KindProposal, LabelProposal).Marker.Answers, want)
}

func TestPartialChoicesReviseConsumesOnlyPrecedingChoices(t *testing.T) {
	r, forks, _ := partialChoices422Rig(t)
	f1 := r.gh.say(1, "gitdek", "/invariant choose F1 A")
	r.gh.say(1, "gitdek", "/invariant revise")
	later := r.gh.say(1, "gitdek", "/invariant choose F3 B")
	r.form.forks = nil
	r.poll()
	if len(r.form.requests) != 2 {
		t.Fatalf("revise drafted %d times; want 2 total", len(r.form.requests))
	}
	partialChoices422Answers(t, r.form.requests[1].Answers, []formalize.Answer{partialChoices422Answer(forks[0], "A", f1)})
	p := r.expect(1, KindProposal, LabelProposal)
	if slices.Contains(p.Marker.ReplyTo, later.ID) {
		t.Fatal("revision consumed a choice that followed it")
	}
	r.poll()
	r.expect(1, KindNote, LabelProposal)
	partialChoices422NoPending(t, r)
}
