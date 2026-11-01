// +gobra

package merges

// Taking main in reads and writes every file, so each file is held behind a
// predicate: a step on one file unfolds only that file, and what the proof
// knows of the others carries over untouched.

// Whose file a row is, and which table spots are.
const (
	inMain   = false
	inBranch = true
	isFrom   = false
	isTo     = true
)

// row is permission to file f's room lines, in main's journal or in the
// branch's. Naming the file and the journal keeps every file's instance
// distinct, so the proof never asks whether two of them are one.
// @ pred row(r []Line, room int, f int, branch bool) {
// @ 	len(r) == room && forall k int :: { &r[k] } 0 <= k && k < room ==> acc(&r[k])
// @ }

// at is line k of file f behind row.
// @ ghost
// @ requires acc(row(r, room, f, branch), _) && 0 <= k && k < room
// @ decreases
// @ pure func at(r []Line, room int, f int, branch bool, k int) Line {
// @ 	return unfolding acc(row(r, room, f, branch), _) in r[k]
// @ }

// spots is permission to room positions in file f, in the from table or the
// to table.
// @ pred spots(r []Spot, room int, f int, to bool) {
// @ 	len(r) == room && forall k int :: { &r[k] } 0 <= k && k < room ==> acc(&r[k])
// @ }

// spot is position k of file f behind spots.
// @ ghost
// @ requires acc(spots(r, room, f, to), _) && 0 <= k && k < room
// @ decreases
// @ pure func spot(r []Spot, room int, f int, to bool, k int) Spot {
// @ 	return unfolding acc(spots(r, room, f, to), _) in r[k]
// @ }

// TakeMainIn takes main in to the branch, as long as one of main's files
// isn't the start of the branch's and each of the branch's files has room
// for main's lines beside its own. Each of the branch's files is then main's
// lines, where they are, followed by the branch's own lines that main's file
// doesn't have, in order. Otherwise it changes nothing. For the proof, it
// says where each of those lines was in the branch's file, in from, and
// where each of the branch's lines that main's file lacked went, in to.
// @ requires acc(&s.Lines, 1/2) && acc(&s.Len, 1/2) && acc(&s.Room, 1/2) && 0 <= s.Room && s.Room <= MaxSize
// @ requires len(s.Len) == len(s.Lines) && len(s.Lines) <= MaxSize
// @ requires forall f int :: { &s.Len[f] } 0 <= f && f < len(s.Len) ==> acc(&s.Len[f], 1/2) && 0 <= s.Len[f] && s.Len[f] <= s.Room
// @ requires forall f int :: { &s.Lines[f] } 0 <= f && f < len(s.Lines) ==> acc(&s.Lines[f], 1/2) && acc(row(s.Lines[f], s.Room, f, inMain), 1/2)
// @ requires acc(&b.Lines, 1/2) && acc(&b.Len, 1/2) && len(b.Lines) == len(s.Lines) && len(b.Len) == len(s.Lines)
// @ requires forall f int :: { &b.Len[f] } 0 <= f && f < len(b.Len) ==> acc(&b.Len[f]) && 0 <= b.Len[f] && b.Len[f] <= s.Room
// @ requires forall f int :: { &b.Lines[f] } 0 <= f && f < len(b.Lines) ==> acc(&b.Lines[f], 1/2) && row(b.Lines[f], s.Room, f, inBranch)
// @ ensures acc(&s.Lines, 1/2) && acc(&s.Len, 1/2) && acc(&s.Room, 1/2) && s.Room == old(s.Room)
// @ ensures len(s.Len) == old(len(s.Len)) && len(s.Lines) == old(len(s.Lines))
// @ ensures forall f int :: { &s.Len[f] } 0 <= f && f < len(s.Len) ==> acc(&s.Len[f], 1/2)
// @ ensures forall f int :: { &s.Lines[f] } 0 <= f && f < len(s.Lines) ==> acc(&s.Lines[f], 1/2) && acc(row(s.Lines[f], s.Room, f, inMain), 1/2)
// @ ensures acc(&b.Lines, 1/2) && acc(&b.Len, 1/2) && len(b.Lines) == old(len(b.Lines)) && len(b.Len) == old(len(b.Len))
// @ ensures forall f int :: { &b.Len[f] } 0 <= f && f < len(b.Len) ==> acc(&b.Len[f]) && 0 <= b.Len[f] && b.Len[f] <= s.Room
// @ ensures forall f int :: { &b.Lines[f] } 0 <= f && f < len(b.Lines) ==> acc(&b.Lines[f], 1/2) && row(b.Lines[f], s.Room, f, inBranch)
// @ ensures forall f int :: { &s.Len[f] } { old(s.Len[f]) } 0 <= f && f < len(s.Len) ==> s.Len[f] == old(s.Len[f])
// @ ensures forall f, k int :: { at(s.Lines[f], s.Room, f, inMain, k) } 0 <= f && f < len(s.Lines) && 0 <= k && k < s.Room ==> at(s.Lines[f], s.Room, f, inMain, k) == old(at(s.Lines[f], s.Room, f, inMain, k))
// @ ensures ran == (!(forall f int :: { old(s.Len[f]) } 0 <= f && f < len(s.Len) ==> old(s.Len[f]) <= old(b.Len[f]) && (forall k int :: { old(at(s.Lines[f], s.Room, f, inMain, k)) } 0 <= k && k < old(s.Len[f]) ==> old(at(s.Lines[f], s.Room, f, inMain, k)) == old(at(b.Lines[f], s.Room, f, inBranch, k)))) && (forall f int :: { old(s.Len[f]) } 0 <= f && f < len(s.Len) ==> old(s.Len[f]) + old(b.Len[f]) <= s.Room))
// @ ensures !ran ==> forall f int :: { &b.Len[f] } { old(b.Len[f]) } 0 <= f && f < len(b.Len) ==> b.Len[f] == old(b.Len[f])
// @ ensures !ran ==> forall f, k int :: { at(b.Lines[f], s.Room, f, inBranch, k) } 0 <= f && f < len(b.Lines) && 0 <= k && k < s.Room ==> at(b.Lines[f], s.Room, f, inBranch, k) == old(at(b.Lines[f], s.Room, f, inBranch, k))
// @ ensures ran ==> len(from) == len(s.Lines) && len(to) == len(s.Lines)
// @ ensures ran ==> forall f int :: { &from[f] } 0 <= f && f < len(from) ==> acc(&from[f], 1/2) && spots(from[f], s.Room, f, isFrom)
// @ ensures ran ==> forall f int :: { &to[f] } 0 <= f && f < len(to) ==> acc(&to[f], 1/2) && spots(to[f], s.Room, f, isTo)
// @ ensures ran ==> forall f int :: { &b.Len[f] } 0 <= f && f < len(b.Len) ==> s.Len[f] <= b.Len[f] && b.Len[f] <= s.Len[f] + old(b.Len[f])
// @ ensures ran ==> forall f, k int :: { at(b.Lines[f], s.Room, f, inBranch, k) } 0 <= f && f < len(b.Lines) && 0 <= k && k < s.Len[f] ==> at(b.Lines[f], s.Room, f, inBranch, k) == at(s.Lines[f], s.Room, f, inMain, k)
// @ ensures ran ==> forall f, k int :: { at(b.Lines[f], s.Room, f, inBranch, k) } { spot(from[f], s.Room, f, isFrom, k) } 0 <= f && f < len(b.Lines) && s.Len[f] <= k && k < b.Len[f] ==> let j := spot(from[f], s.Room, f, isFrom, k).At in 0 <= j && j < old(b.Len[f]) && at(b.Lines[f], s.Room, f, inBranch, k) == old(at(b.Lines[f], s.Room, f, inBranch, j))
// @ ensures ran ==> forall f, k, i int :: { at(b.Lines[f], s.Room, f, inBranch, k), at(s.Lines[f], s.Room, f, inMain, i) } 0 <= f && f < len(b.Lines) && s.Len[f] <= k && k < b.Len[f] && 0 <= i && i < s.Len[f] ==> at(b.Lines[f], s.Room, f, inBranch, k) != at(s.Lines[f], s.Room, f, inMain, i)
// @ ensures ran ==> forall f, q int :: { spot(to[f], s.Room, f, isTo, q) } 0 <= f && f < len(b.Lines) && 0 <= q && q < old(b.Len[f]) && !(exists i int :: { at(s.Lines[f], s.Room, f, inMain, i) } 0 <= i && i < s.Len[f] && at(s.Lines[f], s.Room, f, inMain, i) == old(at(b.Lines[f], s.Room, f, inBranch, q))) ==> s.Len[f] <= spot(to[f], s.Room, f, isTo, q).At && spot(to[f], s.Room, f, isTo, q).At < b.Len[f] && at(b.Lines[f], s.Room, f, inBranch, spot(to[f], s.Room, f, isTo, q).At) == old(at(b.Lines[f], s.Room, f, inBranch, q))
func (s *Core) TakeMainIn(b *Branch) (ran bool, from, to [][]Spot) {
	room := s.Room
	if startsFrom(s.Lines, s.Len, b.Lines, b.Len, 0, room /*@, 1/4 @*/) || !s.fits(b) {
		return false, nil, nil
	}
	from = spotRows(len(s.Lines), room /*@, isFrom @*/)
	to = spotRows(len(s.Lines), room /*@, isTo @*/)
	// @ assert forall f, k int :: { at(s.Lines[f], room, f, inMain, k) } { old(at(s.Lines[f], s.Room, f, inMain, k)) } 0 <= f && f < len(s.Lines) && 0 <= k && k < room ==> at(s.Lines[f], room, f, inMain, k) == old(at(s.Lines[f], s.Room, f, inMain, k))
	// @ assert forall f, k int :: { at(b.Lines[f], room, f, inBranch, k) } { old(at(b.Lines[f], s.Room, f, inBranch, k)) } 0 <= f && f < len(b.Lines) && 0 <= k && k < room ==> at(b.Lines[f], room, f, inBranch, k) == old(at(b.Lines[f], s.Room, f, inBranch, k))
	takeFrom(s.Lines, s.Len, b.Lines, b.Len, from, to, 0, room /*@, 1/4 @*/)
	return true, from, to
}

// startsFrom says whether each of main's files from file f on is the start
// of the branch's. It reads both with the share q of permission it's given,
// and gives each next call half of it.
// @ requires q > 0 && 0 <= f && f <= len(sl) && len(sl) <= MaxSize && 0 <= room && room <= MaxSize
// @ requires len(sn) == len(sl) && len(bl) == len(sl) && len(bn) == len(sl)
// @ requires forall g int :: { &sn[g] } 0 <= g && g < len(sn) ==> acc(&sn[g], q) && 0 <= sn[g] && sn[g] <= room
// @ requires forall g int :: { &bn[g] } 0 <= g && g < len(bn) ==> acc(&bn[g], q) && 0 <= bn[g] && bn[g] <= room
// @ requires forall g int :: { &sl[g] } 0 <= g && g < len(sl) ==> acc(&sl[g], q) && acc(row(sl[g], room, g, inMain), q)
// @ requires forall g int :: { &bl[g] } 0 <= g && g < len(bl) ==> acc(&bl[g], q) && acc(row(bl[g], room, g, inBranch), q)
// @ ensures forall g int :: { &sn[g] } 0 <= g && g < len(sn) ==> acc(&sn[g], q)
// @ ensures forall g int :: { &bn[g] } 0 <= g && g < len(bn) ==> acc(&bn[g], q)
// @ ensures forall g int :: { &sl[g] } 0 <= g && g < len(sl) ==> acc(&sl[g], q) && acc(row(sl[g], room, g, inMain), q)
// @ ensures forall g int :: { &bl[g] } 0 <= g && g < len(bl) ==> acc(&bl[g], q) && acc(row(bl[g], room, g, inBranch), q)
// @ ensures forall g int :: { &sn[g] } { old(sn[g]) } 0 <= g && g < len(sn) ==> sn[g] == old(sn[g])
// @ ensures forall g int :: { &bn[g] } { old(bn[g]) } 0 <= g && g < len(bn) ==> bn[g] == old(bn[g])
// @ ensures forall g, k int :: { at(sl[g], room, g, inMain, k) } 0 <= g && g < len(sl) && 0 <= k && k < room ==> at(sl[g], room, g, inMain, k) == old(at(sl[g], room, g, inMain, k))
// @ ensures forall g, k int :: { at(bl[g], room, g, inBranch, k) } 0 <= g && g < len(bl) && 0 <= k && k < room ==> at(bl[g], room, g, inBranch, k) == old(at(bl[g], room, g, inBranch, k))
// @ ensures ok == (forall g int :: { old(sn[g]) } f <= g && g < len(sl) ==> old(sn[g]) <= old(bn[g]) && (forall k int :: { old(at(sl[g], room, g, inMain, k)) } 0 <= k && k < old(sn[g]) ==> old(at(sl[g], room, g, inMain, k)) == old(at(bl[g], room, g, inBranch, k))))
// @ decreases len(sl) - f
func startsFrom(sl [][]Line, sn []int, bl [][]Line, bn []int, f, room int /*@, ghost q perm @*/) (ok bool) {
	if f == len(sl) {
		return true
	}
	if !startsOne(sl[f], sn[f], bl[f], bn[f], f, room /*@, q/2 @*/) {
		return false
	}
	// @ assert forall g, k int :: { at(sl[g], room, g, inMain, k) } { old(at(sl[g], room, g, inMain, k)) } 0 <= g && g < len(sl) && 0 <= k && k < room ==> at(sl[g], room, g, inMain, k) == old(at(sl[g], room, g, inMain, k))
	// @ assert forall g, k int :: { at(bl[g], room, g, inBranch, k) } { old(at(bl[g], room, g, inBranch, k)) } 0 <= g && g < len(bl) && 0 <= k && k < room ==> at(bl[g], room, g, inBranch, k) == old(at(bl[g], room, g, inBranch, k))
	return startsFrom(sl, sn, bl, bn, f+1, room /*@, q/2 @*/)
}

// startsOne says whether main's file m is the start of the branch's file b,
// which is file f of each. It reads both with the share q of permission it's
// given.
// @ requires q > 0 && 0 <= mlen && mlen <= room && 0 <= blen && blen <= room && room <= MaxSize
// @ requires acc(row(m, room, f, inMain), q) && acc(row(b, room, f, inBranch), q)
// @ ensures acc(row(m, room, f, inMain), q) && acc(row(b, room, f, inBranch), q)
// @ ensures forall k int :: { at(m, room, f, inMain, k) } 0 <= k && k < room ==> at(m, room, f, inMain, k) == old(at(m, room, f, inMain, k))
// @ ensures forall k int :: { at(b, room, f, inBranch, k) } 0 <= k && k < room ==> at(b, room, f, inBranch, k) == old(at(b, room, f, inBranch, k))
// @ ensures ok == (mlen <= blen && forall k int :: { old(at(m, room, f, inMain, k)) } 0 <= k && k < mlen ==> old(at(m, room, f, inMain, k)) == old(at(b, room, f, inBranch, k)))
// @ decreases
func startsOne(m []Line, mlen int, b []Line, blen int, f, room int /*@, ghost q perm @*/) (ok bool) {
	// @ unfold acc(row(m, room, f, inMain), q)
	// @ unfold acc(row(b, room, f, inBranch), q)
	// @ assert forall k int :: { &m[k] } { old(at(m, room, f, inMain, k)) } 0 <= k && k < room ==> m[k] == old(at(m, room, f, inMain, k))
	// @ assert forall k int :: { &b[k] } { old(at(b, room, f, inBranch, k)) } 0 <= k && k < room ==> b[k] == old(at(b, room, f, inBranch, k))
	ok = startsWith(m, mlen, b, blen /*@, q @*/)
	// @ fold acc(row(m, room, f, inMain), q)
	// @ fold acc(row(b, room, f, inBranch), q)
	// @ assert forall k int :: { at(m, room, f, inMain, k) } { old(at(m, room, f, inMain, k)) } 0 <= k && k < room ==> at(m, room, f, inMain, k) == old(at(m, room, f, inMain, k))
	// @ assert forall k int :: { at(b, room, f, inBranch, k) } { old(at(b, room, f, inBranch, k)) } 0 <= k && k < room ==> at(b, room, f, inBranch, k) == old(at(b, room, f, inBranch, k))
	return ok
}

// takeFrom takes main in to each of the branch's files from file f on, as
// TakeMainIn does, and leaves the files before f as they are. Every call
// holds every file, so what one proves of its file comes back from the files
// past it as a fact, not as what the solver must work out was left alone. It
// reads with the share p of permission it's given, and gives each next call
// half of it.
// @ requires p > 0 && 0 <= f && f <= len(sl) && len(sl) <= MaxSize && 0 <= room && room <= MaxSize
// @ requires len(sn) == len(sl) && len(bl) == len(sl) && len(bn) == len(sl) && len(from) == len(sl) && len(to) == len(sl)
// @ requires forall g int :: { &sn[g] } 0 <= g && g < len(sn) ==> acc(&sn[g], p) && 0 <= sn[g] && sn[g] <= room
// @ requires forall g int :: { &sl[g] } 0 <= g && g < len(sl) ==> acc(&sl[g], p) && acc(row(sl[g], room, g, inMain), p)
// @ requires forall g int :: { &bn[g] } 0 <= g && g < len(bn) ==> acc(&bn[g])
// @ requires forall g int :: { &bn[g] } f <= g && g < len(bn) ==> 0 <= bn[g] && sn[g] + bn[g] <= room
// @ requires forall g int :: { &bl[g] } 0 <= g && g < len(bl) ==> acc(&bl[g], p) && row(bl[g], room, g, inBranch)
// @ requires forall g int :: { &from[g] } 0 <= g && g < len(from) ==> acc(&from[g], p) && spots(from[g], room, g, isFrom)
// @ requires forall g int :: { &to[g] } 0 <= g && g < len(to) ==> acc(&to[g], p) && spots(to[g], room, g, isTo)
// @ ensures forall g int :: { &sn[g] } 0 <= g && g < len(sn) ==> acc(&sn[g], p)
// @ ensures forall g int :: { &sl[g] } 0 <= g && g < len(sl) ==> acc(&sl[g], p) && acc(row(sl[g], room, g, inMain), p)
// @ ensures forall g int :: { &bn[g] } 0 <= g && g < len(bn) ==> acc(&bn[g])
// @ ensures forall g int :: { &bl[g] } 0 <= g && g < len(bl) ==> acc(&bl[g], p) && row(bl[g], room, g, inBranch)
// @ ensures forall g int :: { &from[g] } 0 <= g && g < len(from) ==> acc(&from[g], p) && spots(from[g], room, g, isFrom)
// @ ensures forall g int :: { &to[g] } 0 <= g && g < len(to) ==> acc(&to[g], p) && spots(to[g], room, g, isTo)
// @ ensures forall g int :: { &sn[g] } { old(sn[g]) } 0 <= g && g < len(sn) ==> sn[g] == old(sn[g])
// @ ensures forall g, k int :: { at(sl[g], room, g, inMain, k) } { old(at(sl[g], room, g, inMain, k)) } 0 <= g && g < len(sl) && 0 <= k && k < room ==> at(sl[g], room, g, inMain, k) == old(at(sl[g], room, g, inMain, k))
// @ ensures forall g int :: { &bn[g] } { old(bn[g]) } 0 <= g && g < f ==> bn[g] == old(bn[g])
// @ ensures forall g, k int :: { at(bl[g], room, g, inBranch, k) } { old(at(bl[g], room, g, inBranch, k)) } 0 <= g && g < f && 0 <= k && k < room ==> at(bl[g], room, g, inBranch, k) == old(at(bl[g], room, g, inBranch, k))
// @ ensures forall g, k int :: { spot(from[g], room, g, isFrom, k) } { old(spot(from[g], room, g, isFrom, k)) } 0 <= g && g < f && 0 <= k && k < room ==> spot(from[g], room, g, isFrom, k) == old(spot(from[g], room, g, isFrom, k))
// @ ensures forall g, k int :: { spot(to[g], room, g, isTo, k) } { old(spot(to[g], room, g, isTo, k)) } 0 <= g && g < f && 0 <= k && k < room ==> spot(to[g], room, g, isTo, k) == old(spot(to[g], room, g, isTo, k))
// @ ensures forall g int :: { &bn[g] } f <= g && g < len(bn) ==> sn[g] <= bn[g] && bn[g] <= sn[g] + old(bn[g]) && bn[g] <= room
// @ ensures forall g, k int :: { at(bl[g], room, g, inBranch, k) } f <= g && g < len(bl) && 0 <= k && k < sn[g] ==> at(bl[g], room, g, inBranch, k) == at(sl[g], room, g, inMain, k)
// @ ensures forall g, k int :: { at(bl[g], room, g, inBranch, k) } { spot(from[g], room, g, isFrom, k) } f <= g && g < len(bl) && sn[g] <= k && k < bn[g] ==> let j := spot(from[g], room, g, isFrom, k).At in 0 <= j && j < old(bn[g]) && at(bl[g], room, g, inBranch, k) == old(at(bl[g], room, g, inBranch, j))
// @ ensures forall g, k, i int :: { at(bl[g], room, g, inBranch, k), at(sl[g], room, g, inMain, i) } f <= g && g < len(bl) && sn[g] <= k && k < bn[g] && 0 <= i && i < sn[g] ==> at(bl[g], room, g, inBranch, k) != at(sl[g], room, g, inMain, i)
// @ ensures forall g, q int :: { spot(to[g], room, g, isTo, q) } f <= g && g < len(bl) && 0 <= q && q < old(bn[g]) && !(exists i int :: { at(sl[g], room, g, inMain, i) } 0 <= i && i < sn[g] && at(sl[g], room, g, inMain, i) == old(at(bl[g], room, g, inBranch, q))) ==> sn[g] <= spot(to[g], room, g, isTo, q).At && spot(to[g], room, g, isTo, q).At < bn[g] && at(bl[g], room, g, inBranch, spot(to[g], room, g, isTo, q).At) == old(at(bl[g], room, g, inBranch, q))
// @ decreases len(sl) - f
func takeFrom(sl [][]Line, sn []int, bl [][]Line, bn []int, from, to [][]Spot, f, room int /*@, ghost p perm @*/) {
	if f == len(sl) {
		return
	}
	// File f as takeOne finds it.
	// @ assert forall g int :: { &bn[g] } { old(bn[g]) } 0 <= g && g < len(bn) ==> bn[g] == old(bn[g])
	// @ assert forall g, k int :: { at(bl[g], room, g, inBranch, k) } { old(at(bl[g], room, g, inBranch, k)) } g == f && 0 <= k && k < room ==> at(bl[g], room, g, inBranch, k) == old(at(bl[g], room, g, inBranch, k))
	bn[f] = takeOne(sl[f], sn[f], bl[f], bn[f], from[f], to[f], f, room /*@, p/2 @*/)
	// The other files as they were.
	// @ assert forall g, k int :: { at(sl[g], room, g, inMain, k) } { old(at(sl[g], room, g, inMain, k)) } 0 <= g && g < len(sl) && 0 <= k && k < room ==> at(sl[g], room, g, inMain, k) == old(at(sl[g], room, g, inMain, k))
	// @ assert forall g, k int :: { at(bl[g], room, g, inBranch, k) } { old(at(bl[g], room, g, inBranch, k)) } 0 <= g && g < len(bl) && g != f && 0 <= k && k < room ==> at(bl[g], room, g, inBranch, k) == old(at(bl[g], room, g, inBranch, k))
	// @ assert forall g, k int :: { spot(from[g], room, g, isFrom, k) } { old(spot(from[g], room, g, isFrom, k)) } 0 <= g && g < len(from) && g != f && 0 <= k && k < room ==> spot(from[g], room, g, isFrom, k) == old(spot(from[g], room, g, isFrom, k))
	// @ assert forall g, k int :: { spot(to[g], room, g, isTo, k) } { old(spot(to[g], room, g, isTo, k)) } 0 <= g && g < len(to) && g != f && 0 <= k && k < room ==> spot(to[g], room, g, isTo, k) == old(spot(to[g], room, g, isTo, k))
	// @ assert forall g int :: { &bn[g] } { old(bn[g]) } 0 <= g && g < len(bn) && g != f ==> bn[g] == old(bn[g])
	// What takeOne proved of file f, said of whichever file is f, so the
	// solver matches it on any file's lines and needn't work out which.
	// @ assert forall g int :: { &bn[g] } g == f ==> sn[g] <= bn[g] && bn[g] <= sn[g] + old(bn[g]) && bn[g] <= room
	// @ assert forall g, k int :: { at(bl[g], room, g, inBranch, k) } { at(sl[g], room, g, inMain, k) } g == f && 0 <= k && k < sn[g] ==> at(bl[g], room, g, inBranch, k) == at(sl[g], room, g, inMain, k)
	// @ assert forall g, k int :: { at(bl[g], room, g, inBranch, k) } { spot(from[g], room, g, isFrom, k) } g == f && sn[g] <= k && k < bn[g] ==> let j := spot(from[g], room, g, isFrom, k).At in 0 <= j && j < old(bn[g]) && at(bl[g], room, g, inBranch, k) == old(at(bl[g], room, g, inBranch, j))
	// @ assert forall g, k, i int :: { at(bl[g], room, g, inBranch, k), at(sl[g], room, g, inMain, i) } g == f && sn[g] <= k && k < bn[g] && 0 <= i && i < sn[g] ==> at(bl[g], room, g, inBranch, k) != at(sl[g], room, g, inMain, i)
	// @ assert forall g, q int :: { spot(to[g], room, g, isTo, q) } g == f && 0 <= q && q < old(bn[g]) && !(exists i int :: { at(sl[g], room, g, inMain, i) } 0 <= i && i < sn[g] && at(sl[g], room, g, inMain, i) == old(at(bl[g], room, g, inBranch, q))) ==> sn[g] <= spot(to[g], room, g, isTo, q).At && spot(to[g], room, g, isTo, q).At < bn[g] && at(bl[g], room, g, inBranch, spot(to[g], room, g, isTo, q).At) == old(at(bl[g], room, g, inBranch, q))
	takeFrom(sl, sn, bl, bn, from, to, f+1, room /*@, p/2 @*/)
}

// takeOne takes main's file m in to the branch's file b, which is file f of
// each, as TakeMainIn does, rebuilding b from a copy of itself. It reads m
// with the share p of permission it's given.
// @ requires p > 0 && 0 <= sn && 0 <= bn && sn + bn <= room && room <= MaxSize
// @ requires acc(row(m, room, f, inMain), p) && row(b, room, f, inBranch) && spots(from, room, f, isFrom) && spots(to, room, f, isTo)
// @ ensures acc(row(m, room, f, inMain), p) && row(b, room, f, inBranch) && spots(from, room, f, isFrom) && spots(to, room, f, isTo)
// @ ensures forall k int :: { at(m, room, f, inMain, k) } 0 <= k && k < room ==> at(m, room, f, inMain, k) == old(at(m, room, f, inMain, k))
// @ ensures sn <= n && n <= sn + bn && n <= room
// @ ensures forall k int :: { at(b, room, f, inBranch, k) } 0 <= k && k < sn ==> at(b, room, f, inBranch, k) == at(m, room, f, inMain, k)
// @ ensures forall k int :: { at(b, room, f, inBranch, k) } { spot(from, room, f, isFrom, k) } sn <= k && k < n ==> let j := spot(from, room, f, isFrom, k).At in 0 <= j && j < bn && at(b, room, f, inBranch, k) == old(at(b, room, f, inBranch, j))
// @ ensures forall k, i int :: { at(b, room, f, inBranch, k), at(m, room, f, inMain, i) } sn <= k && k < n && 0 <= i && i < sn ==> at(b, room, f, inBranch, k) != at(m, room, f, inMain, i)
// @ ensures forall q int :: { spot(to, room, f, isTo, q) } 0 <= q && q < bn && !(exists i int :: { at(m, room, f, inMain, i) } 0 <= i && i < sn && at(m, room, f, inMain, i) == old(at(b, room, f, inBranch, q))) ==> sn <= spot(to, room, f, isTo, q).At && spot(to, room, f, isTo, q).At < n && at(b, room, f, inBranch, spot(to, room, f, isTo, q).At) == old(at(b, room, f, inBranch, q))
// @ decreases
func takeOne(m []Line, sn int, b []Line, bn int, from, to []Spot, f, room int /*@, ghost p perm @*/) (n int) {
	// Copies of main's file and the branch's, as they are.
	ours := make([]Line, room)
	// @ unfold acc(row(m, room, f, inMain), p)
	// @ assert forall k int :: { &m[k] } { old(at(m, room, f, inMain, k)) } 0 <= k && k < room ==> m[k] == old(at(m, room, f, inMain, k))
	copyLines(ours, m /*@, p @*/)
	// @ assert forall k int :: { &ours[k] } { &m[k] } 0 <= k && k < room ==> ours[k] == m[k]
	// @ fold acc(row(m, room, f, inMain), p)
	was := make([]Line, room)
	// @ unfold row(b, room, f, inBranch)
	// @ assert forall k int :: { &b[k] } { old(at(b, room, f, inBranch, k)) } 0 <= k && k < room ==> b[k] == old(at(b, room, f, inBranch, k))
	copyLines(was, b /*@, 1/2 @*/)
	// @ unfold spots(from, room, f, isFrom)
	// @ unfold spots(to, room, f, isTo)
	n = takeIn(b, ours, sn, was, bn, from, to)
	// @ fold spots(to, room, f, isTo)
	// @ fold spots(from, room, f, isFrom)
	// @ fold row(b, room, f, inBranch)
	// @ assert forall k int :: { at(m, room, f, inMain, k) } { old(at(m, room, f, inMain, k)) } 0 <= k && k < room ==> at(m, room, f, inMain, k) == old(at(m, room, f, inMain, k))
	return n
}

// spotRows allocates a table of n files' room positions each, behind spots.
// @ requires 0 <= n && n <= MaxSize && 0 <= room && room <= MaxSize
// @ ensures len(r) == n
// @ ensures forall f int :: { &r[f] } 0 <= f && f < n ==> acc(&r[f]) && spots(r[f], room, f, to)
func spotRows(n, room int /*@, ghost to bool @*/) (r [][]Spot) {
	r = make([][]Spot, n)
	i := 0
	// @ invariant 0 <= i && i <= n && len(r) == n
	// @ invariant forall f int :: { &r[f] } 0 <= f && f < n ==> acc(&r[f])
	// @ invariant forall f int :: { &r[f] } 0 <= f && f < i ==> spots(r[f], room, f, to)
	// @ decreases n - i
	for i < n {
		r[i] = make([]Spot, room)
		// @ fold spots(r[i], room, i, to)
		i++
	}
	return r
}
