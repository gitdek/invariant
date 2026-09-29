package ids

import "strconv"

// These are limits on the environment, never limits in the allocation core.
const (
	Checkouts  = 2
	MaxDecides = 2
	MaxStops   = 1
)

// State has exactly one field for each TLA+ variable. Journal positions are
// IDs, and their values are decision identities. Store and journal capacities
// cover the whole explored ID space; growing the environment grows the space.
// Only Decides and Stops are environment counters.
type State struct {
	Store   [Checkouts*MaxDecides + MaxStops]bool
	Files   [Checkouts][Checkouts*MaxDecides + MaxStops]Decision
	Main    [Checkouts*MaxDecides + MaxStops]Decision
	Lock    int
	PC      [Checkouts]int
	Next    [Checkouts]int
	Decides [Checkouts]int
	Stops   int
}

// Init copies the constructors' system state into the comparable state.
func Init() (s State) {
	core := New(len(s.Store))
	copy(s.Store[:], core.Store)
	copy(s.Main[:], core.Main)
	s.Lock = core.Lock
	for i := 0; i < Checkouts; i++ {
		checkout := NewCheckout(len(s.Store))
		copy(s.Files[i][:], checkout.Files)
		s.PC[i], s.Next[i] = checkout.PC, checkout.Next
	}
	return s
}

// Try calls every Next operation for every checkout, including refusals after
// an environment limit has been reached. An accepted operation is omitted
// only when the environment cannot schedule it within its remaining budget.
// Each attempt has its own state copy.
func Try(s State, tried func(step string, args []any, next State)) {
	for i := 0; i < Checkouts; i++ {
		for _, step := range []string{"Take", "Reserve", "Write", "Finish", "Stop", "Merge", "Pull"} {
			next := s
			core := &Core{Store: next.Store[:], Main: next.Main[:], Lock: next.Lock}
			checkout := &Checkout{Files: next.Files[i][:], PC: next.PC[i], Next: next.Next[i]}
			switch step {
			case "Take":
				if core.Take(checkout, i+1) && s.Decides[i] >= MaxDecides {
					continue
				}
			case "Reserve":
				core.Reserve(checkout)
			case "Write":
				if core.Write(checkout, Decision{By: i + 1, Number: s.Decides[i] + 1}) {
					next.Decides[i]++
				}
			case "Finish":
				core.Finish(checkout)
			case "Stop":
				if core.Stop(checkout) {
					if s.Stops >= MaxStops {
						continue
					}
					next.Stops++
				}
			case "Merge":
				core.Merge(checkout)
			case "Pull":
				core.Pull(checkout)
			}
			next.Lock = core.Lock
			next.PC[i], next.Next[i] = checkout.PC, checkout.Next
			tried(step, []any{checkoutValue(i)}, next)
		}
	}
	// Done is a stutter when all branches are finished and synchronized. Its
	// disabled attempt also reports the input, with no system transition.
	tried("Done", []any{}, s)
}

func checkoutValue(index int) map[string]any {
	return map[string]any{"$mv": "c" + strconv.Itoa(index+1)}
}

func decisionSet(journal []Decision) map[string]any {
	values := make([]any, 0)
	for index, decision := range journal {
		if decision.Number != 0 {
			values = append(values, map[string]any{
				"id": index + 1,
				"by": checkoutValue(decision.By - 1),
				"n":  decision.Number,
			})
		}
	}
	return map[string]any{"$set": values}
}

// Abstract gives each variable its exact TLA+ name and tagged encoding.
func Abstract(s State) map[string]any {
	store := make([]any, 0)
	for index, taken := range s.Store {
		if taken {
			store = append(store, index+1)
		}
	}
	files, pc := make([]any, 0, Checkouts), make([]any, 0, Checkouts)
	next, decides := make([]any, 0, Checkouts), make([]any, 0, Checkouts)
	phase := [4]string{"idle", "taken", "reserved", "written"}
	for i := 0; i < Checkouts; i++ {
		key := checkoutValue(i)
		files = append(files, []any{key, decisionSet(s.Files[i][:])})
		pc = append(pc, []any{key, phase[s.PC[i]]})
		next = append(next, []any{key, s.Next[i]})
		decides = append(decides, []any{key, s.Decides[i]})
	}
	var lock any = "none"
	if s.Lock != 0 {
		lock = checkoutValue(s.Lock - 1)
	}
	return map[string]any{
		"store":   map[string]any{"$set": store},
		"files":   map[string]any{"$fn": files},
		"main":    decisionSet(s.Main[:]),
		"lock":    lock,
		"pc":      map[string]any{"$fn": pc},
		"next":    map[string]any{"$fn": next},
		"decides": map[string]any{"$fn": decides},
		"stops":   s.Stops,
	}
}
