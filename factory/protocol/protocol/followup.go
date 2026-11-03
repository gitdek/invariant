// +gobra

package protocol

// Follow-up identities and revision stamps use the same unbounded integer
// representation as ordinary proposals and heads. Zero denotes no identity.
const NoPR = 0

const (
	FollowGateNone = 0
	FollowGatePass = 1
	FollowGateFail = 2
	FollowGateSkip = 3
)

// Only these phase distinctions are protocol policy. The Workflow owns all
// classification, effect processing and transitions between its opaque tokens.
const (
	CorePhaseInitial = 0
	CorePhaseOpen    = 1
	CorePhaseBlocked = 2
	CorePhaseClosed  = 3
	CorePhaseMerged  = 4
	CorePhaseReady   = 5
)

const (
	CoreOpen     = 0
	CoreClassify = 1
	CoreRepair   = 2
	CoreEffect   = 3
	CoreClose    = 4
	CoreRetry    = 5
	CoreResume   = 6
	CoreMerge    = 7
)

// Outcome names are opaque to authorization. These conventional names are
// convenient for callers; ApplyFollowupStep preserves every accepted outcome.
const (
	OutcomeNone   = 0
	OutcomeOK     = 1
	OutcomeFailed = 2
)

// Authority identifies the exact ratified plan, source PR and owed decision.
type Authority struct {
	Plan     int
	SourcePR int
	Decision int
}

// FollowupSource is the immutable part of a source observation needed for
// authorization. The caller retains its complete source history; follow-up
// operations never receive a mutable ordinary Issue or replace its history.
type FollowupSource struct {
	Present     bool
	IsPlan      bool
	Owes        bool
	Kind        int
	Proposal    int
	Ratified    int
	PrLock      int
	Head        int
	MergedHead  int
	PR          int
	Decision    int
	CandidatePR int
	Claim       Authority
}

// WorkflowView accompanies an opaque Workflow state token. These observations
// come from that independently proved core, not from a local transition table.
type WorkflowView struct {
	Token     int
	Phase     int
	Hold      bool
	Allowance int
	Repairs   int
}

// FollowupArtifacts represents path classes, not a finite list of files.
// Decisions covers all descendants of decisions/. ProjectCode includes every
// path outside decisions/, SPEC.md and README.md.
type FollowupArtifacts struct {
	Decisions   bool
	Spec        bool
	Readme      bool
	ProjectCode bool
}

// Followup has its own identity, revision, observations and core state. Its
// plan ratification and project lock start empty and are never manufactured.
// Receipt delivery, audit history and the resumed witness belong to the caller.
type Followup struct {
	PR               int
	Head             int
	CIHead           int
	Gate             int
	Decisions        bool
	Graph            bool
	ScopeHead        int
	Paths            FollowupArtifacts
	Tied             bool
	Target           int
	Core             WorkflowView
	MergedHead       int
	Outcome          int
	PlanRatification int
	ProjectLock      int
}

// FollowupContext binds acceptance to the entire request. In particular, a
// changed CI or scope observation invalidates an outstanding receipt even if
// the head and the Workflow token have not changed.
type FollowupContext struct {
	Authority     Authority
	SourcePR      int
	PR            int
	Head          int
	Core          WorkflowView
	Actor         int
	Authenticated bool
	CIHead        int
	Gate          int
	Decisions     bool
	Graph         bool
	ScopeHead     int
	Paths         FollowupArtifacts
	Tied          bool
	Target        int
}

// WorkflowReceipt is delivered by the trusted Workflow boundary. Accepted
// means the amended Workflow accepted this transition for this exact Context.
// This package neither issues that acceptance nor infers it from a branch,
// claimed source, phase or a context-free before/after tuple. Production
// adapters must authenticate this receipt at their Workflow boundary.
type WorkflowReceipt struct {
	Present   bool
	Accepted  bool
	Before    WorkflowView
	Operation int
	Outcome   int
	After     WorkflowView
	Context   FollowupContext
}

// NewFollowup is EmptyFollow, with the Workflow's initial view and a trusted
// initial target supplied by the environment.
// @ requires true
// @ decreases
// @ pure
func NewFollowup(initial WorkflowView, target int) Followup {
	return Followup{PR: NoPR, Head: NoHead, CIHead: NoHead,
		Gate: FollowGateNone, Decisions: false, Graph: false, ScopeHead: NoHead,
		Paths: FollowupArtifacts{}, Tied: false, Target: target, Core: initial,
		MergedHead: NoHead, Outcome: OutcomeNone, PlanRatification: NoP, ProjectLock: NoP}
}

// OrdinaryAllowed is the OrdinaryStep guard. Ordinary operations remain
// available until a distinct follow-up PR becomes active.
// @ requires true
// @ decreases
// @ pure
func OrdinaryAllowed(f Followup) bool {
	return f.PR == NoPR
}

// EligibleFollowupSource requires the exact merged, ratified, owing plan.
// expectedSourcePR comes from the independently verified obligation binding.
// @ requires true
// @ decreases
// @ pure
func EligibleFollowupSource(s FollowupSource, expectedSourcePR int) bool {
	return s.Present && s.IsPlan && s.Owes && s.Kind == KindMerged &&
		s.Ratified != NoP && s.Proposal == s.Ratified && s.PrLock == s.Ratified &&
		s.MergedHead == s.Head && s.MergedHead != NoHead && s.PR == expectedSourcePR
}

// VerifiedFollowupAssociation compares the claim with the observed source
// authority and the independent expected follow-up identity. A claimed source
// or branch name alone cannot establish this association.
// @ requires true
// @ decreases
// @ pure
func VerifiedFollowupAssociation(s FollowupSource, expectedFollowPR int) bool {
	return s.Claim == (Authority{Plan: s.Ratified, SourcePR: s.PR, Decision: s.Decision}) &&
		s.CandidatePR == expectedFollowPR && s.CandidatePR != s.PR
}

// @ requires true
// @ decreases
// @ pure
func FollowupScopeOK(f Followup) bool {
	return f.ScopeHead == f.Head && !f.Paths.ProjectCode && f.Tied
}

// A skipped project gate is admissible only with current successful decision
// and graph checks and an independently verified narrow scope.
// @ requires true
// @ decreases
// @ pure
func FollowupChecksOK(f Followup) bool {
	return f.CIHead == f.Head && f.Decisions && f.Graph &&
		(f.Gate == FollowGatePass || f.Gate == FollowGateSkip) &&
		(f.Gate != FollowGateSkip || FollowupScopeOK(f))
}

// @ requires true
// @ decreases
// @ pure
func FollowupContextFor(f Followup, s FollowupSource, actor Actor, authenticated bool) FollowupContext {
	return FollowupContext{Authority: s.Claim, SourcePR: s.PR, PR: s.CandidatePR,
		Head: f.Head, Core: f.Core, Actor: actor.ID, Authenticated: authenticated,
		CIHead: f.CIHead, Gate: f.Gate, Decisions: f.Decisions, Graph: f.Graph,
		ScopeHead: f.ScopeHead, Paths: f.Paths, Tied: f.Tied, Target: f.Target}
}

// auditClear records that the caller has not already applied this receipt.
// Delivery/discard and audit storage remain environment operations.
// @ requires true
// @ decreases
// @ pure
func MatchingWorkflowReceipt(f Followup, s FollowupSource, receipt WorkflowReceipt, actor Actor, authenticated bool, auditClear bool) bool {
	return receipt.Present && receipt.Accepted && auditClear && receipt.Before == f.Core &&
		receipt.Context == FollowupContextFor(f, s, actor, authenticated)
}

// @ requires true
// @ decreases
// @ pure
func FollowupStepAuthority(f Followup, s FollowupSource, receipt WorkflowReceipt, expectedSourcePR int, expectedFollowPR int) bool {
	return EligibleFollowupSource(s, expectedSourcePR) && VerifiedFollowupAssociation(s, expectedFollowPR) &&
		((receipt.Operation == CoreOpen && f.PR == NoPR) || (receipt.Operation != CoreOpen && f.PR != NoPR))
}

// FollowupStepPolicy checks only the integration's ratified boundary. The
// Workflow still owns classification, retries, repairs and effect outcomes.
// trustedTarget is the environment's membership check for f.Target in its
// trusted base set; it must not be inferred from the proposed branch name.
// Actor membership facts must be authoritative for actor.ID, as with ordinary
// command authorization; the receipt binds that identity and authentication.
// @ requires true
// @ decreases
// @ pure
func FollowupStepPolicy(f Followup, receipt WorkflowReceipt, actor Actor, authenticated bool, trustedTarget bool) bool {
	return 0 <= receipt.After.Repairs && receipt.After.Repairs <= 2 &&
		(receipt.After.Phase != CorePhaseClosed || receipt.After.Hold) &&
		(!receipt.Before.Hold || receipt.After.Hold || receipt.Operation == CoreResume) &&
		((receipt.Operation != CoreRetry && receipt.Operation != CoreResume) || (actor.Director() && authenticated)) &&
		(receipt.Operation != CoreRetry || (receipt.Before.Phase == CorePhaseBlocked &&
			receipt.After.Allowance != receipt.Before.Allowance && receipt.After.Repairs == 0)) &&
		(receipt.Operation != CoreResume || (receipt.Before.Phase == CorePhaseClosed && f.PR != NoPR)) &&
		(receipt.After.Phase != CorePhaseMerged || (receipt.Operation == CoreMerge &&
			!receipt.Before.Hold && !receipt.After.Hold && FollowupScopeOK(f) && FollowupChecksOK(f) && trustedTarget))
}

// @ requires true
// @ decreases
// @ pure
func FollowupStepEnabled(f Followup, s FollowupSource, receipt WorkflowReceipt, actor Actor, authenticated bool, expectedSourcePR int, expectedFollowPR int, trustedTarget bool, auditClear bool) bool {
	return MatchingWorkflowReceipt(f, s, receipt, actor, authenticated, auditClear) &&
		FollowupStepAuthority(f, s, receipt, expectedSourcePR, expectedFollowPR) &&
		FollowupStepPolicy(f, receipt, actor, authenticated, trustedTarget)
}

// ApplyFollowupStep commits exactly the accepted core state and outcome.
// The caller supplies maintained protocol state and authoritative source,
// actor and association observations, rather than caller-authored claims.
// A refusal returns the entire input unchanged. Success changes only the core,
// accepted outcome, initial PR identity and the head recorded by a merge.
// @ requires true
// @ ensures ok == FollowupStepEnabled(f, s, receipt, actor, authenticated, expectedSourcePR, expectedFollowPR, trustedTarget, auditClear)
// @ ensures !ok ==> next == f
// @ ensures ok ==> next.Core == receipt.After && next.Outcome == receipt.Outcome
// @ ensures ok && receipt.Operation == CoreOpen ==> next.PR == s.CandidatePR
// @ ensures ok && receipt.Operation != CoreOpen ==> next.PR == f.PR
// @ ensures ok && receipt.After.Phase == CorePhaseMerged ==> next.MergedHead == f.Head
// @ ensures ok && receipt.After.Phase != CorePhaseMerged ==> next.MergedHead == f.MergedHead
// @ ensures next.Head == f.Head && next.CIHead == f.CIHead && next.Gate == f.Gate
// @ ensures next.Decisions == f.Decisions && next.Graph == f.Graph && next.ScopeHead == f.ScopeHead
// @ ensures next.Paths == f.Paths && next.Tied == f.Tied && next.Target == f.Target
// @ ensures next.PlanRatification == f.PlanRatification && next.ProjectLock == f.ProjectLock
func ApplyFollowupStep(f Followup, s FollowupSource, receipt WorkflowReceipt, actor Actor, authenticated bool, expectedSourcePR int, expectedFollowPR int, trustedTarget bool, auditClear bool) (next Followup, ok bool) {
	if !FollowupStepEnabled(f, s, receipt, actor, authenticated, expectedSourcePR, expectedFollowPR, trustedTarget, auditClear) {
		return f, false
	}
	next = f
	next.Core = receipt.After
	next.Outcome = receipt.Outcome
	if receipt.Operation == CoreOpen {
		next.PR = s.CandidatePR
	}
	if receipt.After.Phase == CorePhaseMerged {
		next.MergedHead = f.Head
	}
	return next, true
}

// ObserveFollowupCI stamps independent CI evidence with the current head.
// The caller clears its audit, retaining any now-stale acceptance receipt.
// @ requires true
// @ ensures ok == (sourcePresent && f.CIHead != f.Head && f.Core.Phase != CorePhaseMerged)
// @ ensures !ok ==> next == f
// @ ensures ok ==> next.CIHead == f.Head && next.Gate == result && next.Decisions == decisions && next.Graph == graph
// @ ensures next.PR == f.PR && next.Head == f.Head && next.ScopeHead == f.ScopeHead && next.Paths == f.Paths
// @ ensures next.Tied == f.Tied && next.Target == f.Target && next.Core == f.Core && next.MergedHead == f.MergedHead
// @ ensures next.Outcome == f.Outcome && next.PlanRatification == f.PlanRatification && next.ProjectLock == f.ProjectLock
func ObserveFollowupCI(f Followup, sourcePresent bool, result int, decisions bool, graph bool) (next Followup, ok bool) {
	if !sourcePresent || f.CIHead == f.Head || f.Core.Phase == CorePhaseMerged {
		return f, false
	}
	next = f
	next.CIHead = f.Head
	next.Gate = result
	next.Decisions = decisions
	next.Graph = graph
	return next, true
}

// ObserveFollowupScope stamps independently verified scope at this head.
// @ requires true
// @ ensures ok == (sourcePresent && f.ScopeHead != f.Head && f.Core.Phase != CorePhaseMerged)
// @ ensures !ok ==> next == f
// @ ensures ok ==> next.ScopeHead == f.Head
// @ ensures next.PR == f.PR && next.Head == f.Head && next.CIHead == f.CIHead && next.Gate == f.Gate
// @ ensures next.Decisions == f.Decisions && next.Graph == f.Graph && next.Paths == f.Paths
// @ ensures next.Tied == f.Tied && next.Target == f.Target && next.Core == f.Core && next.MergedHead == f.MergedHead
// @ ensures next.Outcome == f.Outcome && next.PlanRatification == f.PlanRatification && next.ProjectLock == f.ProjectLock
func ObserveFollowupScope(f Followup, sourcePresent bool) (next Followup, ok bool) {
	if !sourcePresent || f.ScopeHead == f.Head || f.Core.Phase == CorePhaseMerged {
		return f, false
	}
	next = f
	next.ScopeHead = f.Head
	return next, true
}

// PushFollowup accepts arbitrary new revision identities. It retains previous
// CI and scope revision stamps, making stale evidence observable. The explorer
// supplies its own finite set of revisions and path-class observations.
// As with ordinary Push, newHead is a member of the caller's revision domain
// (which excludes NoHead); domain membership is an environment obligation.
// @ requires true
// @ ensures ok == (sourcePresent && f.PR != NoPR && f.Core.Phase != CorePhaseMerged && newHead != f.Head)
// @ ensures !ok ==> next == f
// @ ensures ok ==> next.Head == newHead && next.Paths == paths && next.Tied == tied && next.Target == target
// @ ensures next.PR == f.PR && next.CIHead == f.CIHead && next.Gate == f.Gate
// @ ensures next.Decisions == f.Decisions && next.Graph == f.Graph && next.ScopeHead == f.ScopeHead
// @ ensures next.Core == f.Core && next.MergedHead == f.MergedHead && next.Outcome == f.Outcome
// @ ensures next.PlanRatification == f.PlanRatification && next.ProjectLock == f.ProjectLock
func PushFollowup(f Followup, sourcePresent bool, newHead int, paths FollowupArtifacts, tied bool, target int) (next Followup, ok bool) {
	if !sourcePresent || f.PR == NoPR || f.Core.Phase == CorePhaseMerged || newHead == f.Head {
		return f, false
	}
	next = f
	next.Head = newHead
	next.Paths = paths
	next.Tied = tied
	next.Target = target
	return next, true
}
