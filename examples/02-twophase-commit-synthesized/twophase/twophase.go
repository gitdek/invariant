// +gobra

package twophase

// This package implements two-phase commit as modeled in
// .invariant/specs/TwoPhase.tla. The coordinator and the resource managers
// hold their own state; the messages between them are the network's, which
// the explorer models.

// MaxRMs keeps index arithmetic far from overflow. It's the machine's limit,
// not the model's.
const MaxRMs = 1 << 30

// Resource manager states, the values of rmState[r].
const (
	Working   = 0
	Prepared  = 1
	Committed = 2
	Aborted   = 3
)

// Coordinator is the transaction manager.
type Coordinator struct {
	// Done is tmState = "done"; false means "init".
	Done bool
	// Prepared[r] is r \in tmPrepared, one entry per resource manager.
	Prepared []bool
}

// Participants are the resource managers.
type Participants struct {
	// States[r] is rmState[r].
	States []int
}

// RcvPrepared records resource manager r's Prepared message, and refuses once
// the coordinator has decided.
// @ requires acc(&c.Done) && acc(&c.Prepared, 1/2)
// @ requires forall j int :: { &c.Prepared[j] } 0 <= j && j < len(c.Prepared) ==> acc(&c.Prepared[j])
// @ requires 0 <= r && r < len(c.Prepared)
// @ ensures acc(&c.Done) && acc(&c.Prepared, 1/2) && len(c.Prepared) == old(len(c.Prepared))
// @ ensures forall j int :: { &c.Prepared[j] } 0 <= j && j < len(c.Prepared) ==> acc(&c.Prepared[j])
// @ ensures ok == !old(c.Done)
// @ ensures c.Done == old(c.Done)
// @ ensures ok ==> c.Prepared[r]
// @ ensures !ok ==> c.Prepared[r] == old(c.Prepared[r])
// @ ensures forall j int :: { c.Prepared[j] } 0 <= j && j < len(c.Prepared) && j != r ==> c.Prepared[j] == old(c.Prepared[j])
func (c *Coordinator) RcvPrepared(r int) (ok bool) {
	if c.Done {
		return false
	}
	c.Prepared[r] = true
	return true
}

// Commit decides to commit once every resource manager has prepared, and
// refuses otherwise or once the coordinator has decided.
// @ requires acc(&c.Done) && acc(&c.Prepared, 1/2)
// @ requires forall j int :: { &c.Prepared[j] } 0 <= j && j < len(c.Prepared) ==> acc(&c.Prepared[j], 1/2)
// @ requires len(c.Prepared) <= MaxRMs
// @ ensures acc(&c.Done) && acc(&c.Prepared, 1/2) && len(c.Prepared) == old(len(c.Prepared))
// @ ensures forall j int :: { &c.Prepared[j] } 0 <= j && j < len(c.Prepared) ==> acc(&c.Prepared[j], 1/2)
// @ ensures forall j int :: { c.Prepared[j] } 0 <= j && j < len(c.Prepared) ==> c.Prepared[j] == old(c.Prepared[j])
// @ ensures ok == (!old(c.Done) && forall j int :: { c.Prepared[j] } 0 <= j && j < len(c.Prepared) ==> c.Prepared[j])
// @ ensures ok ==> c.Done
// @ ensures !ok ==> c.Done == old(c.Done)
func (c *Coordinator) Commit() (ok bool) {
	if c.Done {
		return false
	}
	// @ invariant acc(&c.Done) && !c.Done && c.Done == old(c.Done)
	// @ invariant acc(&c.Prepared, 1/2) && len(c.Prepared) == old(len(c.Prepared))
	// @ invariant forall j int :: { &c.Prepared[j] } 0 <= j && j < len(c.Prepared) ==> acc(&c.Prepared[j], 1/2)
	// @ invariant forall j int :: { c.Prepared[j] } 0 <= j && j < len(c.Prepared) ==> c.Prepared[j] == old(c.Prepared[j])
	// @ invariant 0 <= i && i <= len(c.Prepared) && len(c.Prepared) <= MaxRMs
	// @ invariant forall j int :: { c.Prepared[j] } 0 <= j && j < i ==> c.Prepared[j]
	for i := 0; i < len(c.Prepared); i++ {
		if !c.Prepared[i] {
			return false
		}
	}
	c.Done = true
	return true
}

// Abort decides to abort, and refuses once the coordinator has decided.
// @ requires acc(&c.Done)
// @ ensures acc(&c.Done)
// @ ensures ok == !old(c.Done)
// @ ensures c.Done
func (c *Coordinator) Abort() (ok bool) {
	if c.Done {
		return false
	}
	c.Done = true
	return true
}

// Prepare moves resource manager r from working to prepared, and refuses
// once r has left working.
// @ requires acc(&p.States, 1/2)
// @ requires forall j int :: { &p.States[j] } 0 <= j && j < len(p.States) ==> acc(&p.States[j])
// @ requires 0 <= r && r < len(p.States)
// @ ensures acc(&p.States, 1/2) && len(p.States) == old(len(p.States))
// @ ensures forall j int :: { &p.States[j] } 0 <= j && j < len(p.States) ==> acc(&p.States[j])
// @ ensures ok == (old(p.States[r]) == Working)
// @ ensures ok ==> p.States[r] == Prepared
// @ ensures !ok ==> p.States[r] == old(p.States[r])
// @ ensures forall j int :: { p.States[j] } 0 <= j && j < len(p.States) && j != r ==> p.States[j] == old(p.States[j])
func (p *Participants) Prepare(r int) (ok bool) {
	if p.States[r] != Working {
		return false
	}
	p.States[r] = Prepared
	return true
}

// ChooseToAbort moves resource manager r from working to aborted, and refuses
// once r has left working.
// @ requires acc(&p.States, 1/2)
// @ requires forall j int :: { &p.States[j] } 0 <= j && j < len(p.States) ==> acc(&p.States[j])
// @ requires 0 <= r && r < len(p.States)
// @ ensures acc(&p.States, 1/2) && len(p.States) == old(len(p.States))
// @ ensures forall j int :: { &p.States[j] } 0 <= j && j < len(p.States) ==> acc(&p.States[j])
// @ ensures ok == (old(p.States[r]) == Working)
// @ ensures ok ==> p.States[r] == Aborted
// @ ensures !ok ==> p.States[r] == old(p.States[r])
// @ ensures forall j int :: { p.States[j] } 0 <= j && j < len(p.States) && j != r ==> p.States[j] == old(p.States[j])
func (p *Participants) ChooseToAbort(r int) (ok bool) {
	if p.States[r] != Working {
		return false
	}
	p.States[r] = Aborted
	return true
}

// RcvCommit makes resource manager r follow the coordinator's Commit decision.
// @ requires acc(&p.States, 1/2)
// @ requires forall j int :: { &p.States[j] } 0 <= j && j < len(p.States) ==> acc(&p.States[j])
// @ requires 0 <= r && r < len(p.States)
// @ ensures acc(&p.States, 1/2) && len(p.States) == old(len(p.States))
// @ ensures forall j int :: { &p.States[j] } 0 <= j && j < len(p.States) ==> acc(&p.States[j])
// @ ensures p.States[r] == Committed
// @ ensures forall j int :: { p.States[j] } 0 <= j && j < len(p.States) && j != r ==> p.States[j] == old(p.States[j])
func (p *Participants) RcvCommit(r int) {
	p.States[r] = Committed
}

// RcvAbort makes resource manager r follow the coordinator's Abort decision.
// @ requires acc(&p.States, 1/2)
// @ requires forall j int :: { &p.States[j] } 0 <= j && j < len(p.States) ==> acc(&p.States[j])
// @ requires 0 <= r && r < len(p.States)
// @ ensures acc(&p.States, 1/2) && len(p.States) == old(len(p.States))
// @ ensures forall j int :: { &p.States[j] } 0 <= j && j < len(p.States) ==> acc(&p.States[j])
// @ ensures p.States[r] == Aborted
// @ ensures forall j int :: { p.States[j] } 0 <= j && j < len(p.States) && j != r ==> p.States[j] == old(p.States[j])
func (p *Participants) RcvAbort(r int) {
	p.States[r] = Aborted
}
