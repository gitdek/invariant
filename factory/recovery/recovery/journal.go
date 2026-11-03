// +gobra

package recovery

// FollowJournal retains every accepted Workflow snapshot and every operation.
// The operation capacity is supplied by the caller; the snapshot journal grows.
// Save is the transaction boundary: an adapter must save a successful change
// before dispatching the effect identified by its newly dispatched ledger entry.
// A restarted adapter reconciles dispatched entries instead of resending them.
type FollowJournal struct {
	Control    FollowControl
	Operations []FollowOperation
	Snapshots  []FollowSnapshot
}

// @ requires 0 <= capacity && capacity <= MaxAttempt
// @ ensures j.Control == NewFollowControlValue(identity)
// @ ensures len(j.Operations) == capacity && len(j.Snapshots) == 0
// @ ensures forall i int :: { &j.Operations[i] } 0 <= i && i < len(j.Operations) ==> acc(&j.Operations[i]) && j.Operations[i] == (FollowOperation{})
// @ ensures forall i int :: { &j.Snapshots[i] } 0 <= i && i < len(j.Snapshots) ==> acc(&j.Snapshots[i])
func NewFollowJournal(identity FollowIdentity, capacity int) (j FollowJournal) {
	j.Control = NewFollowControl(identity)
	j.Operations = make([]FollowOperation, capacity)
	j.Snapshots = make([]FollowSnapshot, 0)
	return j
}

// The initial cursor's ghost view is used by the constructor's contract.
// @ ghost
// @ decreases
// @ pure func NewFollowControlValue(identity FollowIdentity) FollowControl {
// @     return FollowControl{Identity:identity, Phase:"inactive", Next:"draft", Left:2, Owner:NoOne, CI:"none", MergeOutcome:"none", Proof:"none", Status:"active", Cause:"none"}
// @ }

// Save persists a prepared transition atomically. It refuses malformed journal
// positions, stale cursors and capacity exhaustion without writing any part of
// the change. A prepared transition cannot be committed twice or over a newer
// operation or result.
// The caller serializes Save under the repository lease, as in the model.
// @ requires acc(&j.Control) && acc(&j.Operations,1/2) && acc(&j.Snapshots)
// @ requires j.Control.Valid() && j.Control.Op <= len(j.Operations) && j.Control.N == len(j.Snapshots)
// @ requires forall i int :: { &j.Operations[i] } 0 <= i && i < len(j.Operations) ==> acc(&j.Operations[i])
// @ requires forall i int :: { &j.Snapshots[i] } 0 <= i && i < len(j.Snapshots) ==> acc(&j.Snapshots[i])
// @ ensures acc(&j.Control) && acc(&j.Operations,1/2) && acc(&j.Snapshots)
// @ ensures forall i int :: { &j.Operations[i] } 0 <= i && i < len(j.Operations) ==> acc(&j.Operations[i])
// @ ensures forall i int :: { &j.Snapshots[i] } 0 <= i && i < len(j.Snapshots) ==> acc(&j.Snapshots[i])
// @ ensures j.Control.Valid() && j.Control.Op <= len(j.Operations) && j.Control.N == len(j.Snapshots)
// @ ensures ok == (x.OK && x.Before == old(j.Control) && (old(j.Control.Op) == 0 || x.Previous == old(j.Operations[j.Control.Op-1])) && x.Control.Valid() && x.Control.Identity == old(j.Control.Identity) && old(j.Control.Op) <= x.Control.Op && x.Control.Op <= old(j.Control.Op)+1 && x.Control.Op <= len(j.Operations) && ((x.Snapshot.Stage == "" && x.Control.N == old(j.Control.N)) || (x.Snapshot.Stage != "" && old(j.Control.N) < MaxAttempt && x.Control.N == old(j.Control.N)+1 && x.Snapshot.Identity == x.Control.Identity)))
// @ ensures !ok ==> j.Control == old(j.Control) && len(j.Snapshots) == old(len(j.Snapshots))
// @ ensures ok ==> j.Control == x.Control
// @ ensures ok && x.Control.Op > 0 ==> j.Operations[x.Control.Op-1] == x.Operation
// @ ensures forall i int :: { &j.Operations[i] } 0 <= i && i < len(j.Operations) && (!ok || i != x.Control.Op-1) ==> j.Operations[i] == old(j.Operations[i])
// @ ensures forall i int :: { &j.Snapshots[i] } 0 <= i && i < old(len(j.Snapshots)) ==> j.Snapshots[i] == old(j.Snapshots[i])
// @ ensures ok && x.Snapshot.Stage != "" ==> j.Snapshots[len(j.Snapshots)-1] == x.Snapshot
func (j *FollowJournal) Save(x FollowChange) (ok bool) {
	if x.Before != j.Control || (j.Control.Op > 0 && x.Previous != j.Operations[j.Control.Op-1]) {
		return false
	}
	if !x.OK || !x.Control.Valid() || x.Control.Identity != j.Control.Identity || x.Control.Op < j.Control.Op || x.Control.Op > j.Control.Op+1 || x.Control.Op > len(j.Operations) {
		return false
	}
	if x.Snapshot.Stage == "" {
		if x.Control.N != j.Control.N {
			return false
		}
	} else {
		if j.Control.N >= MaxAttempt || x.Control.N != j.Control.N+1 || x.Snapshot.Identity != x.Control.Identity {
			return false
		}
		j.Snapshots = append(j.Snapshots, x.Snapshot)
	}
	if x.Control.Op > 0 {
		j.Operations[x.Control.Op-1] = x.Operation
	}
	j.Control = x.Control
	return true
}
