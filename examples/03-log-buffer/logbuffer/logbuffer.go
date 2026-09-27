// +gobra

// A spike for slice 8: a ring buffer of any capacity, with O(1) writes and
// ships and no model bounds. The index arithmetic is linear: code wraps with
// an if, and the spec wraps with a ghost conditional.
package logbuffer

// MaxCapacity keeps index arithmetic far from overflow. It's the machine's
// limit, not the model's.
const MaxCapacity = 1 << 30

type Line struct {
	P int
	N int
}

// Buffer is a ring of slots holding N lines, the oldest at Head.
type Buffer struct {
	Slots []Line
	Head  int
	N     int
}

// @ requires acc(&b.Slots, _) && acc(&b.Head, _) && acc(&b.N, _)
// @ decreases
// @ pure
func (b *Buffer) Ok() bool {
	return 0 < len(b.Slots) && len(b.Slots) <= MaxCapacity &&
		0 <= b.Head && b.Head < len(b.Slots) && 0 <= b.N && b.N <= len(b.Slots)
}

// @ ghost
// @ requires 0 <= x && x < 2*n && 0 < n
// @ ensures 0 <= r && r < n
// @ decreases
// @ pure func wrap(x, n int) (r int) { return x < n ? x : x - n }

// @ ghost
// @ requires acc(&b.Slots, _) && acc(&b.Head, _) && acc(&b.N, _) && b.Ok()
// @ requires forall j int :: { &b.Slots[j] } 0 <= j && j < len(b.Slots) ==> acc(&b.Slots[j], _)
// @ requires 0 <= i && i < b.N
// @ decreases
// @ pure func (b *Buffer) At(i int) Line { return b.Slots[wrap(b.Head+i, len(b.Slots))] }

// New makes an empty buffer of the given capacity.
// @ requires 0 < capacity && capacity <= MaxCapacity
// @ ensures acc(&b.Slots) && acc(&b.Head) && acc(&b.N) && b.Ok()
// @ ensures forall j int :: { &b.Slots[j] } 0 <= j && j < len(b.Slots) ==> acc(&b.Slots[j])
// @ ensures len(b.Slots) == capacity && b.N == 0
func New(capacity int) (b *Buffer) {
	return &Buffer{Slots: make([]Line, capacity)}
}

// Write adds a line, newest, when there's room.
// @ requires acc(&b.Slots, 1/2) && acc(&b.Head) && acc(&b.N) && b.Ok()
// @ requires forall j int :: { &b.Slots[j] } 0 <= j && j < len(b.Slots) ==> acc(&b.Slots[j])
// @ requires b.N < len(b.Slots)
// @ ensures acc(&b.Slots, 1/2) && acc(&b.Head) && acc(&b.N) && b.Ok()
// @ ensures forall j int :: { &b.Slots[j] } 0 <= j && j < len(b.Slots) ==> acc(&b.Slots[j])
// @ ensures b.N == old(b.N) + 1 && b.At(old(b.N)) == l
// @ ensures forall i int :: { b.At(i) } 0 <= i && i < old(b.N) ==> b.At(i) == old(b.At(i))
func (b *Buffer) Write(l Line) {
	i := b.Head + b.N
	if i >= len(b.Slots) {
		i = i - len(b.Slots)
	}
	b.Slots[i] = l
	b.N = b.N + 1
}

// Ship takes the oldest line out, when there is one.
// @ requires acc(&b.Slots, 1/2) && acc(&b.Head) && acc(&b.N) && b.Ok()
// @ requires forall j int :: { &b.Slots[j] } 0 <= j && j < len(b.Slots) ==> acc(&b.Slots[j])
// @ requires 0 < b.N
// @ ensures acc(&b.Slots, 1/2) && acc(&b.Head) && acc(&b.N) && b.Ok()
// @ ensures forall j int :: { &b.Slots[j] } 0 <= j && j < len(b.Slots) ==> acc(&b.Slots[j])
// @ ensures l == old(b.At(0)) && b.N == old(b.N) - 1
// @ ensures forall i int :: { b.At(i) } 0 <= i && i < b.N ==> b.At(i) == old(b.At(i + 1))
func (b *Buffer) Ship() (l Line) {
	l = b.Slots[b.Head]
	b.Head = b.Head + 1
	if b.Head == len(b.Slots) {
		b.Head = 0
	}
	b.N = b.N - 1
	return l
}
