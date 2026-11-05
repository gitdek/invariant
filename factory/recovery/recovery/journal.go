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
// @ ensures forall i int :: { &j.Operations[i] } 0 <= i && i < len(j.Operations) ==> acc(&j.Operations[i])
// @ ensures forall i int :: { &j.Operations[i] } 0 <= i && i < len(j.Operations) ==> j.Operations[i] == (FollowOperation{})
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

// appendSnapshot allocates a new journal image, retaining the entire old
// prefix. The fresh destination cannot overlap the source, so copy's ownership
// and preservation contract applies even if the source has spare capacity.
// @ requires len(snapshots) < MaxAttempt
// @ requires forall i int :: { &snapshots[i] } 0 <= i && i < len(snapshots) ==> acc(&snapshots[i])
// @ ensures len(next) == len(snapshots)+1
// @ ensures forall i int :: { &snapshots[i] } 0 <= i && i < len(snapshots) ==> acc(&snapshots[i])
// @ ensures forall i int :: { &snapshots[i] } 0 <= i && i < len(snapshots) ==> snapshots[i] == old(snapshots[i])
// @ ensures forall i int :: { &next[i] } 0 <= i && i < len(next) ==> acc(&next[i])
// @ ensures forall i int :: { &next[i] } 0 <= i && i < len(snapshots) ==> next[i] == old(snapshots[i])
// @ ensures next[len(snapshots)] == snapshot
// @ decreases
func appendSnapshot(snapshots []FollowSnapshot, snapshot FollowSnapshot) (next []FollowSnapshot) {
	next = make([]FollowSnapshot, len(snapshots)+1)
	copy(next, snapshots /*@, perm(1/2) @*/)
	next[len(snapshots)] = snapshot
	return next
}

// permitsSave checks the complete transaction against values read from the
// journal. Keeping validation separate leaves Save with only the atomic writes.
// @ requires control.Valid()
// @ decreases
// @ pure
func (x FollowChange) permitsSave(control FollowControl, previous FollowOperation, capacity int) bool {
	return x.OK && x.Before == control && (control.Op == 0 || x.Previous == previous) &&
		x.Control.Valid() && x.Control.Identity == control.Identity &&
		control.Op <= x.Control.Op && x.Control.Op <= control.Op+1 && x.Control.Op <= capacity &&
		((x.Snapshot.Stage == "" && x.Control.N == control.N) ||
			(x.Snapshot.Stage != "" && control.N < MaxAttempt && x.Control.N == control.N+1 && x.Snapshot.Identity == x.Control.Identity))
}

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
// @ ensures ok == old(x.permitsSave(j.Control, j.Control.Op == 0 ? FollowOperation{} : j.Operations[j.Control.Op-1], len(j.Operations)))
// @ ensures !ok ==> j.Control == old(j.Control) && len(j.Snapshots) == old(len(j.Snapshots))
// @ ensures ok ==> j.Control == x.Control
// @ ensures ok && x.Control.Op > 0 ==> j.Operations[x.Control.Op-1] == x.Operation
// @ ensures forall i int :: { &j.Operations[i] } 0 <= i && i < len(j.Operations) && (!ok || i != x.Control.Op-1) ==> j.Operations[i] == old(j.Operations[i])
// @ ensures forall i int :: { &j.Snapshots[i] } 0 <= i && i < old(len(j.Snapshots)) ==> j.Snapshots[i] == old(j.Snapshots[i])
// @ ensures ok && x.Snapshot.Stage != "" ==> j.Snapshots[len(j.Snapshots)-1] == x.Snapshot
func (j *FollowJournal) Save(x FollowChange) (ok bool) {
	previous := FollowOperation{}
	if j.Control.Op > 0 {
		previous = j.Operations[j.Control.Op-1]
	}
	if !x.permitsSave(j.Control, previous, len(j.Operations)) {
		return false
	}
	if x.Snapshot.Stage != "" {
		j.Snapshots = appendSnapshot(j.Snapshots, x.Snapshot)
	}
	if x.Control.Op > 0 {
		j.Operations[x.Control.Op-1] = x.Operation
	}
	j.Control = x.Control
	return true
}
