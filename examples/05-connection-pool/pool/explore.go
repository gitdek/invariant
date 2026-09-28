package pool

import "fmt"

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

// client is client c as the spec's model value.
func client(c int) any {
	return map[string]any{"$mv": fmt.Sprintf("c%d", c+1)}
}

// Try tries every step of Next in s, for every client: Acquire of each
// connection, Refuse and Release. The code decides whether each runs.
func Try(s State, tried func(step string, args []any, next State)) {
	for c := 0; c < Clients; c++ {
		for k := 0; k < Size; k++ {
			p := load(s)
			p.Acquire(c, k)
			tried("Acquire", []any{client(c)}, store(p))
		}
		p := load(s)
		p.Refuse(c)
		tried("Refuse", []any{client(c)}, store(p))
		p = load(s)
		p.Release(c)
		tried("Release", []any{client(c)}, store(p))
	}
}

// Abstract is s as the spec's held: each client to the set of connections
// it holds, numbered 1..Size.
func Abstract(s State) map[string]any {
	held := []any{}
	for c := 0; c < Clients; c++ {
		conns := []any{}
		if h := s.Held[c]; h != 0 {
			conns = append(conns, h)
		}
		held = append(held, []any{client(c), map[string]any{"$set": conns}})
	}
	return map[string]any{"held": map[string]any{"$fn": held}}
}
