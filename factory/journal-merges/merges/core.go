// +gobra

package merges

// MaxSize is a machine limit that keeps every length, index and line number
// within the signed integer range, with room to add two lengths, even on a
// 32-bit machine.
const MaxSize = 1 << 29

// Line is a journal line: the branch that added it, from 1, and which of
// that branch's lines it is, from 1.
type Line struct {
	By int
	N  int
}

// Core is main's journal. File f holds its lines, in order, in Lines[f][0]
// to Lines[f][Len[f]-1], and every file, main's and each branch's, has room
// for Room lines. Capacity is how many lines each branch may add. Room and
// Capacity are sizes the caller chooses; the methods are synchronous.
type Core struct {
	Lines    [][]Line
	Len      []int
	Room     int
	Capacity int
}

// Branch is a branch's journal, laid out as main's is, and the lines it has
// added: By names the branch, and its lines are numbered from 1 in the order
// it adds them.
type Branch struct {
	Lines [][]Line
	Len   []int
	By    int
	Added int
}

// New makes main's journal: files empty files, each with room for room
// lines, for branches that each add up to capacity lines.
// @ requires 0 <= files && files <= MaxSize && 0 <= room && room <= MaxSize && 0 <= capacity && capacity <= MaxSize
// @ ensures acc(&s.Lines) && acc(&s.Len) && acc(&s.Room) && acc(&s.Capacity)
// @ ensures len(s.Lines) == files && len(s.Len) == files && s.Room == room && s.Capacity == capacity
// @ ensures forall f int :: { &s.Len[f] } 0 <= f && f < len(s.Len) ==> acc(&s.Len[f]) && s.Len[f] == 0
// @ ensures forall f int :: { &s.Lines[f] } 0 <= f && f < len(s.Lines) ==> acc(&s.Lines[f]) && len(s.Lines[f]) == room
// @ ensures forall f, k int :: { &s.Lines[f][k] } 0 <= f && f < len(s.Lines) && 0 <= k && k < room ==> acc(&s.Lines[f][k])
func New(files, room, capacity int) (s *Core) {
	return &Core{Lines: rows(files, room), Len: make([]int, files), Room: room, Capacity: capacity}
}

// NewBranch makes branch id's journal, with files empty files, each with
// room for room lines, and no lines added.
// @ requires 0 < id && 0 <= files && files <= MaxSize && 0 <= room && room <= MaxSize
// @ ensures acc(&b.Lines) && acc(&b.Len) && acc(&b.By) && acc(&b.Added)
// @ ensures len(b.Lines) == files && len(b.Len) == files && b.By == id && b.Added == 0
// @ ensures forall f int :: { &b.Len[f] } 0 <= f && f < len(b.Len) ==> acc(&b.Len[f]) && b.Len[f] == 0
// @ ensures forall f int :: { &b.Lines[f] } 0 <= f && f < len(b.Lines) ==> acc(&b.Lines[f]) && len(b.Lines[f]) == room
// @ ensures forall f, k int :: { &b.Lines[f][k] } 0 <= f && f < len(b.Lines) && 0 <= k && k < room ==> acc(&b.Lines[f][k])
func NewBranch(id, files, room int) (b *Branch) {
	return &Branch{Lines: rows(files, room), Len: make([]int, files), By: id}
}

// rows allocates n files with room for room lines each.
// @ requires 0 <= n && n <= MaxSize && 0 <= room && room <= MaxSize
// @ ensures len(r) == n
// @ ensures forall f int :: { &r[f] } 0 <= f && f < n ==> acc(&r[f]) && len(r[f]) == room
// @ ensures forall f, k int :: { &r[f][k] } 0 <= f && f < n && 0 <= k && k < room ==> acc(&r[f][k])
func rows(n, room int) (r [][]Line) {
	r = make([][]Line, n)
	i := 0
	// @ invariant 0 <= i && i <= n && len(r) == n
	// @ invariant forall f int :: { &r[f] } 0 <= f && f < n ==> acc(&r[f])
	// @ invariant forall f int :: { &r[f] } 0 <= f && f < i ==> len(r[f]) == room
	// @ invariant forall f, k int :: { &r[f][k] } 0 <= f && f < i && 0 <= k && k < room ==> acc(&r[f][k])
	// @ decreases n - i
	for i < n {
		r[i] = make([]Line, room)
		i++
	}
	return r
}

// positions allocates n rows of room positions each, for saying where lines
// came from and went.
// @ requires 0 <= n && n <= MaxSize && 0 <= room && room <= MaxSize
// @ ensures len(r) == n
// @ ensures forall f int :: { &r[f] } 0 <= f && f < n ==> acc(&r[f]) && len(r[f]) == room
// @ ensures forall f, k int :: { &r[f][k] } 0 <= f && f < n && 0 <= k && k < room ==> acc(&r[f][k])
func positions(n, room int) (r [][]int) {
	r = make([][]int, n)
	i := 0
	// @ invariant 0 <= i && i <= n && len(r) == n
	// @ invariant forall f int :: { &r[f] } 0 <= f && f < n ==> acc(&r[f])
	// @ invariant forall f int :: { &r[f] } 0 <= f && f < i ==> len(r[f]) == room
	// @ invariant forall f, k int :: { &r[f][k] } 0 <= f && f < i && 0 <= k && k < room ==> acc(&r[f][k])
	// @ decreases n - i
	for i < n {
		r[i] = make([]int, room)
		i++
	}
	return r
}
