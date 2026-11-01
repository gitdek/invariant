// +gobra

package merges

// Taking main in reads and writes every file, so each file is held behind a
// predicate: a step on one file unfolds only that file, and what the proof
// knows of the others carries over untouched.

// row is permission to a journal file's room lines.
// @ pred row(r []Line, room int) {
// @ 	len(r) == room && forall k int :: { &r[k] } 0 <= k && k < room ==> acc(&r[k])
// @ }

// at is line k of a file behind row.
// @ ghost
// @ requires acc(row(r, room), _) && 0 <= k && k < room
// @ decreases
// @ pure func at(r []Line, room int, k int) Line {
// @ 	return unfolding acc(row(r, room), _) in r[k]
// @ }

// spots is permission to room positions in a file.
// @ pred spots(r []int, room int) {
// @ 	len(r) == room && forall k int :: { &r[k] } 0 <= k && k < room ==> acc(&r[k])
// @ }

// pos is position k behind spots.
// @ ghost
// @ requires acc(spots(r, room), _) && 0 <= k && k < room
// @ decreases
// @ pure func pos(r []int, room int, k int) int {
// @ 	return unfolding acc(spots(r, room), _) in r[k]
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
// @ requires forall f int :: { &s.Lines[f] } 0 <= f && f < len(s.Lines) ==> acc(&s.Lines[f], 1/2) && acc(row(s.Lines[f], s.Room), 1/2)
// @ requires acc(&b.Lines, 1/2) && acc(&b.Len, 1/2) && len(b.Lines) == len(s.Lines) && len(b.Len) == len(s.Lines)
// @ requires forall f int :: { &b.Len[f] } 0 <= f && f < len(b.Len) ==> acc(&b.Len[f]) && 0 <= b.Len[f] && b.Len[f] <= s.Room
// @ requires forall f int :: { &b.Lines[f] } 0 <= f && f < len(b.Lines) ==> acc(&b.Lines[f], 1/2) && row(b.Lines[f], s.Room)
// @ ensures acc(&s.Lines, 1/2) && acc(&s.Len, 1/2) && acc(&s.Room, 1/2) && s.Room == old(s.Room)
// @ ensures len(s.Len) == old(len(s.Len)) && len(s.Lines) == old(len(s.Lines))
// @ ensures forall f int :: { &s.Len[f] } 0 <= f && f < len(s.Len) ==> acc(&s.Len[f], 1/2)
// @ ensures forall f int :: { &s.Lines[f] } 0 <= f && f < len(s.Lines) ==> acc(&s.Lines[f], 1/2) && acc(row(s.Lines[f], s.Room), 1/2)
// @ ensures acc(&b.Lines, 1/2) && acc(&b.Len, 1/2) && len(b.Lines) == old(len(b.Lines)) && len(b.Len) == old(len(b.Len))
// @ ensures forall f int :: { &b.Len[f] } 0 <= f && f < len(b.Len) ==> acc(&b.Len[f]) && 0 <= b.Len[f] && b.Len[f] <= s.Room
// @ ensures forall f int :: { &b.Lines[f] } 0 <= f && f < len(b.Lines) ==> acc(&b.Lines[f], 1/2) && row(b.Lines[f], s.Room)
// @ ensures forall f int :: { &s.Len[f] } { old(s.Len[f]) } 0 <= f && f < len(s.Len) ==> s.Len[f] == old(s.Len[f])
// @ ensures forall f, k int :: { at(s.Lines[f], s.Room, k) } 0 <= f && f < len(s.Lines) && 0 <= k && k < s.Room ==> at(s.Lines[f], s.Room, k) == old(at(s.Lines[f], s.Room, k))
// @ ensures ran == (!(forall f int :: { old(s.Len[f]) } 0 <= f && f < len(s.Len) ==> old(s.Len[f]) <= old(b.Len[f]) && (forall k int :: { old(at(s.Lines[f], s.Room, k)) } 0 <= k && k < old(s.Len[f]) ==> old(at(s.Lines[f], s.Room, k)) == old(at(b.Lines[f], s.Room, k)))) && (forall f int :: { old(s.Len[f]) } 0 <= f && f < len(s.Len) ==> old(s.Len[f]) + old(b.Len[f]) <= s.Room))
// @ ensures !ran ==> forall f int :: { &b.Len[f] } { old(b.Len[f]) } 0 <= f && f < len(b.Len) ==> b.Len[f] == old(b.Len[f])
// @ ensures !ran ==> forall f, k int :: { at(b.Lines[f], s.Room, k) } 0 <= f && f < len(b.Lines) && 0 <= k && k < s.Room ==> at(b.Lines[f], s.Room, k) == old(at(b.Lines[f], s.Room, k))
// @ ensures ran ==> len(from) == len(s.Lines) && len(to) == len(s.Lines)
// @ ensures ran ==> forall f int :: { &from[f] } 0 <= f && f < len(from) ==> acc(&from[f], 1/2) && spots(from[f], s.Room)
// @ ensures ran ==> forall f int :: { &to[f] } 0 <= f && f < len(to) ==> acc(&to[f], 1/2) && spots(to[f], s.Room)
// @ ensures ran ==> forall f int :: { &b.Len[f] } 0 <= f && f < len(b.Len) ==> s.Len[f] <= b.Len[f] && b.Len[f] <= s.Len[f] + old(b.Len[f])
// @ ensures ran ==> forall f, k int :: { at(b.Lines[f], s.Room, k) } 0 <= f && f < len(b.Lines) && 0 <= k && k < s.Len[f] ==> at(b.Lines[f], s.Room, k) == at(s.Lines[f], s.Room, k)
// @ ensures ran ==> forall f, k int :: { pos(from[f], s.Room, k) } 0 <= f && f < len(b.Lines) && s.Len[f] <= k && k < b.Len[f] ==> 0 <= pos(from[f], s.Room, k) && pos(from[f], s.Room, k) < old(b.Len[f])
// @ ensures ran ==> forall f, k, j int :: { at(b.Lines[f], s.Room, k), old(at(b.Lines[f], s.Room, j)) } 0 <= f && f < len(b.Lines) && s.Len[f] <= k && k < b.Len[f] && j == pos(from[f], s.Room, k) ==> at(b.Lines[f], s.Room, k) == old(at(b.Lines[f], s.Room, j))
// @ ensures ran ==> forall f, k, i int :: { at(b.Lines[f], s.Room, k), at(s.Lines[f], s.Room, i) } 0 <= f && f < len(b.Lines) && s.Len[f] <= k && k < b.Len[f] && 0 <= i && i < s.Len[f] ==> at(b.Lines[f], s.Room, k) != at(s.Lines[f], s.Room, i)
// @ ensures ran ==> forall f, q int :: { pos(to[f], s.Room, q) } 0 <= f && f < len(b.Lines) && 0 <= q && q < old(b.Len[f]) && !(exists i int :: { at(s.Lines[f], s.Room, i) } 0 <= i && i < s.Len[f] && at(s.Lines[f], s.Room, i) == old(at(b.Lines[f], s.Room, q))) ==> s.Len[f] <= pos(to[f], s.Room, q) && pos(to[f], s.Room, q) < b.Len[f]
// @ ensures ran ==> forall f, q int :: { pos(to[f], s.Room, q) } 0 <= f && f < len(b.Lines) && 0 <= q && q < old(b.Len[f]) && !(exists i int :: { at(s.Lines[f], s.Room, i) } 0 <= i && i < s.Len[f] && at(s.Lines[f], s.Room, i) == old(at(b.Lines[f], s.Room, q))) ==> at(b.Lines[f], s.Room, pos(to[f], s.Room, q)) == old(at(b.Lines[f], s.Room, q))
func (s *Core) TakeMainIn(b *Branch) (ran bool, from, to [][]int) {
	room := s.Room
	if startsFrom(s.Lines, s.Len, b.Lines, b.Len, 0, room /*@, 1/4 @*/) || !s.fits(b) {
		return false, nil, nil
	}
	from = spotRows(len(s.Lines), room)
	to = spotRows(len(s.Lines), room)
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
// @ requires forall g int :: { &sl[g] } 0 <= g && g < len(sl) ==> acc(&sl[g], q) && acc(row(sl[g], room), q)
// @ requires forall g int :: { &bl[g] } 0 <= g && g < len(bl) ==> acc(&bl[g], q) && acc(row(bl[g], room), q)
// @ ensures forall g int :: { &sn[g] } 0 <= g && g < len(sn) ==> acc(&sn[g], q)
// @ ensures forall g int :: { &bn[g] } 0 <= g && g < len(bn) ==> acc(&bn[g], q)
// @ ensures forall g int :: { &sl[g] } 0 <= g && g < len(sl) ==> acc(&sl[g], q) && acc(row(sl[g], room), q)
// @ ensures forall g int :: { &bl[g] } 0 <= g && g < len(bl) ==> acc(&bl[g], q) && acc(row(bl[g], room), q)
// @ ensures forall g int :: { &sn[g] } { old(sn[g]) } 0 <= g && g < len(sn) ==> sn[g] == old(sn[g])
// @ ensures forall g int :: { &bn[g] } { old(bn[g]) } 0 <= g && g < len(bn) ==> bn[g] == old(bn[g])
// @ ensures forall g, k int :: { at(sl[g], room, k) } 0 <= g && g < len(sl) && 0 <= k && k < room ==> at(sl[g], room, k) == old(at(sl[g], room, k))
// @ ensures forall g, k int :: { at(bl[g], room, k) } 0 <= g && g < len(bl) && 0 <= k && k < room ==> at(bl[g], room, k) == old(at(bl[g], room, k))
// @ ensures ok == (forall g int :: { old(sn[g]) } f <= g && g < len(sl) ==> old(sn[g]) <= old(bn[g]) && (forall k int :: { old(at(sl[g], room, k)) } 0 <= k && k < old(sn[g]) ==> old(at(sl[g], room, k)) == old(at(bl[g], room, k))))
// @ decreases len(sl) - f
func startsFrom(sl [][]Line, sn []int, bl [][]Line, bn []int, f, room int /*@, ghost q perm @*/) (ok bool) {
	if f == len(sl) {
		return true
	}
	// @ unfold acc(row(sl[f], room), q/2)
	// @ unfold acc(row(bl[f], room), q/2)
	ok = startsWith(sl[f], sn[f], bl[f], bn[f] /*@, q/2 @*/)
	// @ fold acc(row(sl[f], room), q/2)
	// @ fold acc(row(bl[f], room), q/2)
	if !ok {
		return false
	}
	return startsFrom(sl, sn, bl, bn, f+1, room /*@, q/2 @*/)
}

// takeFrom takes main in to each of the branch's files from file f on, as
// TakeMainIn does, rebuilding each from a copy of itself. Each call keeps
// its own file, so what it proves of that file holds after the files past it
// are taken. It reads main with the share p of its permission it's given,
// and gives each next call half of it.
// @ requires p > 0 && 0 <= f && f <= len(sl) && len(sl) <= MaxSize && 0 <= room && room <= MaxSize
// @ requires len(sn) == len(sl) && len(bl) == len(sl) && len(bn) == len(sl) && len(from) == len(sl) && len(to) == len(sl)
// @ requires forall g int :: { &sn[g] } 0 <= g && g < len(sn) ==> acc(&sn[g], p) && 0 <= sn[g] && sn[g] <= room
// @ requires forall g int :: { &sl[g] } 0 <= g && g < len(sl) ==> acc(&sl[g], p) && acc(row(sl[g], room), p)
// @ requires forall g int :: { &bn[g] } f <= g && g < len(bn) ==> acc(&bn[g]) && 0 <= bn[g] && sn[g] + bn[g] <= room
// @ requires forall g int :: { &bl[g] } f <= g && g < len(bl) ==> acc(&bl[g], 1/2) && row(bl[g], room)
// @ requires forall g int :: { &from[g] } f <= g && g < len(from) ==> acc(&from[g], 1/2) && spots(from[g], room)
// @ requires forall g int :: { &to[g] } f <= g && g < len(to) ==> acc(&to[g], 1/2) && spots(to[g], room)
// @ ensures forall g int :: { &sn[g] } 0 <= g && g < len(sn) ==> acc(&sn[g], p)
// @ ensures forall g int :: { &sl[g] } 0 <= g && g < len(sl) ==> acc(&sl[g], p) && acc(row(sl[g], room), p)
// @ ensures forall g int :: { &bn[g] } f <= g && g < len(bn) ==> acc(&bn[g])
// @ ensures forall g int :: { &bl[g] } f <= g && g < len(bl) ==> acc(&bl[g], 1/2) && row(bl[g], room)
// @ ensures forall g int :: { &from[g] } f <= g && g < len(from) ==> acc(&from[g], 1/2) && spots(from[g], room)
// @ ensures forall g int :: { &to[g] } f <= g && g < len(to) ==> acc(&to[g], 1/2) && spots(to[g], room)
// @ ensures forall g int :: { &sn[g] } { old(sn[g]) } 0 <= g && g < len(sn) ==> sn[g] == old(sn[g])
// @ ensures forall g, k int :: { at(sl[g], room, k) } 0 <= g && g < len(sl) && 0 <= k && k < room ==> at(sl[g], room, k) == old(at(sl[g], room, k))
// @ ensures forall g int :: { &bn[g] } f <= g && g < len(bn) ==> sn[g] <= bn[g] && bn[g] <= sn[g] + old(bn[g]) && bn[g] <= room
// @ ensures forall g, k int :: { at(bl[g], room, k) } f <= g && g < len(bl) && 0 <= k && k < sn[g] ==> at(bl[g], room, k) == at(sl[g], room, k)
// @ ensures forall g, k int :: { pos(from[g], room, k) } f <= g && g < len(bl) && sn[g] <= k && k < bn[g] ==> 0 <= pos(from[g], room, k) && pos(from[g], room, k) < old(bn[g])
// @ ensures forall g, k, j int :: { at(bl[g], room, k), old(at(bl[g], room, j)) } f <= g && g < len(bl) && sn[g] <= k && k < bn[g] && j == pos(from[g], room, k) ==> at(bl[g], room, k) == old(at(bl[g], room, j))
// @ ensures forall g, k, i int :: { at(bl[g], room, k), at(sl[g], room, i) } f <= g && g < len(bl) && sn[g] <= k && k < bn[g] && 0 <= i && i < sn[g] ==> at(bl[g], room, k) != at(sl[g], room, i)
// @ ensures forall g, q int :: { pos(to[g], room, q) } f <= g && g < len(bl) && 0 <= q && q < old(bn[g]) && !(exists i int :: { at(sl[g], room, i) } 0 <= i && i < sn[g] && at(sl[g], room, i) == old(at(bl[g], room, q))) ==> sn[g] <= pos(to[g], room, q) && pos(to[g], room, q) < bn[g]
// @ ensures forall g, q int :: { pos(to[g], room, q) } f <= g && g < len(bl) && 0 <= q && q < old(bn[g]) && !(exists i int :: { at(sl[g], room, i) } 0 <= i && i < sn[g] && at(sl[g], room, i) == old(at(bl[g], room, q))) ==> at(bl[g], room, pos(to[g], room, q)) == old(at(bl[g], room, q))
// @ decreases len(sl) - f
func takeFrom(sl [][]Line, sn []int, bl [][]Line, bn []int, from, to [][]int, f, room int /*@, ghost p perm @*/) {
	if f == len(sl) {
		return
	}
	// Copies of main's file and the branch's, as they are.
	ours := make([]Line, room)
	// @ unfold acc(row(sl[f], room), p/2)
	copyLines(ours, sl[f] /*@, p/2 @*/)
	// @ fold acc(row(sl[f], room), p/2)
	was := make([]Line, room)
	// @ unfold row(bl[f], room)
	copyLines(was, bl[f] /*@, 1/2 @*/)
	// @ unfold spots(from[f], room)
	// @ unfold spots(to[f], room)
	bn[f] = takeIn(bl[f], ours, sn[f], was, bn[f], from[f], to[f])
	// @ fold spots(to[f], room)
	// @ fold spots(from[f], room)
	// @ fold row(bl[f], room)
	takeFrom(sl, sn, bl, bn, from, to, f+1, room /*@, p/2 @*/)
}

// spotRows allocates n files' room positions each, behind spots.
// @ requires 0 <= n && n <= MaxSize && 0 <= room && room <= MaxSize
// @ ensures len(r) == n
// @ ensures forall f int :: { &r[f] } 0 <= f && f < n ==> acc(&r[f]) && spots(r[f], room)
func spotRows(n, room int) (r [][]int) {
	r = make([][]int, n)
	i := 0
	// @ invariant 0 <= i && i <= n && len(r) == n
	// @ invariant forall f int :: { &r[f] } 0 <= f && f < n ==> acc(&r[f])
	// @ invariant forall f int :: { &r[f] } 0 <= f && f < i ==> spots(r[f], room)
	// @ decreases n - i
	for i < n {
		r[i] = make([]int, room)
		// @ fold spots(r[i], room)
		i++
	}
	return r
}
