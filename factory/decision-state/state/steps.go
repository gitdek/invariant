// +gobra

package state

// Decide records the first line. A one-way door can be proposed or ratified;
// only a person can ratify, including when recording the initial decision.
// @ requires acc(&j.lines, 1/2) && acc(&j.n) && acc(&j.status) && acc(&j.door) && acc(&j.who) && j.Ok()
// @ requires forall k int :: { &j.lines[k] } 0 <= k && k < len(j.lines) ==> acc(&j.lines[k])
// @ ensures acc(&j.lines, 1/2) && acc(&j.n) && acc(&j.status) && acc(&j.door) && acc(&j.who) && j.Ok()
// @ ensures forall k int :: { &j.lines[k] } 0 <= k && k < len(j.lines) ==> acc(&j.lines[k])
// @ ensures len(j.lines) == old(len(j.lines))
// @ ensures ok == (old(j.n) == 0 && old(j.n) < len(j.lines) && w.Valid() && (d == OneWay || d == TwoWay) && (s == Proposed || s == Decided || s == Ratified) && (s != Ratified || w.Kind == Person) && (d != OneWay || s != Decided))
// @ ensures ok ==> j.n == old(j.n) + 1 && j.status == s && j.door == d && j.who == w
// @ ensures ok ==> j.At(old(j.n)) == (Line{DecideOp, w, d, s})
// @ ensures forall k int :: { &j.lines[k] } 0 <= k && k < len(j.lines) && (!ok || k != old(j.n)) ==> j.lines[k] == old(j.lines[k])
// @ ensures !ok ==> j.n == old(j.n) && j.status == old(j.status) && j.door == old(j.door) && j.who == old(j.who)
func (j *Journal) Decide(w Writer, d Door, s Status) (ok bool) {
	if j.n != 0 || j.n == len(j.lines) || !w.Valid() ||
		(d != OneWay && d != TwoWay) ||
		(s != Proposed && s != Decided && s != Ratified) ||
		(s == Ratified && w.Kind != Person) || (d == OneWay && s == Decided) {
		return false
	}
	j.lines[j.n] = Line{DecideOp, w, d, s}
	j.n = j.n + 1
	j.status = s
	j.door = d
	j.who = w
	return true
}

// Ratify appends a person's ratification of a proposed or decided decision.
// @ requires acc(&j.lines, 1/2) && acc(&j.n) && acc(&j.status) && acc(&j.door) && acc(&j.who) && j.Ok()
// @ requires forall k int :: { &j.lines[k] } 0 <= k && k < len(j.lines) ==> acc(&j.lines[k])
// @ ensures acc(&j.lines, 1/2) && acc(&j.n) && acc(&j.status) && acc(&j.door) && acc(&j.who) && j.Ok()
// @ ensures forall k int :: { &j.lines[k] } 0 <= k && k < len(j.lines) ==> acc(&j.lines[k])
// @ ensures len(j.lines) == old(len(j.lines))
// @ ensures ok == (old(j.n) < len(j.lines) && w.Valid() && w.Kind == Person && (old(j.status) == Proposed || old(j.status) == Decided))
// @ ensures ok ==> j.n == old(j.n) + 1 && j.status == Ratified && j.who == w
// @ ensures ok ==> j.At(old(j.n)) == (Line{RatifyOp, w, NoDoor, NoStatus})
// @ ensures j.door == old(j.door)
// @ ensures forall k int :: { &j.lines[k] } 0 <= k && k < len(j.lines) && (!ok || k != old(j.n)) ==> j.lines[k] == old(j.lines[k])
// @ ensures !ok ==> j.n == old(j.n) && j.status == old(j.status) && j.who == old(j.who)
func (j *Journal) Ratify(w Writer) (ok bool) {
	if j.n == len(j.lines) || !w.Valid() || w.Kind != Person ||
		(j.status != Proposed && j.status != Decided) {
		return false
	}
	j.lines[j.n] = Line{RatifyOp, w, NoDoor, NoStatus}
	j.n = j.n + 1
	j.status = Ratified
	j.who = w
	return true
}

// Supersede records that a later decision supersedes this one. It retains
// the door and the deciding or ratifying writer.
// @ requires acc(&j.lines, 1/2) && acc(&j.n) && acc(&j.status) && acc(&j.door) && acc(&j.who) && j.Ok()
// @ requires forall k int :: { &j.lines[k] } 0 <= k && k < len(j.lines) ==> acc(&j.lines[k])
// @ ensures acc(&j.lines, 1/2) && acc(&j.n) && acc(&j.status) && acc(&j.door) && acc(&j.who) && j.Ok()
// @ ensures forall k int :: { &j.lines[k] } 0 <= k && k < len(j.lines) ==> acc(&j.lines[k])
// @ ensures len(j.lines) == old(len(j.lines))
// @ ensures ok == (old(j.n) < len(j.lines) && w.Valid() && (old(j.status) == Proposed || old(j.status) == Decided || old(j.status) == Ratified))
// @ ensures ok ==> j.n == old(j.n) + 1 && j.status == Superseded
// @ ensures ok ==> j.At(old(j.n)) == (Line{SupersedeOp, w, NoDoor, NoStatus})
// @ ensures j.door == old(j.door) && j.who == old(j.who)
// @ ensures forall k int :: { &j.lines[k] } 0 <= k && k < len(j.lines) && (!ok || k != old(j.n)) ==> j.lines[k] == old(j.lines[k])
// @ ensures !ok ==> j.n == old(j.n) && j.status == old(j.status)
func (j *Journal) Supersede(w Writer) (ok bool) {
	if j.n == len(j.lines) || !w.Valid() ||
		(j.status != Proposed && j.status != Decided && j.status != Ratified) {
		return false
	}
	j.lines[j.n] = Line{SupersedeOp, w, NoDoor, NoStatus}
	j.n = j.n + 1
	j.status = Superseded
	return true
}
