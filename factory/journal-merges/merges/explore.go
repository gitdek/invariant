package merges

import "strconv"

// The explorer's finite universe, the model's bounds. MaxLines is also the
// core's Capacity, and Add is tried even when a branch has added that many.
const (
	Branches = 2
	Files    = 2
	MaxLines = 2
)

// A file holds at most every line every branch adds, since a line is in a
// file only once. The core's files have room for two such, as taking main in
// needs, so at these bounds it never refuses for room.
const (
	Held = Branches * MaxLines
	Room = 2 * Held
)

// File is one journal file as the model sees it: its first Len lines, in
// order, and no line past them.
type File struct {
	Lines [Held]Line
	Len   int
}

// State has exactly one field for each of the model's variables. Main, Branch
// and Added are the core's; Ever, main's journal as the last merge left it,
// belongs only to the explorer, since the core keeps no history.
type State struct {
	Main   [Files]File
	Branch [Branches][Files]File
	Added  [Branches]int
	Ever   [Files]File
}

// Init reads the constructors' state, with an empty history.
func Init() (s State) {
	core := New(Files, Room, MaxLines)
	s.Main = journal(s.Main, core.Lines, core.Len)
	for b := 0; b < Branches; b++ {
		br := NewBranch(b+1, Files, Room)
		s.Branch[b] = journal(s.Branch[b], br.Lines, br.Len)
		s.Added[b] = br.Added
	}
	return s
}

// restore gives one attempt its own main and branch b, as s has them.
func restore(s State, b int) (*Core, *Branch) {
	core := New(Files, Room, MaxLines)
	br := NewBranch(b+1, Files, Room)
	for f := 0; f < Files; f++ {
		core.Len[f] = s.Main[f].Len
		copy(core.Lines[f], s.Main[f].Lines[:s.Main[f].Len])
		br.Len[f] = s.Branch[b][f].Len
		copy(br.Lines[f], s.Branch[b][f].Lines[:s.Branch[b][f].Len])
	}
	br.Added = s.Added[b]
	return core, br
}

// journal reads the core's files into the explorer's, with nothing past a
// file's lines, so equal journals are equal states. A file can't hold more
// than Held lines at these bounds, and one that did would stop the
// exploration here rather than lose a line.
func journal(files [Files]File, lines [][]Line, n []int) [Files]File {
	for f := 0; f < Files; f++ {
		if n[f] > Held {
			panic("a journal file holds more lines than every branch adds")
		}
		files[f] = File{Len: n[f]}
		copy(files[f].Lines[:], lines[f][:n[f]])
	}
	return files
}

// read reads back main and branch b after an attempt, leaving the other
// branches and the history as they were.
func read(s State, b int, core *Core, br *Branch) State {
	s.Main = journal(s.Main, core.Lines, core.Len)
	s.Branch[b] = journal(s.Branch[b], br.Lines, br.Len)
	s.Added[b] = br.Added
	return s
}

// Try attempts every step of Next with every argument, refusals included.
// Only the core decides whether Add, Merge or TakeMainIn runs.
func Try(s State, tried func(step string, args []any, next State)) {
	for b := 0; b < Branches; b++ {
		for f := 0; f < Files; f++ {
			core, br := restore(s, b)
			core.Add(br, f)
			tried("Add", []any{branchValue(b), fileValue(f)}, read(s, b, core, br))
		}

		core, br := restore(s, b)
		merged := core.Merge(br)
		next := read(s, b, core, br)
		if merged {
			next.Ever = next.Main
		}
		tried("Merge", []any{branchValue(b)}, next)

		core, br = restore(s, b)
		core.TakeMainIn(br)
		tried("TakeMainIn", []any{branchValue(b)}, read(s, b, core, br))
	}
	// Done is an environment step that changes nothing, whether or not every
	// branch has added all its lines and caught up with main.
	tried("Done", []any{}, s)
}

func branchValue(b int) map[string]any {
	return map[string]any{"$mv": "b" + strconv.Itoa(b+1)}
}

func fileValue(f int) map[string]any {
	return map[string]any{"$mv": "f" + strconv.Itoa(f+1)}
}

// lines is a file as the model's sequence of lines, each a branch and its
// number.
func lines(file File) map[string]any {
	seq := make([]any, 0, file.Len)
	for _, l := range file.Lines[:file.Len] {
		seq = append(seq, map[string]any{"$seq": []any{branchValue(l.By - 1), l.N}})
	}
	return map[string]any{"$seq": seq}
}

// journalValue is a journal as the model's function from files to their
// lines.
func journalValue(files [Files]File) map[string]any {
	fn := make([]any, 0, Files)
	for f := 0; f < Files; f++ {
		fn = append(fn, []any{fileValue(f), lines(files[f])})
	}
	return map[string]any{"$fn": fn}
}

// Abstract gives each variable its TLA+ name and value.
func Abstract(s State) map[string]any {
	branch := make([]any, 0, Branches)
	added := make([]any, 0, Branches)
	for b := 0; b < Branches; b++ {
		branch = append(branch, []any{branchValue(b), journalValue(s.Branch[b])})
		added = append(added, []any{branchValue(b), s.Added[b]})
	}
	return map[string]any{
		"main":   journalValue(s.Main),
		"branch": map[string]any{"$fn": branch},
		"added":  map[string]any{"$fn": added},
		"ever":   journalValue(s.Ever),
	}
}
