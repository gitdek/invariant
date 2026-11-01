// +gobra

package merges

// Taking main in reads and writes every file, so each file is held behind a
// predicate: a step on one file unfolds only that file, and what the proof
// knows of the others carries over untouched.

// row is permission to file f's room lines. Naming the file keeps every
// file's instance distinct.
// @ pred row(r []Line, room int, f int) {
// @ 	len(r) == room && forall k int :: { &r[k] } 0 <= k && k < room ==> acc(&r[k])
// @ }

// at is line k of file f behind row.
// @ ghost
// @ requires acc(row(r, room, f), _) && 0 <= k && k < room
// @ decreases
// @ pure func at(r []Line, room int, f int, k int) Line {
// @ 	return unfolding acc(row(r, room, f), _) in r[k]
// @ }

// spots is permission to room positions in file f.
// @ pred spots(r []Spot, room int, f int) {
// @ 	len(r) == room && forall k int :: { &r[k] } 0 <= k && k < room ==> acc(&r[k])
// @ }

// spot is position k of file f behind spots.
// @ ghost
// @ requires acc(spots(r, room, f), _) && 0 <= k && k < room
// @ decreases
// @ pure func spot(r []Spot, room int, f int, k int) Spot {
// @ 	return unfolding acc(spots(r, room, f), _) in r[k]
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
// @ requires forall f int :: { &s.Lines[f] } 0 <= f && f < len(s.Lines) ==> acc(&s.Lines[f], 1/2) && acc(row(s.Lines[f], s.Room, f), 1/2)
// @ requires acc(&b.Lines, 1/2) && acc(&b.Len, 1/2) && len(b.Lines) == len(s.Lines) && len(b.Len) == len(s.Lines)
// @ requires forall f int :: { &b.Len[f] } 0 <= f && f < len(b.Len) ==> acc(&b.Len[f]) && 0 <= b.Len[f] && b.Len[f] <= s.Room
// @ requires forall f int :: { &b.Lines[f] } 0 <= f && f < len(b.Lines) ==> acc(&b.Lines[f], 1/2) && row(b.Lines[f], s.Room, f)
// @ ensures acc(&s.Lines, 1/2) && acc(&s.Len, 1/2) && acc(&s.Room, 1/2) && s.Room == old(s.Room)
// @ ensures len(s.Len) == old(len(s.Len)) && len(s.Lines) == old(len(s.Lines))
// @ ensures forall f int :: { &s.Len[f] } 0 <= f && f < len(s.Len) ==> acc(&s.Len[f], 1/2)
// @ ensures forall f int :: { &s.Lines[f] } 0 <= f && f < len(s.Lines) ==> acc(&s.Lines[f], 1/2) && acc(row(s.Lines[f], s.Room, f), 1/2)
// @ ensures acc(&b.Lines, 1/2) && acc(&b.Len, 1/2) && len(b.Lines) == old(len(b.Lines)) && len(b.Len) == old(len(b.Len))
// @ ensures forall f int :: { &b.Len[f] } 0 <= f && f < len(b.Len) ==> acc(&b.Len[f]) && 0 <= b.Len[f] && b.Len[f] <= s.Room
// @ ensures forall f int :: { &b.Lines[f] } 0 <= f && f < len(b.Lines) ==> acc(&b.Lines[f], 1/2) && row(b.Lines[f], s.Room, f)
// @ ensures forall f int :: { &s.Len[f] } { old(s.Len[f]) } 0 <= f && f < len(s.Len) ==> s.Len[f] == old(s.Len[f])
// @ ensures forall f, k int :: { at(s.Lines[f], s.Room, f, k) } 0 <= f && f < len(s.Lines) && 0 <= k && k < s.Room ==> at(s.Lines[f], s.Room, f, k) == old(at(s.Lines[f], s.Room, f, k))
// @ ensures ran == (!(forall f int :: { old(s.Len[f]) } 0 <= f && f < len(s.Len) ==> old(s.Len[f]) <= old(b.Len[f]) && (forall k int :: { old(at(s.Lines[f], s.Room, f, k)) } 0 <= k && k < old(s.Len[f]) ==> old(at(s.Lines[f], s.Room, f, k)) == old(at(b.Lines[f], s.Room, f, k)))) && (forall f int :: { old(s.Len[f]) } 0 <= f && f < len(s.Len) ==> old(s.Len[f]) + old(b.Len[f]) <= s.Room))
// @ ensures !ran ==> forall f int :: { &b.Len[f] } { old(b.Len[f]) } 0 <= f && f < len(b.Len) ==> b.Len[f] == old(b.Len[f])
// @ ensures !ran ==> forall f, k int :: { at(b.Lines[f], s.Room, f, k) } 0 <= f && f < len(b.Lines) && 0 <= k && k < s.Room ==> at(b.Lines[f], s.Room, f, k) == old(at(b.Lines[f], s.Room, f, k))
// @ ensures ran ==> len(from) == len(s.Lines) && len(to) == len(s.Lines)
// @ ensures ran ==> forall f int :: { &from[f] } 0 <= f && f < len(from) ==> acc(&from[f], 1/2) && spots(from[f], s.Room, f)
// @ ensures ran ==> forall f int :: { &to[f] } 0 <= f && f < len(to) ==> acc(&to[f], 1/2) && spots(to[f], s.Room, f)
// @ ensures ran ==> forall f int :: { &b.Len[f] } 0 <= f && f < len(b.Len) ==> s.Len[f] <= b.Len[f] && b.Len[f] <= s.Len[f] + old(b.Len[f])
// @ ensures ran ==> forall f, k int :: { at(b.Lines[f], s.Room, f, k) } 0 <= f && f < len(b.Lines) && 0 <= k && k < s.Len[f] ==> at(b.Lines[f], s.Room, f, k) == at(s.Lines[f], s.Room, f, k)
// @ ensures ran ==> forall f, k int :: { spot(from[f], s.Room, f, k) } 0 <= f && f < len(b.Lines) && s.Len[f] <= k && k < b.Len[f] ==> 0 <= spot(from[f], s.Room, f, k).At && spot(from[f], s.Room, f, k).At < old(b.Len[f])
// @ ensures ran ==> forall f, k, j int :: { at(b.Lines[f], s.Room, f, k), old(at(b.Lines[f], s.Room, f, j)) } 0 <= f && f < len(b.Lines) && s.Len[f] <= k && k < b.Len[f] && j == spot(from[f], s.Room, f, k).At && 0 <= j && j < old(b.Len[f]) ==> at(b.Lines[f], s.Room, f, k) == old(at(b.Lines[f], s.Room, f, j))
// @ ensures ran ==> forall f, k, i int :: { at(b.Lines[f], s.Room, f, k), at(s.Lines[f], s.Room, f, i) } 0 <= f && f < len(b.Lines) && s.Len[f] <= k && k < b.Len[f] && 0 <= i && i < s.Len[f] ==> at(b.Lines[f], s.Room, f, k) != at(s.Lines[f], s.Room, f, i)
// @ ensures ran ==> forall f, q int :: { spot(to[f], s.Room, f, q) } 0 <= f && f < len(b.Lines) && 0 <= q && q < old(b.Len[f]) && !(exists i int :: { at(s.Lines[f], s.Room, f, i) } 0 <= i && i < s.Len[f] && at(s.Lines[f], s.Room, f, i) == old(at(b.Lines[f], s.Room, f, q))) ==> s.Len[f] <= spot(to[f], s.Room, f, q).At && spot(to[f], s.Room, f, q).At < b.Len[f] && at(b.Lines[f], s.Room, f, spot(to[f], s.Room, f, q).At) == old(at(b.Lines[f], s.Room, f, q))
func (s *Core) TakeMainIn(b *Branch) (ran bool, from, to [][]Spot) {
	room := s.Room
	if startsFrom(s.Lines, s.Len, b.Lines, b.Len, 0, room /*@, 1/4 @*/) || !s.fits(b) {
		return false, nil, nil
	}
	from = spotRows(len(s.Lines), room)
	to = spotRows(len(s.Lines), room)
	// @ assert forall f, k int :: { at(s.Lines[f], room, f, k) } { old(at(s.Lines[f], s.Room, f, k)) } 0 <= f && f < len(s.Lines) && 0 <= k && k < room ==> at(s.Lines[f], room, f, k) == old(at(s.Lines[f], s.Room, f, k))
	// @ assert forall f, k int :: { at(b.Lines[f], room, f, k) } { old(at(b.Lines[f], s.Room, f, k)) } 0 <= f && f < len(b.Lines) && 0 <= k && k < room ==> at(b.Lines[f], room, f, k) == old(at(b.Lines[f], s.Room, f, k))
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
// @ requires forall g int :: { &sl[g] } 0 <= g && g < len(sl) ==> acc(&sl[g], q) && acc(row(sl[g], room, g), q)
// @ requires forall g int :: { &bl[g] } 0 <= g && g < len(bl) ==> acc(&bl[g], q) && acc(row(bl[g], room, g), q)
// @ ensures forall g int :: { &sn[g] } 0 <= g && g < len(sn) ==> acc(&sn[g], q)
// @ ensures forall g int :: { &bn[g] } 0 <= g && g < len(bn) ==> acc(&bn[g], q)
// @ ensures forall g int :: { &sl[g] } 0 <= g && g < len(sl) ==> acc(&sl[g], q) && acc(row(sl[g], room, g), q)
// @ ensures forall g int :: { &bl[g] } 0 <= g && g < len(bl) ==> acc(&bl[g], q) && acc(row(bl[g], room, g), q)
// @ ensures forall g int :: { &sn[g] } { old(sn[g]) } 0 <= g && g < len(sn) ==> sn[g] == old(sn[g])
// @ ensures forall g int :: { &bn[g] } { old(bn[g]) } 0 <= g && g < len(bn) ==> bn[g] == old(bn[g])
// @ ensures forall g, k int :: { at(sl[g], room, g, k) } 0 <= g && g < len(sl) && 0 <= k && k < room ==> at(sl[g], room, g, k) == old(at(sl[g], room, g, k))
// @ ensures forall g, k int :: { at(bl[g], room, g, k) } 0 <= g && g < len(bl) && 0 <= k && k < room ==> at(bl[g], room, g, k) == old(at(bl[g], room, g, k))
// @ ensures ok == (forall g int :: { old(sn[g]) } f <= g && g < len(sl) ==> old(sn[g]) <= old(bn[g]) && (forall k int :: { old(at(sl[g], room, g, k)) } 0 <= k && k < old(sn[g]) ==> old(at(sl[g], room, g, k)) == old(at(bl[g], room, g, k))))
// @ decreases len(sl) - f
func startsFrom(sl [][]Line, sn []int, bl [][]Line, bn []int, f, room int /*@, ghost q perm @*/) (ok bool) {
	if f == len(sl) {
		return true
	}
	// @ unfold acc(row(sl[f], room, f), q/2)
	// @ unfold acc(row(bl[f], room, f), q/2)
	// @ assert forall k int :: { &sl[f][k] } { old(at(sl[f], room, f, k)) } 0 <= k && k < room ==> sl[f][k] == old(at(sl[f], room, f, k))
	// @ assert forall k int :: { &bl[f][k] } { old(at(bl[f], room, f, k)) } 0 <= k && k < room ==> bl[f][k] == old(at(bl[f], room, f, k))
	ok = startsWith(sl[f], sn[f], bl[f], bn[f] /*@, q/2 @*/)
	// @ fold acc(row(sl[f], room, f), q/2)
	// @ fold acc(row(bl[f], room, f), q/2)
	if !ok {
		return false
	}
	// @ assert forall g, k int :: { at(sl[g], room, g, k) } { old(at(sl[g], room, g, k)) } 0 <= g && g < len(sl) && 0 <= k && k < room ==> at(sl[g], room, g, k) == old(at(sl[g], room, g, k))
	// @ assert forall g, k int :: { at(bl[g], room, g, k) } { old(at(bl[g], room, g, k)) } 0 <= g && g < len(bl) && 0 <= k && k < room ==> at(bl[g], room, g, k) == old(at(bl[g], room, g, k))
	return startsFrom(sl, sn, bl, bn, f+1, room /*@, q/2 @*/)
}

// takeFrom takes main in to each of the branch's files from file f on, as
// TakeMainIn does. Each call keeps its own file, so what it proves of that
// file holds after the files past it are taken. It reads main with the share
// p of its permission it's given, and gives each next call half of it.
// @ requires p > 0 && 0 <= f && f <= len(sl) && len(sl) <= MaxSize && 0 <= room && room <= MaxSize
// @ requires len(sn) == len(sl) && len(bl) == len(sl) && len(bn) == len(sl) && len(from) == len(sl) && len(to) == len(sl)
// @ requires forall g int :: { &sn[g] } 0 <= g && g < len(sn) ==> acc(&sn[g], p) && 0 <= sn[g] && sn[g] <= room
// @ requires forall g int :: { &sl[g] } 0 <= g && g < len(sl) ==> acc(&sl[g], p) && acc(row(sl[g], room, g), p)
// @ requires forall g int :: { &bn[g] } f <= g && g < len(bn) ==> acc(&bn[g]) && 0 <= bn[g] && sn[g] + bn[g] <= room
// @ requires forall g int :: { &bl[g] } f <= g && g < len(bl) ==> acc(&bl[g], 1/2) && row(bl[g], room, g)
// @ requires forall g int :: { &from[g] } f <= g && g < len(from) ==> acc(&from[g], 1/2) && spots(from[g], room, g)
// @ requires forall g int :: { &to[g] } f <= g && g < len(to) ==> acc(&to[g], 1/2) && spots(to[g], room, g)
// @ ensures forall g int :: { &sn[g] } 0 <= g && g < len(sn) ==> acc(&sn[g], p)
// @ ensures forall g int :: { &sl[g] } 0 <= g && g < len(sl) ==> acc(&sl[g], p) && acc(row(sl[g], room, g), p)
// @ ensures forall g int :: { &bn[g] } f <= g && g < len(bn) ==> acc(&bn[g])
// @ ensures forall g int :: { &bl[g] } f <= g && g < len(bl) ==> acc(&bl[g], 1/2) && row(bl[g], room, g)
// @ ensures forall g int :: { &from[g] } f <= g && g < len(from) ==> acc(&from[g], 1/2) && spots(from[g], room, g)
// @ ensures forall g int :: { &to[g] } f <= g && g < len(to) ==> acc(&to[g], 1/2) && spots(to[g], room, g)
// @ ensures forall g int :: { &sn[g] } { old(sn[g]) } 0 <= g && g < len(sn) ==> sn[g] == old(sn[g])
// @ ensures forall g, k int :: { at(sl[g], room, g, k) } 0 <= g && g < len(sl) && 0 <= k && k < room ==> at(sl[g], room, g, k) == old(at(sl[g], room, g, k))
// @ ensures forall g int :: { &bn[g] } f <= g && g < len(bn) ==> sn[g] <= bn[g] && bn[g] <= sn[g] + old(bn[g]) && bn[g] <= room
// @ ensures forall g, k int :: { at(bl[g], room, g, k) } f <= g && g < len(bl) && 0 <= k && k < sn[g] ==> at(bl[g], room, g, k) == at(sl[g], room, g, k)
// @ ensures forall g, k int :: { spot(from[g], room, g, k) } f <= g && g < len(bl) && sn[g] <= k && k < bn[g] ==> 0 <= spot(from[g], room, g, k).At && spot(from[g], room, g, k).At < old(bn[g])
// @ ensures forall g, k, j int :: { at(bl[g], room, g, k), old(at(bl[g], room, g, j)) } f <= g && g < len(bl) && sn[g] <= k && k < bn[g] && j == spot(from[g], room, g, k).At && 0 <= j && j < old(bn[g]) ==> at(bl[g], room, g, k) == old(at(bl[g], room, g, j))
// @ ensures forall g, k, i int :: { at(bl[g], room, g, k), at(sl[g], room, g, i) } f <= g && g < len(bl) && sn[g] <= k && k < bn[g] && 0 <= i && i < sn[g] ==> at(bl[g], room, g, k) != at(sl[g], room, g, i)
// @ ensures forall g, q int :: { spot(to[g], room, g, q) } f <= g && g < len(bl) && 0 <= q && q < old(bn[g]) && !(exists i int :: { at(sl[g], room, g, i) } 0 <= i && i < sn[g] && at(sl[g], room, g, i) == old(at(bl[g], room, g, q))) ==> sn[g] <= spot(to[g], room, g, q).At && spot(to[g], room, g, q).At < bn[g] && at(bl[g], room, g, spot(to[g], room, g, q).At) == old(at(bl[g], room, g, q))
// @ decreases len(sl) - f
func takeFrom(sl [][]Line, sn []int, bl [][]Line, bn []int, from, to [][]Spot, f, room int /*@, ghost p perm @*/) {
	if f == len(sl) {
		return
	}
	bn[f] = takeOne(sl[f], sn[f], bl[f], bn[f], from[f], to[f], f, room /*@, p/2 @*/)
	// @ assert forall g, k int :: { at(sl[g], room, g, k) } { old(at(sl[g], room, g, k)) } 0 <= g && g < len(sl) && 0 <= k && k < room ==> at(sl[g], room, g, k) == old(at(sl[g], room, g, k))
	// @ assert forall g, k int :: { at(bl[g], room, g, k) } { old(at(bl[g], room, g, k)) } f < g && g < len(bl) && 0 <= k && k < room ==> at(bl[g], room, g, k) == old(at(bl[g], room, g, k))
	takeFrom(sl, sn, bl, bn, from, to, f+1, room /*@, p/2 @*/)
	// @ assert forall g, k int :: { at(sl[g], room, g, k) } { old(at(sl[g], room, g, k)) } 0 <= g && g < len(sl) && 0 <= k && k < room ==> at(sl[g], room, g, k) == old(at(sl[g], room, g, k))
}

// takeOne takes main's file m in to the branch's file b, which is file f of
// each, as TakeMainIn does, rebuilding b from a copy of itself. It reads m
// with the share p of permission it's given.
// @ requires p > 0 && 0 <= sn && 0 <= bn && sn + bn <= room && room <= MaxSize
// @ requires acc(row(m, room, f), p) && row(b, room, f) && spots(from, room, f) && spots(to, room, f)
// @ ensures acc(row(m, room, f), p) && row(b, room, f) && spots(from, room, f) && spots(to, room, f)
// @ ensures forall k int :: { at(m, room, f, k) } 0 <= k && k < room ==> at(m, room, f, k) == old(at(m, room, f, k))
// @ ensures sn <= n && n <= sn + bn && n <= room
// @ ensures forall k int :: { at(b, room, f, k) } 0 <= k && k < sn ==> at(b, room, f, k) == at(m, room, f, k)
// @ ensures forall k int :: { spot(from, room, f, k) } sn <= k && k < n ==> 0 <= spot(from, room, f, k).At && spot(from, room, f, k).At < bn
// @ ensures forall k, j int :: { at(b, room, f, k), old(at(b, room, f, j)) } sn <= k && k < n && j == spot(from, room, f, k).At && 0 <= j && j < bn ==> at(b, room, f, k) == old(at(b, room, f, j))
// @ ensures forall k, i int :: { at(b, room, f, k), at(m, room, f, i) } sn <= k && k < n && 0 <= i && i < sn ==> at(b, room, f, k) != at(m, room, f, i)
// @ ensures forall q int :: { spot(to, room, f, q) } 0 <= q && q < bn && !(exists i int :: { at(m, room, f, i) } 0 <= i && i < sn && at(m, room, f, i) == old(at(b, room, f, q))) ==> sn <= spot(to, room, f, q).At && spot(to, room, f, q).At < n && at(b, room, f, spot(to, room, f, q).At) == old(at(b, room, f, q))
// @ decreases
func takeOne(m []Line, sn int, b []Line, bn int, from, to []Spot, f, room int /*@, ghost p perm @*/) (n int) {
	// Copies of main's file and the branch's, as they are.
	ours := make([]Line, room)
	// @ unfold acc(row(m, room, f), p)
	// @ assert forall k int :: { &m[k] } { old(at(m, room, f, k)) } 0 <= k && k < room ==> m[k] == old(at(m, room, f, k))
	copyLines(ours, m /*@, p @*/)
	// @ fold acc(row(m, room, f), p)
	was := make([]Line, room)
	// @ unfold row(b, room, f)
	// @ assert forall k int :: { &b[k] } { old(at(b, room, f, k)) } 0 <= k && k < room ==> b[k] == old(at(b, room, f, k))
	copyLines(was, b /*@, 1/2 @*/)
	// @ unfold spots(from, room, f)
	// @ unfold spots(to, room, f)
	n = takeIn(b, ours, sn, was, bn, from, to)
	// @ fold spots(to, room, f)
	// @ fold spots(from, room, f)
	// @ fold row(b, room, f)
	return n
}

// spotRows allocates n files' room positions each, behind spots.
// @ requires 0 <= n && n <= MaxSize && 0 <= room && room <= MaxSize
// @ ensures len(r) == n
// @ ensures forall f int :: { &r[f] } 0 <= f && f < n ==> acc(&r[f]) && spots(r[f], room, f)
func spotRows(n, room int) (r [][]Spot) {
	r = make([][]Spot, n)
	i := 0
	// @ invariant 0 <= i && i <= n && len(r) == n
	// @ invariant forall f int :: { &r[f] } 0 <= f && f < n ==> acc(&r[f])
	// @ invariant forall f int :: { &r[f] } 0 <= f && f < i ==> spots(r[f], room, f)
	// @ decreases n - i
	for i < n {
		r[i] = make([]Spot, room)
		// @ fold spots(r[i], room, i)
		i++
	}
	return r
}
