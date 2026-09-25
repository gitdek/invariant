// Package twophase implements two-phase commit as a state machine.
//
// Each step function mirrors one action of ../.invariant/specs/TwoPhase.tla.
// It takes the current state and returns the next one. Its Gobra contract
// restates the action: `requires` is the enabling condition and `ensures` is
// the effect, including everything the action leaves unchanged. Gobra checks
// every contract, and every array index, before this code can merge.
package twophase

// N is the number of resource managers. It matches the bound TLC checks:
// RM = {r1, r2, r3}.
const N = 3

// RMState is the state of one resource manager.
type RMState int

const (
	Working   RMState = 0
	Prepared  RMState = 1
	Committed RMState = 2
	Aborted   RMState = 3
)

// TMState is the state of the transaction manager.
type TMState int

const (
	TMInit TMState = 0
	TMDone TMState = 1
)

// State mirrors the spec's variables. msgs is represented exactly: the only
// possible messages are one Prepared per resource manager, Commit and Abort,
// and a message is never removed once sent.
type State struct {
	RM          [N]RMState // rmState
	TM          TMState    // tmState
	TMPrepared  [N]bool    // r \in tmPrepared
	PreparedMsg [N]bool    // [type |-> "Prepared", rm |-> r] \in msgs
	CommitMsg   bool       // [type |-> "Commit"] \in msgs
	AbortMsg    bool       // [type |-> "Abort"] \in msgs
}

// Init mirrors Init: every resource manager is working and no message has
// been sent.
// @ ensures forall i int :: 0 <= i && i < N ==> s.RM[i] == Working && !s.TMPrepared[i] && !s.PreparedMsg[i]
// @ ensures s.TM == TMInit && !s.CommitMsg && !s.AbortMsg
func Init() (s State) {
	return State{}
}

// TMRcvPrepared mirrors TMRcvPrepared(r): the transaction manager records
// r's Prepared message.
// @ requires 0 <= r && r < N
// @ requires s.TM == TMInit && s.PreparedMsg[r]
// @ ensures t.TMPrepared[r]
// @ ensures forall i int :: 0 <= i && i < N && i != r ==> t.TMPrepared[i] == s.TMPrepared[i]
// @ ensures t.RM == s.RM && t.TM == s.TM && t.PreparedMsg == s.PreparedMsg
// @ ensures t.CommitMsg == s.CommitMsg && t.AbortMsg == s.AbortMsg
func TMRcvPrepared(s State, r int) (t State) {
	t = s
	t.TMPrepared[r] = true
	return t
}

// TMCommit mirrors TMCommit: once every resource manager has prepared, the
// transaction manager commits.
// @ requires s.TM == TMInit
// @ requires forall i int :: 0 <= i && i < N ==> s.TMPrepared[i]
// @ ensures t.TM == TMDone && t.CommitMsg
// @ ensures t.RM == s.RM && t.TMPrepared == s.TMPrepared && t.PreparedMsg == s.PreparedMsg
// @ ensures t.AbortMsg == s.AbortMsg
func TMCommit(s State) (t State) {
	t = s
	t.TM = TMDone
	t.CommitMsg = true
	return t
}

// TMAbort mirrors TMAbort: the transaction manager may abort any time before
// it has decided.
// @ requires s.TM == TMInit
// @ ensures t.TM == TMDone && t.AbortMsg
// @ ensures t.RM == s.RM && t.TMPrepared == s.TMPrepared && t.PreparedMsg == s.PreparedMsg
// @ ensures t.CommitMsg == s.CommitMsg
func TMAbort(s State) (t State) {
	t = s
	t.TM = TMDone
	t.AbortMsg = true
	return t
}

// RMPrepare mirrors RMPrepare(r): a working resource manager prepares and
// tells the transaction manager.
// @ requires 0 <= r && r < N
// @ requires s.RM[r] == Working
// @ ensures t.RM[r] == Prepared && t.PreparedMsg[r]
// @ ensures forall i int :: 0 <= i && i < N && i != r ==> t.RM[i] == s.RM[i] && t.PreparedMsg[i] == s.PreparedMsg[i]
// @ ensures t.TM == s.TM && t.TMPrepared == s.TMPrepared
// @ ensures t.CommitMsg == s.CommitMsg && t.AbortMsg == s.AbortMsg
func RMPrepare(s State, r int) (t State) {
	t = s
	t.RM[r] = Prepared
	t.PreparedMsg[r] = true
	return t
}

// RMChooseToAbort mirrors RMChooseToAbort(r): a resource manager that hasn't
// prepared may abort on its own.
// @ requires 0 <= r && r < N
// @ requires s.RM[r] == Working
// @ ensures t.RM[r] == Aborted
// @ ensures forall i int :: 0 <= i && i < N && i != r ==> t.RM[i] == s.RM[i]
// @ ensures t.TM == s.TM && t.TMPrepared == s.TMPrepared && t.PreparedMsg == s.PreparedMsg
// @ ensures t.CommitMsg == s.CommitMsg && t.AbortMsg == s.AbortMsg
func RMChooseToAbort(s State, r int) (t State) {
	t = s
	t.RM[r] = Aborted
	return t
}

// RMRcvCommitMsg mirrors RMRcvCommitMsg(r): r commits after the Commit
// message has been sent.
// @ requires 0 <= r && r < N
// @ requires s.CommitMsg
// @ ensures t.RM[r] == Committed
// @ ensures forall i int :: 0 <= i && i < N && i != r ==> t.RM[i] == s.RM[i]
// @ ensures t.TM == s.TM && t.TMPrepared == s.TMPrepared && t.PreparedMsg == s.PreparedMsg
// @ ensures t.CommitMsg == s.CommitMsg && t.AbortMsg == s.AbortMsg
func RMRcvCommitMsg(s State, r int) (t State) {
	t = s
	t.RM[r] = Committed
	return t
}

// RMRcvAbortMsg mirrors RMRcvAbortMsg(r): r aborts after the Abort message
// has been sent.
// @ requires 0 <= r && r < N
// @ requires s.AbortMsg
// @ ensures t.RM[r] == Aborted
// @ ensures forall i int :: 0 <= i && i < N && i != r ==> t.RM[i] == s.RM[i]
// @ ensures t.TM == s.TM && t.TMPrepared == s.TMPrepared && t.PreparedMsg == s.PreparedMsg
// @ ensures t.CommitMsg == s.CommitMsg && t.AbortMsg == s.AbortMsg
func RMRcvAbortMsg(s State, r int) (t State) {
	t = s
	t.RM[r] = Aborted
	return t
}

// CanTMCommit reports whether TMCommit is enabled. Its contract is exactly
// TMCommit's precondition, so a caller that checks it can never call
// TMCommit out of turn.
// @ ensures ok == (s.TM == TMInit && forall i int :: 0 <= i && i < N ==> s.TMPrepared[i])
func CanTMCommit(s State) (ok bool) {
	if s.TM != TMInit {
		return false
	}
	//@ invariant 0 <= i && i <= N
	//@ invariant forall j int :: 0 <= j && j < i ==> s.TMPrepared[j]
	for i := 0; i < N; i++ {
		if !s.TMPrepared[i] {
			return false
		}
	}
	return true
}

// Consistent mirrors TCConsistent: no resource manager has committed while
// another has aborted.
// @ ensures ok == !(exists i, j int :: 0 <= i && i < N && 0 <= j && j < N && s.RM[i] == Aborted && s.RM[j] == Committed)
func Consistent(s State) (ok bool) {
	sawAbort, sawCommit := false, false
	//@ invariant 0 <= i && i <= N
	//@ invariant sawAbort == (exists k int :: 0 <= k && k < i && s.RM[k] == Aborted)
	//@ invariant sawCommit == (exists k int :: 0 <= k && k < i && s.RM[k] == Committed)
	for i := 0; i < N; i++ {
		if s.RM[i] == Aborted {
			sawAbort = true
		}
		if s.RM[i] == Committed {
			sawCommit = true
		}
	}
	return !(sawAbort && sawCommit)
}
