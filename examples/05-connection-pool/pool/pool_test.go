package pool

import "testing"

func TestExploreReachesModelStates(t *testing.T) {
	seen := map[State]bool{Init(): true}
	frontier := []State{Init()}
	for len(frontier) > 0 {
		var next []State
		for _, s := range frontier {
			for _, t2 := range Successors(s) {
				if !seen[t2] {
					seen[t2] = true
					next = append(next, t2)
				}
			}
		}
		frontier = next
	}
	// 1 empty + 3 clients * 2 conns + 3 pairs * 2 orderings.
	if len(seen) != 13 {
		t.Fatalf("reached %d states, want 13", len(seen))
	}
	for s := range seen {
		out := 0
		for c := 0; c < Clients; c++ {
			if s.Held[c] != 0 {
				out++
				for d := c + 1; d < Clients; d++ {
					if s.Held[d] == s.Held[c] {
						t.Fatalf("shared connection in %v", s)
					}
				}
			}
		}
		if out > Size {
			t.Fatalf("over size in %v", s)
		}
	}
}

func TestFullPoolRefuses(t *testing.T) {
	p := New(1, 2)
	if !p.Acquire(0, 0) {
		t.Fatal("first acquire refused")
	}
	if p.Acquire(1, 0) {
		t.Fatal("acquire from full pool succeeded")
	}
	if !p.Release(0) || p.Release(0) {
		t.Fatal("release should free exactly once")
	}
	if !p.Acquire(1, 0) {
		t.Fatal("acquire after release refused")
	}
}
