package factory

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/github"
)

type commandAnswerRefusalHub struct {
	GitHub
	command  int64
	attempts int
}

func (g *commandAnswerRefusalHub) PostComment(ctx context.Context, n int, body string) (github.Comment, error) {
	if m, ok := DecodeMarker(body); ok && m.Kind == KindNote && slices.Contains(m.ReplyTo, g.command) {
		g.attempts++
		if g.attempts == 1 {
			return github.Comment{}, fmt.Errorf("POST issue comment: command answer refused (HTTP 422)")
		}
	}
	return g.GitHub.PostComment(ctx, n, body)
}

func TestRefusedCommandAnswerPreservesExistingHumanReviewAfterRetry(t *testing.T) {
	r := newRig(t)
	pr := ratified(t, r)
	r.gh.ci(pr.Marker.PR, "failure")
	r.poll()
	failed := r.expect(1, KindFailed, LabelHumanReview)
	builds := len(r.build.built)
	command := r.gh.say(1, "gitdek", "/invariant choose F1 A")
	g := &commandAnswerRefusalHub{GitHub: r.f.GitHub, command: command.ID}
	r.f.GitHub = g

	check := func(attempts, answers int) {
		t.Helper()
		state, ok := (Thread{Posts: r.gh.posts(1)}).State()
		if !ok || state.Comment.ID != failed.Comment.ID || !reflect.DeepEqual(state.Marker, failed.Marker) {
			t.Fatalf("answering the command must preserve the original failed state: %+v", state.Marker)
		}
		if !sameSet(r.gh.labelsOf(1), []string{LabelHumanReview}) {
			t.Fatalf("the original failure must still need human review: %v", r.gh.labelsOf(1))
		}
		got := 0
		for _, p := range r.gh.posts(1) {
			if slices.Contains(p.Marker.ReplyTo, command.ID) {
				got++
				if p.Marker.Kind != KindNote || !strings.Contains(p.Comment.Body, "There's no open question to answer right now.") {
					t.Fatalf("the resumed command must receive its original answer: %s", p.Comment.Body)
				}
			}
		}
		if g.attempts != attempts || got != answers {
			t.Fatalf("command answer attempts=%d posts=%d; want attempts=%d posts=%d", g.attempts, got, attempts, answers)
		}
		if len(r.build.built) != builds || len(r.gh.merged) != 0 {
			t.Fatal("resuming the command answer must not resume the failed build or merge its pull request")
		}
		if n := len(effects434Reports(r, 1, "command answer refused")); n != 1 {
			t.Fatalf("the command's refusal must be reported once; got %d reports", n)
		}
	}

	effects434Poll(t, r)
	check(1, 0)
	publication434Restart(t, r)
	for i := 0; i < 2; i++ {
		r.poll()
		check(1, 0)
	}
	r.gh.say(1, "mallory", "/invariant retry")
	r.poll()
	check(1, 0)

	// This retry releases only the refused answer. The CI failure still
	// needs its own intervention, even though its status is also human review.
	r.gh.say(1, "gitdek", "/invariant retry")
	r.poll()
	check(2, 1)
	posts := len(r.gh.posts(1))
	r.poll()
	publication434Restart(t, r)
	r.poll()
	check(2, 1)
	if len(r.gh.posts(1)) != posts {
		t.Fatal("later polls or another watcher repeated the answered command or its retry")
	}
}
