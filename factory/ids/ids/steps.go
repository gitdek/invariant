// +gobra

package ids

// NextID returns one more than the largest reserved or locally written ID.
// It can return len(store)+1; Take checks that the result fits before changing
// any state. A checkout imported from another branch is therefore respected.
// @ requires 0 < len(store) && len(store) <= MaxCapacity && len(files) == len(store)
// @ requires forall i int :: { &store[i] } 0 <= i && i < len(store) ==> acc(&store[i], 1/2)
// @ requires forall i int :: { &files[i] } 0 <= i && i < len(files) ==> acc(&files[i], 1/2)
// @ ensures forall i int :: { &store[i] } 0 <= i && i < len(store) ==> acc(&store[i], 1/2)
// @ ensures forall i int :: { &files[i] } 0 <= i && i < len(files) ==> acc(&files[i], 1/2)
// @ ensures forall i int :: 0 <= i && i < len(store) ==> store[i] == old(store[i]) && files[i] == old(files[i])
// @ ensures 1 <= id && id <= len(store) + 1
// @ ensures forall i int :: 0 <= i && i < len(store) && (store[i] || files[i].Number != 0) ==> i + 1 < id
// @ ensures id > 1 ==> store[id - 2] || files[id - 2].Number != 0
func NextID(store []bool, files []Decision) (id int) {
	i := len(store)
	// @ invariant 0 <= i && i <= len(store)
	// @ invariant forall j int :: { &store[j] } 0 <= j && j < len(store) ==> acc(&store[j], 1/2)
	// @ invariant forall j int :: { &files[j] } 0 <= j && j < len(files) ==> acc(&files[j], 1/2)
	// @ invariant forall j int :: 0 <= j && j < len(store) ==> store[j] == old(store[j]) && files[j] == old(files[j])
	// @ invariant forall j int :: i <= j && j < len(store) ==> !store[j] && files[j].Number == 0
	// @ decreases i
	for i > 0 {
		if store[i-1] || files[i-1].Number != 0 {
			return i + 1
		}
		i--
	}
	return 1
}

// Take acquires the project lock and selects an ID in one transition.
// @ requires owner > 0
// @ requires acc(&s.Store, 1/2) && acc(&s.Main, 1/2) && acc(&s.Lock)
// @ requires acc(&c.Files, 1/2) && acc(&c.PC) && acc(&c.Next)
// @ requires s.Ok() && c.Ok() && len(c.Files) == len(s.Store)
// @ requires forall i int :: { &s.Store[i] } 0 <= i && i < len(s.Store) ==> acc(&s.Store[i])
// @ requires forall i int :: { &s.Main[i] } 0 <= i && i < len(s.Main) ==> acc(&s.Main[i])
// @ requires forall i int :: { &c.Files[i] } 0 <= i && i < len(c.Files) ==> acc(&c.Files[i])
// @ ensures acc(&s.Store, 1/2) && acc(&s.Main, 1/2) && acc(&s.Lock)
// @ ensures acc(&c.Files, 1/2) && acc(&c.PC) && acc(&c.Next)
// @ ensures s.Ok() && c.Ok() && len(c.Files) == len(s.Store)
// @ ensures len(s.Store) == old(len(s.Store)) && len(s.Main) == old(len(s.Main)) && len(c.Files) == old(len(c.Files))
// @ ensures forall i int :: { &s.Store[i] } 0 <= i && i < len(s.Store) ==> acc(&s.Store[i])
// @ ensures forall i int :: { &s.Main[i] } 0 <= i && i < len(s.Main) ==> acc(&s.Main[i])
// @ ensures forall i int :: { &c.Files[i] } 0 <= i && i < len(c.Files) ==> acc(&c.Files[i])
// @ ensures forall i int :: 0 <= i && i < len(s.Store) ==> s.Store[i] == old(s.Store[i])
// @ ensures forall i int :: 0 <= i && i < len(s.Main) ==> s.Main[i] == old(s.Main[i])
// @ ensures forall i int :: 0 <= i && i < len(c.Files) ==> c.Files[i] == old(c.Files[i])
// @ ensures ok == old(c.PC == Idle && s.Lock == 0 && !s.Store[len(s.Store)-1] && c.Files[len(c.Files)-1].Number == 0)
// @ ensures ok ==> s.Lock == owner && c.PC == Taken && 1 <= c.Next && c.Next <= len(s.Store)
// @ ensures ok ==> forall i int :: 0 <= i && i < len(s.Store) && (s.Store[i] || c.Files[i].Number != 0) ==> i + 1 < c.Next
// @ ensures ok && c.Next > 1 ==> s.Store[c.Next-2] || c.Files[c.Next-2].Number != 0
// @ ensures !ok ==> s.Lock == old(s.Lock) && c.PC == old(c.PC) && c.Next == old(c.Next)
func (s *Core) Take(c *Checkout, owner int) (ok bool) {
	if c.PC != Idle || s.Lock != 0 {
		return false
	}
	id := NextID(s.Store, c.Files)
	if id > len(s.Store) {
		return false
	}
	s.Lock = owner
	c.Next = id
	c.PC = Taken
	return true
}

// Reserve records the chosen ID durably in the modeled store before Write.
// @ requires acc(&s.Store, 1/2) && acc(&s.Main, 1/2) && acc(&s.Lock)
// @ requires acc(&c.Files, 1/2) && acc(&c.PC) && acc(&c.Next)
// @ requires s.Ok() && c.Ok() && len(c.Files) == len(s.Store)
// @ requires forall i int :: { &s.Store[i] } 0 <= i && i < len(s.Store) ==> acc(&s.Store[i])
// @ requires forall i int :: { &s.Main[i] } 0 <= i && i < len(s.Main) ==> acc(&s.Main[i])
// @ requires forall i int :: { &c.Files[i] } 0 <= i && i < len(c.Files) ==> acc(&c.Files[i])
// @ ensures acc(&s.Store, 1/2) && acc(&s.Main, 1/2) && acc(&s.Lock)
// @ ensures acc(&c.Files, 1/2) && acc(&c.PC) && acc(&c.Next)
// @ ensures s.Ok() && c.Ok() && len(c.Files) == len(s.Store)
// @ ensures len(s.Store) == old(len(s.Store)) && len(s.Main) == old(len(s.Main)) && len(c.Files) == old(len(c.Files))
// @ ensures forall i int :: { &s.Store[i] } 0 <= i && i < len(s.Store) ==> acc(&s.Store[i])
// @ ensures forall i int :: { &s.Main[i] } 0 <= i && i < len(s.Main) ==> acc(&s.Main[i])
// @ ensures forall i int :: { &c.Files[i] } 0 <= i && i < len(c.Files) ==> acc(&c.Files[i])
// @ ensures forall i int :: 0 <= i && i < len(s.Main) ==> s.Main[i] == old(s.Main[i])
// @ ensures forall i int :: 0 <= i && i < len(c.Files) ==> c.Files[i] == old(c.Files[i])
// @ ensures ok == (old(c.PC) == Taken)
// @ ensures c.Next == old(c.Next) && s.Lock == old(s.Lock)
// @ ensures c.PC == (ok ? Reserved : old(c.PC))
// @ ensures forall i int :: 0 <= i && i < len(s.Store) ==> s.Store[i] == (old(s.Store[i]) || (ok && i == c.Next-1))
func (s *Core) Reserve(c *Checkout) (ok bool) {
	if c.PC != Taken {
		return false
	}
	s.Store[c.Next-1] = true
	c.PC = Reserved
	return true
}

// Write places the caller's decision in the checkout's reserved journal slot.
// The ordinal is journal content supplied by the environment, not a limit or
// a history counter in the allocator.
// @ requires decision.By > 0 && decision.Number > 0
// @ requires acc(&s.Store, 1/2) && acc(&s.Main, 1/2) && acc(&s.Lock)
// @ requires acc(&c.Files, 1/2) && acc(&c.PC) && acc(&c.Next)
// @ requires s.Ok() && c.Ok() && len(c.Files) == len(s.Store)
// @ requires forall i int :: { &s.Store[i] } 0 <= i && i < len(s.Store) ==> acc(&s.Store[i])
// @ requires forall i int :: { &s.Main[i] } 0 <= i && i < len(s.Main) ==> acc(&s.Main[i])
// @ requires forall i int :: { &c.Files[i] } 0 <= i && i < len(c.Files) ==> acc(&c.Files[i])
// @ ensures acc(&s.Store, 1/2) && acc(&s.Main, 1/2) && acc(&s.Lock)
// @ ensures acc(&c.Files, 1/2) && acc(&c.PC) && acc(&c.Next)
// @ ensures s.Ok() && c.Ok() && len(c.Files) == len(s.Store)
// @ ensures len(s.Store) == old(len(s.Store)) && len(s.Main) == old(len(s.Main)) && len(c.Files) == old(len(c.Files))
// @ ensures forall i int :: { &s.Store[i] } 0 <= i && i < len(s.Store) ==> acc(&s.Store[i])
// @ ensures forall i int :: { &s.Main[i] } 0 <= i && i < len(s.Main) ==> acc(&s.Main[i])
// @ ensures forall i int :: { &c.Files[i] } 0 <= i && i < len(c.Files) ==> acc(&c.Files[i])
// @ ensures forall i int :: 0 <= i && i < len(s.Store) ==> s.Store[i] == old(s.Store[i])
// @ ensures forall i int :: 0 <= i && i < len(s.Main) ==> s.Main[i] == old(s.Main[i])
// @ ensures ok == (old(c.PC) == Reserved)
// @ ensures c.Next == old(c.Next) && s.Lock == old(s.Lock)
// @ ensures c.PC == (ok ? Written : old(c.PC))
// @ ensures forall i int :: 0 <= i && i < len(c.Files) ==> c.Files[i] == ((ok && i == c.Next-1) ? decision : old(c.Files[i]))
func (s *Core) Write(c *Checkout, decision Decision) (ok bool) {
	if c.PC != Reserved {
		return false
	}
	c.Files[c.Next-1] = decision
	c.PC = Written
	return true
}

// Finish releases the lock after the journal write.
// @ requires acc(&s.Store, 1/2) && acc(&s.Main, 1/2) && acc(&s.Lock)
// @ requires acc(&c.Files, 1/2) && acc(&c.PC) && acc(&c.Next)
// @ requires s.Ok() && c.Ok() && len(c.Files) == len(s.Store)
// @ requires forall i int :: { &s.Store[i] } 0 <= i && i < len(s.Store) ==> acc(&s.Store[i])
// @ requires forall i int :: { &s.Main[i] } 0 <= i && i < len(s.Main) ==> acc(&s.Main[i])
// @ requires forall i int :: { &c.Files[i] } 0 <= i && i < len(c.Files) ==> acc(&c.Files[i])
// @ ensures acc(&s.Store, 1/2) && acc(&s.Main, 1/2) && acc(&s.Lock)
// @ ensures acc(&c.Files, 1/2) && acc(&c.PC) && acc(&c.Next)
// @ ensures s.Ok() && c.Ok() && len(c.Files) == len(s.Store)
// @ ensures len(s.Store) == old(len(s.Store)) && len(s.Main) == old(len(s.Main)) && len(c.Files) == old(len(c.Files))
// @ ensures forall i int :: { &s.Store[i] } 0 <= i && i < len(s.Store) ==> acc(&s.Store[i])
// @ ensures forall i int :: { &s.Main[i] } 0 <= i && i < len(s.Main) ==> acc(&s.Main[i])
// @ ensures forall i int :: { &c.Files[i] } 0 <= i && i < len(c.Files) ==> acc(&c.Files[i])
// @ ensures forall i int :: 0 <= i && i < len(s.Store) ==> s.Store[i] == old(s.Store[i])
// @ ensures forall i int :: 0 <= i && i < len(s.Main) ==> s.Main[i] == old(s.Main[i])
// @ ensures forall i int :: 0 <= i && i < len(c.Files) ==> c.Files[i] == old(c.Files[i])
// @ ensures ok == (old(c.PC) == Written)
// @ ensures c.Next == old(c.Next)
// @ ensures c.PC == (ok ? Idle : old(c.PC))
// @ ensures s.Lock == (ok ? 0 : old(s.Lock))
func (s *Core) Finish(c *Checkout) (ok bool) {
	if c.PC != Written {
		return false
	}
	s.Lock = 0
	c.PC = Idle
	return true
}

// Stop releases a live decide's lock and retains every reservation and file.
// @ requires acc(&s.Store, 1/2) && acc(&s.Main, 1/2) && acc(&s.Lock)
// @ requires acc(&c.Files, 1/2) && acc(&c.PC) && acc(&c.Next)
// @ requires s.Ok() && c.Ok() && len(c.Files) == len(s.Store)
// @ requires forall i int :: { &s.Store[i] } 0 <= i && i < len(s.Store) ==> acc(&s.Store[i])
// @ requires forall i int :: { &s.Main[i] } 0 <= i && i < len(s.Main) ==> acc(&s.Main[i])
// @ requires forall i int :: { &c.Files[i] } 0 <= i && i < len(c.Files) ==> acc(&c.Files[i])
// @ ensures acc(&s.Store, 1/2) && acc(&s.Main, 1/2) && acc(&s.Lock)
// @ ensures acc(&c.Files, 1/2) && acc(&c.PC) && acc(&c.Next)
// @ ensures s.Ok() && c.Ok() && len(c.Files) == len(s.Store)
// @ ensures len(s.Store) == old(len(s.Store)) && len(s.Main) == old(len(s.Main)) && len(c.Files) == old(len(c.Files))
// @ ensures forall i int :: { &s.Store[i] } 0 <= i && i < len(s.Store) ==> acc(&s.Store[i])
// @ ensures forall i int :: { &s.Main[i] } 0 <= i && i < len(s.Main) ==> acc(&s.Main[i])
// @ ensures forall i int :: { &c.Files[i] } 0 <= i && i < len(c.Files) ==> acc(&c.Files[i])
// @ ensures forall i int :: 0 <= i && i < len(s.Store) ==> s.Store[i] == old(s.Store[i])
// @ ensures forall i int :: 0 <= i && i < len(s.Main) ==> s.Main[i] == old(s.Main[i])
// @ ensures forall i int :: 0 <= i && i < len(c.Files) ==> c.Files[i] == old(c.Files[i])
// @ ensures ok == (old(c.PC) != Idle)
// @ ensures c.Next == old(c.Next)
// @ ensures c.PC == (ok ? Idle : old(c.PC))
// @ ensures s.Lock == (ok ? 0 : old(s.Lock))
func (s *Core) Stop(c *Checkout) (ok bool) {
	if c.PC == Idle {
		return false
	}
	s.Lock = 0
	c.PC = Idle
	return true
}

// Merge copies this checkout's decisions into main. Existing IDs retain their
// decision identity; reachable journals agree on every ID present in both.
// @ requires acc(&s.Store, 1/2) && acc(&s.Main, 1/2) && acc(&s.Lock)
// @ requires acc(&c.Files, 1/2) && acc(&c.PC) && acc(&c.Next)
// @ requires s.Ok() && c.Ok() && len(c.Files) == len(s.Store)
// @ requires forall i int :: { &s.Store[i] } 0 <= i && i < len(s.Store) ==> acc(&s.Store[i])
// @ requires forall i int :: { &s.Main[i] } 0 <= i && i < len(s.Main) ==> acc(&s.Main[i])
// @ requires forall i int :: { &c.Files[i] } 0 <= i && i < len(c.Files) ==> acc(&c.Files[i])
// @ ensures acc(&s.Store, 1/2) && acc(&s.Main, 1/2) && acc(&s.Lock)
// @ ensures acc(&c.Files, 1/2) && acc(&c.PC) && acc(&c.Next)
// @ ensures s.Ok() && c.Ok() && len(c.Files) == len(s.Store)
// @ ensures len(s.Store) == old(len(s.Store)) && len(s.Main) == old(len(s.Main)) && len(c.Files) == old(len(c.Files))
// @ ensures forall i int :: { &s.Store[i] } 0 <= i && i < len(s.Store) ==> acc(&s.Store[i])
// @ ensures forall i int :: { &s.Main[i] } 0 <= i && i < len(s.Main) ==> acc(&s.Main[i])
// @ ensures forall i int :: { &c.Files[i] } 0 <= i && i < len(c.Files) ==> acc(&c.Files[i])
// @ ensures forall i int :: 0 <= i && i < len(s.Store) ==> s.Store[i] == old(s.Store[i])
// @ ensures forall i int :: 0 <= i && i < len(c.Files) ==> c.Files[i] == old(c.Files[i])
// @ ensures c.PC == old(c.PC) && c.Next == old(c.Next) && s.Lock == old(s.Lock)
// @ ensures ok == (old(c.PC) == Idle && (exists i int :: 0 <= i && i < len(s.Main) && old(s.Main[i].Number) == 0 && c.Files[i].Number != 0))
// @ ensures forall i int :: 0 <= i && i < len(s.Main) ==> s.Main[i] == ((old(c.PC) == Idle && old(s.Main[i].Number) == 0 && c.Files[i].Number != 0) ? c.Files[i] : old(s.Main[i]))
// @ ensures !ok ==> forall i int :: 0 <= i && i < len(s.Main) ==> s.Main[i] == old(s.Main[i])
func (s *Core) Merge(c *Checkout) (ok bool) {
	if c.PC != Idle {
		return false
	}
	return unite(s.Main, c.Files)
}

// Pull copies main's decisions into an idle checkout.
// @ requires acc(&s.Store, 1/2) && acc(&s.Main, 1/2) && acc(&s.Lock)
// @ requires acc(&c.Files, 1/2) && acc(&c.PC) && acc(&c.Next)
// @ requires s.Ok() && c.Ok() && len(c.Files) == len(s.Store)
// @ requires forall i int :: { &s.Store[i] } 0 <= i && i < len(s.Store) ==> acc(&s.Store[i])
// @ requires forall i int :: { &s.Main[i] } 0 <= i && i < len(s.Main) ==> acc(&s.Main[i])
// @ requires forall i int :: { &c.Files[i] } 0 <= i && i < len(c.Files) ==> acc(&c.Files[i])
// @ ensures acc(&s.Store, 1/2) && acc(&s.Main, 1/2) && acc(&s.Lock)
// @ ensures acc(&c.Files, 1/2) && acc(&c.PC) && acc(&c.Next)
// @ ensures s.Ok() && c.Ok() && len(c.Files) == len(s.Store)
// @ ensures len(s.Store) == old(len(s.Store)) && len(s.Main) == old(len(s.Main)) && len(c.Files) == old(len(c.Files))
// @ ensures forall i int :: { &s.Store[i] } 0 <= i && i < len(s.Store) ==> acc(&s.Store[i])
// @ ensures forall i int :: { &s.Main[i] } 0 <= i && i < len(s.Main) ==> acc(&s.Main[i])
// @ ensures forall i int :: { &c.Files[i] } 0 <= i && i < len(c.Files) ==> acc(&c.Files[i])
// @ ensures forall i int :: 0 <= i && i < len(s.Store) ==> s.Store[i] == old(s.Store[i])
// @ ensures forall i int :: 0 <= i && i < len(s.Main) ==> s.Main[i] == old(s.Main[i])
// @ ensures c.PC == old(c.PC) && c.Next == old(c.Next) && s.Lock == old(s.Lock)
// @ ensures ok == (old(c.PC) == Idle && (exists i int :: 0 <= i && i < len(c.Files) && old(c.Files[i].Number) == 0 && s.Main[i].Number != 0))
// @ ensures forall i int :: 0 <= i && i < len(c.Files) ==> c.Files[i] == ((old(c.PC) == Idle && old(c.Files[i].Number) == 0 && s.Main[i].Number != 0) ? s.Main[i] : old(c.Files[i]))
// @ ensures !ok ==> forall i int :: 0 <= i && i < len(c.Files) ==> c.Files[i] == old(c.Files[i])
func (s *Core) Pull(c *Checkout) (ok bool) {
	if c.PC != Idle {
		return false
	}
	return unite(c.Files, s.Main)
}
