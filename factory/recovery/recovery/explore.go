package recovery

import "strconv"

// The model's bounds.
const (
	MaxCrashes             = 2
	Watchers               = 2
	MaxFollowCrashes       = 1
	MaxFollowOps           = 7
	MaxDraftFailures       = 1
	MaxCreateFailures      = 1
	MaxPublicationRefusals = 1
	MaxLostCreations       = 1
	MaxLostPublications    = 1
	MaxLostMerges          = 1
	MaxCloses              = 1
	MaxHeadMoves           = 1
	MaxCIFailures          = 1
	MaxUnknownMerges       = 1
	MaxRefusedMerges       = 1
	MaxFollowEvents        = 3
)

// runKeys lists the steps that start an agent run, in the order State keeps them.
var runKeys = [2]int{Solve, Build}

// postKeys lists the steps the spec's posts counts, in the order State keeps
// them; the latest retry's and build's answers are counted apart.
var postKeys = [4]int{Solve, Ratify, Note, Merge}

// keyNames are the spec's names for the steps, indexed by the code's keys.
var keyNames = [NumKeys]string{
	Solve:  "solve",
	Ratify: "ratify",
	Note:   "note",
	Retry:  "retry",
	Watch:  "watch",
	Build:  "build",
	Merge:  "merge",
}

// runNames are the spec's names for a recorded agent run.
var runNames = [3]string{
	RunNone:     "none",
	RunRecorded: "recorded",
	RunDone:     "done",
}

// prNames and predNames are the spec's names for the latest build's pull
// request and the draft the latest retry replaces.
var (
	prNames   = [5]string{PRNone: "none", PRDraft: "draft", PRReady: "ready", PRClosed: "closed", PRMerged: "merged"}
	predNames = [5]string{PredNone: "none", PredDraft: "draft", PredRetired: "retired", PredClosed: "closed", PredMerged: "merged"}
)

// State is the model's state at its bounds. Watchers are 0..Watchers-1, a
// Lease of NoOne is "nobody", and an InRun of NoRun is "none". Given[Retry]
// is the spec's retried, and Posts[Retry] and Posts[Build] its retryPosts and
// buildPosts; Given[Watch] is its watched, and Posts[Watch] its watchPosts.
// Attempts count from the latest build's, which is latest, so
// Earlier marks a watcher in a build's run that an earlier attempt started:
// the spec's stale run.
type State struct {
	FollowLane bool
	Fence      int
	Follow     followState
	Given      [NumCmds]bool
	Posts      [NumKeys]int
	Runs       [len(runKeys)]int
	RunStarts  [len(runKeys)]int
	RatPushed  bool
	CodePushed bool
	PRs        int
	PR         int
	Gate       bool
	Mergeable  bool
	Merges     int
	Pred       int
	Intent     bool
	Marked     bool
	Lease      int
	Up         [Watchers]bool
	InRun      [Watchers]int
	Earlier    [Watchers]bool
	Crashes    int
	Unleased   [Watchers]bool
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func runIndex(k int) int {
	for i, rk := range runKeys {
		if rk == k {
			return i
		}
	}
	return -1
}

// latest is the latest build's attempt in the code's Repo, as repoOf makes it.
const latest = 1

// repoOf makes the code's Repo from s.
func repoOf(s State) Repo {
	r := NewRepo()
	r.Followup, r.Fence = s.FollowLane, s.Fence
	for c := 0; c < NumCmds; c++ {
		r.Given[c] = s.Given[c]
	}
	for k := 0; k < NumKeys; k++ {
		r.Posted[k] = s.Posts[k] > 0
	}
	for i, k := range runKeys {
		r.Runs[k] = s.Runs[i]
	}
	r.RatPushed = s.RatPushed
	r.CodePushed = s.CodePushed
	r.Opened = s.PRs > 0
	r.PR = s.PR
	r.GatePassed = s.Gate
	r.Mergeable = s.Mergeable
	r.Merged = s.Merges > 0
	r.Pred = s.Pred
	r.Intent = s.Intent
	r.Marked = s.Marked
	r.Attempt = latest
	r.Lease = s.Lease
	return r
}

// withRepo reads the code's Repo back into s, counting attempts from its
// latest build's again: after a retry, every build's run a watcher is in is
// an earlier attempt's.
func withRepo(s State, r Repo) State {
	s.FollowLane, s.Fence = r.Followup, r.Fence
	for w := 0; w < Watchers; w++ {
		s.Earlier[w] = r.Stale(watcherOf(s, w))
	}
	for c := 0; c < NumCmds; c++ {
		s.Given[c] = r.Given[c]
	}
	for k := 0; k < NumKeys; k++ {
		s.Posts[k] = b2i(r.Posted[k])
	}
	for i, k := range runKeys {
		s.Runs[i] = r.Runs[k]
	}
	s.RatPushed = r.RatPushed
	s.CodePushed = r.CodePushed
	s.PRs = b2i(r.Opened)
	s.PR = r.PR
	s.Gate = r.GatePassed
	s.Mergeable = r.Mergeable
	s.Merges = b2i(r.Merged)
	s.Pred = r.Pred
	s.Intent = r.Intent
	s.Marked = r.Marked
	s.Lease = r.Lease
	return s
}

func watcherOf(s State, w int) Watcher {
	v := Watcher{ID: w, InRun: s.InRun[w], Attempt: latest}
	if s.Earlier[w] {
		v.Attempt = latest - 1
	}
	return v
}

// withWatcher reads watcher w's memory back into s, after a step that left
// the latest build's attempt as it was.
func withWatcher(s State, w int, v Watcher) State {
	s.InRun[w] = v.InRun
	s.Earlier[w] = v.InRun == Build && v.Attempt != latest
	return s
}

// otherUp says some watcher other than w is running.
func otherUp(s State, w int) bool {
	for v := 0; v < Watchers; v++ {
		if v != w && s.Up[v] {
			return true
		}
	}
	return false
}

func mv(w int) map[string]any {
	return map[string]any{"$mv": "w" + strconv.Itoa(w+1)}
}

// Init is the model's initial state.
func Init() State {
	var s State
	s = withRepo(s, NewRepo())
	s.Follow = initialFollow()
	for w := 0; w < Watchers; w++ {
		s.Up[w] = true
		s.InRun[w] = NewWatcher(w).InRun
	}
	return s
}

// Try tries every step Next names from s, with every argument, and reports
// the state each reaches: s itself where the code refuses, or where the
// environment doesn't let the step happen.
func Try(s State, tried func(step string, args []any, next State)) {
	r := repoOf(s)
	n := s
	if t, ok := r.SelectFollowup(); ok && s == Init() {
		c := controlOf(s.Follow)
		x := c.Activate(currentOf(s.Follow), observationOf(s.Follow), FollowSession{}, MaxFollowOps)
		n = withFollow(withRepo(s, t), x)
	}
	tried("ChooseFollowup", []any{}, n)

	// A writer gives a command.
	for _, c := range []int{Solve, Ratify, Note} {
		n := s
		if t, ok := r.Give(c); ok {
			n = withRepo(s, t)
		}
		tried("Give", []any{keyNames[c]}, n)
	}

	// A writer retries: a new build, the next attempt, with its own agent
	// run, so a watcher still in the build before's run is in a stale one.
	{
		n := s
		if t, ok := r.Give(Retry); ok {
			n = withRepo(s, t)
			n.RunStarts[runIndex(Build)] = 0
		}
		tried("GiveRetry", []any{}, n)
	}

	// A writer's retry watches the latest build's failed draft again.
	{
		n := s
		if t, ok := r.Give(Watch); ok {
			n = withRepo(s, t)
		}
		tried("GiveWatch", []any{}, n)
	}

	// CI's gate passes, with the pull request mergeable or not.
	for _, m := range []bool{false, true} {
		n := s
		if t, ok := r.PassGate(m); ok {
			n = withRepo(s, t)
		}
		tried("CIPass", []any{}, n)
	}

	// Someone else closes or merges an open pull request: the latest
	// build's, or the draft a retry replaces.
	for _, op := range []func() (Repo, bool){r.OthersClosePR, r.OthersCloseDraft} {
		n := s
		if t, ok := op(); ok {
			n = withRepo(s, t)
		}
		tried("OthersClose", []any{}, n)
	}
	for _, op := range []func() (Repo, bool){r.OthersMergePR, r.OthersMergeDraft} {
		n := s
		if t, ok := op(); ok {
			n = withRepo(s, t)
		}
		tried("OthersMerge", []any{}, n)
	}

	// The lease runs out, unless a running holder keeps renewing it.
	{
		n := s
		if !(s.Lease != NoOne && s.Up[s.Lease]) {
			if t, ok := r.Expire(); ok {
				n = withRepo(s, t)
			}
		}
		tried("Expire", []any{}, n)
	}

	for w := 0; w < Watchers; w++ {
		args := []any{mv(w)}
		wt := watcherOf(s, w)
		up := s.Up[w]

		// A running watcher takes the lease.
		n := s
		if up && (!s.FollowLane || s.Crashes > 0 || w == 0) {
			if t, ok := r.Acquire(wt); ok {
				n = withRepo(s, t)
			}
		}
		tried("Acquire", args, n)

		// Crashes and stalls, left untried only where the environment's
		// bound alone rules them out.
		spare := s.Crashes < MaxCrashes && (!s.FollowLane || (s.Crashes < MaxFollowCrashes && int(s.Follow.Events) < MaxFollowEvents))
		if !(up && otherUp(s, w)) {
			tried("Crash", args, s)
		} else if spare {
			n = s
			n.Up[w] = false
			n.InRun[w], n.Earlier[w] = NoRun, false
			n.Crashes++
			if s.FollowLane {
				n.Follow.Events++
			}
			tried("Crash", args, n)
		}

		if !up {
			tried("Stall", args, s)
		} else if t, ok := r.Lapse(wt); !ok {
			tried("Stall", args, s)
		} else if spare {
			n = withRepo(s, t)
			n.Crashes++
			if s.FollowLane {
				n.Follow.Events++
			}
			tried("Stall", args, n)
		}

		// A crashed watcher restarts, remembering nothing.
		n = s
		if !up {
			if v, ok := r.Restart(wt); ok {
				n.Up[w] = true
				n = withWatcher(n, w, v)
			}
		}
		tried("Restart", args, n)

		// Act: the next effect of some step.
		act := func(op func() (Repo, bool)) {
			n := s
			if up {
				if t, ok := op(); ok {
					n = withRepo(s, t)
				}
			}
			tried("Act", args, n)
		}
		for _, k := range runKeys {
			n = s
			if up {
				if t, v, ok := r.StartRun(wt, k); ok {
					n = withWatcher(withRepo(s, t), w, v)
					n.RunStarts[runIndex(k)]++
				}
			}
			tried("Act", args, n)
			act(func() (Repo, bool) { return r.ReportStopped(wt, k) })
		}
		act(func() (Repo, bool) { return r.PushRatification(wt, Ratify) })
		act(func() (Repo, bool) { return r.RecordIntent(wt) })
		act(func() (Repo, bool) { return r.Retire(wt) })
		act(func() (Repo, bool) { return r.PushCode(wt, Build) })
		for _, ready := range []bool{false, true} {
			act(func() (Repo, bool) { return r.OpenPullRequest(wt, Build, ready) })
		}
		act(func() (Repo, bool) { return r.MarkReady(wt) })
		act(func() (Repo, bool) { return r.MergePullRequest(wt, Merge) })
		for k := 0; k < NumKeys; k++ {
			act(func() (Repo, bool) { return r.Post(wt, k) })
		}

		// The holder records its finished agent run.
		n = s
		if up {
			if t, v, ok := r.FinishRun(wt); ok {
				n = withWatcher(withRepo(s, t), w, v)
			}
		}
		tried("FinishRun", args, n)

		// A watcher that lost the lease drops its agent run.
		n = s
		if up {
			if v, ok := r.DropRun(wt); ok {
				n = withWatcher(s, w, v)
			}
		}
		tried("DropRun", args, n)
	}

	tryFollow(s, tried)

	// Nothing is left to answer, or the step stutters.
	tried("Rest", []any{}, s)
}

func set(xs []any) map[string]any {
	if xs == nil {
		xs = []any{}
	}
	return map[string]any{"$set": xs}
}

func fn(pairs []any) map[string]any {
	if pairs == nil {
		pairs = []any{}
	}
	return map[string]any{"$fn": pairs}
}

// inRunName is the spec's name for the agent run watcher w is in.
func inRunName(s State, w int) string {
	switch {
	case s.InRun[w] == NoRun:
		return "none"
	case s.Earlier[w]:
		return "stale"
	}
	return keyNames[s.InRun[w]]
}

// Abstract is s in the spec's vocabulary.
func Abstract(s State) map[string]any {
	var given []any
	for _, c := range []int{Solve, Ratify, Note} {
		if s.Given[c] {
			given = append(given, keyNames[c])
		}
	}
	var posts []any
	for _, k := range postKeys {
		posts = append(posts, []any{keyNames[k], s.Posts[k]})
	}
	var runs, starts []any
	for i, k := range runKeys {
		runs = append(runs, []any{keyNames[k], runNames[s.Runs[i]]})
		starts = append(starts, []any{keyNames[k], s.RunStarts[i]})
	}
	gate := "none"
	if s.Gate {
		gate = "pass"
	}
	var lease any = "nobody"
	if s.Lease != NoOne {
		lease = mv(s.Lease)
	}
	var up, inRun, unleased []any
	for w := 0; w < Watchers; w++ {
		up = append(up, []any{mv(w), s.Up[w]})
		inRun = append(inRun, []any{mv(w), inRunName(s, w)})
		if s.Unleased[w] {
			unleased = append(unleased, mv(w))
		}
	}
	return map[string]any{
		"lane":       map[bool]string{false: "ordinary", true: "followup"}[s.FollowLane],
		"fence":      s.Fence,
		"follow":     abstractFollow(s.Follow),
		"given":      set(given),
		"posts":      fn(posts),
		"retried":    s.Given[Retry],
		"retryPosts": s.Posts[Retry],
		"buildPosts": s.Posts[Build],
		"runs":       fn(runs),
		"runStarts":  fn(starts),
		"ratPushed":  s.RatPushed,
		"codePushed": s.CodePushed,
		"prs":        s.PRs,
		"pr":         prNames[s.PR],
		"gate":       gate,
		"mergeable":  s.Mergeable,
		"merges":     s.Merges,
		"pred":       predNames[s.Pred],
		"intent":     s.Intent,
		"watched":    s.Given[Watch],
		"watchPosts": s.Posts[Watch],
		"marked":     s.Marked,
		"lease":      lease,
		"up":         fn(up),
		"inRun":      fn(inRun),
		"crashes":    s.Crashes,
		"unleased":   set(unleased),
	}
}

// The explorer uses compact, comparable records. The code uses caller-sized
// journals; only this file limits environment repetition and history storage.
// Each operation contributes at most three authorization/outcome snapshots and
// one CI observation. A head move can add a head observation and another CI
// observation; closures, external merge and renewal supply the remaining slots.
// This is history storage for exploration, not a core capacity or policy hold.
const followSnapshotCapacity = 4*MaxFollowOps + 2*MaxHeadMoves + MaxCloses + 3

var followWords = []string{"", "none", "inactive", "idle", "draft", "create", "repair", "merge", "reopen", "start-run", "running", "run-result", "start-publication", "publication", "watch", "hold", "start-merge", "merge-response", "uncertain", "start-reopen", "reopen-response", "done", "success", "failed", "nochange", "stopped", "invalidated", "unknown", "refused", "open", "closed", "merged", "pass", "fail", "active", "exhausted", "unmergeable", "response", "observation", "ci-failed", "pending", "completed", "cancelled", "authorized", "run-outcome", "superseded-outcome", "creation-outcome", "publication-outcome", "pr-observation", "ci-observation", "writer-renewal", "reopen-outcome", "merge-outcome", "head-observation", "record-run", "record-publication", "record-merge", "record-reopen"}

func word(s string) uint8 {
	for i, v := range followWords {
		if s == v {
			return uint8(i)
		}
	}
	panic("unencoded follow-up value: " + s)
}
func textWord(n uint8) string { return followWords[n] }

var followIdentity = FollowIdentity{"ratified-plan-obligation", "followup-branch", "followup-pr"}
var effectKinds = [5]string{"run", "publication", "merge", "reopen", "answer"}

type smallEffect struct {
	Intent     uint8
	Dispatched bool
	Status     uint8
}
type smallOperation struct {
	Kind, Base, Round, Raw, Result, ResultSnapshot uint8
	Effects                                        [4]smallEffect
}
type smallSnapshot struct{ Op, Kind, Head, Left, Round, Cause, Stage uint8 }
type smallControl struct {
	Phase, Next, Op, N, Left, Round               uint8
	Requested                                     bool
	Consumed                                      uint8
	Answer                                        smallEffect
	Owner                                         int8
	RunFence                                      uint8
	KnownPR                                       bool
	Head, CI, CIHead                              uint8
	CanMerge                                      bool
	MergeHead, MergeOutcome, Proof, Status, Cause uint8
}
type followState struct {
	Control                                                         smallControl
	Ops                                                             [MaxFollowOps]smallOperation
	Snapshots, Accepted                                             [followSnapshotCapacity]smallSnapshot
	Starts, Publications, ExternalDispatches                        [MaxFollowOps]uint8
	Dispatches                                                      [MaxFollowOps + 1][5]uint8
	Lost                                                            [MaxFollowOps + 1][5]bool
	Charged, PublicationRefused                                     [MaxFollowOps]bool
	Holds                                                           [followSnapshotCapacity]bool
	Actors                                                          [Watchers]bool
	Events, DraftFailures, CreateFailures                           uint8
	Barrier, Answers                                                uint8
	Applied, Checks                                                 bool
	RemotePR, RemoteHead, RemoteCI, RemoteCIHead                    uint8
	RemoteCanMerge                                                  bool
	RemoteMergeHead, RemoteMerge                                    uint8
	Creations, Reopens, Merges                                      uint8
	CloseCount, HeadMoves, CIFailures, UnknownMerges, RefusedMerges uint8
	ExternalMerged                                                  bool
}

func smallE(e FollowEffect) smallEffect {
	return smallEffect{uint8(e.Intent), e.Dispatched, word(e.State)}
}
func bigE(e smallEffect) FollowEffect {
	return FollowEffect{Intent: int(e.Intent), Dispatched: e.Dispatched, State: textWord(e.Status)}
}
func smallO(o FollowOperation) smallOperation {
	return smallOperation{uint8(word(o.Kind)), uint8(o.Base), uint8(o.Round), word(o.Raw), word(o.Result), uint8(o.ResultSnapshot), [4]smallEffect{smallE(o.Run), smallE(o.Publication), smallE(o.Merge), smallE(o.Reopen)}}
}
func bigO(o smallOperation) FollowOperation {
	return FollowOperation{Kind: textWord(o.Kind), Base: int(o.Base), Round: int(o.Round), Raw: textWord(o.Raw), Result: textWord(o.Result), ResultSnapshot: int(o.ResultSnapshot), Run: bigE(o.Effects[0]), Publication: bigE(o.Effects[1]), Merge: bigE(o.Effects[2]), Reopen: bigE(o.Effects[3])}
}
func smallS(s FollowSnapshot) smallSnapshot {
	return smallSnapshot{uint8(s.Op), word(s.Kind), uint8(s.Head), uint8(s.Left), uint8(s.Round), word(s.Cause), word(s.Stage)}
}
func bigS(s smallSnapshot) FollowSnapshot {
	return FollowSnapshot{Identity: followIdentity, Op: int(s.Op), Kind: textWord(s.Kind), Head: int(s.Head), Left: int(s.Left), Round: int(s.Round), Cause: textWord(s.Cause), Stage: textWord(s.Stage)}
}
func smallC(c FollowControl) smallControl {
	return smallControl{word(c.Phase), word(c.Next), uint8(c.Op), uint8(c.N), uint8(c.Left), uint8(c.Round), c.Requested, uint8(c.Consumed), smallE(c.Answer), int8(c.Owner), uint8(c.RunFence), c.KnownPR, uint8(c.Head), word(c.CI), uint8(c.CIHead), c.CanMerge, uint8(c.MergeHead), word(c.MergeOutcome), word(c.Proof), word(c.Status), word(c.Cause)}
}
func controlOf(f followState) FollowControl {
	c := f.Control
	return FollowControl{Identity: followIdentity, Phase: textWord(c.Phase), Next: textWord(c.Next), Op: int(c.Op), N: int(c.N), Left: int(c.Left), Round: int(c.Round), Requested: c.Requested, Consumed: int(c.Consumed), Answer: bigE(c.Answer), Owner: int(c.Owner), RunFence: int(c.RunFence), KnownPR: c.KnownPR, Head: int(c.Head), CI: textWord(c.CI), CIHead: int(c.CIHead), CanMerge: c.CanMerge, MergeHead: int(c.MergeHead), MergeOutcome: textWord(c.MergeOutcome), Proof: textWord(c.Proof), Status: textWord(c.Status), Cause: textWord(c.Cause)}
}
func currentOf(f followState) FollowOperation {
	if f.Control.Op == 0 {
		return FollowOperation{Kind: "none", Raw: "none", Result: "none"}
	}
	return bigO(f.Ops[f.Control.Op-1])
}
func initialFollow() followState {
	j := NewFollowJournal(followIdentity, MaxFollowOps)
	f := followState{Control: smallC(j.Control), Applied: true, Checks: true}
	f.RemotePR, f.RemoteCI, f.RemoteMerge = word("none"), word("none"), word("none")
	return f
}
func observationOf(f followState) FollowObservation {
	v := FollowObservation{PR: textWord(f.RemotePR), Head: int(f.RemoteHead), CI: textWord(f.RemoteCI), CIHead: int(f.RemoteCIHead), CanMerge: f.RemoteCanMerge, MergeHead: int(f.RemoteMergeHead), MergeResult: textWord(f.RemoteMerge), ExternalMerged: f.ExternalMerged}
	if f.Control.Op > 0 {
		o := f.Control.Op - 1
		v.PublicationRevision = int(f.Publications[o])
		v.PublicationRefused = f.PublicationRefused[o]
		v.LostMerge = f.Lost[f.Control.Op][2]
	}
	return v
}
func withFollow(s State, x FollowChange) State {
	if !x.OK {
		return s
	}
	f := s.Follow
	j := NewFollowJournal(followIdentity, MaxFollowOps)
	j.Control = controlOf(f)
	for i := 0; i < int(f.Control.Op); i++ {
		j.Operations[i] = bigO(f.Ops[i])
	}
	for i := 0; i < int(f.Control.N); i++ {
		j.Snapshots = append(j.Snapshots, bigS(f.Snapshots[i]))
	}
	if !j.Save(x) {
		return s
	}
	f.Control = smallC(j.Control)
	for i := 0; i < int(f.Control.Op); i++ {
		f.Ops[i] = smallO(j.Operations[i])
	}
	for i, snapshot := range j.Snapshots {
		f.Snapshots[i] = smallS(snapshot)
	}
	if x.Snapshot.Stage != "" {
		f.Accepted[j.Control.N-1] = smallS(x.Snapshot)
	}
	s.Follow = f
	return s
}

// applyFollow records the environment's history of an accepted system step.
// These counters are deliberately independent of the code's ledger booleans.
func applyFollow(s State, x FollowChange, name string, w int, result string, lose bool) State {
	n := withFollow(s, x)
	if !x.OK || n == s {
		return n
	}
	f, old := &n.Follow, s.Follow
	c, o := controlOf(old), currentOf(old)
	id := int(f.Control.Op)
	idx := id - 1
	dispatch := func(k int) { f.Dispatches[id][k]++; f.Actors[w] = true }
	held := func() {
		if f.Control.N > 0 {
			f.Holds[f.Control.N-1] = true
		}
	}
	switch name {
	case "Authorize":
		if x.Operation.Kind == "repair" {
			f.Charged[idx] = true
		}
	case "Start":
		f.Starts[idx]++
		dispatch(0)
	case "Finish":
		if result != "success" {
			f.Events++
			if o.Kind == "draft" {
				f.DraftFailures++
			}
			if o.Kind == "create" {
				f.CreateFailures++
			}
		}
	case "ReconcileRun":
		f.Applied = f.Applied && (x.Operation.Result != "success" || o.Base == x.Control.Head)
		if holdRun(c, o, observationOf(old)) {
			held()
		}
	case "Publish":
		dispatch(1)
		f.Checks = f.Checks && s.Up[w] && s.Lease == w && c.Head == int(old.RemoteHead)
		if o.Kind == "create" {
			f.Creations++
		}
		f.RemotePR = word("open")
		f.RemoteHead++
		f.Publications[idx] = f.RemoteHead
		f.RemoteCI, f.RemoteCIHead, f.RemoteCanMerge, f.RemoteMergeHead = word("none"), 0, false, 0
		if lose {
			f.Events++
			f.Lost[id][1] = true
		}
	case "PublicationRefusal":
		dispatch(1)
		f.PublicationRefused[idx] = true
		f.Events++
		if lose {
			f.Events++
			f.Lost[id][1] = true
		}
	case "ReconcileSuperseded":
		if old.RemotePR == word("closed") || (o.Kind == "repair" && c.Left == 0 && old.RemotePR != word("merged")) {
			held()
		}
	case "ReconcilePublication", "ObservePR":
		if old.RemotePR == word("closed") {
			held()
		}
	case "ObserveCI":
		if old.RemoteCI == word("fail") && c.Left == 0 {
			held()
		}
	case "GiveRequest":
		if c.RepairUnresolved(o) {
			f.Barrier = uint8(c.Op)
		}
	case "AnswerRequest":
		f.Answers++
		f.Dispatches[0][4]++
		f.Actors[w] = true
	case "Reopen":
		dispatch(3)
		f.RemotePR = word("open")
		f.Reopens++
	case "Merge":
		dispatch(2)
		f.Checks = f.Checks && s.Up[w] && s.Lease == w && c.Head == int(old.RemoteHead) && c.CI == "pass" && c.CIHead == c.Head && c.CanMerge && c.MergeHead == c.Head
		f.RemoteMerge = word(result)
		if result != "success" {
			f.Events++
		}
		if result == "unknown" {
			f.UnknownMerges++
		}
		if result == "refused" {
			f.RefusedMerges++
		}
		if result == "success" {
			f.RemotePR = word("merged")
			f.Merges++
		}
		if lose {
			f.Events++
			f.Lost[id][2] = true
		}
	case "ReconcileMerge":
		if x.Control.MergeOutcome != "unknown" {
			f.RemoteMerge = word("none")
		}
	}
	return n
}

func lostCount(f followState, kind, operation string) int {
	count := 0
	for id := 1; id <= int(f.Control.Op); id++ {
		for k, name := range effectKinds {
			if name == kind && f.Lost[id][k] && (operation == "" || textWord(f.Ops[id-1].Kind) == operation) {
				count++
			}
		}
	}
	return count
}
func refusedCount(f followState) int {
	n := 0
	for _, v := range f.PublicationRefused {
		if v {
			n++
		}
	}
	return n
}

func tryFollow(s State, tried func(string, []any, State)) {
	f := s.Follow
	c, o, v := controlOf(f), currentOf(f), observationOf(f)
	for w := 0; w < Watchers; w++ {
		session := FollowSession{ID: w, Lease: s.Lease, Fence: s.Fence, Up: s.Up[w]}
		args := []any{mv(w)}
		// Every internal alternative is attempted, including refused operations.
		run := func(name string, x FollowChange, result string, lose bool, within bool) {
			if !within {
				return
			} // only an environment bound omits an attempt
			n := applyFollow(s, x, name, w, result, lose)
			tried("Act", args, n)
		}
		run("Authorize", c.Authorize(o, v, session, MaxFollowOps), "", false, true)
		run("Start", c.Start(o, v, session, MaxFollowOps), "", false, true)
		for _, r := range []string{"success", "failed", "stopped", "nochange"} {
			within := r == "success" || (int(f.Events) < MaxFollowEvents && (o.Kind != "draft" || int(f.DraftFailures) < MaxDraftFailures) && (o.Kind != "create" || int(f.CreateFailures) < MaxCreateFailures))
			run("Finish", c.Finish(o, v, session, MaxFollowOps, r), r, false, within)
		}
		run("RecoverStopped", c.RecoverStopped(o, v, session, MaxFollowOps), "", false, true)
		run("ReconcileRun", c.ReconcileRun(o, v, session, MaxFollowOps), "", false, true)
		for _, lose := range []bool{false, true} {
			room := !lose || (int(f.Events) < MaxFollowEvents && ((o.Kind == "create" && lostCount(f, "publication", "create") < MaxLostCreations) || (o.Kind != "create" && lostCount(f, "publication", "repair") < MaxLostPublications)))
			run("Publish", c.Publish(o, v, session, MaxFollowOps), "", lose, room)
			room = refusedCount(f) < MaxPublicationRefusals && int(f.Events)+1+b2i(lose) <= MaxFollowEvents && (!lose || lostCount(f, "publication", "create") < MaxLostCreations)
			run("PublicationRefusal", c.PublicationRefusal(o, v, session, MaxFollowOps), "", lose, room)
		}
		run("ReconcileSuperseded", c.ReconcileSuperseded(o, v, session, MaxFollowOps), "", false, true)
		run("ReconcilePublication", c.ReconcilePublication(o, v, session, MaxFollowOps), "", false, true)
		run("ReconcileCreationRefusal", c.ReconcileCreationRefusal(o, v, session, MaxFollowOps), "", false, true)
		run("ObservePR", c.ObservePR(o, v, session, MaxFollowOps), "", false, true)
		run("ObserveCI", c.ObserveCI(o, v, session, MaxFollowOps), "", false, true)
		run("ObserveHead", c.ObserveHead(o, v, session, MaxFollowOps), "", false, true)
		run("ConsumeRequest", c.ConsumeRequest(o, v, session, MaxFollowOps), "", false, true)
		run("RecordAnswer", c.RecordAnswer(o, v, session, MaxFollowOps), "", false, true)
		run("AnswerRequest", c.AnswerRequest(o, v, session, MaxFollowOps), "", false, true)
		run("Reopen", c.Reopen(o, v, session, MaxFollowOps), "", false, true)
		run("ReconcileReopen", c.ReconcileReopen(o, v, session, MaxFollowOps), "", false, true)
		for _, r := range []string{"success", "refused", "unknown"} {
			for _, lose := range []bool{false, true} {
				room := int(f.Events)+b2i(r != "success")+b2i(lose) <= MaxFollowEvents && (r != "unknown" || int(f.UnknownMerges) < MaxUnknownMerges) && (r != "refused" || int(f.RefusedMerges) < MaxRefusedMerges) && (!lose || lostCount(f, "merge", "") < MaxLostMerges)
				run("Merge", c.Merge(o, v, session, MaxFollowOps, r), r, lose, room)
			}
		}
		run("ReconcileMerge", c.ReconcileMerge(o, v, session, MaxFollowOps), "", false, true)
	}
	// FollowOutside is a disjunction in Next, so each environment outcome has
	// that action name and no arguments at the conformance boundary.
	outside := func(n State) { tried("FollowOutside", []any{}, n) }
	outside(applyFollow(s, c.GiveRequest(o, v, FollowSession{}, MaxFollowOps), "GiveRequest", 0, "", false))
	for _, result := range []string{"pass", "fail"} {
		for _, m := range []bool{false, true} {
			if result == "fail" && (int(f.Events) >= MaxFollowEvents || int(f.CIFailures) >= MaxCIFailures) {
				continue
			}
			n := s
			if s.FollowLane && v.PR == "open" && v.CI == "none" {
				n.Follow.RemoteCI = word(result)
				n.Follow.RemoteCIHead = f.RemoteHead
				n.Follow.RemoteCanMerge = m
				n.Follow.RemoteMergeHead = f.RemoteHead
				if result == "fail" {
					n.Follow.Events++
					n.Follow.CIFailures++
				}
			}
			outside(n)
		}
	}
	if int(f.Events) < MaxFollowEvents && int(f.CloseCount) < MaxCloses {
		n := s
		if s.FollowLane && v.PR == "open" {
			n.Follow.RemotePR = word("closed")
			n.Follow.CloseCount++
			n.Follow.Events++
		}
		outside(n)
	}
	if int(f.Events) < MaxFollowEvents {
		n := s
		if s.FollowLane && v.PR == "open" {
			n.Follow.RemotePR = word("merged")
			n.Follow.Merges++
			n.Follow.ExternalMerged = true
			n.Follow.Events++
			for i := 0; i < c.Op; i++ {
				n.Follow.ExternalDispatches[i] = f.Dispatches[i+1][2]
			}
		}
		outside(n)
	}
	if int(f.Events) < MaxFollowEvents && int(f.HeadMoves) < MaxHeadMoves {
		n := s
		if s.FollowLane && (v.PR == "open" || v.PR == "closed") {
			n.Follow.RemoteHead++
			n.Follow.HeadMoves++
			n.Follow.Events++
			n.Follow.RemoteCI = word("none")
			n.Follow.RemoteCIHead = 0
			n.Follow.RemoteCanMerge = false
			n.Follow.RemoteMergeHead = 0
		}
		outside(n)
	}
	for _, result := range []string{"success", "refused"} {
		if result == "refused" && int(f.RefusedMerges) >= MaxRefusedMerges {
			continue
		}
		n := s
		if s.FollowLane && c.Phase == "uncertain" && v.MergeResult == "unknown" && v.PR != "merged" && (result != "success" || (v.PR == "open" && v.Head == o.Base)) {
			n.Follow.RemoteMerge = word(result)
			if result == "refused" {
				n.Follow.RefusedMerges++
			}
			if result == "success" {
				n.Follow.RemotePR = word("merged")
				n.Follow.Merges++
			}
		}
		outside(n)
	}
}

func seq(xs []any) map[string]any {
	if xs == nil {
		xs = []any{}
	}
	return map[string]any{"$seq": xs}
}
func identityAbstract() map[string]any {
	return map[string]any{"source": followIdentity.Source, "branch": followIdentity.Branch, "pr": followIdentity.PR}
}
func snapshotAbstract(s smallSnapshot) map[string]any {
	return map[string]any{"identity": identityAbstract(), "op": int(s.Op), "kind": textWord(s.Kind), "head": int(s.Head), "left": int(s.Left), "round": int(s.Round), "cause": textWord(s.Cause), "stage": textWord(s.Stage)}
}
func abstractFollow(f followState) map[string]any {
	c := controlOf(f)
	var kinds, bases, rounds, starts, raw, results, resultSnapshots, publications, externalDispatches []any
	var intents, dispatches, pending, completed, cancelled, charged, holds, actors, refused, lost, snapshots, accepted []any
	for i := 0; i < c.Op; i++ {
		o := bigO(f.Ops[i])
		kinds = append(kinds, o.Kind)
		bases = append(bases, o.Base)
		rounds = append(rounds, o.Round)
		starts = append(starts, int(f.Starts[i]))
		raw = append(raw, o.Raw)
		results = append(results, o.Result)
		resultSnapshots = append(resultSnapshots, o.ResultSnapshot)
		publications = append(publications, int(f.Publications[i]))
		externalDispatches = append(externalDispatches, int(f.ExternalDispatches[i]))
		if f.Charged[i] {
			charged = append(charged, i+1)
		}
		if f.PublicationRefused[i] {
			refused = append(refused, i+1)
		}
	}
	for i := 0; i <= c.Op; i++ {
		for k, name := range effectKinds {
			e := FollowEffect{}
			if i == 0 && k == 4 {
				e = c.Answer
			}
			if i > 0 && k < 4 {
				e = bigE(f.Ops[i-1].Effects[k])
			}
			key := seq([]any{i, name})
			if e.Intent > 0 {
				intents = append(intents, []any{key, e.Intent})
				dispatches = append(dispatches, []any{key, int(f.Dispatches[i][k])})
				switch e.State {
				case "pending":
					pending = append(pending, key)
				case "completed":
					completed = append(completed, key)
				case "cancelled":
					cancelled = append(cancelled, key)
				}
			}
			if f.Lost[i][k] {
				lost = append(lost, key)
			}
		}
	}
	for i := 0; i < c.N; i++ {
		snapshots = append(snapshots, snapshotAbstract(f.Snapshots[i]))
		accepted = append(accepted, snapshotAbstract(f.Accepted[i]))
		if f.Holds[i] {
			holds = append(holds, i+1)
		}
	}
	for w, v := range f.Actors {
		if v {
			actors = append(actors, mv(w))
		}
	}
	var owner any = "nobody"
	if c.Owner != NoOne {
		owner = mv(c.Owner)
	}
	knownPR := "none"
	if c.KnownPR {
		knownPR = "followup-pr"
	}
	return map[string]any{
		"events": int(f.Events), "draftFailures": int(f.DraftFailures), "createFailures": int(f.CreateFailures),
		"phase": c.Phase, "next": c.Next, "op": c.Op, "kinds": seq(kinds), "bases": seq(bases), "rounds": seq(rounds), "starts": seq(starts), "raw": seq(raw), "results": seq(results), "resultSnapshots": seq(resultSnapshots),
		"snapshots": seq(snapshots), "accepted": seq(accepted), "intents": fn(intents), "pending": set(pending), "completed": set(completed), "cancelled": set(cancelled), "dispatches": fn(dispatches), "charged": set(charged),
		"left": c.Left, "round": c.Round, "holds": set(holds), "requested": c.Requested, "consumed": c.Consumed, "barrier": int(f.Barrier), "answers": int(f.Answers),
		"owner": owner, "runFence": c.RunFence, "applied": f.Applied, "checks": f.Checks, "actors": set(actors), "publicationRefused": set(refused), "externalDispatches": seq(externalDispatches),
		"remotePR": textWord(f.RemotePR), "knownPR": knownPR, "creations": int(f.Creations), "reopens": int(f.Reopens), "remoteHead": int(f.RemoteHead), "head": c.Head, "publications": seq(publications),
		"remoteCI": textWord(f.RemoteCI), "remoteCIHead": int(f.RemoteCIHead), "remoteCanMerge": f.RemoteCanMerge, "remoteMergeHead": int(f.RemoteMergeHead), "ci": c.CI, "ciHead": c.CIHead, "canMerge": c.CanMerge, "mergeHead": c.MergeHead, "merges": int(f.Merges),
		"remoteMerge": textWord(f.RemoteMerge), "mergeOutcome": c.MergeOutcome, "proof": c.Proof, "status": c.Status, "cause": c.Cause, "lost": set(lost),
		"closeCount": int(f.CloseCount), "headMoves": int(f.HeadMoves), "ciFailures": int(f.CIFailures), "unknownMerges": int(f.UnknownMerges), "refusedMerges": int(f.RefusedMerges), "externalMerged": f.ExternalMerged,
	}
}
