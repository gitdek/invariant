package twophase

import "testing"

func reachable() map[State]bool {
	seen := map[State]bool{Init(): true}
	queue := []State{Init()}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		Try(s, func(_ string, _ []any, t State) {
			if !seen[t] {
				seen[t] = true
				queue = append(queue, t)
			}
		})
	}
	return seen
}

func TestInvariantsAndWitnesses(t *testing.T) {
	allCommitted, allAborted := false, false
	for s := range reachable() {
		committed, aborted := 0, 0
		for r := 0; r < RM; r++ {
			if s.RMState[r] < Working || s.RMState[r] > Aborted {
				t.Errorf("bad RM state in %+v", s)
			}
			switch s.RMState[r] {
			case Committed:
				committed++
			case Aborted:
				aborted++
			}
		}
		if committed > 0 && aborted > 0 {
			t.Errorf("TCConsistent violated in %+v", s)
		}
		allCommitted = allCommitted || committed == RM
		allAborted = allAborted || aborted == RM
	}
	if !allCommitted || !allAborted {
		t.Errorf("witnesses: allCommitted=%v allAborted=%v", allCommitted, allAborted)
	}
}

func TestRefusals(t *testing.T) {
	c := &Coordinator{Prepared: make([]bool, 2)}
	if c.Commit() || c.Done {
		t.Errorf("commit before every RM prepared ran")
	}
	if !c.Abort() || c.Abort() {
		t.Errorf("abort should run exactly once")
	}
	if c.RcvPrepared(0) || c.Prepared[0] {
		t.Errorf("RcvPrepared after the decision ran")
	}
	p := &Participants{States: []int{Committed}}
	if p.Prepare(0) || p.ChooseToAbort(0) || p.States[0] != Committed {
		t.Errorf("RM left committed on its own")
	}
}
