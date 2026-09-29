package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gitdek/invariant/factory/protocol/protocol"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/project"
)

// The factory runs on its own protocol, factory/protocol (D-0064). Its
// rules were ratified on #9 and written by the factory itself, as Go that
// Gobra proves against the model, step by step, and that the gate checks
// against TLC state for state. Before the factory takes a step on an issue,
// it describes the issue in the model's terms, as it is and as the step
// would leave it, and it takes the step only if the model has it. So a bug
// in the watcher's own checks can't make it take a step the ratified rules
// don't allow. It can't obey someone who can't direct it, ratify anything
// but the current proposal over the lock it amends, or merge anything but
// the ratified lock, in its one project, at a head that passed CI's gate.

// errOutsideProtocol is what a step the protocol doesn't have fails with.
var errOutsideProtocol = errors.New("the factory's protocol doesn't allow this step")

// view is an issue in the protocol's terms. Proposals and locks are known by
// their hashes and commits by their SHAs, with "" for none. The protocol only
// ever compares them, so a check renames them onto the model's few values.
type view struct {
	kind         int8 // protocol.Kind*
	open         int  // questions still open
	proposal     string
	amends       string // the lock the proposal amends
	base         string // the project's lock on the base branch
	ratified     string
	ratifiedBase string // the lock the ratified proposal amended
	head         string // the pull request's head commit
	gate         int8   // CI's gate on the head: protocol.Gate*
	prLock       string // the project's lock at the head
	scopeMany    bool   // the pull request changes more than its one project
	mergedBy     int8   // protocol.Nobody, ByFactory or ByOther
	mergedHead   string
	directedBy   int8   // who directed the latest step: protocol.NoActor or By*
	stops        int    // builds that stopped since a writer last said retry
	failure      string // why the latest failure happened: Fail*, or "" for none
}

// allows says whether the protocol has a step from one view of an issue to
// the next.
func allows(from, to view) bool {
	r := rename(from, to)
	a, b := r.state(from), r.state(to)
	for _, s := range protocol.Successors(a) {
		if s == b {
			return true
		}
	}
	return false
}

// renaming maps the hashes and commits of the views in one step onto the
// model's values: two proposals and two heads, besides none.
type renaming struct {
	locks map[string]int8
	heads map[string]int8
}

// rename keeps every comparison the protocol's steps make. The current
// proposal gets the first value and the ratified one, if it's different,
// the second, so ratifying and merging compare them exactly. The locks come
// next. A step compares at most two of them besides the proposal: ratifying
// compares the base branch's lock with the one the proposal amends, and
// merging compares the lock at the head with the ratified one. The pull
// request's head gets the first head value. A lock or head past the
// model's values shares the value for none, which only a step that doesn't
// compare it can see.
func rename(views ...view) renaming {
	r := renaming{locks: map[string]int8{"": protocol.NoP}, heads: map[string]int8{"": protocol.NoHead}}
	locks, heads := []int8{protocol.P1, protocol.P2}, []int8{protocol.H1, protocol.H2}
	lock := func(h string) {
		if _, ok := r.locks[h]; !ok {
			r.locks[h] = protocol.NoP
			if len(locks) > 0 {
				r.locks[h], locks = locks[0], locks[1:]
			}
		}
	}
	head := func(h string) {
		if _, ok := r.heads[h]; !ok {
			r.heads[h] = protocol.NoHead
			if len(heads) > 0 {
				r.heads[h], heads = heads[0], heads[1:]
			}
		}
	}
	for _, v := range views {
		lock(v.proposal)
	}
	for _, v := range views {
		lock(v.ratified)
	}
	for _, v := range views {
		lock(v.amends)
		lock(v.base)
	}
	for _, v := range views {
		lock(v.prLock)
		lock(v.ratifiedBase)
	}
	for _, v := range views {
		head(v.head)
	}
	for _, v := range views {
		head(v.mergedHead)
	}
	return r
}

func (r renaming) state(v view) protocol.State {
	s := protocol.State{
		Kind: v.kind, Proposal: r.locks[v.proposal], Amends: r.locks[v.amends], Base: r.locks[v.base],
		Ratified: r.locks[v.ratified], RatifiedBase: r.locks[v.ratifiedBase], Head: r.heads[v.head],
		PrLock: r.locks[v.prLock], MergedBy: v.mergedBy, MergedHead: r.heads[v.mergedHead], DirectedBy: v.directedBy,
	}
	s.Open[0], s.Open[1] = v.open >= 1, v.open >= 2
	if s.Head != protocol.NoHead {
		s.Gate[s.Head-1] = v.gate
	}
	if v.scopeMany {
		s.Scope = protocol.ScopeMany
	}
	s.Stops = int8(min(v.stops, 127))
	s.Failure = failureValue(v.failure)
	return s
}

// failureValue is why a failure happened, as the model says it.
func failureValue(why string) int8 {
	switch why {
	case FailStopped:
		return protocol.FailStopped
	case FailLimit:
		return protocol.FailLimit
	case FailGate:
		return protocol.FailGate
	case FailCI:
		return protocol.FailCI
	case FailUnmergeable, FailTrusted, FailMerge:
		return protocol.FailUnmergeable
	}
	return protocol.FailNone
}

// viewOf is an issue as its latest post left it, with the builds that have
// stopped since a writer last said retry. A thread the factory hasn't posted
// on has no post yet.
func viewOf(state Post, started bool, stops int) view {
	if !started {
		return view{kind: protocol.KindNone, stops: stops}
	}
	m := state.Marker
	v := view{stops: stops, failure: m.failure()}
	switch m.Kind {
	case KindForks:
		v.kind, v.open = protocol.KindAsked, len(m.Forks)
		return v
	case KindStuck:
		v.kind = protocol.KindStuck
		return v
	case KindUnsupported:
		v.kind = protocol.KindUnsupported
		return v
	case KindProposal:
		v.kind = protocol.KindProposed
	case KindRatified:
		v.kind = protocol.KindRatified
	case KindPR:
		v.kind = protocol.KindPROpen
	case KindFailed:
		v.kind = protocol.KindFailed
	case KindMerged:
		v.kind = protocol.KindMerged
	case KindClosed:
		v.kind = protocol.KindClosed
	}
	if p := m.Proposal; p != nil {
		v.proposal = p.Hash
		if p.Target != nil {
			v.amends = p.Target.Amends
		}
	}
	if m.Kind != KindProposal {
		v.ratified, v.ratifiedBase = m.Hash, v.amends
	}
	return v
}

// director is who a command's author is to the protocol: a person with
// write access directs the factory, and anyone else, or any bot, doesn't.
// Write access is what GitHub last said, so a step that runs across polls
// keeps what it read (D-0113).
func (f *Factory) director(login string) (actor int8) {
	f.mu.Lock()
	writes := f.writers[login]
	f.mu.Unlock()
	switch {
	case login == f.Self || strings.HasSuffix(login, "[bot]"):
		return protocol.ByInvbot
	case writes:
		return protocol.ByAlice
	}
	return protocol.ByMallory
}

// allowed checks a step the factory is about to take. A step outside the
// protocol means the watcher's own checks have a bug, so the factory takes
// nothing, and asks a person to look.
func (f *Factory) allowed(ctx context.Context, issue int, what string, from, to view) error {
	if allows(from, to) {
		return nil
	}
	f.logf("#%d: refused to %s: the protocol has no such step", issue, what)
	if err := f.status(ctx, issue, LabelHumanReview); err != nil {
		f.logf("#%d: %v", issue, err)
	}
	return fmt.Errorf("%w: %s", errOutsideProtocol, what)
}

// draftStep is posting a draft in answer to the command that asked for it.
// base is the project's lock on the base branch, which the draft amends.
func (f *Factory) draftStep(t Thread, c Command, base string, m Marker) (from, to view) {
	state, started := t.State()
	from = viewOf(state, started, t.Stops())
	from.base, from.directedBy = base, protocol.NoActor
	if c.Verb == Choose {
		// People answer questions one at a time, and the factory drafts
		// with the answer to the last one.
		from.open = 1
	}
	to = view{base: base, directedBy: f.director(c.By), stops: from.stops}
	switch m.Kind {
	case KindForks:
		to.kind, to.open = protocol.KindAsked, len(m.Forks)
	case KindProposal:
		to.kind, to.proposal = protocol.KindProposed, m.Proposal.Hash
		if m.Proposal.Target != nil {
			to.amends = m.Proposal.Target.Amends
		}
	case KindStuck:
		to.kind = protocol.KindStuck
	case KindUnsupported:
		to.kind = protocol.KindUnsupported
	}
	return from, to
}

// ratifyStep is ratifying the proposal a command names, while the base
// branch holds the lock base.
func (f *Factory) ratifyStep(t Thread, state Post, c Command, named, base string) (from, to view) {
	from = viewOf(state, true, t.Stops())
	from.base = base
	to = from
	to.kind, to.ratified, to.ratifiedBase, to.directedBy = protocol.KindRatified, named, from.amends, f.director(c.By)
	return from, to
}

// pullRequest is an issue whose pull request is open, or has failed: its
// head, CI's gate there, the lock there, and whether it changes only its
// project.
type pullRequest struct {
	head      string
	gate      int8
	lock      string
	scopeMany bool
}

// prView is an issue as its latest post left it, with its pull request.
func prView(state Post, stops int, pr pullRequest) view {
	v := viewOf(state, true, stops)
	v.head, v.gate, v.prLock, v.scopeMany = pr.head, pr.gate, pr.lock, pr.scopeMany
	return v
}

// buildStep is opening the pull request that a build made, with stops the
// builds that stopped before it.
func buildStep(ratified Post, stops int, pr pullRequest) (from, to view) {
	from = viewOf(ratified, true, stops)
	to = prView(ratified, stops, pr)
	to.kind = protocol.KindPROpen
	return from, to
}

// prStep is the factory merging its pull request, or recording what became
// of it: to is the kind of post it would make, and by who merged it.
func prStep(t Thread, state Post, pr pullRequest, to, by int8) (view, view) {
	from := prView(state, t.Stops(), pr)
	next := from
	next.kind, next.failure = to, ""
	if to == protocol.KindMerged {
		next.mergedBy, next.mergedHead, next.base = by, pr.head, pr.lock
	}
	return from, next
}

// headLock is the lock at a pull request's head as the protocol sees it:
// the proposal it records as ratified, when its statements hash to that.
func headLock(b []byte, err error) string {
	var lock project.Lock
	if err != nil || json.Unmarshal(b, &lock) != nil || lock.Ratified == nil {
		return "no ratified lock"
	}
	h := project.ProposalHash(lock.Bounds, lock.Statements)
	if lock.Ratified.Proposal != h {
		return "a lock that hashes to " + h + ", not the proposal it records"
	}
	return h
}

// lockOn is the ProposalHash of a project's lock on the base branch, or ""
// when there's no project there.
func (f *Factory) lockOn(ctx context.Context, dir string) (string, error) {
	cur, err := f.current(ctx, dir)
	if err != nil || cur == nil {
		return "", err
	}
	return project.ProposalHash(cur.Lock.Bounds, cur.Lock.Statements), nil
}

// retryStep is trying again after a failure: looking again at a pull
// request that failed, or building a stopped build's proposal again, with
// its stops counted afresh.
func (f *Factory) retryStep(t Thread, c Command) (from, to view) {
	state, _ := t.State()
	from = viewOf(state, true, t.Stops())
	if state.Marker.PR != 0 {
		from.head = fmt.Sprintf("#%d", state.Marker.PR)
	}
	to = from
	to.kind, to.directedBy, to.failure = protocol.KindPROpen, f.director(c.By), ""
	if from.head == "" {
		to.kind, to.stops = protocol.KindRatified, 0
	}
	return from, to
}

// gateOf is CI's gate as a check run left it.
func gateOf(run github.CheckRun, done bool) int8 {
	switch {
	case !done:
		return protocol.GatePending
	case run.Conclusion == "success":
		return protocol.GatePass
	}
	return protocol.GateFail
}
