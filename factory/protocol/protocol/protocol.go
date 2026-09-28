// +gobra

// Package protocol implements the factory's issue protocol, as modeled in
// .invariant/specs/IssueProtocol.tla, for one issue. Every operation decides
// for itself whether it runs: where the model's action can't run, it returns
// false and changes nothing.
package protocol

// Kinds of the factory's latest post.
const (
	KindNone        = 0
	KindAsked       = 1
	KindProposed    = 2
	KindStuck       = 3
	KindUnsupported = 4
	KindRatified    = 5
	KindPROpen      = 6
	KindFailed      = 7
	KindMerged      = 8
	KindClosed      = 9
)

// NoActor is DirectedBy before anyone has directed the factory.
const NoActor = 0

// NoP is a lock that holds no proposal. Proposals are numbered from 1.
const NoP = 0

// NoHead is no head commit. Heads are numbered from 1.
const NoHead = 0

// CI's gate result on a head.
const (
	GateNone    = 0
	GatePending = 1
	GatePass    = 2
	GateFail    = 3
)

// Scope of the pull request.
const (
	ScopeOne  = 0
	ScopeMany = 1
)

// Who merged the pull request.
const (
	Nobody    = 0
	ByFactory = 1
	ByOther   = 2
)

// Why the issue's latest failure happened.
const (
	FailNone        = 0
	FailStopped     = 1
	FailLimit       = 2
	FailGate        = 3
	FailCI          = 4
	FailUnmergeable = 5
)

// MaxStops is how many builds of an issue may stop partway before the
// factory refuses another.
const MaxStops = 2

// Actor is someone who comments on the issue.
type Actor struct {
	ID     int
	Writer bool
	Bot    bool
}

// Director says whether a directs the factory: a person with write access,
// not a bot.
// @ decreases
// @ pure
func (a Actor) Director() bool {
	return a.Writer && !a.Bot
}

// Draft is a post the factory can make: questions, a proposal, stuck or
// unsupported. Open holds the questions it asks, NOpen how many, and P the
// proposal it makes.
type Draft struct {
	Kind  int
	Open  []bool
	NOpen int
	P     int
}

// Issue is what the factory knows of one issue. Open holds its open
// questions and NOpen how many. HeadGate is CI's result on the pull
// request's current head.
type Issue struct {
	Kind         int
	Open         []bool
	NOpen        int
	Proposal     int
	Amends       int
	Base         int
	Ratified     int
	RatifiedBase int
	Head         int
	HeadGate     int
	PrLock       int
	Scope        int
	MergedBy     int
	MergedHead   int
	DirectedBy   int
	Stops        int
	Failure      int
}

// NewIssue is an issue with no post yet, among the given number of questions.
// @ requires 0 <= questions
// @ ensures acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ ensures acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ ensures acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ ensures len(i.Open) == questions
// @ ensures forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> !i.Open[j]
// @ ensures i.Kind == KindNone && i.NOpen == 0 && i.Proposal == NoP && i.Amends == NoP && i.Base == NoP
// @ ensures i.Ratified == NoP && i.RatifiedBase == NoP && i.Head == NoHead && i.HeadGate == GateNone
// @ ensures i.PrLock == NoP && i.Scope == ScopeOne && i.MergedBy == Nobody && i.MergedHead == NoHead
// @ ensures i.DirectedBy == NoActor && i.Stops == 0 && i.Failure == FailNone
func NewIssue(questions int) (i *Issue) {
	i = &Issue{
		Kind:         KindNone,
		Open:         make([]bool, questions),
		NOpen:        0,
		Proposal:     NoP,
		Amends:       NoP,
		Base:         NoP,
		Ratified:     NoP,
		RatifiedBase: NoP,
		Head:         NoHead,
		HeadGate:     GateNone,
		PrLock:       NoP,
		Scope:        ScopeOne,
		MergedBy:     Nobody,
		MergedHead:   NoHead,
		DirectedBy:   NoActor,
		Stops:        0,
		Failure:      FailNone,
	}
	return i
}

// postDraft makes draft d the issue's latest post, against the base branch's
// current lock, and forgets any pull request. The issue takes d's questions.
// @ requires acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ requires acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ requires acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ requires forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ requires len(d.Open) == len(i.Open)
// @ requires forall j int :: { &d.Open[j] } 0 <= j && j < len(d.Open) ==> acc(&d.Open[j])
// @ ensures acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ ensures acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ ensures acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ ensures len(i.Open) == old(len(i.Open))
// @ ensures forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(d.Open[j])
// @ ensures i.Kind == d.Kind && i.NOpen == d.NOpen && i.Proposal == d.P
// @ ensures d.Kind == KindProposed ==> i.Amends == old(i.Base)
// @ ensures d.Kind != KindProposed ==> i.Amends == NoP
// @ ensures i.Ratified == NoP && i.RatifiedBase == NoP && i.Head == NoHead && i.HeadGate == GateNone
// @ ensures i.PrLock == NoP && i.Scope == ScopeOne && i.MergedBy == Nobody && i.MergedHead == NoHead
// @ ensures i.Failure == FailNone
// @ ensures i.Base == old(i.Base) && i.DirectedBy == old(i.DirectedBy) && i.Stops == old(i.Stops)
func (i *Issue) postDraft(d Draft) {
	i.Kind = d.Kind
	i.Open = d.Open
	i.NOpen = d.NOpen
	i.Proposal = d.P
	if d.Kind == KindProposed {
		i.Amends = i.Base
	} else {
		i.Amends = NoP
	}
	i.Ratified = NoP
	i.RatifiedBase = NoP
	i.Head = NoHead
	i.HeadGate = GateNone
	i.PrLock = NoP
	i.Scope = ScopeOne
	i.MergedBy = Nobody
	i.MergedHead = NoHead
	i.Failure = FailNone
}

// Solve takes the issue at a director's word and posts draft d. It runs when
// there's no post yet, the factory is stuck, the issue is unsupported, the
// pull request was closed, or a build stopped.
// @ requires acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ requires acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ requires acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ requires forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ requires len(d.Open) == len(i.Open)
// @ requires forall j int :: { &d.Open[j] } 0 <= j && j < len(d.Open) ==> acc(&d.Open[j])
// @ ensures acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ ensures acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ ensures acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ ensures len(i.Open) == old(len(i.Open))
// @ ensures forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures ok == (a.Director() && (old(i.Kind) == KindNone || old(i.Kind) == KindStuck || old(i.Kind) == KindUnsupported || old(i.Kind) == KindClosed || (old(i.Kind) == KindFailed && old(i.Head) == NoHead)))
// @ ensures !ok ==> forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(i.Open[j])
// @ ensures !ok ==> i.Kind == old(i.Kind) && i.NOpen == old(i.NOpen) && i.Proposal == old(i.Proposal) && i.Amends == old(i.Amends)
// @ ensures !ok ==> i.Ratified == old(i.Ratified) && i.RatifiedBase == old(i.RatifiedBase) && i.Head == old(i.Head) && i.HeadGate == old(i.HeadGate)
// @ ensures !ok ==> i.PrLock == old(i.PrLock) && i.Scope == old(i.Scope) && i.MergedBy == old(i.MergedBy) && i.MergedHead == old(i.MergedHead)
// @ ensures !ok ==> i.DirectedBy == old(i.DirectedBy) && i.Failure == old(i.Failure)
// @ ensures ok ==> forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(d.Open[j])
// @ ensures ok ==> i.Kind == d.Kind && i.NOpen == d.NOpen && i.Proposal == d.P && i.DirectedBy == a.ID
// @ ensures ok && d.Kind == KindProposed ==> i.Amends == old(i.Base)
// @ ensures ok && d.Kind != KindProposed ==> i.Amends == NoP
// @ ensures ok ==> i.Ratified == NoP && i.RatifiedBase == NoP && i.Head == NoHead && i.HeadGate == GateNone
// @ ensures ok ==> i.PrLock == NoP && i.Scope == ScopeOne && i.MergedBy == Nobody && i.MergedHead == NoHead
// @ ensures ok ==> i.Failure == FailNone
// @ ensures i.Base == old(i.Base) && i.Stops == old(i.Stops)
func (i *Issue) Solve(a Actor, d Draft) (ok bool) {
	if !a.Director() {
		return false
	}
	if !(i.Kind == KindNone || i.Kind == KindStuck || i.Kind == KindUnsupported || i.Kind == KindClosed || (i.Kind == KindFailed && i.Head == NoHead)) {
		return false
	}
	i.postDraft(d)
	i.DirectedBy = a.ID
	return true
}

// Revise posts draft d in place of the factory's latest post, at a
// director's word. It runs when the factory asked, proposed, got stuck or
// found the issue unsupported, the pull request was closed, or a build
// stopped.
// @ requires acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ requires acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ requires acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ requires forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ requires len(d.Open) == len(i.Open)
// @ requires forall j int :: { &d.Open[j] } 0 <= j && j < len(d.Open) ==> acc(&d.Open[j])
// @ ensures acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ ensures acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ ensures acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ ensures len(i.Open) == old(len(i.Open))
// @ ensures forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures ok == (a.Director() && (old(i.Kind) == KindAsked || old(i.Kind) == KindProposed || old(i.Kind) == KindStuck || old(i.Kind) == KindUnsupported || old(i.Kind) == KindClosed || (old(i.Kind) == KindFailed && old(i.Head) == NoHead)))
// @ ensures !ok ==> forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(i.Open[j])
// @ ensures !ok ==> i.Kind == old(i.Kind) && i.NOpen == old(i.NOpen) && i.Proposal == old(i.Proposal) && i.Amends == old(i.Amends)
// @ ensures !ok ==> i.Ratified == old(i.Ratified) && i.RatifiedBase == old(i.RatifiedBase) && i.Head == old(i.Head) && i.HeadGate == old(i.HeadGate)
// @ ensures !ok ==> i.PrLock == old(i.PrLock) && i.Scope == old(i.Scope) && i.MergedBy == old(i.MergedBy) && i.MergedHead == old(i.MergedHead)
// @ ensures !ok ==> i.DirectedBy == old(i.DirectedBy) && i.Failure == old(i.Failure)
// @ ensures ok ==> forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(d.Open[j])
// @ ensures ok ==> i.Kind == d.Kind && i.NOpen == d.NOpen && i.Proposal == d.P && i.DirectedBy == a.ID
// @ ensures ok && d.Kind == KindProposed ==> i.Amends == old(i.Base)
// @ ensures ok && d.Kind != KindProposed ==> i.Amends == NoP
// @ ensures ok ==> i.Ratified == NoP && i.RatifiedBase == NoP && i.Head == NoHead && i.HeadGate == GateNone
// @ ensures ok ==> i.PrLock == NoP && i.Scope == ScopeOne && i.MergedBy == Nobody && i.MergedHead == NoHead
// @ ensures ok ==> i.Failure == FailNone
// @ ensures i.Base == old(i.Base) && i.Stops == old(i.Stops)
func (i *Issue) Revise(a Actor, d Draft) (ok bool) {
	if !a.Director() {
		return false
	}
	if !(i.Kind == KindAsked || i.Kind == KindProposed || i.Kind == KindStuck || i.Kind == KindUnsupported || i.Kind == KindClosed || (i.Kind == KindFailed && i.Head == NoHead)) {
		return false
	}
	i.postDraft(d)
	i.DirectedBy = a.ID
	return true
}

// Choose answers open question q at a director's word. Answering the last
// open question posts draft d; otherwise d is unused.
// @ requires acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ requires acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ requires acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ requires forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ requires 0 <= q && q < len(i.Open)
// @ requires len(d.Open) == len(i.Open)
// @ requires forall j int :: { &d.Open[j] } 0 <= j && j < len(d.Open) ==> acc(&d.Open[j])
// @ ensures acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ ensures acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ ensures acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ ensures len(i.Open) == old(len(i.Open))
// @ ensures forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures ok == (a.Director() && old(i.Kind) == KindAsked && old(i.Open[q]))
// @ ensures !ok ==> forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(i.Open[j])
// @ ensures !ok ==> i.Kind == old(i.Kind) && i.NOpen == old(i.NOpen) && i.Proposal == old(i.Proposal) && i.Amends == old(i.Amends)
// @ ensures !ok ==> i.Ratified == old(i.Ratified) && i.RatifiedBase == old(i.RatifiedBase) && i.Head == old(i.Head) && i.HeadGate == old(i.HeadGate)
// @ ensures !ok ==> i.PrLock == old(i.PrLock) && i.Scope == old(i.Scope) && i.MergedBy == old(i.MergedBy) && i.MergedHead == old(i.MergedHead)
// @ ensures !ok ==> i.DirectedBy == old(i.DirectedBy) && i.Failure == old(i.Failure)
// @ ensures ok ==> i.DirectedBy == a.ID
// @ ensures ok && old(i.NOpen) > 1 ==> !i.Open[q] && i.NOpen == old(i.NOpen) - 1
// @ ensures ok && old(i.NOpen) > 1 ==> forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) && j != q ==> i.Open[j] == old(i.Open[j])
// @ ensures ok && old(i.NOpen) > 1 ==> i.Kind == old(i.Kind) && i.Proposal == old(i.Proposal) && i.Amends == old(i.Amends)
// @ ensures ok && old(i.NOpen) > 1 ==> i.Ratified == old(i.Ratified) && i.RatifiedBase == old(i.RatifiedBase) && i.Head == old(i.Head) && i.HeadGate == old(i.HeadGate)
// @ ensures ok && old(i.NOpen) > 1 ==> i.PrLock == old(i.PrLock) && i.Scope == old(i.Scope) && i.MergedBy == old(i.MergedBy) && i.MergedHead == old(i.MergedHead)
// @ ensures ok && old(i.NOpen) > 1 ==> i.Failure == old(i.Failure)
// @ ensures ok && old(i.NOpen) <= 1 ==> forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(d.Open[j])
// @ ensures ok && old(i.NOpen) <= 1 ==> i.Kind == d.Kind && i.NOpen == d.NOpen && i.Proposal == d.P
// @ ensures ok && old(i.NOpen) <= 1 && d.Kind == KindProposed ==> i.Amends == old(i.Base)
// @ ensures ok && old(i.NOpen) <= 1 && d.Kind != KindProposed ==> i.Amends == NoP
// @ ensures ok && old(i.NOpen) <= 1 ==> i.Ratified == NoP && i.RatifiedBase == NoP && i.Head == NoHead && i.HeadGate == GateNone
// @ ensures ok && old(i.NOpen) <= 1 ==> i.PrLock == NoP && i.Scope == ScopeOne && i.MergedBy == Nobody && i.MergedHead == NoHead
// @ ensures ok && old(i.NOpen) <= 1 ==> i.Failure == FailNone
// @ ensures i.Base == old(i.Base) && i.Stops == old(i.Stops)
func (i *Issue) Choose(a Actor, q int, d Draft) (ok bool) {
	if !a.Director() || i.Kind != KindAsked || !i.Open[q] {
		return false
	}
	if i.NOpen > 1 {
		i.Open[q] = false
		i.NOpen = i.NOpen - 1
	} else {
		i.postDraft(d)
	}
	i.DirectedBy = a.ID
	return true
}

// Ratify ratifies proposal p at a director's word. It runs only when p is
// the current proposal, no question is open, and the base branch still holds
// the lock p amends.
// @ requires acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ requires acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ requires acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ requires forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ ensures acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ ensures acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ ensures len(i.Open) == old(len(i.Open))
// @ ensures forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(i.Open[j])
// @ ensures ok == (a.Director() && old(i.Kind) == KindProposed && old(i.NOpen) == 0 && p == old(i.Proposal) && old(i.Base) == old(i.Amends))
// @ ensures ok ==> i.Kind == KindRatified && i.Ratified == p && i.RatifiedBase == old(i.Base) && i.DirectedBy == a.ID
// @ ensures !ok ==> i.Kind == old(i.Kind) && i.Ratified == old(i.Ratified) && i.RatifiedBase == old(i.RatifiedBase) && i.DirectedBy == old(i.DirectedBy)
// @ ensures i.NOpen == old(i.NOpen) && i.Proposal == old(i.Proposal) && i.Amends == old(i.Amends) && i.Base == old(i.Base)
// @ ensures i.Head == old(i.Head) && i.HeadGate == old(i.HeadGate) && i.PrLock == old(i.PrLock) && i.Scope == old(i.Scope)
// @ ensures i.MergedBy == old(i.MergedBy) && i.MergedHead == old(i.MergedHead) && i.Stops == old(i.Stops) && i.Failure == old(i.Failure)
func (i *Issue) Ratify(a Actor, p int) (ok bool) {
	if !a.Director() || i.Kind != KindProposed || i.NOpen != 0 || p != i.Proposal || i.Base != i.Amends {
		return false
	}
	i.Kind = KindRatified
	i.Ratified = p
	i.RatifiedBase = i.Base
	i.DirectedBy = a.ID
	return true
}

// Build builds the ratified proposal and opens a pull request at head h,
// unless builds have already stopped partway MaxStops times.
// @ requires acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ requires acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ requires acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ requires forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ requires h != NoHead
// @ ensures acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ ensures acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ ensures acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ ensures len(i.Open) == old(len(i.Open))
// @ ensures forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(i.Open[j])
// @ ensures ok == (old(i.Kind) == KindRatified && old(i.Stops) < MaxStops)
// @ ensures ok ==> i.Kind == KindPROpen && i.Head == h && i.HeadGate == GatePending && i.PrLock == old(i.Ratified) && i.Scope == ScopeOne
// @ ensures !ok ==> i.Kind == old(i.Kind) && i.Head == old(i.Head) && i.HeadGate == old(i.HeadGate) && i.PrLock == old(i.PrLock) && i.Scope == old(i.Scope)
// @ ensures i.NOpen == old(i.NOpen) && i.Proposal == old(i.Proposal) && i.Amends == old(i.Amends) && i.Base == old(i.Base)
// @ ensures i.Ratified == old(i.Ratified) && i.RatifiedBase == old(i.RatifiedBase) && i.MergedBy == old(i.MergedBy) && i.MergedHead == old(i.MergedHead)
// @ ensures i.DirectedBy == old(i.DirectedBy) && i.Stops == old(i.Stops) && i.Failure == old(i.Failure)
func (i *Issue) Build(h int) (ok bool) {
	if i.Kind != KindRatified || i.Stops >= MaxStops {
		return false
	}
	i.Kind = KindPROpen
	i.Head = h
	i.HeadGate = GatePending
	i.PrLock = i.Ratified
	i.Scope = ScopeOne
	return true
}

// StopBuild records a build that started and stopped before it made a pull
// request.
// @ requires acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ requires acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ requires acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ requires forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ ensures acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ ensures acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ ensures len(i.Open) == old(len(i.Open))
// @ ensures forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(i.Open[j])
// @ ensures ok == (old(i.Kind) == KindRatified && old(i.Stops) < MaxStops)
// @ ensures ok ==> i.Kind == KindFailed && i.Stops == old(i.Stops) + 1 && i.Failure == FailStopped
// @ ensures !ok ==> i.Kind == old(i.Kind) && i.Stops == old(i.Stops) && i.Failure == old(i.Failure)
// @ ensures i.NOpen == old(i.NOpen) && i.Proposal == old(i.Proposal) && i.Amends == old(i.Amends) && i.Base == old(i.Base)
// @ ensures i.Ratified == old(i.Ratified) && i.RatifiedBase == old(i.RatifiedBase) && i.Head == old(i.Head) && i.HeadGate == old(i.HeadGate)
// @ ensures i.PrLock == old(i.PrLock) && i.Scope == old(i.Scope) && i.MergedBy == old(i.MergedBy) && i.MergedHead == old(i.MergedHead)
// @ ensures i.DirectedBy == old(i.DirectedBy)
func (i *Issue) StopBuild() (ok bool) {
	if i.Kind != KindRatified || i.Stops >= MaxStops {
		return false
	}
	i.Kind = KindFailed
	i.Stops = i.Stops + 1
	i.Failure = FailStopped
	return true
}

// RefuseBuild refuses to start another build once MaxStops builds have
// stopped partway, and posts that the build failed.
// @ requires acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ requires acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ requires acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ requires forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ ensures acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ ensures acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ ensures len(i.Open) == old(len(i.Open))
// @ ensures forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(i.Open[j])
// @ ensures ok == (old(i.Kind) == KindRatified && old(i.Stops) == MaxStops)
// @ ensures ok ==> i.Kind == KindFailed && i.Failure == FailLimit
// @ ensures !ok ==> i.Kind == old(i.Kind) && i.Failure == old(i.Failure)
// @ ensures i.NOpen == old(i.NOpen) && i.Proposal == old(i.Proposal) && i.Amends == old(i.Amends) && i.Base == old(i.Base)
// @ ensures i.Ratified == old(i.Ratified) && i.RatifiedBase == old(i.RatifiedBase) && i.Head == old(i.Head) && i.HeadGate == old(i.HeadGate)
// @ ensures i.PrLock == old(i.PrLock) && i.Scope == old(i.Scope) && i.MergedBy == old(i.MergedBy) && i.MergedHead == old(i.MergedHead)
// @ ensures i.DirectedBy == old(i.DirectedBy) && i.Stops == old(i.Stops)
func (i *Issue) RefuseBuild() (ok bool) {
	if i.Kind != KindRatified || i.Stops != MaxStops {
		return false
	}
	i.Kind = KindFailed
	i.Failure = FailLimit
	return true
}

// BuildFailsGate records a build whose code failed the gate in the factory's
// own run: it opens a draft pull request at head h and posts that the build
// failed.
// @ requires acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ requires acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ requires acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ requires forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ requires h != NoHead
// @ ensures acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ ensures acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ ensures acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ ensures len(i.Open) == old(len(i.Open))
// @ ensures forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(i.Open[j])
// @ ensures ok == (old(i.Kind) == KindRatified && old(i.Stops) < MaxStops)
// @ ensures ok ==> i.Kind == KindFailed && i.Head == h && i.HeadGate == GatePending && i.PrLock == old(i.Ratified) && i.Scope == ScopeOne
// @ ensures ok ==> i.Failure == FailGate
// @ ensures !ok ==> i.Kind == old(i.Kind) && i.Head == old(i.Head) && i.HeadGate == old(i.HeadGate) && i.PrLock == old(i.PrLock) && i.Scope == old(i.Scope)
// @ ensures !ok ==> i.Failure == old(i.Failure)
// @ ensures i.NOpen == old(i.NOpen) && i.Proposal == old(i.Proposal) && i.Amends == old(i.Amends) && i.Base == old(i.Base)
// @ ensures i.Ratified == old(i.Ratified) && i.RatifiedBase == old(i.RatifiedBase) && i.MergedBy == old(i.MergedBy) && i.MergedHead == old(i.MergedHead)
// @ ensures i.DirectedBy == old(i.DirectedBy) && i.Stops == old(i.Stops)
func (i *Issue) BuildFailsGate(h int) (ok bool) {
	if i.Kind != KindRatified || i.Stops >= MaxStops {
		return false
	}
	i.Kind = KindFailed
	i.Head = h
	i.HeadGate = GatePending
	i.PrLock = i.Ratified
	i.Scope = ScopeOne
	i.Failure = FailGate
	return true
}

// Push records someone pushing head h to the pull request, with lock l and
// scope sc.
// @ requires acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ requires acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ requires acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ requires forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ requires h != NoHead
// @ requires sc == ScopeOne || sc == ScopeMany
// @ ensures acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ ensures acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ ensures acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ ensures len(i.Open) == old(len(i.Open))
// @ ensures forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(i.Open[j])
// @ ensures ok == ((old(i.Kind) == KindPROpen || old(i.Kind) == KindFailed) && old(i.Head) != NoHead && h != old(i.Head))
// @ ensures ok ==> i.Head == h && i.HeadGate == GatePending && i.PrLock == l && i.Scope == sc
// @ ensures !ok ==> i.Head == old(i.Head) && i.HeadGate == old(i.HeadGate) && i.PrLock == old(i.PrLock) && i.Scope == old(i.Scope)
// @ ensures i.Kind == old(i.Kind) && i.NOpen == old(i.NOpen) && i.Proposal == old(i.Proposal) && i.Amends == old(i.Amends) && i.Base == old(i.Base)
// @ ensures i.Ratified == old(i.Ratified) && i.RatifiedBase == old(i.RatifiedBase) && i.MergedBy == old(i.MergedBy) && i.MergedHead == old(i.MergedHead)
// @ ensures i.DirectedBy == old(i.DirectedBy) && i.Stops == old(i.Stops) && i.Failure == old(i.Failure)
func (i *Issue) Push(h int, l int, sc int) (ok bool) {
	if !(i.Kind == KindPROpen || i.Kind == KindFailed) || i.Head == NoHead || h == i.Head {
		return false
	}
	i.Head = h
	i.HeadGate = GatePending
	i.PrLock = l
	i.Scope = sc
	return true
}

// CIGate records CI's gate result r, GatePass or GateFail, on the pull
// request's current head.
// @ requires acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ requires acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ requires acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ requires forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ requires r == GatePass || r == GateFail
// @ ensures acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ ensures acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ ensures acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ ensures len(i.Open) == old(len(i.Open))
// @ ensures forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(i.Open[j])
// @ ensures ok == ((old(i.Kind) == KindPROpen || old(i.Kind) == KindFailed) && old(i.Head) != NoHead && old(i.HeadGate) == GatePending)
// @ ensures ok ==> i.HeadGate == r
// @ ensures !ok ==> i.HeadGate == old(i.HeadGate)
// @ ensures i.Kind == old(i.Kind) && i.NOpen == old(i.NOpen) && i.Proposal == old(i.Proposal) && i.Amends == old(i.Amends) && i.Base == old(i.Base)
// @ ensures i.Ratified == old(i.Ratified) && i.RatifiedBase == old(i.RatifiedBase) && i.Head == old(i.Head) && i.PrLock == old(i.PrLock) && i.Scope == old(i.Scope)
// @ ensures i.MergedBy == old(i.MergedBy) && i.MergedHead == old(i.MergedHead) && i.DirectedBy == old(i.DirectedBy)
// @ ensures i.Stops == old(i.Stops) && i.Failure == old(i.Failure)
func (i *Issue) CIGate(r int) (ok bool) {
	if !(i.Kind == KindPROpen || i.Kind == KindFailed) || i.Head == NoHead || i.HeadGate != GatePending {
		return false
	}
	i.HeadGate = r
	return true
}

// NoticeFail posts that the pull request failed CI's gate.
// @ requires acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ requires acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ requires acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ requires forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ ensures acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ ensures acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ ensures len(i.Open) == old(len(i.Open))
// @ ensures forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(i.Open[j])
// @ ensures ok == (old(i.Kind) == KindPROpen && old(i.HeadGate) == GateFail)
// @ ensures ok ==> i.Kind == KindFailed && i.Failure == FailCI
// @ ensures !ok ==> i.Kind == old(i.Kind) && i.Failure == old(i.Failure)
// @ ensures i.NOpen == old(i.NOpen) && i.Proposal == old(i.Proposal) && i.Amends == old(i.Amends) && i.Base == old(i.Base)
// @ ensures i.Ratified == old(i.Ratified) && i.RatifiedBase == old(i.RatifiedBase) && i.Head == old(i.Head) && i.HeadGate == old(i.HeadGate)
// @ ensures i.PrLock == old(i.PrLock) && i.Scope == old(i.Scope) && i.MergedBy == old(i.MergedBy) && i.MergedHead == old(i.MergedHead)
// @ ensures i.DirectedBy == old(i.DirectedBy) && i.Stops == old(i.Stops)
func (i *Issue) NoticeFail() (ok bool) {
	if i.Kind != KindPROpen || i.HeadGate != GateFail {
		return false
	}
	i.Kind = KindFailed
	i.Failure = FailCI
	return true
}

// NoticeUnmergeable posts that the pull request passed CI's gate but changes
// more than its one project or has a lock other than the ratified proposal.
// @ requires acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ requires acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ requires acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ requires forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ ensures acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ ensures acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ ensures len(i.Open) == old(len(i.Open))
// @ ensures forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(i.Open[j])
// @ ensures ok == (old(i.Kind) == KindPROpen && old(i.HeadGate) == GatePass && (old(i.Scope) != ScopeOne || old(i.PrLock) != old(i.Ratified)))
// @ ensures ok ==> i.Kind == KindFailed && i.Failure == FailUnmergeable
// @ ensures !ok ==> i.Kind == old(i.Kind) && i.Failure == old(i.Failure)
// @ ensures i.NOpen == old(i.NOpen) && i.Proposal == old(i.Proposal) && i.Amends == old(i.Amends) && i.Base == old(i.Base)
// @ ensures i.Ratified == old(i.Ratified) && i.RatifiedBase == old(i.RatifiedBase) && i.Head == old(i.Head) && i.HeadGate == old(i.HeadGate)
// @ ensures i.PrLock == old(i.PrLock) && i.Scope == old(i.Scope) && i.MergedBy == old(i.MergedBy) && i.MergedHead == old(i.MergedHead)
// @ ensures i.DirectedBy == old(i.DirectedBy) && i.Stops == old(i.Stops)
func (i *Issue) NoticeUnmergeable() (ok bool) {
	if i.Kind != KindPROpen || i.HeadGate != GatePass || (i.Scope == ScopeOne && i.PrLock == i.Ratified) {
		return false
	}
	i.Kind = KindFailed
	i.Failure = FailUnmergeable
	return true
}

// Retry reopens a failed pull request at a director's word, or builds a
// stopped build's ratified proposal again with the stop limit reset.
// @ requires acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ requires acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ requires acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ requires forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ ensures acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ ensures acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ ensures len(i.Open) == old(len(i.Open))
// @ ensures forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(i.Open[j])
// @ ensures ok == (a.Director() && old(i.Kind) == KindFailed)
// @ ensures ok && old(i.Head) == NoHead ==> i.Kind == KindRatified && i.Stops == 0
// @ ensures ok && old(i.Head) != NoHead ==> i.Kind == KindPROpen && i.Stops == old(i.Stops)
// @ ensures ok ==> i.Failure == FailNone && i.DirectedBy == a.ID
// @ ensures !ok ==> i.Kind == old(i.Kind) && i.Stops == old(i.Stops) && i.Failure == old(i.Failure) && i.DirectedBy == old(i.DirectedBy)
// @ ensures i.NOpen == old(i.NOpen) && i.Proposal == old(i.Proposal) && i.Amends == old(i.Amends) && i.Base == old(i.Base)
// @ ensures i.Ratified == old(i.Ratified) && i.RatifiedBase == old(i.RatifiedBase) && i.Head == old(i.Head) && i.HeadGate == old(i.HeadGate)
// @ ensures i.PrLock == old(i.PrLock) && i.Scope == old(i.Scope) && i.MergedBy == old(i.MergedBy) && i.MergedHead == old(i.MergedHead)
func (i *Issue) Retry(a Actor) (ok bool) {
	if !a.Director() || i.Kind != KindFailed {
		return false
	}
	if i.Head == NoHead {
		i.Kind = KindRatified
		i.Stops = 0
	} else {
		i.Kind = KindPROpen
	}
	i.Failure = FailNone
	i.DirectedBy = a.ID
	return true
}

// Merge merges the pull request, only when CI's gate passed on its current
// head, it changes only its one project, and its lock is the ratified
// proposal.
// @ requires acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ requires acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ requires acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ requires forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ ensures acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ ensures acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ ensures len(i.Open) == old(len(i.Open))
// @ ensures forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(i.Open[j])
// @ ensures ok == (old(i.Kind) == KindPROpen && old(i.HeadGate) == GatePass && old(i.Scope) == ScopeOne && old(i.PrLock) == old(i.Ratified))
// @ ensures ok ==> i.Kind == KindMerged && i.MergedBy == ByFactory && i.MergedHead == old(i.Head) && i.Base == old(i.PrLock)
// @ ensures !ok ==> i.Kind == old(i.Kind) && i.MergedBy == old(i.MergedBy) && i.MergedHead == old(i.MergedHead) && i.Base == old(i.Base)
// @ ensures i.NOpen == old(i.NOpen) && i.Proposal == old(i.Proposal) && i.Amends == old(i.Amends)
// @ ensures i.Ratified == old(i.Ratified) && i.RatifiedBase == old(i.RatifiedBase) && i.Head == old(i.Head) && i.HeadGate == old(i.HeadGate)
// @ ensures i.PrLock == old(i.PrLock) && i.Scope == old(i.Scope) && i.DirectedBy == old(i.DirectedBy)
// @ ensures i.Stops == old(i.Stops) && i.Failure == old(i.Failure)
func (i *Issue) Merge() (ok bool) {
	if i.Kind != KindPROpen || i.HeadGate != GatePass || i.Scope != ScopeOne || i.PrLock != i.Ratified {
		return false
	}
	i.Kind = KindMerged
	i.MergedBy = ByFactory
	i.MergedHead = i.Head
	i.Base = i.PrLock
	return true
}

// OthersMerge records someone else merging the pull request.
// @ requires acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ requires acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ requires acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ requires forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ ensures acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ ensures acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ ensures len(i.Open) == old(len(i.Open))
// @ ensures forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(i.Open[j])
// @ ensures ok == ((old(i.Kind) == KindPROpen || old(i.Kind) == KindFailed) && old(i.Head) != NoHead)
// @ ensures ok ==> i.Kind == KindMerged && i.MergedBy == ByOther && i.MergedHead == old(i.Head) && i.Base == old(i.PrLock) && i.Failure == FailNone
// @ ensures !ok ==> i.Kind == old(i.Kind) && i.MergedBy == old(i.MergedBy) && i.MergedHead == old(i.MergedHead) && i.Base == old(i.Base) && i.Failure == old(i.Failure)
// @ ensures i.NOpen == old(i.NOpen) && i.Proposal == old(i.Proposal) && i.Amends == old(i.Amends)
// @ ensures i.Ratified == old(i.Ratified) && i.RatifiedBase == old(i.RatifiedBase) && i.Head == old(i.Head) && i.HeadGate == old(i.HeadGate)
// @ ensures i.PrLock == old(i.PrLock) && i.Scope == old(i.Scope) && i.DirectedBy == old(i.DirectedBy) && i.Stops == old(i.Stops)
func (i *Issue) OthersMerge() (ok bool) {
	if !(i.Kind == KindPROpen || i.Kind == KindFailed) || i.Head == NoHead {
		return false
	}
	i.Failure = FailNone
	i.Kind = KindMerged
	i.MergedBy = ByOther
	i.MergedHead = i.Head
	i.Base = i.PrLock
	return true
}

// OthersClose records someone else closing the pull request.
// @ requires acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ requires acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ requires acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ requires forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ ensures acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ ensures acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ ensures len(i.Open) == old(len(i.Open))
// @ ensures forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(i.Open[j])
// @ ensures ok == ((old(i.Kind) == KindPROpen || old(i.Kind) == KindFailed) && old(i.Head) != NoHead)
// @ ensures ok ==> i.Kind == KindClosed && i.Failure == FailNone
// @ ensures !ok ==> i.Kind == old(i.Kind) && i.Failure == old(i.Failure)
// @ ensures i.NOpen == old(i.NOpen) && i.Proposal == old(i.Proposal) && i.Amends == old(i.Amends) && i.Base == old(i.Base)
// @ ensures i.Ratified == old(i.Ratified) && i.RatifiedBase == old(i.RatifiedBase) && i.Head == old(i.Head) && i.HeadGate == old(i.HeadGate)
// @ ensures i.PrLock == old(i.PrLock) && i.Scope == old(i.Scope) && i.MergedBy == old(i.MergedBy) && i.MergedHead == old(i.MergedHead)
// @ ensures i.DirectedBy == old(i.DirectedBy) && i.Stops == old(i.Stops)
func (i *Issue) OthersClose() (ok bool) {
	if !(i.Kind == KindPROpen || i.Kind == KindFailed) || i.Head == NoHead {
		return false
	}
	i.Failure = FailNone
	i.Kind = KindClosed
	return true
}

// BaseMoves records the base branch's lock changing elsewhere, to l.
// @ requires acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ requires acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ requires acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ requires forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ ensures acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ ensures acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ ensures len(i.Open) == old(len(i.Open))
// @ ensures forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(i.Open[j])
// @ ensures ok == (l != old(i.Base))
// @ ensures ok ==> i.Base == l
// @ ensures !ok ==> i.Base == old(i.Base)
// @ ensures i.Kind == old(i.Kind) && i.NOpen == old(i.NOpen) && i.Proposal == old(i.Proposal) && i.Amends == old(i.Amends)
// @ ensures i.Ratified == old(i.Ratified) && i.RatifiedBase == old(i.RatifiedBase) && i.Head == old(i.Head) && i.HeadGate == old(i.HeadGate)
// @ ensures i.PrLock == old(i.PrLock) && i.Scope == old(i.Scope) && i.MergedBy == old(i.MergedBy) && i.MergedHead == old(i.MergedHead)
// @ ensures i.DirectedBy == old(i.DirectedBy) && i.Stops == old(i.Stops) && i.Failure == old(i.Failure)
func (i *Issue) BaseMoves(l int) (ok bool) {
	if l == i.Base {
		return false
	}
	i.Base = l
	return true
}

// Finished holds once the pull request is merged: nothing changes.
// @ requires acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ requires acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ requires acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ requires forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures acc(&i.Kind) && acc(&i.Open) && acc(&i.NOpen) && acc(&i.Proposal) && acc(&i.Amends) && acc(&i.Base)
// @ ensures acc(&i.Ratified) && acc(&i.RatifiedBase) && acc(&i.Head) && acc(&i.HeadGate) && acc(&i.PrLock) && acc(&i.Scope)
// @ ensures acc(&i.MergedBy) && acc(&i.MergedHead) && acc(&i.DirectedBy) && acc(&i.Stops) && acc(&i.Failure)
// @ ensures len(i.Open) == old(len(i.Open))
// @ ensures forall j int :: { &i.Open[j] } 0 <= j && j < len(i.Open) ==> acc(&i.Open[j])
// @ ensures forall j int :: { i.Open[j] } 0 <= j && j < len(i.Open) ==> i.Open[j] == old(i.Open[j])
// @ ensures ok == (old(i.Kind) == KindMerged)
// @ ensures i.Kind == old(i.Kind) && i.NOpen == old(i.NOpen) && i.Proposal == old(i.Proposal) && i.Amends == old(i.Amends) && i.Base == old(i.Base)
// @ ensures i.Ratified == old(i.Ratified) && i.RatifiedBase == old(i.RatifiedBase) && i.Head == old(i.Head) && i.HeadGate == old(i.HeadGate)
// @ ensures i.PrLock == old(i.PrLock) && i.Scope == old(i.Scope) && i.MergedBy == old(i.MergedBy) && i.MergedHead == old(i.MergedHead)
// @ ensures i.DirectedBy == old(i.DirectedBy) && i.Stops == old(i.Stops) && i.Failure == old(i.Failure)
func (i *Issue) Finished() (ok bool) {
	return i.Kind == KindMerged
}
