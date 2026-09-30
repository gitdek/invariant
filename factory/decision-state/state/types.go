// +gobra

package state

// MaxCapacity is a machine safety limit, independent of exploration bounds.
const MaxCapacity = 1 << 30

// Kind distinguishes people from agents. The zero value means no writer.
type Kind int

const (
	Nobody Kind = iota
	Person
	Agent
)

// Writer identifies a writer within its kind. IDs are positive and may be
// assigned by the caller's identity registry; equal IDs of different kinds
// name different writers.
type Writer struct {
	Kind Kind
	ID   int
}

// Valid reports whether w names a person or an agent.
// @ decreases
// @ pure
func (w Writer) Valid() bool {
	return (w.Kind == Person || w.Kind == Agent) && w.ID > 0
}

type Door int

const (
	NoDoor Door = iota
	OneWay
	TwoWay
)

type Status int

const (
	None Status = iota
	Proposed
	Decided
	Ratified
	Superseded
	NoStatus // The "-" field of ratify and supersede lines.
)

type Op int

const (
	NoOp Op = iota
	DecideOp
	RatifyOp
	SupersedeOp
)

// Line records one step. Ratify and supersede lines have NoDoor and NoStatus;
// their writer records who appended the line, even when Who is unchanged.
type Line struct {
	Op     Op
	By     Writer
	Door   Door
	Status Status
}

// Journal owns a fixed-capacity slice and the state obtained by applying its
// used lines in order. Fields are private so callers can only append through
// the checked operations. The zero value is an empty journal with no room.
type Journal struct {
	lines  []Line
	n      int
	status Status
	door   Door
	who    Writer
}

// Ok describes the representation, without imposing any exploration bound.
// @ requires acc(&j.lines, _) && acc(&j.n, _) && acc(&j.status, _) && acc(&j.door, _) && acc(&j.who, _)
// @ decreases
// @ pure
func (j *Journal) Ok() bool {
	return 0 <= len(j.lines) && len(j.lines) <= MaxCapacity &&
		0 <= j.n && j.n <= len(j.lines) &&
		None <= j.status && j.status <= Superseded &&
		NoDoor <= j.door && j.door <= TwoWay &&
		(j.who == (Writer{}) || j.who.Valid())
}

// At is the proof's view of a used journal slot.
// @ ghost
// @ requires acc(&j.lines, _) && acc(&j.n, _) && 0 <= j.n && j.n <= len(j.lines)
// @ requires forall k int :: { &j.lines[k] } 0 <= k && k < len(j.lines) ==> acc(&j.lines[k], _)
// @ requires 0 <= i && i < j.n
// @ decreases
// @ pure func (j *Journal) At(i int) Line { return j.lines[i] }

// New allocates an empty journal. A zero capacity is allowed: every step then
// refuses. The caller chooses the capacity independently of model checking.
// @ requires 0 <= capacity && capacity <= MaxCapacity
// @ ensures acc(&j.lines) && acc(&j.n) && acc(&j.status) && acc(&j.door) && acc(&j.who) && j.Ok()
// @ ensures forall k int :: { &j.lines[k] } 0 <= k && k < len(j.lines) ==> acc(&j.lines[k])
// @ ensures len(j.lines) == capacity && j.n == 0 && j.status == None && j.door == NoDoor && j.who == (Writer{})
func New(capacity int) (j *Journal) {
	return &Journal{lines: make([]Line, capacity)}
}

// View is the decision's current state. Who is the writer of the decision or
// its ratification; superseding does not replace that writer.
type View struct {
	Status Status
	Door   Door
	Who    Writer
}

// Current returns the state accumulated so far.
// @ requires acc(&j.status, _) && acc(&j.door, _) && acc(&j.who, _)
// @ ensures v == (View{j.status, j.door, j.who})
// @ decreases
// @ pure
func (j *Journal) Current() (v View) {
	return View{j.status, j.door, j.who}
}

// Len returns the number of accepted lines.
// @ requires acc(&j.n, _)
// @ ensures n == j.n
// @ decreases
// @ pure
func (j *Journal) Len() (n int) {
	return j.n
}

// Read returns a line by zero-based index without exposing the owned slice.
// @ requires acc(&j.lines, 1/2) && acc(&j.n, 1/2) && 0 <= j.n && j.n <= len(j.lines)
// @ requires forall k int :: { &j.lines[k] } 0 <= k && k < len(j.lines) ==> acc(&j.lines[k], 1/2)
// @ ensures acc(&j.lines, 1/2) && acc(&j.n, 1/2) && j.n == old(j.n) && len(j.lines) == old(len(j.lines))
// @ ensures forall k int :: { &j.lines[k] } 0 <= k && k < len(j.lines) ==> acc(&j.lines[k], 1/2)
// @ ensures forall k int :: { &j.lines[k] } 0 <= k && k < len(j.lines) ==> j.lines[k] == old(j.lines[k])
// @ ensures ok == (0 <= i && i < j.n)
// @ ensures ok ==> l == j.At(i)
// @ ensures !ok ==> l == (Line{})
func (j *Journal) Read(i int) (l Line, ok bool) {
	if i < 0 || i >= j.n {
		return Line{}, false
	}
	return j.lines[i], true
}
