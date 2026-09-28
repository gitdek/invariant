package twophase

import "testing"

// successors is every state Try reaches from s, refusals included.
func successors(s State) []State {
	var next []State
	Try(s, func(_ string, _ []any, u State) { next = append(next, u) })
	return next
}

// explore visits every state reachable from Init, breadth first, and
// returns them with the number of levels searched.
func explore(step func(State) []State) (seen map[State]bool, depth int) {
	seen = map[State]bool{Init(): true}
	for frontier := []State{Init()}; len(frontier) > 0; depth++ {
		var next []State
		for _, s := range frontier {
			for _, u := range step(s) {
				if !seen[u] {
					seen[u] = true
					next = append(next, u)
				}
			}
		}
		frontier = next
	}
	return seen, depth
}

// consistent mirrors TCConsistent.
func consistent(s State) bool {
	sawAbort, sawCommit := false, false
	for r := 0; r < RM; r++ {
		sawAbort = sawAbort || s.RMs[r] == Aborted
		sawCommit = sawCommit || s.RMs[r] == Committed
	}
	return !(sawAbort && sawCommit)
}

// The code must reach exactly the states TLC reports for the spec with
// RM = {r1, r2, r3}.
func TestStateSpaceMatchesModel(t *testing.T) {
	if RM != 3 {
		t.Skip("TLC's counts are for RM = {r1, r2, r3}")
	}
	seen, depth := explore(successors)
	if len(seen) != 288 || depth != 11 {
		t.Fatalf("reached %d states in %d levels; TLC reports 288 states, depth 11", len(seen), depth)
	}
}

func TestEveryReachableStateIsConsistent(t *testing.T) {
	seen, _ := explore(successors)
	var allCommitted, allAborted bool
	for s := range seen {
		if !consistent(s) {
			t.Fatalf("reached an inconsistent state: %+v", s)
		}
		all := func(want RMState) bool {
			for r := 0; r < RM; r++ {
				if s.RMs[r] != want {
					return false
				}
			}
			return true
		}
		allCommitted = allCommitted || all(Committed)
		allAborted = allAborted || all(Aborted)
	}
	if !allCommitted || !allAborted {
		t.Errorf("all committed reachable: %v, all aborted reachable: %v; want both", allCommitted, allAborted)
	}
}

// Every step is tried for every resource manager in every state, and a
// decided transaction manager refuses to decide again.
func TestTryTriesEveryStepAndRefuses(t *testing.T) {
	seen, _ := explore(successors)
	for s := range seen {
		n := 0
		Try(s, func(step string, _ []any, u State) {
			n++
			if s.TM == TMDone && (step == "TMCommit" || step == "TMAbort") && u != s {
				t.Fatalf("%s ran after the decision in %+v", step, s)
			}
		})
		if n != 2+5*RM {
			t.Fatalf("tried %d steps in %+v; want %d", n, s, 2+5*RM)
		}
	}
}

// The known bug from the lock file, in Go: a coordinator that commits as soon
// as any resource manager has prepared. Exploring it must reach an
// inconsistent state, just as TLC does with the mutated spec.
func TestEarlyCommitIsCaught(t *testing.T) {
	early := func(s State) []State {
		next := successors(s)
		if s.TM == TMInit {
			for r := 0; r < RM; r++ {
				if s.TMPrepared[r] {
					u := s
					u.TM, u.CommitMsg = TMDone, true
					return append(next, u)
				}
			}
		}
		return next
	}
	seen, _ := explore(early)
	for s := range seen {
		if !consistent(s) {
			return
		}
	}
	t.Fatal("the early-commit coordinator never reached an inconsistent state")
}
