// +gobra

package followup

// This package implements the decision follow-up workflow for one merged plan.
// External adapters supply draft, creation and CI outcomes; this core determines
// when each operation is allowed. Artifact contents and external event history
// are owned by those adapters, rather than duplicated in workflow state.

const (
	Waiting = iota
	Drafting
	DraftFailed
	ReadyToCreate
	Creating
	CreateFailed
	Open
	NeedsRepair
	Blocked
	PhaseClosed
	Merged
)

const (
	DecisionNone = iota
	Proposed
	Decided
)

const (
	CINone = iota
	CIPending
	CIPassed
	CIFailed
)

const (
	HoldNone = iota
	HoldExhausted
	HoldClosed
)

// MaxRepairLimit leaves room for incrementing a repair counter on every Go
// architecture. The repair policy itself is supplied to New.
const MaxRepairLimit = 1 << 30

// Decision contains the agent's classification. A proposed decision does not
// claim an agent decision, even when the underlying change has already merged.
type Decision struct {
	Status         int
	DecidedByAgent bool
	NeedsOwner     bool
}

// Workflow is a value snapshot. An operation returns a new snapshot and whether
// it ran; a refusal returns the original snapshot. Persist accepted snapshots
// atomically before dispatching subsequent work. Followup is an opaque positive
// identifier supplied by the creation adapter; zero means none has been created.
// RepairLimit is configuration and remains fixed for the lifetime of the work.
type Workflow struct {
	Phase       int
	Draft       Decision
	Followup    int
	CI          int
	CIFresh     bool
	Conflict    bool
	RepairsUsed int
	Hold        int
	RepairLimit int
}

// Valid describes the representation accepted by the operations. Value
// receivers require no heap permissions and cannot mutate the caller's state.
// @ requires true
// @ decreases
// @ pure
func (w Workflow) Valid() bool {
	return Waiting <= w.Phase && w.Phase <= Merged &&
		DecisionNone <= w.Draft.Status && w.Draft.Status <= Decided &&
		0 <= w.Followup && CINone <= w.CI && w.CI <= CIFailed &&
		HoldNone <= w.Hold && w.Hold <= HoldClosed &&
		0 <= w.RepairsUsed && w.RepairsUsed <= w.RepairLimit &&
		0 <= w.RepairLimit && w.RepairLimit <= MaxRepairLimit
}

// New constructs waiting work with a caller-supplied repair allowance.
// @ requires 0 <= repairLimit && repairLimit <= MaxRepairLimit
// @ ensures w.Valid()
// @ ensures w == Workflow{Phase: Waiting, Draft: Decision{Status: DecisionNone, DecidedByAgent: false, NeedsOwner: true}, Followup: 0, CI: CINone, CIFresh: false, Conflict: false, RepairsUsed: 0, Hold: HoldNone, RepairLimit: repairLimit}
func New(repairLimit int) (w Workflow) {
	w = Workflow{Phase: Waiting, Draft: Decision{Status: DecisionNone, NeedsOwner: true}, RepairLimit: repairLimit}
	return
}

// StartDraft starts or automatically retries drafting only for an eligible merged plan.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (merged && owed && (w.Phase == Waiting || w.Phase == DraftFailed))
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == Drafting
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit
func (w Workflow) StartDraft(merged, owed bool) (next Workflow, ok bool) {
	if !(merged && owed && (w.Phase == Waiting || w.Phase == DraftFailed)) {
		return w, false
	}
	next = w
	next.Phase = Drafting
	return next, true
}

// DraftFails records a failed attempt, allowing StartDraft to retry it.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == Drafting)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == DraftFailed
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit
func (w Workflow) DraftFails() (next Workflow, ok bool) {
	if !(w.Phase == Drafting) {
		return w, false
	}
	next = w
	next.Phase = DraftFailed
	return next, true
}

// DraftSucceeds accepts the agent's inference after it has read the plan,
// merged change and review. twoWay and executed describe that stable plan.
// Every other classification stays proposed, including every one-way door.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == Drafting)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == ReadyToCreate
// @ ensures ok ==> next.Draft == Decision{Status: (twoWay && executed && !needsOwner ? Decided : Proposed), DecidedByAgent: twoWay && executed && !needsOwner, NeedsOwner: needsOwner}
// @ ensures next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit
func (w Workflow) DraftSucceeds(twoWay, executed, needsOwner bool) (next Workflow, ok bool) {
	if !(w.Phase == Drafting) {
		return w, false
	}
	next = w
	next.Draft = Decision{Status: Proposed, NeedsOwner: needsOwner}
	if twoWay && executed && !needsOwner {
		next.Draft.Status = Decided
		next.Draft.DecidedByAgent = true
	}
	next.Phase = ReadyToCreate
	return next, true
}

// StartCreation starts or retries construction of the decision bundle.
// The adapter runs invariant decisions decide, includes its journal entry and
// log row, cites all SPEC lines and README passages written by the merged plan,
// and rolls up SPEC. It must restrict all changes to decisions/, SPEC.md and
// the README before reporting CreationSucceeds.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == ReadyToCreate || w.Phase == CreateFailed)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == Creating
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit
func (w Workflow) StartCreation() (next Workflow, ok bool) {
	if !(w.Phase == ReadyToCreate || w.Phase == CreateFailed) {
		return w, false
	}
	next = w
	next.Phase = Creating
	return next, true
}

// CreationFails allows automatic retry when bundle construction or PR creation fails.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == Creating)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == CreateFailed
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit
func (w Workflow) CreationFails() (next Workflow, ok bool) {
	if !(w.Phase == Creating) {
		return w, false
	}
	next = w
	next.Phase = CreateFailed
	return next, true
}

// CreationSucceeds accepts the completed bundle and the unique PR identity
// returned by the creation adapter, then awaits CI for those contents.
// @ requires w.Valid()
// @ requires 0 < id
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == Creating && w.Followup == 0)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == Open
// @ ensures ok ==> next.Followup == id
// @ ensures ok ==> next.CI == CIPending
// @ ensures ok ==> next.CIFresh == false
// @ ensures ok ==> next.Conflict == false
// @ ensures next.Draft == w.Draft && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit
func (w Workflow) CreationSucceeds(id int) (next Workflow, ok bool) {
	if !(w.Phase == Creating && w.Followup == 0) {
		return w, false
	}
	next = w
	next.Phase = Open
	next.Followup = id
	next.CI = CIPending
	next.CIFresh = false
	next.Conflict = false
	return next, true
}

// CheckCompletes accepts a result for the current contents. The adapter
// must correlate the result with the current revision, discarding stale runs.
// A result received while closed never lifts the person's hold.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == ((w.Phase == Open || w.Phase == PhaseClosed) && w.CI == CIPending)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == (w.Phase == PhaseClosed ? PhaseClosed : (passed && !hasConflict ? Open : NeedsRepair))
// @ ensures ok ==> next.CI == (passed ? CIPassed : CIFailed)
// @ ensures ok ==> next.CIFresh == true
// @ ensures ok ==> next.Conflict == hasConflict
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit
func (w Workflow) CheckCompletes(passed, hasConflict bool) (next Workflow, ok bool) {
	if !((w.Phase == Open || w.Phase == PhaseClosed) && w.CI == CIPending) {
		return w, false
	}
	next = w
	next.CI = CIFailed
	if passed {
		next.CI = CIPassed
	}
	next.CIFresh = true
	next.Conflict = hasConflict
	if w.Phase != PhaseClosed {
		next.Phase = NeedsRepair
		if passed && !hasConflict {
			next.Phase = Open
		}
	}
	return next, true
}

// ConflictAppears reports a new conflict, preserving any closure hold.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == ((w.Phase == Open || w.Phase == PhaseClosed) && (w.CI == CIPending || w.CI == CIPassed) && !w.Conflict)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == (w.Phase == PhaseClosed ? PhaseClosed : NeedsRepair)
// @ ensures ok ==> next.Conflict == true
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit
func (w Workflow) ConflictAppears() (next Workflow, ok bool) {
	if !((w.Phase == Open || w.Phase == PhaseClosed) && (w.CI == CIPending || w.CI == CIPassed) && !w.Conflict) {
		return w, false
	}
	next = w
	next.Conflict = true
	if w.Phase != PhaseClosed {
		next.Phase = NeedsRepair
	}
	return next, true
}

// Repair consumes one repair and invalidates the preceding CI result.
// The adapter repairs the same PR within its original allowed files and starts
// CI for the new contents. No identity or decision classification changes.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == NeedsRepair && w.Hold == HoldNone && w.RepairsUsed < w.RepairLimit)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == Open
// @ ensures ok ==> next.CI == CIPending
// @ ensures ok ==> next.CIFresh == false
// @ ensures ok ==> next.Conflict == false
// @ ensures ok ==> next.RepairsUsed == w.RepairsUsed + 1
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit
func (w Workflow) Repair() (next Workflow, ok bool) {
	if !(w.Phase == NeedsRepair && w.Hold == HoldNone && w.RepairsUsed < w.RepairLimit) {
		return w, false
	}
	next = w
	next.Phase = Open
	next.CI = CIPending
	next.CIFresh = false
	next.Conflict = false
	next.RepairsUsed = w.RepairsUsed + 1
	return next, true
}

// Block holds exhausted work until a person's explicit Retry or Close.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == NeedsRepair && w.RepairsUsed == w.RepairLimit)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == Blocked
// @ ensures ok ==> next.Hold == HoldExhausted
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.RepairLimit == w.RepairLimit
func (w Workflow) Block() (next Workflow, ok bool) {
	if !(w.Phase == NeedsRepair && w.RepairsUsed == w.RepairLimit) {
		return w, false
	}
	next = w
	next.Phase = Blocked
	next.Hold = HoldExhausted
	return next, true
}

// Close applies an explicit closure by a person, preserving the PR identity.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == Open || w.Phase == NeedsRepair || w.Phase == Blocked)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == PhaseClosed
// @ ensures ok ==> next.Hold == HoldClosed
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.RepairLimit == w.RepairLimit
func (w Workflow) Close() (next Workflow, ok bool) {
	if !(w.Phase == Open || w.Phase == NeedsRepair || w.Phase == Blocked) {
		return w, false
	}
	next = w
	next.Phase = PhaseClosed
	next.Hold = HoldClosed
	return next, true
}

// Retry accepts a person's explicit retry request with a fresh allowance.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == Blocked)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == NeedsRepair
// @ ensures ok ==> next.Hold == HoldNone
// @ ensures ok ==> next.RepairsUsed == 0
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairLimit == w.RepairLimit
func (w Workflow) Retry() (next Workflow, ok bool) {
	if !(w.Phase == Blocked) {
		return w, false
	}
	next = w
	next.Phase = NeedsRepair
	next.Hold = HoldNone
	next.RepairsUsed = 0
	return next, true
}

// Resume accepts a person's explicit request to reopen the same PR.
// Every request renews the allowance, even if the old allowance was unused.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == PhaseClosed)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == (w.CI == CIFailed || w.Conflict ? NeedsRepair : Open)
// @ ensures ok ==> next.Hold == HoldNone
// @ ensures ok ==> next.RepairsUsed == 0
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairLimit == w.RepairLimit
func (w Workflow) Resume() (next Workflow, ok bool) {
	if !(w.Phase == PhaseClosed) {
		return w, false
	}
	next = w
	next.Phase = Open
	if w.CI == CIFailed || w.Conflict {
		next.Phase = NeedsRepair
	}
	next.Hold = HoldNone
	next.RepairsUsed = 0
	return next, true
}

// AutoMerge authorizes merging only after current CI passes without conflict.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == Open && w.CI == CIPassed && w.CIFresh && !w.Conflict && w.Hold == HoldNone)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == Merged
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit
func (w Workflow) AutoMerge() (next Workflow, ok bool) {
	if !(w.Phase == Open && w.CI == CIPassed && w.CIFresh && !w.Conflict && w.Hold == HoldNone) {
		return w, false
	}
	next = w
	next.Phase = Merged
	return next, true
}
