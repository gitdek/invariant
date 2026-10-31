package recovery

import (
	"encoding/json"
	"testing"
)

// TestExplore walks every state Try reaches and checks the invariants in each.
func TestExplore(t *testing.T) {
	seen := map[State]bool{Init(): true}
	queue := []State{Init()}
	merged, replaced := false, false
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
			t.Fatalf("more than one pull request for a build, or a second merge, in %+v", s)
		}
		if s.Merges > 0 && !(s.Gate && s.Mergeable) {
			t.Fatalf("merged before the gate in %+v", s)
		}
		if (s.PR == PRDraft || s.PR == PRReady) && s.Pred == PredDraft {
			t.Fatalf("two pull requests open in %+v", s)
		}
		if s.Pred == PredRetired && !s.Intent {
			t.Fatalf("closed a draft with no record of the retry that replaces it in %+v", s)
		}
		if s.Pred == PredMerged && (s.Runs[runIndex(Build)] != RunNone || s.PRs > 0) {
			t.Fatalf("built again over a merged draft in %+v", s)
		}
		if s.Gate && s.PRs == 0 {
			t.Fatalf("a passed gate with no pull request of the latest build's own in %+v", s)
		}
		if s.Runs[runIndex(Build)] != RunNone && s.RunStarts[runIndex(Build)] == 0 {
			t.Fatalf("the latest build's run counted an earlier build's in %+v", s)
		}
		if s.Merges == 1 && s.Posts[Merge] == 1 && s.Crashes == MaxCrashes {
			merged = true
		}
		if s.Pred == PredRetired && s.Merges == 1 && s.Posts[Merge] == 1 {
			replaced = true
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
	if !replaced {
		t.Fatal("no state merged and reported a pull request that replaced a failed draft")
	}
	t.Logf("%d states", len(seen))
}
