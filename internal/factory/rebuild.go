package factory

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/gitdek/invariant/factory/recovery/recovery"
	"github.com/gitdek/invariant/internal/github"
)

// A writer's retry of a plan's build that failed in the factory's own run,
// and left a draft, builds the plan again (#271, D-0218). It's the recovery core's
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
// the factory's own run, leaving a draft, by building the plan again. A
// retry applies to the failure that was the issue's latest when it was
// posted, so one posted before it rebuilds nothing. The protocol must allow
// the Rebuild before anything happens, and the build starts from the plan's
// ratification, which the draft's branch holds. Then each effect is looked
// for before it's taken (D-0069): the retry's record, the draft's close and
// the answer. A draft someone else merged, or closed before the record was
// stored, isn't the factory's to replace, so watch records it, and the
// retry is answered after it. A watcher that paused partway, and finds the
// retry answered or the issue moved on when it comes back, answers nothing.
func (f *Factory) rebuild(ctx context.Context, t Thread, state Post, c Command) error {
	n, m := t.Issue.Number, state.Marker
	old, step := m.PR, replaceStepName(c)
	want := fmt.Sprintf("#%d", old)
	_, trusted, _ := trustedOf(m)
	if c.Comment < state.Comment.ID {
		return f.note(ctx, t, c, fmt.Sprintf("This retry came before #%d failed, so it doesn't build the plan again.", old), "\n\n"+rebuildOffer(old, trusted))
	}
	before, after := f.rebuildStep(t, state, c)
	if err := f.allowed(ctx, n, "build the ratified plan again, replacing "+want, before, after); err != nil {
		return err
	}
	if err := f.Repo.Fetch(ctx); err != nil {
		return err
	}
	from, missing, err := f.ratificationOf(ctx, m)
	if err != nil {
		return err
	}
	if from == "" {
		return f.note(ctx, t, c, "I can't build the plan again: "+missing+".", cantRebuild(old))
	}
	recorded, err := f.Repo.Recorded(ctx, n, step)
	if err != nil {
		return err
	}
	switch recorded {
	case want:
	case "":
		// The draft as it is now: someone else's close or merge before the
		// record is theirs.
		pr, err := f.GitHub.PullRequest(ctx, old)
		if err != nil {
			return err
		}
		if pr.Merged || pr.State == "closed" {
			return f.watch(ctx, t, state)
		}
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
	if current, err := f.stillCurrent(ctx, t.Issue, state, c); err != nil || !current {
		if err == nil {
			f.logf("#%d: another watcher answered the retry, or the issue moved on, so this one answers nothing", n)
		}
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

// ratificationGone is a retry's build that can't start: what it starts
// from, the plan's ratification, is gone from the clone (D-0220).
type ratificationGone struct{ missing string }

func (e ratificationGone) Error() string { return "I can't build the plan again: " + e.missing }

// ratificationOf is the commit a rebuild of m's plan starts from: the one an
// earlier rebuild started from, or the first commit on the draft's branch
// that holds the plan's lock, as the clone has them after a fetch. When it's
// gone, missing says what's gone.
func (f *Factory) ratificationOf(ctx context.Context, m Marker) (from, missing string, err error) {
	if m.From != "" {
		if _, err := f.Repo.RevParse(ctx, m.From); err != nil {
			return "", fmt.Sprintf("`%s`, the commit with the plan's ratification that its last build started from, is gone", m.From), nil
		}
		return m.From, "", nil
	}
	if _, err := f.Repo.RevParse(ctx, "origin/"+m.Branch); err != nil {
		return "", fmt.Sprintf("`%s`, the branch that holds the plan's ratification, is gone", m.Branch), nil
	}
	if from, err = f.Repo.FirstCommit(ctx, "origin/"+f.Base, "origin/"+m.Branch, lockFileOf(m.Project)); err != nil {
		return "", "", err
	}
	if from == "" {
		return "", fmt.Sprintf("`%s` holds no commit that ratified the plan", m.Branch), nil
	}
	return from, "", nil
}

// stillCurrent says whether state is still the issue's latest post, and
// writer c's command still waits for an answer, as GitHub has them now.
func (f *Factory) stillCurrent(ctx context.Context, issue github.Issue, state Post, c Command) (bool, error) {
	fresh, err := f.read(ctx, issue)
	if err != nil {
		return false, err
	}
	latest, ok := fresh.State()
	if !ok || latest.Comment.ID != state.Comment.ID {
		return false, nil
	}
	return slices.ContainsFunc(fresh.Pending(), func(p Command) bool { return p.Comment == c.Comment }), nil
}

// Why a build's run no longer stands for the issue's latest state, if it
// doesn't.
type supersession int

const (
	current  supersession = iota // the build's post is still the issue's latest state
	replaced                     // a later retry started a newer build, so this run is stale
	answered                     // a later post answered the build, as another watcher's report that it stopped
)

// superseded says whether a later post superseded the build that ratified
// started, as when a writer retried while its watcher was away. Such a
// watcher records the run as its own and publishes nothing of it (D-0212).
func (f *Factory) superseded(ctx context.Context, n int, ratified Post) (supersession, error) {
	comments, err := f.GitHub.Comments(ctx, n)
	if err != nil {
		return current, err
	}
	var t Thread
	for _, c := range comments {
		if m, ok := DecodeMarker(c.Body); ok && (f.Self == "" || c.User.Login == f.Self) {
			t.Posts = append(t.Posts, Post{Comment: c, Marker: m})
		}
	}
	state, ok := t.State()
	switch {
	case !ok || state.Comment.ID == ratified.Comment.ID:
		return current, nil
	case latestBuild(t).Comment.ID != ratified.Comment.ID:
		return replaced, nil
	}
	return answered, nil
}

// buildPullRequest finds the pull request the build ratified started opened
// from branch, whatever has become of it since: open, closed or merged. So a
// crash, or someone closing it before the factory says so, never makes a
// build open a second, as the recovery core's OnePullRequest has it (#271,
// D-0203). One from the branch opened before the build started is an
// earlier build's.
func (f *Factory) buildPullRequest(ctx context.Context, branch string, ratified Post) (github.PullRequest, bool, error) {
	pr, found, err := f.GitHub.PullRequestFrom(ctx, branch)
	if err != nil || !found || pr.CreatedAt < ratified.Comment.CreatedAt {
		return github.PullRequest{}, false, err
	}
	return pr, true, nil
}

// stillBuilding says whether ratified, the post that started a build, still
// stands for the issue's latest state, so its result may be published: a
// watcher that paused after its run publishes nothing once a later post
// answered or replaced the build (#271).
func (f *Factory) stillBuilding(ctx context.Context, n int, ratified Post) (bool, error) {
	why, err := f.superseded(ctx, n, ratified)
	if err == nil && why != current {
		f.logf("#%d: a later post answered or replaced this build, so this watcher publishes nothing of it", n)
	}
	return why == current, err
}

// latestBuild is the post that started the issue's latest build: the latest
// that says it's ratified and building.
func latestBuild(t Thread) Post {
	for i := len(t.Posts) - 1; i >= 0; i-- {
		if t.Posts[i].Marker.Kind == KindRatified {
			return t.Posts[i]
		}
	}
	return Post{}
}

// finishSuperseded records a superseded build's run as its own, and says so:
// a stale run beside a newer build's, or the run of a build already answered.
func (f *Factory) finishSuperseded(ctx context.Context, n int, why supersession, step, result string) error {
	ok := false
	switch why {
	case replaced:
		newer := building(RunNone, false, false)
		newer.attempt = 1
		ok = f.canFinishStaleRun(newer)
	case answered:
		done := building(RunRecorded, false, false)
		done.posted = append(done.posted, recovery.Build)
		ok = f.canFinishRun(done, recovery.Build)
	}
	if err := f.recovers(ctx, n, "record a superseded build's result", ok); err != nil {
		return err
	}
	if err := f.Repo.Finish(ctx, n, step, result); err != nil {
		return err
	}
	f.logf("#%d: a later post superseded %s, so its result is recorded and nothing of it is published", n, step)
	return errSuperseded
}
