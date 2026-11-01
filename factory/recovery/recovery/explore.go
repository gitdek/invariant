package recovery

import "strconv"

// The model's bounds.
const (
	MaxCrashes = 2
	Watchers   = 2
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
