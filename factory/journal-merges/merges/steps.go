// +gobra

package merges

// Add adds the branch's next line to its file f: line Added+1, as long as
// the branch has added fewer than Capacity lines and the file has room.
// Otherwise it changes nothing.
// @ requires acc(&s.Room, 1/2) && acc(&s.Capacity, 1/2) && 0 <= s.Room && s.Room <= MaxSize && 0 <= s.Capacity && s.Capacity <= MaxSize
// @ requires acc(&b.Lines, 1/2) && acc(&b.Len, 1/2) && acc(&b.By, 1/2) && acc(&b.Added)
// @ requires len(b.Len) == len(b.Lines) && 0 <= b.Added && b.Added <= s.Capacity && 0 <= f && f < len(b.Lines)
// @ requires forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g]) && 0 <= b.Len[g] && b.Len[g] <= s.Room
// @ requires forall g int :: { &b.Lines[g] } 0 <= g && g < len(b.Lines) ==> acc(&b.Lines[g], 1/2) && len(b.Lines[g]) == s.Room
// @ requires forall g, k int :: { &b.Lines[g][k] } 0 <= g && g < len(b.Lines) && 0 <= k && k < s.Room ==> acc(&b.Lines[g][k])
// @ ensures acc(&s.Room, 1/2) && acc(&s.Capacity, 1/2) && s.Room == old(s.Room) && s.Capacity == old(s.Capacity)
// @ ensures acc(&b.Lines, 1/2) && acc(&b.Len, 1/2) && acc(&b.By, 1/2) && acc(&b.Added)
// @ ensures len(b.Len) == old(len(b.Len)) && len(b.Lines) == old(len(b.Lines)) && b.By == old(b.By)
// @ ensures 0 <= b.Added && b.Added <= s.Capacity
// @ ensures forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g]) && 0 <= b.Len[g] && b.Len[g] <= s.Room
// @ ensures forall g int :: { &b.Lines[g] } 0 <= g && g < len(b.Lines) ==> acc(&b.Lines[g], 1/2) && len(b.Lines[g]) == s.Room
// @ ensures forall g, k int :: { &b.Lines[g][k] } 0 <= g && g < len(b.Lines) && 0 <= k && k < s.Room ==> acc(&b.Lines[g][k])
// @ ensures ran == (old(b.Added) < s.Capacity && old(b.Len[f]) < s.Room)
// @ ensures b.Added == (ran ? old(b.Added) + 1 : old(b.Added))
// @ ensures forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> b.Len[g] == ((ran && g == f) ? old(b.Len[g]) + 1 : old(b.Len[g]))
// @ ensures forall g, k int :: { &b.Lines[g][k] } 0 <= g && g < len(b.Lines) && 0 <= k && k < s.Room && (!ran || g != f || k != old(b.Len[f])) ==> b.Lines[g][k] == old(b.Lines[g][k])
// @ ensures ran ==> b.Lines[f][old(b.Len[f])] == (Line{b.By, b.Added})
func (s *Core) Add(b *Branch, f int) (ran bool) {
	if b.Added >= s.Capacity || b.Len[f] >= s.Room {
		return false
	}
	b.Lines[f][b.Len[f]] = Line{b.By, b.Added + 1}
	b.Len[f]++
	b.Added++
	return true
}

// Merge merges the branch into main, as long as each of main's files is the
// start of the branch's and one of the branch's has lines past main's: main
// then holds the branch's journal. Otherwise it changes nothing.
// @ requires acc(&s.Lines, 1/2) && acc(&s.Len, 1/2) && acc(&s.Room, 1/2) && 0 <= s.Room && s.Room <= MaxSize
// @ requires len(s.Len) == len(s.Lines) && len(s.Lines) <= MaxSize
// @ requires forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> acc(&s.Len[g], 1) && 0 <= s.Len[g] && s.Len[g] <= s.Room
// @ requires forall g int :: { &s.Lines[g] } 0 <= g && g < len(s.Lines) ==> acc(&s.Lines[g], 1/2) && len(s.Lines[g]) == s.Room
// @ requires forall g, k int :: { &s.Lines[g][k] } 0 <= g && g < len(s.Lines) && 0 <= k && k < s.Room ==> acc(&s.Lines[g][k], 1)
// @ requires acc(&b.Lines, 1/2) && acc(&b.Len, 1/2) && len(b.Lines) == len(s.Lines) && len(b.Len) == len(s.Lines)
// @ requires forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g], 1/2) && 0 <= b.Len[g] && b.Len[g] <= s.Room
// @ requires forall g int :: { &b.Lines[g] } 0 <= g && g < len(b.Lines) ==> acc(&b.Lines[g], 1/2) && len(b.Lines[g]) == s.Room
// @ requires forall g, k int :: { &b.Lines[g][k] } 0 <= g && g < len(b.Lines) && 0 <= k && k < s.Room ==> acc(&b.Lines[g][k], 1/2)
// @ ensures acc(&s.Lines, 1/2) && acc(&s.Len, 1/2) && acc(&s.Room, 1/2) && 0 <= s.Room && s.Room <= MaxSize
// @ ensures len(s.Len) == len(s.Lines) && len(s.Lines) <= MaxSize
// @ ensures s.Room == old(s.Room) && len(s.Lines) == old(len(s.Lines)) && len(s.Len) == old(len(s.Len))
// @ ensures forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> acc(&s.Len[g], 1) && 0 <= s.Len[g] && s.Len[g] <= s.Room
// @ ensures forall g int :: { &s.Lines[g] } 0 <= g && g < len(s.Lines) ==> acc(&s.Lines[g], 1/2) && len(s.Lines[g]) == s.Room
// @ ensures forall g, k int :: { &s.Lines[g][k] } 0 <= g && g < len(s.Lines) && 0 <= k && k < s.Room ==> acc(&s.Lines[g][k], 1)
// @ ensures acc(&b.Lines, 1/2) && acc(&b.Len, 1/2) && len(b.Lines) == len(s.Lines) && len(b.Len) == len(s.Lines)
// @ ensures len(b.Lines) == old(len(b.Lines)) && len(b.Len) == old(len(b.Len))
// @ ensures forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g], 1/2) && 0 <= b.Len[g] && b.Len[g] <= s.Room
// @ ensures forall g int :: { &b.Lines[g] } 0 <= g && g < len(b.Lines) ==> acc(&b.Lines[g], 1/2) && len(b.Lines[g]) == s.Room
// @ ensures forall g, k int :: { &b.Lines[g][k] } 0 <= g && g < len(b.Lines) && 0 <= k && k < s.Room ==> acc(&b.Lines[g][k], 1/2)
// @ ensures forall f int :: { &b.Len[f] } 0 <= f && f < len(b.Len) ==> b.Len[f] == old(b.Len[f])
// @ ensures forall f, k int :: { &b.Lines[f][k] } 0 <= f && f < len(b.Lines) && 0 <= k && k < s.Room ==> b.Lines[f][k] == old(b.Lines[f][k])
// @ ensures ran == ((forall f int :: { old(s.Len[f]) } 0 <= f && f < len(s.Len) ==> old(s.Len[f]) <= old(b.Len[f]) && (forall k int :: { old(s.Lines[f][k]) } 0 <= k && k < old(s.Len[f]) ==> old(s.Lines[f][k]) == old(b.Lines[f][k]))) && (exists f int :: { old(s.Len[f]) } 0 <= f && f < len(s.Len) && old(s.Len[f]) < old(b.Len[f])))
// @ ensures forall f int :: { &s.Len[f] } 0 <= f && f < len(s.Len) ==> s.Len[f] == (ran ? old(b.Len[f]) : old(s.Len[f]))
// @ ensures forall f, k int :: { &s.Lines[f][k] } 0 <= f && f < len(s.Lines) && 0 <= k && k < s.Room ==> s.Lines[f][k] == (ran ? old(b.Lines[f][k]) : old(s.Lines[f][k]))
func (s *Core) Merge(b *Branch) (ran bool) {
	if !s.startsEvery(b) || !s.longerOne(b) {
		return false
	}
	s.copyFrom(b)
	return true
}

// TakeMainIn takes main in to the branch, as long as one of main's files
// isn't the start of the branch's and each of the branch's files has room
// for main's lines beside its own. Each of the branch's files is then main's
// lines, where they are, followed by the branch's own lines that main's file
// doesn't have, in order. Otherwise it changes nothing.
// @ requires acc(&s.Lines, 1/2) && acc(&s.Len, 1/2) && acc(&s.Room, 1/2) && 0 <= s.Room && s.Room <= MaxSize
// @ requires len(s.Len) == len(s.Lines) && len(s.Lines) <= MaxSize
// @ requires forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> acc(&s.Len[g], 1/2) && 0 <= s.Len[g] && s.Len[g] <= s.Room
// @ requires forall g int :: { &s.Lines[g] } 0 <= g && g < len(s.Lines) ==> acc(&s.Lines[g], 1/2) && len(s.Lines[g]) == s.Room
// @ requires forall g, k int :: { &s.Lines[g][k] } 0 <= g && g < len(s.Lines) && 0 <= k && k < s.Room ==> acc(&s.Lines[g][k], 1/2)
// @ requires acc(&b.Lines, 1/2) && acc(&b.Len, 1/2) && len(b.Lines) == len(s.Lines) && len(b.Len) == len(s.Lines)
// @ requires forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g], 1) && 0 <= b.Len[g] && b.Len[g] <= s.Room
// @ requires forall g int :: { &b.Lines[g] } 0 <= g && g < len(b.Lines) ==> acc(&b.Lines[g], 1/2) && len(b.Lines[g]) == s.Room
// @ requires forall g, k int :: { &b.Lines[g][k] } 0 <= g && g < len(b.Lines) && 0 <= k && k < s.Room ==> acc(&b.Lines[g][k], 1)
// @ ensures acc(&s.Lines, 1/2) && acc(&s.Len, 1/2) && acc(&s.Room, 1/2) && 0 <= s.Room && s.Room <= MaxSize
// @ ensures len(s.Len) == len(s.Lines) && len(s.Lines) <= MaxSize
// @ ensures s.Room == old(s.Room) && len(s.Lines) == old(len(s.Lines)) && len(s.Len) == old(len(s.Len))
// @ ensures forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> acc(&s.Len[g], 1/2) && 0 <= s.Len[g] && s.Len[g] <= s.Room
// @ ensures forall g int :: { &s.Lines[g] } 0 <= g && g < len(s.Lines) ==> acc(&s.Lines[g], 1/2) && len(s.Lines[g]) == s.Room
// @ ensures forall g, k int :: { &s.Lines[g][k] } 0 <= g && g < len(s.Lines) && 0 <= k && k < s.Room ==> acc(&s.Lines[g][k], 1/2)
// @ ensures acc(&b.Lines, 1/2) && acc(&b.Len, 1/2) && len(b.Lines) == len(s.Lines) && len(b.Len) == len(s.Lines)
// @ ensures len(b.Lines) == old(len(b.Lines)) && len(b.Len) == old(len(b.Len))
// @ ensures forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g], 1) && 0 <= b.Len[g] && b.Len[g] <= s.Room
// @ ensures forall g int :: { &b.Lines[g] } 0 <= g && g < len(b.Lines) ==> acc(&b.Lines[g], 1/2) && len(b.Lines[g]) == s.Room
// @ ensures forall g, k int :: { &b.Lines[g][k] } 0 <= g && g < len(b.Lines) && 0 <= k && k < s.Room ==> acc(&b.Lines[g][k], 1)
// @ ensures forall f int :: { &s.Len[f] } 0 <= f && f < len(s.Len) ==> s.Len[f] == old(s.Len[f])
// @ ensures forall f, k int :: { &s.Lines[f][k] } 0 <= f && f < len(s.Lines) && 0 <= k && k < s.Room ==> s.Lines[f][k] == old(s.Lines[f][k])
// @ ensures ran == (!(forall f int :: { old(s.Len[f]) } 0 <= f && f < len(s.Len) ==> old(s.Len[f]) <= old(b.Len[f]) && (forall k int :: { old(s.Lines[f][k]) } 0 <= k && k < old(s.Len[f]) ==> old(s.Lines[f][k]) == old(b.Lines[f][k]))) && (forall f int :: { old(s.Len[f]) } 0 <= f && f < len(s.Len) ==> old(s.Len[f]) + old(b.Len[f]) <= s.Room))
// @ ensures !ran ==> forall f int :: { &b.Len[f] } 0 <= f && f < len(b.Len) ==> b.Len[f] == old(b.Len[f])
// @ ensures !ran ==> forall f, k int :: { &b.Lines[f][k] } 0 <= f && f < len(b.Lines) && 0 <= k && k < s.Room ==> b.Lines[f][k] == old(b.Lines[f][k])
// @ ensures ran ==> forall f int :: { &b.Len[f] } 0 <= f && f < len(b.Len) ==> s.Len[f] <= b.Len[f] && b.Len[f] <= s.Len[f] + old(b.Len[f])
// @ ensures ran ==> forall f, k int :: { &b.Lines[f][k] } 0 <= f && f < len(b.Lines) && 0 <= k && k < s.Len[f] ==> b.Lines[f][k] == s.Lines[f][k]
// @ ensures ran ==> forall f, k int, l Line :: 0 <= f && f < len(b.Lines) && s.Len[f] <= k && k < b.Len[f] && b.Lines[f][k] == l ==> (exists j int :: { old(b.Lines[f][j]) } 0 <= j && j < old(b.Len[f]) && old(b.Lines[f][j]) == l)
// @ ensures ran ==> forall f, k, i int :: { &b.Lines[f][k], &s.Lines[f][i] } 0 <= f && f < len(b.Lines) && s.Len[f] <= k && k < b.Len[f] && 0 <= i && i < s.Len[f] ==> b.Lines[f][k] != s.Lines[f][i]
// @ ensures ran ==> forall f, j int, l Line :: 0 <= f && f < len(b.Lines) && 0 <= j && j < old(b.Len[f]) && old(b.Lines[f][j]) == l && (forall i int :: { &s.Lines[f][i] } 0 <= i && i < s.Len[f] ==> s.Lines[f][i] != l) ==> (exists k int :: { &b.Lines[f][k] } s.Len[f] <= k && k < b.Len[f] && b.Lines[f][k] == l)
func (s *Core) TakeMainIn(b *Branch) (ran bool) {
	if s.startsEvery(b) || !s.fits(b) {
		return false
	}
	out, n := s.takeEvery(b)
	s.replace(b, out, n)
	return true
}

// startsEvery says whether each of main's files is the start of the
// branch's.
// @ requires acc(&s.Lines, 1/4) && acc(&s.Len, 1/4) && acc(&s.Room, 1/4) && 0 <= s.Room && s.Room <= MaxSize
// @ requires len(s.Len) == len(s.Lines) && len(s.Lines) <= MaxSize
// @ requires forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> acc(&s.Len[g], 1/4) && 0 <= s.Len[g] && s.Len[g] <= s.Room
// @ requires forall g int :: { &s.Lines[g] } 0 <= g && g < len(s.Lines) ==> acc(&s.Lines[g], 1/4) && len(s.Lines[g]) == s.Room
// @ requires forall g, k int :: { &s.Lines[g][k] } 0 <= g && g < len(s.Lines) && 0 <= k && k < s.Room ==> acc(&s.Lines[g][k], 1/4)
// @ requires acc(&b.Lines, 1/4) && acc(&b.Len, 1/4) && len(b.Lines) == len(s.Lines) && len(b.Len) == len(s.Lines)
// @ requires forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g], 1/4) && 0 <= b.Len[g] && b.Len[g] <= s.Room
// @ requires forall g int :: { &b.Lines[g] } 0 <= g && g < len(b.Lines) ==> acc(&b.Lines[g], 1/4) && len(b.Lines[g]) == s.Room
// @ requires forall g, k int :: { &b.Lines[g][k] } 0 <= g && g < len(b.Lines) && 0 <= k && k < s.Room ==> acc(&b.Lines[g][k], 1/4)
// @ ensures acc(&s.Lines, 1/4) && acc(&s.Len, 1/4) && acc(&s.Room, 1/4) && 0 <= s.Room && s.Room <= MaxSize
// @ ensures len(s.Len) == len(s.Lines) && len(s.Lines) <= MaxSize
// @ ensures s.Room == old(s.Room) && len(s.Lines) == old(len(s.Lines)) && len(s.Len) == old(len(s.Len))
// @ ensures forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> acc(&s.Len[g], 1/4) && 0 <= s.Len[g] && s.Len[g] <= s.Room
// @ ensures forall g int :: { &s.Lines[g] } 0 <= g && g < len(s.Lines) ==> acc(&s.Lines[g], 1/4) && len(s.Lines[g]) == s.Room
// @ ensures forall g, k int :: { &s.Lines[g][k] } 0 <= g && g < len(s.Lines) && 0 <= k && k < s.Room ==> acc(&s.Lines[g][k], 1/4)
// @ ensures acc(&b.Lines, 1/4) && acc(&b.Len, 1/4) && len(b.Lines) == len(s.Lines) && len(b.Len) == len(s.Lines)
// @ ensures len(b.Lines) == old(len(b.Lines)) && len(b.Len) == old(len(b.Len))
// @ ensures forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g], 1/4) && 0 <= b.Len[g] && b.Len[g] <= s.Room
// @ ensures forall g int :: { &b.Lines[g] } 0 <= g && g < len(b.Lines) ==> acc(&b.Lines[g], 1/4) && len(b.Lines[g]) == s.Room
// @ ensures forall g, k int :: { &b.Lines[g][k] } 0 <= g && g < len(b.Lines) && 0 <= k && k < s.Room ==> acc(&b.Lines[g][k], 1/4)
// @ ensures forall f int :: { &s.Len[f] } 0 <= f && f < len(s.Len) ==> s.Len[f] == old(s.Len[f])
// @ ensures forall f, k int :: { &s.Lines[f][k] } 0 <= f && f < len(s.Lines) && 0 <= k && k < s.Room ==> s.Lines[f][k] == old(s.Lines[f][k])
// @ ensures forall f int :: { &b.Len[f] } 0 <= f && f < len(b.Len) ==> b.Len[f] == old(b.Len[f])
// @ ensures forall f, k int :: { &b.Lines[f][k] } 0 <= f && f < len(b.Lines) && 0 <= k && k < s.Room ==> b.Lines[f][k] == old(b.Lines[f][k])
// @ ensures ok == (forall f int :: { old(s.Len[f]) } 0 <= f && f < len(s.Len) ==> old(s.Len[f]) <= old(b.Len[f]) && (forall k int :: { old(s.Lines[f][k]) } 0 <= k && k < old(s.Len[f]) ==> old(s.Lines[f][k]) == old(b.Lines[f][k])))
func (s *Core) startsEvery(b *Branch) (ok bool) {
	f := 0
	// @ invariant acc(&s.Lines, 1/4) && acc(&s.Len, 1/4) && acc(&s.Room, 1/4) && 0 <= s.Room && s.Room <= MaxSize
	// @ invariant len(s.Len) == len(s.Lines) && len(s.Lines) <= MaxSize
	// @ invariant s.Room == old(s.Room) && len(s.Lines) == old(len(s.Lines)) && len(s.Len) == old(len(s.Len))
	// @ invariant forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> acc(&s.Len[g], 1/4) && 0 <= s.Len[g] && s.Len[g] <= s.Room
	// @ invariant forall g int :: { &s.Lines[g] } 0 <= g && g < len(s.Lines) ==> acc(&s.Lines[g], 1/4) && len(s.Lines[g]) == s.Room
	// @ invariant forall g, k int :: { &s.Lines[g][k] } 0 <= g && g < len(s.Lines) && 0 <= k && k < s.Room ==> acc(&s.Lines[g][k], 1/4)
	// @ invariant acc(&b.Lines, 1/4) && acc(&b.Len, 1/4) && len(b.Lines) == len(s.Lines) && len(b.Len) == len(s.Lines)
	// @ invariant len(b.Lines) == old(len(b.Lines)) && len(b.Len) == old(len(b.Len))
	// @ invariant forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g], 1/4) && 0 <= b.Len[g] && b.Len[g] <= s.Room
	// @ invariant forall g int :: { &b.Lines[g] } 0 <= g && g < len(b.Lines) ==> acc(&b.Lines[g], 1/4) && len(b.Lines[g]) == s.Room
	// @ invariant forall g, k int :: { &b.Lines[g][k] } 0 <= g && g < len(b.Lines) && 0 <= k && k < s.Room ==> acc(&b.Lines[g][k], 1/4)
	// @ invariant 0 <= f && f <= len(s.Lines)
	// @ invariant forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> s.Len[g] == old(s.Len[g])
	// @ invariant forall g, k int :: { &s.Lines[g][k] } 0 <= g && g < len(s.Lines) && 0 <= k && k < s.Room ==> s.Lines[g][k] == old(s.Lines[g][k])
	// @ invariant forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> b.Len[g] == old(b.Len[g])
	// @ invariant forall g, k int :: { &b.Lines[g][k] } 0 <= g && g < len(b.Lines) && 0 <= k && k < s.Room ==> b.Lines[g][k] == old(b.Lines[g][k])
	// @ invariant forall g int :: { old(s.Len[g]) } 0 <= g && g < f ==> old(s.Len[g]) <= old(b.Len[g]) && (forall k int :: { old(s.Lines[g][k]) } 0 <= k && k < old(s.Len[g]) ==> old(s.Lines[g][k]) == old(b.Lines[g][k]))
	// @ decreases len(s.Lines) - f
	for f < len(s.Lines) {
		if !startsWith(s.Lines[f], s.Len[f], b.Lines[f], b.Len[f]) {
			return false
		}
		f++
	}
	return true
}

// longerOne says whether one of the branch's files has lines past main's.
// @ requires acc(&s.Len, 1/4) && acc(&s.Room, 1/4) && acc(&b.Len, 1/4) && 0 <= s.Room && s.Room <= MaxSize
// @ requires len(b.Len) == len(s.Len) && len(s.Len) <= MaxSize
// @ requires forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> acc(&s.Len[g], 1/4) && 0 <= s.Len[g] && s.Len[g] <= s.Room
// @ requires forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g], 1/4) && 0 <= b.Len[g] && b.Len[g] <= s.Room
// @ ensures acc(&s.Len, 1/4) && acc(&s.Room, 1/4) && acc(&b.Len, 1/4) && 0 <= s.Room && s.Room <= MaxSize
// @ ensures len(b.Len) == len(s.Len) && len(s.Len) <= MaxSize
// @ ensures s.Room == old(s.Room) && len(s.Len) == old(len(s.Len)) && len(b.Len) == old(len(b.Len))
// @ ensures forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> acc(&s.Len[g], 1/4) && 0 <= s.Len[g] && s.Len[g] <= s.Room
// @ ensures forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g], 1/4) && 0 <= b.Len[g] && b.Len[g] <= s.Room
// @ ensures forall f int :: { &s.Len[f] } 0 <= f && f < len(s.Len) ==> s.Len[f] == old(s.Len[f])
// @ ensures forall f int :: { &b.Len[f] } 0 <= f && f < len(b.Len) ==> b.Len[f] == old(b.Len[f])
// @ ensures ok == (exists f int :: { old(s.Len[f]) } 0 <= f && f < len(s.Len) && old(s.Len[f]) < old(b.Len[f]))
func (s *Core) longerOne(b *Branch) (ok bool) {
	f := 0
	// @ invariant acc(&s.Len, 1/4) && acc(&s.Room, 1/4) && acc(&b.Len, 1/4) && 0 <= s.Room && s.Room <= MaxSize
	// @ invariant len(b.Len) == len(s.Len) && len(s.Len) <= MaxSize
	// @ invariant s.Room == old(s.Room) && len(s.Len) == old(len(s.Len)) && len(b.Len) == old(len(b.Len))
	// @ invariant forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> acc(&s.Len[g], 1/4) && 0 <= s.Len[g] && s.Len[g] <= s.Room
	// @ invariant forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g], 1/4) && 0 <= b.Len[g] && b.Len[g] <= s.Room
	// @ invariant 0 <= f && f <= len(s.Len)
	// @ invariant forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> s.Len[g] == old(s.Len[g])
	// @ invariant forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> b.Len[g] == old(b.Len[g])
	// @ invariant forall g int :: { old(s.Len[g]) } 0 <= g && g < f ==> old(b.Len[g]) <= old(s.Len[g])
	// @ decreases len(s.Len) - f
	for f < len(s.Len) {
		if s.Len[f] < b.Len[f] {
			return true
		}
		f++
	}
	return false
}

// fits says whether each of the branch's files has room for main's lines
// beside its own.
// @ requires acc(&s.Len, 1/4) && acc(&s.Room, 1/4) && acc(&b.Len, 1/4) && 0 <= s.Room && s.Room <= MaxSize
// @ requires len(b.Len) == len(s.Len) && len(s.Len) <= MaxSize
// @ requires forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> acc(&s.Len[g], 1/4) && 0 <= s.Len[g] && s.Len[g] <= s.Room
// @ requires forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g], 1/4) && 0 <= b.Len[g] && b.Len[g] <= s.Room
// @ ensures acc(&s.Len, 1/4) && acc(&s.Room, 1/4) && acc(&b.Len, 1/4) && 0 <= s.Room && s.Room <= MaxSize
// @ ensures len(b.Len) == len(s.Len) && len(s.Len) <= MaxSize
// @ ensures s.Room == old(s.Room) && len(s.Len) == old(len(s.Len)) && len(b.Len) == old(len(b.Len))
// @ ensures forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> acc(&s.Len[g], 1/4) && 0 <= s.Len[g] && s.Len[g] <= s.Room
// @ ensures forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g], 1/4) && 0 <= b.Len[g] && b.Len[g] <= s.Room
// @ ensures forall f int :: { &s.Len[f] } 0 <= f && f < len(s.Len) ==> s.Len[f] == old(s.Len[f])
// @ ensures forall f int :: { &b.Len[f] } 0 <= f && f < len(b.Len) ==> b.Len[f] == old(b.Len[f])
// @ ensures ok == (forall f int :: { old(s.Len[f]) } 0 <= f && f < len(s.Len) ==> old(s.Len[f]) + old(b.Len[f]) <= s.Room)
func (s *Core) fits(b *Branch) (ok bool) {
	f := 0
	// @ invariant acc(&s.Len, 1/4) && acc(&s.Room, 1/4) && acc(&b.Len, 1/4) && 0 <= s.Room && s.Room <= MaxSize
	// @ invariant len(b.Len) == len(s.Len) && len(s.Len) <= MaxSize
	// @ invariant s.Room == old(s.Room) && len(s.Len) == old(len(s.Len)) && len(b.Len) == old(len(b.Len))
	// @ invariant forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> acc(&s.Len[g], 1/4) && 0 <= s.Len[g] && s.Len[g] <= s.Room
	// @ invariant forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g], 1/4) && 0 <= b.Len[g] && b.Len[g] <= s.Room
	// @ invariant 0 <= f && f <= len(s.Len)
	// @ invariant forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> s.Len[g] == old(s.Len[g])
	// @ invariant forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> b.Len[g] == old(b.Len[g])
	// @ invariant forall g int :: { old(s.Len[g]) } 0 <= g && g < f ==> old(s.Len[g]) + old(b.Len[g]) <= s.Room
	// @ decreases len(s.Len) - f
	for f < len(s.Len) {
		if s.Len[f]+b.Len[f] > s.Room {
			return false
		}
		f++
	}
	return true
}

// copyFrom makes main's journal the branch's.
// @ requires acc(&s.Lines, 1/4) && acc(&s.Len, 1/4) && acc(&s.Room, 1/4) && 0 <= s.Room && s.Room <= MaxSize
// @ requires len(s.Len) == len(s.Lines) && len(s.Lines) <= MaxSize
// @ requires forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> acc(&s.Len[g], 1) && 0 <= s.Len[g] && s.Len[g] <= s.Room
// @ requires forall g int :: { &s.Lines[g] } 0 <= g && g < len(s.Lines) ==> acc(&s.Lines[g], 1/4) && len(s.Lines[g]) == s.Room
// @ requires forall g, k int :: { &s.Lines[g][k] } 0 <= g && g < len(s.Lines) && 0 <= k && k < s.Room ==> acc(&s.Lines[g][k], 1)
// @ requires acc(&b.Lines, 1/4) && acc(&b.Len, 1/4) && len(b.Lines) == len(s.Lines) && len(b.Len) == len(s.Lines)
// @ requires forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g], 1/4) && 0 <= b.Len[g] && b.Len[g] <= s.Room
// @ requires forall g int :: { &b.Lines[g] } 0 <= g && g < len(b.Lines) ==> acc(&b.Lines[g], 1/4) && len(b.Lines[g]) == s.Room
// @ requires forall g, k int :: { &b.Lines[g][k] } 0 <= g && g < len(b.Lines) && 0 <= k && k < s.Room ==> acc(&b.Lines[g][k], 1/4)
// @ ensures acc(&s.Lines, 1/4) && acc(&s.Len, 1/4) && acc(&s.Room, 1/4) && 0 <= s.Room && s.Room <= MaxSize
// @ ensures len(s.Len) == len(s.Lines) && len(s.Lines) <= MaxSize
// @ ensures s.Room == old(s.Room) && len(s.Lines) == old(len(s.Lines)) && len(s.Len) == old(len(s.Len))
// @ ensures forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> acc(&s.Len[g], 1) && 0 <= s.Len[g] && s.Len[g] <= s.Room
// @ ensures forall g int :: { &s.Lines[g] } 0 <= g && g < len(s.Lines) ==> acc(&s.Lines[g], 1/4) && len(s.Lines[g]) == s.Room
// @ ensures forall g, k int :: { &s.Lines[g][k] } 0 <= g && g < len(s.Lines) && 0 <= k && k < s.Room ==> acc(&s.Lines[g][k], 1)
// @ ensures acc(&b.Lines, 1/4) && acc(&b.Len, 1/4) && len(b.Lines) == len(s.Lines) && len(b.Len) == len(s.Lines)
// @ ensures len(b.Lines) == old(len(b.Lines)) && len(b.Len) == old(len(b.Len))
// @ ensures forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g], 1/4) && 0 <= b.Len[g] && b.Len[g] <= s.Room
// @ ensures forall g int :: { &b.Lines[g] } 0 <= g && g < len(b.Lines) ==> acc(&b.Lines[g], 1/4) && len(b.Lines[g]) == s.Room
// @ ensures forall g, k int :: { &b.Lines[g][k] } 0 <= g && g < len(b.Lines) && 0 <= k && k < s.Room ==> acc(&b.Lines[g][k], 1/4)
// @ ensures forall f int :: { &b.Len[f] } 0 <= f && f < len(b.Len) ==> b.Len[f] == old(b.Len[f])
// @ ensures forall f, k int :: { &b.Lines[f][k] } 0 <= f && f < len(b.Lines) && 0 <= k && k < s.Room ==> b.Lines[f][k] == old(b.Lines[f][k])
// @ ensures forall f int :: { &s.Len[f] } 0 <= f && f < len(s.Len) ==> s.Len[f] == old(b.Len[f])
// @ ensures forall f, k int :: { &s.Lines[f][k] } 0 <= f && f < len(s.Lines) && 0 <= k && k < s.Room ==> s.Lines[f][k] == old(b.Lines[f][k])
func (s *Core) copyFrom(b *Branch) {
	f := 0
	// @ invariant acc(&s.Lines, 1/4) && acc(&s.Len, 1/4) && acc(&s.Room, 1/4) && 0 <= s.Room && s.Room <= MaxSize
	// @ invariant len(s.Len) == len(s.Lines) && len(s.Lines) <= MaxSize
	// @ invariant s.Room == old(s.Room) && len(s.Lines) == old(len(s.Lines)) && len(s.Len) == old(len(s.Len))
	// @ invariant forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> acc(&s.Len[g], 1) && 0 <= s.Len[g] && s.Len[g] <= s.Room
	// @ invariant forall g int :: { &s.Lines[g] } 0 <= g && g < len(s.Lines) ==> acc(&s.Lines[g], 1/4) && len(s.Lines[g]) == s.Room
	// @ invariant forall g, k int :: { &s.Lines[g][k] } 0 <= g && g < len(s.Lines) && 0 <= k && k < s.Room ==> acc(&s.Lines[g][k], 1)
	// @ invariant acc(&b.Lines, 1/4) && acc(&b.Len, 1/4) && len(b.Lines) == len(s.Lines) && len(b.Len) == len(s.Lines)
	// @ invariant len(b.Lines) == old(len(b.Lines)) && len(b.Len) == old(len(b.Len))
	// @ invariant forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g], 1/4) && 0 <= b.Len[g] && b.Len[g] <= s.Room
	// @ invariant forall g int :: { &b.Lines[g] } 0 <= g && g < len(b.Lines) ==> acc(&b.Lines[g], 1/4) && len(b.Lines[g]) == s.Room
	// @ invariant forall g, k int :: { &b.Lines[g][k] } 0 <= g && g < len(b.Lines) && 0 <= k && k < s.Room ==> acc(&b.Lines[g][k], 1/4)
	// @ invariant 0 <= f && f <= len(s.Lines)
	// @ invariant forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> b.Len[g] == old(b.Len[g])
	// @ invariant forall g, k int :: { &b.Lines[g][k] } 0 <= g && g < len(b.Lines) && 0 <= k && k < s.Room ==> b.Lines[g][k] == old(b.Lines[g][k])
	// @ invariant forall g int :: { &s.Len[g] } 0 <= g && g < f ==> s.Len[g] == old(b.Len[g])
	// @ invariant forall g, k int :: { &s.Lines[g][k] } 0 <= g && g < f && 0 <= k && k < s.Room ==> s.Lines[g][k] == old(b.Lines[g][k])
	// @ decreases len(s.Lines) - f
	for f < len(s.Lines) {
		copyLines(s.Lines[f], b.Lines[f])
		s.Len[f] = b.Len[f]
		f++
	}
}

// takeEvery works out, for each file, what taking main in leaves in the
// branch's: main's lines, where they are, then the branch's own lines that
// main's file doesn't have, in order. It changes neither journal.
// @ requires acc(&s.Lines, 1/4) && acc(&s.Len, 1/4) && acc(&s.Room, 1/4) && 0 <= s.Room && s.Room <= MaxSize
// @ requires len(s.Len) == len(s.Lines) && len(s.Lines) <= MaxSize
// @ requires forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> acc(&s.Len[g], 1/4) && 0 <= s.Len[g] && s.Len[g] <= s.Room
// @ requires forall g int :: { &s.Lines[g] } 0 <= g && g < len(s.Lines) ==> acc(&s.Lines[g], 1/4) && len(s.Lines[g]) == s.Room
// @ requires forall g, k int :: { &s.Lines[g][k] } 0 <= g && g < len(s.Lines) && 0 <= k && k < s.Room ==> acc(&s.Lines[g][k], 1/4)
// @ requires acc(&b.Lines, 1/4) && acc(&b.Len, 1/4) && len(b.Lines) == len(s.Lines) && len(b.Len) == len(s.Lines)
// @ requires forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g], 1/4) && 0 <= b.Len[g] && b.Len[g] <= s.Room
// @ requires forall g int :: { &b.Lines[g] } 0 <= g && g < len(b.Lines) ==> acc(&b.Lines[g], 1/4) && len(b.Lines[g]) == s.Room
// @ requires forall g, k int :: { &b.Lines[g][k] } 0 <= g && g < len(b.Lines) && 0 <= k && k < s.Room ==> acc(&b.Lines[g][k], 1/4)
// @ requires forall f int :: { &s.Len[f] } 0 <= f && f < len(s.Len) ==> s.Len[f] + b.Len[f] <= s.Room
// @ ensures acc(&s.Lines, 1/4) && acc(&s.Len, 1/4) && acc(&s.Room, 1/4) && 0 <= s.Room && s.Room <= MaxSize
// @ ensures len(s.Len) == len(s.Lines) && len(s.Lines) <= MaxSize
// @ ensures s.Room == old(s.Room) && len(s.Lines) == old(len(s.Lines)) && len(s.Len) == old(len(s.Len))
// @ ensures forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> acc(&s.Len[g], 1/4) && 0 <= s.Len[g] && s.Len[g] <= s.Room
// @ ensures forall g int :: { &s.Lines[g] } 0 <= g && g < len(s.Lines) ==> acc(&s.Lines[g], 1/4) && len(s.Lines[g]) == s.Room
// @ ensures forall g, k int :: { &s.Lines[g][k] } 0 <= g && g < len(s.Lines) && 0 <= k && k < s.Room ==> acc(&s.Lines[g][k], 1/4)
// @ ensures acc(&b.Lines, 1/4) && acc(&b.Len, 1/4) && len(b.Lines) == len(s.Lines) && len(b.Len) == len(s.Lines)
// @ ensures len(b.Lines) == old(len(b.Lines)) && len(b.Len) == old(len(b.Len))
// @ ensures forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g], 1/4) && 0 <= b.Len[g] && b.Len[g] <= s.Room
// @ ensures forall g int :: { &b.Lines[g] } 0 <= g && g < len(b.Lines) ==> acc(&b.Lines[g], 1/4) && len(b.Lines[g]) == s.Room
// @ ensures forall g, k int :: { &b.Lines[g][k] } 0 <= g && g < len(b.Lines) && 0 <= k && k < s.Room ==> acc(&b.Lines[g][k], 1/4)
// @ ensures forall f int :: { &s.Len[f] } 0 <= f && f < len(s.Len) ==> s.Len[f] == old(s.Len[f])
// @ ensures forall f, k int :: { &s.Lines[f][k] } 0 <= f && f < len(s.Lines) && 0 <= k && k < s.Room ==> s.Lines[f][k] == old(s.Lines[f][k])
// @ ensures forall f int :: { &b.Len[f] } 0 <= f && f < len(b.Len) ==> b.Len[f] == old(b.Len[f])
// @ ensures forall f, k int :: { &b.Lines[f][k] } 0 <= f && f < len(b.Lines) && 0 <= k && k < s.Room ==> b.Lines[f][k] == old(b.Lines[f][k])
// @ ensures len(out) == len(s.Lines) && len(n) == len(s.Lines)
// @ ensures forall f int :: { &n[f] } 0 <= f && f < len(n) ==> acc(&n[f])
// @ ensures forall f int :: { &out[f] } 0 <= f && f < len(out) ==> acc(&out[f]) && len(out[f]) == s.Room
// @ ensures forall f, k int :: { &out[f][k] } 0 <= f && f < len(out) && 0 <= k && k < s.Room ==> acc(&out[f][k])
// @ ensures forall f int :: { &n[f] } 0 <= f && f < len(n) ==> old(s.Len[f]) <= n[f] && n[f] <= old(s.Len[f]) + old(b.Len[f])
// @ ensures forall f, k int :: { &out[f][k] } 0 <= f && f < len(out) && 0 <= k && k < old(s.Len[f]) ==> out[f][k] == old(s.Lines[f][k])
// @ ensures forall f, k int, l Line :: 0 <= f && f < len(out) && old(s.Len[f]) <= k && k < n[f] && out[f][k] == l ==> (exists j int :: { old(b.Lines[f][j]) } 0 <= j && j < old(b.Len[f]) && old(b.Lines[f][j]) == l)
// @ ensures forall f, k, i int :: { &out[f][k], old(s.Lines[f][i]) } 0 <= f && f < len(out) && old(s.Len[f]) <= k && k < n[f] && 0 <= i && i < old(s.Len[f]) ==> out[f][k] != old(s.Lines[f][i])
// @ ensures forall f, j int, l Line :: 0 <= f && f < len(out) && 0 <= j && j < old(b.Len[f]) && old(b.Lines[f][j]) == l && (forall i int :: { old(s.Lines[f][i]) } 0 <= i && i < old(s.Len[f]) ==> old(s.Lines[f][i]) != l) ==> (exists k int :: { &out[f][k] } old(s.Len[f]) <= k && k < n[f] && out[f][k] == l)
func (s *Core) takeEvery(b *Branch) (out [][]Line, n []int) {
	out = rows(len(s.Lines), s.Room)
	n = make([]int, len(s.Lines))
	f := 0
	// @ invariant acc(&s.Lines, 1/4) && acc(&s.Len, 1/4) && acc(&s.Room, 1/4) && 0 <= s.Room && s.Room <= MaxSize
	// @ invariant len(s.Len) == len(s.Lines) && len(s.Lines) <= MaxSize
	// @ invariant s.Room == old(s.Room) && len(s.Lines) == old(len(s.Lines)) && len(s.Len) == old(len(s.Len))
	// @ invariant forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> acc(&s.Len[g], 1/4) && 0 <= s.Len[g] && s.Len[g] <= s.Room
	// @ invariant forall g int :: { &s.Lines[g] } 0 <= g && g < len(s.Lines) ==> acc(&s.Lines[g], 1/4) && len(s.Lines[g]) == s.Room
	// @ invariant forall g, k int :: { &s.Lines[g][k] } 0 <= g && g < len(s.Lines) && 0 <= k && k < s.Room ==> acc(&s.Lines[g][k], 1/4)
	// @ invariant acc(&b.Lines, 1/4) && acc(&b.Len, 1/4) && len(b.Lines) == len(s.Lines) && len(b.Len) == len(s.Lines)
	// @ invariant len(b.Lines) == old(len(b.Lines)) && len(b.Len) == old(len(b.Len))
	// @ invariant forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g], 1/4) && 0 <= b.Len[g] && b.Len[g] <= s.Room
	// @ invariant forall g int :: { &b.Lines[g] } 0 <= g && g < len(b.Lines) ==> acc(&b.Lines[g], 1/4) && len(b.Lines[g]) == s.Room
	// @ invariant forall g, k int :: { &b.Lines[g][k] } 0 <= g && g < len(b.Lines) && 0 <= k && k < s.Room ==> acc(&b.Lines[g][k], 1/4)
	// @ invariant 0 <= f && f <= len(s.Lines) && len(out) == len(s.Lines) && len(n) == len(s.Lines)
	// @ invariant forall g int :: { &n[g] } 0 <= g && g < len(n) ==> acc(&n[g])
	// @ invariant forall g int :: { &out[g] } 0 <= g && g < len(out) ==> acc(&out[g]) && len(out[g]) == s.Room
	// @ invariant forall g, k int :: { &out[g][k] } 0 <= g && g < len(out) && 0 <= k && k < s.Room ==> acc(&out[g][k])
	// @ invariant forall g int :: { &s.Len[g] } 0 <= g && g < len(s.Len) ==> s.Len[g] == old(s.Len[g])
	// @ invariant forall g, k int :: { &s.Lines[g][k] } 0 <= g && g < len(s.Lines) && 0 <= k && k < s.Room ==> s.Lines[g][k] == old(s.Lines[g][k])
	// @ invariant forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> b.Len[g] == old(b.Len[g])
	// @ invariant forall g, k int :: { &b.Lines[g][k] } 0 <= g && g < len(b.Lines) && 0 <= k && k < s.Room ==> b.Lines[g][k] == old(b.Lines[g][k])
	// @ invariant forall g int :: { old(s.Len[g]) } 0 <= g && g < len(s.Len) ==> 0 <= old(s.Len[g]) && 0 <= old(b.Len[g]) && old(s.Len[g]) + old(b.Len[g]) <= s.Room
	// @ invariant forall g int :: { &n[g] } 0 <= g && g < f ==> old(s.Len[g]) <= n[g] && n[g] <= old(s.Len[g]) + old(b.Len[g])
	// @ invariant forall g, k int :: { &out[g][k] } 0 <= g && g < f && 0 <= k && k < old(s.Len[g]) ==> out[g][k] == old(s.Lines[g][k])
	// @ invariant forall g, k int, l Line :: 0 <= g && g < f && old(s.Len[g]) <= k && k < n[g] && out[g][k] == l ==> (exists j int :: { old(b.Lines[g][j]) } 0 <= j && j < old(b.Len[g]) && old(b.Lines[g][j]) == l)
	// @ invariant forall g, k, i int :: { &out[g][k], old(s.Lines[g][i]) } 0 <= g && g < f && old(s.Len[g]) <= k && k < n[g] && 0 <= i && i < old(s.Len[g]) ==> out[g][k] != old(s.Lines[g][i])
	// @ invariant forall g, j int, l Line :: 0 <= g && g < f && 0 <= j && j < old(b.Len[g]) && old(b.Lines[g][j]) == l && (forall i int :: { old(s.Lines[g][i]) } 0 <= i && i < old(s.Len[g]) ==> old(s.Lines[g][i]) != l) ==> (exists k int :: { &out[g][k] } old(s.Len[g]) <= k && k < n[g] && out[g][k] == l)
	// @ decreases len(s.Lines) - f
	for f < len(s.Lines) {
		n[f] = takeIn(out[f], s.Lines[f], s.Len[f], b.Lines[f], b.Len[f])
		f++
	}
	return out, n
}

// replace makes each of the branch's files what takeEvery worked out for it.
// @ requires acc(&s.Room, 1/4) && 0 <= s.Room && s.Room <= MaxSize
// @ requires acc(&b.Lines, 1/4) && acc(&b.Len, 1/4) && len(b.Len) == len(b.Lines) && len(b.Lines) <= MaxSize
// @ requires len(out) == len(b.Lines) && len(n) == len(b.Lines)
// @ requires forall f int :: { &b.Len[f] } 0 <= f && f < len(b.Len) ==> acc(&b.Len[f])
// @ requires forall f int :: { &b.Lines[f] } 0 <= f && f < len(b.Lines) ==> acc(&b.Lines[f], 1/4) && len(b.Lines[f]) == s.Room
// @ requires forall f, k int :: { &b.Lines[f][k] } 0 <= f && f < len(b.Lines) && 0 <= k && k < s.Room ==> acc(&b.Lines[f][k])
// @ requires forall f int :: { &n[f] } 0 <= f && f < len(n) ==> acc(&n[f], 1/4) && 0 <= n[f] && n[f] <= s.Room
// @ requires forall f int :: { &out[f] } 0 <= f && f < len(out) ==> acc(&out[f], 1/4) && len(out[f]) == s.Room
// @ requires forall f, k int :: { &out[f][k] } 0 <= f && f < len(out) && 0 <= k && k < s.Room ==> acc(&out[f][k], 1/4)
// @ ensures acc(&s.Room, 1/4) && acc(&b.Lines, 1/4) && acc(&b.Len, 1/4) && s.Room == old(s.Room)
// @ ensures len(b.Len) == old(len(b.Len)) && len(b.Lines) == old(len(b.Lines)) && len(out) == old(len(out)) && len(n) == old(len(n))
// @ ensures forall f int :: { &b.Len[f] } 0 <= f && f < len(b.Len) ==> acc(&b.Len[f])
// @ ensures forall f int :: { &b.Lines[f] } 0 <= f && f < len(b.Lines) ==> acc(&b.Lines[f], 1/4) && len(b.Lines[f]) == s.Room
// @ ensures forall f, k int :: { &b.Lines[f][k] } 0 <= f && f < len(b.Lines) && 0 <= k && k < s.Room ==> acc(&b.Lines[f][k])
// @ ensures forall f int :: { &n[f] } 0 <= f && f < len(n) ==> acc(&n[f], 1/4)
// @ ensures forall f int :: { &out[f] } 0 <= f && f < len(out) ==> acc(&out[f], 1/4) && len(out[f]) == s.Room
// @ ensures forall f, k int :: { &out[f][k] } 0 <= f && f < len(out) && 0 <= k && k < s.Room ==> acc(&out[f][k], 1/4)
// @ ensures forall f int :: { &n[f] } 0 <= f && f < len(n) ==> n[f] == old(n[f])
// @ ensures forall f, k int :: { &out[f][k] } 0 <= f && f < len(out) && 0 <= k && k < s.Room ==> out[f][k] == old(out[f][k])
// @ ensures forall f int :: { &b.Len[f] } 0 <= f && f < len(b.Len) ==> b.Len[f] == old(n[f])
// @ ensures forall f, k int :: { &b.Lines[f][k] } 0 <= f && f < len(b.Lines) && 0 <= k && k < s.Room ==> b.Lines[f][k] == old(out[f][k])
func (s *Core) replace(b *Branch, out [][]Line, n []int) {
	f := 0
	// @ invariant acc(&s.Room, 1/4) && acc(&b.Lines, 1/4) && acc(&b.Len, 1/4) && s.Room == old(s.Room) && 0 <= s.Room && s.Room <= MaxSize
	// @ invariant len(b.Len) == len(b.Lines) && len(out) == len(b.Lines) && len(n) == len(b.Lines)
	// @ invariant len(b.Len) == old(len(b.Len)) && len(b.Lines) == old(len(b.Lines)) && len(out) == old(len(out)) && len(n) == old(len(n))
	// @ invariant 0 <= f && f <= len(b.Lines)
	// @ invariant forall g int :: { &b.Len[g] } 0 <= g && g < len(b.Len) ==> acc(&b.Len[g])
	// @ invariant forall g int :: { &b.Lines[g] } 0 <= g && g < len(b.Lines) ==> acc(&b.Lines[g], 1/4) && len(b.Lines[g]) == s.Room
	// @ invariant forall g, k int :: { &b.Lines[g][k] } 0 <= g && g < len(b.Lines) && 0 <= k && k < s.Room ==> acc(&b.Lines[g][k])
	// @ invariant forall g int :: { &n[g] } 0 <= g && g < len(n) ==> acc(&n[g], 1/4) && 0 <= n[g] && n[g] <= s.Room
	// @ invariant forall g int :: { &out[g] } 0 <= g && g < len(out) ==> acc(&out[g], 1/4) && len(out[g]) == s.Room
	// @ invariant forall g, k int :: { &out[g][k] } 0 <= g && g < len(out) && 0 <= k && k < s.Room ==> acc(&out[g][k], 1/4)
	// @ invariant forall g int :: { &n[g] } 0 <= g && g < len(n) ==> n[g] == old(n[g])
	// @ invariant forall g, k int :: { &out[g][k] } 0 <= g && g < len(out) && 0 <= k && k < s.Room ==> out[g][k] == old(out[g][k])
	// @ invariant forall g int :: { &b.Len[g] } 0 <= g && g < f ==> b.Len[g] == old(n[g])
	// @ invariant forall g, k int :: { &b.Lines[g][k] } 0 <= g && g < f && 0 <= k && k < s.Room ==> b.Lines[g][k] == old(out[g][k])
	// @ decreases len(b.Lines) - f
	for f < len(b.Lines) {
		copyLines(b.Lines[f], out[f])
		b.Len[f] = n[f]
		f++
	}
}
