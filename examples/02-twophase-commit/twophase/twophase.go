// +gobra

// Package twophase implements two-phase commit for any number of resource
// managers.
//
// Each operation mirrors one action of ../.invariant/specs/TwoPhase.tla. It
// decides for itself whether the action can run: where it can't, the
// operation refuses, returning false and changing nothing. Its Gobra contract
// says both outcomes, including everything the action leaves unchanged. The
// messages the protocol sends are the network's, not the transaction's, so
// they live with the caller: an operation that sends one reports that it ran,
// and an operation that receives one is called once it has arrived.
package twophase

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

// MaxRMs keeps index arithmetic far from overflow. It's the machine's limit,
// not the model's.
const MaxRMs = 1 << 30

// Tx is one transaction across len(RM) resource managers.
type Tx struct {
	RM         []RMState // rmState
	TM         TMState   // tmState
	TMPrepared []bool    // r \in tmPrepared
}

// Ok says the transaction's slices agree on the number of resource managers.
// @ requires acc(&t.RM, _) && acc(&t.TMPrepared, _)
// @ decreases
// @ pure
func (t *Tx) Ok() bool {
	return len(t.TMPrepared) == len(t.RM) && len(t.RM) <= MaxRMs
}

// New mirrors Init: n resource managers, all working, and a transaction
// manager that has heard from none of them.
// @ requires 0 <= n && n <= MaxRMs
// @ ensures acc(&t.RM) && acc(&t.TMPrepared) && acc(&t.TM) && t.Ok()
// @ ensures forall j int :: { &t.RM[j] } 0 <= j && j < len(t.RM) ==> acc(&t.RM[j])
// @ ensures forall j int :: { &t.TMPrepared[j] } 0 <= j && j < len(t.TMPrepared) ==> acc(&t.TMPrepared[j])
// @ ensures len(t.RM) == n && t.TM == TMInit
// @ ensures forall i int :: { t.RM[i] } 0 <= i && i < n ==> t.RM[i] == Working
// @ ensures forall i int :: { t.TMPrepared[i] } 0 <= i && i < n ==> !t.TMPrepared[i]
func New(n int) (t *Tx) {
	t = &Tx{RM: make([]RMState, n), TM: TMInit, TMPrepared: make([]bool, n)}
	return t
}

// TMRcvPrepared mirrors TMRcvPrepared(r): the transaction manager records
// r's Prepared message, and refuses once it has decided.
// @ requires acc(&t.RM, 1/2) && acc(&t.TMPrepared, 1/2) && acc(&t.TM) && t.Ok()
// @ requires forall j int :: { &t.RM[j] } 0 <= j && j < len(t.RM) ==> acc(&t.RM[j])
// @ requires forall j int :: { &t.TMPrepared[j] } 0 <= j && j < len(t.TMPrepared) ==> acc(&t.TMPrepared[j])
// @ requires 0 <= r && r < len(t.RM)
// @ ensures acc(&t.RM, 1/2) && acc(&t.TMPrepared, 1/2) && acc(&t.TM) && t.Ok()
// @ ensures forall j int :: { &t.RM[j] } 0 <= j && j < len(t.RM) ==> acc(&t.RM[j])
// @ ensures forall j int :: { &t.TMPrepared[j] } 0 <= j && j < len(t.TMPrepared) ==> acc(&t.TMPrepared[j])
// @ ensures len(t.RM) == old(len(t.RM)) && len(t.TMPrepared) == old(len(t.TMPrepared))
// @ ensures ok == (old(t.TM) == TMInit)
// @ ensures ok ==> t.TMPrepared[r]
// @ ensures !ok ==> t.TMPrepared[r] == old(t.TMPrepared[r])
// @ ensures forall i int :: { t.TMPrepared[i] } 0 <= i && i < len(t.TMPrepared) && i != r ==> t.TMPrepared[i] == old(t.TMPrepared[i])
// @ ensures forall i int :: { t.RM[i] } 0 <= i && i < len(t.RM) ==> t.RM[i] == old(t.RM[i])
// @ ensures t.TM == old(t.TM)
func (t *Tx) TMRcvPrepared(r int) (ok bool) {
	if t.TM != TMInit {
		return false
	}
	t.TMPrepared[r] = true
	return true
}

// allPrepared says whether the transaction manager has heard from every
// resource manager.
// @ requires acc(&t.TMPrepared, 1/4) && len(t.TMPrepared) <= MaxRMs
// @ requires forall j int :: { &t.TMPrepared[j] } 0 <= j && j < len(t.TMPrepared) ==> acc(&t.TMPrepared[j], 1/4)
// @ ensures acc(&t.TMPrepared, 1/4)
// @ ensures forall j int :: { &t.TMPrepared[j] } 0 <= j && j < len(t.TMPrepared) ==> acc(&t.TMPrepared[j], 1/4)
// @ ensures len(t.TMPrepared) == old(len(t.TMPrepared))
// @ ensures ok == (forall i int :: { t.TMPrepared[i] } 0 <= i && i < len(t.TMPrepared) ==> t.TMPrepared[i])
func (t *Tx) allPrepared() (ok bool) {
	// @ invariant acc(&t.TMPrepared, 1/8) && len(t.TMPrepared) <= MaxRMs
	// @ invariant forall j int :: { &t.TMPrepared[j] } 0 <= j && j < len(t.TMPrepared) ==> acc(&t.TMPrepared[j], 1/8)
	// @ invariant 0 <= i && i <= len(t.TMPrepared)
	// @ invariant forall j int :: { t.TMPrepared[j] } 0 <= j && j < i ==> t.TMPrepared[j]
	for i := 0; i < len(t.TMPrepared); i++ {
		if !t.TMPrepared[i] {
			return false
		}
	}
	return true
}

// TMCommit mirrors TMCommit: once every resource manager has prepared, the
// transaction manager commits. It refuses before then, and once it has
// decided. The caller sends Commit when it runs.
// @ requires acc(&t.RM, 1/2) && acc(&t.TMPrepared, 1/2) && acc(&t.TM) && t.Ok()
// @ requires forall j int :: { &t.RM[j] } 0 <= j && j < len(t.RM) ==> acc(&t.RM[j])
// @ requires forall j int :: { &t.TMPrepared[j] } 0 <= j && j < len(t.TMPrepared) ==> acc(&t.TMPrepared[j])
// @ ensures acc(&t.RM, 1/2) && acc(&t.TMPrepared, 1/2) && acc(&t.TM) && t.Ok()
// @ ensures forall j int :: { &t.RM[j] } 0 <= j && j < len(t.RM) ==> acc(&t.RM[j])
// @ ensures forall j int :: { &t.TMPrepared[j] } 0 <= j && j < len(t.TMPrepared) ==> acc(&t.TMPrepared[j])
// @ ensures len(t.RM) == old(len(t.RM)) && len(t.TMPrepared) == old(len(t.TMPrepared))
// @ ensures ok == (old(t.TM) == TMInit && (forall i int :: { t.TMPrepared[i] } 0 <= i && i < len(t.TMPrepared) ==> t.TMPrepared[i]))
// @ ensures ok ==> t.TM == TMDone
// @ ensures !ok ==> t.TM == old(t.TM)
// @ ensures forall i int :: { t.TMPrepared[i] } 0 <= i && i < len(t.TMPrepared) ==> t.TMPrepared[i] == old(t.TMPrepared[i])
// @ ensures forall i int :: { t.RM[i] } 0 <= i && i < len(t.RM) ==> t.RM[i] == old(t.RM[i])
func (t *Tx) TMCommit() (ok bool) {
	if t.TM != TMInit {
		return false
	}
	if !t.allPrepared() {
		return false
	}
	t.TM = TMDone
	return true
}

// TMAbort mirrors TMAbort: the transaction manager may abort any time before
// it has decided, and refuses after. The caller sends Abort when it runs.
// @ requires acc(&t.RM, 1/2) && acc(&t.TMPrepared, 1/2) && acc(&t.TM) && t.Ok()
// @ requires forall j int :: { &t.RM[j] } 0 <= j && j < len(t.RM) ==> acc(&t.RM[j])
// @ requires forall j int :: { &t.TMPrepared[j] } 0 <= j && j < len(t.TMPrepared) ==> acc(&t.TMPrepared[j])
// @ ensures acc(&t.RM, 1/2) && acc(&t.TMPrepared, 1/2) && acc(&t.TM) && t.Ok()
// @ ensures forall j int :: { &t.RM[j] } 0 <= j && j < len(t.RM) ==> acc(&t.RM[j])
// @ ensures forall j int :: { &t.TMPrepared[j] } 0 <= j && j < len(t.TMPrepared) ==> acc(&t.TMPrepared[j])
// @ ensures len(t.RM) == old(len(t.RM)) && len(t.TMPrepared) == old(len(t.TMPrepared))
// @ ensures ok == (old(t.TM) == TMInit)
// @ ensures ok ==> t.TM == TMDone
// @ ensures !ok ==> t.TM == old(t.TM)
// @ ensures forall i int :: { t.TMPrepared[i] } 0 <= i && i < len(t.TMPrepared) ==> t.TMPrepared[i] == old(t.TMPrepared[i])
// @ ensures forall i int :: { t.RM[i] } 0 <= i && i < len(t.RM) ==> t.RM[i] == old(t.RM[i])
func (t *Tx) TMAbort() (ok bool) {
	if t.TM != TMInit {
		return false
	}
	t.TM = TMDone
	return true
}

// RMPrepare mirrors RMPrepare(r): a working resource manager prepares, and
// one that isn't working refuses. The caller sends r's Prepared when it runs.
// @ requires acc(&t.RM, 1/2) && acc(&t.TMPrepared, 1/2) && acc(&t.TM) && t.Ok()
// @ requires forall j int :: { &t.RM[j] } 0 <= j && j < len(t.RM) ==> acc(&t.RM[j])
// @ requires forall j int :: { &t.TMPrepared[j] } 0 <= j && j < len(t.TMPrepared) ==> acc(&t.TMPrepared[j])
// @ requires 0 <= r && r < len(t.RM)
// @ ensures acc(&t.RM, 1/2) && acc(&t.TMPrepared, 1/2) && acc(&t.TM) && t.Ok()
// @ ensures forall j int :: { &t.RM[j] } 0 <= j && j < len(t.RM) ==> acc(&t.RM[j])
// @ ensures forall j int :: { &t.TMPrepared[j] } 0 <= j && j < len(t.TMPrepared) ==> acc(&t.TMPrepared[j])
// @ ensures len(t.RM) == old(len(t.RM)) && len(t.TMPrepared) == old(len(t.TMPrepared))
// @ ensures ok == (old(t.RM[r]) == Working)
// @ ensures ok ==> t.RM[r] == Prepared
// @ ensures !ok ==> t.RM[r] == old(t.RM[r])
// @ ensures forall i int :: { t.RM[i] } 0 <= i && i < len(t.RM) && i != r ==> t.RM[i] == old(t.RM[i])
// @ ensures forall i int :: { t.TMPrepared[i] } 0 <= i && i < len(t.TMPrepared) ==> t.TMPrepared[i] == old(t.TMPrepared[i])
// @ ensures t.TM == old(t.TM)
func (t *Tx) RMPrepare(r int) (ok bool) {
	if t.RM[r] != Working {
		return false
	}
	t.RM[r] = Prepared
	return true
}

// RMChooseToAbort mirrors RMChooseToAbort(r): a resource manager that hasn't
// prepared may abort on its own, and one that isn't working refuses.
// @ requires acc(&t.RM, 1/2) && acc(&t.TMPrepared, 1/2) && acc(&t.TM) && t.Ok()
// @ requires forall j int :: { &t.RM[j] } 0 <= j && j < len(t.RM) ==> acc(&t.RM[j])
// @ requires forall j int :: { &t.TMPrepared[j] } 0 <= j && j < len(t.TMPrepared) ==> acc(&t.TMPrepared[j])
// @ requires 0 <= r && r < len(t.RM)
// @ ensures acc(&t.RM, 1/2) && acc(&t.TMPrepared, 1/2) && acc(&t.TM) && t.Ok()
// @ ensures forall j int :: { &t.RM[j] } 0 <= j && j < len(t.RM) ==> acc(&t.RM[j])
// @ ensures forall j int :: { &t.TMPrepared[j] } 0 <= j && j < len(t.TMPrepared) ==> acc(&t.TMPrepared[j])
// @ ensures len(t.RM) == old(len(t.RM)) && len(t.TMPrepared) == old(len(t.TMPrepared))
// @ ensures ok == (old(t.RM[r]) == Working)
// @ ensures ok ==> t.RM[r] == Aborted
// @ ensures !ok ==> t.RM[r] == old(t.RM[r])
// @ ensures forall i int :: { t.RM[i] } 0 <= i && i < len(t.RM) && i != r ==> t.RM[i] == old(t.RM[i])
// @ ensures forall i int :: { t.TMPrepared[i] } 0 <= i && i < len(t.TMPrepared) ==> t.TMPrepared[i] == old(t.TMPrepared[i])
// @ ensures t.TM == old(t.TM)
func (t *Tx) RMChooseToAbort(r int) (ok bool) {
	if t.RM[r] != Working {
		return false
	}
	t.RM[r] = Aborted
	return true
}

// RMRcvCommitMsg mirrors RMRcvCommitMsg(r): r commits on receiving Commit.
// The caller delivers the message only once it has been sent.
// @ requires acc(&t.RM, 1/2) && acc(&t.TMPrepared, 1/2) && acc(&t.TM) && t.Ok()
// @ requires forall j int :: { &t.RM[j] } 0 <= j && j < len(t.RM) ==> acc(&t.RM[j])
// @ requires forall j int :: { &t.TMPrepared[j] } 0 <= j && j < len(t.TMPrepared) ==> acc(&t.TMPrepared[j])
// @ requires 0 <= r && r < len(t.RM)
// @ ensures acc(&t.RM, 1/2) && acc(&t.TMPrepared, 1/2) && acc(&t.TM) && t.Ok()
// @ ensures forall j int :: { &t.RM[j] } 0 <= j && j < len(t.RM) ==> acc(&t.RM[j])
// @ ensures forall j int :: { &t.TMPrepared[j] } 0 <= j && j < len(t.TMPrepared) ==> acc(&t.TMPrepared[j])
// @ ensures len(t.RM) == old(len(t.RM)) && len(t.TMPrepared) == old(len(t.TMPrepared))
// @ ensures t.RM[r] == Committed
// @ ensures forall i int :: { t.RM[i] } 0 <= i && i < len(t.RM) && i != r ==> t.RM[i] == old(t.RM[i])
// @ ensures forall i int :: { t.TMPrepared[i] } 0 <= i && i < len(t.TMPrepared) ==> t.TMPrepared[i] == old(t.TMPrepared[i])
// @ ensures t.TM == old(t.TM)
func (t *Tx) RMRcvCommitMsg(r int) {
	t.RM[r] = Committed
}

// RMRcvAbortMsg mirrors RMRcvAbortMsg(r): r aborts on receiving Abort. The
// caller delivers the message only once it has been sent.
// @ requires acc(&t.RM, 1/2) && acc(&t.TMPrepared, 1/2) && acc(&t.TM) && t.Ok()
// @ requires forall j int :: { &t.RM[j] } 0 <= j && j < len(t.RM) ==> acc(&t.RM[j])
// @ requires forall j int :: { &t.TMPrepared[j] } 0 <= j && j < len(t.TMPrepared) ==> acc(&t.TMPrepared[j])
// @ requires 0 <= r && r < len(t.RM)
// @ ensures acc(&t.RM, 1/2) && acc(&t.TMPrepared, 1/2) && acc(&t.TM) && t.Ok()
// @ ensures forall j int :: { &t.RM[j] } 0 <= j && j < len(t.RM) ==> acc(&t.RM[j])
// @ ensures forall j int :: { &t.TMPrepared[j] } 0 <= j && j < len(t.TMPrepared) ==> acc(&t.TMPrepared[j])
// @ ensures len(t.RM) == old(len(t.RM)) && len(t.TMPrepared) == old(len(t.TMPrepared))
// @ ensures t.RM[r] == Aborted
// @ ensures forall i int :: { t.RM[i] } 0 <= i && i < len(t.RM) && i != r ==> t.RM[i] == old(t.RM[i])
// @ ensures forall i int :: { t.TMPrepared[i] } 0 <= i && i < len(t.TMPrepared) ==> t.TMPrepared[i] == old(t.TMPrepared[i])
// @ ensures t.TM == old(t.TM)
func (t *Tx) RMRcvAbortMsg(r int) {
	t.RM[r] = Aborted
}
