package twophase

import "testing"

func reachable() map[State]bool {
	seen := map[State]bool{Init(): true}
	queue := []State{Init()}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		for _, t := range Successors(s) {
			if !seen[t] {
				seen[t] = true
				queue = append(queue, t)
			}
		}
	}
	return seen
}

func TestInvariantsAndWitnesses(t *testing.T) {
	allCommitted, allAborted := false, false
	for s := range reachable() {
		if len(Successors(s)) == 0 {
			t.Errorf("deadlock in %+v", s)
		}
		committed, aborted := 0, 0
		for r := 0; r < N; r++ {
			if s.RM[r] > Aborted {
				t.Errorf("bad RM state in %+v", s)
			}
			switch s.RM[r] {
			case Committed:
				committed++
			case Aborted:
				aborted++
			}
		}
		if committed > 0 && aborted > 0 {
			t.Errorf("TCConsistent violated in %+v", s)
		}
		allCommitted = allCommitted || committed == N
		allAborted = allAborted || aborted == N
	}
	if !allCommitted || !allAborted {
		t.Errorf("witnesses: allCommitted=%v allAborted=%v", allCommitted, allAborted)
	}
}
