package factory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type interruptedRefusalFinish434Repo struct {
	Repo
	answer error
	failed bool
	step   string
}

func (r *interruptedRefusalFinish434Repo) Finish(ctx context.Context, issue int, step, result string) error {
	if !r.failed && strings.HasPrefix(step, "effect-refusal-") {
		r.failed = true
		r.step = step
		return r.answer
	}
	return r.Repo.Finish(ctx, issue, step, result)
}

// A retry's answer can reach GitHub before the durable refusal finishes.
// Recovering that record must retain the answer's current status while CI
// is pending, rather than restoring the old refusal's human-review label.
func TestMergeRefusal434InterruptedRetryKeepsWatchStatus(t *testing.T) {
	r, g := draftRig(t)
	n := ratified(t, r).Marker.PR
	const refusal = "merge rejected by installation (HTTP 422)"
	g.refuse = refusal
	r.gh.ci(n, "success")
	pollOK(t, r, "when GitHub refuses the merge")
	r.expect(1, KindFailed, LabelHumanReview)
	builds := len(r.build.built)

	g.refuse = ""
	retry := r.gh.say(1, "gitdek", "/invariant retry")
	interrupted := errors.New("the refusal finish lost its connection")
	repo := &interruptedRefusalFinish434Repo{Repo: r.f.Repo, answer: interrupted}
	r.f.Repo = repo
	if err := r.f.Poll(context.Background()); !errors.Is(err, interrupted) || !repo.failed {
		t.Fatalf("fixture must interrupt finishing the refusal after answering retry: failed=%v err=%v", repo.failed, err)
	}
	watched := r.expect(1, KindPR, LabelPR)
	if len(watched.Marker.ReplyTo) != 1 || watched.Marker.ReplyTo[0] != retry.ID {
		t.Fatalf("the retry must already have its answer: %+v", watched.Marker)
	}

	r.gh.ci(n, "")
	runs := r.gh.checks[r.gh.prs[n].Head.SHA]
	runs[len(runs)-1].Status = "in_progress"
	publication434Restart(t, r)
	for i := 0; i < 3; i++ {
		r.poll()
		r.expect(1, KindPR, LabelPR)
	}
	answers := 0
	for _, p := range r.gh.posts(1) {
		for _, id := range p.Marker.ReplyTo {
			if id == retry.ID {
				answers++
			}
		}
	}
	if answers != 1 || g.asked(fmt.Sprintf("merge #%d", n)) != 1 || len(r.gh.merged) != 0 || len(r.build.built) != builds {
		t.Fatalf("restart must retain one retry answer and wait for CI: answers=%d calls=%v merges=%v builds=%d", answers, g.calls, r.gh.merged, len(r.build.built))
	}
	if len(effects434Reports(r, 1, refusal)) != 1 {
		t.Fatal("recovering the retry repeated its refusal report")
	}

	r.gh.ci(n, "success")
	r.poll()
	r.expect(1, KindMerged, LabelMerged)
	if g.asked(fmt.Sprintf("merge #%d", n)) != 2 || len(r.gh.merged) != 1 {
		t.Fatalf("the already authorized watch must merge once CI passes: calls=%v merges=%v", g.calls, r.gh.merged)
	}
}

// A person can finish a refused merge while its issue stays open. If the
// observation reaches GitHub before its refusal record finishes, another
// watcher completes that record without repeating or relabeling the post,
// even if the issue closes after the observation was already recorded.
func TestMergeRefusal434InterruptedExternalMergeStaysRecorded(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(fmt.Sprintf("closed-before-restart=%v", closed), func(t *testing.T) {
			r, g := draftRig(t)
			n := ratified(t, r).Marker.PR
			const refusal = "merge rejected by installation (HTTP 422)"
			g.refuse = refusal
			r.gh.ci(n, "success")
			pollOK(t, r, "when GitHub refuses the merge")
			r.expect(1, KindFailed, LabelHumanReview)
			posts, builds := len(r.gh.posts(1)), len(r.build.built)

			pr := r.gh.prs[n]
			pr.Merged, pr.State, pr.MergeCommitSHA = true, "closed", "person434merge"
			interrupted := errors.New("the observed merge's refusal finish lost its connection")
			repo := &interruptedRefusalFinish434Repo{Repo: r.f.Repo, answer: interrupted}
			r.f.Repo = repo
			if err := r.f.Poll(context.Background()); !errors.Is(err, interrupted) || !repo.failed {
				t.Fatalf("fixture must interrupt finishing the refusal after recording the person's merge: failed=%v err=%v", repo.failed, err)
			}
			merged := r.expect(1, KindMerged, LabelMerged)
			visible, _, _ := strings.Cut(merged.Comment.Body, "<!-- invariant:")
			if !strings.Contains(visible, fmt.Sprintf("#%d was merged", n)) || strings.Contains(visible, "I merged") {
				t.Fatalf("the observation must attribute the merge to a person:\n%s", visible)
			}
			if r.gh.issues[1].State != "open" {
				t.Fatal("the fixture must leave the issue open until after its observation")
			}
			if closed {
				r.gh.issues[1].State = "closed"
			}

			publication434Restart(t, r)
			for i := 0; i < 3; i++ {
				r.poll()
				recovered := r.expect(1, KindMerged, LabelMerged)
				if recovered.Comment.ID != merged.Comment.ID {
					t.Fatalf("restart repeated the merged observation: before=%d after=%d", merged.Comment.ID, recovered.Comment.ID)
				}
			}
			if state, _, err := r.f.Repo.Run(context.Background(), 1, repo.step); err != nil || state != RunDone {
				t.Fatalf("the already-recorded observation must finish its refusal: state=%v err=%v", state, err)
			}
			if len(r.gh.posts(1)) != posts+1 || len(effects434Reports(r, 1, refusal)) != 1 {
				t.Fatalf("recovery must retain one refusal and one merged observation: posts=%d reports=%d", len(r.gh.posts(1))-posts, len(effects434Reports(r, 1, refusal)))
			}
			if g.asked(fmt.Sprintf("merge #%d", n)) != 1 || len(r.gh.merged) != 0 || len(r.gh.deleted) != 0 || len(r.build.built) != builds {
				t.Fatalf("recording a person's merge must perform no build, merge or branch cleanup: calls=%v merges=%v deleted=%v builds=%d", g.calls, r.gh.merged, r.gh.deleted, len(r.build.built))
			}
		})
	}
}
