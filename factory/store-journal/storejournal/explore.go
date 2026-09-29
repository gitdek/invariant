package storejournal

import "strconv"

// The explorer's finite universe. MaxLines is also the journal's capacity
// parameter; Write is attempted even when that capacity has been reached.
const (
	Checkouts = 2
	MaxLines  = 2
)

// State represents exactly the model's five variables. Store, Files, and Ever
// encode sets by the length of each origin's prefix of consecutive lines.
// Written and Ever belong only to the explorer; Journal has neither history.
type State struct {
	Store     [Checkouts]int
	Files     [Checkouts][Checkouts]int
	Written   [Checkouts]int
	Abandoned [Checkouts]bool
	Ever      [Checkouts]int
}

// Init reads the system's initial state, with empty environment histories.
func Init() State {
	return readSystem(State{}, New(Checkouts, MaxLines))
}

// restore gives each attempted action its own mutable system state.
func restore(s State) *Journal {
	j := New(Checkouts, MaxLines)
	for c := 0; c < Checkouts; c++ {
		j.Store[c] = s.Store[c]
		j.Abandoned[c] = s.Abandoned[c]
		for p := 0; p < Checkouts; p++ {
			j.Files[c][p] = s.Files[c][p]
		}
	}
	return j
}

// readSystem copies only system state; histories remain the explorer's own.
func readSystem(s State, j *Journal) State {
	for c := 0; c < Checkouts; c++ {
		s.Store[c] = j.Store[c]
		s.Abandoned[c] = j.Abandoned[c]
		for p := 0; p < Checkouts; p++ {
			s.Files[c][p] = j.Files[c][p]
		}
	}
	return s
}

// Try attempts every action and every argument in Next, including disabled
// actions. Only the core decides whether a system action can run.
func Try(s State, tried func(step string, args []any, next State)) {
	for c := 0; c < Checkouts; c++ {
		j := restore(s)
		ran := j.Write(c)
		next := readSystem(s, j)
		if ran {
			next.Written[c]++
			if next.Written[c] > next.Ever[c] {
				next.Ever[c] = next.Written[c]
			}
		}
		tried("Write", []any{checkoutValue(c)}, next)

		j = restore(s)
		ran = j.Rebuild(c)
		next = readSystem(s, j)
		if ran {
			for p := 0; p < Checkouts; p++ {
				if s.Files[c][p] > next.Ever[p] {
					next.Ever[p] = s.Files[c][p]
				}
			}
		}
		tried("Rebuild", []any{checkoutValue(c)}, next)

		j = restore(s)
		j.Abandon(c)
		tried("Abandon", []any{checkoutValue(c)}, readSystem(s, j))
	}
	for c := 0; c < Checkouts; c++ {
		for d := 0; d < Checkouts; d++ {
			j := restore(s)
			j.TakeIn(c, d)
			tried("TakeIn", []any{checkoutValue(c), checkoutValue(d)}, readSystem(s, j))
		}
	}
	// Done is an environment step. Both a permitted Done and a premature
	// attempt leave the state unchanged, so every state reports this attempt.
	tried("Done", []any{}, s)
}

func checkoutValue(c int) map[string]any {
	return map[string]any{"$mv": "c" + strconv.Itoa(c+1)}
}

// lineSet expands a prefix representation into the model's set of tuples.
func lineSet(prefixes [Checkouts]int) map[string]any {
	lines := make([]any, 0)
	for c := 0; c < Checkouts; c++ {
		for n := 1; n <= prefixes[c]; n++ {
			lines = append(lines, map[string]any{"$seq": []any{checkoutValue(c), n}})
		}
	}
	return map[string]any{"$set": lines}
}

// Abstract gives each variable in the spec's vocabulary and value encoding.
func Abstract(s State) map[string]any {
	files := make([]any, 0, Checkouts)
	written := make([]any, 0, Checkouts)
	abandoned := make([]any, 0, Checkouts)
	for c := 0; c < Checkouts; c++ {
		files = append(files, []any{checkoutValue(c), lineSet(s.Files[c])})
		written = append(written, []any{checkoutValue(c), s.Written[c]})
		if s.Abandoned[c] {
			abandoned = append(abandoned, checkoutValue(c))
		}
	}
	return map[string]any{
		"store":     lineSet(s.Store),
		"files":     map[string]any{"$fn": files},
		"written":   map[string]any{"$fn": written},
		"abandoned": map[string]any{"$set": abandoned},
		"ever":      lineSet(s.Ever),
	}
}
