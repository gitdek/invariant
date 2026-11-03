// +gobra

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
			// Keep the original ordinary-protocol regression exhaustive. The
			// gate explores both lanes; follow-up interactions also have focused
			// tests below so this suite need not duplicate that larger graph.
			if step == "ChooseFollowup" {
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

// followHarness supplies remote observations independently of the recovery core.
// In production these inputs come from durable effect results and GitHub reads.
type followHarness struct {
	t *testing.T
	r FollowRepo
	x FollowInput
}

func newFollowHarness(t *testing.T) *followHarness {
	return &followHarness{t: t,
		r: NewFollowRepo("source-obligation", "stable-branch", "stable-pr", 32, 128),
		x: FollowInput{Watcher: 0, Lease: 0, Fence: 1, Up: true,
			RemotePR: "none", RemoteCI: "none", RemoteMerge: "none"},
	}
}

func (h *followHarness) step(action string) {
	h.t.Helper()
	h.x.Operation = h.r.Op
	r, ok := h.r.Step(action, h.x)
	if !ok {
		h.t.Fatalf("%s refused in phase %s, operation %d", action, h.r.Phase, h.r.Op)
	}
	h.r = r
}

func (h *followHarness) refuse(action string) {
	h.t.Helper()
	h.x.Operation = h.r.Op
	if _, ok := h.r.Step(action, h.x); ok {
		h.t.Fatalf("unexpectedly accepted %s", action)
	}
}

func (h *followHarness) run(result string) {
	h.t.Helper()
	h.step("FAuthorize")
	h.step("FStart")
	h.x.Result = result
	h.step("FFinish")
	h.step("FReconcileRun")
}

func (h *followHarness) publish() {
	h.t.Helper()
	h.step("FPublish")
	h.x.RemotePR = "open"
	h.x.RemoteHead++
	h.x.Publication = h.x.RemoteHead
	h.x.RemoteCI, h.x.RemoteCIHead, h.x.RemoteCanMerge, h.x.RemoteMergeHead = "none", 0, false, 0
}

func (h *followHarness) create() {
	h.t.Helper()
	h.run("success")
	h.run("success")
	h.publish()
	h.step("FReconcilePublication")
}

func (h *followHarness) ci(result string, mergeable bool) {
	h.t.Helper()
	h.x.RemoteCI, h.x.RemoteCIHead = result, h.x.RemoteHead
	h.x.RemoteCanMerge, h.x.RemoteMergeHead = mergeable, h.x.RemoteHead
	h.step("FObserveCI")
}

func TestFollowRenewalWaitsForLostPublication(t *testing.T) {
	h := newFollowHarness(t)
	h.create()
	h.ci("fail", true)
	h.step("FAuthorize")
	repair := h.r.Op
	h.step("FStart")
	h.x.Requested = true
	h.refuse("FConsumeRequest")
	h.x.Result = "success"
	h.step("FFinish")
	h.step("FReconcileRun")
	h.refuse("FConsumeRequest")
	h.publish()
	// No reply was received; a person merged the PR before recovery read it.
	h.x.RemotePR, h.x.ExternalMerged = "merged", true
	h.refuse("FConsumeRequest")
	h.step("FReconcilePublication")
	if h.r.Status != "merged" || h.r.Proof != "observation" || h.r.Left != 1 {
		t.Fatalf("lost publication not reconciled from observation: %+v", h.r)
	}
	outcome := h.r.ResultSnapshots[repair]
	h.step("FConsumeRequest")
	h.step("FRecordAnswer")
	h.step("FAnswer")
	if h.r.Consumed <= outcome || h.r.Left != 2 || h.r.Answers != 1 {
		t.Fatal("renewal did not follow the durable repair outcome")
	}
	h.refuse("FConsumeRequest")
	h.refuse("FAnswer")
	h.refuse("FPublish")
	h.refuse("FMerge")
	if e := h.r.Effects[repair*FollowEffectKinds+FollowPublicationEffect]; e.Dispatches != 1 || !e.Completed {
		t.Fatal("publication was not completed exactly once")
	}
}

func TestFollowFailuresKeepUsageAndClosedPRReopens(t *testing.T) {
	h := newFollowHarness(t)
	h.create()
	h.ci("fail", true)
	h.run("failed")
	failed := h.r.Op
	h.run("nochange")
	nochange := h.r.Op
	if h.r.Status != "exhausted" || h.r.Left != 0 || h.r.Results[failed] != "failed" || h.r.Results[nochange] != "nochange" {
		t.Fatal("repair causes or charged usage were lost")
	}
	h.refuse("FAuthorize")
	h.x.RemotePR = "closed"
	h.step("FObservePR")
	h.refuse("FAuthorize")
	h.x.Requested = true
	h.step("FConsumeRequest")
	h.step("FRecordAnswer")
	h.step("FAnswer")
	h.step("FAuthorize")
	if h.r.Kinds[h.r.Op] != "reopen" || !h.r.KnownPR || h.r.PR != "stable-pr" {
		t.Fatal("resumption did not authorize reopening the same PR")
	}
	h.step("FReopen")
	h.x.RemotePR = "open"
	h.step("FReconcileReopen")
	if h.r.Left != 2 || !h.r.Charged[failed] || !h.r.Charged[nochange] {
		t.Fatal("renewal erased previous attempt usage")
	}
}

func TestFollowFencedAndStaleResults(t *testing.T) {
	h := newFollowHarness(t)
	h.step("FAuthorize")
	h.step("FStart")
	h.x.Operation = h.r.Op - 1
	h.x.Result = "success"
	if _, ok := h.r.Step("FFinish", h.x); ok {
		t.Fatal("accepted another operation's result")
	}
	h.x.Operation = h.r.Op
	h.x.Fence++
	h.refuse("FFinish")
	h.step("FRecoverStopped")
	h.step("FReconcileRun")
	old := h.r.Op
	if h.r.Results[old] != "stopped" {
		t.Fatal("lease loss did not retain stopped outcome")
	}
	h.step("FAuthorize")
	if h.r.Op == old {
		t.Fatal("automatic retry reused stopped operation identity")
	}
	h.step("FStart")
	h.x.Result = "success"
	h.step("FFinish")
	h.step("FReconcileRun")
	if h.r.Starts[old] != 1 {
		t.Fatal("restarted old operation")
	}
}

func TestFollowMergeIntentIsNotSuccess(t *testing.T) {
	h := newFollowHarness(t)
	h.create()
	h.ci("pass", true)
	h.step("FAuthorize")
	merge := h.r.Op
	if h.r.Status == "merged" || h.r.Proof != "none" {
		t.Fatal("authorization fabricated a merge")
	}
	h.x.Result = "unknown"
	h.step("FMerge")
	h.x.RemoteMerge = "unknown"
	h.step("FReconcileMerge")
	if h.r.MergeOutcome != "unknown" || h.r.Status == "merged" || h.r.ResultSnapshots[merge] != 0 {
		t.Fatal("unknown merge treated as a confirmed outcome")
	}
	h.refuse("FMerge")
	h.x.RemoteMerge = "refused"
	h.step("FReconcileMerge")
	if h.r.Status != "refused" || h.r.Results[merge] != "refused" {
		t.Fatal("definitive refusal was lost")
	}
	h.x.RemotePR, h.x.ExternalMerged = "merged", true
	h.step("FObservePR")
	if h.r.Status != "merged" || h.r.Proof != "observation" || h.r.Results[merge] != "refused" {
		t.Fatal("later external merge lost the original refusal")
	}
}

func TestFollowSnapshotsImmutableAndCapacity(t *testing.T) {
	h := newFollowHarness(t)
	h.run("failed")
	before := append([]FollowSnapshot(nil), h.r.Snapshots[:h.r.SnapshotCount]...)
	h.run("success")
	for i, snapshot := range before {
		if h.r.Snapshots[i] != snapshot {
			t.Fatalf("accepted snapshot %d changed", i)
		}
	}
	// Capacity is a caller-selected storage limit, independent of Workflow.
	r := NewFollowRepo("source", "branch", "pr", 0, 0)
	if _, ok := r.Step("FAuthorize", h.x); ok {
		t.Fatal("authorized without durable storage")
	}
}

func TestFollowLostCreationAdoptsExistingPR(t *testing.T) {
	for _, remote := range []string{"closed", "merged"} {
		t.Run(remote, func(t *testing.T) {
			h := newFollowHarness(t)
			h.run("success")
			h.run("success")
			h.publish()
			// Simulate a lost creation response and takeover by the other watcher.
			h.x.Watcher, h.x.Lease, h.x.Fence = 1, 1, 2
			h.x.RemotePR = remote
			h.step("FReconcilePublication")
			if !h.r.KnownPR || h.r.Status != remote || h.r.PR != "stable-pr" {
				t.Fatal("did not adopt the original PR across takeover")
			}
			h.refuse("FPublish")
			h.refuse("FAuthorize")
		})
	}
}

func TestFollowLostMergeReconcilesAfterTakeover(t *testing.T) {
	h := newFollowHarness(t)
	h.create()
	h.ci("pass", true)
	h.step("FAuthorize")
	h.x.Result = "success"
	h.step("FMerge")
	h.x.RemotePR, h.x.RemoteMerge, h.x.MergeLost = "merged", "success", true
	h.x.Watcher, h.x.Lease, h.x.Fence = 1, 1, 2
	h.step("FReconcileMerge")
	if h.r.Status != "merged" || h.r.Proof != "observation" {
		t.Fatal("lost merge response was not reconciled from the remote result")
	}
	h.refuse("FMerge")
}

func TestFollowAbstractUsesExplicitContainers(t *testing.T) {
	s := Init()
	Try(s, func(step string, _ []any, next State) {
		if step == "ChooseFollowup" {
			s = next
		}
	})
	// Include a recorded operation so tuple keys and snapshots are exercised.
	r := followOf(s)
	x := FollowInput{Up: true, Watcher: 0, Lease: 0, Fence: 1, RemotePR: "none"}
	var ok bool
	r, ok = r.Step("FAuthorize", x)
	if !ok {
		t.Fatal("authorization refused")
	}
	s = withFollow(s, r)
	var check func(any)
	check = func(v any) {
		switch value := v.(type) {
		case []any:
			t.Fatal("bare array in TLA abstraction")
		case map[string]any:
			for k, child := range value {
				switch k {
				case "$set", "$seq":
					for _, item := range child.([]any) {
						check(item)
					}
				case "$fn":
					for _, item := range child.([]any) {
						pair := item.([]any)
						check(pair[0])
						check(pair[1])
					}
				default:
					check(child)
				}
			}
		}
	}
	check(Abstract(s))
}
