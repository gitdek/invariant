package factory

import (
	"context"
	"errors"
	"fmt"

	"github.com/gitdek/invariant/factory/recovery/recovery"
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
// close and its answer are the core's own steps, and a watcher still in a
// build's run that a later post superseded is the core's Stale run.

// errOutsideRecovery is what an effect the core doesn't allow fails with.
var errOutsideRecovery = errors.New("the factory's recovery rules don't allow this effect")

// me is this watcher in the core's terms, and another is any other.
const (
	me      = 0
	another = 1
)

// step is the step in hand, as every watcher can see it. A pull request the
// build opened is a ready one, as far as these steps are concerned. pred and
// intent are a retry's replacement: the draft it replaces, as a
// recovery.Pred value, and whether its record is stored.
type step struct {
	given     []int // the commands a writer gave for it: recovery.Solve, Ratify, Note or Retry
	posted    []int // the steps already answered
	runs      map[int]RunState
	ratPushed bool
	codePushed,
	prOpen,
	merged,
	gatePassed,
	mergeable bool
	pred   int
	intent bool
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
	case s.prOpen:
		r.PR = recovery.PRReady
	}
	r.GatePassed, r.Mergeable = s.gatePassed, s.mergeable
	r.Pred, r.Intent = s.pred, s.intent
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
// or in none.
func watcher(inRun int) recovery.Watcher {
	w := recovery.NewWatcher(me)
	w.InRun = inRun
	return w
}

// Each effect, as the core has it.

func (f *Factory) canStartRun(s step, k int) bool {
	_, _, ok := f.repo(s).StartRun(watcher(recovery.NoRun), k)
	return ok
}

func (f *Factory) canFinishRun(s step, k int) bool {
	_, _, ok := f.repo(s).FinishRun(watcher(k))
	return ok
}

func (f *Factory) canRecordIntent(s step) bool {
	_, ok := f.repo(s).RecordIntent(watcher(recovery.NoRun))
	return ok
}

func (f *Factory) canRetire(s step) bool {
	_, ok := f.repo(s).Retire(watcher(recovery.NoRun))
	return ok
}

func (f *Factory) canReportStopped(s step, k int) bool {
	_, ok := f.repo(s).ReportStopped(watcher(recovery.NoRun), k)
	return ok
}

func (f *Factory) canPushRatification(s step) bool {
	_, ok := f.repo(s).PushRatification(watcher(recovery.NoRun), recovery.Ratify)
	return ok
}

func (f *Factory) canPushCode(s step) bool {
	_, ok := f.repo(s).PushCode(watcher(recovery.NoRun), recovery.Build)
	return ok
}

// canOpenPullRequest says whether the build may open its pull request. The
// core allows a draft exactly when it allows a ready one.
func (f *Factory) canOpenPullRequest(s step) bool {
	_, ok := f.repo(s).OpenPullRequest(watcher(recovery.NoRun), recovery.Build, true)
	return ok
}

func (f *Factory) canMerge(s step) bool {
	_, ok := f.repo(s).MergePullRequest(watcher(recovery.NoRun), recovery.Merge)
	return ok
}

func (f *Factory) canPost(s step, k int) bool {
	_, ok := f.repo(s).Post(watcher(recovery.NoRun), k)
	return ok
}
