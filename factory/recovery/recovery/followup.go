// +gobra

package recovery

// FollowIdentity belongs to a ratified-plan obligation, not to an attempt.
// Drafts, creation attempts, repairs, reopening and merging allocate separate
// operation identities while retaining this identity in every accepted snapshot.
type FollowIdentity struct{ Source, Branch, PR string }

// FollowEffect is a durable intent and its reconciliation state. Dispatch is
// recorded once; a lost reply leaves it pending instead of enabling a resend.
type FollowEffect struct {
	Intent     int
	Dispatched bool
	State      string // empty, pending, completed or cancelled
}

type FollowOperation struct {
	Kind                            string
	Base, Round                     int
	Raw, Result                     string
	ResultSnapshot                  int
	Run, Publication, Merge, Reopen FollowEffect
}

type FollowSnapshot struct {
	Identity          FollowIdentity
	Op                int
	Kind              string
	Head, Left, Round int
	Cause, Stage      string
}

// FollowControl is the durable Workflow cursor. Remote facts and effect counts
// are not stored here: callers supply observations, and the explorer keeps the
// environment and audit history. All transitions are fenced by FollowSession.
type FollowControl struct {
	Identity                           FollowIdentity
	Phase, Next                        string
	Op, N, Left, Round                 int
	Requested                          bool
	Consumed                           int
	Answer                             FollowEffect
	Owner, RunFence                    int
	KnownPR                            bool
	Head                               int
	CI                                 string
	CIHead                             int
	CanMerge                           bool
	MergeHead                          int
	MergeOutcome, Proof, Status, Cause string
}

// FollowObservation contains the result actually found in the durable run
// store or at GitHub, including closed and merged PRs. Authorization is never
// used in place of a merge observation.
type FollowObservation struct {
	PR                        string
	Head                      int
	CI                        string
	CIHead                    int
	CanMerge                  bool
	MergeHead                 int
	MergeResult               string
	PublicationRevision       int
	PublicationRefused        bool
	LostMerge, ExternalMerged bool
}

type FollowSession struct {
	ID, Lease, Fence int
	Up               bool
}

// FollowChange is prepared from a cursor and committed to the journal before
// an adapter performs its newly dispatched effect. An empty snapshot denotes
// a ledger-only transition. Effect identities are (Op, effect kind).
type FollowChange struct {
	Before    FollowControl
	Previous  FollowOperation
	Control   FollowControl
	Operation FollowOperation
	Snapshot  FollowSnapshot
	OK        bool
}

// @ ensures c.Identity == identity && c.Phase == "inactive" && c.Next == "draft"
// @ ensures c.Op == 0 && c.N == 0 && c.Left == 2 && c.Round == 0
// @ ensures !c.Requested && c.Consumed == 0 && c.Answer == (FollowEffect{})
// @ ensures c.Owner == NoOne && c.RunFence == 0 && !c.KnownPR && c.Head == 0
// @ ensures c.CI == "none" && c.CIHead == 0 && !c.CanMerge && c.MergeHead == 0
// @ ensures c.MergeOutcome == "none" && c.Proof == "none" && c.Status == "active" && c.Cause == "none"
func NewFollowControl(identity FollowIdentity) (c FollowControl) {
	c.Identity = identity
	c.Phase, c.Next = "inactive", "draft"
	c.Left, c.Owner = 2, NoOne
	c.CI, c.MergeOutcome, c.Proof, c.Status, c.Cause = "none", "none", "none", "active", "none"
	return c
}

// @ decreases
// @ pure
func (c FollowControl) Valid() bool {
	return 0 <= c.Op && c.Op <= MaxAttempt && 0 <= c.N && c.N <= MaxAttempt &&
		0 <= c.Left && c.Left <= 2 && 0 <= c.Round && c.Round < MaxAttempt
}

// @ decreases
// @ pure
func (c FollowControl) RepairUnresolved(o FollowOperation) bool {
	return c.Op > 0 && o.Kind == "repair" && o.ResultSnapshot == 0
}

// @ decreases
// @ pure
func (c FollowControl) Consumable(o FollowOperation) bool {
	return c.Requested && c.Consumed == 0 && !c.RepairUnresolved(o) &&
		(c.Phase == "hold" || c.Phase == "idle" || c.Phase == "watch" || c.Phase == "done")
}

// @ decreases
// @ pure
func (c FollowControl) Holding(w FollowSession) bool {
	return c.Phase != "inactive" && w.Up && w.ID == w.Lease
}

// Internal answer priority is Workflow's scheduling rule, shared by all
// effect operations. A renewal cannot overtake an unresolved repair.
// @ decreases
// @ pure
func (c FollowControl) WorkAllowed(o FollowOperation, w FollowSession) bool {
	return c.Holding(w) && !(c.Consumed > 0 && c.Answer.State != "completed") && !c.Consumable(o)
}

// @ decreases
// @ pure
func (c FollowControl) Snapshot(op int, kind string, head, left, round int, cause, stage string) FollowSnapshot {
	return FollowSnapshot{Identity: c.Identity, Op: op, Kind: kind, Head: head, Left: left, Round: round, Cause: cause, Stage: stage}
}

// @ decreases
// @ pure
func invalidRun(o FollowOperation, v FollowObservation) bool {
	return o.Raw == "success" && o.Kind == "repair" && (o.Base != v.Head || v.PR != "open")
}

// @ decreases
// @ pure
func publishRun(o FollowOperation, v FollowObservation) bool {
	return o.Raw == "success" && !invalidRun(o, v) && (o.Kind == "create" || o.Kind == "repair")
}

// @ decreases
// @ pure
func holdRun(c FollowControl, o FollowOperation, v FollowObservation) bool {
	return o.Kind == "repair" && !publishRun(o, v) && (c.Left == 0 || v.PR == "closed")
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsActivate(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.Phase == "inactive"
}

// Activate refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsActivate(o,v,w,capacity)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == "idle"
// @ ensures x.OK ==> x.Control.Next == c.Next
// @ ensures x.OK ==> x.Control.Op == c.Op
// @ ensures x.OK ==> x.Control.N == c.N
// @ ensures x.OK ==> x.Control.Left == c.Left
// @ ensures x.OK ==> x.Control.Round == c.Round
// @ ensures x.OK ==> x.Control.Requested == c.Requested
// @ ensures x.OK ==> x.Control.Consumed == c.Consumed
// @ ensures x.OK ==> x.Control.Answer == c.Answer
// @ ensures x.OK ==> x.Control.Owner == c.Owner
// @ ensures x.OK ==> x.Control.RunFence == c.RunFence
// @ ensures x.OK ==> x.Control.KnownPR == c.KnownPR
// @ ensures x.OK ==> x.Control.Head == c.Head
// @ ensures x.OK ==> x.Control.CI == c.CI
// @ ensures x.OK ==> x.Control.CIHead == c.CIHead
// @ ensures x.OK ==> x.Control.CanMerge == c.CanMerge
// @ ensures x.OK ==> x.Control.MergeHead == c.MergeHead
// @ ensures x.OK ==> x.Control.MergeOutcome == c.MergeOutcome
// @ ensures x.OK ==> x.Control.Proof == c.Proof
// @ ensures x.OK ==> x.Control.Status == c.Status
// @ ensures x.OK ==> x.Control.Cause == c.Cause
// @ ensures x.OK ==> x.Operation.Kind == (o.Kind)
// @ ensures x.OK ==> x.Operation.Base == (o.Base)
// @ ensures x.OK ==> x.Operation.Round == (o.Round)
// @ ensures x.OK ==> x.Operation.Raw == (o.Raw)
// @ ensures x.OK ==> x.Operation.Result == (o.Result)
// @ ensures x.OK ==> x.Operation.ResultSnapshot == (o.ResultSnapshot)
// @ ensures x.OK ==> x.Operation.Run == (o.Run)
// @ ensures x.OK ==> x.Operation.Publication == (o.Publication)
// @ ensures x.OK ==> x.Operation.Merge == (o.Merge)
// @ ensures x.OK ==> x.Operation.Reopen == (o.Reopen)
// @ ensures x.OK ==> x.Snapshot == (FollowSnapshot{})
func (c FollowControl) Activate(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsActivate(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	t.Phase = "idle"
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsAuthorize(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.Phase == "idle" && !(c.Requested && c.Consumed == 0) &&
		c.Op < capacity && c.Op < MaxAttempt && c.N < MaxAttempt && c.Status == "active" &&
		(c.Next != "repair" || (c.Left > 0 && v.PR == "open")) &&
		(c.Next != "merge" || (v.PR == "open" && c.Head == v.Head)) &&
		(c.Next != "reopen" || (c.Consumed > 0 && v.PR == "closed"))
}

// Authorize refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsAuthorize(o,v,w,capacity)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == (c.Next == "merge" ? "start-merge" : (c.Next == "reopen" ? "start-reopen" : "start-run"))
// @ ensures x.OK ==> x.Control.Next == c.Next
// @ ensures x.OK ==> x.Control.Op == c.Op + 1
// @ ensures x.OK ==> x.Control.N == c.N + 1
// @ ensures x.OK ==> x.Control.Left == (c.Next == "repair" ? c.Left - 1 : c.Left)
// @ ensures x.OK ==> x.Control.Round == c.Round
// @ ensures x.OK ==> x.Control.Requested == c.Requested
// @ ensures x.OK ==> x.Control.Consumed == c.Consumed
// @ ensures x.OK ==> x.Control.Answer == c.Answer
// @ ensures x.OK ==> x.Control.Owner == c.Owner
// @ ensures x.OK ==> x.Control.RunFence == c.RunFence
// @ ensures x.OK ==> x.Control.KnownPR == c.KnownPR
// @ ensures x.OK ==> x.Control.Head == c.Head
// @ ensures x.OK ==> x.Control.CI == c.CI
// @ ensures x.OK ==> x.Control.CIHead == c.CIHead
// @ ensures x.OK ==> x.Control.CanMerge == c.CanMerge
// @ ensures x.OK ==> x.Control.MergeHead == c.MergeHead
// @ ensures x.OK ==> x.Control.MergeOutcome == c.MergeOutcome
// @ ensures x.OK ==> x.Control.Proof == c.Proof
// @ ensures x.OK ==> x.Control.Status == c.Status
// @ ensures x.OK ==> x.Control.Cause == c.Cause
// @ ensures x.OK ==> x.Operation.Kind == (c.Next)
// @ ensures x.OK ==> x.Operation.Base == (c.Head)
// @ ensures x.OK ==> x.Operation.Round == (c.Round)
// @ ensures x.OK ==> x.Operation.Raw == ("none")
// @ ensures x.OK ==> x.Operation.Result == ("none")
// @ ensures x.OK ==> x.Operation.ResultSnapshot == (0)
// @ ensures x.OK ==> x.Operation.Run == (((c.Next != "merge" && c.Next != "reopen") ? FollowEffect{Intent: c.N+1, State: "pending"} : FollowEffect{}))
// @ ensures x.OK ==> x.Operation.Publication == (FollowEffect{})
// @ ensures x.OK ==> x.Operation.Merge == ((c.Next == "merge" ? FollowEffect{Intent: c.N+1, State: "pending"} : FollowEffect{}))
// @ ensures x.OK ==> x.Operation.Reopen == ((c.Next == "reopen" ? FollowEffect{Intent: c.N+1, State: "pending"} : FollowEffect{}))
// @ ensures x.OK ==> x.Snapshot == (c.Snapshot(c.Op+1, c.Next, c.Head, x.Control.Left, c.Round, c.Cause, "authorized"))
func (c FollowControl) Authorize(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsAuthorize(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	t.Op++
	t.N++
	if c.Next == "repair" {
		t.Left--
	}
	p = FollowOperation{Kind: c.Next, Base: c.Head, Round: c.Round, Raw: "none", Result: "none"}
	e := FollowEffect{Intent: t.N, State: "pending"}
	switch c.Next {
	case "merge":
		p.Merge = e
		t.Phase = "start-merge"
	case "reopen":
		p.Reopen = e
		t.Phase = "start-reopen"
	default:
		p.Run = e
		t.Phase = "start-run"
	}
	snap = c.Snapshot(t.Op, c.Next, c.Head, t.Left, c.Round, c.Cause, "authorized")
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsStart(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.Phase == "start-run" && (o.Kind != "repair" || (v.PR == "open" && o.Base == v.Head))
}

// Start refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsStart(o,v,w,capacity)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == "running"
// @ ensures x.OK ==> x.Control.Next == c.Next
// @ ensures x.OK ==> x.Control.Op == c.Op
// @ ensures x.OK ==> x.Control.N == c.N
// @ ensures x.OK ==> x.Control.Left == c.Left
// @ ensures x.OK ==> x.Control.Round == c.Round
// @ ensures x.OK ==> x.Control.Requested == c.Requested
// @ ensures x.OK ==> x.Control.Consumed == c.Consumed
// @ ensures x.OK ==> x.Control.Answer == c.Answer
// @ ensures x.OK ==> x.Control.Owner == w.ID
// @ ensures x.OK ==> x.Control.RunFence == w.Fence
// @ ensures x.OK ==> x.Control.KnownPR == c.KnownPR
// @ ensures x.OK ==> x.Control.Head == c.Head
// @ ensures x.OK ==> x.Control.CI == c.CI
// @ ensures x.OK ==> x.Control.CIHead == c.CIHead
// @ ensures x.OK ==> x.Control.CanMerge == c.CanMerge
// @ ensures x.OK ==> x.Control.MergeHead == c.MergeHead
// @ ensures x.OK ==> x.Control.MergeOutcome == c.MergeOutcome
// @ ensures x.OK ==> x.Control.Proof == c.Proof
// @ ensures x.OK ==> x.Control.Status == c.Status
// @ ensures x.OK ==> x.Control.Cause == c.Cause
// @ ensures x.OK ==> x.Operation.Kind == (o.Kind)
// @ ensures x.OK ==> x.Operation.Base == (o.Base)
// @ ensures x.OK ==> x.Operation.Round == (o.Round)
// @ ensures x.OK ==> x.Operation.Raw == (o.Raw)
// @ ensures x.OK ==> x.Operation.Result == (o.Result)
// @ ensures x.OK ==> x.Operation.ResultSnapshot == (o.ResultSnapshot)
// @ ensures x.OK ==> x.Operation.Run == (FollowEffect{Intent:o.Run.Intent, Dispatched:true, State:o.Run.State})
// @ ensures x.OK ==> x.Operation.Publication == (o.Publication)
// @ ensures x.OK ==> x.Operation.Merge == (o.Merge)
// @ ensures x.OK ==> x.Operation.Reopen == (o.Reopen)
// @ ensures x.OK ==> x.Snapshot == (FollowSnapshot{})
func (c FollowControl) Start(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsStart(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	p.Run.Dispatched = true
	t.Owner, t.RunFence, t.Phase = w.ID, w.Fence, "running"
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsFinish(o FollowOperation, v FollowObservation, w FollowSession, capacity int, result string) bool {
	return c.WorkAllowed(o, w) && c.Phase == "running" && c.Owner == w.ID && c.RunFence == w.Fence && (result == "success" || result == "failed" || result == "stopped" || result == "nochange")
}

// Finish refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsFinish(o,v,w,capacity,result)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == "run-result"
// @ ensures x.OK ==> x.Control.Next == c.Next
// @ ensures x.OK ==> x.Control.Op == c.Op
// @ ensures x.OK ==> x.Control.N == c.N
// @ ensures x.OK ==> x.Control.Left == c.Left
// @ ensures x.OK ==> x.Control.Round == c.Round
// @ ensures x.OK ==> x.Control.Requested == c.Requested
// @ ensures x.OK ==> x.Control.Consumed == c.Consumed
// @ ensures x.OK ==> x.Control.Answer == c.Answer
// @ ensures x.OK ==> x.Control.Owner == c.Owner
// @ ensures x.OK ==> x.Control.RunFence == c.RunFence
// @ ensures x.OK ==> x.Control.KnownPR == c.KnownPR
// @ ensures x.OK ==> x.Control.Head == c.Head
// @ ensures x.OK ==> x.Control.CI == c.CI
// @ ensures x.OK ==> x.Control.CIHead == c.CIHead
// @ ensures x.OK ==> x.Control.CanMerge == c.CanMerge
// @ ensures x.OK ==> x.Control.MergeHead == c.MergeHead
// @ ensures x.OK ==> x.Control.MergeOutcome == c.MergeOutcome
// @ ensures x.OK ==> x.Control.Proof == c.Proof
// @ ensures x.OK ==> x.Control.Status == c.Status
// @ ensures x.OK ==> x.Control.Cause == c.Cause
// @ ensures x.OK ==> x.Operation.Kind == (o.Kind)
// @ ensures x.OK ==> x.Operation.Base == (o.Base)
// @ ensures x.OK ==> x.Operation.Round == (o.Round)
// @ ensures x.OK ==> x.Operation.Raw == (result)
// @ ensures x.OK ==> x.Operation.Result == (o.Result)
// @ ensures x.OK ==> x.Operation.ResultSnapshot == (o.ResultSnapshot)
// @ ensures x.OK ==> x.Operation.Run == (o.Run)
// @ ensures x.OK ==> x.Operation.Publication == (o.Publication)
// @ ensures x.OK ==> x.Operation.Merge == (o.Merge)
// @ ensures x.OK ==> x.Operation.Reopen == (o.Reopen)
// @ ensures x.OK ==> x.Snapshot == (FollowSnapshot{})
func (c FollowControl) Finish(o FollowOperation, v FollowObservation, w FollowSession, capacity int, result string) (x FollowChange) {
	if !c.AllowsFinish(o, v, w, capacity, result) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	p.Raw = result
	t.Phase = "run-result"
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsRecoverStopped(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.Phase == "running" && (c.Owner != w.ID || c.RunFence != w.Fence)
}

// RecoverStopped refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsRecoverStopped(o,v,w,capacity)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == "run-result"
// @ ensures x.OK ==> x.Control.Next == c.Next
// @ ensures x.OK ==> x.Control.Op == c.Op
// @ ensures x.OK ==> x.Control.N == c.N
// @ ensures x.OK ==> x.Control.Left == c.Left
// @ ensures x.OK ==> x.Control.Round == c.Round
// @ ensures x.OK ==> x.Control.Requested == c.Requested
// @ ensures x.OK ==> x.Control.Consumed == c.Consumed
// @ ensures x.OK ==> x.Control.Answer == c.Answer
// @ ensures x.OK ==> x.Control.Owner == c.Owner
// @ ensures x.OK ==> x.Control.RunFence == c.RunFence
// @ ensures x.OK ==> x.Control.KnownPR == c.KnownPR
// @ ensures x.OK ==> x.Control.Head == c.Head
// @ ensures x.OK ==> x.Control.CI == c.CI
// @ ensures x.OK ==> x.Control.CIHead == c.CIHead
// @ ensures x.OK ==> x.Control.CanMerge == c.CanMerge
// @ ensures x.OK ==> x.Control.MergeHead == c.MergeHead
// @ ensures x.OK ==> x.Control.MergeOutcome == c.MergeOutcome
// @ ensures x.OK ==> x.Control.Proof == c.Proof
// @ ensures x.OK ==> x.Control.Status == c.Status
// @ ensures x.OK ==> x.Control.Cause == c.Cause
// @ ensures x.OK ==> x.Operation.Kind == (o.Kind)
// @ ensures x.OK ==> x.Operation.Base == (o.Base)
// @ ensures x.OK ==> x.Operation.Round == (o.Round)
// @ ensures x.OK ==> x.Operation.Raw == ("stopped")
// @ ensures x.OK ==> x.Operation.Result == (o.Result)
// @ ensures x.OK ==> x.Operation.ResultSnapshot == (o.ResultSnapshot)
// @ ensures x.OK ==> x.Operation.Run == (o.Run)
// @ ensures x.OK ==> x.Operation.Publication == (o.Publication)
// @ ensures x.OK ==> x.Operation.Merge == (o.Merge)
// @ ensures x.OK ==> x.Operation.Reopen == (o.Reopen)
// @ ensures x.OK ==> x.Snapshot == (FollowSnapshot{})
func (c FollowControl) RecoverStopped(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsRecoverStopped(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	p.Raw = "stopped"
	t.Phase = "run-result"
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsReconcileRun(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.Phase == "run-result" && c.N < MaxAttempt
}

// ReconcileRun refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsReconcileRun(o,v,w,capacity)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == (publishRun(o,v) ? "start-publication" : (holdRun(c,o,v) ? "hold" : "idle"))
// @ ensures x.OK ==> x.Control.Next == (o.Kind == "draft" && (invalidRun(o,v) ? "invalidated" : o.Raw) == "success" ? "create" : o.Kind)
// @ ensures x.OK ==> x.Control.Op == c.Op
// @ ensures x.OK ==> x.Control.N == c.N+1
// @ ensures x.OK ==> x.Control.Left == c.Left
// @ ensures x.OK ==> x.Control.Round == c.Round
// @ ensures x.OK ==> x.Control.Requested == c.Requested
// @ ensures x.OK ==> x.Control.Consumed == c.Consumed
// @ ensures x.OK ==> x.Control.Answer == c.Answer
// @ ensures x.OK ==> x.Control.Owner == NoOne
// @ ensures x.OK ==> x.Control.RunFence == 0
// @ ensures x.OK ==> x.Control.KnownPR == c.KnownPR
// @ ensures x.OK ==> x.Control.Head == (invalidRun(o,v) ? v.Head : c.Head)
// @ ensures x.OK ==> x.Control.CI == (invalidRun(o,v) ? "none" : c.CI)
// @ ensures x.OK ==> x.Control.CIHead == (invalidRun(o,v) ? 0 : c.CIHead)
// @ ensures x.OK ==> x.Control.CanMerge == (!invalidRun(o,v) && c.CanMerge)
// @ ensures x.OK ==> x.Control.MergeHead == (invalidRun(o,v) ? 0 : c.MergeHead)
// @ ensures x.OK ==> x.Control.MergeOutcome == c.MergeOutcome
// @ ensures x.OK ==> x.Control.Proof == c.Proof
// @ ensures x.OK ==> x.Control.Status == (holdRun(c,o,v) ? (v.PR == "closed" ? "closed" : "exhausted") : c.Status)
// @ ensures x.OK ==> x.Control.Cause == ((invalidRun(o,v) ? "invalidated" : o.Raw) == "success" ? c.Cause : (invalidRun(o,v) ? "invalidated" : o.Raw))
// @ ensures x.OK ==> x.Operation.Kind == (o.Kind)
// @ ensures x.OK ==> x.Operation.Base == (o.Base)
// @ ensures x.OK ==> x.Operation.Round == (o.Round)
// @ ensures x.OK ==> x.Operation.Raw == (o.Raw)
// @ ensures x.OK ==> x.Operation.Result == ((invalidRun(o,v) ? "invalidated" : o.Raw))
// @ ensures x.OK ==> x.Operation.ResultSnapshot == ((publishRun(o,v) ? 0 : c.N+1))
// @ ensures x.OK ==> x.Operation.Run == (FollowEffect{Intent:o.Run.Intent, Dispatched:o.Run.Dispatched, State:"completed"})
// @ ensures x.OK ==> x.Operation.Publication == ((publishRun(o,v) ? FollowEffect{Intent:c.N+1, State:"pending"} : o.Publication))
// @ ensures x.OK ==> x.Operation.Merge == (o.Merge)
// @ ensures x.OK ==> x.Operation.Reopen == (o.Reopen)
// @ ensures x.OK ==> x.Snapshot == (c.Snapshot(c.Op,o.Kind,(invalidRun(o,v) ? v.Head : c.Head),c.Left,c.Round,(invalidRun(o,v) ? "invalidated" : o.Raw),"run-outcome"))
func (c FollowControl) ReconcileRun(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsReconcileRun(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	result := o.Raw
	if invalidRun(o, v) {
		result = "invalidated"
		t.Head = v.Head
		t.CI = "none"
		t.CIHead = 0
		t.CanMerge = false
		t.MergeHead = 0
	}
	publish, hold := publishRun(o, v), holdRun(c, o, v)
	t.N++
	p.Result = result
	t.Owner, t.RunFence = NoOne, 0
	p.Run.State = "completed"
	if publish {
		p.Publication = FollowEffect{Intent: t.N, State: "pending"}
		p.ResultSnapshot = 0
		t.Phase = "start-publication"
	} else {
		p.ResultSnapshot = t.N
		t.Phase = "idle"
		if hold {
			t.Phase = "hold"
			t.Status = "exhausted"
			if v.PR == "closed" {
				t.Status = "closed"
			}
		}
	}
	if result != "success" {
		t.Cause = result
	}
	t.Next = o.Kind
	if o.Kind == "draft" && result == "success" {
		t.Next = "create"
	}
	snap = c.Snapshot(c.Op, o.Kind, t.Head, c.Left, c.Round, result, "run-outcome")
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsPublish(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.Phase == "start-publication" && (o.Kind != "create" || v.PR == "none") && (o.Kind != "repair" || (v.PR == "open" && o.Base == v.Head))
}

// Publish refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsPublish(o,v,w,capacity)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == "publication"
// @ ensures x.OK ==> x.Control.Next == c.Next
// @ ensures x.OK ==> x.Control.Op == c.Op
// @ ensures x.OK ==> x.Control.N == c.N
// @ ensures x.OK ==> x.Control.Left == c.Left
// @ ensures x.OK ==> x.Control.Round == c.Round
// @ ensures x.OK ==> x.Control.Requested == c.Requested
// @ ensures x.OK ==> x.Control.Consumed == c.Consumed
// @ ensures x.OK ==> x.Control.Answer == c.Answer
// @ ensures x.OK ==> x.Control.Owner == c.Owner
// @ ensures x.OK ==> x.Control.RunFence == c.RunFence
// @ ensures x.OK ==> x.Control.KnownPR == c.KnownPR
// @ ensures x.OK ==> x.Control.Head == c.Head
// @ ensures x.OK ==> x.Control.CI == c.CI
// @ ensures x.OK ==> x.Control.CIHead == c.CIHead
// @ ensures x.OK ==> x.Control.CanMerge == c.CanMerge
// @ ensures x.OK ==> x.Control.MergeHead == c.MergeHead
// @ ensures x.OK ==> x.Control.MergeOutcome == c.MergeOutcome
// @ ensures x.OK ==> x.Control.Proof == c.Proof
// @ ensures x.OK ==> x.Control.Status == c.Status
// @ ensures x.OK ==> x.Control.Cause == c.Cause
// @ ensures x.OK ==> x.Operation.Kind == (o.Kind)
// @ ensures x.OK ==> x.Operation.Base == (o.Base)
// @ ensures x.OK ==> x.Operation.Round == (o.Round)
// @ ensures x.OK ==> x.Operation.Raw == (o.Raw)
// @ ensures x.OK ==> x.Operation.Result == (o.Result)
// @ ensures x.OK ==> x.Operation.ResultSnapshot == (o.ResultSnapshot)
// @ ensures x.OK ==> x.Operation.Run == (o.Run)
// @ ensures x.OK ==> x.Operation.Publication == (FollowEffect{Intent:o.Publication.Intent, Dispatched:true, State:o.Publication.State})
// @ ensures x.OK ==> x.Operation.Merge == (o.Merge)
// @ ensures x.OK ==> x.Operation.Reopen == (o.Reopen)
// @ ensures x.OK ==> x.Snapshot == (FollowSnapshot{})
func (c FollowControl) Publish(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsPublish(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	p.Publication.Dispatched = true
	t.Phase = "publication"
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsPublicationRefusal(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.Phase == "start-publication" && o.Kind == "create"
}

// PublicationRefusal refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsPublicationRefusal(o,v,w,capacity)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == "publication"
// @ ensures x.OK ==> x.Control.Next == c.Next
// @ ensures x.OK ==> x.Control.Op == c.Op
// @ ensures x.OK ==> x.Control.N == c.N
// @ ensures x.OK ==> x.Control.Left == c.Left
// @ ensures x.OK ==> x.Control.Round == c.Round
// @ ensures x.OK ==> x.Control.Requested == c.Requested
// @ ensures x.OK ==> x.Control.Consumed == c.Consumed
// @ ensures x.OK ==> x.Control.Answer == c.Answer
// @ ensures x.OK ==> x.Control.Owner == c.Owner
// @ ensures x.OK ==> x.Control.RunFence == c.RunFence
// @ ensures x.OK ==> x.Control.KnownPR == c.KnownPR
// @ ensures x.OK ==> x.Control.Head == c.Head
// @ ensures x.OK ==> x.Control.CI == c.CI
// @ ensures x.OK ==> x.Control.CIHead == c.CIHead
// @ ensures x.OK ==> x.Control.CanMerge == c.CanMerge
// @ ensures x.OK ==> x.Control.MergeHead == c.MergeHead
// @ ensures x.OK ==> x.Control.MergeOutcome == c.MergeOutcome
// @ ensures x.OK ==> x.Control.Proof == c.Proof
// @ ensures x.OK ==> x.Control.Status == c.Status
// @ ensures x.OK ==> x.Control.Cause == c.Cause
// @ ensures x.OK ==> x.Operation.Kind == (o.Kind)
// @ ensures x.OK ==> x.Operation.Base == (o.Base)
// @ ensures x.OK ==> x.Operation.Round == (o.Round)
// @ ensures x.OK ==> x.Operation.Raw == (o.Raw)
// @ ensures x.OK ==> x.Operation.Result == (o.Result)
// @ ensures x.OK ==> x.Operation.ResultSnapshot == (o.ResultSnapshot)
// @ ensures x.OK ==> x.Operation.Run == (o.Run)
// @ ensures x.OK ==> x.Operation.Publication == (FollowEffect{Intent:o.Publication.Intent, Dispatched:true, State:o.Publication.State})
// @ ensures x.OK ==> x.Operation.Merge == (o.Merge)
// @ ensures x.OK ==> x.Operation.Reopen == (o.Reopen)
// @ ensures x.OK ==> x.Snapshot == (FollowSnapshot{})
func (c FollowControl) PublicationRefusal(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsPublicationRefusal(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	p.Publication.Dispatched = true
	t.Phase = "publication"
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsReconcileCreationRefusal(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.Phase == "publication" && v.PublicationRefused && c.N < MaxAttempt
}

// ReconcileCreationRefusal refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsReconcileCreationRefusal(o,v,w,capacity)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == "idle"
// @ ensures x.OK ==> x.Control.Next == "create"
// @ ensures x.OK ==> x.Control.Op == c.Op
// @ ensures x.OK ==> x.Control.N == c.N+1
// @ ensures x.OK ==> x.Control.Left == c.Left
// @ ensures x.OK ==> x.Control.Round == c.Round
// @ ensures x.OK ==> x.Control.Requested == c.Requested
// @ ensures x.OK ==> x.Control.Consumed == c.Consumed
// @ ensures x.OK ==> x.Control.Answer == c.Answer
// @ ensures x.OK ==> x.Control.Owner == c.Owner
// @ ensures x.OK ==> x.Control.RunFence == c.RunFence
// @ ensures x.OK ==> x.Control.KnownPR == c.KnownPR
// @ ensures x.OK ==> x.Control.Head == c.Head
// @ ensures x.OK ==> x.Control.CI == c.CI
// @ ensures x.OK ==> x.Control.CIHead == c.CIHead
// @ ensures x.OK ==> x.Control.CanMerge == c.CanMerge
// @ ensures x.OK ==> x.Control.MergeHead == c.MergeHead
// @ ensures x.OK ==> x.Control.MergeOutcome == c.MergeOutcome
// @ ensures x.OK ==> x.Control.Proof == c.Proof
// @ ensures x.OK ==> x.Control.Status == c.Status
// @ ensures x.OK ==> x.Control.Cause == "refused"
// @ ensures x.OK ==> x.Operation.Kind == (o.Kind)
// @ ensures x.OK ==> x.Operation.Base == (o.Base)
// @ ensures x.OK ==> x.Operation.Round == (o.Round)
// @ ensures x.OK ==> x.Operation.Raw == (o.Raw)
// @ ensures x.OK ==> x.Operation.Result == ("refused")
// @ ensures x.OK ==> x.Operation.ResultSnapshot == (c.N+1)
// @ ensures x.OK ==> x.Operation.Run == (o.Run)
// @ ensures x.OK ==> x.Operation.Publication == (FollowEffect{Intent:o.Publication.Intent, Dispatched:o.Publication.Dispatched, State:"completed"})
// @ ensures x.OK ==> x.Operation.Merge == (o.Merge)
// @ ensures x.OK ==> x.Operation.Reopen == (o.Reopen)
// @ ensures x.OK ==> x.Snapshot == (c.Snapshot(c.Op,o.Kind,c.Head,c.Left,c.Round,"refused","creation-outcome"))
func (c FollowControl) ReconcileCreationRefusal(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsReconcileCreationRefusal(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	t.N++
	p.Publication.State = "completed"
	p.ResultSnapshot = t.N
	p.Result, t.Cause, t.Phase, t.Next = "refused", "refused", "idle", "create"
	snap = c.Snapshot(c.Op, o.Kind, c.Head, c.Left, c.Round, "refused", "creation-outcome")
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsReconcilePublication(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.Phase == "publication" && v.PublicationRevision > 0 && c.N < MaxAttempt
}

// ReconcilePublication refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsReconcilePublication(o,v,w,capacity)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == (v.PR == "merged" ? "done" : (v.PR == "closed" ? "hold" : "watch"))
// @ ensures x.OK ==> x.Control.Next == c.Next
// @ ensures x.OK ==> x.Control.Op == c.Op
// @ ensures x.OK ==> x.Control.N == c.N+1
// @ ensures x.OK ==> x.Control.Left == c.Left
// @ ensures x.OK ==> x.Control.Round == c.Round
// @ ensures x.OK ==> x.Control.Requested == c.Requested
// @ ensures x.OK ==> x.Control.Consumed == c.Consumed
// @ ensures x.OK ==> x.Control.Answer == c.Answer
// @ ensures x.OK ==> x.Control.Owner == c.Owner
// @ ensures x.OK ==> x.Control.RunFence == c.RunFence
// @ ensures x.OK ==> x.Control.KnownPR == true
// @ ensures x.OK ==> x.Control.Head == v.Head
// @ ensures x.OK ==> x.Control.CI == "none"
// @ ensures x.OK ==> x.Control.CIHead == 0
// @ ensures x.OK ==> x.Control.CanMerge == false
// @ ensures x.OK ==> x.Control.MergeHead == 0
// @ ensures x.OK ==> x.Control.MergeOutcome == c.MergeOutcome
// @ ensures x.OK ==> x.Control.Proof == (v.PR == "merged" ? "observation" : c.Proof)
// @ ensures x.OK ==> x.Control.Status == (v.PR == "merged" ? "merged" : (v.PR == "closed" ? "closed" : "active"))
// @ ensures x.OK ==> x.Control.Cause == c.Cause
// @ ensures x.OK ==> x.Operation.Kind == (o.Kind)
// @ ensures x.OK ==> x.Operation.Base == (o.Base)
// @ ensures x.OK ==> x.Operation.Round == (o.Round)
// @ ensures x.OK ==> x.Operation.Raw == (o.Raw)
// @ ensures x.OK ==> x.Operation.Result == (o.Result)
// @ ensures x.OK ==> x.Operation.ResultSnapshot == (c.N+1)
// @ ensures x.OK ==> x.Operation.Run == (o.Run)
// @ ensures x.OK ==> x.Operation.Publication == (FollowEffect{Intent:o.Publication.Intent, Dispatched:o.Publication.Dispatched, State:"completed"})
// @ ensures x.OK ==> x.Operation.Merge == (o.Merge)
// @ ensures x.OK ==> x.Operation.Reopen == (o.Reopen)
// @ ensures x.OK ==> x.Snapshot == (c.Snapshot(c.Op,o.Kind,v.Head,c.Left,c.Round,c.Cause,"publication-outcome"))
func (c FollowControl) ReconcilePublication(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsReconcilePublication(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	t.N++
	t.KnownPR, t.Head = true, v.Head
	t.CI, t.CIHead, t.CanMerge, t.MergeHead = "none", 0, false, 0
	p.Publication.State = "completed"
	p.ResultSnapshot = t.N
	t.Phase, t.Status = "watch", "active"
	if v.PR == "closed" {
		t.Phase, t.Status = "hold", "closed"
	}
	if v.PR == "merged" {
		t.Phase, t.Status, t.Proof = "done", "merged", "observation"
	}
	snap = c.Snapshot(c.Op, o.Kind, v.Head, c.Left, c.Round, c.Cause, "publication-outcome")
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsReconcileSuperseded(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.N < MaxAttempt &&
		(c.Phase == "record-run" || c.Phase == "start-run" || c.Phase == "record-publication" || c.Phase == "start-publication" || c.Phase == "record-merge" || c.Phase == "start-merge") &&
		(o.Kind == "repair" || o.Kind == "merge") && (o.Base != v.Head || v.PR != "open")
}

// ReconcileSuperseded refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsReconcileSuperseded(o,v,w,capacity)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == (v.PR == "merged" ? "done" : (v.PR == "closed" || (o.Kind == "repair" && c.Left == 0) ? "hold" : (o.Kind == "merge" ? "watch" : "idle")))
// @ ensures x.OK ==> x.Control.Next == "repair"
// @ ensures x.OK ==> x.Control.Op == c.Op
// @ ensures x.OK ==> x.Control.N == c.N+1
// @ ensures x.OK ==> x.Control.Left == c.Left
// @ ensures x.OK ==> x.Control.Round == c.Round
// @ ensures x.OK ==> x.Control.Requested == c.Requested
// @ ensures x.OK ==> x.Control.Consumed == c.Consumed
// @ ensures x.OK ==> x.Control.Answer == c.Answer
// @ ensures x.OK ==> x.Control.Owner == c.Owner
// @ ensures x.OK ==> x.Control.RunFence == c.RunFence
// @ ensures x.OK ==> x.Control.KnownPR == c.KnownPR
// @ ensures x.OK ==> x.Control.Head == v.Head
// @ ensures x.OK ==> x.Control.CI == "none"
// @ ensures x.OK ==> x.Control.CIHead == 0
// @ ensures x.OK ==> x.Control.CanMerge == false
// @ ensures x.OK ==> x.Control.MergeHead == 0
// @ ensures x.OK ==> x.Control.MergeOutcome == c.MergeOutcome
// @ ensures x.OK ==> x.Control.Proof == (v.PR == "merged" ? "observation" : c.Proof)
// @ ensures x.OK ==> x.Control.Status == (v.PR == "merged" ? "merged" : (v.PR == "closed" ? "closed" : (o.Kind == "repair" && c.Left == 0 ? "exhausted" : c.Status)))
// @ ensures x.OK ==> x.Control.Cause == "invalidated"
// @ ensures x.OK ==> x.Operation.Kind == (o.Kind)
// @ ensures x.OK ==> x.Operation.Base == (o.Base)
// @ ensures x.OK ==> x.Operation.Round == (o.Round)
// @ ensures x.OK ==> x.Operation.Raw == (o.Raw)
// @ ensures x.OK ==> x.Operation.Result == ("invalidated")
// @ ensures x.OK ==> x.Operation.ResultSnapshot == (c.N+1)
// @ ensures x.OK ==> x.Operation.Run == ((o.Kind != "merge" && (c.Phase == "record-run" || c.Phase == "start-run") && o.Run.Intent > 0 ? FollowEffect{Intent:o.Run.Intent, Dispatched:o.Run.Dispatched, State:"cancelled"} : o.Run))
// @ ensures x.OK ==> x.Operation.Publication == ((o.Kind != "merge" && c.Phase != "record-run" && c.Phase != "start-run" && o.Publication.Intent > 0 ? FollowEffect{Intent:o.Publication.Intent, Dispatched:o.Publication.Dispatched, State:"cancelled"} : o.Publication))
// @ ensures x.OK ==> x.Operation.Merge == ((o.Kind == "merge" && o.Merge.Intent > 0 ? FollowEffect{Intent:o.Merge.Intent, Dispatched:o.Merge.Dispatched, State:"cancelled"} : o.Merge))
// @ ensures x.OK ==> x.Operation.Reopen == (o.Reopen)
// @ ensures x.OK ==> x.Snapshot == (c.Snapshot(c.Op,o.Kind,v.Head,c.Left,c.Round,"invalidated","superseded-outcome"))
func (c FollowControl) ReconcileSuperseded(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsReconcileSuperseded(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	t.N++
	p.Result, t.Cause = "invalidated", "invalidated"
	p.ResultSnapshot = t.N
	t.Head = v.Head
	t.CI, t.CIHead, t.CanMerge, t.MergeHead = "none", 0, false, 0
	if o.Kind == "merge" {
		if p.Merge.Intent > 0 {
			p.Merge.State = "cancelled"
		}
	} else if c.Phase == "record-run" || c.Phase == "start-run" {
		if p.Run.Intent > 0 {
			p.Run.State = "cancelled"
		}
	} else {
		if p.Publication.Intent > 0 {
			p.Publication.State = "cancelled"
		}
	}
	hold := v.PR == "closed" || (o.Kind == "repair" && c.Left == 0 && v.PR != "merged")
	t.Phase = "idle"
	if o.Kind == "merge" {
		t.Phase = "watch"
	}
	if hold {
		t.Phase, t.Status = "hold", "exhausted"
	}
	if v.PR == "closed" {
		t.Status = "closed"
	}
	if v.PR == "merged" {
		t.Phase, t.Status, t.Proof = "done", "merged", "observation"
	}
	t.Next = "repair"
	snap = c.Snapshot(c.Op, o.Kind, v.Head, c.Left, c.Round, "invalidated", "superseded-outcome")
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsObservePR(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.N < MaxAttempt &&
		(c.Phase == "watch" || c.Phase == "idle" || c.Phase == "hold" || c.Phase == "done") &&
		o.Run.State != "pending" && o.Publication.State != "pending" && o.Merge.State != "pending" && o.Reopen.State != "pending" &&
		c.Status != "merged" && !(c.Status == "closed" && v.PR == "closed") &&
		!(c.Phase == "idle" && c.Next == "reopen" && c.Consumed > 0) && (v.PR == "closed" || v.PR == "merged")
}

// ObservePR refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsObservePR(o,v,w,capacity)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == (v.PR == "merged" ? "done" : "hold")
// @ ensures x.OK ==> x.Control.Next == c.Next
// @ ensures x.OK ==> x.Control.Op == c.Op
// @ ensures x.OK ==> x.Control.N == c.N+1
// @ ensures x.OK ==> x.Control.Left == c.Left
// @ ensures x.OK ==> x.Control.Round == c.Round
// @ ensures x.OK ==> x.Control.Requested == c.Requested
// @ ensures x.OK ==> x.Control.Consumed == c.Consumed
// @ ensures x.OK ==> x.Control.Answer == c.Answer
// @ ensures x.OK ==> x.Control.Owner == c.Owner
// @ ensures x.OK ==> x.Control.RunFence == c.RunFence
// @ ensures x.OK ==> x.Control.KnownPR == c.KnownPR
// @ ensures x.OK ==> x.Control.Head == v.Head
// @ ensures x.OK ==> x.Control.CI == (c.Head != v.Head ? "none" : c.CI)
// @ ensures x.OK ==> x.Control.CIHead == (c.Head != v.Head ? 0 : c.CIHead)
// @ ensures x.OK ==> x.Control.CanMerge == (c.Head == v.Head && c.CanMerge)
// @ ensures x.OK ==> x.Control.MergeHead == (c.Head != v.Head ? 0 : c.MergeHead)
// @ ensures x.OK ==> x.Control.MergeOutcome == c.MergeOutcome
// @ ensures x.OK ==> x.Control.Proof == (v.PR == "merged" ? "observation" : c.Proof)
// @ ensures x.OK ==> x.Control.Status == (v.PR == "merged" ? "merged" : "closed")
// @ ensures x.OK ==> x.Control.Cause == c.Cause
// @ ensures x.OK ==> x.Operation.Kind == (o.Kind)
// @ ensures x.OK ==> x.Operation.Base == (o.Base)
// @ ensures x.OK ==> x.Operation.Round == (o.Round)
// @ ensures x.OK ==> x.Operation.Raw == (o.Raw)
// @ ensures x.OK ==> x.Operation.Result == (o.Result)
// @ ensures x.OK ==> x.Operation.ResultSnapshot == (o.ResultSnapshot)
// @ ensures x.OK ==> x.Operation.Run == (o.Run)
// @ ensures x.OK ==> x.Operation.Publication == (o.Publication)
// @ ensures x.OK ==> x.Operation.Merge == (o.Merge)
// @ ensures x.OK ==> x.Operation.Reopen == (o.Reopen)
// @ ensures x.OK ==> x.Snapshot == (c.Snapshot(c.Op,o.Kind,v.Head,c.Left,c.Round,c.Cause,"pr-observation"))
func (c FollowControl) ObservePR(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsObservePR(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	t.N++
	t.Head = v.Head
	if c.Head != v.Head {
		t.CI, t.CIHead, t.CanMerge, t.MergeHead = "none", 0, false, 0
	}
	t.Phase, t.Status = "hold", "closed"
	if v.PR == "merged" {
		t.Phase, t.Status, t.Proof = "done", "merged", "observation"
	}
	snap = c.Snapshot(c.Op, o.Kind, v.Head, c.Left, c.Round, c.Cause, "pr-observation")
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsObserveCI(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.N < MaxAttempt && c.Phase == "watch" && v.PR == "open" &&
		v.CI != "none" && v.CIHead == c.Head && v.MergeHead == c.Head
}

// ObserveCI refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsObserveCI(o,v,w,capacity)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == (v.CI != "fail" && !v.CanMerge ? "done" : (v.CI == "fail" && c.Left == 0 ? "hold" : "idle"))
// @ ensures x.OK ==> x.Control.Next == (v.CI == "fail" ? "repair" : "merge")
// @ ensures x.OK ==> x.Control.Op == c.Op
// @ ensures x.OK ==> x.Control.N == c.N+1
// @ ensures x.OK ==> x.Control.Left == c.Left
// @ ensures x.OK ==> x.Control.Round == c.Round
// @ ensures x.OK ==> x.Control.Requested == c.Requested
// @ ensures x.OK ==> x.Control.Consumed == c.Consumed
// @ ensures x.OK ==> x.Control.Answer == c.Answer
// @ ensures x.OK ==> x.Control.Owner == c.Owner
// @ ensures x.OK ==> x.Control.RunFence == c.RunFence
// @ ensures x.OK ==> x.Control.KnownPR == c.KnownPR
// @ ensures x.OK ==> x.Control.Head == c.Head
// @ ensures x.OK ==> x.Control.CI == v.CI
// @ ensures x.OK ==> x.Control.CIHead == c.Head
// @ ensures x.OK ==> x.Control.CanMerge == v.CanMerge
// @ ensures x.OK ==> x.Control.MergeHead == v.MergeHead
// @ ensures x.OK ==> x.Control.MergeOutcome == c.MergeOutcome
// @ ensures x.OK ==> x.Control.Proof == c.Proof
// @ ensures x.OK ==> x.Control.Status == (v.CI != "fail" && !v.CanMerge ? "unmergeable" : (v.CI == "fail" && c.Left == 0 ? "exhausted" : c.Status))
// @ ensures x.OK ==> x.Control.Cause == (v.CI == "fail" ? "ci-failed" : c.Cause)
// @ ensures x.OK ==> x.Operation.Kind == (o.Kind)
// @ ensures x.OK ==> x.Operation.Base == (o.Base)
// @ ensures x.OK ==> x.Operation.Round == (o.Round)
// @ ensures x.OK ==> x.Operation.Raw == (o.Raw)
// @ ensures x.OK ==> x.Operation.Result == (o.Result)
// @ ensures x.OK ==> x.Operation.ResultSnapshot == (o.ResultSnapshot)
// @ ensures x.OK ==> x.Operation.Run == (o.Run)
// @ ensures x.OK ==> x.Operation.Publication == (o.Publication)
// @ ensures x.OK ==> x.Operation.Merge == (o.Merge)
// @ ensures x.OK ==> x.Operation.Reopen == (o.Reopen)
// @ ensures x.OK ==> x.Snapshot == (c.Snapshot(c.Op,o.Kind,c.Head,c.Left,c.Round,x.Control.Cause,"ci-observation"))
func (c FollowControl) ObserveCI(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsObserveCI(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	t.N++
	t.CI, t.CIHead, t.CanMerge, t.MergeHead = v.CI, c.Head, v.CanMerge, v.MergeHead
	t.Phase, t.Next = "idle", "merge"
	if v.CI == "fail" {
		t.Next, t.Cause = "repair", "ci-failed"
		if c.Left == 0 {
			t.Phase, t.Status = "hold", "exhausted"
		}
	} else if !v.CanMerge {
		t.Phase, t.Status = "done", "unmergeable"
	}
	snap = c.Snapshot(c.Op, o.Kind, c.Head, c.Left, c.Round, t.Cause, "ci-observation")
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsObserveHead(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.N < MaxAttempt &&
		(c.Phase == "watch" || c.Phase == "idle" || c.Phase == "hold" || c.Phase == "done") &&
		c.Head != v.Head && (v.PR == "open" || v.PR == "closed")
}

// ObserveHead refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsObserveHead(o,v,w,capacity)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == (c.Phase == "idle" && c.Next == "merge" ? "watch" : c.Phase)
// @ ensures x.OK ==> x.Control.Next == c.Next
// @ ensures x.OK ==> x.Control.Op == c.Op
// @ ensures x.OK ==> x.Control.N == c.N+1
// @ ensures x.OK ==> x.Control.Left == c.Left
// @ ensures x.OK ==> x.Control.Round == c.Round
// @ ensures x.OK ==> x.Control.Requested == c.Requested
// @ ensures x.OK ==> x.Control.Consumed == c.Consumed
// @ ensures x.OK ==> x.Control.Answer == c.Answer
// @ ensures x.OK ==> x.Control.Owner == c.Owner
// @ ensures x.OK ==> x.Control.RunFence == c.RunFence
// @ ensures x.OK ==> x.Control.KnownPR == c.KnownPR
// @ ensures x.OK ==> x.Control.Head == v.Head
// @ ensures x.OK ==> x.Control.CI == "none"
// @ ensures x.OK ==> x.Control.CIHead == 0
// @ ensures x.OK ==> x.Control.CanMerge == false
// @ ensures x.OK ==> x.Control.MergeHead == 0
// @ ensures x.OK ==> x.Control.MergeOutcome == c.MergeOutcome
// @ ensures x.OK ==> x.Control.Proof == c.Proof
// @ ensures x.OK ==> x.Control.Status == c.Status
// @ ensures x.OK ==> x.Control.Cause == c.Cause
// @ ensures x.OK ==> x.Operation.Kind == (o.Kind)
// @ ensures x.OK ==> x.Operation.Base == (o.Base)
// @ ensures x.OK ==> x.Operation.Round == (o.Round)
// @ ensures x.OK ==> x.Operation.Raw == (o.Raw)
// @ ensures x.OK ==> x.Operation.Result == (o.Result)
// @ ensures x.OK ==> x.Operation.ResultSnapshot == (o.ResultSnapshot)
// @ ensures x.OK ==> x.Operation.Run == (o.Run)
// @ ensures x.OK ==> x.Operation.Publication == (o.Publication)
// @ ensures x.OK ==> x.Operation.Merge == (o.Merge)
// @ ensures x.OK ==> x.Operation.Reopen == (o.Reopen)
// @ ensures x.OK ==> x.Snapshot == (c.Snapshot(c.Op,o.Kind,v.Head,c.Left,c.Round,c.Cause,"head-observation"))
func (c FollowControl) ObserveHead(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsObserveHead(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	t.N++
	t.Head = v.Head
	t.CI, t.CIHead, t.CanMerge, t.MergeHead = "none", 0, false, 0
	if c.Phase == "idle" && c.Next == "merge" {
		t.Phase = "watch"
	}
	snap = c.Snapshot(c.Op, o.Kind, v.Head, c.Left, c.Round, c.Cause, "head-observation")
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsGiveRequest(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.Phase != "inactive" && !c.Requested && (c.Phase == "hold" || c.RepairUnresolved(o))
}

// GiveRequest refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsGiveRequest(o,v,w,capacity)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == c.Phase
// @ ensures x.OK ==> x.Control.Next == c.Next
// @ ensures x.OK ==> x.Control.Op == c.Op
// @ ensures x.OK ==> x.Control.N == c.N
// @ ensures x.OK ==> x.Control.Left == c.Left
// @ ensures x.OK ==> x.Control.Round == c.Round
// @ ensures x.OK ==> x.Control.Requested == true
// @ ensures x.OK ==> x.Control.Consumed == c.Consumed
// @ ensures x.OK ==> x.Control.Answer == c.Answer
// @ ensures x.OK ==> x.Control.Owner == c.Owner
// @ ensures x.OK ==> x.Control.RunFence == c.RunFence
// @ ensures x.OK ==> x.Control.KnownPR == c.KnownPR
// @ ensures x.OK ==> x.Control.Head == c.Head
// @ ensures x.OK ==> x.Control.CI == c.CI
// @ ensures x.OK ==> x.Control.CIHead == c.CIHead
// @ ensures x.OK ==> x.Control.CanMerge == c.CanMerge
// @ ensures x.OK ==> x.Control.MergeHead == c.MergeHead
// @ ensures x.OK ==> x.Control.MergeOutcome == c.MergeOutcome
// @ ensures x.OK ==> x.Control.Proof == c.Proof
// @ ensures x.OK ==> x.Control.Status == c.Status
// @ ensures x.OK ==> x.Control.Cause == c.Cause
// @ ensures x.OK ==> x.Operation.Kind == (o.Kind)
// @ ensures x.OK ==> x.Operation.Base == (o.Base)
// @ ensures x.OK ==> x.Operation.Round == (o.Round)
// @ ensures x.OK ==> x.Operation.Raw == (o.Raw)
// @ ensures x.OK ==> x.Operation.Result == (o.Result)
// @ ensures x.OK ==> x.Operation.ResultSnapshot == (o.ResultSnapshot)
// @ ensures x.OK ==> x.Operation.Run == (o.Run)
// @ ensures x.OK ==> x.Operation.Publication == (o.Publication)
// @ ensures x.OK ==> x.Operation.Merge == (o.Merge)
// @ ensures x.OK ==> x.Operation.Reopen == (o.Reopen)
// @ ensures x.OK ==> x.Snapshot == (FollowSnapshot{})
func (c FollowControl) GiveRequest(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsGiveRequest(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	t.Requested = true
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsConsumeRequest(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.Holding(w) && c.Consumable(o) && c.N < MaxAttempt
}

// ConsumeRequest refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsConsumeRequest(o,v,w,capacity)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == (v.PR == "closed" || c.Status == "exhausted" ? "idle" : c.Phase)
// @ ensures x.OK ==> x.Control.Next == (v.PR == "closed" ? "reopen" : (c.Status == "exhausted" ? "repair" : c.Next))
// @ ensures x.OK ==> x.Control.Op == c.Op
// @ ensures x.OK ==> x.Control.N == c.N+1
// @ ensures x.OK ==> x.Control.Left == 2
// @ ensures x.OK ==> x.Control.Round == 1
// @ ensures x.OK ==> x.Control.Requested == c.Requested
// @ ensures x.OK ==> x.Control.Consumed == c.N+1
// @ ensures x.OK ==> x.Control.Answer == c.Answer
// @ ensures x.OK ==> x.Control.Owner == c.Owner
// @ ensures x.OK ==> x.Control.RunFence == c.RunFence
// @ ensures x.OK ==> x.Control.KnownPR == c.KnownPR
// @ ensures x.OK ==> x.Control.Head == c.Head
// @ ensures x.OK ==> x.Control.CI == c.CI
// @ ensures x.OK ==> x.Control.CIHead == c.CIHead
// @ ensures x.OK ==> x.Control.CanMerge == c.CanMerge
// @ ensures x.OK ==> x.Control.MergeHead == c.MergeHead
// @ ensures x.OK ==> x.Control.MergeOutcome == c.MergeOutcome
// @ ensures x.OK ==> x.Control.Proof == c.Proof
// @ ensures x.OK ==> x.Control.Status == (c.Phase == "done" && v.PR != "closed" ? c.Status : "active")
// @ ensures x.OK ==> x.Control.Cause == c.Cause
// @ ensures x.OK ==> x.Operation.Kind == (o.Kind)
// @ ensures x.OK ==> x.Operation.Base == (o.Base)
// @ ensures x.OK ==> x.Operation.Round == (o.Round)
// @ ensures x.OK ==> x.Operation.Raw == (o.Raw)
// @ ensures x.OK ==> x.Operation.Result == (o.Result)
// @ ensures x.OK ==> x.Operation.ResultSnapshot == (o.ResultSnapshot)
// @ ensures x.OK ==> x.Operation.Run == (o.Run)
// @ ensures x.OK ==> x.Operation.Publication == (o.Publication)
// @ ensures x.OK ==> x.Operation.Merge == (o.Merge)
// @ ensures x.OK ==> x.Operation.Reopen == (o.Reopen)
// @ ensures x.OK ==> x.Snapshot == (c.Snapshot(0,"none",c.Head,2,1,c.Cause,"writer-renewal"))
func (c FollowControl) ConsumeRequest(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsConsumeRequest(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	t.N++
	t.Consumed, t.Round, t.Left = t.N, 1, 2
	if v.PR == "closed" || c.Status == "exhausted" {
		t.Phase = "idle"
	}
	if v.PR == "closed" {
		t.Next = "reopen"
	} else if c.Status == "exhausted" {
		t.Next = "repair"
	}
	if c.Phase != "done" || v.PR == "closed" {
		t.Status = "active"
	}
	snap = c.Snapshot(0, "none", c.Head, 2, 1, c.Cause, "writer-renewal")
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsRecordAnswer(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.Holding(w) && c.Consumed > 0 && c.Answer.State != "completed" && c.Answer.Intent == 0
}

// RecordAnswer refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsRecordAnswer(o,v,w,capacity)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == c.Phase
// @ ensures x.OK ==> x.Control.Next == c.Next
// @ ensures x.OK ==> x.Control.Op == c.Op
// @ ensures x.OK ==> x.Control.N == c.N
// @ ensures x.OK ==> x.Control.Left == c.Left
// @ ensures x.OK ==> x.Control.Round == c.Round
// @ ensures x.OK ==> x.Control.Requested == c.Requested
// @ ensures x.OK ==> x.Control.Consumed == c.Consumed
// @ ensures x.OK ==> x.Control.Answer == FollowEffect{Intent:c.Consumed, State:"pending"}
// @ ensures x.OK ==> x.Control.Owner == c.Owner
// @ ensures x.OK ==> x.Control.RunFence == c.RunFence
// @ ensures x.OK ==> x.Control.KnownPR == c.KnownPR
// @ ensures x.OK ==> x.Control.Head == c.Head
// @ ensures x.OK ==> x.Control.CI == c.CI
// @ ensures x.OK ==> x.Control.CIHead == c.CIHead
// @ ensures x.OK ==> x.Control.CanMerge == c.CanMerge
// @ ensures x.OK ==> x.Control.MergeHead == c.MergeHead
// @ ensures x.OK ==> x.Control.MergeOutcome == c.MergeOutcome
// @ ensures x.OK ==> x.Control.Proof == c.Proof
// @ ensures x.OK ==> x.Control.Status == c.Status
// @ ensures x.OK ==> x.Control.Cause == c.Cause
// @ ensures x.OK ==> x.Operation.Kind == (o.Kind)
// @ ensures x.OK ==> x.Operation.Base == (o.Base)
// @ ensures x.OK ==> x.Operation.Round == (o.Round)
// @ ensures x.OK ==> x.Operation.Raw == (o.Raw)
// @ ensures x.OK ==> x.Operation.Result == (o.Result)
// @ ensures x.OK ==> x.Operation.ResultSnapshot == (o.ResultSnapshot)
// @ ensures x.OK ==> x.Operation.Run == (o.Run)
// @ ensures x.OK ==> x.Operation.Publication == (o.Publication)
// @ ensures x.OK ==> x.Operation.Merge == (o.Merge)
// @ ensures x.OK ==> x.Operation.Reopen == (o.Reopen)
// @ ensures x.OK ==> x.Snapshot == (FollowSnapshot{})
func (c FollowControl) RecordAnswer(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsRecordAnswer(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	t.Answer = FollowEffect{Intent: c.Consumed, State: "pending"}
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsAnswerRequest(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.Holding(w) && c.Consumed > 0 && c.Answer.State == "pending"
}

// AnswerRequest refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsAnswerRequest(o,v,w,capacity)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == c.Phase
// @ ensures x.OK ==> x.Control.Next == c.Next
// @ ensures x.OK ==> x.Control.Op == c.Op
// @ ensures x.OK ==> x.Control.N == c.N
// @ ensures x.OK ==> x.Control.Left == c.Left
// @ ensures x.OK ==> x.Control.Round == c.Round
// @ ensures x.OK ==> x.Control.Requested == c.Requested
// @ ensures x.OK ==> x.Control.Consumed == c.Consumed
// @ ensures x.OK ==> x.Control.Answer == FollowEffect{Intent:c.Answer.Intent, Dispatched:true, State:"completed"}
// @ ensures x.OK ==> x.Control.Owner == c.Owner
// @ ensures x.OK ==> x.Control.RunFence == c.RunFence
// @ ensures x.OK ==> x.Control.KnownPR == c.KnownPR
// @ ensures x.OK ==> x.Control.Head == c.Head
// @ ensures x.OK ==> x.Control.CI == c.CI
// @ ensures x.OK ==> x.Control.CIHead == c.CIHead
// @ ensures x.OK ==> x.Control.CanMerge == c.CanMerge
// @ ensures x.OK ==> x.Control.MergeHead == c.MergeHead
// @ ensures x.OK ==> x.Control.MergeOutcome == c.MergeOutcome
// @ ensures x.OK ==> x.Control.Proof == c.Proof
// @ ensures x.OK ==> x.Control.Status == c.Status
// @ ensures x.OK ==> x.Control.Cause == c.Cause
// @ ensures x.OK ==> x.Operation.Kind == (o.Kind)
// @ ensures x.OK ==> x.Operation.Base == (o.Base)
// @ ensures x.OK ==> x.Operation.Round == (o.Round)
// @ ensures x.OK ==> x.Operation.Raw == (o.Raw)
// @ ensures x.OK ==> x.Operation.Result == (o.Result)
// @ ensures x.OK ==> x.Operation.ResultSnapshot == (o.ResultSnapshot)
// @ ensures x.OK ==> x.Operation.Run == (o.Run)
// @ ensures x.OK ==> x.Operation.Publication == (o.Publication)
// @ ensures x.OK ==> x.Operation.Merge == (o.Merge)
// @ ensures x.OK ==> x.Operation.Reopen == (o.Reopen)
// @ ensures x.OK ==> x.Snapshot == (FollowSnapshot{})
func (c FollowControl) AnswerRequest(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsAnswerRequest(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	t.Answer.Dispatched, t.Answer.State = true, "completed"
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsReopen(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.Phase == "start-reopen" && v.PR == "closed" && c.Consumed > 0
}

// Reopen refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsReopen(o,v,w,capacity)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == "reopen-response"
// @ ensures x.OK ==> x.Control.Next == c.Next
// @ ensures x.OK ==> x.Control.Op == c.Op
// @ ensures x.OK ==> x.Control.N == c.N
// @ ensures x.OK ==> x.Control.Left == c.Left
// @ ensures x.OK ==> x.Control.Round == c.Round
// @ ensures x.OK ==> x.Control.Requested == c.Requested
// @ ensures x.OK ==> x.Control.Consumed == c.Consumed
// @ ensures x.OK ==> x.Control.Answer == c.Answer
// @ ensures x.OK ==> x.Control.Owner == c.Owner
// @ ensures x.OK ==> x.Control.RunFence == c.RunFence
// @ ensures x.OK ==> x.Control.KnownPR == c.KnownPR
// @ ensures x.OK ==> x.Control.Head == c.Head
// @ ensures x.OK ==> x.Control.CI == c.CI
// @ ensures x.OK ==> x.Control.CIHead == c.CIHead
// @ ensures x.OK ==> x.Control.CanMerge == c.CanMerge
// @ ensures x.OK ==> x.Control.MergeHead == c.MergeHead
// @ ensures x.OK ==> x.Control.MergeOutcome == c.MergeOutcome
// @ ensures x.OK ==> x.Control.Proof == c.Proof
// @ ensures x.OK ==> x.Control.Status == c.Status
// @ ensures x.OK ==> x.Control.Cause == c.Cause
// @ ensures x.OK ==> x.Operation.Kind == (o.Kind)
// @ ensures x.OK ==> x.Operation.Base == (o.Base)
// @ ensures x.OK ==> x.Operation.Round == (o.Round)
// @ ensures x.OK ==> x.Operation.Raw == (o.Raw)
// @ ensures x.OK ==> x.Operation.Result == (o.Result)
// @ ensures x.OK ==> x.Operation.ResultSnapshot == (o.ResultSnapshot)
// @ ensures x.OK ==> x.Operation.Run == (o.Run)
// @ ensures x.OK ==> x.Operation.Publication == (o.Publication)
// @ ensures x.OK ==> x.Operation.Merge == (o.Merge)
// @ ensures x.OK ==> x.Operation.Reopen == (FollowEffect{Intent:o.Reopen.Intent, Dispatched:true, State:o.Reopen.State})
// @ ensures x.OK ==> x.Snapshot == (FollowSnapshot{})
func (c FollowControl) Reopen(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsReopen(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	p.Reopen.Dispatched = true
	t.Phase = "reopen-response"
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsReconcileReopen(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.Phase == "reopen-response" && c.N < MaxAttempt
}

// ReconcileReopen refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsReconcileReopen(o,v,w,capacity)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == "watch"
// @ ensures x.OK ==> x.Control.Next == c.Next
// @ ensures x.OK ==> x.Control.Op == c.Op
// @ ensures x.OK ==> x.Control.N == c.N+1
// @ ensures x.OK ==> x.Control.Left == c.Left
// @ ensures x.OK ==> x.Control.Round == c.Round
// @ ensures x.OK ==> x.Control.Requested == c.Requested
// @ ensures x.OK ==> x.Control.Consumed == c.Consumed
// @ ensures x.OK ==> x.Control.Answer == c.Answer
// @ ensures x.OK ==> x.Control.Owner == c.Owner
// @ ensures x.OK ==> x.Control.RunFence == c.RunFence
// @ ensures x.OK ==> x.Control.KnownPR == c.KnownPR
// @ ensures x.OK ==> x.Control.Head == c.Head
// @ ensures x.OK ==> x.Control.CI == c.CI
// @ ensures x.OK ==> x.Control.CIHead == c.CIHead
// @ ensures x.OK ==> x.Control.CanMerge == c.CanMerge
// @ ensures x.OK ==> x.Control.MergeHead == c.MergeHead
// @ ensures x.OK ==> x.Control.MergeOutcome == c.MergeOutcome
// @ ensures x.OK ==> x.Control.Proof == c.Proof
// @ ensures x.OK ==> x.Control.Status == c.Status
// @ ensures x.OK ==> x.Control.Cause == c.Cause
// @ ensures x.OK ==> x.Operation.Kind == (o.Kind)
// @ ensures x.OK ==> x.Operation.Base == (o.Base)
// @ ensures x.OK ==> x.Operation.Round == (o.Round)
// @ ensures x.OK ==> x.Operation.Raw == (o.Raw)
// @ ensures x.OK ==> x.Operation.Result == ("success")
// @ ensures x.OK ==> x.Operation.ResultSnapshot == (c.N+1)
// @ ensures x.OK ==> x.Operation.Run == (o.Run)
// @ ensures x.OK ==> x.Operation.Publication == (o.Publication)
// @ ensures x.OK ==> x.Operation.Merge == (o.Merge)
// @ ensures x.OK ==> x.Operation.Reopen == (FollowEffect{Intent:o.Reopen.Intent, Dispatched:o.Reopen.Dispatched, State:"completed"})
// @ ensures x.OK ==> x.Snapshot == (c.Snapshot(c.Op,o.Kind,c.Head,c.Left,c.Round,c.Cause,"reopen-outcome"))
func (c FollowControl) ReconcileReopen(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsReconcileReopen(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	t.N++
	p.Reopen.State = "completed"
	p.ResultSnapshot = t.N
	p.Result, t.Phase = "success", "watch"
	snap = c.Snapshot(c.Op, o.Kind, c.Head, c.Left, c.Round, c.Cause, "reopen-outcome")
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsMerge(o FollowOperation, v FollowObservation, w FollowSession, capacity int, result string) bool {
	return c.WorkAllowed(o, w) && c.Phase == "start-merge" &&
		(result == "success" || result == "refused" || result == "unknown") && v.PR == "open" && c.Head == v.Head &&
		c.CI == "pass" && c.CIHead == c.Head && c.CanMerge && c.MergeHead == c.Head
}

// Merge refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsMerge(o,v,w,capacity,result)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == "merge-response"
// @ ensures x.OK ==> x.Control.Next == c.Next
// @ ensures x.OK ==> x.Control.Op == c.Op
// @ ensures x.OK ==> x.Control.N == c.N
// @ ensures x.OK ==> x.Control.Left == c.Left
// @ ensures x.OK ==> x.Control.Round == c.Round
// @ ensures x.OK ==> x.Control.Requested == c.Requested
// @ ensures x.OK ==> x.Control.Consumed == c.Consumed
// @ ensures x.OK ==> x.Control.Answer == c.Answer
// @ ensures x.OK ==> x.Control.Owner == c.Owner
// @ ensures x.OK ==> x.Control.RunFence == c.RunFence
// @ ensures x.OK ==> x.Control.KnownPR == c.KnownPR
// @ ensures x.OK ==> x.Control.Head == c.Head
// @ ensures x.OK ==> x.Control.CI == c.CI
// @ ensures x.OK ==> x.Control.CIHead == c.CIHead
// @ ensures x.OK ==> x.Control.CanMerge == c.CanMerge
// @ ensures x.OK ==> x.Control.MergeHead == c.MergeHead
// @ ensures x.OK ==> x.Control.MergeOutcome == c.MergeOutcome
// @ ensures x.OK ==> x.Control.Proof == c.Proof
// @ ensures x.OK ==> x.Control.Status == c.Status
// @ ensures x.OK ==> x.Control.Cause == c.Cause
// @ ensures x.OK ==> x.Operation.Kind == (o.Kind)
// @ ensures x.OK ==> x.Operation.Base == (o.Base)
// @ ensures x.OK ==> x.Operation.Round == (o.Round)
// @ ensures x.OK ==> x.Operation.Raw == (o.Raw)
// @ ensures x.OK ==> x.Operation.Result == (o.Result)
// @ ensures x.OK ==> x.Operation.ResultSnapshot == (o.ResultSnapshot)
// @ ensures x.OK ==> x.Operation.Run == (o.Run)
// @ ensures x.OK ==> x.Operation.Publication == (o.Publication)
// @ ensures x.OK ==> x.Operation.Merge == (FollowEffect{Intent:o.Merge.Intent, Dispatched:true, State:o.Merge.State})
// @ ensures x.OK ==> x.Operation.Reopen == (o.Reopen)
// @ ensures x.OK ==> x.Snapshot == (FollowSnapshot{})
func (c FollowControl) Merge(o FollowOperation, v FollowObservation, w FollowSession, capacity int, result string) (x FollowChange) {
	if !c.AllowsMerge(o, v, w, capacity, result) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	p.Merge.Dispatched = true
	t.Phase = "merge-response"
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

// @ requires c.Valid()
// @ decreases
// @ pure
func (c FollowControl) AllowsReconcileMerge(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.N < MaxAttempt && (c.Phase == "merge-response" || c.Phase == "uncertain") &&
		(c.Phase != "uncertain" || v.MergeResult != "unknown" || v.PR == "merged")
}

// ReconcileMerge refuses without changing the cursor when its Workflow step is not due.
// @ requires c.Valid()
// @ ensures x.Before == c && x.Previous == o
// @ ensures x.OK == c.AllowsReconcileMerge(o,v,w,capacity)
// @ ensures !x.OK ==> x.Control == c && x.Operation == o && x.Snapshot == (FollowSnapshot{})
// @ ensures x.Control.Valid()
// @ ensures x.OK ==> x.Control.Identity == c.Identity
// @ ensures x.OK ==> x.Control.Phase == (v.PR == "merged" || v.MergeResult != "unknown" ? "done" : "uncertain")
// @ ensures x.OK ==> x.Control.Next == c.Next
// @ ensures x.OK ==> x.Control.Op == c.Op
// @ ensures x.OK ==> x.Control.N == c.N+1
// @ ensures x.OK ==> x.Control.Left == c.Left
// @ ensures x.OK ==> x.Control.Round == c.Round
// @ ensures x.OK ==> x.Control.Requested == c.Requested
// @ ensures x.OK ==> x.Control.Consumed == c.Consumed
// @ ensures x.OK ==> x.Control.Answer == c.Answer
// @ ensures x.OK ==> x.Control.Owner == c.Owner
// @ ensures x.OK ==> x.Control.RunFence == c.RunFence
// @ ensures x.OK ==> x.Control.KnownPR == c.KnownPR
// @ ensures x.OK ==> x.Control.Head == c.Head
// @ ensures x.OK ==> x.Control.CI == c.CI
// @ ensures x.OK ==> x.Control.CIHead == c.CIHead
// @ ensures x.OK ==> x.Control.CanMerge == c.CanMerge
// @ ensures x.OK ==> x.Control.MergeHead == c.MergeHead
// @ ensures x.OK ==> x.Control.MergeOutcome == (v.PR == "merged" ? "success" : v.MergeResult)
// @ ensures x.OK ==> x.Control.Proof == (v.PR == "merged" || v.MergeResult == "success" ? (v.LostMerge || v.ExternalMerged || v.MergeResult != "success" ? "observation" : "response") : c.Proof)
// @ ensures x.OK ==> x.Control.Status == (v.PR == "merged" || v.MergeResult == "success" ? "merged" : (v.MergeResult == "refused" ? "refused" : c.Status))
// @ ensures x.OK ==> x.Control.Cause == (v.PR != "merged" && v.MergeResult == "refused" ? "refused" : c.Cause)
// @ ensures x.OK ==> x.Operation.Kind == (o.Kind)
// @ ensures x.OK ==> x.Operation.Base == (o.Base)
// @ ensures x.OK ==> x.Operation.Round == (o.Round)
// @ ensures x.OK ==> x.Operation.Raw == (o.Raw)
// @ ensures x.OK ==> x.Operation.Result == ((v.PR == "merged" ? "success" : v.MergeResult))
// @ ensures x.OK ==> x.Operation.ResultSnapshot == ((v.PR == "merged" || v.MergeResult != "unknown" ? c.N+1 : 0))
// @ ensures x.OK ==> x.Operation.Run == (o.Run)
// @ ensures x.OK ==> x.Operation.Publication == (o.Publication)
// @ ensures x.OK ==> x.Operation.Merge == ((v.PR == "merged" || v.MergeResult != "unknown" ? FollowEffect{Intent:o.Merge.Intent, Dispatched:o.Merge.Dispatched, State:"completed"} : o.Merge))
// @ ensures x.OK ==> x.Operation.Reopen == (o.Reopen)
// @ ensures x.OK ==> x.Snapshot == (c.Snapshot(c.Op,o.Kind,c.Head,c.Left,c.Round,x.Operation.Result,"merge-outcome"))
func (c FollowControl) ReconcileMerge(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsReconcileMerge(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	result := v.MergeResult
	if v.PR == "merged" {
		result = "success"
	}
	terminal := result != "unknown"
	t.N++
	t.MergeOutcome, p.Result = result, result
	t.Phase = "uncertain"
	if terminal {
		t.Phase = "done"
		p.Merge.State = "completed"
		p.ResultSnapshot = t.N
	} else {
		p.ResultSnapshot = 0
	}
	if result == "success" {
		t.Status = "merged"
		t.Proof = "response"
		if v.LostMerge || v.ExternalMerged || v.MergeResult != "success" {
			t.Proof = "observation"
		}
	}
	if result == "refused" {
		t.Status, t.Cause = "refused", "refused"
	}
	snap = c.Snapshot(c.Op, o.Kind, c.Head, c.Left, c.Round, result, "merge-outcome")
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}
