package state

import "strconv"

// The model checker's bounds belong only to the explorer. MaxLines is a
// constructor parameter, so full journals still have every operation tried.
const (
	People   = 1
	Agents   = 1
	MaxLines = 3
)

// State contains exactly the model's journal and cached decision state.
// N encodes the length of Lines; unused array entries remain zero.
type State struct {
	Lines  [MaxLines]Line
	N      int
	Status Status
	Door   Door
	Who    Writer
}

func Init() State {
	return snapshot(New(MaxLines))
}

func restore(s State) *Journal {
	j := New(MaxLines)
	copy(j.lines, s.Lines[:])
	j.n = s.N
	j.status = s.Status
	j.door = s.Door
	j.who = s.Who
	return j
}

func snapshot(j *Journal) State {
	s := State{N: j.n, Status: j.status, Door: j.door, Who: j.who}
	copy(s.Lines[:], j.lines)
	return s
}

// Try enumerates Next's arguments independently of the system's state.
// Refusal is observed by reading the unchanged core, never by predicting
// whether an operation should run.
func Try(s State, tried func(step string, args []any, next State)) {
	for kind := Person; kind <= Agent; kind++ {
		count := People
		if kind == Agent {
			count = Agents
		}
		for id := 1; id <= count; id++ {
			w := Writer{Kind: kind, ID: id}
			for d := OneWay; d <= TwoWay; d++ {
				for status := None; status <= Superseded; status++ {
					j := restore(s)
					j.Decide(w, d, status)
					tried("Decide", []any{abstractWriter(w), abstractDoor(d), abstractStatus(status)}, snapshot(j))
				}
			}
			j := restore(s)
			j.Supersede(w)
			tried("Supersede", []any{abstractWriter(w)}, snapshot(j))
		}
	}
	for id := 1; id <= People; id++ {
		w := Writer{Kind: Person, ID: id}
		j := restore(s)
		j.Ratify(w)
		tried("Ratify", []any{abstractWriter(w)}, snapshot(j))
	}
	// Done has no system effect. When its guard is false it is refused,
	// which is the same unchanged state as when the guard holds.
	tried("Done", []any{}, s)
}

func abstractWriter(w Writer) any {
	if w == (Writer{}) {
		return "-"
	}
	name := "alice"
	if w.Kind == Agent {
		name = "agent"
	}
	if w.ID > 1 {
		name += strconv.Itoa(w.ID)
	}
	return map[string]any{"$mv": name}
}

func abstractDoor(d Door) string {
	switch d {
	case NoDoor:
		return "-"
	case OneWay:
		return "one-way"
	case TwoWay:
		return "two-way"
	default:
		panic("invalid door in explorer")
	}
}

func abstractStatus(s Status) string {
	switch s {
	case None:
		return "none"
	case Proposed:
		return "proposed"
	case Decided:
		return "decided"
	case Ratified:
		return "ratified"
	case Superseded:
		return "superseded"
	case NoStatus:
		return "-"
	default:
		panic("invalid status in explorer")
	}
}

func abstractLine(l Line) any {
	op := ""
	switch l.Op {
	case DecideOp:
		op = "decide"
	case RatifyOp:
		op = "ratify"
	case SupersedeOp:
		op = "supersede"
	default:
		panic("invalid operation in explorer")
	}
	return map[string]any{
		"op": op, "by": abstractWriter(l.By),
		"door": abstractDoor(l.Door), "status": abstractStatus(l.Status),
	}
}

func Abstract(s State) map[string]any {
	lines := make([]any, s.N)
	for i := 0; i < s.N; i++ {
		lines[i] = abstractLine(s.Lines[i])
	}
	return map[string]any{
		"lines":  map[string]any{"$seq": lines},
		"status": abstractStatus(s.Status),
		"door":   abstractDoor(s.Door),
		"who":    abstractWriter(s.Who),
	}
}
