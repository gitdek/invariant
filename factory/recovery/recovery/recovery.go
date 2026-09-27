// +gobra

package recovery

// This is the core a watcher runs each step of an issue through, so that
// every effect on GitHub happens exactly once across crashes, restarts and a
// second watcher. A Repo is what every watcher can see: the issue's commands
// and the factory's posts, the recorded agent runs, the branch, the pull
// request and the repository's lease. A Watcher is what one running watcher
// remembers, which a crash loses.
//
// Every operation looks for its effect before taking it, checks the lease
// just before it, and refuses when the effect is already there or isn't due.

// The steps the factory answers with a post. The first NumCmds are commands
// a writer gives; Build and Merge answer events.
const (
	Solve   = 0
	Ratify  = 1
	Note    = 2
	Retry   = 3
	Build   = 4
	Merge   = 5
	NumCmds = 4
	NumKeys = 6
)

// An agent run as recorded where every watcher can read it.
const (
	RunNone     = 0
	RunRecorded = 1
	RunDone     = 2
)

// NoOne holds the lease when nobody does; NoRun is a watcher in no agent run.
const (
	NoOne = -1
	NoRun = -1
)

// Repo is the shared, visible state of one issue in one repository.
type Repo struct {
	Given      [NumKeys]bool // commands a writer has given
	Posted     [NumKeys]bool // a factory post answers the step
	Runs       [NumKeys]int  // the agent run recorded for the step
	RatPushed  bool          // the branch holds the ratification
	CodePushed bool          // the branch holds the code
	PROpen     bool          // a pull request is open from the branch
	Merged     bool          // the pull request is merged
	GatePassed bool          // CI's gate passed on the pull request's head
	Mergeable  bool          // GitHub can merge the pull request
	Lease      int           // the watcher holding the lease, or NoOne
}

// Watcher is one running watcher's memory.
type Watcher struct {
	ID    int // the watcher's name for the lease
	InRun int // the step whose agent run it's waiting on, or NoRun
}

// NewRepo is an issue with no commands, posts, runs, branch or pull request,
// and a lease nobody holds.
// @ ensures r.Lease == NoOne
// @ ensures !r.RatPushed && !r.CodePushed && !r.PROpen && !r.Merged
// @ ensures !r.GatePassed && !r.Mergeable
// @ ensures forall j int :: { r.Given[j] } 0 <= j && j < NumKeys ==> !r.Given[j]
// @ ensures forall j int :: { r.Posted[j] } 0 <= j && j < NumKeys ==> !r.Posted[j]
// @ ensures forall j int :: { r.Runs[j] } 0 <= j && j < NumKeys ==> r.Runs[j] == RunNone
func NewRepo() (r Repo) {
	r.Lease = NoOne
	return r
}

// NewWatcher is a watcher that has just started, remembering nothing.
// @ requires 0 <= id
// @ ensures w.ID == id && w.InRun == NoRun && w.Ok()
func NewWatcher(id int) (w Watcher) {
	w.ID = id
	w.InRun = NoRun
	return w
}

// @ decreases
// @ pure
func (w Watcher) Ok() bool {
	return 0 <= w.ID && NoRun <= w.InRun && w.InRun < NumKeys
}

// @ decreases
// @ pure
func IsCmd(k int) bool {
	return 0 <= k && k < NumCmds
}

// @ decreases
// @ pure
func IsRunKey(k int) bool {
	return k == Solve || k == Build || k == Retry
}

// Triggered says step k has been asked for: its command was given, the
// ratification was answered, or the gate passed.
// @ requires 0 <= k && k < NumKeys
// @ decreases
// @ pure
func (r Repo) Triggered(k int) bool {
	return (IsCmd(k) && r.Given[k]) || (k == Build && r.Posted[Ratify]) || (k == Merge && r.GatePassed)
}

// Active says step k is asked for and no post answers it yet.
// @ requires 0 <= k && k < NumKeys
// @ decreases
// @ pure
func (r Repo) Active(k int) bool {
	return r.Triggered(k) && !r.Posted[k]
}

// EffectsDone says every effect of step k that comes before its post is there.
// @ requires 0 <= k && k < NumKeys
// @ decreases
// @ pure
func (r Repo) EffectsDone(k int) bool {
	return (k == Solve && r.Runs[Solve] == RunDone) ||
		(k == Ratify && r.RatPushed) ||
		((k == Build || k == Retry) && r.Runs[k] == RunDone && r.CodePushed && r.PROpen) ||
		(k == Merge && (r.Merged || !r.Mergeable)) ||
		k == Note
}

// Holds says w holds the lease and isn't waiting on an agent run.
// @ decreases
// @ pure
func (r Repo) Holds(w Watcher) bool {
	return r.Lease == w.ID && w.InRun == NoRun
}

// @ requires 0 <= c && c < NumKeys
// @ decreases
// @ pure
func (r Repo) CanGive(c int) bool {
	return IsCmd(c) && !r.Given[c] &&
		(c != Ratify || r.Posted[Solve]) &&
		(c != Retry || (r.Posted[Build] && !r.PROpen))
}

// @ requires 0 <= k && k < NumKeys
// @ decreases
// @ pure
func (r Repo) CanStartRun(w Watcher, k int) bool {
	return r.Holds(w) && IsRunKey(k) && r.Active(k) && r.Runs[k] == RunNone
}

// @ requires 0 <= k && k < NumKeys
// @ decreases
// @ pure
func (r Repo) CanReportStopped(w Watcher, k int) bool {
	return r.Holds(w) && IsRunKey(k) && r.Active(k) && r.Runs[k] == RunRecorded
}

// @ requires 0 <= k && k < NumKeys
// @ decreases
// @ pure
func (r Repo) CanPushRatification(w Watcher, k int) bool {
	return r.Holds(w) && k == Ratify && r.Active(k) && !r.RatPushed
}

// @ requires 0 <= k && k < NumKeys
// @ decreases
// @ pure
func (r Repo) CanPushCode(w Watcher, k int) bool {
	return r.Holds(w) && (k == Build || k == Retry) && r.Active(k) && r.Runs[k] == RunDone && !r.CodePushed
}

// @ requires 0 <= k && k < NumKeys
// @ decreases
// @ pure
func (r Repo) CanOpenPullRequest(w Watcher, k int) bool {
	return r.Holds(w) && (k == Build || k == Retry) && r.Active(k) && r.Runs[k] == RunDone && r.CodePushed && !r.PROpen
}

// @ requires 0 <= k && k < NumKeys
// @ decreases
// @ pure
func (r Repo) CanMerge(w Watcher, k int) bool {
	return r.Holds(w) && k == Merge && r.Active(k) && r.Mergeable && !r.Merged
}

// @ requires 0 <= k && k < NumKeys
// @ decreases
// @ pure
func (r Repo) CanPost(w Watcher, k int) bool {
	return r.Holds(w) && r.Active(k) && r.EffectsDone(k)
}

// @ requires w.Ok()
// @ decreases
// @ pure
func (r Repo) CanFinishRun(w Watcher) bool {
	return r.Lease == w.ID && w.InRun != NoRun
}

// Give records a writer's command c, when the issue protocol allows it.
// @ requires 0 <= c && c < NumKeys
// @ ensures ok == r.CanGive(c)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.Given[c] && s.Posted == r.Posted && s.Runs == r.Runs && s.Lease == r.Lease
// @ ensures ok ==> forall j int :: { s.Given[j] } 0 <= j && j < NumKeys && j != c ==> s.Given[j] == r.Given[j]
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && s.PROpen == r.PROpen
// @ ensures ok ==> s.Merged == r.Merged && s.GatePassed == r.GatePassed && s.Mergeable == r.Mergeable
func (r Repo) Give(c int) (s Repo, ok bool) {
	if !r.CanGive(c) {
		return r, false
	}
	g := r.Given
	g[c] = true
	s = r
	s.Given = g
	return s, true
}

// PassGate records CI's gate passing on the pull request's head, and whether
// GitHub can merge it.
// @ ensures ok == (r.PROpen && !r.GatePassed)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.GatePassed && s.Mergeable == m
// @ ensures ok ==> s.Given == r.Given && s.Posted == r.Posted && s.Runs == r.Runs && s.Lease == r.Lease
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && s.PROpen == r.PROpen && s.Merged == r.Merged
func (r Repo) PassGate(m bool) (s Repo, ok bool) {
	if !(r.PROpen && !r.GatePassed) {
		return r, false
	}
	s = r
	s.GatePassed = true
	s.Mergeable = m
	return s, true
}

// Acquire takes the lease for w, atomically, when nobody holds it.
// @ requires w.Ok()
// @ ensures ok == (r.Lease == NoOne)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.Lease == w.ID
// @ ensures ok ==> s.Given == r.Given && s.Posted == r.Posted && s.Runs == r.Runs
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && s.PROpen == r.PROpen
// @ ensures ok ==> s.Merged == r.Merged && s.GatePassed == r.GatePassed && s.Mergeable == r.Mergeable
func (r Repo) Acquire(w Watcher) (s Repo, ok bool) {
	if r.Lease != NoOne {
		return r, false
	}
	s = r
	s.Lease = w.ID
	return s, true
}

// Lapse lets w's lease run out, because w stopped renewing it.
// @ requires w.Ok()
// @ ensures ok == (r.Lease == w.ID)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.Lease == NoOne
// @ ensures ok ==> s.Given == r.Given && s.Posted == r.Posted && s.Runs == r.Runs
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && s.PROpen == r.PROpen
// @ ensures ok ==> s.Merged == r.Merged && s.GatePassed == r.GatePassed && s.Mergeable == r.Mergeable
func (r Repo) Lapse(w Watcher) (s Repo, ok bool) {
	if r.Lease != w.ID {
		return r, false
	}
	s = r
	s.Lease = NoOne
	return s, true
}

// StartRun records step k's agent run, then starts it, when none was recorded.
// @ requires w.Ok() && 0 <= k && k < NumKeys
// @ ensures ok == r.CanStartRun(w, k)
// @ ensures !ok ==> s == r && v == w
// @ ensures ok ==> s.Runs[k] == RunRecorded && v.ID == w.ID && v.InRun == k && v.Ok()
// @ ensures ok ==> forall j int :: { s.Runs[j] } 0 <= j && j < NumKeys && j != k ==> s.Runs[j] == r.Runs[j]
// @ ensures ok ==> s.Given == r.Given && s.Posted == r.Posted && s.Lease == r.Lease
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && s.PROpen == r.PROpen
// @ ensures ok ==> s.Merged == r.Merged && s.GatePassed == r.GatePassed && s.Mergeable == r.Mergeable
func (r Repo) StartRun(w Watcher, k int) (s Repo, v Watcher, ok bool) {
	if !r.CanStartRun(w, k) {
		return r, w, false
	}
	rs := r.Runs
	rs[k] = RunRecorded
	s = r
	s.Runs = rs
	v = w
	v.InRun = k
	return s, v, true
}

// FinishRun records that w's agent run finished, while w holds the lease.
// @ requires w.Ok()
// @ ensures ok == r.CanFinishRun(w)
// @ ensures !ok ==> s == r && v == w
// @ ensures ok ==> 0 <= w.InRun && s.Runs[w.InRun] == RunDone
// @ ensures ok ==> forall j int :: { s.Runs[j] } 0 <= j && j < NumKeys && j != w.InRun ==> s.Runs[j] == r.Runs[j]
// @ ensures ok ==> v.ID == w.ID && v.InRun == NoRun && v.Ok()
// @ ensures ok ==> s.Given == r.Given && s.Posted == r.Posted && s.Lease == r.Lease
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && s.PROpen == r.PROpen
// @ ensures ok ==> s.Merged == r.Merged && s.GatePassed == r.GatePassed && s.Mergeable == r.Mergeable
func (r Repo) FinishRun(w Watcher) (s Repo, v Watcher, ok bool) {
	if !r.CanFinishRun(w) {
		return r, w, false
	}
	rs := r.Runs
	rs[w.InRun] = RunDone
	s = r
	s.Runs = rs
	v = w
	v.InRun = NoRun
	return s, v, true
}

// DropRun gives up w's agent run, recording nothing, once w finds it lost the
// lease.
// @ requires w.Ok()
// @ ensures ok == (r.Lease != w.ID && w.InRun != NoRun)
// @ ensures !ok ==> v == w
// @ ensures ok ==> v.ID == w.ID && v.InRun == NoRun && v.Ok()
func (r Repo) DropRun(w Watcher) (v Watcher, ok bool) {
	if !(r.Lease != w.ID && w.InRun != NoRun) {
		return w, false
	}
	v = w
	v.InRun = NoRun
	return v, true
}

// ReportStopped answers step k by saying its agent run stopped partway: it
// was recorded and never finished. It starts no other run.
// @ requires w.Ok() && 0 <= k && k < NumKeys
// @ ensures ok == r.CanReportStopped(w, k)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.Posted[k] && s.Given == r.Given && s.Runs == r.Runs && s.Lease == r.Lease
// @ ensures ok ==> forall j int :: { s.Posted[j] } 0 <= j && j < NumKeys && j != k ==> s.Posted[j] == r.Posted[j]
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && s.PROpen == r.PROpen
// @ ensures ok ==> s.Merged == r.Merged && s.GatePassed == r.GatePassed && s.Mergeable == r.Mergeable
func (r Repo) ReportStopped(w Watcher, k int) (s Repo, ok bool) {
	if !r.CanReportStopped(w, k) {
		return r, false
	}
	p := r.Posted
	p[k] = true
	s = r
	s.Posted = p
	return s, true
}

// PushRatification pushes the ratification to the issue's branch, unless the
// branch already holds it.
// @ requires w.Ok() && 0 <= k && k < NumKeys
// @ ensures ok == r.CanPushRatification(w, k)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.RatPushed && s.Given == r.Given && s.Posted == r.Posted && s.Runs == r.Runs && s.Lease == r.Lease
// @ ensures ok ==> s.CodePushed == r.CodePushed && s.PROpen == r.PROpen
// @ ensures ok ==> s.Merged == r.Merged && s.GatePassed == r.GatePassed && s.Mergeable == r.Mergeable
func (r Repo) PushRatification(w Watcher, k int) (s Repo, ok bool) {
	if !r.CanPushRatification(w, k) {
		return r, false
	}
	s = r
	s.RatPushed = true
	return s, true
}

// PushCode pushes the built code to the issue's branch, unless the branch
// already holds it.
// @ requires w.Ok() && 0 <= k && k < NumKeys
// @ ensures ok == r.CanPushCode(w, k)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.CodePushed && s.Given == r.Given && s.Posted == r.Posted && s.Runs == r.Runs && s.Lease == r.Lease
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.PROpen == r.PROpen
// @ ensures ok ==> s.Merged == r.Merged && s.GatePassed == r.GatePassed && s.Mergeable == r.Mergeable
func (r Repo) PushCode(w Watcher, k int) (s Repo, ok bool) {
	if !r.CanPushCode(w, k) {
		return r, false
	}
	s = r
	s.CodePushed = true
	return s, true
}

// OpenPullRequest opens the pull request from the branch, unless one is
// already open.
// @ requires w.Ok() && 0 <= k && k < NumKeys
// @ ensures ok == r.CanOpenPullRequest(w, k)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.PROpen && s.Given == r.Given && s.Posted == r.Posted && s.Runs == r.Runs && s.Lease == r.Lease
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed
// @ ensures ok ==> s.Merged == r.Merged && s.GatePassed == r.GatePassed && s.Mergeable == r.Mergeable
func (r Repo) OpenPullRequest(w Watcher, k int) (s Repo, ok bool) {
	if !r.CanOpenPullRequest(w, k) {
		return r, false
	}
	s = r
	s.PROpen = true
	return s, true
}

// MergePullRequest merges the pull request, unless it's already merged.
// @ requires w.Ok() && 0 <= k && k < NumKeys
// @ ensures ok == r.CanMerge(w, k)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.Merged && s.Given == r.Given && s.Posted == r.Posted && s.Runs == r.Runs && s.Lease == r.Lease
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && s.PROpen == r.PROpen
// @ ensures ok ==> s.GatePassed == r.GatePassed && s.Mergeable == r.Mergeable
func (r Repo) MergePullRequest(w Watcher, k int) (s Repo, ok bool) {
	if !r.CanMerge(w, k) {
		return r, false
	}
	s = r
	s.Merged = true
	return s, true
}

// Post answers step k, once its other effects are there and no answer is.
// @ requires w.Ok() && 0 <= k && k < NumKeys
// @ ensures ok == r.CanPost(w, k)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.Posted[k] && s.Given == r.Given && s.Runs == r.Runs && s.Lease == r.Lease
// @ ensures ok ==> forall j int :: { s.Posted[j] } 0 <= j && j < NumKeys && j != k ==> s.Posted[j] == r.Posted[j]
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && s.PROpen == r.PROpen
// @ ensures ok ==> s.Merged == r.Merged && s.GatePassed == r.GatePassed && s.Mergeable == r.Mergeable
func (r Repo) Post(w Watcher, k int) (s Repo, ok bool) {
	if !r.CanPost(w, k) {
		return r, false
	}
	p := r.Posted
	p[k] = true
	s = r
	s.Posted = p
	return s, true
}
