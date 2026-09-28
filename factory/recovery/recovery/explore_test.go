package recovery

import (
	"encoding/json"
	"testing"
)

// TestExplore walks every state Try reaches and checks the invariants in each.
func TestExplore(t *testing.T) {
	seen := map[State]bool{Init(): true}
	queue := []State{Init()}
	merged := false
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		for k := 0; k < NumKeys; k++ {
			if s.Posts[k] > 1 {
				t.Fatalf("step %d answered twice in %+v", k, s)
			}
		}
		for i := range s.RunStarts {
			if s.RunStarts[i] > 1 {
				t.Fatalf("run %d started twice in %+v", i, s)
			}
		}
		if s.PRs > 1 || s.Merges > 1 {
			t.Fatalf("more than one pull request or merge in %+v", s)
		}
		if s.Merges > 0 && !(s.Gate && s.Mergeable) {
			t.Fatalf("merged before the gate in %+v", s)
		}
		if s.Merges == 1 && s.Posts[Merge] == 1 && s.Crashes == MaxCrashes {
			merged = true
		}
		if _, err := json.Marshal(Abstract(s)); err != nil {
			t.Fatal(err)
		}
		Try(s, func(step string, args []any, next State) {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		})
	}
	if !merged {
		t.Fatal("no state merged and reported after the most crashes")
	}
	t.Logf("%d states", len(seen))
}
