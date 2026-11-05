package recovery

// The journal is plain Go, without the // +gobra header: proving its
// quantified slice contracts ran past Gobra's twenty-minute limit, so
// conformance checks it instead. The explorer saves every follow-up
// transition through Save, in every state it reaches.

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

func NewFollowJournal(identity FollowIdentity, capacity int) (j FollowJournal) {
	j.Control = NewFollowControl(identity)
	j.Operations = make([]FollowOperation, capacity)
	j.Snapshots = make([]FollowSnapshot, 0)
	return j
}

// appendSnapshot allocates a new journal image, retaining the entire old
// prefix. The fresh destination cannot overlap the source, so copy's ownership
// and preservation contract applies even if the source has spare capacity.
func appendSnapshot(snapshots []FollowSnapshot, snapshot FollowSnapshot) (next []FollowSnapshot) {
	next = make([]FollowSnapshot, len(snapshots)+1)
	copy(next, snapshots)
	next[len(snapshots)] = snapshot
	return next
}

// permitsSave checks the complete transaction against values read from the
// journal. Keeping validation separate leaves Save with only the atomic writes.
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
