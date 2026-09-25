package twophase

import "testing"

// successors applies every step whose precondition holds, the same way TLC
// expands the spec's Next.
func successors(s State) []State {
	var next []State
	if CanTMCommit(s) {
		next = append(next, TMCommit(s))
	}
	if s.TM == TMInit {
		next = append(next, TMAbort(s))
	}
	for r := 0; r < N; r++ {
		if s.TM == TMInit && s.PreparedMsg[r] {
			next = append(next, TMRcvPrepared(s, r))
		}
		if s.RM[r] == Working {
			next = append(next, RMPrepare(s, r), RMChooseToAbort(s, r))
		}
		if s.CommitMsg {
			next = append(next, RMRcvCommitMsg(s, r))
		}
		if s.AbortMsg {
			next = append(next, RMRcvAbortMsg(s, r))
		}
	}
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

// The Go state machine must reach exactly the states TLC reports for the
// spec with RM = {r1, r2, r3}. Matching counts are strong evidence the step
// functions implement the model's actions, no more and no fewer.
func TestStateSpaceMatchesModel(t *testing.T) {
	seen, depth := explore(successors)
	if len(seen) != 288 || depth != 11 {
		t.Fatalf("reached %d states in %d levels; TLC reports 288 states, depth 11", len(seen), depth)
	}
}

func TestEveryReachableStateIsConsistent(t *testing.T) {
	seen, _ := explore(successors)
	var allCommitted, allAborted bool
	for s := range seen {
		if !Consistent(s) {
			t.Fatalf("reached an inconsistent state: %+v", s)
		}
		allCommitted = allCommitted || s.RM == [N]RMState{Committed, Committed, Committed}
		allAborted = allAborted || s.RM == [N]RMState{Aborted, Aborted, Aborted}
	}
	if !allCommitted || !allAborted {
		t.Errorf("all committed reachable: %v, all aborted reachable: %v; want both", allCommitted, allAborted)
	}
}

// The known bug from the lock file, in Go: a coordinator that commits as soon
// as any resource manager has prepared. Exploring it must reach an
// inconsistent state, just as TLC does with the mutated spec.
func TestEarlyCommitIsCaught(t *testing.T) {
	early := func(s State) []State {
		next := successors(s)
		if s.TM == TMInit && !CanTMCommit(s) {
			for r := 0; r < N; r++ {
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
		if !Consistent(s) {
			return
		}
	}
	t.Fatal("the early-commit coordinator never reached an inconsistent state")
}
