package protocol

import "testing"

func reachable() map[State]bool {
	seen := map[State]bool{Init(): true}
	frontier := []State{Init()}
	for len(frontier) > 0 {
		var next []State
		for _, s := range frontier {
			for _, t := range Successors(s) {
				if !seen[t] {
					seen[t] = true
					next = append(next, t)
				}
			}
		}
		frontier = next
	}
	return seen
}

func TestInvariants(t *testing.T) {
	building := func(k int8) bool {
		return k == KindRatified || k == KindPROpen || k == KindFailed || k == KindMerged
	}
	var sawFactoryMerge, sawAmendment bool
	sawFailure := map[int8]bool{}
	for s := range reachable() {
		if len(Successors(s)) == 0 {
			t.Fatalf("deadlock in %+v", s)
		}
		if s.DirectedBy != NoActor && s.DirectedBy != ByAlice {
			t.Fatalf("directed by a non-director: %+v", s)
		}
		if s.Stops > MaxStops {
			t.Fatalf("past the stop limit: %+v", s)
		}
		if (s.Failure == FailStopped || s.Failure == FailLimit) && s.Head != NoHead {
			t.Fatalf("stopped build has a pull request: %+v", s)
		}
		if s.Kind == KindFailed {
			sawFailure[s.Failure] = true
		}
		if building(s.Kind) {
			if s.Ratified == NoP || s.Ratified != s.Proposal {
				t.Fatalf("not the current proposal: %+v", s)
			}
			if s.Open[0] || s.Open[1] {
				t.Fatalf("open question while ratified: %+v", s)
			}
			if s.RatifiedBase != s.Amends {
				t.Fatalf("amended a moved lock: %+v", s)
			}
		}
		if s.Kind == KindMerged && s.MergedBy == ByFactory {
			sawFactoryMerge = true
			if s.RatifiedBase != NoP {
				sawAmendment = true
			}
			if s.MergedHead != s.Head || s.Gate[s.MergedHead-1] != GatePass ||
				s.Scope != ScopeOne || s.PrLock != s.Ratified {
				t.Fatalf("bad merge: %+v", s)
			}
		}
	}
	if !sawFactoryMerge || !sawAmendment {
		t.Fatalf("factory merge %v, amendment merge %v", sawFactoryMerge, sawAmendment)
	}
	for _, f := range []int8{FailStopped, FailLimit, FailGate, FailCI, FailUnmergeable} {
		if !sawFailure[f] {
			t.Fatalf("no failure of kind %d", f)
		}
	}
}
