package factory

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/gitdek/invariant/factory/recovery/recovery"
)

// A writer's retry of a plan's build that failed in the factory's own run,
// and left a draft, builds the plan again (#271). It's the recovery core's
// replacement (D-0203) and the protocol's Rebuild (D-0202): the factory
// records that the retry replaces the draft, closes the draft without
// merging it, so its failed work stays there to read, and answers the
// retry. The build that answer starts runs on a branch of its own, from the
// plan's ratification, so nothing of the draft stands for it, and it opens
// a pull request of its own, once, whatever becomes of it.

// errSuperseded is a build's run that a later post superseded, as when a
// writer retried while its watcher was away: its result is recorded as its
// own, and nothing of it is published.
var errSuperseded = errors.New("a later post superseded the build")

// replaceStepName names a retry's record that it replaces a failed draft,
// after the retry's own comment.
func replaceStepName(c Command) string { return fmt.Sprintf("replace-%d", c.Comment) }

// retrySuffix is what attemptBranch adds to a plan's branch.
var retrySuffix = regexp.MustCompile(`-retry-\d+$`)

// attemptBranch is the branch a retry's build of a plan builds on: the
// plan's branch, named for the retry's comment.
func attemptBranch(branch string, c Command) string {
	return fmt.Sprintf("%s-retry-%d", retrySuffix.ReplaceAllString(branch, ""), c.Comment)
}

// replacement finds the writer's retry that is replacing the draft of the
// issue's latest post: the first command still to answer is a retry, and
// the record it left says it replaces that draft. A closed draft with such a
// record is the factory's own replacement under way, not someone else's
// close (#271).
func (f *Factory) replacement(ctx context.Context, t Thread, state Post) (Command, bool, error) {
	pending := slices.DeleteFunc(t.Pending(), func(c Command) bool { return c.Verb == Stop })
	if len(pending) == 0 || pending[0].Verb != Retry || !state.Marker.Rebuilds() {
		return Command{}, false, nil
	}
	c := pending[0]
	what, err := f.Repo.Recorded(ctx, t.Issue.Number, replaceStepName(c))
	if err != nil {
		return c, false, err
	}
	return c, what == fmt.Sprintf("#%d", state.Marker.PR), nil
}

// rebuild answers writer c's retry of the plan build state says failed in
// the factory's own run, leaving a draft, by building the plan again. Each
// effect is looked for before it's taken (D-0069): the retry's record, the
// draft's close and the answer. A draft someone merged first has nothing to
// replace, so watch records the merge, and the retry is answered after it.
func (f *Factory) rebuild(ctx context.Context, t Thread, state Post, c Command) error {
	n, m := t.Issue.Number, state.Marker
	old, step := m.PR, replaceStepName(c)
	want := fmt.Sprintf("#%d", old)
	recorded, err := f.Repo.Recorded(ctx, n, step)
	if err != nil {
		return err
	}
	switch recorded {
	case want:
	case "":
		if err := f.recovers(ctx, n, "record that the retry replaces "+want, f.canRecordIntent(replacing(recovery.PredDraft, false))); err != nil {
			return err
		}
		if err := f.Repo.Record(ctx, n, step, want); err != nil && !errors.Is(err, ErrRecorded) {
			return err
		}
	default:
		return fmt.Errorf("#%d: the retry's record says it replaces %s, not %s", n, recorded, want)
	}
	pr, err := f.GitHub.PullRequest(ctx, old)
	if err != nil {
		return err
	}
	if !pr.Merged && pr.State != "closed" {
		if err := f.recovers(ctx, n, "close "+want, f.canRetire(replacing(recovery.PredDraft, true))); err != nil {
			return err
		}
		if pr, err = f.GitHub.ClosePullRequest(ctx, old); err != nil {
			return err
		}
	}
	if pr.Merged {
		return f.watch(ctx, t, state)
	}
	from := m.From
	if from == "" {
		if err := f.Repo.Fetch(ctx); err != nil {
			return err
		}
		if from, err = f.Repo.FirstCommit(ctx, "origin/"+f.Base, "origin/"+m.Branch, lockFileOf(m.Project)); err != nil {
			return err
		}
		if from == "" {
			return fmt.Errorf("#%d: %s holds no commit that ratified %s, so the plan can't be built again from it", n, m.Branch, m.Project)
		}
	}
	before, after := f.rebuildStep(t, state, c)
	if err := f.allowed(ctx, n, "build the ratified plan again, replacing "+want, before, after); err != nil {
		return err
	}
	if err := f.recovers(ctx, n, "answer the retry", f.canPost(replacing(recovery.PredRetired, true), recovery.Retry)); err != nil {
		return err
	}
	next := m.carried()
	next.Kind, next.ReplyTo, next.Failure, next.PR = KindRatified, []int64{c.Comment}, "", 0
	next.Branch, next.From, next.Replaces = attemptBranch(m.Branch, c), from, old
	return f.say(ctx, n, rebuildingComment(c.By, old, next), LabelBuilding)
}

// superseded says a build's post is no longer the issue's latest state: a
// watcher that comes back to such a build's run, as after another watcher
// reported it stopped and a writer retried, records the run as its own and
// publishes nothing of it, as the recovery core's Stale run (D-0203).
func (f *Factory) superseded(ctx context.Context, n int, ratified Post) (bool, error) {
	comments, err := f.GitHub.Comments(ctx, n)
	if err != nil {
		return false, err
	}
	var t Thread
	for _, c := range comments {
		if m, ok := DecodeMarker(c.Body); ok && (f.Self == "" || c.User.Login == f.Self) {
			t.Posts = append(t.Posts, Post{Comment: c, Marker: m})
		}
	}
	state, ok := t.State()
	return ok && state.Comment.ID != ratified.Comment.ID, nil
}

// finishSuperseded records a superseded build's run as its own, and says so.
func (f *Factory) finishSuperseded(ctx context.Context, n int, step, result string) error {
	if err := f.recovers(ctx, n, "record a superseded build's result", f.canFinishRun(building(RunRecorded, false, false), recovery.Stale)); err != nil {
		return err
	}
	if err := f.Repo.Finish(ctx, n, step, result); err != nil {
		return err
	}
	f.logf("#%d: a later post superseded %s, so its result is recorded and nothing of it is published", n, step)
	return errSuperseded
}
