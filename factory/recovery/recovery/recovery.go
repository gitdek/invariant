// +gobra

package recovery

// This is the core a watcher runs each step of an issue through, so that
// every effect on GitHub happens exactly once across crashes, restarts and a
// second watcher. A Repo is what every watcher can see: the issue's commands
// and the factory's posts, the recorded agent runs, the branch, the latest
// build's pull request, the draft a retry replaces and the repository's
// lease. A Watcher is what one running watcher remembers, which a crash loses.
//
// Every operation looks for its effect before taking it, checks the lease
// just before it, and refuses when the effect is already there or isn't due.
//
// A writer can retry after each build that stopped or left a failed draft,
// and each retry starts a new build, with its own agent run, that replaces
// that draft (#271). So Posted[Retry], Posted[Build], Runs[Build] and the
// pull request's fields are the latest retry's and the latest build's. Each
// build has an attempt number, and a watcher in a build's run remembers the
// attempt it started in, so a run an earlier build started is stale.

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

// The latest build's pull request.
const (
	PRNone   = 0 // none yet
	PRDraft  = 1 // open as a draft: the build failed the factory's checks or review
	PRReady  = 2 // open and ready
	PRClosed = 3 // closed by someone else
	PRMerged = 4 // merged, by the factory or someone else
)

// The draft the latest retry replaces.
const (
	PredNone    = 0 // there was none
	PredDraft   = 1 // still open
	PredRetired = 2 // closed by the factory, as the retry's record says
	PredClosed  = 3 // closed by someone else
	PredMerged  = 4 // merged by someone else
)

// NoOne holds the lease when nobody does, and NoRun is a watcher in no
// agent run. MaxAttempt is the most retries an issue may have, so the
// attempt number never overflows; no issue comes near it.
const (
	NoOne      = -1
	NoRun      = -1
	MaxAttempt = 1 << 30
)

// Repo is the shared, visible state of one issue in one repository.
type Repo struct {
	Given      [NumKeys]bool // commands a writer has given; Given[Retry] once any retry is
	Posted     [NumKeys]bool // a factory post answers the step
	Runs       [NumKeys]int  // the agent run recorded for the step
	RatPushed  bool          // the branch holds the ratification
	CodePushed bool          // the latest build's code is pushed
	Opened     bool          // the latest build opened its pull request
	PR         int           // that pull request: PRNone, PRDraft, PRReady, PRClosed or PRMerged
	GatePassed bool          // CI's gate passed on that pull request's head
	Mergeable  bool          // GitHub can merge it
	Merged     bool          // the factory merged it
	Pred       int           // the draft the latest retry replaces: a Pred value
	Intent     bool          // the latest retry's record that it replaces that draft is stored
	Attempt    int           // the latest build's attempt: how many retries started a build
	Lease      int           // the watcher holding the lease, or NoOne
}

// Watcher is one running watcher's memory.
type Watcher struct {
	ID      int // the watcher's name for the lease
	InRun   int // the step whose agent run it's waiting on, or NoRun
	Attempt int // the attempt of the build whose agent run it started
}

// NewRepo is an issue with no commands, posts, runs, branch or pull request,
// and a lease nobody holds.
// @ ensures r.Lease == NoOne
// @ ensures !r.RatPushed && !r.CodePushed && !r.Opened && r.PR == PRNone
// @ ensures !r.GatePassed && !r.Mergeable && !r.Merged && r.Pred == PredNone && !r.Intent && r.Attempt == 0
// @ ensures forall j int :: { r.Given[j] } 0 <= j && j < NumKeys ==> !r.Given[j]
// @ ensures forall j int :: { r.Posted[j] } 0 <= j && j < NumKeys ==> !r.Posted[j]
// @ ensures forall j int :: { r.Runs[j] } 0 <= j && j < NumKeys ==> r.Runs[j] == RunNone
func NewRepo() (r Repo) {
	r.Lease = NoOne
	return r
}

// NewWatcher is a watcher that has just started, remembering nothing.
// @ requires 0 <= id
// @ ensures w.ID == id && w.InRun == NoRun && w.Attempt == 0 && w.Ok()
func NewWatcher(id int) (w Watcher) {
	w.ID = id
	w.InRun = NoRun
	return w
}

// @ decreases
// @ pure
func (w Watcher) Ok() bool {
	return 0 <= w.ID && NoRun <= w.InRun && w.InRun < NumKeys && 0 <= w.Attempt
}

// @ decreases
// @ pure
func IsCmd(k int) bool {
	return 0 <= k && k < NumCmds
}

// @ decreases
// @ pure
func IsRunKey(k int) bool {
	return k == Solve || k == Build
}

// Replacing says the latest retry builds again: there was no draft to
// replace, or its record says it replaces the draft, which is closed. A
// retry whose draft someone merged, or closed before the record was stored,
// is answered without a build.
// @ decreases
// @ pure
func (r Repo) Replacing() bool {
	return r.Pred == PredNone || (r.Intent && (r.Pred == PredRetired || r.Pred == PredClosed))
}

// Triggered says step k has been asked for: its command was given, the
// ratification's or the latest retry's answer asks for a build, or the gate
// passed on a pull request that isn't a failed draft.
// @ requires 0 <= k && k < NumKeys
// @ decreases
// @ pure
func (r Repo) Triggered(k int) bool {
	return (IsCmd(k) && r.Given[k]) ||
		(k == Build && r.Given[Retry] && r.Posted[Retry] && r.Replacing()) ||
		(k == Build && !r.Given[Retry] && r.Posted[Ratify]) ||
		(k == Merge && r.GatePassed && r.PR != PRDraft)
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
		k == Note ||
		(k == Retry && r.Pred != PredDraft) ||
		(k == Build && r.Runs[Build] == RunDone && r.CodePushed && r.Opened) ||
		(k == Merge && (r.Merged || !r.Mergeable || r.PR == PRClosed || r.PR == PRMerged))
}

// Settled says no command, and no merge, waits for an answer, so a writer's
// next retry is the factory's next step on the issue.
// @ decreases
// @ pure
func (r Repo) Settled() bool {
	return !r.Active(Solve) && !r.Active(Ratify) && !r.Active(Note) && !r.Active(Merge)
}

// Holds says w holds the lease and isn't waiting on an agent run.
// @ decreases
// @ pure
func (r Repo) Holds(w Watcher) bool {
	return r.Lease == w.ID && w.InRun == NoRun
}

// samePR says s's latest pull request is r's.
// @ decreases
// @ pure
func (r Repo) samePR(s Repo) bool {
	return s.Opened == r.Opened && s.PR == r.PR && s.GatePassed == r.GatePassed && s.Mergeable == r.Mergeable && s.Merged == r.Merged
}

// sameDraft says s's latest retry is r's: the same attempt, replacing r's
// draft, with r's record.
// @ decreases
// @ pure
func (r Repo) sameDraft(s Repo) bool {
	return s.Pred == r.Pred && s.Intent == r.Intent && s.Attempt == r.Attempt
}

// Stale says w is in an agent run an earlier build started, before the
// latest retry started the latest build.
// @ requires w.Ok()
// @ decreases
// @ pure
func (r Repo) Stale(w Watcher) bool {
	return w.InRun == Build && w.Attempt != r.Attempt
}

// @ requires 0 <= c && c < NumKeys
// @ decreases
// @ pure
func (r Repo) CanGive(c int) bool {
	return (IsCmd(c) && c != Retry && !r.Given[c] && (c != Ratify || r.Posted[Solve])) ||
		(c == Retry && r.Posted[Build] && (r.PR == PRNone || r.PR == PRDraft) && !r.Merged && r.Settled() && 0 <= r.Attempt && r.Attempt < MaxAttempt)
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

// @ decreases
// @ pure
func (r Repo) CanRecordIntent(w Watcher) bool {
	return r.Holds(w) && r.Active(Retry) && r.Pred == PredDraft && !r.Intent
}

// @ decreases
// @ pure
func (r Repo) CanRetire(w Watcher) bool {
	return r.Holds(w) && r.Active(Retry) && r.Pred == PredDraft && r.Intent
}

// @ requires 0 <= k && k < NumKeys
// @ decreases
// @ pure
func (r Repo) CanPushCode(w Watcher, k int) bool {
	return r.Holds(w) && k == Build && r.Active(k) && r.Runs[k] == RunDone && !r.CodePushed
}

// @ requires 0 <= k && k < NumKeys
// @ decreases
// @ pure
func (r Repo) CanOpenPullRequest(w Watcher, k int) bool {
	return r.Holds(w) && k == Build && r.Active(k) && r.Runs[k] == RunDone && r.CodePushed && !r.Opened
}

// @ requires 0 <= k && k < NumKeys
// @ decreases
// @ pure
func (r Repo) CanMerge(w Watcher, k int) bool {
	return r.Holds(w) && k == Merge && r.Active(k) && r.Mergeable && r.PR == PRReady && !r.Merged
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

// Give records a writer's command c, when the issue protocol allows it. A
// retry, once the latest build was answered, stopped or left a failed draft,
// and every command before it is answered, starts a new build: with no run,
// code, pull request or gate of its own yet, replacing that draft. After a
// build that stopped, the draft an earlier retry replaced is still the one
// it replaces, with that retry's record. The new build is the next attempt.
// @ requires 0 <= c && c < NumKeys
// @ ensures ok == r.CanGive(c)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.Given[c] && s.Lease == r.Lease && s.RatPushed == r.RatPushed
// @ ensures ok ==> forall j int :: { s.Given[j] } 0 <= j && j < NumKeys && j != c ==> s.Given[j] == r.Given[j]
// @ ensures ok && c != Retry ==> s.Posted == r.Posted && s.Runs == r.Runs && s.CodePushed == r.CodePushed && r.samePR(s) && r.sameDraft(s)
// @ ensures ok && c == Retry ==> !s.Posted[Retry] && !s.Posted[Build] && s.Runs[Build] == RunNone
// @ ensures ok && c == Retry ==> forall j int :: { s.Posted[j] } 0 <= j && j < NumKeys && j != Retry && j != Build ==> s.Posted[j] == r.Posted[j]
// @ ensures ok && c == Retry ==> forall j int :: { s.Runs[j] } 0 <= j && j < NumKeys && j != Build ==> s.Runs[j] == r.Runs[j]
// @ ensures ok && c == Retry ==> !s.CodePushed && !s.Opened && s.PR == PRNone && !s.GatePassed && !s.Mergeable && s.Merged == r.Merged
// @ ensures ok && c == Retry ==> s.Attempt == r.Attempt + 1
// @ ensures ok && c == Retry && r.PR == PRDraft ==> s.Pred == PredDraft && !s.Intent
// @ ensures ok && c == Retry && r.PR != PRDraft ==> s.Pred == r.Pred && s.Intent == r.Intent
func (r Repo) Give(c int) (s Repo, ok bool) {
	if !r.CanGive(c) {
		return r, false
	}
	s = r
	g := r.Given
	g[c] = true
	s.Given = g
	if c == Retry {
		p := r.Posted
		p[Retry] = false
		p[Build] = false
		s.Posted = p
		rs := r.Runs
		rs[Build] = RunNone
		s.Runs = rs
		s.CodePushed = false
		s.Opened = false
		if r.PR == PRDraft {
			s.Pred = PredDraft
			s.Intent = false
		}
		s.PR = PRNone
		s.GatePassed = false
		s.Mergeable = false
		s.Attempt = r.Attempt + 1
	}
	return s, true
}

// PassGate records CI's gate passing on the latest build's pull request's
// head, and whether GitHub can merge it.
// @ ensures ok == ((r.PR == PRDraft || r.PR == PRReady) && !r.GatePassed)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.GatePassed && s.Mergeable == m
// @ ensures ok ==> s.Given == r.Given && s.Posted == r.Posted && s.Runs == r.Runs && s.Lease == r.Lease
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && s.Opened == r.Opened && s.PR == r.PR && s.Merged == r.Merged
// @ ensures ok ==> r.sameDraft(s)
func (r Repo) PassGate(m bool) (s Repo, ok bool) {
	if !((r.PR == PRDraft || r.PR == PRReady) && !r.GatePassed) {
		return r, false
	}
	s = r
	s.GatePassed = true
	s.Mergeable = m
	return s, true
}

// OthersClosePR records someone else closing the latest build's open pull
// request.
// @ ensures ok == (r.PR == PRDraft || r.PR == PRReady)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.PR == PRClosed
// @ ensures ok ==> s.Given == r.Given && s.Posted == r.Posted && s.Runs == r.Runs && s.Lease == r.Lease
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && s.Opened == r.Opened
// @ ensures ok ==> s.GatePassed == r.GatePassed && s.Mergeable == r.Mergeable && s.Merged == r.Merged && r.sameDraft(s)
func (r Repo) OthersClosePR() (s Repo, ok bool) {
	if !(r.PR == PRDraft || r.PR == PRReady) {
		return r, false
	}
	s = r
	s.PR = PRClosed
	return s, true
}

// OthersMergePR records someone else merging the latest build's open pull
// request.
// @ ensures ok == (r.PR == PRDraft || r.PR == PRReady)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.PR == PRMerged
// @ ensures ok ==> s.Given == r.Given && s.Posted == r.Posted && s.Runs == r.Runs && s.Lease == r.Lease
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && s.Opened == r.Opened
// @ ensures ok ==> s.GatePassed == r.GatePassed && s.Mergeable == r.Mergeable && s.Merged == r.Merged && r.sameDraft(s)
func (r Repo) OthersMergePR() (s Repo, ok bool) {
	if !(r.PR == PRDraft || r.PR == PRReady) {
		return r, false
	}
	s = r
	s.PR = PRMerged
	return s, true
}

// OthersCloseDraft records someone else closing the draft the latest retry
// replaces, while it's open.
// @ ensures ok == (r.Pred == PredDraft)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.Pred == PredClosed && s.Intent == r.Intent && s.Attempt == r.Attempt
// @ ensures ok ==> s.Given == r.Given && s.Posted == r.Posted && s.Runs == r.Runs && s.Lease == r.Lease
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && r.samePR(s)
func (r Repo) OthersCloseDraft() (s Repo, ok bool) {
	if r.Pred != PredDraft {
		return r, false
	}
	s = r
	s.Pred = PredClosed
	return s, true
}

// OthersMergeDraft records someone else merging the draft the latest retry
// replaces, while it's open.
// @ ensures ok == (r.Pred == PredDraft)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.Pred == PredMerged && s.Intent == r.Intent && s.Attempt == r.Attempt
// @ ensures ok ==> s.Given == r.Given && s.Posted == r.Posted && s.Runs == r.Runs && s.Lease == r.Lease
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && r.samePR(s)
func (r Repo) OthersMergeDraft() (s Repo, ok bool) {
	if r.Pred != PredDraft {
		return r, false
	}
	s = r
	s.Pred = PredMerged
	return s, true
}

// Acquire takes the lease for w, atomically, when nobody holds it.
// @ requires w.Ok()
// @ ensures ok == (r.Lease == NoOne)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.Lease == w.ID
// @ ensures ok ==> s.Given == r.Given && s.Posted == r.Posted && s.Runs == r.Runs
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && r.samePR(s) && r.sameDraft(s)
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
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && r.samePR(s) && r.sameDraft(s)
func (r Repo) Lapse(w Watcher) (s Repo, ok bool) {
	if r.Lease != w.ID {
		return r, false
	}
	s = r
	s.Lease = NoOne
	return s, true
}

// Expire lets the lease run out, because its holder stopped renewing it. It
// refuses when nobody holds the lease.
// @ ensures ok == (r.Lease != NoOne)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.Lease == NoOne
// @ ensures ok ==> s.Given == r.Given && s.Posted == r.Posted && s.Runs == r.Runs
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && r.samePR(s) && r.sameDraft(s)
func (r Repo) Expire() (s Repo, ok bool) {
	if r.Lease == NoOne {
		return r, false
	}
	s = r
	s.Lease = NoOne
	return s, true
}

// Restart starts w afresh, remembering nothing, once the lease it may have
// held before it went down has run out.
// @ requires w.Ok()
// @ ensures ok == (r.Lease != w.ID)
// @ ensures !ok ==> v == w
// @ ensures ok ==> v.ID == w.ID && v.InRun == NoRun && v.Ok()
func (r Repo) Restart(w Watcher) (v Watcher, ok bool) {
	if r.Lease == w.ID {
		return w, false
	}
	return NewWatcher(w.ID), true
}

// StartRun records step k's agent run, then starts it, when none was
// recorded. The watcher remembers the attempt it started in.
// @ requires w.Ok() && 0 <= k && k < NumKeys
// @ requires 0 <= r.Attempt
// @ ensures ok == r.CanStartRun(w, k)
// @ ensures !ok ==> s == r && v == w
// @ ensures ok ==> s.Runs[k] == RunRecorded && v.ID == w.ID && v.InRun == k && v.Attempt == r.Attempt && v.Ok()
// @ ensures ok ==> forall j int :: { s.Runs[j] } 0 <= j && j < NumKeys && j != k ==> s.Runs[j] == r.Runs[j]
// @ ensures ok ==> s.Given == r.Given && s.Posted == r.Posted && s.Lease == r.Lease
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && r.samePR(s) && r.sameDraft(s)
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
	v.Attempt = r.Attempt
	return s, v, true
}

// FinishRun records that w's agent run finished, while w holds the lease. A
// stale run, which an earlier build started, is recorded as its own, so it
// changes nothing of the latest build.
// @ requires w.Ok()
// @ ensures ok == r.CanFinishRun(w)
// @ ensures !ok ==> s == r && v == w
// @ ensures ok && !r.Stale(w) ==> 0 <= w.InRun && w.InRun < NumKeys && s.Runs[w.InRun] == RunDone
// @ ensures ok && !r.Stale(w) ==> forall j int :: { s.Runs[j] } 0 <= j && j < NumKeys && j != w.InRun ==> s.Runs[j] == r.Runs[j]
// @ ensures ok && r.Stale(w) ==> s.Runs == r.Runs
// @ ensures ok ==> v.ID == w.ID && v.InRun == NoRun && v.Ok()
// @ ensures ok ==> s.Given == r.Given && s.Posted == r.Posted && s.Lease == r.Lease
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && r.samePR(s) && r.sameDraft(s)
func (r Repo) FinishRun(w Watcher) (s Repo, v Watcher, ok bool) {
	if !r.CanFinishRun(w) {
		return r, w, false
	}
	s = r
	if !r.Stale(w) {
		rs := r.Runs
		rs[w.InRun] = RunDone
		s.Runs = rs
	}
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
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && r.samePR(s) && r.sameDraft(s)
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
// @ ensures ok ==> s.CodePushed == r.CodePushed && r.samePR(s) && r.sameDraft(s)
func (r Repo) PushRatification(w Watcher, k int) (s Repo, ok bool) {
	if !r.CanPushRatification(w, k) {
		return r, false
	}
	s = r
	s.RatPushed = true
	return s, true
}

// RecordIntent stores the latest retry's record that it replaces the open
// draft, where every watcher can read it, before the draft is closed: so
// after a crash the factory's own close is never taken for someone else's.
// @ requires w.Ok()
// @ ensures ok == r.CanRecordIntent(w)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.Intent && s.Pred == r.Pred && s.Attempt == r.Attempt
// @ ensures ok ==> s.Given == r.Given && s.Posted == r.Posted && s.Runs == r.Runs && s.Lease == r.Lease
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && r.samePR(s)
func (r Repo) RecordIntent(w Watcher) (s Repo, ok bool) {
	if !r.CanRecordIntent(w) {
		return r, false
	}
	s = r
	s.Intent = true
	return s, true
}

// Retire closes the draft the latest retry replaces, as its record says.
// @ requires w.Ok()
// @ ensures ok == r.CanRetire(w)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.Pred == PredRetired && s.Intent == r.Intent && s.Attempt == r.Attempt
// @ ensures ok ==> s.Given == r.Given && s.Posted == r.Posted && s.Runs == r.Runs && s.Lease == r.Lease
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && r.samePR(s)
func (r Repo) Retire(w Watcher) (s Repo, ok bool) {
	if !r.CanRetire(w) {
		return r, false
	}
	s = r
	s.Pred = PredRetired
	return s, true
}

// PushCode pushes the latest build's code, unless it's already pushed.
// @ requires w.Ok() && 0 <= k && k < NumKeys
// @ ensures ok == r.CanPushCode(w, k)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.CodePushed && s.Given == r.Given && s.Posted == r.Posted && s.Runs == r.Runs && s.Lease == r.Lease
// @ ensures ok ==> s.RatPushed == r.RatPushed && r.samePR(s) && r.sameDraft(s)
func (r Repo) PushCode(w Watcher, k int) (s Repo, ok bool) {
	if !r.CanPushCode(w, k) {
		return r, false
	}
	s = r
	s.CodePushed = true
	return s, true
}

// OpenPullRequest opens the latest build's pull request, unless it opened
// one already, even one since closed or merged: ready when the build passed
// the factory's checks and review, and as a draft when it didn't.
// @ requires w.Ok() && 0 <= k && k < NumKeys
// @ ensures ok == r.CanOpenPullRequest(w, k)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.Opened && (ready ==> s.PR == PRReady) && (!ready ==> s.PR == PRDraft)
// @ ensures ok ==> s.Given == r.Given && s.Posted == r.Posted && s.Runs == r.Runs && s.Lease == r.Lease
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed
// @ ensures ok ==> s.GatePassed == r.GatePassed && s.Mergeable == r.Mergeable && s.Merged == r.Merged && r.sameDraft(s)
func (r Repo) OpenPullRequest(w Watcher, k int, ready bool) (s Repo, ok bool) {
	if !r.CanOpenPullRequest(w, k) {
		return r, false
	}
	s = r
	s.Opened = true
	if ready {
		s.PR = PRReady
	} else {
		s.PR = PRDraft
	}
	return s, true
}

// MergePullRequest merges the latest build's ready pull request, unless it's
// already merged.
// @ requires w.Ok() && 0 <= k && k < NumKeys
// @ ensures ok == r.CanMerge(w, k)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.Merged && s.PR == PRMerged && s.Given == r.Given && s.Posted == r.Posted && s.Runs == r.Runs && s.Lease == r.Lease
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && s.Opened == r.Opened
// @ ensures ok ==> s.GatePassed == r.GatePassed && s.Mergeable == r.Mergeable && r.sameDraft(s)
func (r Repo) MergePullRequest(w Watcher, k int) (s Repo, ok bool) {
	if !r.CanMerge(w, k) {
		return r, false
	}
	s = r
	s.Merged = true
	s.PR = PRMerged
	return s, true
}

// Post answers step k, once its other effects are there and no answer is:
// a command, the merge, the latest retry once the draft it replaces is
// closed or merged, or the latest build with its pull request.
// @ requires w.Ok() && 0 <= k && k < NumKeys
// @ ensures ok == r.CanPost(w, k)
// @ ensures !ok ==> s == r
// @ ensures ok ==> s.Posted[k] && s.Given == r.Given && s.Runs == r.Runs && s.Lease == r.Lease
// @ ensures ok ==> forall j int :: { s.Posted[j] } 0 <= j && j < NumKeys && j != k ==> s.Posted[j] == r.Posted[j]
// @ ensures ok ==> s.RatPushed == r.RatPushed && s.CodePushed == r.CodePushed && r.samePR(s) && r.sameDraft(s)
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
