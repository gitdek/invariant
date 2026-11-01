package factory

import (
	"context"
	"errors"
	"fmt"

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
// ready one is ready.

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

// alreadyWatched says whether a writer's retry already watched pr, the latest
// build's failed draft, again: a post answering a retry says the factory
// watches it (#354). The core's model has one watch per build, so a second,
// after CI failed on the draft, is the protocol's alone.
func alreadyWatched(t Thread, pr int) bool {
	retries := map[int64]bool{}
	for _, c := range t.Commands {
		if c.Verb == Retry {
			retries[c.Comment] = true
		}
	}
	for _, p := range t.Posts {
		if p.Marker.Kind != KindPR || p.Marker.PR != pr {
			continue
		}
		for _, id := range p.Marker.ReplyTo {
			if retries[id] {
				return true
			}
		}
	}
	return false
}

// mergingPR is merging for pr, as GitHub has it: ready, a draft a writer's
// retry watches again, or any other draft, such as one a person made of the
// factory's pull request to stop it, which no retry watches, and which the
// core never marks ready or reports on (#369's review).
func mergingPR(t Thread, pr github.PullRequest, mergeable, merged bool) step {
	switch {
	case !pr.Draft || merged:
		return merging(mergeable, merged)
	case alreadyWatched(t, pr.Number):
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
