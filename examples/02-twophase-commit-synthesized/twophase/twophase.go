// +gobra

package twophase

// This package implements two-phase commit as modeled in
// .invariant/specs/TwoPhase.tla.

// N is the number of resource managers, RM = {r1, r2, r3}.
const N = 3

// Resource manager states, the values of rmState[r].
const (
	Working   = 0
	Prepared  = 1
	Committed = 2
	Aborted   = 3
)

// State mirrors the spec's variables one to one.
type State struct {
	// RM[r] is rmState[r].
	RM [N]uint8
	// TMDone is tmState = "done"; false means "init".
	TMDone bool
	// TMPrepared[r] is r \in tmPrepared.
	TMPrepared [N]bool
	// MsgPrepared[r] is [type |-> "Prepared", rm |-> r] \in msgs.
	MsgPrepared [N]bool
	// MsgCommit is [type |-> "Commit"] \in msgs.
	MsgCommit bool
	// MsgAbort is [type |-> "Abort"] \in msgs.
	MsgAbort bool
}

// Init mirrors the TLA+ Init.
// @ ensures forall j int :: 0 <= j && j < N ==> s.RM[j] == Working
// @ ensures !s.TMDone
// @ ensures forall j int :: 0 <= j && j < N ==> !s.TMPrepared[j]
// @ ensures forall j int :: 0 <= j && j < N ==> !s.MsgPrepared[j]
// @ ensures !s.MsgCommit && !s.MsgAbort
func Init() (s State) {
	return State{}
}

// TMRcvPrepared mirrors the TLA+ action TMRcvPrepared(r).
// @ requires 0 <= r && r < N
// @ requires !s.TMDone && s.MsgPrepared[r]
// @ ensures t.TMPrepared[r]
// @ ensures forall j int :: 0 <= j && j < N && j != r ==> t.TMPrepared[j] == s.TMPrepared[j]
// @ ensures t.RM == s.RM && t.TMDone == s.TMDone
// @ ensures t.MsgPrepared == s.MsgPrepared && t.MsgCommit == s.MsgCommit && t.MsgAbort == s.MsgAbort
func TMRcvPrepared(s State, r int) (t State) {
	t = s
	t.TMPrepared[r] = true
	return t
}

// TMCommit mirrors the TLA+ action TMCommit.
// @ requires !s.TMDone
// @ requires forall j int :: 0 <= j && j < N ==> s.TMPrepared[j]
// @ ensures t.TMDone && t.MsgCommit
// @ ensures t.RM == s.RM && t.TMPrepared == s.TMPrepared
// @ ensures t.MsgPrepared == s.MsgPrepared && t.MsgAbort == s.MsgAbort
func TMCommit(s State) (t State) {
	t = s
	t.TMDone = true
	t.MsgCommit = true
	return t
}

// TMAbort mirrors the TLA+ action TMAbort.
// @ requires !s.TMDone
// @ ensures t.TMDone && t.MsgAbort
// @ ensures t.RM == s.RM && t.TMPrepared == s.TMPrepared
// @ ensures t.MsgPrepared == s.MsgPrepared && t.MsgCommit == s.MsgCommit
func TMAbort(s State) (t State) {
	t = s
	t.TMDone = true
	t.MsgAbort = true
	return t
}

// RMPrepare mirrors the TLA+ action RMPrepare(r).
// @ requires 0 <= r && r < N
// @ requires s.RM[r] == Working
// @ ensures t.RM[r] == Prepared && t.MsgPrepared[r]
// @ ensures forall j int :: 0 <= j && j < N && j != r ==> t.RM[j] == s.RM[j]
// @ ensures forall j int :: 0 <= j && j < N && j != r ==> t.MsgPrepared[j] == s.MsgPrepared[j]
// @ ensures t.TMDone == s.TMDone && t.TMPrepared == s.TMPrepared
// @ ensures t.MsgCommit == s.MsgCommit && t.MsgAbort == s.MsgAbort
func RMPrepare(s State, r int) (t State) {
	t = s
	t.RM[r] = Prepared
	t.MsgPrepared[r] = true
	return t
}

// RMChooseToAbort mirrors the TLA+ action RMChooseToAbort(r).
// @ requires 0 <= r && r < N
// @ requires s.RM[r] == Working
// @ ensures t.RM[r] == Aborted
// @ ensures forall j int :: 0 <= j && j < N && j != r ==> t.RM[j] == s.RM[j]
// @ ensures t.TMDone == s.TMDone && t.TMPrepared == s.TMPrepared
// @ ensures t.MsgPrepared == s.MsgPrepared && t.MsgCommit == s.MsgCommit && t.MsgAbort == s.MsgAbort
func RMChooseToAbort(s State, r int) (t State) {
	t = s
	t.RM[r] = Aborted
	return t
}

// RMRcvCommitMsg mirrors the TLA+ action RMRcvCommitMsg(r).
// @ requires 0 <= r && r < N
// @ requires s.MsgCommit
// @ ensures t.RM[r] == Committed
// @ ensures forall j int :: 0 <= j && j < N && j != r ==> t.RM[j] == s.RM[j]
// @ ensures t.TMDone == s.TMDone && t.TMPrepared == s.TMPrepared
// @ ensures t.MsgPrepared == s.MsgPrepared && t.MsgCommit == s.MsgCommit && t.MsgAbort == s.MsgAbort
func RMRcvCommitMsg(s State, r int) (t State) {
	t = s
	t.RM[r] = Committed
	return t
}

// RMRcvAbortMsg mirrors the TLA+ action RMRcvAbortMsg(r).
// @ requires 0 <= r && r < N
// @ requires s.MsgAbort
// @ ensures t.RM[r] == Aborted
// @ ensures forall j int :: 0 <= j && j < N && j != r ==> t.RM[j] == s.RM[j]
// @ ensures t.TMDone == s.TMDone && t.TMPrepared == s.TMPrepared
// @ ensures t.MsgPrepared == s.MsgPrepared && t.MsgCommit == s.MsgCommit && t.MsgAbort == s.MsgAbort
func RMRcvAbortMsg(s State, r int) (t State) {
	t = s
	t.RM[r] = Aborted
	return t
}
