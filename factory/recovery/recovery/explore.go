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
	Followup   bool
	Fence      int
	Follow     FollowState
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
	r.Fence = s.Fence
	return r
}

// withRepo reads the code's Repo back into s, counting attempts from its
// latest build's again: after a retry, every build's run a watcher is in is
// an earlier attempt's.
func withRepo(s State, r Repo) State {
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
	s.Fence = r.Fence
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
	// Several underlying operations share one Next action, notably Act(w).
	// Still call every operation, but report an identical refused action only
	// once: repeating the same (action, arguments, state) adds no transition.
	// Keep every changed successor, including distinct outcomes of one action.
	type attemptKey struct{ step, argument string }
	refused := make(map[attemptKey]bool)
	tryAll(s, func(step string, args []any, next State) {
		if next == s {
			key := attemptKey{step: step}
			if len(args) == 1 {
				switch arg := args[0].(type) {
				case string:
					key.argument = "string:" + arg
				case map[string]any:
					key.argument = "model-value:" + arg["$mv"].(string)
				default:
					panic("unsupported Next action argument")
				}
			} else if len(args) != 0 {
				panic("unsupported Next action arity")
			}
			if refused[key] {
				return
			}
			refused[key] = true
		}
		tried(step, args, next)
	})
}

func tryAll(s State, tried func(step string, args []any, next State)) {
	n := s
	if s == Init() {
		n.Followup = true
		n = withFollow(n, NewFollowRepo("ratified-plan-obligation", "followup-branch", "followup-pr", MaxFollowOps, followSnapshotCapacity))
		n.Follow.Applied, n.Follow.Checks = true, true
	}
	tried("ChooseFollowup", []any{}, n)
	if s.Followup {
		tryFollow(s, tried)
		return
	}
	tried("FollowOutside", []any{}, s)
	r := repoOf(s)

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
		if up {
			if t, ok := r.Acquire(wt); ok {
				n = withRepo(s, t)
			}
		}
		tried("Acquire", args, n)

		// Crashes and stalls, left untried only where the environment's
		// bound alone rules them out.
		spare := s.Crashes < MaxCrashes
		if !(up && otherUp(s, w)) {
			tried("Crash", args, s)
		} else if spare {
			n = s
			n.Up[w] = false
			n.InRun[w], n.Earlier[w] = NoRun, false
			n.Crashes++
			tried("Crash", args, n)
		}

		if !up {
			tried("Stall", args, s)
		} else if t, ok := r.Lapse(wt); !ok {
			tried("Stall", args, s)
		} else if spare {
			n = withRepo(s, t)
			n.Crashes++
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

func seq(values []any) map[string]any {
	if values == nil {
		values = []any{}
	}
	return map[string]any{"$seq": values}
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
		"lane":       laneName(s),
		"fence":      s.Fence,
		"follow":     abstractFollow(s),
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

// FollowState stores every follow variable at the model's bounds. The compact
// words and fixed arrays keep State comparable without truncating its journals.
// Each operation uses its durable identity as its array index; zero is reserved
// for the writer answer. Journal capacity is derived storage, not a guard on an
// enabled transition: each operation accepts at most five snapshots, and each
// exceptional input at most five more, with room for renewal and observations.
const followSnapshotCapacity = 5*MaxFollowOps + 5*MaxFollowEvents + 8

const (
	followRun = iota
	followPublication
	followMerge
	followReopen
	followAnswer
	followEffectKinds
)

var followEffectNames = [followEffectKinds]string{"run", "publication", "merge", "reopen", "answer"}

// A word is an index into the spec's finite vocabulary, not an operation ID.
type followWord uint8

var followWords = [...]string{
	"none", "idle", "draft", "create", "repair", "merge", "reopen",
	"record-run", "start-run", "running", "run-result", "record-publication",
	"start-publication", "publication", "watch", "hold", "record-merge",
	"start-merge", "merge-response", "uncertain", "record-reopen", "start-reopen",
	"reopen-response", "done", "success", "failed", "nochange", "stopped",
	"invalidated", "unknown", "refused", "open", "closed", "merged", "pass",
	"fail", "response", "observation", "active", "exhausted", "unmergeable",
	"ci-failed", "authorized", "run-outcome", "superseded-outcome",
	"creation-outcome", "publication-outcome", "pr-observation", "ci-observation",
	"writer-renewal", "reopen-outcome", "merge-outcome", "head-observation",
}

func followCode(word string) followWord {
	for i, name := range followWords {
		if name == word {
			return followWord(i)
		}
	}
	panic("unknown follow-up word: " + word)
}

func (word followWord) name() string { return followWords[word] }

type followSnapshot struct {
	Op, Head, Left, Round uint8
	Kind, Cause, Stage    followWord
}

type followEffect struct {
	Intent                        uint8
	Pending, Completed, Cancelled bool
	Dispatches                    uint8
}

type FollowState struct {
	Phase, Next                                  followWord
	Op, Left, Round, Consumed, Answers, RunFence uint8
	Owner                                        int8
	KnownPR                                      bool
	Head, CIHead, MergeHead                      uint8
	CI, MergeOutcome, Proof, Status, Cause       followWord
	CanMerge                                     bool
	Kinds                                        [MaxFollowOps + 1]followWord
	Bases, Rounds, Starts, ResultSnapshots       [MaxFollowOps + 1]uint8
	Raw, Results                                 [MaxFollowOps + 1]followWord
	Charged                                      [MaxFollowOps + 1]bool
	Effects                                      [MaxFollowOps + 1][followEffectKinds]followEffect
	Snapshots, Accepted                          [followSnapshotCapacity]followSnapshot
	SnapshotCount, AcceptedCount                 uint8
	Events, DraftFailures, CreateFailures        uint8
	Requested                                    bool
	Barrier                                      uint8
	Holds                                        [followSnapshotCapacity + 1]bool
	Applied, Checks                              bool
	Actors                                       [Watchers]bool
	PublicationRefused                           [MaxFollowOps + 1]bool
	ExternalDispatches, Publications             [MaxFollowOps + 1]uint8
	RemotePR, RemoteCI, RemoteMerge              followWord
	Creations, Reopens, RemoteHead               uint8
	RemoteCIHead, RemoteMergeHead, Merges        uint8
	RemoteCanMerge                               bool
	Lost                                         [MaxFollowOps + 1][followEffectKinds]bool
	CloseCount, HeadMoves, CIFailures            uint8
	UnknownMerges, RefusedMerges                 uint8
	ExternalMerged                               bool
}

func compactSnapshot(s FollowSnapshot) followSnapshot {
	return followSnapshot{Op: uint8(s.Op), Head: uint8(s.Head), Left: uint8(s.Left),
		Round: uint8(s.Round), Kind: followCode(s.Kind), Cause: followCode(s.Cause), Stage: followCode(s.Stage)}
}

func expandSnapshot(s followSnapshot) FollowSnapshot {
	return FollowSnapshot{Source: "ratified-plan-obligation", Branch: "followup-branch", PR: "followup-pr",
		Op: int(s.Op), Head: int(s.Head), Left: int(s.Left), Round: int(s.Round),
		Kind: s.Kind.name(), Cause: s.Cause.name(), Stage: s.Stage.name()}
}

// followOf reconstructs only durable system state. Remote state, bounds and
// historical observations stay in the explorer and are supplied as inputs.
func followOf(s State) FollowRepo {
	f := s.Follow
	r := NewFollowRepo("ratified-plan-obligation", "followup-branch", "followup-pr", MaxFollowOps, followSnapshotCapacity)
	r.Phase, r.Next = f.Phase.name(), f.Next.name()
	r.Op, r.Left, r.Round = int(f.Op), int(f.Left), int(f.Round)
	r.Consumed, r.Answers, r.Owner, r.RunFence = int(f.Consumed), int(f.Answers), int(f.Owner), int(f.RunFence)
	r.KnownPR, r.Head = f.KnownPR, int(f.Head)
	r.CI, r.CIHead, r.CanMerge, r.MergeHead = f.CI.name(), int(f.CIHead), f.CanMerge, int(f.MergeHead)
	r.MergeOutcome, r.Proof, r.Status, r.Cause = f.MergeOutcome.name(), f.Proof.name(), f.Status.name(), f.Cause.name()
	for o := 0; o <= MaxFollowOps; o++ {
		r.Kinds[o], r.Bases[o], r.Rounds[o], r.Starts[o] = f.Kinds[o].name(), int(f.Bases[o]), int(f.Rounds[o]), int(f.Starts[o])
		r.Raw[o], r.Results[o], r.ResultSnapshots[o], r.Charged[o] = f.Raw[o].name(), f.Results[o].name(), int(f.ResultSnapshots[o]), f.Charged[o]
		for k := 0; k < followEffectKinds; k++ {
			e := f.Effects[o][k]
			r.Effects[o*followEffectKinds+k] = FollowEffect{Intent: int(e.Intent), Pending: e.Pending,
				Completed: e.Completed, Cancelled: e.Cancelled, Dispatches: int(e.Dispatches)}
		}
	}
	r.SnapshotCount = int(f.SnapshotCount)
	for i := 0; i < r.SnapshotCount; i++ {
		r.Snapshots[i] = expandSnapshot(f.Snapshots[i])
	}
	return r
}

// withFollow copies the core's actual result, leaving environment and history
// fields intact. Accepted snapshots are appended to an independent journal, so
// mutating a previously durable snapshot would remain visible to the model.
func withFollow(s State, r FollowRepo) State {
	f := &s.Follow
	f.Phase, f.Next = followCode(r.Phase), followCode(r.Next)
	f.Op, f.Left, f.Round = uint8(r.Op), uint8(r.Left), uint8(r.Round)
	f.Consumed, f.Answers, f.Owner, f.RunFence = uint8(r.Consumed), uint8(r.Answers), int8(r.Owner), uint8(r.RunFence)
	f.KnownPR, f.Head = r.KnownPR, uint8(r.Head)
	f.CI, f.CIHead, f.CanMerge, f.MergeHead = followCode(r.CI), uint8(r.CIHead), r.CanMerge, uint8(r.MergeHead)
	f.MergeOutcome, f.Proof, f.Status, f.Cause = followCode(r.MergeOutcome), followCode(r.Proof), followCode(r.Status), followCode(r.Cause)
	for o := 0; o <= MaxFollowOps; o++ {
		if o > 0 && o <= r.Op {
			f.Kinds[o], f.Bases[o], f.Rounds[o], f.Starts[o] = followCode(r.Kinds[o]), uint8(r.Bases[o]), uint8(r.Rounds[o]), uint8(r.Starts[o])
			f.Raw[o], f.Results[o], f.ResultSnapshots[o], f.Charged[o] = followCode(r.Raw[o]), followCode(r.Results[o]), uint8(r.ResultSnapshots[o]), r.Charged[o]
		}
		for k := 0; k < followEffectKinds; k++ {
			e := r.Effects[o*followEffectKinds+k]
			f.Effects[o][k] = followEffect{Intent: uint8(e.Intent), Pending: e.Pending,
				Completed: e.Completed, Cancelled: e.Cancelled, Dispatches: uint8(e.Dispatches)}
		}
	}
	f.SnapshotCount = uint8(r.SnapshotCount)
	for i := 0; i < r.SnapshotCount; i++ {
		f.Snapshots[i] = compactSnapshot(r.Snapshots[i])
	}
	for int(f.AcceptedCount) < r.SnapshotCount {
		f.Accepted[f.AcceptedCount] = compactSnapshot(r.Snapshots[f.AcceptedCount])
		f.AcceptedCount++
	}
	return s
}

func followInput(s State, w int) FollowInput {
	f, o := s.Follow, s.Follow.Op
	return FollowInput{Watcher: w, Lease: s.Lease, Fence: s.Fence, Operation: int(o), Up: s.Up[w], Requested: f.Requested,
		RemotePR: f.RemotePR.name(), RemoteHead: int(f.RemoteHead), RemoteCI: f.RemoteCI.name(),
		RemoteCIHead: int(f.RemoteCIHead), RemoteCanMerge: f.RemoteCanMerge, RemoteMergeHead: int(f.RemoteMergeHead),
		RemoteMerge: f.RemoteMerge.name(), Publication: int(f.Publications[o]),
		PublicationRefused: f.PublicationRefused[o], MergeLost: f.Lost[o][followMerge], ExternalMerged: f.ExternalMerged}
}

func laneName(s State) string {
	if s.Followup {
		return "followup"
	}
	return "ordinary"
}

func abstractSnapshot(s followSnapshot) map[string]any {
	return map[string]any{
		"identity": map[string]any{"source": "ratified-plan-obligation", "branch": "followup-branch", "pr": "followup-pr"},
		"op":       s.Op, "kind": s.Kind.name(), "head": s.Head, "left": s.Left,
		"round": s.Round, "cause": s.Cause.name(), "stage": s.Stage.name(),
	}
}

func abstractFollow(s State) any {
	if !s.Followup {
		return "inactive"
	}
	f := s.Follow
	kinds, bases, rounds, starts, raw, results, resultSnapshots := []any{}, []any{}, []any{}, []any{}, []any{}, []any{}, []any{}
	externalDispatches, publications, charged, refused := []any{}, []any{}, []any{}, []any{}
	for o := 1; o <= int(f.Op); o++ {
		kinds = append(kinds, f.Kinds[o].name())
		bases = append(bases, f.Bases[o])
		rounds = append(rounds, f.Rounds[o])
		starts = append(starts, f.Starts[o])
		raw = append(raw, f.Raw[o].name())
		results = append(results, f.Results[o].name())
		resultSnapshots = append(resultSnapshots, f.ResultSnapshots[o])
		externalDispatches = append(externalDispatches, f.ExternalDispatches[o])
		publications = append(publications, f.Publications[o])
		if f.Charged[o] {
			charged = append(charged, o)
		}
		if f.PublicationRefused[o] {
			refused = append(refused, o)
		}
	}
	intents, dispatches, pending, completed, cancelled, lost := []any{}, []any{}, []any{}, []any{}, []any{}, []any{}
	for o := 0; o <= int(f.Op); o++ {
		for k, name := range followEffectNames {
			e, key := f.Effects[o][k], seq([]any{o, name})
			if e.Intent > 0 {
				intents = append(intents, []any{key, e.Intent})
				dispatches = append(dispatches, []any{key, e.Dispatches})
			}
			if e.Pending {
				pending = append(pending, key)
			}
			if e.Completed {
				completed = append(completed, key)
			}
			if e.Cancelled {
				cancelled = append(cancelled, key)
			}
			if f.Lost[o][k] {
				lost = append(lost, key)
			}
		}
	}
	snapshots, accepted, holds, actors := []any{}, []any{}, []any{}, []any{}
	for i := 0; i < int(f.SnapshotCount); i++ {
		snapshots = append(snapshots, abstractSnapshot(f.Snapshots[i]))
	}
	for i := 0; i < int(f.AcceptedCount); i++ {
		accepted = append(accepted, abstractSnapshot(f.Accepted[i]))
	}
	for i, held := range f.Holds {
		if held {
			holds = append(holds, i)
		}
	}
	for w, acted := range f.Actors {
		if acted {
			actors = append(actors, mv(w))
		}
	}
	var owner any = "nobody"
	if f.Owner != NoOne {
		owner = mv(int(f.Owner))
	}
	knownPR := "none"
	if f.KnownPR {
		knownPR = "followup-pr"
	}
	return map[string]any{
		"events": f.Events, "draftFailures": f.DraftFailures, "createFailures": f.CreateFailures,
		"phase": f.Phase.name(), "next": f.Next.name(), "op": f.Op,
		"kinds": seq(kinds), "bases": seq(bases), "rounds": seq(rounds), "starts": seq(starts),
		"raw": seq(raw), "results": seq(results), "resultSnapshots": seq(resultSnapshots),
		"snapshots": seq(snapshots), "accepted": seq(accepted), "intents": fn(intents),
		"pending": set(pending), "completed": set(completed), "cancelled": set(cancelled), "dispatches": fn(dispatches),
		"charged": set(charged), "left": f.Left, "round": f.Round, "holds": set(holds),
		"requested": f.Requested, "consumed": f.Consumed, "barrier": f.Barrier, "answers": f.Answers,
		"owner": owner, "runFence": f.RunFence, "applied": f.Applied, "checks": f.Checks, "actors": set(actors),
		"publicationRefused": set(refused), "externalDispatches": seq(externalDispatches),
		"remotePR": f.RemotePR.name(), "knownPR": knownPR, "creations": f.Creations, "reopens": f.Reopens,
		"remoteHead": f.RemoteHead, "head": f.Head, "publications": seq(publications),
		"remoteCI": f.RemoteCI.name(), "remoteCIHead": f.RemoteCIHead, "remoteCanMerge": f.RemoteCanMerge,
		"remoteMergeHead": f.RemoteMergeHead, "ci": f.CI.name(), "ciHead": f.CIHead,
		"canMerge": f.CanMerge, "mergeHead": f.MergeHead, "merges": f.Merges,
		"remoteMerge": f.RemoteMerge.name(), "mergeOutcome": f.MergeOutcome.name(), "proof": f.Proof.name(),
		"status": f.Status.name(), "cause": f.Cause.name(), "lost": set(lost),
		"closeCount": f.CloseCount, "headMoves": f.HeadMoves, "ciFailures": f.CIFailures,
		"unknownMerges": f.UnknownMerges, "refusedMerges": f.RefusedMerges, "externalMerged": f.ExternalMerged,
	}
}

// tryFollow retains the shared lease transitions and tries each follow-up
// operation through the core. External inputs are free choices at each effect;
// only the declared repetition bounds can omit an otherwise enabled choice.
func tryFollow(s State, tried func(step string, args []any, next State)) {
	for _, c := range []int{Solve, Ratify, Note} {
		tried("Give", []any{keyNames[c]}, s)
	}
	for _, step := range []string{"GiveRetry", "GiveWatch", "CIPass", "OthersClose", "OthersMerge"} {
		tried(step, []any{}, s)
	}
	r := repoOf(s)
	n := s
	if s.Lease != NoOne && !s.Up[s.Lease] {
		if t, ok := r.Expire(); ok {
			n = withRepo(s, t)
		}
	}
	tried("Expire", []any{}, n)
	f := followOf(s)
	for w := 0; w < Watchers; w++ {
		args, wt := []any{mv(w)}, watcherOf(s, w)
		n = s
		if s.Up[w] && (s.Crashes > 0 || w == 0) {
			if t, ok := r.AcquireFenced(wt); ok {
				n = withRepo(s, t)
			}
		}
		tried("Acquire", args, n)
		spare := s.Crashes < MaxCrashes && s.Crashes < MaxFollowCrashes && s.Follow.Events < MaxFollowEvents
		if !(s.Up[w] && otherUp(s, w)) {
			tried("Crash", args, s)
		} else if spare {
			n = s
			n.Up[w] = false
			n.InRun[w], n.Earlier[w] = NoRun, false
			n.Crashes++
			n.Follow.Events++
			tried("Crash", args, n)
		}
		if !s.Up[w] {
			tried("Stall", args, s)
		} else if t, ok := r.Lapse(wt); !ok {
			tried("Stall", args, s)
		} else if spare {
			n = withRepo(s, t)
			n.Crashes++
			n.Follow.Events++
			tried("Stall", args, n)
		}
		n = s
		if !s.Up[w] {
			if v, ok := r.Restart(wt); ok {
				n.Up[w] = true
				n = withWatcher(n, w, v)
			}
		}
		tried("Restart", args, n)
		for _, action := range []string{"FAuthorize", "FStart", "FRecoverStopped", "FReconcileRun", "FReconcileSuperseded",
			"FReconcilePublication", "FReconcileCreationRefusal", "FObservePR", "FObserveCI", "FObserveHead",
			"FConsumeRequest", "FRecordAnswer", "FAnswer", "FReopen", "FReconcileReopen", "FReconcileMerge"} {
			tryFollowAct(s, f, w, action, "none", false, tried)
		}
		for _, result := range []string{"success", "failed", "stopped", "nochange"} {
			tryFollowAct(s, f, w, "FFinish", result, false, tried)
		}
		for _, lose := range []bool{false, true} {
			tryFollowAct(s, f, w, "FPublish", "none", lose, tried)
			tryFollowAct(s, f, w, "FPublicationRefusal", "none", lose, tried)
			for _, result := range []string{"success", "refused", "unknown"} {
				tryFollowAct(s, f, w, "FMerge", result, lose, tried)
			}
		}
		tried("FinishRun", args, s)
		n = s
		if s.Up[w] {
			if v, ok := r.DropRun(wt); ok {
				n = withWatcher(s, w, v)
			}
		}
		tried("DropRun", args, n)
	}
	tryFollowOutside(s, tried)
	tried("Rest", []any{}, s)
}

func followLosses(f FollowState, effect int, kind string) int {
	n := 0
	for o := 0; o <= int(f.Op); o++ {
		if f.Lost[o][effect] && (kind == "" || f.Kinds[o].name() == kind) {
			n++
		}
	}
	return n
}

func tryFollowAct(s State, r FollowRepo, w int, action, result string, lose bool, tried func(string, []any, State)) {
	f, o := s.Follow, int(s.Follow.Op)
	kind := f.Kinds[o].name()
	// These are environmental repetition bounds, not recovery policy.
	switch action {
	case "FFinish":
		if result != "success" && (f.Events >= MaxFollowEvents ||
			(kind == "draft" && f.DraftFailures >= MaxDraftFailures) ||
			(kind == "create" && f.CreateFailures >= MaxCreateFailures)) {
			return
		}
	case "FPublish":
		if lose && (f.Events >= MaxFollowEvents ||
			(kind == "create" && followLosses(f, followPublication, "create") >= MaxLostCreations) ||
			(kind != "create" && followLosses(f, followPublication, "repair") >= MaxLostPublications)) {
			return
		}
	case "FPublicationRefusal":
		refusals := 0
		for _, refused := range f.PublicationRefused {
			if refused {
				refusals++
			}
		}
		if refusals >= MaxPublicationRefusals || int(f.Events)+1+b2i(lose) > MaxFollowEvents ||
			(lose && followLosses(f, followPublication, "create") >= MaxLostCreations) {
			return
		}
	case "FMerge":
		if int(f.Events)+b2i(result != "success")+b2i(lose) > MaxFollowEvents ||
			(result == "unknown" && f.UnknownMerges >= MaxUnknownMerges) ||
			(result == "refused" && f.RefusedMerges >= MaxRefusedMerges) ||
			(lose && followLosses(f, followMerge, "") >= MaxLostMerges) {
			return
		}
	}
	x := followInput(s, w)
	x.Result = result
	// Invoke Step for refusals too. Its refusal contract preserves every
	// scalar and slice element, so only enabled operations need fresh storage.
	candidate := r
	if r.CanStep(action, x) {
		candidate = followOf(s)
	}
	n := s
	if t, ok := candidate.Step(action, x); ok {
		n = withFollow(s, t)
		g := &n.Follow
		switch action {
		case "FStart", "FAnswer":
			g.Actors[w] = true
		case "FFinish":
			if result != "success" {
				g.Events++
				if kind == "draft" {
					g.DraftFailures++
				}
				if kind == "create" {
					g.CreateFailures++
				}
			}
		case "FReconcileRun":
			g.Applied = f.Applied && (g.Results[o].name() != "success" || f.Bases[o] == g.Head)
		case "FPublish":
			g.Checks = f.Checks && s.Up[w] && s.Lease == w && f.Head == f.RemoteHead
			g.Actors[w] = true
			g.RemotePR = followCode("open")
			if kind == "create" {
				g.Creations++
			}
			g.RemoteHead++
			g.Publications[o] = g.RemoteHead
			g.RemoteCI, g.RemoteCIHead, g.RemoteCanMerge, g.RemoteMergeHead = followCode("none"), 0, false, 0
			if lose {
				g.Lost[o][followPublication] = true
				g.Events++
			}
		case "FPublicationRefusal":
			g.PublicationRefused[o] = true
			g.Actors[w] = true
			g.Events += uint8(1 + b2i(lose))
			if lose {
				g.Lost[o][followPublication] = true
			}
		case "FReopen":
			g.RemotePR = followCode("open")
			g.Reopens++
			g.Actors[w] = true
		case "FMerge":
			g.Checks = f.Checks && s.Up[w] && s.Lease == w && f.Head == f.RemoteHead &&
				f.CI.name() == "pass" && f.CIHead == f.Head && f.CanMerge && f.MergeHead == f.Head
			g.Actors[w] = true
			g.RemoteMerge = followCode(result)
			g.Events += uint8(b2i(result != "success") + b2i(lose))
			if result == "unknown" {
				g.UnknownMerges++
			}
			if result == "refused" {
				g.RefusedMerges++
			}
			if result == "success" {
				g.RemotePR = followCode("merged")
				g.Merges++
			}
			if lose {
				g.Lost[o][followMerge] = true
			}
		case "FReconcileMerge":
			if g.MergeOutcome.name() != "unknown" {
				g.RemoteMerge = followCode("none")
			}
		}
		switch action {
		case "FReconcileRun", "FReconcileSuperseded", "FReconcilePublication", "FObservePR", "FObserveCI":
			if g.Phase.name() == "hold" {
				g.Holds[g.SnapshotCount] = true
			}
		}
	}
	tried("Act", []any{mv(w)}, n)
}

func tryFollowOutside(s State, tried func(string, []any, State)) {
	f := s.Follow
	outside := func(n State) { tried("FollowOutside", []any{}, n) }
	n := s
	unresolved := f.Kinds[f.Op].name() == "repair" && f.ResultSnapshots[f.Op] == 0
	if !f.Requested && (f.Phase.name() == "hold" || unresolved) {
		n.Follow.Requested = true
		if unresolved {
			n.Follow.Barrier = f.Op
		}
	}
	outside(n)
	for _, result := range []string{"pass", "fail"} {
		for _, mergeable := range []bool{false, true} {
			if result == "fail" && (f.CIFailures >= MaxCIFailures || f.Events >= MaxFollowEvents) {
				continue
			}
			n = s
			if f.RemotePR.name() == "open" && f.RemoteCI.name() == "none" {
				n.Follow.RemoteCI, n.Follow.RemoteCIHead = followCode(result), f.RemoteHead
				n.Follow.RemoteCanMerge, n.Follow.RemoteMergeHead = mergeable, f.RemoteHead
				if result == "fail" {
					n.Follow.CIFailures++
					n.Follow.Events++
				}
			}
			outside(n)
		}
	}
	if f.CloseCount < MaxCloses && f.Events < MaxFollowEvents {
		n = s
		if f.RemotePR.name() == "open" {
			n.Follow.RemotePR = followCode("closed")
			n.Follow.CloseCount++
			n.Follow.Events++
		}
		outside(n)
	}
	if f.Events < MaxFollowEvents {
		n = s
		if f.RemotePR.name() == "open" {
			n.Follow.RemotePR = followCode("merged")
			n.Follow.Merges++
			n.Follow.ExternalMerged = true
			n.Follow.Events++
			for o := 1; o <= int(f.Op); o++ {
				n.Follow.ExternalDispatches[o] = f.Effects[o][followMerge].Dispatches
			}
		}
		outside(n)
	}
	if f.HeadMoves < MaxHeadMoves && f.Events < MaxFollowEvents {
		n = s
		if f.RemotePR.name() == "open" || f.RemotePR.name() == "closed" {
			n.Follow.RemoteHead++
			n.Follow.RemoteCI = followCode("none")
			n.Follow.RemoteCIHead, n.Follow.RemoteCanMerge, n.Follow.RemoteMergeHead = 0, false, 0
			n.Follow.HeadMoves++
			n.Follow.Events++
		}
		outside(n)
	}
	for _, result := range []string{"success", "refused"} {
		if result == "refused" && f.RefusedMerges >= MaxRefusedMerges {
			continue
		}
		n = s
		if f.Phase.name() == "uncertain" && f.RemoteMerge.name() == "unknown" && f.RemotePR.name() != "merged" &&
			(result != "success" || (f.RemotePR.name() == "open" && f.RemoteHead == f.Bases[f.Op])) {
			n.Follow.RemoteMerge = followCode(result)
			if result == "refused" {
				n.Follow.RefusedMerges++
			}
			if result == "success" {
				n.Follow.RemotePR = followCode("merged")
				n.Follow.Merges++
			}
		}
		outside(n)
	}
}
