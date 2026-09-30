// +gobra

package state

// Allows checks a complete journal line, including the placeholder fields on
// ratify and supersede lines. It is also the pre-state view used by Apply's
// contract; callers need not check it before calling Apply.
// @ requires acc(&j.lines, _) && acc(&j.n, _) && acc(&j.status, _)
// @ decreases
// @ pure
func (j *Journal) Allows(l Line) bool {
	return j.n < len(j.lines) && l.By.Valid() &&
		((l.Op == DecideOp && j.n == 0 && (l.Door == OneWay || l.Door == TwoWay) &&
			(l.Status == Proposed || l.Status == Decided || l.Status == Ratified) &&
			(l.Status != Ratified || l.By.Kind == Person) &&
			(l.Door != OneWay || l.Status != Decided)) ||
			(l.Door == NoDoor && l.Status == NoStatus &&
				((l.Op == RatifyOp && l.By.Kind == Person && (j.status == Proposed || j.status == Decided)) ||
					(l.Op == SupersedeOp && (j.status == Proposed || j.status == Decided || j.status == Ratified)))))
}

// Apply folds one line. Refusing a line leaves both the journal and state
// unchanged; no later line can hide an invalid earlier line during Fold.
// @ requires acc(&j.lines, 1/2) && acc(&j.n) && acc(&j.status) && acc(&j.door) && acc(&j.who) && j.Ok()
// @ requires forall k int :: { &j.lines[k] } 0 <= k && k < len(j.lines) ==> acc(&j.lines[k])
// @ ensures acc(&j.lines, 1/2) && acc(&j.n) && acc(&j.status) && acc(&j.door) && acc(&j.who) && j.Ok()
// @ ensures forall k int :: { &j.lines[k] } 0 <= k && k < len(j.lines) ==> acc(&j.lines[k])
// @ ensures len(j.lines) == old(len(j.lines))
// @ ensures ok == old(j.Allows(l))
// @ ensures ok ==> j.n == old(j.n) + 1 && j.At(old(j.n)) == l
// @ ensures ok && l.Op == DecideOp ==> j.status == l.Status && j.door == l.Door && j.who == l.By
// @ ensures ok && l.Op == RatifyOp ==> j.status == Ratified && j.door == old(j.door) && j.who == l.By
// @ ensures ok && l.Op == SupersedeOp ==> j.status == Superseded && j.door == old(j.door) && j.who == old(j.who)
// @ ensures forall k int :: { &j.lines[k] } 0 <= k && k < len(j.lines) && (!ok || k != old(j.n)) ==> j.lines[k] == old(j.lines[k])
// @ ensures !ok ==> j.n == old(j.n) && j.status == old(j.status) && j.door == old(j.door) && j.who == old(j.who)
func (j *Journal) Apply(l Line) (ok bool) {
	if l.Op == DecideOp {
		return j.Decide(l.By, l.Door, l.Status)
	}
	if l.Door != NoDoor || l.Status != NoStatus {
		return false
	}
	if l.Op == RatifyOp {
		return j.Ratify(l.By)
	}
	if l.Op == SupersedeOp {
		return j.Supersede(l.By)
	}
	return false
}

// Fold replays a journal from the empty state, stopping at the first refused
// line. accepted is that line's zero-based index, or len(input) on success.
// The returned journal owns its storage; input is neither changed nor kept.
// A reader must reject the journal if accepted != len(input).
// @ requires len(input) <= MaxCapacity
// @ requires forall k int :: { &input[k] } 0 <= k && k < len(input) ==> acc(&input[k], 1/2)
// @ ensures forall k int :: { &input[k] } 0 <= k && k < len(input) ==> acc(&input[k], 1/2)
// @ ensures forall k int :: { &input[k] } 0 <= k && k < len(input) ==> input[k] == old(input[k])
// @ ensures acc(&j.lines) && acc(&j.n) && acc(&j.status) && acc(&j.door) && acc(&j.who) && j.Ok()
// @ ensures forall k int :: { &j.lines[k] } 0 <= k && k < len(j.lines) ==> acc(&j.lines[k])
// @ ensures len(j.lines) == len(input) && 0 <= accepted && accepted <= len(input) && j.n == accepted
// @ ensures forall k int :: { &j.lines[k] } 0 <= k && k < accepted ==> j.lines[k] == input[k]
// @ ensures accepted < len(input) ==> !j.Allows(input[accepted])
func Fold(input []Line) (j *Journal, accepted int) {
	j = New(len(input))
	accepted = 0
	// @ invariant forall k int :: { &input[k] } 0 <= k && k < len(input) ==> acc(&input[k], 1/2)
	// @ invariant forall k int :: { &input[k] } 0 <= k && k < len(input) ==> input[k] == old(input[k])
	// @ invariant acc(&j.lines) && acc(&j.n) && acc(&j.status) && acc(&j.door) && acc(&j.who) && j.Ok()
	// @ invariant forall k int :: { &j.lines[k] } 0 <= k && k < len(j.lines) ==> acc(&j.lines[k])
	// @ invariant len(j.lines) == len(input) && 0 <= accepted && accepted <= len(input) && j.n == accepted
	// @ invariant forall k int :: { &j.lines[k] } 0 <= k && k < accepted ==> j.lines[k] == input[k]
	// @ decreases len(input) - accepted
	for accepted < len(input) {
		if !j.Apply(input[accepted]) {
			return j, accepted
		}
		accepted = accepted + 1
	}
	return j, accepted
}
