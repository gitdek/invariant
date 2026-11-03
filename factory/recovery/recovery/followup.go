// +gobra

package recovery

// FollowSnapshot is an accepted Workflow decision, retained before the next
// effect. The source obligation and branch/PR identity outlive every operation.
type FollowSnapshot struct {
	Source, Branch, PR string
	Op                 int
	Kind               string
	Head, Left, Round  int
	Cause, Stage       string
}

// FollowEffect records intent separately from dispatch and reconciliation.
// Operations own five effect slots: run, publication, merge, reopen, answer.
type FollowEffect struct {
	Intent                        int
	Pending, Completed, Cancelled bool
	Dispatches                    int
}

// FollowRepo contains only durable recovery state. The caller provides storage
// capacities; these are storage limits, never Workflow's repair allowance.
// Operation zero is reserved for the writer's command answer. Slice storage is
// exclusively owned: callers retain old versions by copying before Step.
type FollowRepo struct {
	Source, Branch, PR                                  string
	Phase, Next                                         string
	Op, Left, Round, Consumed, Answers, Owner, RunFence int
	KnownPR                                             bool
	Head                                                int
	CI                                                  string
	CIHead                                              int
	CanMerge                                            bool
	MergeHead                                           int
	MergeOutcome, Proof, Status, Cause                  string
	Kinds                                               []string
	Bases, Rounds, Starts                               []int
	Raw, Results                                        []string
	ResultSnapshots                                     []int
	Charged                                             []bool
	Snapshots                                           []FollowSnapshot
	SnapshotCount                                       int
	Effects                                             []FollowEffect
}

// FollowInput is an observation at a transition boundary, not persisted world
// state. Result refers to the recorded operation whose result is being accepted.
type FollowInput struct {
	Watcher, Lease, Fence                         int
	Operation                                     int
	Up, Requested                                 bool
	RemotePR                                      string
	RemoteHead                                    int
	RemoteCI                                      string
	RemoteCIHead                                  int
	RemoteCanMerge                                bool
	RemoteMergeHead                               int
	RemoteMerge                                   string
	Publication                                   int
	PublicationRefused, MergeLost, ExternalMerged bool
	Result                                        string
}

const (
	FollowRunEffect         = 0
	FollowPublicationEffect = 1
	FollowMergeEffect       = 2
	FollowReopenEffect      = 3
	FollowAnswerEffect      = 4
	FollowEffectKinds       = 5
)

// Dispatchable requires the stored intent and excludes every settled effect.
// @ decreases
// @ pure
func (e FollowEffect) Dispatchable(snapshotCount int) bool {
	return e.Intent > 0 && e.Intent <= snapshotCount && e.Pending &&
		!e.Completed && !e.Cancelled && e.Dispatches == 0
}

// FollowPendingOnlyAnswer checks the actual ledger before observations can
// settle a PR. The recursive form exposes the entire scan to the verifier.
// @ requires 0 <= n && n <= len(effects)
// @ requires forall i int :: { effects[i] } 0 <= i && i < len(effects) ==> acc(&effects[i], 1/2)
// @ decreases n
// @ pure
func FollowPendingOnlyAnswer(effects []FollowEffect, n int) bool {
	return n == 0 || ((n-1 == FollowAnswerEffect || !effects[n-1].Pending) && FollowPendingOnlyAnswer(effects, n-1))
}

// NewFollowRepo allocates caller-sized durable storage for one source obligation.
// @ requires 0 <= operationCapacity && operationCapacity < MaxAttempt / FollowEffectKinds
// @ requires 0 <= snapshotCapacity && snapshotCapacity <= MaxAttempt
// @ ensures r.Shape()
// @ ensures r.Source == source && r.Branch == branch && r.PR == pr
// @ ensures r.Phase == "idle" && r.Next == "draft" && r.Left == 2 && r.Owner == NoOne
// @ ensures r.Op == 0 && r.Round == 0 && r.Consumed == 0 && r.Answers == 0 && r.SnapshotCount == 0
// @ ensures !r.KnownPR && r.Head == 0 && r.CI == "none" && r.CIHead == 0 && !r.CanMerge && r.MergeHead == 0
// @ ensures r.RunFence == 0 && r.MergeOutcome == "none" && r.Proof == "none" && r.Status == "active" && r.Cause == "none"
// @ ensures len(r.Kinds) == operationCapacity + 1 && len(r.Snapshots) == snapshotCapacity
// @ ensures forall i int :: { r.Kinds[i] } 0 <= i && i < len(r.Kinds) ==> acc(&r.Kinds[i])
// @ ensures forall i int :: { r.Bases[i] } 0 <= i && i < len(r.Bases) ==> acc(&r.Bases[i])
// @ ensures forall i int :: { r.Rounds[i] } 0 <= i && i < len(r.Rounds) ==> acc(&r.Rounds[i])
// @ ensures forall i int :: { r.Starts[i] } 0 <= i && i < len(r.Starts) ==> acc(&r.Starts[i])
// @ ensures forall i int :: { r.Raw[i] } 0 <= i && i < len(r.Raw) ==> acc(&r.Raw[i])
// @ ensures forall i int :: { r.Results[i] } 0 <= i && i < len(r.Results) ==> acc(&r.Results[i])
// @ ensures forall i int :: { r.ResultSnapshots[i] } 0 <= i && i < len(r.ResultSnapshots) ==> acc(&r.ResultSnapshots[i])
// @ ensures forall i int :: { r.Charged[i] } 0 <= i && i < len(r.Charged) ==> acc(&r.Charged[i])
// @ ensures forall i int :: { r.Snapshots[i] } 0 <= i && i < len(r.Snapshots) ==> acc(&r.Snapshots[i])
// @ ensures forall i int :: { r.Effects[i] } 0 <= i && i < len(r.Effects) ==> acc(&r.Effects[i])
// @ ensures r.Kinds[0] == "none"
// @ ensures forall i int :: { r.Effects[i] } 0 <= i && i < len(r.Effects) ==> r.Effects[i] == FollowEffect{}
func NewFollowRepo(source, branch, pr string, operationCapacity, snapshotCapacity int) (r FollowRepo) {
	r.Source, r.Branch, r.PR = source, branch, pr
	r.Phase, r.Next, r.Left, r.Owner = "idle", "draft", 2, NoOne
	r.CI, r.MergeOutcome, r.Proof, r.Status, r.Cause = "none", "none", "none", "active", "none"
	r.Kinds = make([]string, operationCapacity+1)
	r.Bases = make([]int, operationCapacity+1)
	r.Rounds = make([]int, operationCapacity+1)
	r.Starts = make([]int, operationCapacity+1)
	r.Raw = make([]string, operationCapacity+1)
	r.Results = make([]string, operationCapacity+1)
	r.ResultSnapshots = make([]int, operationCapacity+1)
	r.Charged = make([]bool, operationCapacity+1)
	r.Snapshots = make([]FollowSnapshot, snapshotCapacity)
	r.Effects = make([]FollowEffect, (operationCapacity+1)*FollowEffectKinds)
	r.Kinds[0] = "none"
	return r
}

// Shape describes storage, without imposing any model-checking bound.
// @ decreases
// @ pure
func (r FollowRepo) Shape() bool {
	return 0 <= r.Op && r.Op < len(r.Kinds) &&
		len(r.Kinds) <= MaxAttempt/FollowEffectKinds && len(r.Snapshots) <= MaxAttempt &&
		len(r.Bases) == len(r.Kinds) && len(r.Rounds) == len(r.Kinds) &&
		len(r.Starts) == len(r.Kinds) && len(r.Raw) == len(r.Kinds) &&
		len(r.Results) == len(r.Kinds) && len(r.ResultSnapshots) == len(r.Kinds) &&
		len(r.Charged) == len(r.Kinds) && len(r.Effects) == len(r.Kinds)*FollowEffectKinds &&
		0 <= r.SnapshotCount && r.SnapshotCount <= len(r.Snapshots)
}

// CanStep includes the Workflow answer/renewal priority and lease fence. Remote
// choices and their exploration bounds are deliberately absent from this core.
// @ requires r.Shape()
// @ requires forall i int :: { r.Kinds[i] } 0 <= i && i < len(r.Kinds) ==> acc(&r.Kinds[i], 1/2)
// @ requires forall i int :: { r.Bases[i] } 0 <= i && i < len(r.Bases) ==> acc(&r.Bases[i], 1/2)
// @ requires forall i int :: { r.Starts[i] } 0 <= i && i < len(r.Starts) ==> acc(&r.Starts[i], 1/2)
// @ requires forall i int :: { r.ResultSnapshots[i] } 0 <= i && i < len(r.ResultSnapshots) ==> acc(&r.ResultSnapshots[i], 1/2)
// @ requires forall i int :: { r.Effects[i] } 0 <= i && i < len(r.Effects) ==> acc(&r.Effects[i], 1/2)
// @ decreases
// @ pure
func (r FollowRepo) CanStep(action string, x FollowInput) bool {
	return 0 <= x.Watcher && 0 <= x.Fence && x.Up && x.Lease == x.Watcher &&
		(action == "FStart" || action == "FFinish" || action == "FRecoverStopped" ||
			action == "FPublish" || action == "FPublicationRefusal" || action == "FRecordAnswer" ||
			action == "FAnswer" || action == "FReopen" || action == "FMerge" || r.SnapshotCount < len(r.Snapshots)) &&
		((action == "FConsumeRequest" && x.Requested && r.Consumed == 0 && 0 <= r.Round && r.Round < MaxAttempt &&
			!(r.Kinds[r.Op] == "repair" && r.ResultSnapshots[r.Op] == 0) &&
			(r.Phase == "hold" || r.Phase == "idle" || r.Phase == "watch" || r.Phase == "done")) ||
			(r.Consumed > 0 && r.Answers == 0 &&
				((action == "FRecordAnswer" && r.Effects[FollowAnswerEffect].Intent == 0) ||
					(action == "FAnswer" && r.Effects[FollowAnswerEffect].Dispatchable(r.SnapshotCount)))) ||
			(!(r.Consumed > 0 && r.Answers == 0) &&
				!(x.Requested && r.Consumed == 0 &&
					!(r.Kinds[r.Op] == "repair" && r.ResultSnapshots[r.Op] == 0) &&
					(r.Phase == "hold" || r.Phase == "idle" || r.Phase == "watch" || r.Phase == "done")) &&
				((action == "FAuthorize" && r.Phase == "idle" && !(x.Requested && r.Consumed == 0) &&
					r.Op+1 < len(r.Kinds) && r.Status == "active" &&
					(r.Next != "repair" || (r.Left > 0 && x.RemotePR == "open")) &&
					(r.Next != "merge" || (x.RemotePR == "open" && r.Head == x.RemoteHead)) &&
					(r.Next != "reopen" || (r.Consumed > 0 && x.RemotePR == "closed"))) ||
					(action == "FStart" && r.Phase == "start-run" && r.Starts[r.Op] == 0 && r.Effects[r.Op*FollowEffectKinds+FollowRunEffect].Dispatchable(r.SnapshotCount) &&
						(r.Kinds[r.Op] != "repair" || (x.RemotePR == "open" && r.Bases[r.Op] == x.RemoteHead))) ||
					(action == "FFinish" && x.Operation == r.Op && r.Phase == "running" &&
						r.Owner == x.Watcher && r.RunFence == x.Fence &&
						(x.Result == "success" || x.Result == "failed" || x.Result == "stopped" || x.Result == "nochange")) ||
					(action == "FRecoverStopped" && r.Phase == "running" && (r.Owner != x.Watcher || r.RunFence != x.Fence)) ||
					(action == "FReconcileRun" && r.Phase == "run-result") ||
					(action == "FPublish" && r.Phase == "start-publication" && r.Effects[r.Op*FollowEffectKinds+FollowPublicationEffect].Dispatchable(r.SnapshotCount) &&
						(r.Kinds[r.Op] != "create" || x.RemotePR == "none") &&
						(r.Kinds[r.Op] != "repair" || (x.RemotePR == "open" && r.Bases[r.Op] == x.RemoteHead))) ||
					(action == "FReconcileSuperseded" &&
						(r.Phase == "record-run" || r.Phase == "start-run" || r.Phase == "record-publication" ||
							r.Phase == "start-publication" || r.Phase == "record-merge" || r.Phase == "start-merge") &&
						(r.Kinds[r.Op] == "repair" || r.Kinds[r.Op] == "merge") &&
						(r.Bases[r.Op] != x.RemoteHead || x.RemotePR != "open")) ||
					(action == "FPublicationRefusal" && r.Phase == "start-publication" && r.Kinds[r.Op] == "create" && r.Effects[r.Op*FollowEffectKinds+FollowPublicationEffect].Dispatchable(r.SnapshotCount)) ||
					(action == "FReconcileCreationRefusal" && x.Operation == r.Op && r.Phase == "publication" && x.PublicationRefused) ||
					(action == "FReconcilePublication" && x.Operation == r.Op && r.Phase == "publication" && x.Publication > 0) ||
					(action == "FObservePR" &&
						FollowPendingOnlyAnswer(r.Effects, len(r.Effects)) &&
						(r.Phase == "watch" || r.Phase == "idle" || r.Phase == "hold" || r.Phase == "done") &&
						r.Status != "merged" && !(r.Status == "closed" && x.RemotePR == "closed") &&
						!(r.Phase == "idle" && r.Next == "reopen" && r.Consumed > 0) &&
						(x.RemotePR == "closed" || x.RemotePR == "merged")) ||
					(action == "FObserveCI" && r.Phase == "watch" && x.RemotePR == "open" &&
						x.RemoteCI != "none" && x.RemoteCIHead == r.Head && x.RemoteMergeHead == r.Head) ||
					(action == "FReopen" && r.Phase == "start-reopen" && x.RemotePR == "closed" && r.Consumed > 0 && r.Effects[r.Op*FollowEffectKinds+FollowReopenEffect].Dispatchable(r.SnapshotCount)) ||
					(action == "FReconcileReopen" && r.Phase == "reopen-response") ||
					(action == "FMerge" && r.Phase == "start-merge" && r.Effects[r.Op*FollowEffectKinds+FollowMergeEffect].Dispatchable(r.SnapshotCount) &&
						(x.Result == "success" || x.Result == "refused" || x.Result == "unknown") &&
						x.RemotePR == "open" && r.Head == x.RemoteHead &&
						r.CI == "pass" && r.CIHead == r.Head && r.CanMerge && r.MergeHead == r.Head) ||
					(action == "FReconcileMerge" && x.Operation == r.Op &&
						(r.Phase == "merge-response" || (r.Phase == "uncertain" && (x.RemoteMerge != "unknown" || x.RemotePR == "merged")))) ||
					(action == "FObserveHead" &&
						(r.Phase == "watch" || r.Phase == "idle" || r.Phase == "hold" || r.Phase == "done") &&
						r.Head != x.RemoteHead && (x.RemotePR == "open" || x.RemotePR == "closed")))))
}

// sameValues is the frame for all non-storage fields.
// @ decreases
// @ pure
func (r FollowRepo) sameValues(s FollowRepo) bool {
	return r.Source == s.Source &&
		r.Branch == s.Branch &&
		r.PR == s.PR &&
		r.Phase == s.Phase &&
		r.Next == s.Next &&
		r.Op == s.Op &&
		r.Left == s.Left &&
		r.Round == s.Round &&
		r.Consumed == s.Consumed &&
		r.Answers == s.Answers &&
		r.Owner == s.Owner &&
		r.RunFence == s.RunFence &&
		r.KnownPR == s.KnownPR &&
		r.Head == s.Head &&
		r.CI == s.CI &&
		r.CIHead == s.CIHead &&
		r.CanMerge == s.CanMerge &&
		r.MergeHead == s.MergeHead &&
		r.MergeOutcome == s.MergeOutcome &&
		r.Proof == s.Proof &&
		r.Status == s.Status &&
		r.Cause == s.Cause &&
		r.SnapshotCount == s.SnapshotCount
}

// FollowWritesSnapshot identifies the accepted Workflow transitions.
// @ decreases
// @ pure
func FollowWritesSnapshot(action string) bool {
	return action == "FAuthorize" ||
		action == "FReconcileRun" ||
		action == "FReconcileSuperseded" ||
		action == "FReconcileCreationRefusal" ||
		action == "FReconcilePublication" ||
		action == "FObservePR" ||
		action == "FObserveCI" ||
		action == "FConsumeRequest" ||
		action == "FReconcileReopen" ||
		action == "FReconcileMerge" ||
		action == "FObserveHead"
}

// Step applies one authorized system transition. Its slice storage belongs to
// this durable record; publication/merge observations are supplied, not invented.
// @ requires r.Shape()
// @ requires forall i int :: { r.Kinds[i] } 0 <= i && i < len(r.Kinds) ==> acc(&r.Kinds[i])
// @ requires forall i int :: { r.Bases[i] } 0 <= i && i < len(r.Bases) ==> acc(&r.Bases[i])
// @ requires forall i int :: { r.Rounds[i] } 0 <= i && i < len(r.Rounds) ==> acc(&r.Rounds[i])
// @ requires forall i int :: { r.Starts[i] } 0 <= i && i < len(r.Starts) ==> acc(&r.Starts[i])
// @ requires forall i int :: { r.Raw[i] } 0 <= i && i < len(r.Raw) ==> acc(&r.Raw[i])
// @ requires forall i int :: { r.Results[i] } 0 <= i && i < len(r.Results) ==> acc(&r.Results[i])
// @ requires forall i int :: { r.ResultSnapshots[i] } 0 <= i && i < len(r.ResultSnapshots) ==> acc(&r.ResultSnapshots[i])
// @ requires forall i int :: { r.Charged[i] } 0 <= i && i < len(r.Charged) ==> acc(&r.Charged[i])
// @ requires forall i int :: { r.Snapshots[i] } 0 <= i && i < len(r.Snapshots) ==> acc(&r.Snapshots[i])
// @ requires forall i int :: { r.Effects[i] } 0 <= i && i < len(r.Effects) ==> acc(&r.Effects[i])
// @ ensures ok == old(r.CanStep(action, x))
// @ ensures s.Shape()
// @ ensures len(s.Kinds) == len(r.Kinds)
// @ ensures forall i int :: { s.Kinds[i] } 0 <= i && i < len(s.Kinds) ==> acc(&s.Kinds[i])
// @ ensures len(s.Bases) == len(r.Bases)
// @ ensures forall i int :: { s.Bases[i] } 0 <= i && i < len(s.Bases) ==> acc(&s.Bases[i])
// @ ensures len(s.Rounds) == len(r.Rounds)
// @ ensures forall i int :: { s.Rounds[i] } 0 <= i && i < len(s.Rounds) ==> acc(&s.Rounds[i])
// @ ensures len(s.Starts) == len(r.Starts)
// @ ensures forall i int :: { s.Starts[i] } 0 <= i && i < len(s.Starts) ==> acc(&s.Starts[i])
// @ ensures len(s.Raw) == len(r.Raw)
// @ ensures forall i int :: { s.Raw[i] } 0 <= i && i < len(s.Raw) ==> acc(&s.Raw[i])
// @ ensures len(s.Results) == len(r.Results)
// @ ensures forall i int :: { s.Results[i] } 0 <= i && i < len(s.Results) ==> acc(&s.Results[i])
// @ ensures len(s.ResultSnapshots) == len(r.ResultSnapshots)
// @ ensures forall i int :: { s.ResultSnapshots[i] } 0 <= i && i < len(s.ResultSnapshots) ==> acc(&s.ResultSnapshots[i])
// @ ensures len(s.Charged) == len(r.Charged)
// @ ensures forall i int :: { s.Charged[i] } 0 <= i && i < len(s.Charged) ==> acc(&s.Charged[i])
// @ ensures len(s.Snapshots) == len(r.Snapshots)
// @ ensures forall i int :: { s.Snapshots[i] } 0 <= i && i < len(s.Snapshots) ==> acc(&s.Snapshots[i])
// @ ensures len(s.Effects) == len(r.Effects)
// @ ensures forall i int :: { s.Effects[i] } 0 <= i && i < len(s.Effects) ==> acc(&s.Effects[i])
// @ ensures s.Source == r.Source && s.Branch == r.Branch && s.PR == r.PR
// @ ensures s.Op == r.Op || s.Op == r.Op + 1
// @ ensures s.SnapshotCount == r.SnapshotCount || s.SnapshotCount == r.SnapshotCount + 1
// @ ensures !ok ==> r.sameValues(s)
// @ ensures !ok ==> forall i int :: { s.Kinds[i] } 0 <= i && i < len(s.Kinds) ==> s.Kinds[i] == old(r.Kinds[i])
// @ ensures !ok ==> forall i int :: { s.Bases[i] } 0 <= i && i < len(s.Bases) ==> s.Bases[i] == old(r.Bases[i])
// @ ensures !ok ==> forall i int :: { s.Rounds[i] } 0 <= i && i < len(s.Rounds) ==> s.Rounds[i] == old(r.Rounds[i])
// @ ensures !ok ==> forall i int :: { s.Starts[i] } 0 <= i && i < len(s.Starts) ==> s.Starts[i] == old(r.Starts[i])
// @ ensures !ok ==> forall i int :: { s.Raw[i] } 0 <= i && i < len(s.Raw) ==> s.Raw[i] == old(r.Raw[i])
// @ ensures !ok ==> forall i int :: { s.Results[i] } 0 <= i && i < len(s.Results) ==> s.Results[i] == old(r.Results[i])
// @ ensures !ok ==> forall i int :: { s.ResultSnapshots[i] } 0 <= i && i < len(s.ResultSnapshots) ==> s.ResultSnapshots[i] == old(r.ResultSnapshots[i])
// @ ensures !ok ==> forall i int :: { s.Charged[i] } 0 <= i && i < len(s.Charged) ==> s.Charged[i] == old(r.Charged[i])
// @ ensures !ok ==> forall i int :: { s.Snapshots[i] } 0 <= i && i < len(s.Snapshots) ==> s.Snapshots[i] == old(r.Snapshots[i])
// @ ensures !ok ==> forall i int :: { s.Effects[i] } 0 <= i && i < len(s.Effects) ==> s.Effects[i] == old(r.Effects[i])
// @ ensures ok && FollowWritesSnapshot(action) ==> s.SnapshotCount == r.SnapshotCount + 1
// @ ensures !ok || !FollowWritesSnapshot(action) ==> s.SnapshotCount == r.SnapshotCount
// @ ensures forall i int :: { s.Snapshots[i] } 0 <= i && i < len(s.Snapshots) && i != r.SnapshotCount ==> s.Snapshots[i] == old(r.Snapshots[i])
// @ ensures s.SnapshotCount == r.SnapshotCount ==> forall i int :: { s.Snapshots[i] } 0 <= i && i < len(s.Snapshots) ==> s.Snapshots[i] == old(r.Snapshots[i])
// @ ensures s.SnapshotCount > r.SnapshotCount ==> s.Snapshots[r.SnapshotCount].Source == r.Source && s.Snapshots[r.SnapshotCount].Branch == r.Branch && s.Snapshots[r.SnapshotCount].PR == r.PR
// @ ensures !ok || action != "FAuthorize" ==> s.Op == r.Op
// @ ensures !ok || action != "FConsumeRequest" ==> s.Round == r.Round && s.Consumed == r.Consumed
// @ ensures !ok || action != "FAnswer" ==> s.Answers == r.Answers
// @ ensures !ok || (action != "FAuthorize" && action != "FConsumeRequest") ==> s.Left == r.Left
// @ ensures !ok || action != "FReconcilePublication" ==> s.KnownPR == r.KnownPR
// @ ensures !ok || action != "FReconcileMerge" ==> s.MergeOutcome == r.MergeOutcome
// @ ensures !ok || (action != "FStart" && action != "FReconcileRun") ==> s.Owner == r.Owner && s.RunFence == r.RunFence
// @ ensures !ok || (action != "FReconcileRun" && action != "FReconcileSuperseded" && action != "FReconcilePublication" && action != "FObservePR" && action != "FObserveHead") ==> s.Head == r.Head
// @ ensures !ok || (action != "FReconcileRun" && action != "FReconcileSuperseded" && action != "FReconcilePublication" && action != "FObservePR" && action != "FObserveHead" && action != "FObserveCI") ==> s.CI == r.CI && s.CIHead == r.CIHead && s.CanMerge == r.CanMerge && s.MergeHead == r.MergeHead
// @ ensures !ok || (action != "FReconcileRun" && action != "FReconcileSuperseded" && action != "FReconcileCreationRefusal" && action != "FObserveCI" && action != "FConsumeRequest") ==> s.Next == r.Next
// @ ensures !ok || (action != "FReconcileRun" && action != "FReconcileSuperseded" && action != "FReconcileCreationRefusal" && action != "FObserveCI" && action != "FReconcileMerge") ==> s.Cause == r.Cause
// @ ensures !ok || (action != "FReconcileSuperseded" && action != "FReconcilePublication" && action != "FObservePR" && action != "FReconcileMerge") ==> s.Proof == r.Proof
// @ ensures !ok || (action != "FReconcileRun" && action != "FReconcileSuperseded" && action != "FReconcilePublication" && action != "FObservePR" && action != "FObserveCI" && action != "FConsumeRequest" && action != "FReconcileMerge") ==> s.Status == r.Status
// @ ensures ok && (action == "FRecordAnswer" || action == "FAnswer") ==> s.Phase == r.Phase
// @ ensures forall i int :: { s.Kinds[i] } 0 <= i && i < len(s.Kinds) && (!ok || action != "FAuthorize" || i != r.Op + 1) ==> s.Kinds[i] == old(r.Kinds[i])
// @ ensures forall i int :: { s.Bases[i] } 0 <= i && i < len(s.Bases) && (!ok || action != "FAuthorize" || i != r.Op + 1) ==> s.Bases[i] == old(r.Bases[i])
// @ ensures forall i int :: { s.Rounds[i] } 0 <= i && i < len(s.Rounds) && (!ok || action != "FAuthorize" || i != r.Op + 1) ==> s.Rounds[i] == old(r.Rounds[i])
// @ ensures forall i int :: { s.Charged[i] } 0 <= i && i < len(s.Charged) && (!ok || action != "FAuthorize" || i != r.Op + 1) ==> s.Charged[i] == old(r.Charged[i])
// @ ensures forall i int :: { s.Starts[i] } 0 <= i && i < len(s.Starts) && i != r.Op && i != r.Op + 1 ==> s.Starts[i] == old(r.Starts[i])
// @ ensures forall i int :: { s.Raw[i] } 0 <= i && i < len(s.Raw) && i != r.Op && i != r.Op + 1 ==> s.Raw[i] == old(r.Raw[i])
// @ ensures forall i int :: { s.Results[i] } 0 <= i && i < len(s.Results) && i != r.Op && i != r.Op + 1 ==> s.Results[i] == old(r.Results[i])
// @ ensures forall i int :: { s.ResultSnapshots[i] } 0 <= i && i < len(s.ResultSnapshots) && i != r.Op && i != r.Op + 1 ==> s.ResultSnapshots[i] == old(r.ResultSnapshots[i])
// @ ensures forall i int :: { s.Effects[i] } 0 <= i && i < len(s.Effects) && i != FollowAnswerEffect && (i < r.Op * FollowEffectKinds || (r.Op + 2) * FollowEffectKinds <= i) ==> s.Effects[i] == old(r.Effects[i])
// @ ensures ok && action == "FAuthorize" ==> s.Op == r.Op + 1 && s.Kinds[s.Op] == r.Next && s.Bases[s.Op] == r.Head && s.Rounds[s.Op] == r.Round
// @ ensures ok && action == "FAuthorize" ==> s.Starts[s.Op] == 0 && s.Raw[s.Op] == "none" && s.Results[s.Op] == "none" && s.ResultSnapshots[s.Op] == 0
// @ ensures ok && action == "FAuthorize" && r.Next == "repair" ==> s.Left == r.Left - 1 && s.Charged[s.Op]
// @ ensures ok && action == "FAuthorize" && r.Next != "repair" ==> s.Left == r.Left
// @ ensures ok && action == "FAuthorize" && r.Next == "merge" ==> s.Phase == "start-merge" && s.Effects[s.Op * FollowEffectKinds + FollowMergeEffect] == FollowEffect{Intent: r.SnapshotCount + 1, Pending: true}
// @ ensures ok && action == "FAuthorize" && r.Next == "reopen" ==> s.Phase == "start-reopen" && s.Effects[s.Op * FollowEffectKinds + FollowReopenEffect] == FollowEffect{Intent: r.SnapshotCount + 1, Pending: true}
// @ ensures ok && action == "FAuthorize" && r.Next != "merge" && r.Next != "reopen" ==> s.Phase == "start-run" && s.Effects[s.Op * FollowEffectKinds + FollowRunEffect] == FollowEffect{Intent: r.SnapshotCount + 1, Pending: true}
// @ ensures ok && action == "FStart" ==> s.Phase == "running" && s.Owner == x.Watcher && s.RunFence == x.Fence && s.Starts[r.Op] == old(r.Starts[r.Op]) + 1 && s.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Dispatches == 1
// @ ensures ok && action == "FFinish" ==> s.Phase == "run-result" && s.Raw[r.Op] == x.Result && x.Operation == r.Op && r.Owner == x.Watcher && r.RunFence == x.Fence
// @ ensures ok && action == "FRecoverStopped" ==> s.Phase == "run-result" && s.Raw[r.Op] == "stopped"
// @ ensures ok && action == "FReconcileRun" ==> s.Owner == NoOne && s.RunFence == 0 && s.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Completed && !s.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Pending
// @ ensures ok && action == "FReconcileRun" && old(r.Raw[r.Op]) == "success" && old(r.Kinds[r.Op]) == "repair" && (old(r.Bases[r.Op]) != x.RemoteHead || x.RemotePR != "open") ==> s.Results[r.Op] == "invalidated" && s.Cause == "invalidated" && s.Head == x.RemoteHead && s.CI == "none" && s.CIHead == 0 && !s.CanMerge && s.MergeHead == 0
// @ ensures ok && action == "FReconcileRun" && (old(r.Raw[r.Op]) != "success" || old(r.Kinds[r.Op]) != "repair" || (old(r.Bases[r.Op]) == x.RemoteHead && x.RemotePR == "open")) ==> s.Results[r.Op] == old(r.Raw[r.Op])
// @ ensures ok && action == "FReconcileRun" && s.Results[r.Op] != "success" ==> s.Cause == s.Results[r.Op] && s.ResultSnapshots[r.Op] == r.SnapshotCount + 1
// @ ensures ok && action == "FReconcileRun" && s.Results[r.Op] == "success" && (old(r.Kinds[r.Op]) == "create" || old(r.Kinds[r.Op]) == "repair") ==> s.Phase == "start-publication" && s.ResultSnapshots[r.Op] == 0 && s.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect] == FollowEffect{Intent: r.SnapshotCount + 1, Pending: true}
// @ ensures ok && (action == "FPublish" || action == "FPublicationRefusal") ==> s.Phase == "publication" && s.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Dispatches == 1
// @ ensures ok && action == "FReconcileSuperseded" ==> s.Results[r.Op] == "invalidated" && s.Cause == "invalidated" && s.ResultSnapshots[r.Op] == r.SnapshotCount + 1 && s.Head == x.RemoteHead && s.CI == "none" && s.CIHead == 0 && !s.CanMerge && s.MergeHead == 0 && s.Next == "repair"
// @ ensures ok && action == "FReconcileCreationRefusal" ==> s.Results[r.Op] == "refused" && s.Cause == "refused" && s.Phase == "idle" && s.Next == "create" && s.ResultSnapshots[r.Op] == r.SnapshotCount + 1
// @ ensures ok && (action == "FReconcileCreationRefusal" || action == "FReconcilePublication") ==> !s.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Pending && s.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Completed
// @ ensures ok && action == "FReconcilePublication" ==> s.KnownPR && s.Head == x.RemoteHead && s.CI == "none" && s.CIHead == 0 && !s.CanMerge && s.MergeHead == 0 && s.ResultSnapshots[r.Op] == r.SnapshotCount + 1
// @ ensures ok && action == "FReconcilePublication" && x.RemotePR == "merged" ==> s.Phase == "done" && s.Status == "merged" && s.Proof == "observation"
// @ ensures ok && action == "FReconcilePublication" && x.RemotePR == "closed" ==> s.Phase == "hold" && s.Status == "closed"
// @ ensures ok && action == "FReconcilePublication" && x.RemotePR != "closed" && x.RemotePR != "merged" ==> s.Phase == "watch" && s.Status == "active"
// @ ensures ok && action == "FObservePR" ==> s.Head == x.RemoteHead
// @ ensures ok && action == "FObservePR" && x.RemotePR == "closed" ==> s.Phase == "hold" && s.Status == "closed"
// @ ensures ok && action == "FObservePR" && x.RemotePR != "closed" ==> s.Phase == "done" && s.Status == "merged" && s.Proof == "observation"
// @ ensures ok && action == "FObserveCI" ==> s.CI == x.RemoteCI && s.CIHead == r.Head && s.CanMerge == x.RemoteCanMerge && s.MergeHead == x.RemoteMergeHead
// @ ensures ok && action == "FObserveCI" && x.RemoteCI == "fail" ==> s.Next == "repair" && s.Cause == "ci-failed"
// @ ensures ok && action == "FObserveCI" && x.RemoteCI == "fail" && r.Left == 0 ==> s.Phase == "hold" && s.Status == "exhausted"
// @ ensures ok && action == "FObserveCI" && x.RemoteCI == "fail" && r.Left != 0 ==> s.Phase == "idle"
// @ ensures ok && action == "FObserveCI" && x.RemoteCI != "fail" && !x.RemoteCanMerge ==> s.Phase == "done" && s.Status == "unmergeable"
// @ ensures ok && action == "FObserveCI" && x.RemoteCI != "fail" && x.RemoteCanMerge ==> s.Phase == "idle" && s.Next == "merge"
// @ ensures ok && action == "FConsumeRequest" ==> s.Consumed == r.SnapshotCount + 1 && s.Round == r.Round + 1 && s.Left == 2
// @ ensures ok && action == "FConsumeRequest" ==> old(r.Kinds[r.Op]) != "repair" || old(r.ResultSnapshots[r.Op]) != 0
// @ ensures ok && action == "FConsumeRequest" && x.RemotePR == "closed" ==> s.Phase == "idle" && s.Next == "reopen" && s.Status == "active"
// @ ensures ok && action == "FConsumeRequest" && x.RemotePR != "closed" && r.Status == "exhausted" ==> s.Phase == "idle" && s.Next == "repair"
// @ ensures ok && action == "FRecordAnswer" ==> s.Effects[FollowAnswerEffect] == FollowEffect{Intent: r.Consumed, Pending: true}
// @ ensures ok && action == "FAnswer" ==> s.Answers == r.Answers + 1 && !s.Effects[FollowAnswerEffect].Pending && s.Effects[FollowAnswerEffect].Completed && s.Effects[FollowAnswerEffect].Dispatches == 1
// @ ensures ok && action == "FReopen" ==> s.Phase == "reopen-response" && s.Effects[r.Op * FollowEffectKinds + FollowReopenEffect].Dispatches == 1 && r.Consumed > 0
// @ ensures ok && action == "FReconcileReopen" ==> s.Phase == "watch" && s.Results[r.Op] == "success" && s.ResultSnapshots[r.Op] == r.SnapshotCount + 1 && !s.Effects[r.Op * FollowEffectKinds + FollowReopenEffect].Pending && s.Effects[r.Op * FollowEffectKinds + FollowReopenEffect].Completed
// @ ensures ok && action == "FMerge" ==> s.Phase == "merge-response" && s.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Dispatches == 1
// @ ensures ok && action == "FMerge" ==> r.CI == "pass" && r.CIHead == r.Head && r.Head == x.RemoteHead && r.CanMerge && r.MergeHead == r.Head
// @ ensures ok && action == "FReconcileMerge" && x.RemotePR == "merged" ==> s.MergeOutcome == "success" && s.Results[r.Op] == "success"
// @ ensures ok && action == "FReconcileMerge" && x.RemotePR != "merged" ==> s.MergeOutcome == x.RemoteMerge && s.Results[r.Op] == x.RemoteMerge
// @ ensures ok && action == "FReconcileMerge" && s.MergeOutcome == "unknown" ==> s.Phase == "uncertain" && s.ResultSnapshots[r.Op] == 0
// @ ensures ok && action == "FReconcileMerge" && s.MergeOutcome != "unknown" ==> s.Phase == "done" && s.ResultSnapshots[r.Op] == r.SnapshotCount + 1 && !s.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Pending && s.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Completed
// @ ensures ok && action == "FReconcileMerge" && s.MergeOutcome == "success" ==> s.Status == "merged" && (s.Proof == "response" || s.Proof == "observation")
// @ ensures ok && action == "FReconcileMerge" && s.MergeOutcome == "refused" ==> s.Status == "refused" && s.Cause == "refused"
// @ ensures ok && action == "FObserveHead" ==> s.Head == x.RemoteHead && s.CI == "none" && s.CIHead == 0 && !s.CanMerge && s.MergeHead == 0
// @ ensures ok && action == "FObserveHead" && r.Phase == "idle" && r.Next == "merge" ==> s.Phase == "watch"
// @ ensures forall i int :: { s.Starts[i] } 0 <= i && i < len(s.Starts) && (!ok || !((action == "FAuthorize" && i == r.Op + 1) || (action == "FStart" && i == r.Op))) ==> s.Starts[i] == old(r.Starts[i])
// @ ensures forall i int :: { s.Raw[i] } 0 <= i && i < len(s.Raw) && (!ok || !((action == "FAuthorize" && i == r.Op + 1) || ((action == "FFinish" || action == "FRecoverStopped") && i == r.Op))) ==> s.Raw[i] == old(r.Raw[i])
// @ ensures forall i int :: { s.Results[i] } 0 <= i && i < len(s.Results) && (!ok || !((action == "FAuthorize" && i == r.Op + 1) || ((action == "FReconcileRun" || action == "FReconcileSuperseded" || action == "FReconcileCreationRefusal" || action == "FReconcileReopen" || action == "FReconcileMerge") && i == r.Op))) ==> s.Results[i] == old(r.Results[i])
// @ ensures forall i int :: { s.ResultSnapshots[i] } 0 <= i && i < len(s.ResultSnapshots) && (!ok || !((action == "FAuthorize" && i == r.Op + 1) || ((action == "FReconcileRun" || action == "FReconcileSuperseded" || action == "FReconcileCreationRefusal" || action == "FReconcilePublication" || action == "FReconcileReopen" || action == "FReconcileMerge") && i == r.Op))) ==> s.ResultSnapshots[i] == old(r.ResultSnapshots[i])
// @ ensures forall i int :: { s.Charged[i] } 0 <= i && i < len(s.Charged) && (!ok || action != "FAuthorize" || r.Next != "repair" || i != r.Op + 1) ==> s.Charged[i] == old(r.Charged[i])
// @ ensures forall i int :: { s.Effects[i] } 0 <= i && i < len(s.Effects) && (!ok || !((action == "FAuthorize" && ((r.Next == "merge" && i == (r.Op + 1) * FollowEffectKinds + FollowMergeEffect) || (r.Next == "reopen" && i == (r.Op + 1) * FollowEffectKinds + FollowReopenEffect) || (r.Next != "merge" && r.Next != "reopen" && i == (r.Op + 1) * FollowEffectKinds + FollowRunEffect))) || ((action == "FStart" || action == "FReconcileRun") && i == r.Op * FollowEffectKinds + FollowRunEffect) || (action == "FReconcileRun" && s.Results[r.Op] == "success" && (old(r.Kinds[r.Op]) == "create" || old(r.Kinds[r.Op]) == "repair") && i == r.Op * FollowEffectKinds + FollowPublicationEffect) || ((action == "FPublish" || action == "FPublicationRefusal" || action == "FReconcileCreationRefusal" || action == "FReconcilePublication") && i == r.Op * FollowEffectKinds + FollowPublicationEffect) || (action == "FReconcileSuperseded" && ((old(r.Kinds[r.Op]) == "merge" && i == r.Op * FollowEffectKinds + FollowMergeEffect) || (old(r.Kinds[r.Op]) != "merge" && (r.Phase == "record-run" || r.Phase == "start-run") && i == r.Op * FollowEffectKinds + FollowRunEffect) || (old(r.Kinds[r.Op]) != "merge" && r.Phase != "record-run" && r.Phase != "start-run" && i == r.Op * FollowEffectKinds + FollowPublicationEffect))) || ((action == "FRecordAnswer" || action == "FAnswer") && i == FollowAnswerEffect) || ((action == "FReopen" || action == "FReconcileReopen") && i == r.Op * FollowEffectKinds + FollowReopenEffect) || ((action == "FMerge" || (action == "FReconcileMerge" && s.MergeOutcome != "unknown")) && i == r.Op * FollowEffectKinds + FollowMergeEffect))) ==> s.Effects[i] == old(r.Effects[i])
// @ ensures ok && action == "FAuthorize" ==> s.Snapshots[r.SnapshotCount] == FollowSnapshot{Source: r.Source, Branch: r.Branch, PR: r.PR, Op: r.Op + 1, Kind: r.Next, Head: r.Head, Left: s.Left, Round: r.Round, Cause: r.Cause, Stage: "authorized"}
// @ ensures ok && action == "FReconcileRun" ==> s.Snapshots[r.SnapshotCount] == FollowSnapshot{Source: r.Source, Branch: r.Branch, PR: r.PR, Op: r.Op, Kind: old(r.Kinds[r.Op]), Head: s.Head, Left: r.Left, Round: r.Round, Cause: s.Results[r.Op], Stage: "run-outcome"}
// @ ensures ok && action == "FReconcileSuperseded" ==> s.Snapshots[r.SnapshotCount] == FollowSnapshot{Source: r.Source, Branch: r.Branch, PR: r.PR, Op: r.Op, Kind: old(r.Kinds[r.Op]), Head: x.RemoteHead, Left: r.Left, Round: r.Round, Cause: "invalidated", Stage: "superseded-outcome"}
// @ ensures ok && action == "FReconcileCreationRefusal" ==> s.Snapshots[r.SnapshotCount] == FollowSnapshot{Source: r.Source, Branch: r.Branch, PR: r.PR, Op: r.Op, Kind: old(r.Kinds[r.Op]), Head: r.Head, Left: r.Left, Round: r.Round, Cause: "refused", Stage: "creation-outcome"}
// @ ensures ok && action == "FReconcilePublication" ==> s.Snapshots[r.SnapshotCount] == FollowSnapshot{Source: r.Source, Branch: r.Branch, PR: r.PR, Op: r.Op, Kind: old(r.Kinds[r.Op]), Head: x.RemoteHead, Left: r.Left, Round: r.Round, Cause: r.Cause, Stage: "publication-outcome"}
// @ ensures ok && action == "FObservePR" ==> s.Snapshots[r.SnapshotCount] == FollowSnapshot{Source: r.Source, Branch: r.Branch, PR: r.PR, Op: r.Op, Kind: old(r.Kinds[r.Op]), Head: x.RemoteHead, Left: r.Left, Round: r.Round, Cause: r.Cause, Stage: "pr-observation"}
// @ ensures ok && action == "FObserveCI" ==> s.Snapshots[r.SnapshotCount] == FollowSnapshot{Source: r.Source, Branch: r.Branch, PR: r.PR, Op: r.Op, Kind: old(r.Kinds[r.Op]), Head: r.Head, Left: r.Left, Round: r.Round, Cause: s.Cause, Stage: "ci-observation"}
// @ ensures ok && action == "FConsumeRequest" ==> s.Snapshots[r.SnapshotCount] == FollowSnapshot{Source: r.Source, Branch: r.Branch, PR: r.PR, Op: 0, Kind: "none", Head: r.Head, Left: 2, Round: s.Round, Cause: r.Cause, Stage: "writer-renewal"}
// @ ensures ok && action == "FReconcileReopen" ==> s.Snapshots[r.SnapshotCount] == FollowSnapshot{Source: r.Source, Branch: r.Branch, PR: r.PR, Op: r.Op, Kind: old(r.Kinds[r.Op]), Head: r.Head, Left: r.Left, Round: r.Round, Cause: r.Cause, Stage: "reopen-outcome"}
// @ ensures ok && action == "FReconcileMerge" ==> s.Snapshots[r.SnapshotCount] == FollowSnapshot{Source: r.Source, Branch: r.Branch, PR: r.PR, Op: r.Op, Kind: old(r.Kinds[r.Op]), Head: r.Head, Left: r.Left, Round: r.Round, Cause: s.MergeOutcome, Stage: "merge-outcome"}
// @ ensures ok && action == "FObserveHead" ==> s.Snapshots[r.SnapshotCount] == FollowSnapshot{Source: r.Source, Branch: r.Branch, PR: r.PR, Op: r.Op, Kind: old(r.Kinds[r.Op]), Head: x.RemoteHead, Left: r.Left, Round: r.Round, Cause: r.Cause, Stage: "head-observation"}
// @ ensures ok && action == "FReconcileRun" && s.Results[r.Op] == "success" ==> s.Cause == r.Cause
// @ ensures ok && action == "FReconcileRun" && s.Results[r.Op] != "invalidated" ==> s.Head == r.Head && s.CI == r.CI && s.CIHead == r.CIHead && s.CanMerge == r.CanMerge && s.MergeHead == r.MergeHead
// @ ensures ok && action == "FReconcileRun" && old(r.Kinds[r.Op]) == "draft" && s.Results[r.Op] == "success" ==> s.Next == "create"
// @ ensures ok && action == "FReconcileRun" && (old(r.Kinds[r.Op]) != "draft" || s.Results[r.Op] != "success") ==> s.Next == old(r.Kinds[r.Op])
// @ ensures ok && action == "FReconcileRun" && !(s.Results[r.Op] == "success" && (old(r.Kinds[r.Op]) == "create" || old(r.Kinds[r.Op]) == "repair")) ==> s.ResultSnapshots[r.Op] == r.SnapshotCount + 1
// @ ensures ok && action == "FReconcileRun" && old(r.Kinds[r.Op]) == "repair" && s.Results[r.Op] != "success" && (r.Left == 0 || x.RemotePR == "closed") ==> s.Phase == "hold"
// @ ensures ok && action == "FReconcileRun" && old(r.Kinds[r.Op]) == "repair" && s.Results[r.Op] != "success" && x.RemotePR == "closed" ==> s.Status == "closed"
// @ ensures ok && action == "FReconcileRun" && old(r.Kinds[r.Op]) == "repair" && s.Results[r.Op] != "success" && r.Left == 0 && x.RemotePR != "closed" ==> s.Status == "exhausted"
// @ ensures ok && action == "FReconcileRun" && !(old(r.Kinds[r.Op]) == "repair" && s.Results[r.Op] != "success" && (r.Left == 0 || x.RemotePR == "closed")) ==> s.Status == r.Status
// @ ensures ok && action == "FReconcileRun" && !(s.Results[r.Op] == "success" && (old(r.Kinds[r.Op]) == "create" || old(r.Kinds[r.Op]) == "repair")) && !(old(r.Kinds[r.Op]) == "repair" && s.Results[r.Op] != "success" && (r.Left == 0 || x.RemotePR == "closed")) ==> s.Phase == "idle"
// @ ensures ok && action == "FReconcileSuperseded" && x.RemotePR == "merged" ==> s.Phase == "done" && s.Status == "merged" && s.Proof == "observation"
// @ ensures ok && action == "FReconcileSuperseded" && x.RemotePR != "merged" ==> s.Proof == r.Proof
// @ ensures ok && action == "FReconcileSuperseded" && x.RemotePR == "closed" ==> s.Phase == "hold" && s.Status == "closed"
// @ ensures ok && action == "FReconcileSuperseded" && x.RemotePR != "merged" && x.RemotePR != "closed" && old(r.Kinds[r.Op]) == "repair" && r.Left == 0 ==> s.Phase == "hold" && s.Status == "exhausted"
// @ ensures ok && action == "FReconcileSuperseded" && x.RemotePR != "merged" && x.RemotePR != "closed" && !(old(r.Kinds[r.Op]) == "repair" && r.Left == 0) ==> s.Status == r.Status
// @ ensures ok && action == "FReconcileSuperseded" && x.RemotePR != "merged" && x.RemotePR != "closed" && old(r.Kinds[r.Op]) == "merge" ==> s.Phase == "watch"
// @ ensures ok && action == "FReconcileSuperseded" && x.RemotePR != "merged" && x.RemotePR != "closed" && old(r.Kinds[r.Op]) != "merge" && !(old(r.Kinds[r.Op]) == "repair" && r.Left == 0) ==> s.Phase == "idle"
// @ ensures ok && action == "FReconcilePublication" && x.RemotePR != "merged" ==> s.Proof == r.Proof
// @ ensures ok && action == "FObservePR" && x.RemotePR == "closed" ==> s.Proof == r.Proof
// @ ensures ok && action == "FObservePR" && r.Head != x.RemoteHead ==> s.CI == "none" && s.CIHead == 0 && !s.CanMerge && s.MergeHead == 0
// @ ensures ok && action == "FObservePR" && r.Head == x.RemoteHead ==> s.CI == r.CI && s.CIHead == r.CIHead && s.CanMerge == r.CanMerge && s.MergeHead == r.MergeHead
// @ ensures ok && action == "FObserveCI" && x.RemoteCI != "fail" ==> s.Next == "merge" && s.Cause == r.Cause
// @ ensures ok && action == "FObserveCI" && !((x.RemoteCI == "fail" && r.Left == 0) || (x.RemoteCI != "fail" && !x.RemoteCanMerge)) ==> s.Status == r.Status
// @ ensures ok && action == "FConsumeRequest" && x.RemotePR != "closed" && r.Status != "exhausted" ==> s.Phase == r.Phase && s.Next == r.Next
// @ ensures ok && action == "FConsumeRequest" && r.Phase == "done" && x.RemotePR != "closed" ==> s.Status == r.Status
// @ ensures ok && action == "FConsumeRequest" && (r.Phase != "done" || x.RemotePR == "closed") ==> s.Status == "active"
// @ ensures ok && action == "FReconcileMerge" && s.MergeOutcome != "success" && s.MergeOutcome != "refused" ==> s.Status == r.Status
// @ ensures ok && action == "FReconcileMerge" && s.MergeOutcome != "refused" ==> s.Cause == r.Cause
// @ ensures ok && action == "FReconcileMerge" && s.MergeOutcome != "success" ==> s.Proof == r.Proof
// @ ensures ok && action == "FReconcileMerge" && s.MergeOutcome == "success" && (x.MergeLost || x.ExternalMerged || x.RemoteMerge != "success") ==> s.Proof == "observation"
// @ ensures ok && action == "FReconcileMerge" && s.MergeOutcome == "success" && !(x.MergeLost || x.ExternalMerged || x.RemoteMerge != "success") ==> s.Proof == "response"
// @ ensures ok && action == "FObserveHead" && !(r.Phase == "idle" && r.Next == "merge") ==> s.Phase == r.Phase
// @ ensures ok && (action == "FStart") ==> s.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Intent == old(r.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Intent) && s.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Pending == old(r.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Pending) && s.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Completed == old(r.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Completed) && s.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Cancelled == old(r.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Cancelled)
// @ ensures ok && (action == "FPublish" || action == "FPublicationRefusal") ==> s.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Intent == old(r.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Intent) && s.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Pending == old(r.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Pending) && s.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Completed == old(r.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Completed) && s.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Cancelled == old(r.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Cancelled)
// @ ensures ok && (action == "FReopen") ==> s.Effects[r.Op * FollowEffectKinds + FollowReopenEffect].Intent == old(r.Effects[r.Op * FollowEffectKinds + FollowReopenEffect].Intent) && s.Effects[r.Op * FollowEffectKinds + FollowReopenEffect].Pending == old(r.Effects[r.Op * FollowEffectKinds + FollowReopenEffect].Pending) && s.Effects[r.Op * FollowEffectKinds + FollowReopenEffect].Completed == old(r.Effects[r.Op * FollowEffectKinds + FollowReopenEffect].Completed) && s.Effects[r.Op * FollowEffectKinds + FollowReopenEffect].Cancelled == old(r.Effects[r.Op * FollowEffectKinds + FollowReopenEffect].Cancelled)
// @ ensures ok && (action == "FMerge") ==> s.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Intent == old(r.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Intent) && s.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Pending == old(r.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Pending) && s.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Completed == old(r.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Completed) && s.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Cancelled == old(r.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Cancelled)
// @ ensures ok && (action == "FReconcileRun") ==> s.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Intent == old(r.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Intent) && s.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Dispatches == old(r.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Dispatches) && s.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Cancelled == old(r.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Cancelled)
// @ ensures ok && (action == "FReconcileCreationRefusal" || action == "FReconcilePublication") ==> s.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Intent == old(r.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Intent) && s.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Dispatches == old(r.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Dispatches) && s.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Cancelled == old(r.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Cancelled)
// @ ensures ok && (action == "FReconcileReopen") ==> s.Effects[r.Op * FollowEffectKinds + FollowReopenEffect].Intent == old(r.Effects[r.Op * FollowEffectKinds + FollowReopenEffect].Intent) && s.Effects[r.Op * FollowEffectKinds + FollowReopenEffect].Dispatches == old(r.Effects[r.Op * FollowEffectKinds + FollowReopenEffect].Dispatches) && s.Effects[r.Op * FollowEffectKinds + FollowReopenEffect].Cancelled == old(r.Effects[r.Op * FollowEffectKinds + FollowReopenEffect].Cancelled)
// @ ensures ok && (action == "FReconcileMerge" && s.MergeOutcome != "unknown") ==> s.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Intent == old(r.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Intent) && s.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Dispatches == old(r.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Dispatches) && s.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Cancelled == old(r.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Cancelled)
// @ ensures ok && action == "FAnswer" ==> s.Effects[FollowAnswerEffect].Intent == old(r.Effects[FollowAnswerEffect].Intent) && s.Effects[FollowAnswerEffect].Cancelled == old(r.Effects[FollowAnswerEffect].Cancelled)
// @ ensures ok && action == "FReconcileSuperseded" && (old(r.Kinds[r.Op]) == "merge") ==> !s.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Pending && s.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Cancelled == (old(r.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Intent) > 0 || old(r.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Cancelled)) && s.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Intent == old(r.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Intent) && s.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Completed == old(r.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Completed) && s.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Dispatches == old(r.Effects[r.Op * FollowEffectKinds + FollowMergeEffect].Dispatches)
// @ ensures ok && action == "FReconcileSuperseded" && (old(r.Kinds[r.Op]) != "merge" && (r.Phase == "record-run" || r.Phase == "start-run")) ==> !s.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Pending && s.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Cancelled == (old(r.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Intent) > 0 || old(r.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Cancelled)) && s.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Intent == old(r.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Intent) && s.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Completed == old(r.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Completed) && s.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Dispatches == old(r.Effects[r.Op * FollowEffectKinds + FollowRunEffect].Dispatches)
// @ ensures ok && action == "FReconcileSuperseded" && (old(r.Kinds[r.Op]) != "merge" && r.Phase != "record-run" && r.Phase != "start-run") ==> !s.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Pending && s.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Cancelled == (old(r.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Intent) > 0 || old(r.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Cancelled)) && s.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Intent == old(r.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Intent) && s.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Completed == old(r.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Completed) && s.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Dispatches == old(r.Effects[r.Op * FollowEffectKinds + FollowPublicationEffect].Dispatches)
func (r FollowRepo) Step(action string, x FollowInput) (s FollowRepo, ok bool) {
	if !r.CanStep(action, x) {
		return r, false
	}
	s = r
	o := r.Op
	kind := r.Kinds[o]
	idx := r.SnapshotCount + 1
	snapshot := FollowSnapshot{Source: r.Source, Branch: r.Branch, PR: r.PR,
		Op: o, Kind: kind, Head: r.Head, Left: r.Left, Round: r.Round, Cause: r.Cause}
	save := false
	switch action {
	case "FAuthorize":
		o = r.Op + 1
		kind = r.Next
		effect := FollowRunEffect
		s.Phase = "start-run"
		if kind == "merge" {
			effect, s.Phase = FollowMergeEffect, "start-merge"
		} else if kind == "reopen" {
			effect, s.Phase = FollowReopenEffect, "start-reopen"
		}
		s.Op = o
		s.Kinds[o], s.Bases[o], s.Rounds[o] = kind, r.Head, r.Round
		s.Starts[o], s.Raw[o], s.Results[o], s.ResultSnapshots[o] = 0, "none", "none", 0
		if kind == "repair" {
			s.Left = r.Left - 1
			s.Charged[o] = true
		}
		s.Effects[o*FollowEffectKinds+effect] = FollowEffect{Intent: idx, Pending: true}
		snapshot.Op, snapshot.Kind, snapshot.Left, snapshot.Stage = o, kind, s.Left, "authorized"
		save = true
	case "FStart":
		s.Starts[o]++
		e := o*FollowEffectKinds + FollowRunEffect
		entry := r.Effects[e]
		entry.Dispatches++
		s.Effects[e] = entry
		s.Owner, s.RunFence, s.Phase = x.Watcher, x.Fence, "running"
	case "FFinish":
		s.Raw[o], s.Phase = x.Result, "run-result"
	case "FRecoverStopped":
		s.Raw[o], s.Phase = "stopped", "run-result"
	case "FReconcileRun":
		result := r.Raw[o]
		if result == "success" && kind == "repair" && (r.Bases[o] != x.RemoteHead || x.RemotePR != "open") {
			result = "invalidated"
		}
		publish := result == "success" && (kind == "create" || kind == "repair")
		hold := kind == "repair" && !publish && (r.Left == 0 || x.RemotePR == "closed")
		s.Results[o], s.Owner, s.RunFence = result, NoOne, 0
		e := o*FollowEffectKinds + FollowRunEffect
		entry := r.Effects[e]
		entry.Completed, entry.Pending = true, false
		s.Effects[e] = entry
		s.ResultSnapshots[o] = idx
		s.Phase = "idle"
		if publish {
			s.Effects[o*FollowEffectKinds+FollowPublicationEffect] = FollowEffect{Intent: idx, Pending: true}
			s.ResultSnapshots[o], s.Phase = 0, "start-publication"
		} else if hold {
			s.Phase, s.Status = "hold", "exhausted"
			if x.RemotePR == "closed" {
				s.Status = "closed"
			}
		}
		if result == "invalidated" {
			s.Head, s.CI, s.CIHead, s.CanMerge, s.MergeHead = x.RemoteHead, "none", 0, false, 0
		}
		if result != "success" {
			s.Cause = result
		}
		s.Next = kind
		if kind == "draft" && result == "success" {
			s.Next = "create"
		}
		snapshot.Head, snapshot.Cause, snapshot.Stage = s.Head, result, "run-outcome"
		save = true
	case "FPublish", "FPublicationRefusal":
		e := o*FollowEffectKinds + FollowPublicationEffect
		entry := r.Effects[e]
		entry.Dispatches++
		s.Effects[e], s.Phase = entry, "publication"
	case "FReconcileSuperseded":
		effect := FollowPublicationEffect
		if kind == "merge" {
			effect = FollowMergeEffect
		} else if r.Phase == "record-run" || r.Phase == "start-run" {
			effect = FollowRunEffect
		}
		e := o*FollowEffectKinds + effect
		entry := r.Effects[e]
		entry.Pending = false
		if entry.Intent > 0 {
			entry.Cancelled = true
		}
		s.Effects[e] = entry
		s.Results[o], s.Cause, s.ResultSnapshots[o] = "invalidated", "invalidated", idx
		s.Head, s.CI, s.CIHead, s.CanMerge, s.MergeHead = x.RemoteHead, "none", 0, false, 0
		s.Phase, s.Next = "idle", "repair"
		if kind == "merge" {
			s.Phase = "watch"
		}
		if x.RemotePR == "merged" {
			s.Phase, s.Status, s.Proof = "done", "merged", "observation"
		} else if x.RemotePR == "closed" {
			s.Phase, s.Status = "hold", "closed"
		} else if kind == "repair" && r.Left == 0 {
			s.Phase, s.Status = "hold", "exhausted"
		}
		snapshot.Head, snapshot.Cause, snapshot.Stage = x.RemoteHead, "invalidated", "superseded-outcome"
		save = true
	case "FReconcileCreationRefusal":
		e := o*FollowEffectKinds + FollowPublicationEffect
		entry := r.Effects[e]
		entry.Pending, entry.Completed = false, true
		s.Effects[e] = entry
		s.ResultSnapshots[o], s.Results[o], s.Cause = idx, "refused", "refused"
		s.Phase, s.Next = "idle", "create"
		snapshot.Cause, snapshot.Stage = "refused", "creation-outcome"
		save = true
	case "FReconcilePublication":
		s.KnownPR = true
		s.Head, s.CI, s.CIHead, s.CanMerge, s.MergeHead = x.RemoteHead, "none", 0, false, 0
		e := o*FollowEffectKinds + FollowPublicationEffect
		entry := r.Effects[e]
		entry.Pending, entry.Completed = false, true
		s.Effects[e] = entry
		s.ResultSnapshots[o], s.Phase, s.Status = idx, "watch", "active"
		if x.RemotePR == "merged" {
			s.Phase, s.Status, s.Proof = "done", "merged", "observation"
		} else if x.RemotePR == "closed" {
			s.Phase, s.Status = "hold", "closed"
		}
		snapshot.Head, snapshot.Stage = x.RemoteHead, "publication-outcome"
		save = true
	case "FObservePR":
		s.Head = x.RemoteHead
		if r.Head != x.RemoteHead {
			s.CI, s.CIHead, s.CanMerge, s.MergeHead = "none", 0, false, 0
		}
		if x.RemotePR == "closed" {
			s.Phase, s.Status = "hold", "closed"
		} else {
			s.Phase, s.Status, s.Proof = "done", "merged", "observation"
		}
		snapshot.Head, snapshot.Stage = x.RemoteHead, "pr-observation"
		save = true
	case "FObserveCI":
		s.CI, s.CIHead, s.CanMerge, s.MergeHead = x.RemoteCI, r.Head, x.RemoteCanMerge, x.RemoteMergeHead
		s.Phase, s.Next = "idle", "merge"
		if x.RemoteCI == "fail" {
			s.Next, s.Cause = "repair", "ci-failed"
			if r.Left == 0 {
				s.Phase, s.Status = "hold", "exhausted"
			}
		} else if !x.RemoteCanMerge {
			s.Phase, s.Status = "done", "unmergeable"
		}
		snapshot.Cause, snapshot.Stage = s.Cause, "ci-observation"
		save = true
	case "FConsumeRequest":
		s.Consumed, s.Round, s.Left = idx, r.Round+1, 2
		if x.RemotePR == "closed" {
			s.Phase, s.Next = "idle", "reopen"
		} else if r.Status == "exhausted" {
			s.Phase, s.Next = "idle", "repair"
		}
		if r.Phase != "done" || x.RemotePR == "closed" {
			s.Status = "active"
		}
		snapshot.Op, snapshot.Kind, snapshot.Left, snapshot.Round, snapshot.Stage = 0, "none", 2, s.Round, "writer-renewal"
		save = true
	case "FRecordAnswer":
		s.Effects[FollowAnswerEffect] = FollowEffect{Intent: r.Consumed, Pending: true}
	case "FAnswer":
		entry := r.Effects[FollowAnswerEffect]
		entry.Pending, entry.Completed, entry.Dispatches = false, true, entry.Dispatches+1
		s.Effects[FollowAnswerEffect] = entry
		s.Answers++
	case "FReopen":
		e := o*FollowEffectKinds + FollowReopenEffect
		entry := r.Effects[e]
		entry.Dispatches++
		s.Effects[e], s.Phase = entry, "reopen-response"
	case "FReconcileReopen":
		e := o*FollowEffectKinds + FollowReopenEffect
		entry := r.Effects[e]
		entry.Pending, entry.Completed = false, true
		s.Effects[e] = entry
		s.ResultSnapshots[o], s.Results[o], s.Phase = idx, "success", "watch"
		snapshot.Stage = "reopen-outcome"
		save = true
	case "FMerge":
		e := o*FollowEffectKinds + FollowMergeEffect
		entry := r.Effects[e]
		entry.Dispatches++
		s.Effects[e], s.Phase = entry, "merge-response"
	case "FReconcileMerge":
		result := x.RemoteMerge
		if x.RemotePR == "merged" {
			result = "success"
		}
		s.MergeOutcome, s.Results[o], s.Phase = result, result, "uncertain"
		s.ResultSnapshots[o] = 0
		if result != "unknown" {
			s.Phase = "done"
			e := o*FollowEffectKinds + FollowMergeEffect
			entry := r.Effects[e]
			entry.Pending, entry.Completed = false, true
			s.Effects[e] = entry
			s.ResultSnapshots[o] = idx
		}
		if result == "success" {
			s.Status, s.Proof = "merged", "response"
			if x.MergeLost || x.ExternalMerged || x.RemoteMerge != "success" {
				s.Proof = "observation"
			}
		} else if result == "refused" {
			s.Status, s.Cause = "refused", "refused"
		}
		snapshot.Cause, snapshot.Stage = result, "merge-outcome"
		save = true
	case "FObserveHead":
		s.Head, s.CI, s.CIHead, s.CanMerge, s.MergeHead = x.RemoteHead, "none", 0, false, 0
		if r.Phase == "idle" && r.Next == "merge" {
			s.Phase = "watch"
		}
		snapshot.Head, snapshot.Stage = x.RemoteHead, "head-observation"
		save = true
	}
	if save {
		s.Snapshots[r.SnapshotCount] = snapshot
		s.SnapshotCount = idx
	}
	return s, true
}
