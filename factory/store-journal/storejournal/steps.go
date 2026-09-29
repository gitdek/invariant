// +gobra

package storejournal

// Write appends the checkout's next line to its files and the store atomically.
// It refuses an abandoned checkout or one whose line capacity is exhausted.
// @ requires acc(&j.Store, 1/2) && acc(&j.Files, 1/2) && acc(&j.Abandoned, 1/2) && acc(&j.Capacity, 1/2)
// @ requires 0 <= j.Capacity && j.Capacity <= MaxSize
// @ requires len(j.Store) <= MaxSize && len(j.Files) == len(j.Store) && len(j.Abandoned) == len(j.Store)
// @ requires forall p int :: { &j.Store[p] } 0 <= p && p < len(j.Store) ==> acc(&j.Store[p])
// @ requires forall r int :: { &j.Abandoned[r] } 0 <= r && r < len(j.Store) ==> acc(&j.Abandoned[r])
// @ requires forall r int :: { &j.Files[r] } 0 <= r && r < len(j.Store) ==> acc(&j.Files[r], 1/2) && len(j.Files[r]) == len(j.Store)
// @ requires forall r, p int :: { &j.Files[r][p] } 0 <= r && r < len(j.Store) && 0 <= p && p < len(j.Store) ==> acc(&j.Files[r][p])
// @ requires forall p int :: { at(j.Store, p) } 0 <= p && p < len(j.Store) ==> 0 <= at(j.Store, p) && at(j.Store, p) <= j.Capacity
// @ requires forall r, p int :: { at(j.Files[r], p) } 0 <= r && r < len(j.Store) && 0 <= p && p < len(j.Store) ==> 0 <= at(j.Files[r], p) && at(j.Files[r], p) <= j.Capacity
// @ requires 0 <= c && c < len(j.Store)
// @ ensures acc(&j.Store, 1/2) && acc(&j.Files, 1/2) && acc(&j.Abandoned, 1/2) && acc(&j.Capacity, 1/2)
// @ ensures 0 <= j.Capacity && j.Capacity <= MaxSize
// @ ensures len(j.Store) <= MaxSize && len(j.Files) == len(j.Store) && len(j.Abandoned) == len(j.Store)
// @ ensures forall p int :: { &j.Store[p] } 0 <= p && p < len(j.Store) ==> acc(&j.Store[p])
// @ ensures forall r int :: { &j.Abandoned[r] } 0 <= r && r < len(j.Store) ==> acc(&j.Abandoned[r])
// @ ensures forall r int :: { &j.Files[r] } 0 <= r && r < len(j.Store) ==> acc(&j.Files[r], 1/2) && len(j.Files[r]) == len(j.Store)
// @ ensures forall r, p int :: { &j.Files[r][p] } 0 <= r && r < len(j.Store) && 0 <= p && p < len(j.Store) ==> acc(&j.Files[r][p])
// @ ensures forall p int :: { at(j.Store, p) } 0 <= p && p < len(j.Store) ==> 0 <= at(j.Store, p) && at(j.Store, p) <= j.Capacity
// @ ensures forall r, p int :: { at(j.Files[r], p) } 0 <= r && r < len(j.Store) && 0 <= p && p < len(j.Store) ==> 0 <= at(j.Files[r], p) && at(j.Files[r], p) <= j.Capacity
// @ ensures j.Capacity == old(j.Capacity) && len(j.Store) == old(len(j.Store))
// @ ensures ran == (!old(j.Abandoned[c]) && old(at(j.Files[c], c)) < j.Capacity)
// @ ensures ran ==> at(j.Files[c], c) == old(at(j.Files[c], c)) + 1
// @ ensures ran ==> at(j.Store, c) == larger(old(at(j.Store, c)), old(at(j.Files[c], c)) + 1)
// @ ensures forall p int :: { at(j.Store, p) } 0 <= p && p < len(j.Store) && (p != c || !ran) ==> at(j.Store, p) == old(at(j.Store, p))
// @ ensures forall r, p int :: { at(j.Files[r], p) } 0 <= r && r < len(j.Store) && 0 <= p && p < len(j.Store) && (r != c || p != c || !ran) ==> at(j.Files[r], p) == old(at(j.Files[r], p))
// @ ensures forall r int :: { j.Abandoned[r] } 0 <= r && r < len(j.Store) ==> j.Abandoned[r] == old(j.Abandoned[r])
func (j *Journal) Write(c int) (ran bool) {
	if j.Abandoned[c] {
		return false
	}
	current := j.Files[c][c]
	// @ assert current == at(j.Files[c], c)
	// @ assert 0 <= current && current <= j.Capacity
	if current >= j.Capacity {
		return false
	}
	n := current + 1
	j.Files[c][c] = n
	if j.Store[c] < n {
		j.Store[c] = n
	}
	return true
}

// TakeIn brings the source checkout's complete lines into the destination.
// Self-merges, abandoned checkouts, and merges adding no lines are refused.
// @ requires acc(&j.Store, 1/2) && acc(&j.Files, 1/2) && acc(&j.Abandoned, 1/2) && acc(&j.Capacity, 1/2)
// @ requires 0 <= j.Capacity && j.Capacity <= MaxSize
// @ requires len(j.Store) <= MaxSize && len(j.Files) == len(j.Store) && len(j.Abandoned) == len(j.Store)
// @ requires forall p int :: { &j.Store[p] } 0 <= p && p < len(j.Store) ==> acc(&j.Store[p])
// @ requires forall r int :: { &j.Abandoned[r] } 0 <= r && r < len(j.Store) ==> acc(&j.Abandoned[r])
// @ requires forall r int :: { &j.Files[r] } 0 <= r && r < len(j.Store) ==> acc(&j.Files[r], 1/2) && len(j.Files[r]) == len(j.Store)
// @ requires forall r, p int :: { &j.Files[r][p] } 0 <= r && r < len(j.Store) && 0 <= p && p < len(j.Store) ==> acc(&j.Files[r][p])
// @ requires forall p int :: { at(j.Store, p) } 0 <= p && p < len(j.Store) ==> 0 <= at(j.Store, p) && at(j.Store, p) <= j.Capacity
// @ requires forall r, p int :: { at(j.Files[r], p) } 0 <= r && r < len(j.Store) && 0 <= p && p < len(j.Store) ==> 0 <= at(j.Files[r], p) && at(j.Files[r], p) <= j.Capacity
// @ requires 0 <= c && c < len(j.Store)
// @ requires 0 <= d && d < len(j.Store)
// @ requires c != d ==> forall p, q int :: { &j.Files[c][p], &j.Files[d][q] } 0 <= p && p < len(j.Store) && 0 <= q && q < len(j.Store) ==> &j.Files[c][p] != &j.Files[d][q]
// @ ensures acc(&j.Store, 1/2) && acc(&j.Files, 1/2) && acc(&j.Abandoned, 1/2) && acc(&j.Capacity, 1/2)
// @ ensures 0 <= j.Capacity && j.Capacity <= MaxSize
// @ ensures len(j.Store) <= MaxSize && len(j.Files) == len(j.Store) && len(j.Abandoned) == len(j.Store)
// @ ensures forall p int :: { &j.Store[p] } 0 <= p && p < len(j.Store) ==> acc(&j.Store[p])
// @ ensures forall r int :: { &j.Abandoned[r] } 0 <= r && r < len(j.Store) ==> acc(&j.Abandoned[r])
// @ ensures forall r int :: { &j.Files[r] } 0 <= r && r < len(j.Store) ==> acc(&j.Files[r], 1/2) && len(j.Files[r]) == len(j.Store)
// @ ensures forall r, p int :: { &j.Files[r][p] } 0 <= r && r < len(j.Store) && 0 <= p && p < len(j.Store) ==> acc(&j.Files[r][p])
// @ ensures forall p int :: { at(j.Store, p) } 0 <= p && p < len(j.Store) ==> 0 <= at(j.Store, p) && at(j.Store, p) <= j.Capacity
// @ ensures forall r, p int :: { at(j.Files[r], p) } 0 <= r && r < len(j.Store) && 0 <= p && p < len(j.Store) ==> 0 <= at(j.Files[r], p) && at(j.Files[r], p) <= j.Capacity
// @ ensures j.Capacity == old(j.Capacity) && len(j.Store) == old(len(j.Store))
// @ ensures ran == (c != d && !old(j.Abandoned[c]) && !old(j.Abandoned[d]) && (exists p int :: 0 <= p && p < len(j.Store) && old(at(j.Files[d], p)) > old(at(j.Files[c], p))))
// @ ensures forall r, p int :: { at(j.Files[r], p) } 0 <= r && r < len(j.Store) && 0 <= p && p < len(j.Store) ==> at(j.Files[r], p) == (ran && r == c ? larger(old(at(j.Files[c], p)), old(at(j.Files[d], p))) : old(at(j.Files[r], p)))
// @ ensures forall p int :: { at(j.Store, p) } 0 <= p && p < len(j.Store) ==> at(j.Store, p) == old(at(j.Store, p))
// @ ensures forall r int :: { j.Abandoned[r] } 0 <= r && r < len(j.Store) ==> j.Abandoned[r] == old(j.Abandoned[r])
func (j *Journal) TakeIn(c, d int) (ran bool) {
	if c == d || j.Abandoned[c] || j.Abandoned[d] {
		return false
	}
	return mergePrefixes(j.Files[c], j.Files[d])
}

// Rebuild journals every line held by a checkout, including an abandoned one.
// The store keeps its other lines. If all lines are present already, it refuses.
// @ requires acc(&j.Store, 1/2) && acc(&j.Files, 1/2) && acc(&j.Abandoned, 1/2) && acc(&j.Capacity, 1/2)
// @ requires 0 <= j.Capacity && j.Capacity <= MaxSize
// @ requires len(j.Store) <= MaxSize && len(j.Files) == len(j.Store) && len(j.Abandoned) == len(j.Store)
// @ requires forall p int :: { &j.Store[p] } 0 <= p && p < len(j.Store) ==> acc(&j.Store[p])
// @ requires forall r int :: { &j.Abandoned[r] } 0 <= r && r < len(j.Store) ==> acc(&j.Abandoned[r])
// @ requires forall r int :: { &j.Files[r] } 0 <= r && r < len(j.Store) ==> acc(&j.Files[r], 1/2) && len(j.Files[r]) == len(j.Store)
// @ requires forall r, p int :: { &j.Files[r][p] } 0 <= r && r < len(j.Store) && 0 <= p && p < len(j.Store) ==> acc(&j.Files[r][p])
// @ requires forall p int :: { at(j.Store, p) } 0 <= p && p < len(j.Store) ==> 0 <= at(j.Store, p) && at(j.Store, p) <= j.Capacity
// @ requires forall r, p int :: { at(j.Files[r], p) } 0 <= r && r < len(j.Store) && 0 <= p && p < len(j.Store) ==> 0 <= at(j.Files[r], p) && at(j.Files[r], p) <= j.Capacity
// @ requires 0 <= c && c < len(j.Store)
// @ requires forall p, q int :: { &j.Store[p], &j.Files[c][q] } 0 <= p && p < len(j.Store) && 0 <= q && q < len(j.Store) ==> &j.Store[p] != &j.Files[c][q]
// @ ensures acc(&j.Store, 1/2) && acc(&j.Files, 1/2) && acc(&j.Abandoned, 1/2) && acc(&j.Capacity, 1/2)
// @ ensures 0 <= j.Capacity && j.Capacity <= MaxSize
// @ ensures len(j.Store) <= MaxSize && len(j.Files) == len(j.Store) && len(j.Abandoned) == len(j.Store)
// @ ensures forall p int :: { &j.Store[p] } 0 <= p && p < len(j.Store) ==> acc(&j.Store[p])
// @ ensures forall r int :: { &j.Abandoned[r] } 0 <= r && r < len(j.Store) ==> acc(&j.Abandoned[r])
// @ ensures forall r int :: { &j.Files[r] } 0 <= r && r < len(j.Store) ==> acc(&j.Files[r], 1/2) && len(j.Files[r]) == len(j.Store)
// @ ensures forall r, p int :: { &j.Files[r][p] } 0 <= r && r < len(j.Store) && 0 <= p && p < len(j.Store) ==> acc(&j.Files[r][p])
// @ ensures forall p int :: { at(j.Store, p) } 0 <= p && p < len(j.Store) ==> 0 <= at(j.Store, p) && at(j.Store, p) <= j.Capacity
// @ ensures forall r, p int :: { at(j.Files[r], p) } 0 <= r && r < len(j.Store) && 0 <= p && p < len(j.Store) ==> 0 <= at(j.Files[r], p) && at(j.Files[r], p) <= j.Capacity
// @ ensures j.Capacity == old(j.Capacity) && len(j.Store) == old(len(j.Store))
// @ ensures ran == (exists p int :: 0 <= p && p < len(j.Store) && old(at(j.Files[c], p)) > old(at(j.Store, p)))
// @ ensures forall p int :: { at(j.Store, p) } 0 <= p && p < len(j.Store) ==> at(j.Store, p) == larger(old(at(j.Store, p)), old(at(j.Files[c], p)))
// @ ensures !ran ==> forall p int :: { at(j.Store, p) } 0 <= p && p < len(j.Store) ==> at(j.Store, p) == old(at(j.Store, p))
// @ ensures forall r, p int :: { at(j.Files[r], p) } 0 <= r && r < len(j.Store) && 0 <= p && p < len(j.Store) ==> at(j.Files[r], p) == old(at(j.Files[r], p))
// @ ensures forall r int :: { j.Abandoned[r] } 0 <= r && r < len(j.Store) ==> j.Abandoned[r] == old(j.Abandoned[r])
func (j *Journal) Rebuild(c int) (ran bool) {
	return mergePrefixes(j.Store, j.Files[c])
}

// Abandon prevents further writes and merges involving a checkout.
// It preserves every line and refuses if the checkout is already abandoned.
// @ requires acc(&j.Store, 1/2) && acc(&j.Files, 1/2) && acc(&j.Abandoned, 1/2) && acc(&j.Capacity, 1/2)
// @ requires 0 <= j.Capacity && j.Capacity <= MaxSize
// @ requires len(j.Store) <= MaxSize && len(j.Files) == len(j.Store) && len(j.Abandoned) == len(j.Store)
// @ requires forall p int :: { &j.Store[p] } 0 <= p && p < len(j.Store) ==> acc(&j.Store[p])
// @ requires forall r int :: { &j.Abandoned[r] } 0 <= r && r < len(j.Store) ==> acc(&j.Abandoned[r])
// @ requires forall r int :: { &j.Files[r] } 0 <= r && r < len(j.Store) ==> acc(&j.Files[r], 1/2) && len(j.Files[r]) == len(j.Store)
// @ requires forall r, p int :: { &j.Files[r][p] } 0 <= r && r < len(j.Store) && 0 <= p && p < len(j.Store) ==> acc(&j.Files[r][p])
// @ requires forall p int :: { at(j.Store, p) } 0 <= p && p < len(j.Store) ==> 0 <= at(j.Store, p) && at(j.Store, p) <= j.Capacity
// @ requires forall r, p int :: { at(j.Files[r], p) } 0 <= r && r < len(j.Store) && 0 <= p && p < len(j.Store) ==> 0 <= at(j.Files[r], p) && at(j.Files[r], p) <= j.Capacity
// @ requires 0 <= c && c < len(j.Store)
// @ ensures acc(&j.Store, 1/2) && acc(&j.Files, 1/2) && acc(&j.Abandoned, 1/2) && acc(&j.Capacity, 1/2)
// @ ensures 0 <= j.Capacity && j.Capacity <= MaxSize
// @ ensures len(j.Store) <= MaxSize && len(j.Files) == len(j.Store) && len(j.Abandoned) == len(j.Store)
// @ ensures forall p int :: { &j.Store[p] } 0 <= p && p < len(j.Store) ==> acc(&j.Store[p])
// @ ensures forall r int :: { &j.Abandoned[r] } 0 <= r && r < len(j.Store) ==> acc(&j.Abandoned[r])
// @ ensures forall r int :: { &j.Files[r] } 0 <= r && r < len(j.Store) ==> acc(&j.Files[r], 1/2) && len(j.Files[r]) == len(j.Store)
// @ ensures forall r, p int :: { &j.Files[r][p] } 0 <= r && r < len(j.Store) && 0 <= p && p < len(j.Store) ==> acc(&j.Files[r][p])
// @ ensures forall p int :: { at(j.Store, p) } 0 <= p && p < len(j.Store) ==> 0 <= at(j.Store, p) && at(j.Store, p) <= j.Capacity
// @ ensures forall r, p int :: { at(j.Files[r], p) } 0 <= r && r < len(j.Store) && 0 <= p && p < len(j.Store) ==> 0 <= at(j.Files[r], p) && at(j.Files[r], p) <= j.Capacity
// @ ensures j.Capacity == old(j.Capacity) && len(j.Store) == old(len(j.Store))
// @ ensures ran == !old(j.Abandoned[c])
// @ ensures forall r int :: { j.Abandoned[r] } 0 <= r && r < len(j.Store) ==> j.Abandoned[r] == (r == c || old(j.Abandoned[r]))
// @ ensures forall p int :: { at(j.Store, p) } 0 <= p && p < len(j.Store) ==> at(j.Store, p) == old(at(j.Store, p))
// @ ensures forall r, p int :: { at(j.Files[r], p) } 0 <= r && r < len(j.Store) && 0 <= p && p < len(j.Store) ==> at(j.Files[r], p) == old(at(j.Files[r], p))
func (j *Journal) Abandon(c int) (ran bool) {
	if j.Abandoned[c] {
		return false
	}
	j.Abandoned[c] = true
	return true
}
