package recovery

// The follow-up logic is plain Go, without the // +gobra header: Gobra
// proves the journal's transaction core (followstate.go, journal.go and
// recovery.go), and conformance checks this logic against the model in every
// state the explorer reaches.

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

func (c FollowControl) RepairUnresolved(o FollowOperation) bool {
	return c.Op > 0 && o.Kind == "repair" && o.ResultSnapshot == 0
}

func (c FollowControl) Consumable(o FollowOperation) bool {
	return c.Requested && c.Consumed == 0 && !c.RepairUnresolved(o) &&
		(c.Phase == "hold" || c.Phase == "idle" || c.Phase == "watch" || c.Phase == "done")
}

func (c FollowControl) Holding(w FollowSession) bool {
	return c.Phase != "inactive" && w.Up && w.ID == w.Lease
}

// Internal answer priority is Workflow's scheduling rule, shared by all
// effect operations. A renewal cannot overtake an unresolved repair.
func (c FollowControl) WorkAllowed(o FollowOperation, w FollowSession) bool {
	return c.Holding(w) && !(c.Consumed > 0 && c.Answer.State != "completed") && !c.Consumable(o)
}

func (c FollowControl) Snapshot(op int, kind string, head, left, round int, cause, stage string) FollowSnapshot {
	return FollowSnapshot{Identity: c.Identity, Op: op, Kind: kind, Head: head, Left: left, Round: round, Cause: cause, Stage: stage}
}

func invalidRun(o FollowOperation, v FollowObservation) bool {
	return o.Raw == "success" && o.Kind == "repair" && (o.Base != v.Head || v.PR != "open")
}

func publishRun(o FollowOperation, v FollowObservation) bool {
	return o.Raw == "success" && !invalidRun(o, v) && (o.Kind == "create" || o.Kind == "repair")
}

func holdRun(c FollowControl, o FollowOperation, v FollowObservation) bool {
	return o.Kind == "repair" && !publishRun(o, v) && (c.Left == 0 || v.PR == "closed")
}

func (c FollowControl) AllowsActivate(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.Phase == "inactive"
}

// Activate refuses without changing the cursor when its Workflow step is not due.
func (c FollowControl) Activate(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsActivate(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	t.Phase = "idle"
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

func (c FollowControl) AllowsAuthorize(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.Phase == "idle" && !(c.Requested && c.Consumed == 0) &&
		c.Op < capacity && c.Op < MaxAttempt && c.N < MaxAttempt && c.Status == "active" &&
		(c.Next != "repair" || (c.Left > 0 && v.PR == "open")) &&
		(c.Next != "merge" || (v.PR == "open" && c.Head == v.Head)) &&
		(c.Next != "reopen" || (c.Consumed > 0 && v.PR == "closed"))
}

// Authorize refuses without changing the cursor when its Workflow step is not due.
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

func (c FollowControl) AllowsStart(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.Phase == "start-run" && (o.Kind != "repair" || (v.PR == "open" && o.Base == v.Head))
}

// Start refuses without changing the cursor when its Workflow step is not due.
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

func (c FollowControl) AllowsFinish(o FollowOperation, v FollowObservation, w FollowSession, capacity int, result string) bool {
	return c.WorkAllowed(o, w) && c.Phase == "running" && c.Owner == w.ID && c.RunFence == w.Fence && (result == "success" || result == "failed" || result == "stopped" || result == "nochange")
}

// Finish refuses without changing the cursor when its Workflow step is not due.
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

func (c FollowControl) AllowsRecoverStopped(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.Phase == "running" && (c.Owner != w.ID || c.RunFence != w.Fence)
}

// RecoverStopped refuses without changing the cursor when its Workflow step is not due.
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

func (c FollowControl) AllowsReconcileRun(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.Phase == "run-result" && c.N < MaxAttempt
}

// ReconcileRun refuses without changing the cursor when its Workflow step is not due.
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

func (c FollowControl) AllowsPublish(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.Phase == "start-publication" && (o.Kind != "create" || v.PR == "none") && (o.Kind != "repair" || (v.PR == "open" && o.Base == v.Head))
}

// Publish refuses without changing the cursor when its Workflow step is not due.
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

func (c FollowControl) AllowsPublicationRefusal(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.Phase == "start-publication" && o.Kind == "create"
}

// PublicationRefusal refuses without changing the cursor when its Workflow step is not due.
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

func (c FollowControl) AllowsReconcileCreationRefusal(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.Phase == "publication" && v.PublicationRefused && c.N < MaxAttempt
}

// ReconcileCreationRefusal refuses without changing the cursor when its Workflow step is not due.
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

func (c FollowControl) AllowsReconcilePublication(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.Phase == "publication" && v.PublicationRevision > 0 && c.N < MaxAttempt
}

// ReconcilePublication refuses without changing the cursor when its Workflow step is not due.
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

func (c FollowControl) AllowsReconcileSuperseded(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.N < MaxAttempt &&
		(c.Phase == "record-run" || c.Phase == "start-run" || c.Phase == "record-publication" || c.Phase == "start-publication" || c.Phase == "record-merge" || c.Phase == "start-merge") &&
		(o.Kind == "repair" || o.Kind == "merge") && (o.Base != v.Head || v.PR != "open")
}

// ReconcileSuperseded refuses without changing the cursor when its Workflow step is not due.
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

func (c FollowControl) AllowsObservePR(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.N < MaxAttempt &&
		(c.Phase == "watch" || c.Phase == "idle" || c.Phase == "hold" || c.Phase == "done") &&
		o.Run.State != "pending" && o.Publication.State != "pending" && o.Merge.State != "pending" && o.Reopen.State != "pending" &&
		c.Status != "merged" && !(c.Status == "closed" && v.PR == "closed") &&
		!(c.Phase == "idle" && c.Next == "reopen" && c.Consumed > 0) && (v.PR == "closed" || v.PR == "merged")
}

// ObservePR refuses without changing the cursor when its Workflow step is not due.
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

func (c FollowControl) AllowsObserveCI(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.N < MaxAttempt && c.Phase == "watch" && v.PR == "open" &&
		v.CI != "none" && v.CIHead == c.Head && v.MergeHead == c.Head
}

// ObserveCI refuses without changing the cursor when its Workflow step is not due.
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

func (c FollowControl) AllowsObserveHead(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.N < MaxAttempt &&
		(c.Phase == "watch" || c.Phase == "idle" || c.Phase == "hold" || c.Phase == "done") &&
		c.Head != v.Head && (v.PR == "open" || v.PR == "closed")
}

// ObserveHead refuses without changing the cursor when its Workflow step is not due.
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

func (c FollowControl) AllowsGiveRequest(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.Phase != "inactive" && !c.Requested && (c.Phase == "hold" || c.RepairUnresolved(o))
}

// GiveRequest refuses without changing the cursor when its Workflow step is not due.
func (c FollowControl) GiveRequest(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsGiveRequest(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	t.Requested = true
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

func (c FollowControl) AllowsConsumeRequest(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.Holding(w) && c.Consumable(o) && c.N < MaxAttempt
}

// ConsumeRequest refuses without changing the cursor when its Workflow step is not due.
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

func (c FollowControl) AllowsRecordAnswer(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.Holding(w) && c.Consumed > 0 && c.Answer.State != "completed" && c.Answer.Intent == 0
}

// RecordAnswer refuses without changing the cursor when its Workflow step is not due.
func (c FollowControl) RecordAnswer(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsRecordAnswer(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	t.Answer = FollowEffect{Intent: c.Consumed, State: "pending"}
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

func (c FollowControl) AllowsAnswerRequest(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.Holding(w) && c.Consumed > 0 && c.Answer.State == "pending"
}

// AnswerRequest refuses without changing the cursor when its Workflow step is not due.
func (c FollowControl) AnswerRequest(o FollowOperation, v FollowObservation, w FollowSession, capacity int) (x FollowChange) {
	if !c.AllowsAnswerRequest(o, v, w, capacity) {
		return FollowChange{Before: c, Previous: o, Control: c, Operation: o}
	}
	t, p := c, o
	var snap FollowSnapshot
	t.Answer.Dispatched, t.Answer.State = true, "completed"
	return FollowChange{Before: c, Previous: o, Control: t, Operation: p, Snapshot: snap, OK: true}
}

func (c FollowControl) AllowsReopen(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.Phase == "start-reopen" && v.PR == "closed" && c.Consumed > 0
}

// Reopen refuses without changing the cursor when its Workflow step is not due.
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

func (c FollowControl) AllowsReconcileReopen(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.Phase == "reopen-response" && c.N < MaxAttempt
}

// ReconcileReopen refuses without changing the cursor when its Workflow step is not due.
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

func (c FollowControl) AllowsMerge(o FollowOperation, v FollowObservation, w FollowSession, capacity int, result string) bool {
	return c.WorkAllowed(o, w) && c.Phase == "start-merge" &&
		(result == "success" || result == "refused" || result == "unknown") && v.PR == "open" && c.Head == v.Head &&
		c.CI == "pass" && c.CIHead == c.Head && c.CanMerge && c.MergeHead == c.Head
}

// Merge refuses without changing the cursor when its Workflow step is not due.
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

func (c FollowControl) AllowsReconcileMerge(o FollowOperation, v FollowObservation, w FollowSession, capacity int) bool {
	return c.WorkAllowed(o, w) && c.N < MaxAttempt && (c.Phase == "merge-response" || c.Phase == "uncertain") &&
		(c.Phase != "uncertain" || v.MergeResult != "unknown" || v.PR == "merged")
}

// ReconcileMerge refuses without changing the cursor when its Workflow step is not due.
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
