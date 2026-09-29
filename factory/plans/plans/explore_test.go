package plans

import "testing"

// TestExplore walks every state the code reaches and checks the plan's invariants.
func TestExplore(t *testing.T) {
	seen := map[State]bool{Init(): true}
	queue := []State{Init()}
	finished := false
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		check(t, s)
		if s.Done && s.Crashes == MaxCrashes {
			finished = true
		}
		Try(s, func(step string, args []any, next State) {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		})
	}
	if !finished {
		t.Error("no plan finished after the crashes")
	}
}

func check(t *testing.T, s State) {
	t.Helper()
	if s.Count > 0 && s.Ratifier == 0 {
		t.Fatalf("issue opened before ratified: %+v", s)
	}
	if s.StopMark >= 0 && s.Count > s.StopMark {
		t.Fatalf("issue opened after stop: %+v", s)
	}
	unmerged := 0
	for k := 0; k < int(s.Count); k++ {
		if s.Author[k] != s.Ratifier {
			t.Fatalf("issue %d not on the ratifier's authority: %+v", k+1, s)
		}
		if s.Status[k] != Merged {
			unmerged++
		}
		if k+1 < int(s.Count) && s.Status[k] != Merged {
			t.Fatalf("issue %d opened before %d merged: %+v", k+2, k+1, s)
		}
	}
	if unmerged > 1 {
		t.Fatalf("more than one issue open: %+v", s)
	}
	for k := int(s.Count); k < N; k++ {
		if s.Recorded[k] {
			t.Fatalf("recorded issue %d that wasn't created: %+v", k+1, s)
		}
	}
	if s.Done && (s.Count != N || s.Status[N-1] != Merged) {
		t.Fatalf("done before every issue merged: %+v", s)
	}
}
