package pool

// The model's bounds: Size = 2, Clients = {c1, c2, c3}.
const (
	Size    = 2
	Clients = 3
)

// State is the model's held: Held[c] is 1 + the connection client c holds,
// or 0 when it holds none.
type State struct {
	Held [Clients]int
}

// Init is the model's Init: no connection is out.
func Init() State {
	return State{}
}

// load makes the code's pool from s.
func load(s State) *Pool {
	p := New(Size, Clients)
	for c := 0; c < Clients; c++ {
		if h := s.Held[c]; h != 0 {
			p.Held[c] = h
			p.Owner[h-1] = c + 1
		}
	}
	return p
}

// store reads the code's pool back into a State.
func store(p *Pool) State {
	var s State
	for c := 0; c < Clients; c++ {
		s.Held[c] = p.Held[c]
	}
	return s
}

// Successors expands Next in s: for each client, Acquire of each connection,
// Refuse, and Release.
func Successors(s State) []State {
	var out []State
	for c := 0; c < Clients; c++ {
		for k := 0; k < Size; k++ {
			p := load(s)
			if p.Acquire(c, k) {
				out = append(out, store(p))
			}
		}
		// Refuse: the code refuses every request when all are out, and
		// nothing changes.
		p := load(s)
		full := true
		for k := 0; k < Size; k++ {
			if p.Owner[k] == 0 {
				full = false
			}
		}
		if full {
			refused := true
			for k := 0; k < Size; k++ {
				if p.Acquire(c, k) {
					refused = false
				}
			}
			if refused {
				out = append(out, store(p))
			}
		}
		p = load(s)
		if p.Release(c) {
			out = append(out, store(p))
		}
	}
	return out
}
