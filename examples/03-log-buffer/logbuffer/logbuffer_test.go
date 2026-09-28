package logbuffer

import "testing"

func TestRefusals(t *testing.T) {
	b := New(1)
	if _, ok := b.Ship(); ok {
		t.Fatal("shipped from an empty buffer")
	}
	if !b.Write(Line{P: 0, N: 1}) {
		t.Fatal("refused a write with room")
	}
	if b.Write(Line{P: 1, N: 1}) || b.N != 1 {
		t.Fatal("wrote into a full buffer")
	}
	if l, ok := b.Ship(); !ok || l != (Line{P: 0, N: 1}) {
		t.Fatalf("shipped %v, %v", l, ok)
	}
}

func TestExploreHoldsInvariants(t *testing.T) {
	seen := map[State]bool{Init(): true}
	queue := []State{Init()}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		Try(s, func(step string, args []any, n State) {
			if n.BufLen > Capacity || n.SentLen+n.BufLen != n.LogLen {
				t.Fatalf("%s from %+v reached %+v", step, s, n)
			}
			if !seen[n] {
				seen[n] = true
				queue = append(queue, n)
			}
		})
	}
	if len(seen) < 2 {
		t.Fatalf("explored only %d states", len(seen))
	}
}
