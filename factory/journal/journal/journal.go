// +gobra

package journal

// MaxCapacity bounds each size a journal is made with, so that its index
// arithmetic stays far from overflow. It's the machine's limit, not the model's.
const MaxCapacity = 1 << 10

// MaxRoom bounds the lines one row holds, and MaxCells the cells of all rows.
const MaxRoom = MaxCapacity * MaxCapacity

const MaxCells = 1 << 42

// The kinds of line a journal holds.
const (
	OpDecide    = 1
	OpPropose   = 2
	OpRatify    = 3
	OpSupersede = 4
	OpLink      = 5
)

// The doors a decision can be.
const (
	DoorNone = 0
	DoorOne  = 1
	DoorTwo  = 2
)

// Writer is the N-th person, or the N-th agent.
type Writer struct {
	Person bool
	N      int
}

// Line is one line of a decision's journal.
type Line struct {
	Op   int
	By   Writer
	Door int
}

// Record is a line the store has recorded for decision Id.
type Record struct {
	Id   int
	Line Line
}

// Journal holds main, every checkout's branch, and the store.
//
// Each row is a sequence of lines in Cells, starting at Start[row] with room
// for Room lines, of which Lens[row] are used. Row i is main's journal of
// decision i. Row Off[c]+i holds the lines checkout c added to decision i
// since it last took main in or merged: its file is the first Base[row] lines
// of main's journal, then those.
//
// The store is Store[0:NStore]. A write in progress is Busy, on checkout PC
// and decision PId, with line PLine; its file is still to write when PFile,
// and its store record otherwise.
type Journal struct {
	Ids    int
	MaxLen int
	Room   int
	Cells  []Line
	Start  []int
	Off    []int
	Lens   []int
	Base   []int
	Store  []Record
	NStore int
	Busy   bool
	PC     int
	PId    int
	PLine  Line
	PFile  bool
}

// @ ghost
// @ requires acc(&j.Ids, _) && acc(&j.MaxLen, _) && acc(&j.Room, _) && acc(&j.Cells, _) && acc(&j.Start, _) && acc(&j.Off, _) && acc(&j.Lens, _) && acc(&j.Base, _)
// @ requires forall r int :: { &j.Start[r] } 0 <= r && r < len(j.Start) ==> acc(&j.Start[r], _)
// @ requires forall r int :: { &j.Off[r] } 0 <= r && r < len(j.Off) ==> acc(&j.Off[r], _)
// @ requires forall r int :: { &j.Lens[r] } 0 <= r && r < len(j.Lens) ==> acc(&j.Lens[r], _)
// @ requires forall r int :: { &j.Base[r] } 0 <= r && r < len(j.Base) ==> acc(&j.Base[r], _)
// @ decreases
// @ pure func (j *Journal) Shape() bool {
// @ 	return 0 < j.Ids && j.Ids <= MaxCapacity && 0 < j.MaxLen && j.MaxLen <= j.Room && j.Room <= MaxRoom &&
// @ 		len(j.Cells) <= MaxCells && len(j.Start) == len(j.Lens) && len(j.Base) == len(j.Lens) && j.Ids <= len(j.Lens) &&
// @ 		(forall r int :: { j.Start[r] } 0 <= r && r < len(j.Start) ==> 0 <= j.Start[r] && j.Start[r]+j.Room <= len(j.Cells)) &&
// @ 		(forall c int :: { j.Off[c] } 0 <= c && c < len(j.Off) ==> j.Ids <= j.Off[c] && j.Off[c]+j.Ids <= len(j.Lens)) &&
// @ 		(forall r int :: { j.Lens[r] } 0 <= r && r < len(j.Lens) ==> 0 <= j.Lens[r] && j.Lens[r] <= j.Room) &&
// @ 		(forall r int :: { j.Base[r] } 0 <= r && r < len(j.Base) ==> 0 <= j.Base[r] && j.Base[r] <= j.Room)
// @ }

// @ ghost
// @ requires acc(&j.Ids, _) && acc(&j.Off, _)
// @ requires acc(&j.Store, _) && acc(&j.NStore, _) && acc(&j.Busy, _) && acc(&j.PC, _) && acc(&j.PId, _) && acc(&j.PLine, _) && acc(&j.PFile, _)
// @ requires forall k int :: { &j.Store[k] } 0 <= k && k < len(j.Store) ==> acc(&j.Store[k], _)
// @ decreases
// @ pure func (j *Journal) StoreOk() bool {
// @ 	return 0 <= j.NStore && j.NStore <= len(j.Store) &&
// @ 		(forall k int :: { j.Store[k] } 0 <= k && k < j.NStore ==> 0 <= j.Store[k].Id && j.Store[k].Id < j.Ids) &&
// @ 		(j.Busy ==> 0 <= j.PC && j.PC < len(j.Off) && 0 <= j.PId && j.PId < j.Ids)
// @ }

// New makes a journal of ids decisions, for main and the given number of
// checkouts. A checkout's file holds at most maxLen lines, and the store
// maxWrites records. Main's journal of a decision has room for maxLen lines
// from each checkout.
// @ requires 0 < ids && ids <= MaxCapacity && 0 < checkouts && checkouts <= MaxCapacity
// @ requires 0 < maxLen && maxLen <= MaxCapacity && 0 <= maxWrites && maxWrites <= MaxCapacity
// @ ensures acc(&j.Ids) && acc(&j.MaxLen) && acc(&j.Room) && acc(&j.Cells) && acc(&j.Start) && acc(&j.Off) && acc(&j.Lens) && acc(&j.Base)
// @ ensures forall r int :: { &j.Start[r] } 0 <= r && r < len(j.Start) ==> acc(&j.Start[r])
// @ ensures forall r int :: { &j.Off[r] } 0 <= r && r < len(j.Off) ==> acc(&j.Off[r])
// @ ensures forall r int :: { &j.Lens[r] } 0 <= r && r < len(j.Lens) ==> acc(&j.Lens[r])
// @ ensures forall r int :: { &j.Base[r] } 0 <= r && r < len(j.Base) ==> acc(&j.Base[r])
// @ ensures forall k int :: { &j.Cells[k] } 0 <= k && k < len(j.Cells) ==> acc(&j.Cells[k])
// @ ensures acc(&j.Store) && acc(&j.NStore) && acc(&j.Busy) && acc(&j.PC) && acc(&j.PId) && acc(&j.PLine) && acc(&j.PFile)
// @ ensures forall k int :: { &j.Store[k] } 0 <= k && k < len(j.Store) ==> acc(&j.Store[k])
// @ ensures j.Shape() && j.StoreOk()
// @ ensures j.Ids == ids && j.MaxLen == maxLen && len(j.Off) == checkouts && len(j.Store) == maxWrites
// @ ensures j.NStore == 0 && !j.Busy
// @ ensures forall r int :: { j.Lens[r] } 0 <= r && r < len(j.Lens) ==> j.Lens[r] == 0
// @ ensures forall r int :: { j.Base[r] } 0 <= r && r < len(j.Base) ==> j.Base[r] == 0
func New(ids, checkouts, maxLen, maxWrites int) (j *Journal) {
	off := make([]int, checkouts)
	rows := ids
	c := 0
	// @ invariant 0 < ids && ids <= MaxCapacity && 0 < checkouts && checkouts <= MaxCapacity
	// @ invariant 0 <= c && c <= checkouts && len(off) == checkouts
	// @ invariant forall x int :: { &off[x] } 0 <= x && x < len(off) ==> acc(&off[x])
	// @ invariant ids <= rows && rows <= ids + c*MaxCapacity
	// @ invariant forall x int :: { off[x] } 0 <= x && x < c ==> ids <= off[x] && off[x]+ids <= rows
	for c < checkouts {
		off[c] = rows
		rows = rows + ids
		c = c + 1
	}
	room := 0
	c = 0
	// @ invariant 0 < checkouts && checkouts <= MaxCapacity && 0 < maxLen && maxLen <= MaxCapacity
	// @ invariant 0 <= c && c <= checkouts
	// @ invariant 0 <= room && room <= c*MaxCapacity && (c > 0 ==> maxLen <= room)
	for c < checkouts {
		room = room + maxLen
		c = c + 1
	}
	start := make([]int, rows)
	pos := 0
	r := 0
	// @ invariant 0 < room && room <= MaxRoom && rows <= MaxCapacity + MaxCapacity*MaxCapacity
	// @ invariant 0 <= r && r <= rows && len(start) == rows
	// @ invariant forall x int :: { &start[x] } 0 <= x && x < len(start) ==> acc(&start[x])
	// @ invariant 0 <= pos && pos <= r*MaxRoom
	// @ invariant forall x int :: { start[x] } 0 <= x && x < r ==> 0 <= start[x] && start[x]+room <= pos
	for r < rows {
		start[r] = pos
		pos = pos + room
		r = r + 1
	}
	j = &Journal{Ids: ids, MaxLen: maxLen, Room: room, Cells: make([]Line, pos), Start: start, Off: off,
		Lens: make([]int, rows), Base: make([]int, rows), Store: make([]Record, maxWrites)}
	return j
}

// DecideLine is whether l is a line w may decide with: a person decides
// either door, and an agent decides a two-way door or proposes a one-way door.
// @ decreases
// @ pure
func DecideLine(w Writer, l Line) bool {
	return l.By == w && ((w.Person && l.Op == OpDecide && (l.Door == DoorOne || l.Door == DoorTwo)) ||
		(!w.Person && ((l.Op == OpDecide && l.Door == DoorTwo) || (l.Op == OpPropose && l.Door == DoorOne))))
}

// @ decreases
// @ pure
func isRS(op int) bool {
	return op == OpRatify || op == OpSupersede
}

// okLine is whether l may stand where it is: only a journal's first line
// records its decision, and no ratify follows a ratify or supersede.
// @ decreases
// @ pure
func okLine(l Line, first, seen bool) bool {
	return first == (l.Op == OpDecide || l.Op == OpPropose) && !(seen && l.Op == OpRatify)
}

// row is checkout c's row of decision i.
// @ requires acc(&j.Ids, _) && acc(&j.MaxLen, _) && acc(&j.Room, _) && acc(&j.Cells, _) && acc(&j.Start, _) && acc(&j.Off, _) && acc(&j.Lens, _) && acc(&j.Base, _)
// @ requires forall r int :: { &j.Start[r] } 0 <= r && r < len(j.Start) ==> acc(&j.Start[r], _)
// @ requires forall r int :: { &j.Off[r] } 0 <= r && r < len(j.Off) ==> acc(&j.Off[r], _)
// @ requires forall r int :: { &j.Lens[r] } 0 <= r && r < len(j.Lens) ==> acc(&j.Lens[r], _)
// @ requires forall r int :: { &j.Base[r] } 0 <= r && r < len(j.Base) ==> acc(&j.Base[r], _)
// @ requires j.Shape() && 0 <= c && c < len(j.Off) && 0 <= i && i < j.Ids
// @ ensures j.Ids <= r && r < len(j.Lens)
// @ decreases
// @ pure
func (j *Journal) row(c, i int) (r int) {
	return j.Off[c] + i
}

// fileLen is the length of checkout c's file of decision i.
// @ requires acc(&j.Ids, _) && acc(&j.MaxLen, _) && acc(&j.Room, _) && acc(&j.Cells, _) && acc(&j.Start, _) && acc(&j.Off, _) && acc(&j.Lens, _) && acc(&j.Base, _)
// @ requires forall r int :: { &j.Start[r] } 0 <= r && r < len(j.Start) ==> acc(&j.Start[r], _)
// @ requires forall r int :: { &j.Off[r] } 0 <= r && r < len(j.Off) ==> acc(&j.Off[r], _)
// @ requires forall r int :: { &j.Lens[r] } 0 <= r && r < len(j.Lens) ==> acc(&j.Lens[r], _)
// @ requires forall r int :: { &j.Base[r] } 0 <= r && r < len(j.Base) ==> acc(&j.Base[r], _)
// @ requires j.Shape() && 0 <= c && c < len(j.Off) && 0 <= i && i < j.Ids
// @ ensures 0 <= n
// @ decreases
// @ pure
func (j *Journal) fileLen(c, i int) (n int) {
	return j.Base[j.row(c, i)] + j.Lens[j.row(c, i)]
}

// lineAt is line k of row r.
// @ requires acc(&j.Ids, _) && acc(&j.MaxLen, _) && acc(&j.Room, _) && acc(&j.Cells, _) && acc(&j.Start, _) && acc(&j.Off, _) && acc(&j.Lens, _) && acc(&j.Base, _)
// @ requires forall r int :: { &j.Start[r] } 0 <= r && r < len(j.Start) ==> acc(&j.Start[r], _)
// @ requires forall r int :: { &j.Off[r] } 0 <= r && r < len(j.Off) ==> acc(&j.Off[r], _)
// @ requires forall r int :: { &j.Lens[r] } 0 <= r && r < len(j.Lens) ==> acc(&j.Lens[r], _)
// @ requires forall r int :: { &j.Base[r] } 0 <= r && r < len(j.Base) ==> acc(&j.Base[r], _)
// @ requires forall k int :: { &j.Cells[k] } 0 <= k && k < len(j.Cells) ==> acc(&j.Cells[k], _)
// @ requires j.Shape() && 0 <= r && r < len(j.Start) && 0 <= k && k < j.Room
// @ decreases
// @ pure
func (j *Journal) lineAt(r, k int) Line {
	return j.Cells[j.Start[r]+k]
}

// hasNL is whether row r's first n lines hold one that isn't a link.
// @ requires acc(&j.Ids, _) && acc(&j.MaxLen, _) && acc(&j.Room, _) && acc(&j.Cells, _) && acc(&j.Start, _) && acc(&j.Off, _) && acc(&j.Lens, _) && acc(&j.Base, _)
// @ requires forall r int :: { &j.Start[r] } 0 <= r && r < len(j.Start) ==> acc(&j.Start[r], _)
// @ requires forall r int :: { &j.Off[r] } 0 <= r && r < len(j.Off) ==> acc(&j.Off[r], _)
// @ requires forall r int :: { &j.Lens[r] } 0 <= r && r < len(j.Lens) ==> acc(&j.Lens[r], _)
// @ requires forall r int :: { &j.Base[r] } 0 <= r && r < len(j.Base) ==> acc(&j.Base[r], _)
// @ requires forall k int :: { &j.Cells[k] } 0 <= k && k < len(j.Cells) ==> acc(&j.Cells[k], _)
// @ requires j.Shape() && 0 <= r && r < len(j.Start) && 0 <= n && n <= j.Room
// @ decreases n
// @ pure
func (j *Journal) hasNL(r, n int) bool {
	return n > 0 && (j.lineAt(r, n-1).Op != OpLink || j.hasNL(r, n-1))
}

// lastRS is whether the last line of row r's first n that isn't a link is a
// ratify or a supersede.
// @ requires acc(&j.Ids, _) && acc(&j.MaxLen, _) && acc(&j.Room, _) && acc(&j.Cells, _) && acc(&j.Start, _) && acc(&j.Off, _) && acc(&j.Lens, _) && acc(&j.Base, _)
// @ requires forall r int :: { &j.Start[r] } 0 <= r && r < len(j.Start) ==> acc(&j.Start[r], _)
// @ requires forall r int :: { &j.Off[r] } 0 <= r && r < len(j.Off) ==> acc(&j.Off[r], _)
// @ requires forall r int :: { &j.Lens[r] } 0 <= r && r < len(j.Lens) ==> acc(&j.Lens[r], _)
// @ requires forall r int :: { &j.Base[r] } 0 <= r && r < len(j.Base) ==> acc(&j.Base[r], _)
// @ requires forall k int :: { &j.Cells[k] } 0 <= k && k < len(j.Cells) ==> acc(&j.Cells[k], _)
// @ requires j.Shape() && 0 <= r && r < len(j.Start) && 0 <= n && n <= j.Room
// @ decreases n
// @ pure
func (j *Journal) lastRS(r, n int) bool {
	return n > 0 && (isRS(j.lineAt(r, n-1).Op) || (j.lineAt(r, n-1).Op == OpLink && j.lastRS(r, n-1)))
}

// anyRS is whether row r's first n lines hold a ratify or a supersede.
// @ requires acc(&j.Ids, _) && acc(&j.MaxLen, _) && acc(&j.Room, _) && acc(&j.Cells, _) && acc(&j.Start, _) && acc(&j.Off, _) && acc(&j.Lens, _) && acc(&j.Base, _)
// @ requires forall r int :: { &j.Start[r] } 0 <= r && r < len(j.Start) ==> acc(&j.Start[r], _)
// @ requires forall r int :: { &j.Off[r] } 0 <= r && r < len(j.Off) ==> acc(&j.Off[r], _)
// @ requires forall r int :: { &j.Lens[r] } 0 <= r && r < len(j.Lens) ==> acc(&j.Lens[r], _)
// @ requires forall r int :: { &j.Base[r] } 0 <= r && r < len(j.Base) ==> acc(&j.Base[r], _)
// @ requires forall k int :: { &j.Cells[k] } 0 <= k && k < len(j.Cells) ==> acc(&j.Cells[k], _)
// @ requires j.Shape() && 0 <= r && r < len(j.Start) && 0 <= n && n <= j.Room
// @ decreases n
// @ pure
func (j *Journal) anyRS(r, n int) bool {
	return n > 0 && (isRS(j.lineAt(r, n-1).Op) || j.anyRS(r, n-1))
}

// wfSeg is whether lines k to n of row r may stand where they are, when the
// row's first line is its journal's first if head, and a ratify or supersede
// comes before them if seen.
// @ requires acc(&j.Ids, _) && acc(&j.MaxLen, _) && acc(&j.Room, _) && acc(&j.Cells, _) && acc(&j.Start, _) && acc(&j.Off, _) && acc(&j.Lens, _) && acc(&j.Base, _)
// @ requires forall r int :: { &j.Start[r] } 0 <= r && r < len(j.Start) ==> acc(&j.Start[r], _)
// @ requires forall r int :: { &j.Off[r] } 0 <= r && r < len(j.Off) ==> acc(&j.Off[r], _)
// @ requires forall r int :: { &j.Lens[r] } 0 <= r && r < len(j.Lens) ==> acc(&j.Lens[r], _)
// @ requires forall r int :: { &j.Base[r] } 0 <= r && r < len(j.Base) ==> acc(&j.Base[r], _)
// @ requires forall k int :: { &j.Cells[k] } 0 <= k && k < len(j.Cells) ==> acc(&j.Cells[k], _)
// @ requires j.Shape() && 0 <= r && r < len(j.Start) && 0 <= k && k <= n && n <= j.Room
// @ decreases n - k
// @ pure
func (j *Journal) wfSeg(r, k, n int, head, seen bool) bool {
	return k >= n || (okLine(j.lineAt(r, k), head && k == 0, seen) &&
		j.wfSeg(r, k+1, n, head, seen || isRS(j.lineAt(r, k).Op)))
}

// takenWF is whether decision i's journal stays well formed when checkout c
// takes main in: main's lines, then the checkout's new ones.
// @ requires acc(&j.Ids, _) && acc(&j.MaxLen, _) && acc(&j.Room, _) && acc(&j.Cells, _) && acc(&j.Start, _) && acc(&j.Off, _) && acc(&j.Lens, _) && acc(&j.Base, _)
// @ requires forall r int :: { &j.Start[r] } 0 <= r && r < len(j.Start) ==> acc(&j.Start[r], _)
// @ requires forall r int :: { &j.Off[r] } 0 <= r && r < len(j.Off) ==> acc(&j.Off[r], _)
// @ requires forall r int :: { &j.Lens[r] } 0 <= r && r < len(j.Lens) ==> acc(&j.Lens[r], _)
// @ requires forall r int :: { &j.Base[r] } 0 <= r && r < len(j.Base) ==> acc(&j.Base[r], _)
// @ requires forall k int :: { &j.Cells[k] } 0 <= k && k < len(j.Cells) ==> acc(&j.Cells[k], _)
// @ requires j.Shape() && 0 <= c && c < len(j.Off) && 0 <= i && i < j.Ids
// @ decreases
// @ pure
func (j *Journal) takenWF(c, i int) bool {
	return j.wfSeg(i, 0, j.Lens[i], true, false) &&
		j.wfSeg(j.row(c, i), 0, j.Lens[j.row(c, i)], j.Lens[i] == 0, j.anyRS(i, j.Lens[i]))
}

// closed is whether checkout c's file of decision i is ratified or
// superseded: its last line that isn't a link is a ratify or a supersede.
// @ requires acc(&j.Ids, _) && acc(&j.MaxLen, _) && acc(&j.Room, _) && acc(&j.Cells, _) && acc(&j.Start, _) && acc(&j.Off, _) && acc(&j.Lens, _) && acc(&j.Base, _)
// @ requires forall r int :: { &j.Start[r] } 0 <= r && r < len(j.Start) ==> acc(&j.Start[r], _)
// @ requires forall r int :: { &j.Off[r] } 0 <= r && r < len(j.Off) ==> acc(&j.Off[r], _)
// @ requires forall r int :: { &j.Lens[r] } 0 <= r && r < len(j.Lens) ==> acc(&j.Lens[r], _)
// @ requires forall r int :: { &j.Base[r] } 0 <= r && r < len(j.Base) ==> acc(&j.Base[r], _)
// @ requires forall k int :: { &j.Cells[k] } 0 <= k && k < len(j.Cells) ==> acc(&j.Cells[k], _)
// @ requires j.Shape() && 0 <= c && c < len(j.Off) && 0 <= i && i < j.Ids
// @ decreases
// @ pure
func (j *Journal) closed(c, i int) bool {
	return j.lastRS(j.row(c, i), j.Lens[j.row(c, i)]) ||
		(!j.hasNL(j.row(c, i), j.Lens[j.row(c, i)]) && j.lastRS(i, j.Base[j.row(c, i)]))
}

// prefixFrom is whether main's lines k to m of row mr are the lines from k-b
// of checkout row nr.
// @ requires acc(&j.Ids, _) && acc(&j.MaxLen, _) && acc(&j.Room, _) && acc(&j.Cells, _) && acc(&j.Start, _) && acc(&j.Off, _) && acc(&j.Lens, _) && acc(&j.Base, _)
// @ requires forall r int :: { &j.Start[r] } 0 <= r && r < len(j.Start) ==> acc(&j.Start[r], _)
// @ requires forall r int :: { &j.Off[r] } 0 <= r && r < len(j.Off) ==> acc(&j.Off[r], _)
// @ requires forall r int :: { &j.Lens[r] } 0 <= r && r < len(j.Lens) ==> acc(&j.Lens[r], _)
// @ requires forall r int :: { &j.Base[r] } 0 <= r && r < len(j.Base) ==> acc(&j.Base[r], _)
// @ requires forall k int :: { &j.Cells[k] } 0 <= k && k < len(j.Cells) ==> acc(&j.Cells[k], _)
// @ requires j.Shape() && 0 <= mr && mr < len(j.Start) && 0 <= nr && nr < len(j.Start)
// @ requires 0 <= b && b <= k && k <= m && m <= j.Room
// @ decreases m - k
// @ pure
func (j *Journal) prefixFrom(mr, nr, k, m, b int) bool {
	return k >= m || (j.lineAt(mr, k) == j.lineAt(nr, k-b) && j.prefixFrom(mr, nr, k+1, m, b))
}

// prefixOk is whether main's journal of decision i is the start of checkout
// c's file, and the file fits main's room.
// @ requires acc(&j.Ids, _) && acc(&j.MaxLen, _) && acc(&j.Room, _) && acc(&j.Cells, _) && acc(&j.Start, _) && acc(&j.Off, _) && acc(&j.Lens, _) && acc(&j.Base, _)
// @ requires forall r int :: { &j.Start[r] } 0 <= r && r < len(j.Start) ==> acc(&j.Start[r], _)
// @ requires forall r int :: { &j.Off[r] } 0 <= r && r < len(j.Off) ==> acc(&j.Off[r], _)
// @ requires forall r int :: { &j.Lens[r] } 0 <= r && r < len(j.Lens) ==> acc(&j.Lens[r], _)
// @ requires forall r int :: { &j.Base[r] } 0 <= r && r < len(j.Base) ==> acc(&j.Base[r], _)
// @ requires forall k int :: { &j.Cells[k] } 0 <= k && k < len(j.Cells) ==> acc(&j.Cells[k], _)
// @ requires j.Shape() && 0 <= c && c < len(j.Off) && 0 <= i && i < j.Ids
// @ decreases
// @ pure
func (j *Journal) prefixOk(c, i int) bool {
	return j.Base[j.row(c, i)] <= j.Lens[i] && j.Lens[i] <= j.fileLen(c, i) && j.fileLen(c, i) <= j.Room &&
		j.prefixFrom(i, j.row(c, i), j.Base[j.row(c, i)], j.Lens[i], j.Base[j.row(c, i)])
}

// mergeOk is whether prefixOk holds for decisions i and on.
// @ requires acc(&j.Ids, _) && acc(&j.MaxLen, _) && acc(&j.Room, _) && acc(&j.Cells, _) && acc(&j.Start, _) && acc(&j.Off, _) && acc(&j.Lens, _) && acc(&j.Base, _)
// @ requires forall r int :: { &j.Start[r] } 0 <= r && r < len(j.Start) ==> acc(&j.Start[r], _)
// @ requires forall r int :: { &j.Off[r] } 0 <= r && r < len(j.Off) ==> acc(&j.Off[r], _)
// @ requires forall r int :: { &j.Lens[r] } 0 <= r && r < len(j.Lens) ==> acc(&j.Lens[r], _)
// @ requires forall r int :: { &j.Base[r] } 0 <= r && r < len(j.Base) ==> acc(&j.Base[r], _)
// @ requires forall k int :: { &j.Cells[k] } 0 <= k && k < len(j.Cells) ==> acc(&j.Cells[k], _)
// @ requires j.Shape() && 0 <= c && c < len(j.Off) && 0 <= i && i <= j.Ids
// @ decreases j.Ids - i
// @ pure
func (j *Journal) mergeOk(c, i int) bool {
	return i >= j.Ids || (j.prefixOk(c, i) && j.mergeOk(c, i+1))
}

// anyDiff is whether main's journal of a decision from i on differs in
// length from checkout c's file.
// @ requires acc(&j.Ids, _) && acc(&j.MaxLen, _) && acc(&j.Room, _) && acc(&j.Cells, _) && acc(&j.Start, _) && acc(&j.Off, _) && acc(&j.Lens, _) && acc(&j.Base, _)
// @ requires forall r int :: { &j.Start[r] } 0 <= r && r < len(j.Start) ==> acc(&j.Start[r], _)
// @ requires forall r int :: { &j.Off[r] } 0 <= r && r < len(j.Off) ==> acc(&j.Off[r], _)
// @ requires forall r int :: { &j.Lens[r] } 0 <= r && r < len(j.Lens) ==> acc(&j.Lens[r], _)
// @ requires forall r int :: { &j.Base[r] } 0 <= r && r < len(j.Base) ==> acc(&j.Base[r], _)
// @ requires j.Shape() && 0 <= c && c < len(j.Off) && 0 <= i && i <= j.Ids
// @ decreases j.Ids - i
// @ pure
func (j *Journal) anyDiff(c, i int) bool {
	return i < j.Ids && (j.Lens[i] != j.fileLen(c, i) || j.anyDiff(c, i+1))
}

// behind is whether main has lines from decision i on that checkout c hasn't
// taken in.
// @ requires acc(&j.Ids, _) && acc(&j.MaxLen, _) && acc(&j.Room, _) && acc(&j.Cells, _) && acc(&j.Start, _) && acc(&j.Off, _) && acc(&j.Lens, _) && acc(&j.Base, _)
// @ requires forall r int :: { &j.Start[r] } 0 <= r && r < len(j.Start) ==> acc(&j.Start[r], _)
// @ requires forall r int :: { &j.Off[r] } 0 <= r && r < len(j.Off) ==> acc(&j.Off[r], _)
// @ requires forall r int :: { &j.Lens[r] } 0 <= r && r < len(j.Lens) ==> acc(&j.Lens[r], _)
// @ requires forall r int :: { &j.Base[r] } 0 <= r && r < len(j.Base) ==> acc(&j.Base[r], _)
// @ requires j.Shape() && 0 <= c && c < len(j.Off) && 0 <= i && i <= j.Ids
// @ decreases j.Ids - i
// @ pure
func (j *Journal) behind(c, i int) bool {
	return i < j.Ids && (j.Base[j.row(c, i)] != j.Lens[i] || j.behind(c, i+1))
}

// syncOk is whether takenWF holds for decisions i and on.
// @ requires acc(&j.Ids, _) && acc(&j.MaxLen, _) && acc(&j.Room, _) && acc(&j.Cells, _) && acc(&j.Start, _) && acc(&j.Off, _) && acc(&j.Lens, _) && acc(&j.Base, _)
// @ requires forall r int :: { &j.Start[r] } 0 <= r && r < len(j.Start) ==> acc(&j.Start[r], _)
// @ requires forall r int :: { &j.Off[r] } 0 <= r && r < len(j.Off) ==> acc(&j.Off[r], _)
// @ requires forall r int :: { &j.Lens[r] } 0 <= r && r < len(j.Lens) ==> acc(&j.Lens[r], _)
// @ requires forall r int :: { &j.Base[r] } 0 <= r && r < len(j.Base) ==> acc(&j.Base[r], _)
// @ requires forall k int :: { &j.Cells[k] } 0 <= k && k < len(j.Cells) ==> acc(&j.Cells[k], _)
// @ requires j.Shape() && 0 <= c && c < len(j.Off) && 0 <= i && i <= j.Ids
// @ decreases j.Ids - i
// @ pure
func (j *Journal) syncOk(c, i int) bool {
	return i >= j.Ids || (j.takenWF(c, i) && j.syncOk(c, i+1))
}

// storeHasFrom is whether a record from the k-th on is of decision id.
// @ requires acc(&j.Ids, _) && acc(&j.Off, _)
// @ requires acc(&j.Store, _) && acc(&j.NStore, _) && acc(&j.Busy, _) && acc(&j.PC, _) && acc(&j.PId, _) && acc(&j.PLine, _) && acc(&j.PFile, _)
// @ requires forall k int :: { &j.Store[k] } 0 <= k && k < len(j.Store) ==> acc(&j.Store[k], _)
// @ requires j.StoreOk() && 0 <= k && k <= j.NStore
// @ decreases j.NStore - k
// @ pure
func (j *Journal) storeHasFrom(k, id int) bool {
	return k < j.NStore && (j.Store[k].Id == id || j.storeHasFrom(k+1, id))
}

// Topped is whether checkout c has no ID left to take: the store has
// journaled the last, or the checkout holds it.
// @ requires acc(&j.Ids, _) && acc(&j.MaxLen, _) && acc(&j.Room, _) && acc(&j.Cells, _) && acc(&j.Start, _) && acc(&j.Off, _) && acc(&j.Lens, _) && acc(&j.Base, _)
// @ requires forall r int :: { &j.Start[r] } 0 <= r && r < len(j.Start) ==> acc(&j.Start[r], _)
// @ requires forall r int :: { &j.Off[r] } 0 <= r && r < len(j.Off) ==> acc(&j.Off[r], _)
// @ requires forall r int :: { &j.Lens[r] } 0 <= r && r < len(j.Lens) ==> acc(&j.Lens[r], _)
// @ requires forall r int :: { &j.Base[r] } 0 <= r && r < len(j.Base) ==> acc(&j.Base[r], _)
// @ requires acc(&j.Store, _) && acc(&j.NStore, _) && acc(&j.Busy, _) && acc(&j.PC, _) && acc(&j.PId, _) && acc(&j.PLine, _) && acc(&j.PFile, _)
// @ requires forall k int :: { &j.Store[k] } 0 <= k && k < len(j.Store) ==> acc(&j.Store[k], _)
// @ requires j.Shape() && j.StoreOk() && 0 <= c && c < len(j.Off)
// @ decreases
// @ pure
func (j *Journal) Topped(c int) bool {
	return j.storeHasFrom(0, j.Ids-1) || j.fileLen(c, j.Ids-1) > 0
}
