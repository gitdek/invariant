// +gobra

package followup

// This package implements the decision follow-up workflow for one merged plan.
// The value snapshot holds policy, current observations, and one recoverable
// operation. Adapters own artifacts and authentic remote outcomes; the explorer
// owns execution history and finite environmental choices.

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
	Repairing
	Merging
	MergeRefused
	ExternallyMerged
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
	HoldMergeRefused
)

const (
	ErrorNone = iota
	ErrorFailed
	ErrorNoChange
)

const (
	EffectNone = iota
	EffectRepair
	EffectMerge
)

const (
	StageIdle = iota
	StageAuthorized
	StagePending
	StageLost
	StageSettled
)

const (
	ResultNone = iota
	ResultPublished
	ResultFailed
	ResultNoChange
	ResultMerged
	ResultRefused
)

// These machine limits leave room for safe increments on every Go architecture.
// The repair policy is supplied to New; neither limit is an exploration bound.
const MaxRepairLimit = 1 << 30
const MaxSequence = 1 << 30

// Decision contains the agent's evidence-based classification.
type Decision struct {
	Status         int
	DecidedByAgent bool
	NeedsOwner     bool
}

// Causes retains the real reasons for authorizing an attempt. Execution failure
// is distinct from a GitHub CI failure and can itself require another attempt.
type Causes struct {
	CI        bool
	Conflict  bool
	Execution bool
}

// Operation is a stable authorization tied to a revision. Its stage is the
// execution claim: once pending, recovery reconciles instead of executing again.
// Actual remote results and execution counts are not workflow state. An adapter
// must reconcile a trusted result for this identity before delivering an outcome.
type Operation struct {
	ID    int
	Kind  int
	Head  int
	Stage int
	Cause Causes
}

// Workflow is a value snapshot: refusal returns it unchanged. Persist accepted
// transitions atomically before dispatching further work. Followup is an opaque
// positive PR identity. RepairLimit is fixed configuration, not a model bound.
// This core does not implement the durable dispatch or remote recovery protocol.
type Workflow struct {
	Phase         int
	Draft         Decision
	Followup      int
	CI            int
	CIFresh       bool
	Conflict      bool
	RepairsUsed   int
	Hold          int
	RepairLimit   int
	Head          int
	CIHead        int
	RepairError   int
	FailureCause  Causes
	Effect        Operation
	ResumePending bool
}

// Valid describes representation constraints at every configured allowance.
// Value receivers need no heap permissions and never mutate their caller.
// @ requires true
// @ decreases
// @ pure
func (w Workflow) Valid() bool {
	return Waiting <= w.Phase && w.Phase <= ExternallyMerged &&
		DecisionNone <= w.Draft.Status && w.Draft.Status <= Decided &&
		0 <= w.Followup && CINone <= w.CI && w.CI <= CIFailed &&
		HoldNone <= w.Hold && w.Hold <= HoldMergeRefused &&
		0 <= w.RepairsUsed && w.RepairsUsed <= w.RepairLimit &&
		0 <= w.RepairLimit && w.RepairLimit <= MaxRepairLimit &&
		0 <= w.Head && w.Head <= MaxSequence &&
		0 <= w.CIHead && w.CIHead <= MaxSequence &&
		ErrorNone <= w.RepairError && w.RepairError <= ErrorNoChange &&
		0 <= w.Effect.ID && w.Effect.ID <= MaxSequence &&
		0 <= w.Effect.Head && w.Effect.Head <= MaxSequence &&
		EffectNone <= w.Effect.Kind && w.Effect.Kind <= EffectMerge &&
		StageIdle <= w.Effect.Stage && w.Effect.Stage <= StageSettled
}

// @ requires true
// @ decreases
// @ pure
func (e Operation) Active() bool {
	return e.Stage == StageAuthorized || e.Stage == StagePending || e.Stage == StageLost
}

// Matching requires an executed, unresolved operation and the complete token.
// @ requires true
// @ decreases
// @ pure
func (e Operation) Matching(id, kind, revision int) bool {
	return (e.Stage == StagePending || e.Stage == StageLost) &&
		id == e.ID && kind == e.Kind && revision == e.Head
}

// @ requires true
// @ decreases
// @ pure
func (w Workflow) Live() bool {
	return w.Followup != 0 && w.Phase != Merged && w.Phase != ExternallyMerged
}

// @ requires true
// @ decreases
// @ pure
func (w Workflow) NeedsRepair() bool {
	return w.CI == CIFailed || w.Conflict || w.RepairError != ErrorNone
}

// @ requires true
// @ decreases
// @ pure
func (w Workflow) CurrentCauses() Causes {
	return Causes{CI: w.CI == CIFailed, Conflict: w.Conflict, Execution: w.RepairError != ErrorNone}
}

// @ ghost
// @ requires true
// @ decreases
// @ pure func phaseValue(hold int, effect Operation, check int, conflict bool, repairError int) int { return hold == HoldClosed ? PhaseClosed : (hold == HoldExhausted ? Blocked : (hold == HoldMergeRefused ? MergeRefused : (effect.Active() ? (effect.Kind == EffectRepair ? Repairing : Merging) : (check == CIFailed || conflict || repairError != ErrorNone ? NeedsRepair : Open)))) }

// PhaseFor gives human holds priority over effects and observed repair needs.
// @ requires true
// @ ensures Open <= p && p <= MergeRefused
// @ ensures p == phaseValue(hold, effect, check, conflict, repairError)
func PhaseFor(hold int, effect Operation, check int, conflict bool, repairError int) (p int) {
	if hold == HoldClosed {
		return PhaseClosed
	}
	if hold == HoldExhausted {
		return Blocked
	}
	if hold == HoldMergeRefused {
		return MergeRefused
	}
	if effect.Active() {
		if effect.Kind == EffectRepair {
			return Repairing
		}
		return Merging
	}
	if check == CIFailed || conflict || repairError != ErrorNone {
		return NeedsRepair
	}
	return Open
}

// @ requires true
// @ decreases
// @ pure
func (w Workflow) ReadyToMerge() bool {
	return w.Phase == Open && w.CI == CIPassed && w.CIFresh && !w.Conflict &&
		w.CIHead == w.Head && w.Hold == HoldNone && !w.Effect.Active() && w.RepairError == ErrorNone
}

// New constructs waiting work with a caller-supplied repair allowance.
// @ requires 0 <= repairLimit && repairLimit <= MaxRepairLimit
// @ ensures w.Valid()
// @ ensures w == Workflow{Phase: Waiting, Draft: Decision{Status: DecisionNone, DecidedByAgent: false, NeedsOwner: true}, Followup: 0, CI: CINone, CIFresh: false, Conflict: false, RepairsUsed: 0, Hold: HoldNone, RepairLimit: repairLimit, Head: 0, CIHead: 0, RepairError: ErrorNone, FailureCause: Causes{}, Effect: Operation{}, ResumePending: false}
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
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.Effect == w.Effect && next.ResumePending == w.ResumePending
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
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.Effect == w.Effect && next.ResumePending == w.ResumePending
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
// @ ensures next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.Effect == w.Effect && next.ResumePending == w.ResumePending
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
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.Effect == w.Effect && next.ResumePending == w.ResumePending
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
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.Effect == w.Effect && next.ResumePending == w.ResumePending
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
// @ ensures ok ==> next.Head == 1 && next.CIHead == 1
// @ ensures next.Draft == w.Draft && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.Effect == w.Effect && next.ResumePending == w.ResumePending
func (w Workflow) CreationSucceeds(id int) (next Workflow, ok bool) {
	if !(w.Phase == Creating && w.Followup == 0) {
		return w, false
	}
	next = w
	next.Phase = Open
	next.Followup = id
	next.Head = 1
	next.CIHead = 1
	next.CI = CIPending
	next.CIFresh = false
	next.Conflict = false
	return next, true
}

// RequestCI requests a run for the observed revision after publication or head
// movement. A failed execution needs repair, not a fictitious CI request.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Live() && w.Hold == HoldNone && !w.Effect.Active() && !w.NeedsRepair() && w.CI == CINone)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.CI == CIPending && next.CIHead == w.Head && !next.CIFresh
// @ ensures next.Phase == w.Phase && next.Draft == w.Draft && next.Followup == w.Followup && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.Effect == w.Effect && next.ResumePending == w.ResumePending
func (w Workflow) RequestCI() (next Workflow, ok bool) {
	if !(w.Live() && w.Hold == HoldNone && !w.Effect.Active() && !w.NeedsRepair() && w.CI == CINone) {
		return w, false
	}
	next = w
	next.CI = CIPending
	next.CIHead = w.Head
	next.CIFresh = false
	return next, true
}

// CheckCompletes accepts only the current requested revision. Conflict is an
// independent observation, and every hold survives a CI completion.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Live() && w.CI == CIPending && w.CIHead == w.Head && revision == w.Head && (result == CIPassed || result == CIFailed))
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.CI == result && next.CIFresh
// @ ensures ok ==> next.Phase == phaseValue(w.Hold, w.Effect, result, w.Conflict, w.RepairError)
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.Effect == w.Effect && next.ResumePending == w.ResumePending
func (w Workflow) CheckCompletes(revision, result int) (next Workflow, ok bool) {
	if !(w.Live() && w.CI == CIPending && w.CIHead == w.Head && revision == w.Head && (result == CIPassed || result == CIFailed)) {
		return w, false
	}
	next = w
	next.CI = result
	next.CIFresh = true
	next.Phase = PhaseFor(w.Hold, w.Effect, result, w.Conflict, w.RepairError)
	return next, true
}

// ObserveRevision records the next distinct head and invalidates CI. It neither
// spends nor renews repairs, clears execution failure, nor releases a hold.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Live() && w.Head < MaxSequence)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Head == w.Head + 1 && next.CI == CINone && next.CIHead == 0 && !next.CIFresh
// @ ensures ok ==> next.Phase == phaseValue(w.Hold, w.Effect, CINone, w.Conflict, w.RepairError)
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.Effect == w.Effect && next.ResumePending == w.ResumePending
func (w Workflow) ObserveRevision() (next Workflow, ok bool) {
	if !(w.Live() && w.Head < MaxSequence) {
		return w, false
	}
	next = w
	next.Head = w.Head + 1
	next.CI = CINone
	next.CIHead = 0
	next.CIFresh = false
	next.Phase = PhaseFor(w.Hold, w.Effect, CINone, w.Conflict, w.RepairError)
	return next, true
}

// ConflictAppears records a real conflict, including during a merge attempt.
// It leaves the current CI result and any human hold intact.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Live() && !w.Conflict)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Conflict
// @ ensures ok ==> next.Phase == phaseValue(w.Hold, w.Effect, w.CI, true, w.RepairError)
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.Effect == w.Effect && next.ResumePending == w.ResumePending
func (w Workflow) ConflictAppears() (next Workflow, ok bool) {
	if !(w.Live() && !w.Conflict) {
		return w, false
	}
	next = w
	next.Conflict = true
	next.Phase = PhaseFor(w.Hold, w.Effect, w.CI, true, w.RepairError)
	return next, true
}

// Repair authorizes and charges exactly one attempt on the same PR. Publication,
// failure and no change all spend this charge; executing and settling do not.
// The adapter must keep any publication within the original artifact scope.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == NeedsRepair && w.Hold == HoldNone && !w.Effect.Active() && w.RepairsUsed < w.RepairLimit && w.Effect.ID < MaxSequence)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.RepairsUsed == w.RepairsUsed + 1 && next.Phase == Repairing
// @ ensures ok ==> next.Effect == Operation{ID: w.Effect.ID + 1, Kind: EffectRepair, Head: w.Head, Stage: StageAuthorized, Cause: w.CurrentCauses()}
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.ResumePending == w.ResumePending
func (w Workflow) Repair() (next Workflow, ok bool) {
	if !(w.Phase == NeedsRepair && w.Hold == HoldNone && !w.Effect.Active() && w.RepairsUsed < w.RepairLimit && w.Effect.ID < MaxSequence) {
		return w, false
	}
	next = w
	next.RepairsUsed = w.RepairsUsed + 1
	next.Effect = Operation{ID: w.Effect.ID + 1, Kind: EffectRepair, Head: w.Head, Stage: StageAuthorized, Cause: w.CurrentCauses()}
	next.Phase = Repairing
	return next, true
}

// ExecuteRepair advances the execution claim once. The executor owns the actual
// result until delivery or reconciliation; it is not an observed CI result.
// Persist this claim as part of the later durable execution protocol.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Live() && w.Effect.Kind == EffectRepair && w.Effect.Stage == StageAuthorized && (result == ResultPublished || result == ResultFailed || result == ResultNoChange))
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Effect == Operation{ID: w.Effect.ID, Kind: w.Effect.Kind, Head: w.Effect.Head, Stage: StagePending, Cause: w.Effect.Cause}
// @ ensures next.Phase == w.Phase && next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.ResumePending == w.ResumePending
func (w Workflow) ExecuteRepair(result int) (next Workflow, ok bool) {
	if !(w.Live() && w.Effect.Kind == EffectRepair && w.Effect.Stage == StageAuthorized && (result == ResultPublished || result == ResultFailed || result == ResultNoChange)) {
		return w, false
	}
	next = w
	next.Effect.Stage = StagePending
	return next, true
}

// AutoMerge authorizes a merge attempt for current passing CI without conflict.
// The PR remains nonterminal until its authenticated remote outcome is accepted.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.ReadyToMerge() && w.Effect.ID < MaxSequence)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == Merging
// @ ensures ok ==> next.Effect == Operation{ID: w.Effect.ID + 1, Kind: EffectMerge, Head: w.Head, Stage: StageAuthorized, Cause: Causes{}}
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.ResumePending == w.ResumePending
func (w Workflow) AutoMerge() (next Workflow, ok bool) {
	if !(w.ReadyToMerge() && w.Effect.ID < MaxSequence) {
		return w, false
	}
	next = w
	next.Effect = Operation{ID: w.Effect.ID + 1, Kind: EffectMerge, Head: w.Head, Stage: StageAuthorized}
	next.Phase = Merging
	return next, true
}

// ExecuteMerge advances the merge execution claim once. The executor can report
// refusal; success requires the authorized head to remain open, current and ready.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Live() && w.Effect.Kind == EffectMerge && w.Effect.Stage == StageAuthorized && (result == ResultMerged || result == ResultRefused) && (result != ResultMerged || (w.Hold == HoldNone && w.Head == w.Effect.Head && w.CI == CIPassed && w.CIFresh && w.CIHead == w.Head && !w.Conflict)))
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Effect == Operation{ID: w.Effect.ID, Kind: w.Effect.Kind, Head: w.Effect.Head, Stage: StagePending, Cause: w.Effect.Cause}
// @ ensures next.Phase == w.Phase && next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.ResumePending == w.ResumePending
func (w Workflow) ExecuteMerge(result int) (next Workflow, ok bool) {
	if !(w.Live() && w.Effect.Kind == EffectMerge && w.Effect.Stage == StageAuthorized && (result == ResultMerged || result == ResultRefused) && (result != ResultMerged || (w.Hold == HoldNone && w.Head == w.Effect.Head && w.CI == CIPassed && w.CIFresh && w.CIHead == w.Head && !w.Conflict))) {
		return w, false
	}
	next = w
	next.Effect.Stage = StagePending
	return next, true
}

// LoseReply keeps the operation unresolved. Recovery must reconcile its stable
// identity; neither execution nor authorization becomes enabled again.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Live() && w.Effect.Stage == StagePending)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Effect == Operation{ID: w.Effect.ID, Kind: w.Effect.Kind, Head: w.Effect.Head, Stage: StageLost, Cause: w.Effect.Cause}
// @ ensures next.Phase == w.Phase && next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.ResumePending == w.ResumePending
func (w Workflow) LoseReply() (next Workflow, ok bool) {
	if !(w.Live() && w.Effect.Stage == StagePending) {
		return w, false
	}
	next = w
	next.Effect.Stage = StageLost
	return next, true
}

// RepairOutcome accepts a trusted executor outcome or reconciliation receipt.
// The adapter supplies the actual result for this token. A failed or unchanged
// attempt preserves observed CI/head/conflict and records the authorization cause.
// An overtaken publication settles without overwriting the newer observed head.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Live() && w.Effect.Matching(id, EffectRepair, revision) && (result == ResultPublished || result == ResultFailed || result == ResultNoChange) && (result != ResultPublished || w.Head != w.Effect.Head || w.Head < MaxSequence))
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Effect == Operation{ID: w.Effect.ID, Kind: w.Effect.Kind, Head: w.Effect.Head, Stage: StageSettled, Cause: w.Effect.Cause}
// @ ensures ok ==> next.Head == (result == ResultPublished && w.Head == w.Effect.Head ? w.Head + 1 : w.Head)
// @ ensures ok ==> next.CI == (result == ResultPublished && w.Head == w.Effect.Head ? CINone : w.CI)
// @ ensures ok ==> next.CIHead == (result == ResultPublished && w.Head == w.Effect.Head ? 0 : w.CIHead)
// @ ensures ok ==> next.CIFresh == (result == ResultPublished && w.Head == w.Effect.Head ? false : w.CIFresh)
// @ ensures ok ==> next.Conflict == (result == ResultPublished && w.Head == w.Effect.Head ? false : w.Conflict)
// @ ensures ok ==> next.RepairError == (result == ResultFailed ? ErrorFailed : (result == ResultNoChange ? ErrorNoChange : (w.Head == w.Effect.Head ? ErrorNone : w.RepairError)))
// @ ensures ok ==> next.FailureCause == (result == ResultFailed || result == ResultNoChange ? w.Effect.Cause : (w.Head == w.Effect.Head ? Causes{} : w.FailureCause))
// @ ensures ok ==> next.Phase == phaseValue(next.Hold, next.Effect, next.CI, next.Conflict, next.RepairError)
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit && next.ResumePending == w.ResumePending
func (w Workflow) RepairOutcome(id, revision, result int) (next Workflow, ok bool) {
	if !(w.Live() && w.Effect.Matching(id, EffectRepair, revision) && (result == ResultPublished || result == ResultFailed || result == ResultNoChange) && (result != ResultPublished || w.Head != w.Effect.Head || w.Head < MaxSequence)) {
		return w, false
	}
	next = w
	next.Effect.Stage = StageSettled
	if result == ResultFailed || result == ResultNoChange {
		next.RepairError = ErrorFailed
		if result == ResultNoChange {
			next.RepairError = ErrorNoChange
		}
		next.FailureCause = w.Effect.Cause
	} else if w.Head == w.Effect.Head {
		next.Head = w.Head + 1
		next.CI = CINone
		next.CIHead = 0
		next.CIFresh = false
		next.Conflict = false
		next.RepairError = ErrorNone
		next.FailureCause = Causes{}
	}
	next.Phase = PhaseFor(next.Hold, next.Effect, next.CI, next.Conflict, next.RepairError)
	return next, true
}

// MergeOutcome confirms a trusted remote result for the executed authorization.
// A clean definitive refusal needs a person. Otherwise current observations
// determine the nonterminal phase, and closure remains effective.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Live() && w.Effect.Matching(id, EffectMerge, revision) && (result == ResultMerged || result == ResultRefused))
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Effect == Operation{ID: w.Effect.ID, Kind: w.Effect.Kind, Head: w.Effect.Head, Stage: StageSettled, Cause: w.Effect.Cause}
// @ ensures ok ==> next.Hold == (result == ResultMerged ? HoldNone : (w.Hold == HoldNone && w.Head == w.Effect.Head && w.CI == CIPassed && w.CIFresh && !w.Conflict ? HoldMergeRefused : w.Hold))
// @ ensures ok ==> next.ResumePending == (result == ResultMerged ? false : w.ResumePending)
// @ ensures ok ==> next.Phase == (result == ResultMerged ? Merged : phaseValue(next.Hold, next.Effect, next.CI, next.Conflict, next.RepairError))
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause
func (w Workflow) MergeOutcome(id, revision, result int) (next Workflow, ok bool) {
	if !(w.Live() && w.Effect.Matching(id, EffectMerge, revision) && (result == ResultMerged || result == ResultRefused)) {
		return w, false
	}
	next = w
	next.Effect.Stage = StageSettled
	if result == ResultMerged {
		next.Hold = HoldNone
		next.ResumePending = false
		next.Phase = Merged
	} else {
		if w.Hold == HoldNone && w.Head == w.Effect.Head && w.CI == CIPassed && w.CIFresh && !w.Conflict {
			next.Hold = HoldMergeRefused
		}
		next.Phase = PhaseFor(next.Hold, next.Effect, next.CI, next.Conflict, next.RepairError)
	}
	return next, true
}

// Block holds exhausted repair work for a person, even if later head movement
// invalidates the CI failure that originally needed repair.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == NeedsRepair && !w.Effect.Active() && w.RepairsUsed == w.RepairLimit)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == Blocked && next.Hold == HoldExhausted
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.Effect == w.Effect && next.ResumePending == w.ResumePending
func (w Workflow) Block() (next Workflow, ok bool) {
	if !(w.Phase == NeedsRepair && !w.Effect.Active() && w.RepairsUsed == w.RepairLimit) {
		return w, false
	}
	next = w
	next.Phase = Blocked
	next.Hold = HoldExhausted
	return next, true
}

// Close preserves an explicit closure during in-flight work. Observation
// adapters report closure only while the PR is actually unmerged.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Live() && w.Hold != HoldClosed)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == PhaseClosed && next.Hold == HoldClosed
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.Effect == w.Effect && next.ResumePending == w.ResumePending
func (w Workflow) Close() (next Workflow, ok bool) {
	if !(w.Live() && w.Hold != HoldClosed) {
		return w, false
	}
	next = w
	next.Phase = PhaseClosed
	next.Hold = HoldClosed
	return next, true
}

// Retry accepts an explicit authorized human request after exhaustion or a
// definitive merge refusal. It renews repairs without changing observed facts.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Live() && (w.Hold == HoldExhausted || w.Hold == HoldMergeRefused) && !w.Effect.Active())
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Hold == HoldNone && next.RepairsUsed == 0
// @ ensures ok ==> next.Phase == phaseValue(HoldNone, w.Effect, w.CI, w.Conflict, w.RepairError)
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.Effect == w.Effect && next.ResumePending == w.ResumePending
func (w Workflow) Retry() (next Workflow, ok bool) {
	if !(w.Live() && (w.Hold == HoldExhausted || w.Hold == HoldMergeRefused) && !w.Effect.Active()) {
		return w, false
	}
	next = w
	next.Hold = HoldNone
	next.RepairsUsed = 0
	next.Phase = PhaseFor(HoldNone, w.Effect, w.CI, w.Conflict, w.RepairError)
	return next, true
}

// Resume applies an explicit authorized request to reopen the same PR when
// no outcome is unresolved. Use DeferResume while an effect remains active.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Live() && w.Hold == HoldClosed && !w.Effect.Active() && !w.ResumePending)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Hold == HoldNone && next.RepairsUsed == 0
// @ ensures ok ==> next.Phase == phaseValue(HoldNone, w.Effect, w.CI, w.Conflict, w.RepairError)
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.Effect == w.Effect && next.ResumePending == w.ResumePending
func (w Workflow) Resume() (next Workflow, ok bool) {
	if !(w.Live() && w.Hold == HoldClosed && !w.Effect.Active() && !w.ResumePending) {
		return w, false
	}
	next = w
	next.Hold = HoldNone
	next.RepairsUsed = 0
	next.Phase = PhaseFor(HoldNone, w.Effect, w.CI, w.Conflict, w.RepairError)
	return next, true
}

// DeferResume remembers the person's request while reconciliation is pending.
// Closure and the spent allowance remain unchanged; the person need not ask twice.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Live() && w.Hold == HoldClosed && w.Effect.Active() && !w.ResumePending)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.ResumePending
// @ ensures next.Phase == w.Phase && next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.Effect == w.Effect
func (w Workflow) DeferResume() (next Workflow, ok bool) {
	if !(w.Live() && w.Hold == HoldClosed && w.Effect.Active() && !w.ResumePending) {
		return w, false
	}
	next = w
	next.ResumePending = true
	return next, true
}

// ApplyDeferredResume applies the remembered request after reconciliation
// leaves the same PR unmerged. External settlement cancels it instead.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Live() && w.Hold == HoldClosed && !w.Effect.Active() && w.ResumePending)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Hold == HoldNone && next.RepairsUsed == 0 && !next.ResumePending
// @ ensures ok ==> next.Phase == phaseValue(HoldNone, w.Effect, w.CI, w.Conflict, w.RepairError)
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.Effect == w.Effect
func (w Workflow) ApplyDeferredResume() (next Workflow, ok bool) {
	if !(w.Live() && w.Hold == HoldClosed && !w.Effect.Active() && w.ResumePending) {
		return w, false
	}
	next = w
	next.Hold = HoldNone
	next.RepairsUsed = 0
	next.ResumePending = false
	next.Phase = PhaseFor(HoldNone, w.Effect, w.CI, w.Conflict, w.RepairError)
	return next, true
}

// ObserveExternalMerge settles an externally merged PR without claiming factory
// success, creating passing CI, or applying a deferred resumption. The adapter
// reconciles factory success through MergeOutcome instead of this observation.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Live())
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == ExternallyMerged && next.Hold == HoldNone && !next.ResumePending
// @ ensures ok ==> next.Effect == Operation{ID: w.Effect.ID, Kind: w.Effect.Kind, Head: w.Effect.Head, Stage: (w.Effect.Active() ? StageSettled : w.Effect.Stage), Cause: w.Effect.Cause}
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause
func (w Workflow) ObserveExternalMerge() (next Workflow, ok bool) {
	if !(w.Live()) {
		return w, false
	}
	next = w
	next.Phase = ExternallyMerged
	next.Hold = HoldNone
	next.ResumePending = false
	if w.Effect.Active() {
		next.Effect.Stage = StageSettled
	}
	return next, true
}

// RejectCI recognizes a stale or unsolicited CI completion. A true return
// permits the adapter to report rejection; the workflow always stays unchanged.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Live() && 0 <= revision && revision <= w.Head && (revision != w.Head || w.CI != CIPending))
// @ ensures !ok ==> next == w
// @ ensures next.Phase == w.Phase && next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.Effect == w.Effect && next.ResumePending == w.ResumePending
func (w Workflow) RejectCI(revision int) (next Workflow, ok bool) {
	if !(w.Live() && 0 <= revision && revision <= w.Head && (revision != w.Head || w.CI != CIPending)) {
		return w, false
	}
	next = w

	return next, true
}

// RejectOutcome recognizes a mismatched or already settled operation token.
// Rejection never changes the current operation, observations, allowance or hold.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Live() && w.Effect.ID > 0 && 0 <= id && id <= w.Effect.ID && (kind == EffectRepair || kind == EffectMerge) && 0 <= revision && revision <= w.Head && !w.Effect.Matching(id, kind, revision))
// @ ensures !ok ==> next == w
// @ ensures next.Phase == w.Phase && next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.Hold == w.Hold && next.RepairLimit == w.RepairLimit && next.Head == w.Head && next.CIHead == w.CIHead && next.RepairError == w.RepairError && next.FailureCause == w.FailureCause && next.Effect == w.Effect && next.ResumePending == w.ResumePending
func (w Workflow) RejectOutcome(id, kind, revision int) (next Workflow, ok bool) {
	if !(w.Live() && w.Effect.ID > 0 && 0 <= id && id <= w.Effect.ID && (kind == EffectRepair || kind == EffectMerge) && 0 <= revision && revision <= w.Head && !w.Effect.Matching(id, kind, revision)) {
		return w, false
	}
	next = w

	return next, true
}
