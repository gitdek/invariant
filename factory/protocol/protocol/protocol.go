// +gobra

// Package protocol implements the factory's issue protocol, as modeled in
// .invariant/specs/IssueProtocol.tla, for one issue with two questions, two
// proposals and two head commits.
package protocol

// Bounds of the model.
const (
	NActors    = 3
	NQuestions = 2
	NProposals = 2
	NHeads     = 2
	NLocks     = 3
	NDrafts    = 7
)

// Actors, as step parameters. Only alice is a director: invbot writes but is
// a bot, and mallory has no write access.
const (
	ActorAlice   = 0
	ActorMallory = 1
	ActorInvbot  = 2
)

// Values of DirectedBy: NoActor, or the actor's index plus one.
const (
	NoActor   = 0
	ByAlice   = 1
	ByMallory = 2
	ByInvbot  = 3
)

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

// Locks: no proposal, or one of the two proposals.
const (
	NoP = 0
	P1  = 1
	P2  = 2
)

// Head commits.
const (
	NoHead = 0
	H1     = 1
	H2     = 2
)

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

// State mirrors the spec's variables one to one. Open[i] is question f(i+1);
// Gate[i] is CI's result on head h(i+1).
type State struct {
	Kind         int8
	Open         [NQuestions]bool
	Proposal     int8
	Amends       int8
	Base         int8
	Ratified     int8
	RatifiedBase int8
	Head         int8
	Gate         [NHeads]int8
	PrLock       int8
	Scope        int8
	MergedBy     int8
	MergedHead   int8
	DirectedBy   int8
}

// Init mirrors the TLA+ Init.
// @ ensures t.Kind == KindNone && !t.Open[0] && !t.Open[1]
// @ ensures t.Proposal == NoP && t.Amends == NoP && t.Base == NoP
// @ ensures t.Ratified == NoP && t.RatifiedBase == NoP
// @ ensures t.Head == NoHead && t.Gate[0] == GateNone && t.Gate[1] == GateNone
// @ ensures t.PrLock == NoP && t.Scope == ScopeOne && t.MergedBy == Nobody && t.MergedHead == NoHead
// @ ensures t.DirectedBy == NoActor
func Init() (t State) {
	t = State{}
	return t
}

// draft mirrors the TLA+ Draft, except for directedBy. d chooses the outcome:
// 0, 1, 2 ask {f1}, {f2}, {f1, f2}; 3, 4 propose p1, p2; 5 is stuck; 6 is
// unsupported.
// @ requires 0 <= d && d < NDrafts
// @ ensures t.Head == NoHead && t.Gate[0] == GateNone && t.Gate[1] == GateNone
// @ ensures t.PrLock == NoP && t.Scope == ScopeOne && t.MergedBy == Nobody && t.MergedHead == NoHead
// @ ensures t.Ratified == NoP && t.RatifiedBase == NoP && t.Base == s.Base
// @ ensures d == 0 ==> t.Kind == KindAsked && t.Open[0] && !t.Open[1] && t.Proposal == NoP && t.Amends == NoP
// @ ensures d == 1 ==> t.Kind == KindAsked && !t.Open[0] && t.Open[1] && t.Proposal == NoP && t.Amends == NoP
// @ ensures d == 2 ==> t.Kind == KindAsked && t.Open[0] && t.Open[1] && t.Proposal == NoP && t.Amends == NoP
// @ ensures d == 3 ==> t.Kind == KindProposed && !t.Open[0] && !t.Open[1] && t.Proposal == P1 && t.Amends == s.Base
// @ ensures d == 4 ==> t.Kind == KindProposed && !t.Open[0] && !t.Open[1] && t.Proposal == P2 && t.Amends == s.Base
// @ ensures d == 5 ==> t.Kind == KindStuck && !t.Open[0] && !t.Open[1] && t.Proposal == NoP && t.Amends == NoP
// @ ensures d == 6 ==> t.Kind == KindUnsupported && !t.Open[0] && !t.Open[1] && t.Proposal == NoP && t.Amends == NoP
// @ ensures t.DirectedBy == s.DirectedBy
func draft(s State, d int) (t State) {
	t = s
	t.Head = NoHead
	t.Gate[0] = GateNone
	t.Gate[1] = GateNone
	t.PrLock = NoP
	t.Scope = ScopeOne
	t.MergedBy = Nobody
	t.MergedHead = NoHead
	t.Ratified = NoP
	t.RatifiedBase = NoP
	t.Open[0] = false
	t.Open[1] = false
	t.Proposal = NoP
	t.Amends = NoP
	if d == 0 {
		t.Kind = KindAsked
		t.Open[0] = true
	} else if d == 1 {
		t.Kind = KindAsked
		t.Open[1] = true
	} else if d == 2 {
		t.Kind = KindAsked
		t.Open[0] = true
		t.Open[1] = true
	} else if d == 3 {
		t.Kind = KindProposed
		t.Proposal = P1
		t.Amends = s.Base
	} else if d == 4 {
		t.Kind = KindProposed
		t.Proposal = P2
		t.Amends = s.Base
	} else if d == 5 {
		t.Kind = KindStuck
	} else {
		t.Kind = KindUnsupported
	}
	return t
}

// Solve mirrors the TLA+ Solve(a), with d choosing Draft's outcome.
// @ requires 0 <= a && a < NActors && a == ActorAlice
// @ requires 0 <= d && d < NDrafts
// @ requires s.Kind == KindNone || s.Kind == KindStuck || s.Kind == KindUnsupported || s.Kind == KindClosed
// @ ensures t.DirectedBy == ByAlice
// @ ensures t.Head == NoHead && t.Gate[0] == GateNone && t.Gate[1] == GateNone
// @ ensures t.PrLock == NoP && t.Scope == ScopeOne && t.MergedBy == Nobody && t.MergedHead == NoHead
// @ ensures t.Ratified == NoP && t.RatifiedBase == NoP && t.Base == s.Base
// @ ensures d == 0 ==> t.Kind == KindAsked && t.Open[0] && !t.Open[1] && t.Proposal == NoP && t.Amends == NoP
// @ ensures d == 1 ==> t.Kind == KindAsked && !t.Open[0] && t.Open[1] && t.Proposal == NoP && t.Amends == NoP
// @ ensures d == 2 ==> t.Kind == KindAsked && t.Open[0] && t.Open[1] && t.Proposal == NoP && t.Amends == NoP
// @ ensures d == 3 ==> t.Kind == KindProposed && !t.Open[0] && !t.Open[1] && t.Proposal == P1 && t.Amends == s.Base
// @ ensures d == 4 ==> t.Kind == KindProposed && !t.Open[0] && !t.Open[1] && t.Proposal == P2 && t.Amends == s.Base
// @ ensures d == 5 ==> t.Kind == KindStuck && !t.Open[0] && !t.Open[1] && t.Proposal == NoP && t.Amends == NoP
// @ ensures d == 6 ==> t.Kind == KindUnsupported && !t.Open[0] && !t.Open[1] && t.Proposal == NoP && t.Amends == NoP
func Solve(s State, a int, d int) (t State) {
	t = draft(s, d)
	t.DirectedBy = ByAlice
	return t
}

// Revise mirrors the TLA+ Revise(a), with d choosing Draft's outcome.
// @ requires 0 <= a && a < NActors && a == ActorAlice
// @ requires 0 <= d && d < NDrafts
// @ requires s.Kind == KindAsked || s.Kind == KindProposed || s.Kind == KindStuck || s.Kind == KindUnsupported || s.Kind == KindClosed
// @ ensures t.DirectedBy == ByAlice
// @ ensures t.Head == NoHead && t.Gate[0] == GateNone && t.Gate[1] == GateNone
// @ ensures t.PrLock == NoP && t.Scope == ScopeOne && t.MergedBy == Nobody && t.MergedHead == NoHead
// @ ensures t.Ratified == NoP && t.RatifiedBase == NoP && t.Base == s.Base
// @ ensures d == 0 ==> t.Kind == KindAsked && t.Open[0] && !t.Open[1] && t.Proposal == NoP && t.Amends == NoP
// @ ensures d == 1 ==> t.Kind == KindAsked && !t.Open[0] && t.Open[1] && t.Proposal == NoP && t.Amends == NoP
// @ ensures d == 2 ==> t.Kind == KindAsked && t.Open[0] && t.Open[1] && t.Proposal == NoP && t.Amends == NoP
// @ ensures d == 3 ==> t.Kind == KindProposed && !t.Open[0] && !t.Open[1] && t.Proposal == P1 && t.Amends == s.Base
// @ ensures d == 4 ==> t.Kind == KindProposed && !t.Open[0] && !t.Open[1] && t.Proposal == P2 && t.Amends == s.Base
// @ ensures d == 5 ==> t.Kind == KindStuck && !t.Open[0] && !t.Open[1] && t.Proposal == NoP && t.Amends == NoP
// @ ensures d == 6 ==> t.Kind == KindUnsupported && !t.Open[0] && !t.Open[1] && t.Proposal == NoP && t.Amends == NoP
func Revise(s State, a int, d int) (t State) {
	t = draft(s, d)
	t.DirectedBy = ByAlice
	return t
}

// Choose mirrors the TLA+ Choose(a, q). When q is the last open question, d
// chooses Draft's outcome; otherwise d is ignored.
// @ requires 0 <= a && a < NActors && a == ActorAlice
// @ requires 0 <= q && q < NQuestions
// @ requires 0 <= d && d < NDrafts
// @ requires s.Kind == KindAsked && s.Open[q]
// @ ensures t.DirectedBy == ByAlice
// @ ensures s.Open[1-q] ==> !t.Open[q] && t.Open[1-q]
// @ ensures s.Open[1-q] ==> t.Kind == s.Kind && t.Proposal == s.Proposal && t.Amends == s.Amends && t.Base == s.Base
// @ ensures s.Open[1-q] ==> t.Ratified == s.Ratified && t.RatifiedBase == s.RatifiedBase && t.Head == s.Head && t.Gate == s.Gate
// @ ensures s.Open[1-q] ==> t.PrLock == s.PrLock && t.Scope == s.Scope && t.MergedBy == s.MergedBy && t.MergedHead == s.MergedHead
// @ ensures !s.Open[1-q] ==> t.Head == NoHead && t.Gate[0] == GateNone && t.Gate[1] == GateNone
// @ ensures !s.Open[1-q] ==> t.PrLock == NoP && t.Scope == ScopeOne && t.MergedBy == Nobody && t.MergedHead == NoHead
// @ ensures !s.Open[1-q] ==> t.Ratified == NoP && t.RatifiedBase == NoP && t.Base == s.Base
// @ ensures !s.Open[1-q] && d == 0 ==> t.Kind == KindAsked && t.Open[0] && !t.Open[1] && t.Proposal == NoP && t.Amends == NoP
// @ ensures !s.Open[1-q] && d == 1 ==> t.Kind == KindAsked && !t.Open[0] && t.Open[1] && t.Proposal == NoP && t.Amends == NoP
// @ ensures !s.Open[1-q] && d == 2 ==> t.Kind == KindAsked && t.Open[0] && t.Open[1] && t.Proposal == NoP && t.Amends == NoP
// @ ensures !s.Open[1-q] && d == 3 ==> t.Kind == KindProposed && !t.Open[0] && !t.Open[1] && t.Proposal == P1 && t.Amends == s.Base
// @ ensures !s.Open[1-q] && d == 4 ==> t.Kind == KindProposed && !t.Open[0] && !t.Open[1] && t.Proposal == P2 && t.Amends == s.Base
// @ ensures !s.Open[1-q] && d == 5 ==> t.Kind == KindStuck && !t.Open[0] && !t.Open[1] && t.Proposal == NoP && t.Amends == NoP
// @ ensures !s.Open[1-q] && d == 6 ==> t.Kind == KindUnsupported && !t.Open[0] && !t.Open[1] && t.Proposal == NoP && t.Amends == NoP
func Choose(s State, a int, q int, d int) (t State) {
	if s.Open[1-q] {
		t = s
		t.Open[q] = false
	} else {
		t = draft(s, d)
	}
	t.DirectedBy = ByAlice
	return t
}

// Ratify mirrors the TLA+ Ratify(a, p), where p names proposal p(p+1).
// @ requires 0 <= a && a < NActors && a == ActorAlice
// @ requires 0 <= p && p < NProposals
// @ requires s.Kind == KindProposed && !s.Open[0] && !s.Open[1]
// @ requires (p == 0 && s.Proposal == P1) || (p == 1 && s.Proposal == P2)
// @ requires s.Base == s.Amends
// @ ensures t.Kind == KindRatified && t.Ratified == s.Proposal && t.RatifiedBase == s.Base && t.DirectedBy == ByAlice
// @ ensures t.Open == s.Open && t.Proposal == s.Proposal && t.Amends == s.Amends && t.Base == s.Base
// @ ensures t.Head == s.Head && t.Gate == s.Gate && t.PrLock == s.PrLock && t.Scope == s.Scope
// @ ensures t.MergedBy == s.MergedBy && t.MergedHead == s.MergedHead
func Ratify(s State, a int, p int) (t State) {
	t = s
	t.Kind = KindRatified
	t.Ratified = s.Proposal
	t.RatifiedBase = s.Base
	t.DirectedBy = ByAlice
	return t
}

// Build mirrors the TLA+ Build, with h choosing the head h(h+1).
// @ requires 0 <= h && h < NHeads
// @ requires s.Kind == KindRatified
// @ ensures t.Kind == KindPROpen
// @ ensures h == 0 ==> t.Head == H1
// @ ensures h == 1 ==> t.Head == H2
// @ ensures t.Gate[h] == GatePending && t.Gate[1-h] == s.Gate[1-h]
// @ ensures t.PrLock == s.Ratified && t.Scope == ScopeOne
// @ ensures t.Open == s.Open && t.Proposal == s.Proposal && t.Amends == s.Amends && t.Base == s.Base
// @ ensures t.Ratified == s.Ratified && t.RatifiedBase == s.RatifiedBase
// @ ensures t.MergedBy == s.MergedBy && t.MergedHead == s.MergedHead && t.DirectedBy == s.DirectedBy
func Build(s State, h int) (t State) {
	t = s
	t.Kind = KindPROpen
	if h == 0 {
		t.Head = H1
	} else {
		t.Head = H2
	}
	t.Gate[h] = GatePending
	t.PrLock = s.Ratified
	t.Scope = ScopeOne
	return t
}

// Push mirrors the TLA+ Push(h, l, s): head h(h+1), lock l (NoP, P1, P2) and
// scope sc (ScopeOne, ScopeMany).
// @ requires 0 <= h && h < NHeads
// @ requires 0 <= l && l < NLocks
// @ requires 0 <= sc && sc < 2
// @ requires s.Kind == KindPROpen || s.Kind == KindFailed
// @ requires (h == 0 ==> s.Head != H1) && (h == 1 ==> s.Head != H2)
// @ ensures h == 0 ==> t.Head == H1
// @ ensures h == 1 ==> t.Head == H2
// @ ensures t.Gate[h] == GatePending && t.Gate[1-h] == s.Gate[1-h]
// @ ensures l == 0 ==> t.PrLock == NoP
// @ ensures l == 1 ==> t.PrLock == P1
// @ ensures l == 2 ==> t.PrLock == P2
// @ ensures sc == 0 ==> t.Scope == ScopeOne
// @ ensures sc == 1 ==> t.Scope == ScopeMany
// @ ensures t.Kind == s.Kind && t.Open == s.Open && t.Proposal == s.Proposal && t.Amends == s.Amends && t.Base == s.Base
// @ ensures t.Ratified == s.Ratified && t.RatifiedBase == s.RatifiedBase
// @ ensures t.MergedBy == s.MergedBy && t.MergedHead == s.MergedHead && t.DirectedBy == s.DirectedBy
func Push(s State, h int, l int, sc int) (t State) {
	t = s
	if h == 0 {
		t.Head = H1
	} else {
		t.Head = H2
	}
	t.Gate[h] = GatePending
	if l == 0 {
		t.PrLock = NoP
	} else if l == 1 {
		t.PrLock = P1
	} else {
		t.PrLock = P2
	}
	if sc == 0 {
		t.Scope = ScopeOne
	} else {
		t.Scope = ScopeMany
	}
	return t
}

// CIGate mirrors the TLA+ CIGate, with r choosing the result: 0 passes, 1
// fails.
// @ requires 0 <= r && r < 2
// @ requires s.Kind == KindPROpen || s.Kind == KindFailed
// @ requires (s.Head == H1 && s.Gate[0] == GatePending) || (s.Head == H2 && s.Gate[1] == GatePending)
// @ ensures s.Head == H1 && r == 0 ==> t.Gate[0] == GatePass
// @ ensures s.Head == H1 && r == 1 ==> t.Gate[0] == GateFail
// @ ensures s.Head == H1 ==> t.Gate[1] == s.Gate[1]
// @ ensures s.Head == H2 && r == 0 ==> t.Gate[1] == GatePass
// @ ensures s.Head == H2 && r == 1 ==> t.Gate[1] == GateFail
// @ ensures s.Head == H2 ==> t.Gate[0] == s.Gate[0]
// @ ensures t.Kind == s.Kind && t.Open == s.Open && t.Proposal == s.Proposal && t.Amends == s.Amends && t.Base == s.Base
// @ ensures t.Ratified == s.Ratified && t.RatifiedBase == s.RatifiedBase && t.Head == s.Head
// @ ensures t.PrLock == s.PrLock && t.Scope == s.Scope
// @ ensures t.MergedBy == s.MergedBy && t.MergedHead == s.MergedHead && t.DirectedBy == s.DirectedBy
func CIGate(s State, r int) (t State) {
	t = s
	var v int8 = GatePass
	if r == 1 {
		v = GateFail
	}
	if s.Head == H1 {
		t.Gate[0] = v
	} else {
		t.Gate[1] = v
	}
	return t
}

// NoticeFail mirrors the TLA+ NoticeFail.
// @ requires s.Kind == KindPROpen
// @ requires (s.Head == H1 && s.Gate[0] == GateFail) || (s.Head == H2 && s.Gate[1] == GateFail)
// @ ensures t.Kind == KindFailed
// @ ensures t.Open == s.Open && t.Proposal == s.Proposal && t.Amends == s.Amends && t.Base == s.Base
// @ ensures t.Ratified == s.Ratified && t.RatifiedBase == s.RatifiedBase && t.Head == s.Head && t.Gate == s.Gate
// @ ensures t.PrLock == s.PrLock && t.Scope == s.Scope
// @ ensures t.MergedBy == s.MergedBy && t.MergedHead == s.MergedHead && t.DirectedBy == s.DirectedBy
func NoticeFail(s State) (t State) {
	t = s
	t.Kind = KindFailed
	return t
}

// Retry mirrors the TLA+ Retry(a).
// @ requires 0 <= a && a < NActors && a == ActorAlice
// @ requires s.Kind == KindFailed
// @ ensures t.Kind == KindPROpen && t.DirectedBy == ByAlice
// @ ensures t.Open == s.Open && t.Proposal == s.Proposal && t.Amends == s.Amends && t.Base == s.Base
// @ ensures t.Ratified == s.Ratified && t.RatifiedBase == s.RatifiedBase && t.Head == s.Head && t.Gate == s.Gate
// @ ensures t.PrLock == s.PrLock && t.Scope == s.Scope
// @ ensures t.MergedBy == s.MergedBy && t.MergedHead == s.MergedHead
func Retry(s State, a int) (t State) {
	t = s
	t.Kind = KindPROpen
	t.DirectedBy = ByAlice
	return t
}

// Merge mirrors the TLA+ Merge.
// @ requires s.Kind == KindPROpen
// @ requires (s.Head == H1 && s.Gate[0] == GatePass) || (s.Head == H2 && s.Gate[1] == GatePass)
// @ requires s.Scope == ScopeOne && s.PrLock == s.Ratified
// @ ensures t.Kind == KindMerged && t.MergedBy == ByFactory && t.MergedHead == s.Head && t.Base == s.PrLock
// @ ensures t.Open == s.Open && t.Proposal == s.Proposal && t.Amends == s.Amends
// @ ensures t.Ratified == s.Ratified && t.RatifiedBase == s.RatifiedBase && t.Head == s.Head && t.Gate == s.Gate
// @ ensures t.PrLock == s.PrLock && t.Scope == s.Scope && t.DirectedBy == s.DirectedBy
func Merge(s State) (t State) {
	t = s
	t.Kind = KindMerged
	t.MergedBy = ByFactory
	t.MergedHead = s.Head
	t.Base = s.PrLock
	return t
}

// OthersMerge mirrors the TLA+ OthersMerge.
// @ requires s.Kind == KindPROpen || s.Kind == KindFailed
// @ ensures t.Kind == KindMerged && t.MergedBy == ByOther && t.MergedHead == s.Head && t.Base == s.PrLock
// @ ensures t.Open == s.Open && t.Proposal == s.Proposal && t.Amends == s.Amends
// @ ensures t.Ratified == s.Ratified && t.RatifiedBase == s.RatifiedBase && t.Head == s.Head && t.Gate == s.Gate
// @ ensures t.PrLock == s.PrLock && t.Scope == s.Scope && t.DirectedBy == s.DirectedBy
func OthersMerge(s State) (t State) {
	t = s
	t.Kind = KindMerged
	t.MergedBy = ByOther
	t.MergedHead = s.Head
	t.Base = s.PrLock
	return t
}

// OthersClose mirrors the TLA+ OthersClose.
// @ requires s.Kind == KindPROpen || s.Kind == KindFailed
// @ ensures t.Kind == KindClosed
// @ ensures t.Open == s.Open && t.Proposal == s.Proposal && t.Amends == s.Amends && t.Base == s.Base
// @ ensures t.Ratified == s.Ratified && t.RatifiedBase == s.RatifiedBase && t.Head == s.Head && t.Gate == s.Gate
// @ ensures t.PrLock == s.PrLock && t.Scope == s.Scope
// @ ensures t.MergedBy == s.MergedBy && t.MergedHead == s.MergedHead && t.DirectedBy == s.DirectedBy
func OthersClose(s State) (t State) {
	t = s
	t.Kind = KindClosed
	return t
}

// BaseMoves mirrors the TLA+ BaseMoves(l), with l one of NoP, P1, P2.
// @ requires 0 <= l && l < NLocks
// @ requires (l == 0 ==> s.Base != NoP) && (l == 1 ==> s.Base != P1) && (l == 2 ==> s.Base != P2)
// @ ensures l == 0 ==> t.Base == NoP
// @ ensures l == 1 ==> t.Base == P1
// @ ensures l == 2 ==> t.Base == P2
// @ ensures t.Kind == s.Kind && t.Open == s.Open && t.Proposal == s.Proposal && t.Amends == s.Amends
// @ ensures t.Ratified == s.Ratified && t.RatifiedBase == s.RatifiedBase && t.Head == s.Head && t.Gate == s.Gate
// @ ensures t.PrLock == s.PrLock && t.Scope == s.Scope
// @ ensures t.MergedBy == s.MergedBy && t.MergedHead == s.MergedHead && t.DirectedBy == s.DirectedBy
func BaseMoves(s State, l int) (t State) {
	t = s
	if l == 0 {
		t.Base = NoP
	} else if l == 1 {
		t.Base = P1
	} else {
		t.Base = P2
	}
	return t
}

// Finished mirrors the TLA+ Finished: once merged, nothing changes.
// @ requires s.Kind == KindMerged
// @ ensures t.Kind == s.Kind && t.Open == s.Open && t.Proposal == s.Proposal && t.Amends == s.Amends && t.Base == s.Base
// @ ensures t.Ratified == s.Ratified && t.RatifiedBase == s.RatifiedBase && t.Head == s.Head && t.Gate == s.Gate
// @ ensures t.PrLock == s.PrLock && t.Scope == s.Scope
// @ ensures t.MergedBy == s.MergedBy && t.MergedHead == s.MergedHead && t.DirectedBy == s.DirectedBy
func Finished(s State) (t State) {
	t = s
	return t
}
