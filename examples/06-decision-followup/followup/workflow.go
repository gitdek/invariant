// +gobra

package followup

// MaxSize is a machine safety limit, independent of any exploration size.
const MaxSize = 1 << 30

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
	ClosedPhase
	Merged
)

const (
	NoDecision = iota
	Proposed
	Decided
)

const (
	NoCI = iota
	Pending
	Passed
	Failed
)

const (
	NoHold = iota
	ExhaustedHold
	ClosedHold
)

// Draft retains the agent's classification and the evidence it used.
type Draft struct {
	Status         int
	DecidedByAgent bool
	NeedsOwner     bool
	PlanSource     bool
	ChangeSource   bool
	ReviewSource   bool
}

// Workflow is the system's durable state for one plan. Followup is an opaque,
// positive pull request identity; zero means creation has not succeeded.
// Methods return a new value, making refusal atomic and persistence explicit.
type Workflow struct {
	Phase       int
	Draft       Draft
	Followup    int
	CI          int
	CIFresh     bool
	Conflict    bool
	RepairsUsed int
	RepairLimit int
	Hold        int
}

// Valid describes the arithmetic assumptions shared by all operations.
// @ decreases
// @ pure
func (w Workflow) Valid() bool {
	return 0 <= w.RepairLimit && w.RepairLimit <= MaxSize &&
		0 <= w.RepairsUsed && w.RepairsUsed <= w.RepairLimit
}

// New starts an obligation with a caller-selected repair allowance.
// @ requires 0 <= repairLimit && repairLimit <= MaxSize
// @ ensures w.Valid()
// @ ensures w.Phase == Waiting && w.Followup == 0
// @ ensures w.Draft.Status == NoDecision && !w.Draft.DecidedByAgent && w.Draft.NeedsOwner
// @ ensures !w.Draft.PlanSource && !w.Draft.ChangeSource && !w.Draft.ReviewSource
// @ ensures w.CI == NoCI && !w.CIFresh && !w.Conflict
// @ ensures w.RepairsUsed == 0 && w.RepairLimit == repairLimit && w.Hold == NoHold
func New(repairLimit int) (w Workflow) {
	return Workflow{Phase: Waiting, Draft: Draft{NeedsOwner: true}, RepairLimit: repairLimit}
}

// StartDraft starts eligible work, including an automatic retry after drafting fails.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (planMerged && decisionOwed && (w.Phase == Waiting || w.Phase == DraftFailed))
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == Drafting
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI
// @ ensures next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed
// @ ensures next.RepairLimit == w.RepairLimit && next.Hold == w.Hold
func (w Workflow) StartDraft(planMerged, decisionOwed bool) (next Workflow, ok bool) {
	next = w
	if !(planMerged && decisionOwed && (w.Phase == Waiting || w.Phase == DraftFailed)) {
		return next, false
	}
	next.Phase = Drafting
	return next, true
}

// DraftFails records an unsuccessful agent attempt so it can be retried.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == Drafting)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == DraftFailed
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI
// @ ensures next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed
// @ ensures next.RepairLimit == w.RepairLimit && next.Hold == w.Hold
func (w Workflow) DraftFails() (next Workflow, ok bool) {
	next = w
	if !(w.Phase == Drafting) {
		return next, false
	}
	next.Phase = DraftFailed
	return next, true
}

// DraftSucceeds accepts the agent's inference from the plan, merged change and review.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == Drafting)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == ReadyToCreate
// @ ensures ok ==> next.Draft.Status == (twoWay && executed && !needsOwner ? Decided : Proposed)
// @ ensures ok ==> next.Draft.DecidedByAgent == (twoWay && executed && !needsOwner)
// @ ensures ok ==> next.Draft.NeedsOwner == needsOwner
// @ ensures ok ==> next.Draft.PlanSource && next.Draft.ChangeSource && next.Draft.ReviewSource
// @ ensures next.Followup == w.Followup && next.CI == w.CI && next.CIFresh == w.CIFresh
// @ ensures next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed && next.RepairLimit == w.RepairLimit
// @ ensures next.Hold == w.Hold
func (w Workflow) DraftSucceeds(twoWay, executed, needsOwner bool) (next Workflow, ok bool) {
	next = w
	if !(w.Phase == Drafting) {
		return next, false
	}
	next.Draft = Classify(twoWay, executed, needsOwner)
	next.Phase = ReadyToCreate
	return next, true
}

// StartCreation starts or retries publication of the drafted decision.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == ReadyToCreate || w.Phase == CreateFailed)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == Creating
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI
// @ ensures next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed
// @ ensures next.RepairLimit == w.RepairLimit && next.Hold == w.Hold
func (w Workflow) StartCreation() (next Workflow, ok bool) {
	next = w
	if !(w.Phase == ReadyToCreate || w.Phase == CreateFailed) {
		return next, false
	}
	next.Phase = Creating
	return next, true
}

// CreationFails leaves the draft available for an automatic creation retry.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == Creating)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == CreateFailed
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI
// @ ensures next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed
// @ ensures next.RepairLimit == w.RepairLimit && next.Hold == w.Hold
func (w Workflow) CreationFails() (next Workflow, ok bool) {
	next = w
	if !(w.Phase == Creating) {
		return next, false
	}
	next.Phase = CreateFailed
	return next, true
}

// CreationSucceeds binds the one follow-up identity and emits its complete publication.
// @ requires w.Valid()
// @ requires 0 < id
// @ requires 0 <= specCount && specCount <= MaxSize
// @ requires 0 <= readmeCount && readmeCount <= MaxSize
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == Creating && w.Followup == 0)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == Open
// @ ensures ok ==> next.Followup == id
// @ ensures ok ==> next.CI == Pending
// @ ensures ok ==> next.CIFresh == false
// @ ensures ok ==> next.Conflict == false
// @ ensures next.Draft == w.Draft && next.RepairsUsed == w.RepairsUsed && next.RepairLimit == w.RepairLimit
// @ ensures next.Hold == w.Hold
// @ ensures ok ==> bundle.Command == "invariant decisions decide" && bundle.Journal == "newDecision" && bundle.LogRow == "newDecision"
// @ ensures ok ==> bundle.RolledUp && bundle.DecisionsFile && bundle.SpecFile && bundle.ReadmeFile
// @ ensures ok ==> len(bundle.SpecCitations) == specCount && len(bundle.ReadmeCitations) == readmeCount
// @ ensures forall i int :: { &bundle.SpecCitations[i] } 0 <= i && i < len(bundle.SpecCitations) ==> acc(&bundle.SpecCitations[i])
// @ ensures forall i int :: { &bundle.ReadmeCitations[i] } 0 <= i && i < len(bundle.ReadmeCitations) ==> acc(&bundle.ReadmeCitations[i])
// @ ensures ok ==> forall i int :: { bundle.SpecCitations[i] } 0 <= i && i < len(bundle.SpecCitations) ==> bundle.SpecCitations[i] == "newDecision"
// @ ensures ok ==> forall i int :: { bundle.ReadmeCitations[i] } 0 <= i && i < len(bundle.ReadmeCitations) ==> bundle.ReadmeCitations[i] == "newDecision"
// @ ensures !ok ==> len(bundle.SpecCitations) == 0 && len(bundle.ReadmeCitations) == 0
// @ ensures !ok ==> bundle.Command == "" && bundle.Journal == "" && bundle.LogRow == ""
// @ ensures !ok ==> !bundle.RolledUp && !bundle.DecisionsFile && !bundle.SpecFile && !bundle.ReadmeFile
func (w Workflow) CreationSucceeds(id, specCount, readmeCount int) (next Workflow, bundle Bundle, ok bool) {
	next = w
	if !(w.Phase == Creating && w.Followup == 0) {
		return next, bundle, false
	}
	bundle = RecordDecision(specCount, readmeCount)
	next.Followup = id
	next.Phase = Open
	next.CI = Pending
	next.CIFresh = false
	next.Conflict = false
	return next, bundle, true
}

// CheckCompletes accepts a result for the current contents, including while closed.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == ((w.Phase == Open || w.Phase == ClosedPhase) && w.CI == Pending)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.CI == (passed ? Passed : Failed)
// @ ensures ok ==> next.Conflict == hasConflict
// @ ensures ok ==> next.CIFresh == true
// @ ensures ok ==> next.Phase == (w.Phase == ClosedPhase ? ClosedPhase : (passed && !hasConflict ? Open : NeedsRepair))
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.RepairsUsed == w.RepairsUsed
// @ ensures next.RepairLimit == w.RepairLimit && next.Hold == w.Hold
func (w Workflow) CheckCompletes(passed, hasConflict bool) (next Workflow, ok bool) {
	next = w
	if !((w.Phase == Open || w.Phase == ClosedPhase) && w.CI == Pending) {
		return next, false
	}
	if passed {
		next.CI = Passed
	} else {
		next.CI = Failed
	}
	next.Conflict = hasConflict
	next.CIFresh = true
	if w.Phase != ClosedPhase {
		if passed && !hasConflict {
			next.Phase = Open
		} else {
			next.Phase = NeedsRepair
		}
	}
	return next, true
}

// ConflictAppears records a conflict caused by another merge.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == ((w.Phase == Open || w.Phase == ClosedPhase) && (w.CI == Pending || w.CI == Passed) && !w.Conflict)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Conflict == true
// @ ensures ok ==> next.Phase == (w.Phase == ClosedPhase ? ClosedPhase : NeedsRepair)
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI
// @ ensures next.CIFresh == w.CIFresh && next.RepairsUsed == w.RepairsUsed && next.RepairLimit == w.RepairLimit
// @ ensures next.Hold == w.Hold
func (w Workflow) ConflictAppears() (next Workflow, ok bool) {
	next = w
	if !((w.Phase == Open || w.Phase == ClosedPhase) && (w.CI == Pending || w.CI == Passed) && !w.Conflict) {
		return next, false
	}
	next.Conflict = true
	if w.Phase != ClosedPhase {
		next.Phase = NeedsRepair
	}
	return next, true
}

// Repair consumes one allowance and invalidates CI for the repaired contents.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == NeedsRepair && w.Hold == NoHold && w.RepairsUsed < w.RepairLimit)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.RepairsUsed == w.RepairsUsed + 1
// @ ensures ok ==> next.Phase == Open
// @ ensures ok ==> next.CI == Pending
// @ ensures ok ==> next.CIFresh == false
// @ ensures ok ==> next.Conflict == false
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.RepairLimit == w.RepairLimit
// @ ensures next.Hold == w.Hold
func (w Workflow) Repair() (next Workflow, ok bool) {
	next = w
	if !(w.Phase == NeedsRepair && w.Hold == NoHold && w.RepairsUsed < w.RepairLimit) {
		return next, false
	}
	next.RepairsUsed = w.RepairsUsed + 1
	next.Phase = Open
	next.CI = Pending
	next.CIFresh = false
	next.Conflict = false
	return next, true
}

// Block waits for a person once the repair allowance has been exhausted.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == NeedsRepair && w.RepairsUsed == w.RepairLimit)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == Blocked
// @ ensures ok ==> next.Hold == ExhaustedHold
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI
// @ ensures next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed
// @ ensures next.RepairLimit == w.RepairLimit
func (w Workflow) Block() (next Workflow, ok bool) {
	next = w
	if !(w.Phase == NeedsRepair && w.RepairsUsed == w.RepairLimit) {
		return next, false
	}
	next.Phase = Blocked
	next.Hold = ExhaustedHold
	return next, true
}

// Close records an explicit human closure without replacing the follow-up.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == Open || w.Phase == NeedsRepair || w.Phase == Blocked)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == ClosedPhase
// @ ensures ok ==> next.Hold == ClosedHold
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI
// @ ensures next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed
// @ ensures next.RepairLimit == w.RepairLimit
func (w Workflow) Close() (next Workflow, ok bool) {
	next = w
	if !(w.Phase == Open || w.Phase == NeedsRepair || w.Phase == Blocked) {
		return next, false
	}
	next.Phase = ClosedPhase
	next.Hold = ClosedHold
	return next, true
}

// Retry handles an explicit human request with a fresh repair allowance.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == Blocked)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == NeedsRepair
// @ ensures ok ==> next.Hold == NoHold
// @ ensures ok ==> next.RepairsUsed == 0
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI
// @ ensures next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairLimit == w.RepairLimit
func (w Workflow) Retry() (next Workflow, ok bool) {
	next = w
	if !(w.Phase == Blocked) {
		return next, false
	}
	next.Phase = NeedsRepair
	next.Hold = NoHold
	next.RepairsUsed = 0
	return next, true
}

// Resume reopens the same follow-up on request and renews its repair allowance.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == ClosedPhase)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == (w.CI == Failed || w.Conflict ? NeedsRepair : Open)
// @ ensures ok ==> next.Hold == NoHold
// @ ensures ok ==> next.RepairsUsed == 0
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI
// @ ensures next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairLimit == w.RepairLimit
func (w Workflow) Resume() (next Workflow, ok bool) {
	next = w
	if !(w.Phase == ClosedPhase) {
		return next, false
	}
	next.Phase = Open
	if w.CI == Failed || w.Conflict {
		next.Phase = NeedsRepair
	}
	next.Hold = NoHold
	next.RepairsUsed = 0
	return next, true
}

// AutoMerge accepts only passing CI for the current, conflict-free contents.
// @ requires w.Valid()
// @ ensures next.Valid()
// @ ensures ok == (w.Phase == Open && w.CI == Passed && w.CIFresh && !w.Conflict && w.Hold == NoHold)
// @ ensures !ok ==> next == w
// @ ensures ok ==> next.Phase == Merged
// @ ensures next.Draft == w.Draft && next.Followup == w.Followup && next.CI == w.CI
// @ ensures next.CIFresh == w.CIFresh && next.Conflict == w.Conflict && next.RepairsUsed == w.RepairsUsed
// @ ensures next.RepairLimit == w.RepairLimit && next.Hold == w.Hold
func (w Workflow) AutoMerge() (next Workflow, ok bool) {
	next = w
	if !(w.Phase == Open && w.CI == Passed && w.CIFresh && !w.Conflict && w.Hold == NoHold) {
		return next, false
	}
	next.Phase = Merged
	return next, true
}
