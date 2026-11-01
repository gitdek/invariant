package factory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gitdek/invariant/factory/recovery/recovery"
	"github.com/gitdek/invariant/internal/github"
)

// The watcher runs every effect through its recovery core, factory/recovery
// (D-0069). #23 ratified its rules, and the factory wrote it as Go that
// Gobra proves at every size and the gate checks against TLC, with a crash
// at any point and a second watcher. Before each effect, the watcher
// describes the step in hand in the core's terms: what every watcher can
// see of it on GitHub and in the run records, and whether this watcher holds
// the lease. It takes the effect only if the core allows it. So a bug in the
// watcher's own checks can't answer a command twice, start a run twice,
// open a second pull request or merge twice, and a watcher without the
// lease can't take an effect at all.
//
// The core knows each step's effects, not the issue protocol's rules. The
// protocol still decides which steps there are (D-0064). The posts the
// core's model leaves out, a CI failure and the limit on stopped builds, are
// the protocol's alone. A writer's retry that builds a plan's failed draft
// again is the core's replacement (#271, D-0203): its record, the draft's
// close and its answer are the core's own steps. Someone else closing or
// merging a pull request is still the protocol's alone: the posts that say
// so take no step of the core. Each build is an attempt
// with its own run, so a watcher still in the run of a build a later retry
// replaced is in a stale run, which the core records as its own (D-0212). A
// writer's retry that watches a modeled project's failed draft again is the
// core's Watch (#354): its answer, the draft's mark ready and the merge or
// the report that it can't merge are the core's steps too. Each pull
// request is the core's as GitHub has it: a draft is a draft, and only a
// ready one is ready. A draft is watched only as the draft the factory's
// answer saw (#371), so one a person made to stop the factory, after a retry
// watched it ready, is the core's draft no retry watches.

// errOutsideRecovery is what an effect the core doesn't allow fails with.
var errOutsideRecovery = errors.New("the factory's recovery rules don't allow this effect")

// me is this watcher in the core's terms, and another is any other.
const (
	me      = 0
	another = 1
)

// step is the step in hand, as every watcher can see it. draft says the
// pull request the build opened is a draft, and closedAs, when someone else
// closed or merged it since, says which, as a recovery.PR value. pred and
// intent are a retry's replacement: the draft it replaces, as a
// recovery.Pred value, and whether its record is stored. attempt is the
// latest build's attempt: 0 for the ratification's build, and one more for
// each retry's, as the core counts them.
type step struct {
	given     []int // the commands a writer gave for it: recovery.Solve, Ratify, Note, Retry or Watch
	posted    []int // the steps already answered
	runs      map[int]RunState
	ratPushed bool
	codePushed,
	prOpen,
	draft,
	merged,
	gatePassed,
	mergeable bool
	closedAs int
	pred     int
	intent   bool
	attempt  int
}

// repo is the step in the core's terms, with the lease as this watcher
// holds it.
func (f *Factory) repo(s step) recovery.Repo {
	r := recovery.NewRepo()
	for _, k := range s.given {
		r.Given[k] = true
	}
	for _, k := range s.posted {
		r.Posted[k] = true
	}
	for k, state := range s.runs {
		switch state {
		case RunRecorded:
			r.Runs[k] = recovery.RunRecorded
		case RunDone:
			r.Runs[k] = recovery.RunDone
		}
	}
	r.RatPushed, r.CodePushed, r.Opened, r.Merged = s.ratPushed, s.codePushed, s.prOpen, s.merged
	switch {
	case s.merged:
		r.PR = recovery.PRMerged
	case s.closedAs != recovery.PRNone:
		r.PR = s.closedAs
	case s.prOpen && s.draft:
		r.PR = recovery.PRDraft
	case s.prOpen:
		r.PR = recovery.PRReady
	}
	r.GatePassed, r.Mergeable = s.gatePassed, s.mergeable
	r.Pred, r.Intent, r.Attempt = s.pred, s.intent, s.attempt
	r.Lease = another
	if f.holds() {
		r.Lease = me
	}
	return r
}

// recovers checks one effect against the core. A refused effect labels the
// issue for a person, as a step outside the protocol does.
func (f *Factory) recovers(ctx context.Context, issue int, what string, ok bool) error {
	if ok {
		return nil
	}
	f.logf("#%d: refused to %s: the recovery rules don't allow it", issue, what)
	if err := f.status(ctx, issue, LabelHumanReview); err != nil {
		f.logf("#%d: %v", issue, err)
	}
	return fmt.Errorf("%w: %s", errOutsideRecovery, what)
}

// The steps as the watcher meets them.

// drafting is a draft for a writer's command, with its run as recorded.
func drafting(run RunState) step {
	return step{given: []int{recovery.Solve}, runs: map[int]RunState{recovery.Solve: run}}
}

// ratifying is a ratification, pushed or not. A writer's retry of a
// stopped build is one too: it answers the retry once the branch holds the
// ratification, which it already does.
func ratifying(pushed bool) step {
	return step{given: []int{recovery.Solve, recovery.Ratify}, posted: []int{recovery.Solve}, ratPushed: pushed}
}

// building is the build a ratification started, with its run as recorded,
// its code on the branch or not, and its pull request open or not.
func building(run RunState, codePushed, prOpen bool) step {
	return step{given: []int{recovery.Solve, recovery.Ratify}, posted: []int{recovery.Solve, recovery.Ratify},
		runs: map[int]RunState{recovery.Build: run}, ratPushed: true, codePushed: codePushed, prOpen: prOpen}
}

// merging is a pull request whose gate passed, which GitHub can merge or
// not, merged or not.
func merging(mergeable, merged bool) step {
	s := building(RunDone, true, true)
	s.posted = append(s.posted, recovery.Build)
	s.gatePassed, s.mergeable, s.merged = true, mergeable, merged
	return s
}

// drafted is s with its pull request a draft, as a build that failed opens
// it.
func drafted(s step) step {
	s.draft = true
	return s
}

// watching is a writer's retry that watches the latest build's failed draft
// again, answered or not (#354).
func watching(answered bool) step {
	s := drafted(building(RunDone, true, true))
	s.posted = append(s.posted, recovery.Build)
	s.given = append(s.given, recovery.Watch)
	if answered {
		s.posted = append(s.posted, recovery.Watch)
	}
	return s
}

// watchedMerging is a failed draft a writer's retry watches again, its
// retry answered and CI's gate passed on it, which the factory can merge or
// not (#354).
func watchedMerging(mergeable bool) step {
	s := watching(true)
	s.gatePassed, s.mergeable = true, mergeable
	return s
}

// watchState is how the factory's answers to writers' retries watch a draft
// (#371).
type watchState int

const (
	watchNone      watchState = iota // no answer to a retry watches it
	watchUnknown                     // the latest answer is from before answers recorded what they watched
	watchReady                       // the latest answer watched it ready, so someone turned it into a draft since
	watchRedrafted                   // the latest answer watched it as a draft, but someone turned it into one at or after the retry it answers
	watchUnread                      // GitHub didn't show its events, so the factory can't tell
	watchDraft                       // the latest answer watched it as the draft it still is
)

// convertToDraft is GitHub's event for a pull request someone turned into a
// draft.
const convertToDraft = "convert_to_draft"

// alreadyWatched says whether a writer's retry already watched pr, the latest
// build's failed draft, as that draft: the factory answered a retry of it
// with a post that says it watches it (#354), recording it as a draft, and
// it's still that draft (#371).
func (f *Factory) alreadyWatched(ctx context.Context, t Thread, pr github.PullRequest) (bool, error) {
	if !pr.Draft {
		return false, nil
	}
	w, err := f.watchOf(ctx, t, pr)
	return w == watchDraft, err
}

// watchOf is how the factory's answers watch pr, a draft. Only its latest
// answer to a retry that watches pr counts, which records whether pr was a
// draft or ready then, and when the writer made the retry (#371). Only that
// answer says so: a build's own post answers no command. It goes by the
// factory's answer, which stays, rather than the retry, which its writer may
// delete, or lose the right to give (#369's review). A draft the answer saw
// ready, someone turned into a draft since. One it saw as a draft is still
// that draft only if GitHub shows no one turned it into a draft at or after
// the retry: one made after the retry and before the factory answered it,
// as a person stopping the factory would while the watcher is busy or away,
// or after it was made ready since, by a person or by the factory as it
// began a merge that didn't finish, is a person's (#389's review). GitHub
// gives both times to the second, so a draft made in the retry's second
// counts as made after, as does one whose time doesn't read: a person
// decides. An answer from before answers recorded what they watched watches
// no draft, so a person decides that too. When GitHub doesn't show pr's
// events, it returns watchUnread and why. The core's model has one watch per
// build, so a second, after CI failed on the draft, is the protocol's alone.
func (f *Factory) watchOf(ctx context.Context, t Thread, pr github.PullRequest) (watchState, error) {
	answer, ok := latestWatch(t, pr.Number)
	if w, known := answerWatch(answer, ok); known {
		return w, nil
	}
	events, err := f.GitHub.Events(ctx, pr.Number)
	if err != nil {
		return watchUnread, err
	}
	return draftWatch(answer, events), nil
}

// answerWatch is how answer, the latest answer to a retry that watches a
// draft, if ok, watches it, when that's plain without GitHub's events.
func answerWatch(answer Post, ok bool) (watchState, bool) {
	switch {
	case !ok:
		return watchNone, true
	case answer.Marker.Watched == WatchedReady:
		return watchReady, true
	case answer.Marker.Watched != WatchedDraft:
		return watchUnknown, true
	}
	return watchDraft, false
}

// draftWatch is how answer, which saw a draft, watches it, as events, GitHub's
// for it, show.
func draftWatch(answer Post, events []github.Event) watchState {
	if draftedSince(events, answer.Marker.RetriedAt) {
		return watchRedrafted
	}
	return watchDraft
}

// draftedSince says whether GitHub's events show someone turned the pull
// request into a draft at or after since, a time as GitHub gives it, to the
// second. A draft made at a time that doesn't read counts, and when since
// doesn't read, every draft made counts.
func draftedSince(events []github.Event, since string) bool {
	from, err := time.Parse(time.RFC3339, since)
	for _, e := range events {
		if e.Event != convertToDraft {
			continue
		}
		if at, atErr := time.Parse(time.RFC3339, e.CreatedAt); err != nil || atErr != nil || !at.Before(from) {
			return true
		}
	}
	return false
}

// retriedDraft is what the factory makes of c, a writer's retry of pr, a
// draft, as it answers it (#371): whether its latest answer to a retry
// already watched pr as that draft, so this retry is a second watch, outside
// the core's model, and whether someone turned pr into a draft at or after
// c, as a person stopping the factory would, which the answer says, since
// the factory won't mark that draft ready (#389's review). A draft someone
// made since an answer that saw a draft, or since one that saw it ready, is
// no draft that answer watched, so a retry of it is the core's watch again,
// though in the model's terms it's a second watch of the build. When GitHub
// doesn't show pr's events, the factory says so in its log and takes it as
// no watch yet, and as no draft made since c: only the core's step and that
// sentence of the answer rest on them, and the factory reads them again
// before it marks pr ready.
func (f *Factory) retriedDraft(ctx context.Context, t Thread, pr github.PullRequest, c Command) (watched, stopped bool) {
	events, err := f.GitHub.Events(ctx, pr.Number)
	if err != nil {
		f.logf("#%d: GitHub didn't show #%d's events, so I'm answering the retry as a watch of the draft: %v", t.Issue.Number, pr.Number, err)
		return false, false
	}
	answer, ok := latestWatch(t, pr.Number)
	w, known := answerWatch(answer, ok)
	if !known {
		w = draftWatch(answer, events)
	}
	return w == watchDraft, draftedSince(events, c.At)
}

// latestWatch is the factory's latest answer to a writer's retry that
// watches pr, if it answered one: the pull request's post that answers a
// command.
func latestWatch(t Thread, pr int) (Post, bool) {
	for i := len(t.Posts) - 1; i >= 0; i-- {
		if p := t.Posts[i]; p.Marker.Kind == KindPR && p.Marker.PR == pr && len(p.Marker.ReplyTo) > 0 {
			return p, true
		}
	}
	return Post{}, false
}

// mergingPR is merging for pr, as GitHub has it: ready, a draft a writer's
// retry watches again as that draft, as watched says, or any other draft,
// such as one a person made of the factory's pull request to stop it,
// before any retry watched it or after a retry watched it ready, which no
// retry watches, and which the core never marks ready or reports on (#369's
// review, #371).
func mergingPR(watched bool, pr github.PullRequest, mergeable, merged bool) step {
	switch {
	case !pr.Draft || merged:
		return merging(mergeable, merged)
	case watched:
		return watchedMerging(mergeable)
	}
	return drafted(merging(mergeable, false))
}

// openedAs is the build's step once its pull request is opened, as GitHub
// has pr now: open, as a draft or ready, or closed or merged by someone
// else.
func openedAs(pr github.PullRequest) step {
	s := building(RunDone, true, true)
	s.draft = pr.Draft
	switch {
	case pr.Merged:
		s.closedAs = recovery.PRMerged
	case pr.State == "closed":
		s.closedAs = recovery.PRClosed
	}
	return s
}

// noting is a command that changes nothing, answered by a post.
func noting() step {
	return step{given: []int{recovery.Note}}
}

// replacing is a writer's retry of a plan's failed draft (#271): its build
// has been answered, and the retry starts a new one, which replaces the
// draft, pred, with intent its record that it does, stored or not.
func replacing(pred int, intent bool) step {
	return step{given: []int{recovery.Solve, recovery.Ratify, recovery.Retry}, posted: []int{recovery.Solve, recovery.Ratify},
		ratPushed: true, pred: pred, intent: intent}
}

// watcher is this watcher in the core's terms, in the agent run for key,
// or in none, which the build of attempt started. The watcher's attempt is
// never ahead of the step's: it's the step's own, or, for a run a later
// retry replaced, the one before (D-0212).
func watcher(inRun, attempt int) recovery.Watcher {
	w := recovery.NewWatcher(me)
	w.InRun, w.Attempt = inRun, attempt
	return w
}

// Each effect, as the core has it.

func (f *Factory) canStartRun(s step, k int) bool {
	_, _, ok := f.repo(s).StartRun(watcher(recovery.NoRun, s.attempt), k)
	return ok
}

func (f *Factory) canFinishRun(s step, k int) bool {
	_, _, ok := f.repo(s).FinishRun(watcher(k, s.attempt))
	return ok
}

// canFinishStaleRun says whether a watcher may record the run of a build
// that a later retry's build replaced, the attempt before s's latest: the
// core records it as its own, and nothing of the latest build changes.
func (f *Factory) canFinishStaleRun(s step) bool {
	if s.attempt == 0 {
		return false
	}
	r := f.repo(s)
	w := watcher(recovery.Build, s.attempt-1)
	t, _, ok := r.FinishRun(w)
	return ok && r.Stale(w) && t.Runs == r.Runs
}

func (f *Factory) canRecordIntent(s step) bool {
	_, ok := f.repo(s).RecordIntent(watcher(recovery.NoRun, s.attempt))
	return ok
}

func (f *Factory) canRetire(s step) bool {
	_, ok := f.repo(s).Retire(watcher(recovery.NoRun, s.attempt))
	return ok
}

func (f *Factory) canReportStopped(s step, k int) bool {
	_, ok := f.repo(s).ReportStopped(watcher(recovery.NoRun, s.attempt), k)
	return ok
}

func (f *Factory) canPushRatification(s step) bool {
	_, ok := f.repo(s).PushRatification(watcher(recovery.NoRun, s.attempt), recovery.Ratify)
	return ok
}

func (f *Factory) canPushCode(s step) bool {
	_, ok := f.repo(s).PushCode(watcher(recovery.NoRun, s.attempt), recovery.Build)
	return ok
}

// canOpenPullRequest says whether the build may open its pull request,
// ready or as a draft.
func (f *Factory) canOpenPullRequest(s step, ready bool) bool {
	_, ok := f.repo(s).OpenPullRequest(watcher(recovery.NoRun, s.attempt), recovery.Build, ready)
	return ok
}

// canMarkReady says whether the factory may mark a watched draft ready.
func (f *Factory) canMarkReady(s step) bool {
	_, ok := f.repo(s).MarkReady(watcher(recovery.NoRun, s.attempt))
	return ok
}

func (f *Factory) canMerge(s step) bool {
	_, ok := f.repo(s).MergePullRequest(watcher(recovery.NoRun, s.attempt), recovery.Merge)
	return ok
}

func (f *Factory) canPost(s step, k int) bool {
	_, ok := f.repo(s).Post(watcher(recovery.NoRun, s.attempt), k)
	return ok
}
