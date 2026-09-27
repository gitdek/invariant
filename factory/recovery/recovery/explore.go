package recovery

// The model's bounds.
const (
	MaxCrashes = 2
	Watchers   = 2
)

// runKeys lists the steps that start an agent run, in the order State keeps them.
var runKeys = [3]int{Solve, Build, Retry}

// State is the model's state at its bounds. Watchers are 0..Watchers-1, a
// Lease of NoOne is "nobody", and an InRun of NoRun is "none".
type State struct {
	Given      [NumCmds]bool
	Posts      [NumKeys]int
	Runs       [len(runKeys)]int
	RunStarts  [len(runKeys)]int
	RatPushed  bool
	CodePushed bool
	PRs        int
	Gate       bool
	Mergeable  bool
	Merges     int
	Lease      int
	Up         [Watchers]bool
	InRun      [Watchers]int
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
	r.PROpen = s.PRs > 0
	r.Merged = s.Merges > 0
	r.GatePassed = s.Gate
	r.Mergeable = s.Mergeable
	r.Lease = s.Lease
	return r
}

// withRepo reads the code's Repo back into s.
func withRepo(s State, r Repo) State {
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
	s.PRs = b2i(r.PROpen)
	s.Merges = b2i(r.Merged)
	s.Gate = r.GatePassed
	s.Mergeable = r.Mergeable
	s.Lease = r.Lease
	return s
}

func watcherOf(s State, w int) Watcher {
	return Watcher{ID: w, InRun: s.InRun[w]}
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

// Successors are the states one Next step from s.
func Successors(s State) []State {
	var out []State
	r := repoOf(s)

	// A writer gives a command.
	for c := 0; c < NumCmds; c++ {
		if t, ok := r.Give(c); ok {
			out = append(out, withRepo(s, t))
		}
	}

	// CI's gate passes, with the pull request mergeable or not.
	for _, m := range []bool{false, true} {
		if t, ok := r.PassGate(m); ok {
			out = append(out, withRepo(s, t))
		}
	}

	// The lease of a crashed holder runs out.
	if s.Lease != NoOne && !s.Up[s.Lease] {
		if t, ok := r.Lapse(watcherOf(s, s.Lease)); ok {
			out = append(out, withRepo(s, t))
		}
	}

	for w := 0; w < Watchers; w++ {
		if !s.Up[w] {
			// Restart, remembering nothing.
			if s.Lease != w {
				n := s
				n.Up[w] = true
				n.InRun[w] = NewWatcher(w).InRun
				out = append(out, n)
			}
			continue
		}
		wt := watcherOf(s, w)

		if t, ok := r.Acquire(wt); ok {
			out = append(out, withRepo(s, t))
		}

		// Crash, while some other watcher keeps running.
		otherUp := false
		for v := 0; v < Watchers; v++ {
			if v != w && s.Up[v] {
				otherUp = true
			}
		}
		if s.Crashes < MaxCrashes && otherUp {
			n := s
			n.Up[w] = false
			n.InRun[w] = NewWatcher(w).InRun
			n.Crashes++
			out = append(out, n)
		}

		// Stall past the lease.
		if s.Crashes < MaxCrashes {
			if t, ok := r.Lapse(wt); ok {
				n := withRepo(s, t)
				n.Crashes++
				out = append(out, n)
			}
		}

		// Act: the next effect of some step.
		for k := 0; k < NumKeys; k++ {
			if t, v, ok := r.StartRun(wt, k); ok {
				n := withRepo(s, t)
				n.InRun[w] = v.InRun
				n.RunStarts[runIndex(k)]++
				out = append(out, n)
			}
			if t, ok := r.ReportStopped(wt, k); ok {
				out = append(out, withRepo(s, t))
			}
			if t, ok := r.PushRatification(wt, k); ok {
				out = append(out, withRepo(s, t))
			}
			if t, ok := r.PushCode(wt, k); ok {
				out = append(out, withRepo(s, t))
			}
			if t, ok := r.OpenPullRequest(wt, k); ok {
				out = append(out, withRepo(s, t))
			}
			if t, ok := r.MergePullRequest(wt, k); ok {
				out = append(out, withRepo(s, t))
			}
			if t, ok := r.Post(wt, k); ok {
				out = append(out, withRepo(s, t))
			}
		}

		if t, v, ok := r.FinishRun(wt); ok {
			n := withRepo(s, t)
			n.InRun[w] = v.InRun
			out = append(out, n)
		}
		if v, ok := r.DropRun(wt); ok {
			n := s
			n.InRun[w] = v.InRun
			out = append(out, n)
		}
	}
	return out
}
