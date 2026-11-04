package recovery

import (
	"encoding/json"
	"testing"
)

// TestExplore preserves the ordinary recovery regression exploration. The gate
// explores both lanes together and checks their exact TLA+ state correspondence.
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
			if next.FollowLane {
				return
			}
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

// followPick selects an actual offered edge. Directed regression paths exercise
// recovery combinations; the explorer itself still offers every environment
// choice independently, and the gate checks its complete graph.
func followPick(t *testing.T, s State, step string, accept func(State) bool) State {
	t.Helper()
	result, found := s, false
	Try(s, func(name string, args []any, n State) {
		if !found && name == step && n != s && accept(n) {
			result, found = n, true
		}
	})
	if !found {
		t.Fatalf("no %s transition from phase %s, operation %d", step, textWord(s.Follow.Control.Phase), s.Follow.Control.Op)
	}
	checkFollow(t, result)
	return result
}
func followPhase(t *testing.T, s State, phase string) State {
	t.Helper()
	return followPick(t, s, "Act", func(n State) bool { return textWord(n.Follow.Control.Phase) == phase })
}
func followFinish(t *testing.T, s State, result string) State {
	t.Helper()
	return followPick(t, s, "Act", func(n State) bool {
		return textWord(n.Follow.Control.Phase) == "run-result" && currentOf(n.Follow).Raw == result
	})
}
func followBegin(t *testing.T) State {
	t.Helper()
	s := followPick(t, Init(), "ChooseFollowup", func(n State) bool { return n.FollowLane })
	return followPick(t, s, "Acquire", func(n State) bool { return n.Lease == 0 })
}
func followOpen(t *testing.T) State {
	t.Helper()
	s := followBegin(t)
	s = followPhase(t, s, "start-run")
	s = followPhase(t, s, "running")
	s = followFinish(t, s, "success")
	s = followPhase(t, s, "idle")
	s = followPhase(t, s, "start-run")
	s = followPhase(t, s, "running")
	s = followFinish(t, s, "success")
	s = followPhase(t, s, "start-publication")
	s = followPick(t, s, "Act", func(n State) bool {
		return textWord(n.Follow.Control.Phase) == "publication" && n.Follow.Events == s.Follow.Events
	})
	return followPhase(t, s, "watch")
}
func followFailCI(t *testing.T, s State) State {
	t.Helper()
	s = followPick(t, s, "FollowOutside", func(n State) bool { return textWord(n.Follow.RemoteCI) == "fail" })
	return followPhase(t, s, "idle")
}
func checkFollow(t *testing.T, s State) {
	t.Helper()
	f, c := s.Follow, controlOf(s.Follow)
	if f.Creations > 1 || f.Merges > 1 || f.Answers > 1 || !f.Applied || !f.Checks {
		t.Fatal("duplicate or stale effect")
	}
	used := [2]int{}
	for i := 0; i < c.Op; i++ {
		o := bigO(f.Ops[i])
		if f.Starts[i] > 1 {
			t.Fatal("operation started twice")
		}
		if f.Charged[i] {
			used[o.Round]++
		}
		if (o.Raw == "failed" || o.Raw == "nochange" || o.Raw == "stopped") && o.Result != "none" && o.Result != o.Raw {
			t.Fatal("repair failure changed")
		}
		for k, e := range f.Ops[i].Effects {
			if f.Dispatches[i+1][k] > 1 {
				t.Fatal("duplicate dispatch")
			}
			if e.Dispatched && (e.Intent == 0 || int(e.Intent) > c.N || int(f.Snapshots[e.Intent-1].Op) != i+1) {
				t.Fatal("unrecorded effect")
			}
			if o.ResultSnapshot > 0 && textWord(e.Status) == "pending" {
				t.Fatal("settled operation has pending effect")
			}
		}
	}
	if used[0] > 2 || used[1] > 2 || c.Left+used[c.Round] != 2 {
		t.Fatal("repair allowance mismatch")
	}
	for i := 0; i < c.N; i++ {
		if f.Snapshots[i] != f.Accepted[i] {
			t.Fatal("accepted snapshot changed")
		}
	}
	if c.Consumed > 0 && (!c.Requested || (f.Barrier > 0 && (f.Ops[f.Barrier-1].ResultSnapshot == 0 || int(f.Ops[f.Barrier-1].ResultSnapshot) >= c.Consumed))) {
		t.Fatal("premature renewal")
	}
	if f.Reopens > 0 && c.Consumed == 0 {
		t.Fatal("reopened without writer")
	}
	if c.CI != "none" && c.CIHead != c.Head {
		t.Fatal("reused CI")
	}
	if c.Status == "merged" && (textWord(f.RemotePR) != "merged" || f.Merges != 1 || c.Proof == "none") {
		t.Fatal("merge without confirmation")
	}
}

func TestFollowLostPushThenExternalMerge(t *testing.T) {
	s := followFailCI(t, followOpen(t))
	s = followPhase(t, s, "start-run")
	s = followPhase(t, s, "running")
	s = followFinish(t, s, "success")
	s = followPhase(t, s, "start-publication")
	id := s.Follow.Control.Op
	s = followPick(t, s, "Act", func(n State) bool { return n.Follow.Lost[id][1] })
	s = followPick(t, s, "FollowOutside", func(n State) bool { return n.Follow.ExternalMerged })
	s = followPhase(t, s, "done")
	if s.Follow.Dispatches[id][1] != 1 || s.Follow.Control.Left != 1 || textWord(s.Follow.Control.Status) != "merged" {
		t.Fatal("lost push was not reconciled")
	}
	for _, d := range s.Follow.Dispatches {
		if d[2] != 0 {
			t.Fatal("factory merged externally merged PR")
		}
	}
}

func TestFollowRenewalWaitsAndPreservesFailures(t *testing.T) {
	s := followFailCI(t, followOpen(t))
	s = followPhase(t, s, "start-run")
	s = followPhase(t, s, "running")
	s = followFinish(t, s, "failed")
	s = followPhase(t, s, "idle")
	first := s.Follow.Control.Op
	s = followPhase(t, s, "start-run")
	s = followPhase(t, s, "running")
	second := s.Follow.Control.Op
	s = followPick(t, s, "FollowOutside", func(n State) bool { return n.Follow.Control.Requested })
	Try(s, func(step string, args []any, n State) {
		if n.Follow.Control.Consumed != 0 {
			t.Fatal("request consumed during repair")
		}
	})
	s = followFinish(t, s, "nochange")
	s = followPhase(t, s, "hold")
	s = followPick(t, s, "Act", func(n State) bool { return n.Follow.Control.Consumed > 0 })
	s = followPick(t, s, "Act", func(n State) bool { return n.Follow.Control.Answer.Intent > 0 })
	s = followPick(t, s, "Act", func(n State) bool { return n.Follow.Answers == 1 })
	if s.Follow.Control.Left != 2 || textWord(s.Follow.Ops[first-1].Result) != "failed" || textWord(s.Follow.Ops[second-1].Result) != "nochange" {
		t.Fatal("renewal lost failure history")
	}
	s = followPhase(t, s, "start-run")
	if s.Follow.Control.Left != 1 {
		t.Fatal("renewed repair was not charged once")
	}
}

func TestFollowClosedCreationAndResume(t *testing.T) {
	s := followBegin(t)
	s = followPhase(t, s, "start-run")
	s = followPhase(t, s, "running")
	s = followFinish(t, s, "success")
	s = followPhase(t, s, "idle")
	s = followPhase(t, s, "start-run")
	s = followPhase(t, s, "running")
	s = followFinish(t, s, "success")
	s = followPhase(t, s, "start-publication")
	id := s.Follow.Control.Op
	s = followPick(t, s, "Act", func(n State) bool { return n.Follow.Lost[id][1] })
	s = followPick(t, s, "FollowOutside", func(n State) bool { return textWord(n.Follow.RemotePR) == "closed" })
	s = followPhase(t, s, "hold")
	if !s.Follow.Control.KnownPR || s.Follow.Creations != 1 {
		t.Fatal("closed creation was not adopted")
	}
	Try(s, func(step string, args []any, n State) {
		if n.Follow.Reopens != 0 {
			t.Fatal("automatic reopening")
		}
	})
	s = followPick(t, s, "FollowOutside", func(n State) bool { return n.Follow.Control.Requested })
	s = followPick(t, s, "Act", func(n State) bool { return n.Follow.Control.Consumed > 0 })
	s = followPick(t, s, "Act", func(n State) bool { return n.Follow.Control.Answer.Intent > 0 })
	s = followPick(t, s, "Act", func(n State) bool { return n.Follow.Answers == 1 })
	s = followPhase(t, s, "start-reopen")
	s = followPhase(t, s, "reopen-response")
	s = followPhase(t, s, "watch")
	if s.Follow.Creations != 1 || s.Follow.Reopens != 1 || textWord(s.Follow.RemotePR) != "open" {
		t.Fatal("resume did not reopen original PR")
	}
}

func TestFollowLeaseLossAndLostMerge(t *testing.T) {
	s := followOpen(t)
	s = followPick(t, s, "FollowOutside", func(n State) bool { return textWord(n.Follow.RemoteCI) == "pass" && n.Follow.RemoteCanMerge })
	s = followPhase(t, s, "idle")
	s = followPhase(t, s, "start-merge")
	if textWord(s.Follow.Control.Status) == "merged" {
		t.Fatal("authorization treated as confirmation")
	}
	id := s.Follow.Control.Op
	s = followPick(t, s, "Act", func(n State) bool { return n.Follow.Lost[id][2] && textWord(n.Follow.RemotePR) == "merged" })
	s = followPick(t, s, "Crash", func(n State) bool { return !n.Up[0] })
	s = followPick(t, s, "Expire", func(n State) bool { return n.Lease == NoOne })
	s = followPick(t, s, "Acquire", func(n State) bool { return n.Lease == 1 })
	s = followPhase(t, s, "done")
	if textWord(s.Follow.Control.Status) != "merged" || textWord(s.Follow.Control.Proof) != "observation" || s.Follow.Dispatches[id][2] != 1 {
		t.Fatal("lost merge did not reconcile actual remote result")
	}
}

func TestFollowJournalRejectsStaleCommitAndCapacity(t *testing.T) {
	for _, capacity := range []int{0, 3, 17} {
		j := NewFollowJournal(followIdentity, capacity)
		o := FollowOperation{Kind: "none", Raw: "none", Result: "none"}
		v := FollowObservation{PR: "none"}
		w := FollowSession{ID: 0, Lease: 0, Fence: 1, Up: true}
		activation := j.Control.Activate(o, v, w, capacity)
		if !j.Save(activation) || j.Save(activation) {
			t.Fatal("activation was not committed exactly once")
		}
		x := j.Control.Authorize(o, v, w, capacity)
		if capacity == 0 {
			if x.OK || j.Save(x) || len(j.Snapshots) != 0 {
				t.Fatal("zero capacity accepted an operation")
			}
			continue
		}
		if !j.Save(x) || len(j.Snapshots) != 1 {
			t.Fatal("authorization snapshot was not persisted")
		}
		snapshot := j.Snapshots[0]
		start := j.Control.Start(j.Operations[0], v, w, capacity)
		if !j.Save(start) {
			t.Fatal("recorded start refused")
		}
		if j.Save(x) || j.Save(start) {
			t.Fatal("stale transition was committed")
		}
		if j.Snapshots[0] != snapshot || !j.Operations[0].Run.Dispatched {
			t.Fatal("journal changed during refusal")
		}
		stale := w
		stale.Lease = 1
		if j.Control.Finish(j.Operations[0], v, stale, capacity, "success").OK {
			t.Fatal("old lease accepted a result")
		}
		stale = w
		stale.Fence = 2
		if j.Control.Finish(j.Operations[0], v, stale, capacity, "success").OK {
			t.Fatal("old fence accepted a result")
		}
	}
}

// Check the follow-up safety assertions over every offered environment
// combination, including interleavings outside the directed regression paths.
// TLC separately checks the ratified statements and exact model agreement.
func TestFollowExplore(t *testing.T) {
	s := followPick(t, Init(), "ChooseFollowup", func(n State) bool { return n.FollowLane })
	seen := map[State]bool{s: true}
	layer := []State{s}
	for len(layer) > 0 {
		var nextLayer []State
		for _, s := range layer {
			checkFollow(t, s)
			Try(s, func(_ string, _ []any, next State) {
				if !seen[next] {
					seen[next] = true
					nextLayer = append(nextLayer, next)
				}
			})
		}
		layer = nextLayer
	}
	t.Logf("%d follow-up states", len(seen))
}
