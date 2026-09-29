// +gobra

package ids

// MaxCapacity leaves room for index arithmetic even on a 32-bit machine.
const MaxCapacity = 1 << 30

const (
	Idle = iota
	Taken
	Reserved
	Written
)

// Decision identifies the contents of a journal file. The file's ID is its
// one-based position in a journal; Number == 0 denotes an empty position.
// Number is supplied by the caller, not counted by the allocation protocol.
type Decision struct {
	By     int
	Number int
}

// Core is the machine's project store and the journal on main. Lock is zero
// when free and otherwise identifies the checkout holding the store's lock.
// The caller executes transitions while exclusively accessing this state.
type Core struct {
	Store []bool
	Main  []Decision
	Lock  int
}

// Checkout contains a branch's journal and its in-progress decide. Next is
// retained after Finish or Stop, just as the allocation protocol specifies.
type Checkout struct {
	Files []Decision
	PC    int
	Next  int
}

// @ requires acc(&s.Store, _) && acc(&s.Main, _) && acc(&s.Lock, _)
// @ decreases
// @ pure
func (s *Core) Ok() bool {
	return 0 < len(s.Store) && len(s.Store) <= MaxCapacity &&
		len(s.Main) == len(s.Store) && 0 <= s.Lock
}

// @ requires acc(&c.Files, _) && acc(&c.PC, _) && acc(&c.Next, _)
// @ decreases
// @ pure
func (c *Checkout) Ok() bool {
	return 0 < len(c.Files) && len(c.Files) <= MaxCapacity &&
		Idle <= c.PC && c.PC <= Written &&
		0 <= c.Next && c.Next <= len(c.Files) &&
		(c.PC == Idle || c.Next > 0)
}

// New provisions room for IDs 1 through capacity. A full store causes Take to
// refuse without acquiring the lock. Capacity is chosen by the caller.
// @ requires 0 < capacity && capacity <= MaxCapacity
// @ ensures acc(&s.Store) && acc(&s.Main) && acc(&s.Lock) && s.Ok()
// @ ensures len(s.Store) == capacity && s.Lock == 0
// @ ensures forall i int :: { &s.Store[i] } 0 <= i && i < capacity ==> acc(&s.Store[i])
// @ ensures forall i int :: { &s.Main[i] } 0 <= i && i < capacity ==> acc(&s.Main[i])
// @ ensures forall i int :: 0 <= i && i < capacity ==> !s.Store[i] && s.Main[i] == (Decision{})
func New(capacity int) (s *Core) {
	return &Core{Store: make([]bool, capacity), Main: make([]Decision, capacity)}
}

// NewCheckout creates an idle checkout with an empty journal.
// @ requires 0 < capacity && capacity <= MaxCapacity
// @ ensures acc(&c.Files) && acc(&c.PC) && acc(&c.Next) && c.Ok()
// @ ensures len(c.Files) == capacity && c.PC == Idle && c.Next == 0
// @ ensures forall i int :: { &c.Files[i] } 0 <= i && i < capacity ==> acc(&c.Files[i])
// @ ensures forall i int :: 0 <= i && i < capacity ==> c.Files[i] == (Decision{})
func NewCheckout(capacity int) (c *Checkout) {
	return &Checkout{Files: make([]Decision, capacity)}
}
