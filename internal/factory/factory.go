package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/gitdek/invariant/factory/protocol/protocol"
	"github.com/gitdek/invariant/factory/recovery/recovery"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/scope"
	"github.com/gitdek/invariant/internal/synth"
)

// GitHub is what the factory does on GitHub. github.Client is the real one.
type GitHub interface {
	OpenIssues(ctx context.Context) ([]github.Issue, error)
	ClosedIssues(ctx context.Context, since string) ([]github.Issue, error)
	// Issues lists issues in every state; an empty label includes them all.
	Issues(ctx context.Context, label string) ([]github.Issue, error)
	// Issue reads one issue as it is now, open or closed.
	Issue(ctx context.Context, n int) (github.Issue, error)
	Comments(ctx context.Context, issue int) ([]github.Comment, error)
	Events(ctx context.Context, issue int) ([]github.Event, error)
	Permission(ctx context.Context, login string) (string, error)
	PostComment(ctx context.Context, issue int, body string) (github.Comment, error)
	// CreateIssue opens an issue, as the factory does for each step of a
	// ratified plan of issues (#126).
	CreateIssue(ctx context.Context, is github.NewIssue) (github.Issue, error)
	EnsureLabel(ctx context.Context, name, color, description string) error
	AddLabels(ctx context.Context, issue int, labels ...string) error
	RemoveLabel(ctx context.Context, issue int, label string) error
	CreatePullRequest(ctx context.Context, pr github.NewPullRequest) (github.PullRequest, error)
	PullRequest(ctx context.Context, n int) (github.PullRequest, error)
	// DescribePullRequest replaces a pull request's description, as
	// publication does for one a crash left open without its handoff.
	DescribePullRequest(ctx context.Context, n int, body string) (github.PullRequest, error)
	// OpenPullRequest finds the open pull request from a branch, if there is one.
	OpenPullRequest(ctx context.Context, branch string) (github.PullRequest, bool, error)
	// PullRequestFrom finds the newest pull request from a branch, in any
	// state, if there is one.
	PullRequestFrom(ctx context.Context, branch string) (github.PullRequest, bool, error)
	// MergedPullRequests lists the merged pull requests among those closed
	// most recently, newest first.
	MergedPullRequests(ctx context.Context) ([]github.PullRequest, error)
	// ClosePullRequest closes a pull request without merging it, and
	// returns it as it is then: closing a closed one changes nothing, and a
	// merged one stays merged.
	ClosePullRequest(ctx context.Context, n int) (github.PullRequest, error)
	CheckRuns(ctx context.Context, sha, name string) ([]github.CheckRun, error)
	// Job reads the job behind a check run, to tell a gate that failed from
	// one GitHub never started.
	Job(ctx context.Context, id int64) (github.Job, error)
	Merge(ctx context.Context, n int, sha, method string) (string, error)
	// MarkReady marks a draft pull request ready for review, since GitHub
	// won't merge a draft (#145).
	MarkReady(ctx context.Context, n int) error
	DeleteBranch(ctx context.Context, branch string) error
}

// Formalizer drafts statements for an issue. formalize.Formalizer is the
// real one.
type Formalizer interface {
	Formalize(ctx context.Context, req formalize.Request, out string) (*formalize.Result, error)
}

// Builder writes and gates the code for a ratified project in dir, leaving
// the finished project in out/result. For an amendment, it starts from the
// project's existing code.
type Builder interface {
	Build(ctx context.Context, dir, out string, amend bool) (*synth.Result, error)
}

// Repo is the factory's own clone of the repository. Clone is the real one.
type Repo interface {
	Fetch(ctx context.Context) error
	Dirs(ctx context.Context, ref, under string) ([]string, error)
	Worktree(ctx context.Context, branch, from string) (string, error)
	RemoveWorktree(ctx context.Context, dir string) error
	ClearWorktrees(ctx context.Context) error
	// Commit commits everything under dir. Each of written that exists is
	// committed whatever the repository's ignore rules say.
	Commit(ctx context.Context, worktree, dir, message string, written ...string) (string, error)
	Push(ctx context.Context, worktree, branch string) error
	RevParse(ctx context.Context, ref string) (string, error)
	Show(ctx context.Context, ref, file string) ([]byte, error)
	Repository(ctx context.Context, ref string) (*repository.Snapshot, error)
	Workflows(ctx context.Context, ref string) (map[string]string, error)
	Export(ctx context.Context, ref string, paths []string, dst string) error
	Scope(ctx context.Context, base, head string, issue int) (scope.Result, error)
	Lease(ctx context.Context) (sha string, rec LeaseRecord, err error)
	PushLease(ctx context.Context, old string, rec LeaseRecord) (string, error)
	// Runs and merges are recorded before they happen (D-0069).
	Run(ctx context.Context, issue int, step string) (RunState, string, error)
	Record(ctx context.Context, issue int, step, what string) error
	Recorded(ctx context.Context, issue int, step string) (string, error)
	Finish(ctx context.Context, issue int, step, result string) error
	Save(ctx context.Context, dir, message, parent string) (string, error)
	Load(ctx context.Context, commit, dir string) error
	Holds(ctx context.Context, ref, commit string) (bool, error)
	PushCommit(ctx context.Context, commit, branch string) error
	// FirstCommit is the first commit on head, past base, that touched
	// path, or "" when none did.
	FirstCommit(ctx context.Context, base, head, path string) (string, error)
}

// Factory turns issues into merged pull requests.
type Factory struct {
	Repository string // owner/name
	GitHub     GitHub
	Repo       Repo
	Formalizer Formalizer
	Builder    Builder
	Plumbing   PlanBuilder // builds a plumbing issue's ratified plan (D-0105); nil takes none
	Base       string      // the branch pull requests merge into
	Projects   string      // the directory new projects go in, such as examples
	Work       string      // where transcripts and logs go
	Check      string      // the CI check that gates a merge: invariant/gate
	Language   string      // the code's language when an issue has no language label (D-0040)

	// RepositoryDependencies is the host's default for configured requests.
	// Unconfigured and modeled runners do not opt into repository execution.
	RepositoryDependencies plumbing.DependencyPreparer

	// Agent names the coding agent that Formalizer, Builder and Plumbing
	// run, which an issue gets unless it or its project picks another
	// (#173). Empty is claude-code. Agents are the other agents the watcher
	// can run, by name. An issue that picks one it can't run is answered,
	// and never drafted or built by another.
	Agent  string
	Agents map[string]Runners
	// Self is the factory's own login when it acts as its App's bot
	// (D-0041). Then only the bot's comments count as the factory's posts,
	// and no bot's comment is ever a command. Empty means the factory posts
	// as the person gh is logged in as, and its posts are told apart by
	// their markers alone.
	Self string
	Log  func(format string, args ...any)
	Now  func() time.Time
	// Activity, when set, hears the steps the watcher is running, as Steps
	// lists them, each time they change and at the end of each poll. It
	// hears one change at a time, in order, and the steps wait while it
	// runs, so it mustn't call the factory. The watcher writes them down for
	// the dashboard (D-0049).
	Activity func(steps []Step)
	// Finished, when set, hears once that a step's agent run ended, with the
	// step as Activity last heard it and Ended, when it really ended: as
	// soon as the step returned, whether it succeeded, failed, was called
	// off or panicked, or when the step moved on to something else (#269).
	// It runs under the same lock as Activity, before Activity hears the
	// step has gone, so it mustn't call the factory either. Activity and
	// Steps never hold a finished step, and a step that starts on the same
	// issue has no run until its own new directory exists (D-0166).
	Finished func(Step)
	// Holder names this watcher, and LeaseFor is how long its lease on the
	// repository lasts (D-0069). A watcher acts only while it holds the
	// lease. Zero means no lease, as in a one-off run.
	Holder   string
	LeaseFor time.Duration
	// Parallel bounds agent operations, including their reviews and repairs,
	// with one step per issue across polls (D-0113, #455). Positive values
	// dispatch steps without waiting; zero keeps Poll synchronous.
	Parallel int
	// recheck is how often a step that waits for capacity or its agent's
	// turn reads its issue again; zero means once a minute (#677).
	recheck time.Duration

	// mu guards the cache of who can write, which steps share: writers is
	// what GitHub last said of each login, and asked is whom the latest poll
	// has asked. Each poll asks again, and a step that runs across polls
	// keeps the answers it read. It guards mergeWait too: the drafts, at
	// their heads, whose wait for GitHub to say it can merge them was
	// logged. And seenMerges: the merged pull requests whose issue a step
	// has read since the watcher started.
	mu         sync.Mutex
	writers    map[string]bool
	asked      map[string]bool
	mergeWait  map[string]bool
	seenMerges map[int]bool
	poll       polling // guarded by mu, shared by polls and their finishing steps
	running    running
	admission  admission
	lease      lease
}

// Labels show where each issue is. The factory keeps exactly one status
// label on an issue it's working on.
const (
	LabelTrigger     = "invariant"
	LabelGo          = "invariant:go"
	LabelTypeScript  = "invariant:typescript"
	LabelPython      = "invariant:python"
	LabelAsking      = "invariant:asking"
	LabelProposal    = "invariant:awaiting-ratification"
	LabelBuilding    = "invariant:building"
	LabelPR          = "invariant:pr-open"
	LabelMerged      = "invariant:merged"
	LabelHumanReview = "invariant:human-review-needed"
)

var labels = []struct{ name, color, description string }{
	{LabelTrigger, "0CA678", "Ask Invariant to take this issue"},
	{LabelGo, "00ADD8", "Invariant writes this issue's code in Go"},
	{LabelTypeScript, "3178C6", "Invariant writes this issue's code in TypeScript"},
	{LabelPython, "3776AB", "Invariant writes this issue's code in Python"},
	{LabelAsking, "38D9A9", "Invariant asked a question"},
	{LabelProposal, "38D9A9", "Invariant proposed statements to ratify"},
	{LabelBuilding, "0CA678", "Invariant is writing the code"},
	{LabelPR, "0CA678", "Invariant opened a pull request"},
	{LabelMerged, "8250DF", "Invariant merged its pull request"},
	{LabelHumanReview, "D1242F", "Invariant needs a person to look"},
}

var statusLabels = []string{LabelAsking, LabelProposal, LabelBuilding, LabelPR, LabelMerged, LabelHumanReview}

// activeLabels are the status labels of work under way.
var activeLabels = []string{LabelAsking, LabelProposal, LabelBuilding, LabelPR, LabelHumanReview}

// Prepare creates the labels the factory uses.
func (f *Factory) Prepare(ctx context.Context) error {
	for _, l := range labels {
		if err := f.GitHub.EnsureLabel(ctx, l.name, l.color, l.description); err != nil {
			return fmt.Errorf("label %s: %w", l.name, err)
		}
	}
	return nil
}

// Watch polls until ctx ends or Drain finishes, then joins its steps. With a
// lease, it polls only while it holds the lease, and checks it before each
// effect. The first time it may act, before it starts any step, it clears
// the clone's worktrees: the clone is the watcher's own, so any worktree
// there is left over from a watcher that stopped mid-build. It clears them
// only then, since after that its steps run across polls, each in a
// worktree of its own (D-0113). A one-off run doesn't clear them, since it
// may share the clone with a running watcher. Then, for the same reasons,
// it clears the Go build caches runs in its work directory left (#366).
func (f *Factory) Watch(ctx context.Context, every time.Duration) (err error) {
	drained := f.drainDone()
	select {
	case <-drained:
		return ctx.Err()
	default:
	}
	renewCtx, stopRenewal := context.WithCancel(ctx)
	leaseDone := make(chan struct{})
	defer func() {
		f.Wait()
		stopRenewal()
		<-leaseDone
		if f.LeaseFor > 0 {
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := f.releaseLease(cleanup); err != nil {
				f.logf("release lease: %v", err)
			}
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
	}()
	if f.LeaseFor > 0 {
		f.GitHub, f.Repo = leasedGitHub{f.GitHub, f}, leasedRepo{f.Repo, f}
		if _, err := f.hold(ctx); err != nil {
			f.logf("lease: %v", err)
		}
		go func() {
			defer close(leaseDone)
			select {
			case <-renewCtx.Done():
			case <-time.After(every):
				f.keepLease(renewCtx, every)
			}
		}()
	} else {
		close(leaseDone)
	}
	cleared := false
	clearOnce := func() error {
		if cleared {
			return nil
		}
		err := f.Repo.ClearWorktrees(ctx)
		cleared = err == nil
		if cleared {
			// A cache it can't remove now waits for the next start; it
			// holds nothing a run needs.
			if n, err := f.clearRunCaches(); err != nil {
				f.logf("run caches: %v", err)
			} else if n > 0 {
				f.logf("cleared the Go build caches %d ended runs left", n)
			}
		}
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-drained:
			return ctx.Err()
		default:
		}
		if !f.holds() {
			// Another watcher holds the lease. Wait for it to run out.
		} else if err := clearOnce(); err != nil {
			f.logf("worktrees: %v", err)
		} else if err := f.Poll(ctx); err != nil {
			f.logf("poll: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-drained:
			return ctx.Err()
		case <-time.After(every):
		}
	}
}

// Poll starts a step on each changed or unfinished issue with none running,
// in order of number, and each step reads its issue again first. It reads the open
// issues, and the closed ones with something left to record (pollIssues).
// With positive Parallel, Poll returns without waiting for its steps, so
// they run across polls, and each logs its error as it ends. Only new agent
// work waits for a slot; commands and other effects go on while those slots
// are full (D-0113, #455). Otherwise Poll takes one step at a time, and
// returns their errors once they've ended.
func (f *Factory) Poll(ctx context.Context) error {
	defer func() {
		// Activity hears every poll, including a failed discovery.
		f.running.mu.Lock()
		f.tell()
		f.running.mu.Unlock()
	}()
	startedAt := f.now()
	f.mu.Lock()
	if f.writers == nil {
		f.writers = map[string]bool{}
	}
	f.asked = map[string]bool{}
	if f.seenMerges == nil {
		f.seenMerges = map[int]bool{}
	}
	f.mu.Unlock()
	issues, merges, err := f.pollIssues(ctx)
	if err != nil {
		return err
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	most := 1
	if f.Parallel > 0 {
		most = 0 // no limit on steps; only agent operations wait for slots
	}
	var errs []error
	for _, issue := range issues {
		n := issue.Number
		started, err := f.start(ctx, n, most)
		if err != nil {
			errs = append(errs, err)
			break
		}
		switch {
		case !started:
			// Its last step is still running.
		case f.Parallel <= 0:
			err := f.pollStep(ctx, issue)
			f.end(n, f.now())
			f.sawMerge(merges[n], err)
			if err != nil {
				errs = append(errs, fmt.Errorf("#%d: %w", n, err))
			}
		default:
			go func() {
				// The step ended when it returned, before its error is logged,
				// and it ends however its goroutine exits.
				var ended time.Time
				defer func() {
					if ended.IsZero() {
						ended = f.now()
					}
					f.end(n, ended)
				}()
				err := f.pollStep(ctx, issue)
				ended = f.now()
				f.sawMerge(merges[n], err)
				if err != nil {
					f.logf("#%d: %v", n, err)
				}
			}()
		}
	}
	if len(errs) == 0 {
		f.mu.Lock()
		f.poll.since = startedAt.Add(-time.Minute).UTC().Format(time.RFC3339)
		f.mu.Unlock()
	}
	return errors.Join(errs...)
}

// pollIssues discovers changed issues and retains unfinished work. The
// first closed scan includes all history; later scans overlap the last
// successful poll. Merges are discovered independently of issue updates.
func (f *Factory) pollIssues(ctx context.Context) ([]github.Issue, map[int]int, error) {
	f.mu.Lock()
	since := f.poll.since
	f.mu.Unlock()
	issues, err := f.GitHub.OpenIssues(ctx)
	if err != nil {
		return nil, nil, err
	}
	seen := map[int]bool{}
	for _, issue := range issues {
		seen[issue.Number] = true
	}
	closed, err := f.GitHub.ClosedIssues(ctx, since)
	if err != nil {
		return nil, nil, err
	}
	for _, issue := range closed {
		if issue.State == "closed" && issue.PullRequest == nil && !seen[issue.Number] &&
			slices.ContainsFunc(activeLabels, issue.HasLabel) {
			seen[issue.Number] = true
			issues = append(issues, issue)
		}
	}
	prs, err := f.GitHub.MergedPullRequests(ctx)
	if err != nil {
		return nil, nil, err
	}
	merges := map[int]int{}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, pr := range prs {
		n, ok := branchIssue(pr.Head.Ref)
		if !ok || f.seenMerges[pr.Number] || merges[n] != 0 {
			continue
		}
		merges[n] = pr.Number
		if !seen[n] {
			seen[n] = true
			issues = append(issues, github.Issue{Number: n})
		}
	}
	return f.poll.selectIssues(issues, merges), merges, nil
}

// sawMerge remembers that a step on the issue of merged pull request pr
// ended, so no later poll reads it again for that merge. A step that failed
// is read again, even for a refusal, whose report may be what failed,
// unless the issue is gone.
func (f *Factory) sawMerge(pr int, err error) {
	if pr == 0 || err != nil && !errors.Is(err, github.ErrNotFound) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seenMerges[pr] = true
}

// branchIssue is the issue a factory branch is for. Its branches are named
// invariant/issue-<n>-<slug>.
func branchIssue(branch string) (int, bool) {
	rest, ok := strings.CutPrefix(branch, "invariant/issue-")
	if !ok {
		return 0, false
	}
	num, _, ok := strings.Cut(rest, "-")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(num)
	return n, err == nil && n > 0
}

// Step is a step the watcher is running on an issue: what it's doing,
// formalizing, answering, ratifying, building or waiting for checks, or "" before it starts
// one of those, and since when. Agent is the coding agent it runs, from
// when its run starts (#179). Run is its output directory's opaque basename,
// published after the directory exists and before its agent starts (#236).
// Ended is set only on the step Finished hears (#269).
type Step struct {
	Issue int
	Doing string
	Since time.Time
	Agent string
	Run   string
	Ended *time.Time
}

// running is the steps a watcher is running, one per issue (D-0113).
type running struct {
	mu    sync.Mutex
	steps map[int]Step
	ended chan struct{} // closed, and made anew, each time a step ends
}

// admission guards agent slots and drain requests separately from running,
// whose activity callbacks may wait on IO. An admitted step stays here
// through publication, even after its agent slot has been released.
type admission struct {
	mu    sync.Mutex
	slots chan struct{}
	drain chan struct{}
	done  chan struct{}
	steps map[int]bool
}

func (a *admission) init() {
	if a.drain == nil {
		a.drain, a.done = make(chan struct{}), make(chan struct{})
		a.steps = map[int]bool{}
	}
}

// finish closes done once draining has no admitted step left. Its caller
// holds the admission lock.
func (a *admission) finish() {
	select {
	case <-a.drain:
		if len(a.steps) == 0 {
			select {
			case <-a.done:
			default:
				close(a.done)
			}
		}
	default:
	}
}

// Drain closes admission to new agent operations without waiting for work
// to finish. It is safe to repeat, including before Watch starts. Admitted
// steps finish normally; cancellation still stops them.
func (f *Factory) Drain() {
	a := &f.admission
	a.mu.Lock()
	defer a.mu.Unlock()
	a.init()
	select {
	case <-a.drain:
	default:
		close(a.drain)
	}
	a.finish()
}

func (f *Factory) drainDone() <-chan struct{} {
	a := &f.admission
	a.mu.Lock()
	defer a.mu.Unlock()
	a.init()
	return a.done
}

// list is the steps running, by issue.
func (r *running) list() []Step {
	steps := make([]Step, 0, len(r.steps))
	for _, s := range r.steps {
		steps = append(steps, s)
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].Issue < steps[j].Issue })
	return steps
}

// Steps lists the steps the watcher is running, by issue.
func (f *Factory) Steps() []Step {
	f.running.mu.Lock()
	defer f.running.mu.Unlock()
	return f.running.list()
}

// Wait waits until no step is running or waiting for an agent slot.
func (f *Factory) Wait() {
	for {
		f.running.mu.Lock()
		n, ended := len(f.running.steps), f.running.ended
		f.running.mu.Unlock()
		if n == 0 {
			return
		}
		<-ended
	}
}

// start waits until fewer than most steps are running, then starts one on
// issue n, unless its last step is still running. It says whether it
// started one, and stops waiting when ctx ends. Zero means no limit.
func (f *Factory) start(ctx context.Context, n, most int) (bool, error) {
	drained := f.drainDone()
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		select {
		case <-drained:
			return false, nil
		default:
		}
		f.running.mu.Lock()
		if f.running.steps == nil {
			f.running.steps, f.running.ended = map[int]Step{}, make(chan struct{})
		}
		_, busy := f.running.steps[n]
		free := !busy && (most == 0 || len(f.running.steps) < most)
		if free {
			f.running.steps[n] = Step{Issue: n, Since: f.now()}
			f.tell()
		}
		ended := f.running.ended
		f.running.mu.Unlock()
		if busy || free {
			return free, nil
		}
		select {
		case <-ended:
		case <-drained:
			return false, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
}

var errIssueClosed = errors.New("the issue closed before its agent run started")
var errDraining = errors.New("the watcher is draining, so this agent operation waits for its successor")

// agentSlot waits for capacity for new agent work, then checks the issue
// and the step's tenure again. Its caller defers the release through the
// whole operation, including reviews, fallbacks and repairs. A wait starts
// no run record or directory, and cancellation never starts a queued run.
func (f *Factory) agentSlot(ctx context.Context, n int) (func(), error) {
	a := &f.admission
	a.mu.Lock()
	a.init()
	if a.slots == nil {
		a.slots = make(chan struct{}, max(f.Parallel, 1))
	}
	slots, drain := a.slots, a.drain
	a.mu.Unlock()
	select {
	case <-drain:
		return nil, errDraining
	default:
	}
	// A step queued for capacity ends when its issue closes, without
	// waiting for a slot first (#677).
	recheck := time.NewTicker(f.recheckEvery())
	defer recheck.Stop()
	for queued := true; queued; {
		select {
		case slots <- struct{}{}:
			queued = false
		case <-drain:
			return nil, errDraining
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-recheck.C:
			if issue, err := f.GitHub.Issue(ctx, n); err == nil && issue.State == "closed" {
				return nil, errIssueClosed
			}
		}
	}
	keep := false
	defer func() {
		if !keep {
			<-slots
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := f.mayAct(ctx); err != nil {
		return nil, err
	}
	issue, err := f.GitHub.Issue(ctx, n)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := f.mayAct(ctx); err != nil {
		return nil, err
	}
	if issue.State == "closed" {
		return nil, errIssueClosed
	}
	// Admission and Drain share this lock, so a slot obtained just as drain
	// begins cannot start another operation after the request.
	a.mu.Lock()
	defer a.mu.Unlock()
	select {
	case <-drain:
		return nil, errDraining
	default:
	}
	a.steps[n] = true
	keep = true
	return func() { <-slots }, nil
}

func (f *Factory) recheckEvery() time.Duration {
	if f.recheck > 0 {
		return f.recheck
	}
	return time.Minute
}

// whileOpen returns ctx carrying a check that issue n is still open, which
// the step's agent makes while it waits for its turn and once more before it
// starts (#677). closed says whether the check found the issue closed: the
// agent never started, so the step ends as it does when the issue closes
// before admission, posting nothing, and the run stays recorded, as one that
// stopped does. A read that fails leaves the run waiting, since only a closed
// issue stops it.
func (f *Factory) whileOpen(ctx context.Context, n int) (context.Context, func() bool) {
	var closed atomic.Bool
	ctx = synth.WhileWanted(ctx, f.recheckEvery(), func(ctx context.Context) error {
		if issue, err := f.GitHub.Issue(ctx, n); err == nil && issue.State == "closed" {
			closed.Store(true)
			return errIssueClosed
		}
		return nil
	})
	return ctx, closed.Load
}

// end ends the step on issue n, which ended at ended.
func (f *Factory) end(n int, ended time.Time) {
	f.running.mu.Lock()
	defer f.running.mu.Unlock()
	defer func() {
		a := &f.admission
		a.mu.Lock()
		defer a.mu.Unlock()
		delete(a.steps, n)
		a.finish()
	}()
	f.finished(f.running.steps[n], ended)
	delete(f.running.steps, n)
	f.tell()
	close(f.running.ended)
	f.running.ended = make(chan struct{})
}

// finished lets Finished hear that a step's agent run ended at ended. A step
// with no run started no agent, so there's nothing to hear. Its caller holds
// the steps' lock, and the step is still among them.
func (f *Factory) finished(s Step, ended time.Time) {
	if s.Run == "" || f.Finished == nil {
		return
	}
	s.Ended = &ended
	f.Finished(s)
}

// read gathers an issue's thread: the factory's posts, and the commands and
// comments of people with write access.
func (f *Factory) read(ctx context.Context, issue github.Issue) (Thread, error) {
	t := Thread{Issue: issue}
	comments, err := f.GitHub.Comments(ctx, issue.Number)
	if err != nil {
		return t, err
	}
	for _, c := range comments {
		p, ok, err := f.draftPost(ctx, issue.Number, c)
		if err != nil {
			return t, err
		}
		if ok {
			t.Posts = append(t.Posts, p)
		}
	}
	// A writer's issue that opens with /invariant plan is a PRD, as one a
	// writer commented it on is (#201). Its command stays in the thread,
	// answered by the factory's first post, so every step knows the issue
	// is a PRD.
	planned := false
	if hasVerb(ParseCommands(issue.Body), Plan) {
		ok, err := f.writer(ctx, issue.User.Login)
		if err != nil {
			return t, err
		}
		if planned = ok; planned {
			t.Commands = append(t.Commands, Command{Verb: Plan, By: issue.User.Login, URL: issue.URL, At: issue.CreatedAt})
		}
	}
	// A new issue is the factory's if a writer opened it with /invariant solve,
	// or a writer gave it the invariant label. One the factory opened for a
	// ratified plan is solved as if the plan's ratifier had opened it with
	// /invariant solve (#126).
	work := slices.ContainsFunc(t.Posts, func(p Post) bool { return p.Marker.Refusal == "" })
	if !work && !planned {
		if ok, err := f.writer(ctx, issue.User.Login); err != nil {
			return t, err
		} else if ok && hasVerb(ParseCommands(issue.Body), Solve) {
			t.Commands = append(t.Commands, Command{Verb: Solve, By: issue.User.Login, URL: issue.URL, At: issue.CreatedAt})
		} else if issue.HasLabel(LabelTrigger) {
			if by, err := f.labeledBy(ctx, issue); err != nil {
				return t, err
			} else if by != "" {
				t.Commands = append(t.Commands, Command{Verb: Solve, By: by, URL: issue.URL, At: issue.CreatedAt})
			}
		} else if by, err := f.plannedBy(ctx, issue); err != nil {
			return t, err
		} else if by != "" {
			t.Commands = append(t.Commands, Command{Verb: Solve, By: by, URL: issue.URL, At: issue.CreatedAt})
		}
	}
	for _, c := range comments {
		if c.User.Type == "Bot" || c.User.Login == f.Self {
			continue // a bot, the factory included, never directs the factory
		}
		if _, ok := DecodeMarker(c.Body); ok && f.Self == "" {
			continue
		}
		ok, err := f.writer(ctx, c.User.Login)
		if err != nil {
			return t, err
		}
		if !ok {
			continue
		}
		t.People = append(t.People, c)
		for _, cmd := range ParseCommands(c.Body) {
			cmd.Comment, cmd.By, cmd.URL, cmd.At = c.ID, c.User.Login, c.URL, c.CreatedAt
			t.Commands = append(t.Commands, cmd)
		}
	}
	return t, nil
}

// labeledBy is the writer who most recently gave the issue the trigger
// label, if a writer did.
func (f *Factory) labeledBy(ctx context.Context, issue github.Issue) (string, error) {
	events, err := f.GitHub.Events(ctx, issue.Number)
	if err != nil {
		return "", err
	}
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Event == "labeled" && e.Label != nil && e.Label.Name == LabelTrigger && e.Actor.Type != "Bot" {
			if ok, err := f.writer(ctx, e.Actor.Login); err != nil || ok {
				return e.Actor.Login, err
			}
		}
	}
	return "", nil
}

// writer says whether login can write, asking GitHub once each poll.
func (f *Factory) writer(ctx context.Context, login string) (bool, error) {
	f.mu.Lock()
	ok, asked := f.writers[login], f.asked[login]
	f.mu.Unlock()
	if asked {
		return ok, nil
	}
	perm, err := f.GitHub.Permission(ctx, login)
	if err != nil {
		return false, err
	}
	ok = perm == "admin" || perm == "maintain" || perm == "write"
	f.mu.Lock()
	f.writers[login], f.asked[login] = ok, true
	f.mu.Unlock()
	return ok, nil
}

// step takes the next step on one issue.
func (f *Factory) step(ctx context.Context, issue github.Issue) error {
	t, err := f.read(ctx, issue)
	if err != nil {
		return err
	}
	if held, err := f.workflowPublicationHeld(ctx, t); err != nil || held {
		return err
	}
	var stopped bool
	ctx, stopped, err = f.effects(ctx, t)
	if err != nil || stopped {
		if next, ok := ctx.Value(pollStepKey{}).(*pollContinuation); ok && stopped && err == nil {
			next.again = ctx.Value(effectsKey{}).(*issueEffects).retried
		}
		return err
	}
	// A retry's answer may have been posted while releasing a refusal.
	if ctx.Value(effectsKey{}).(*issueEffects).retried {
		t, err = f.read(ctx, issue)
		if err != nil {
			return err
		}
	}
	if held, err := f.repositoryHolds(ctx, t); err != nil || held {
		return err
	}
	state, started := t.State()
	if next, ok := ctx.Value(pollStepKey{}).(*pollContinuation); ok {
		next.again = continues(t, state)
	}
	if issue.State == "closed" {
		return f.closedIssue(ctx, t, state)
	}
	// open is a failed build's pull request while it's open, which a writer's
	// retry has the factory watch again.
	var open github.PullRequest
	switch state.Marker.Kind {
	case KindPR:
		return f.watch(ctx, t, state)
	case KindRatified:
		// A ratified plan of issues builds nothing itself (#112), and working
		// it is its issue's whole step (#148).
		if isIssuePlan(state.Marker) {
			return f.workRatified(ctx, t, state)
		}
		// No post has answered the ratification's build yet: start it, or
		// take up what its run left, whichever watcher ran it.
		return f.build(ctx, t, state)
	case KindFailed:
		// A person can merge or close the pull request of a build that
		// failed, and the protocol has both steps. Record them as watch does
		// for an open pull request, so the issue can go on (copythis-ad#36).
		if state.Marker.PR != 0 {
			pr, err := f.GitHub.PullRequest(ctx, state.Marker.PR)
			if err != nil {
				return err
			}
			if pr.Merged || pr.State == "closed" {
				// A draft closed for a writer's retry that recorded it
				// replaces it is the factory's own replacement under way,
				// not someone else's close, so the retry goes on (#271).
				if !pr.Merged {
					if c, ok, err := f.replacement(ctx, t, state); err != nil {
						return err
					} else if ok {
						return f.effectResult(ctx, issue.Number, "replace the failed draft", f.rebuild(ctx, t, state, c))
					}
				}
				return f.watch(ctx, t, state)
			}
			open = pr
		}
	}
	// A stop is for a ratified plan of issues, and WorkPlan answers it on the
	// plan's issue (#126). Any other issue's step never answers one, and one
	// never holds up the commands after it.
	pending := slices.DeleteFunc(t.Pending(), func(c Command) bool { return c.Verb == Stop })
	if len(pending) == 0 {
		return nil
	}
	c := pending[0]
	kind := state.Marker.Kind
	switch c.Verb {
	case Solve:
		if started && kind != KindStuck && kind != KindUnsupported && kind != KindClosed && !stoppedBuild(state) {
			return f.note(ctx, t, c, "I'm already working on this issue.")
		}
		return f.formalize(ctx, t, c, nil, nil, []int64{c.Comment})
	case Plan:
		// A writer asks for a plan of issues, which is drafted afresh wherever
		// a writer's command can draft (#112).
		switch {
		case !started, kind == KindForks, kind == KindProposal, kind == KindStuck, kind == KindUnsupported, kind == KindClosed, stoppedBuild(state):
			return f.formalize(ctx, t, c, nil, nil, []int64{c.Comment})
		}
		return f.note(ctx, t, c, "I'm already working on this issue.")
	case Revise:
		switch {
		case kind == KindForks, kind == KindProposal, kind == KindStuck, kind == KindUnsupported, kind == KindClosed, stoppedBuild(state):
			return f.formalize(ctx, t, c, state.Marker.Answers, state.Marker.Proposal, []int64{c.Comment})
		case kind == KindFailed && state.Marker.Rebuilds():
			// A retry builds the plan again, replacing #n (#271).
			n := state.Marker.PR
			_, trusted, _ := trustedOf(state.Marker)
			return f.note(ctx, t, c, fmt.Sprintf("#%d is still open, so I can't draft again yet.", n), "\n\n"+rebuildOffer(n, trusted))
		case kind == KindFailed && state.Marker.PR != 0:
			// A retry only watches #n again (#263). The note says a person
			// merges it whenever the failure or the plan shows it changes
			// the trusted base, even for an older failure with no plan.
			n, m := state.Marker.PR, state.Marker
			_, trusted, unsure := trustedOf(m)
			return f.note(ctx, t, c, fmt.Sprintf("#%d is still open, so I can't draft again yet.", n),
				fmt.Sprintf("\n\nComment `/invariant retry` to have me watch #%d again. %s %s", n, watchedAgain(open, trusted, unsure), buildAgainInstead(n, isPlan(m.Project))))
		case kind == KindPR:
			n := state.Marker.PR
			return f.note(ctx, t, c, fmt.Sprintf("#%d is still open, so I can't draft again yet.", n),
				fmt.Sprintf(" To change what must be true, close #%d and comment `/invariant revise` again.", n))
		}
		return f.note(ctx, t, c, "There's no draft to revise right now.")
	case Choose:
		if kind != KindForks {
			return f.note(ctx, t, c, "There's no open question to answer right now.")
		}
		return f.choose(ctx, t, state, pending)
	case Retry:
		if state.Marker.Refusal != "" && c.Comment <= state.Comment.ID {
			return f.note(ctx, t, c, "That retry was posted before GitHub's refusal was reported. The recorded work is still waiting for a person.",
				"\n\nAfter fixing the refusal, comment `/invariant retry` again.")
		}
		if kind != KindFailed {
			return f.note(ctx, t, c, "There's nothing that failed to try again right now.")
		}
		if state.Marker.PR == 0 {
			// The build stopped before it made a pull request. Build the
			// ratified proposal again, and start counting stops afresh.
			m := state.Marker.carried()
			m.Kind, m.ReplyTo, m.Failure, m.Gone = KindRatified, []int64{c.Comment}, "", false
			from, to := f.retryStep(t, c)
			if err := f.allowed(ctx, t.Issue.Number, "build the ratified proposal again", from, to); err != nil {
				return err
			}
			// The branch still holds the ratification, so the retry is
			// answered as a ratification is, and the build it starts has its
			// own run.
			if err := f.recovers(ctx, t.Issue.Number, "answer the retry", f.canPost(ratifying(true), recovery.Ratify)); err != nil {
				return err
			}
			return f.say(ctx, t.Issue.Number, post("building again", fmt.Sprintf("Building the proposal @%s ratified again, in `%s`.", c.By, m.Project), m), LabelBuilding)
		}
		// A plan's build that failed in the factory's own run is built
		// again, replacing its draft (#271).
		if state.Marker.Rebuilds() {
			return f.effectResult(ctx, issue.Number, "replace the failed draft", f.rebuild(ctx, t, state, c))
		}
		// Watch the pull request again. Nothing merges unless CI's gate passes
		// on its current head, it stays in scope, and its lock is still the
		// ratified proposal. It runs no build and no review, so the answer
		// says what merges then (#263).
		m := state.Marker.carried()
		m.Kind, m.ReplyTo, m.Failure = KindPR, []int64{c.Comment}, ""
		from, to := f.retryStep(t, c)
		if err := f.allowed(ctx, t.Issue.Number, fmt.Sprintf("look at #%d again", m.PR), from, to); err != nil {
			return err
		}
		// Only a person merges a plan's pull request that changes the trusted
		// base (D-0105), so the answer doesn't promise the factory will.
		trusted, err := f.trustedAt(ctx, t.Issue.Number, m)
		if err != nil {
			return err
		}
		// A retry that watches the latest build's failed draft again is the
		// recovery core's watch (#354). Any other, of a ready pull request,
		// or a second watch of the same draft once CI failed on it, is
		// outside the core's model, as CI's failures are, and is answered as
		// a note. The answer records whether the pull request is a draft or
		// ready, and when the writer retried, so the factory marks ready
		// only a draft a retry watched as that draft (#371, #389's review).
		answered, key := noting(), recovery.Note
		m.Watched, m.RetriedAt = WatchedReady, c.At
		stopped := false
		if open.Draft {
			m.Watched = WatchedDraft
			var watched bool
			if watched, stopped = f.retriedDraft(ctx, t, open, c); !watched {
				answered, key = watching(false), recovery.Watch
			}
		}
		if err := f.recovers(ctx, t.Issue.Number, "answer the retry", f.canPost(answered, key)); err != nil {
			return err
		}
		return f.say(ctx, t.Issue.Number, retriedComment(open, trusted, stopped, m), LabelPR)
	case Ratify:
		if kind != KindProposal {
			return f.note(ctx, t, c, "There's no proposal waiting for ratification right now.")
		}
		p := state.Marker.Proposal
		if !matches(c.Args, p.Hash) {
			return f.note(ctx, t, c, "That doesn't name the current proposal, so I haven't ratified anything.",
				fmt.Sprintf(" To ratify it, comment `/invariant ratify %s`.", strings.TrimPrefix(p.Hash, "sha256:")[:hashChars]))
		}
		return f.ratify(ctx, t, state, c)
	}
	return nil
}

// matches says whether a ratify command names the proposal: at least
// hashChars of its hex digest.
func matches(args []string, hash string) bool {
	if len(args) != 1 {
		return false
	}
	got := strings.ToLower(strings.TrimPrefix(args[0], "sha256:"))
	return len(got) >= hashChars && strings.HasPrefix(strings.TrimPrefix(hash, "sha256:"), got)
}

// choose records people's answers to the open forks. Once every fork is
// answered, or a following revise asks, it drafts again with the answers.
func (f *Factory) choose(ctx context.Context, t Thread, state Post, pending []Command) error {
	f.doing(t.Issue.Number, "answering")
	forks := state.Marker.Forks
	if p := state.Marker.Proposal; p != nil && len(p.Forks) > 0 {
		forks = p.Forks // a writer can correct a choice while other forks remain
	}
	chosen := map[string]formalize.Answer{}
	var replyTo []int64
	var last Command
	var revision *Command
	for _, c := range pending {
		if c.Verb == Revise {
			revision = &c
			replyTo = append(replyTo, c.Comment)
			break
		}
		if c.Verb != Choose {
			continue
		}
		last = c
		fork, option, problem, closing := findChoice(forks, c.Args)
		if problem != "" || closing != "" {
			return f.note(ctx, t, c, problem, closing)
		}
		chosen[strings.ToUpper(fork.ID)] = formalize.Answer{Fork: fork.ID, Question: fork.Question, Option: option.ID, Says: option.Says, By: c.By, Comment: c.URL}
		replyTo = append(replyTo, c.Comment)
	}
	answers := append([]formalize.Answer(nil), state.Marker.Answers...)
	for _, fork := range forks {
		a, ok := chosen[strings.ToUpper(fork.ID)]
		if !ok {
			continue
		}
		if i := slices.IndexFunc(answers, func(a formalize.Answer) bool { return strings.EqualFold(a.Fork, fork.ID) }); i >= 0 {
			answers[i] = a
		} else {
			answers = append(answers, a)
		}
	}
	var remaining []formalize.Fork
	for _, fork := range state.Marker.Forks {
		if _, ok := chosen[strings.ToUpper(fork.ID)]; !ok {
			remaining = append(remaining, fork)
		}
	}
	if revision != nil {
		return f.formalize(ctx, t, *revision, answers, state.Marker.Proposal, replyTo)
	}
	if len(remaining) == 0 {
		// Answers aren't a revision: the next draft starts from them, and
		// carries on only the baseline and target the question held.
		return f.formalize(ctx, t, last, answers, state.Marker.Proposal.Carrier(), replyTo)
	}
	// The post is the saved state, so idle polls and another watcher keep
	// the answers and never ask for the same choices again.
	m := state.Marker.carried()
	m.Forks, m.Answers, m.ReplyTo = remaining, answers, replyTo
	from, to := viewOf(state, true, t.Stops()), viewOf(Post{Marker: m}, true, t.Stops())
	to.directedBy = f.director(last.By)
	if err := f.allowed(ctx, t.Issue.Number, "record partial choices", from, to); err != nil {
		return err
	}
	if err := f.recovers(ctx, t.Issue.Number, "answer partial choices", f.canPost(noting(), recovery.Note)); err != nil {
		return err
	}
	body := forksComment(remaining, m)
	if m.Proposal.HasPreviews() {
		problem := ""
		if err := m.Proposal.ValidatePreviews(); err != nil {
			problem = err.Error()
		}
		if _, ok := DecodeMarker(m.encode()); !ok || len(body) > maxComment {
			problem = "The complete remaining alternatives exceed the comment or expanded marker size limit. Revise and check the options again."
		}
		if problem != "" {
			m.Kind, m.Forks, m.Proposal = KindStuck, nil, nil
			return f.say(ctx, t.Issue.Number, stuckComment(problem, m), LabelHumanReview)
		}
	}
	return f.say(ctx, t.Issue.Number, body, LabelAsking)
}

func findChoice(forks []formalize.Fork, args []string) (formalize.Fork, formalize.Option, string, string) {
	if len(args) != 2 {
		return formalize.Fork{}, formalize.Option{}, "", "To answer a question, comment `/invariant choose` with its number and an option, like `/invariant choose F1 A`."
	}
	for _, fork := range forks {
		if strings.EqualFold(fork.ID, args[0]) {
			if o, ok := fork.Option(args[1]); ok {
				return fork, o, "", ""
			}
			return fork, formalize.Option{}, fmt.Sprintf("%s has no option %s.", fork.ID, args[1]), ""
		}
	}
	return formalize.Fork{}, formalize.Option{}, fmt.Sprintf("There's no open question %s.", args[0]), ""
}

// formalize drafts statements for the issue and posts what came of it:
// forks to decide, a proposal to ratify, or why neither. cause is the
// command that asked for the draft.
func (f *Factory) formalize(ctx context.Context, t Thread, cause Command, answers []formalize.Answer, previous *formalize.Proposal, replyTo []int64) error {
	n := t.Issue.Number
	// New completed records already contain publication authority. Recover
	// before consulting editable issue text or preparing another agent run.
	state, commit, err := f.Repo.Run(ctx, n, draftStepName(cause))
	if err != nil {
		return err
	}
	if state == RunDone {
		res, err := f.loadDraft(ctx, commit)
		if err != nil {
			return err
		}
		if res.Publication != nil {
			return f.publishDraft(ctx, t, cause, replyTo, res)
		}
	}
	// base is the project's lock on the base branch, which a draft amends.
	var base string
	stuck := func(problem string) error {
		m := retainedDraft(t, Marker{Kind: KindStuck, ReplyTo: replyTo, Answers: answers, Proposal: previous})
		return f.drafted(ctx, t, cause, base, m, stuckComment(problem, m), LabelHumanReview)
	}
	f.logf("#%d: formalizing", n)
	f.doing(n, "formalizing")
	req := f.request(t, answers, previous)
	parent, _, err := f.plannedStep(ctx, t.Issue)
	if err != nil {
		return err
	}
	req.ParentPlan = parent
	// A writer's /invariant plan makes an ordinary issue a PRD (#112).
	// A recorded step still drafts one complete change, including after
	// that command, an answer or a revision (#560).
	prd := parent == 0 && isPRD(t)
	if prd {
		root, err := f.planBase(ctx)
		if err != nil {
			return err
		}
		defer os.RemoveAll(root)
		req.PRD = root
	}
	// authority is the native plan a plumbing draft keeps the captured
	// repository authority of, if any.
	var authority *formalize.Proposal
	// A plumbing issue gets a plan instead of statements, checked against
	// the committed base (D-0105). External repositories need a reviewed
	// contract and capable gate before any agent run or branch is recorded.
	if !prd && kindLine(t.Issue.Body) == "plumbing" {
		if err := f.Repo.Fetch(ctx); err != nil {
			return err
		}
		var snapshot *repository.Snapshot
		var err error
		authority = nativeAuthority(t, cause, previous)
		if authority != nil {
			snapshot, err = f.planRepository(ctx, authority.Plan)
		} else {
			snapshot, err = f.repositoryAt(ctx, "origin/"+f.Base)
		}
		if errors.Is(err, repository.ErrMissing) && authority == nil {
			if !f.plansPlumbing() {
				m := Marker{Kind: KindUnsupported, ReplyTo: replyTo, Answers: answers}
				return f.drafted(ctx, t, cause, base, m, plumbingElsewhereComment(m), LabelHumanReview)
			}
			err = nil // Invariant's unconfigured built-in runner.
		}
		if err == nil && snapshot != nil {
			_, err = f.repositoryGates(ctx, snapshot.Commit())
		}
		if err != nil {
			var authority repositoryAuthority
			if errors.As(err, &authority) {
				return stuck(err.Error())
			}
			return err
		}
		if projectLine(t.Issue.Body) != "" || len(codeLines(t.Issue.Body)) > 0 {
			return stuck("A plumbing issue's plan names the files it changes, so it names no project or code to check. Take out the Project: and Code: lines, or the Kind: plumbing line.")
		}
		var root string
		if snapshot != nil {
			// The native checker reads captured objects independently of the
			// agent's copy, so keep a detached checkout for this request.
			root, err = f.Repo.Worktree(ctx, "", snapshot.Commit())
			if err == nil {
				defer f.Repo.RemoveWorktree(context.WithoutCancel(ctx), root)
			}
		} else {
			root, err = f.planBaseAt(ctx, "origin/"+f.Base)
			if err == nil {
				defer os.RemoveAll(root)
			}
		}
		if err != nil {
			return err
		}
		req.Plumbing = root
		if snapshot == nil {
			req.Language = "go"
		}
		ctx = plumbing.WithRepositoryRuntime(ctx, snapshot, f.RepositoryDependencies)
	}
	// A Project: line names the project the issue changes, or where a new
	// one goes (D-0045).
	var newDir string
	if dir := projectLine(t.Issue.Body); !prd && dir != "" {
		if problem := badDir(dir); problem != "" {
			return stuck("The issue names the project `" + dir + "`, but " + problem + ".")
		}
		cur, err := f.current(ctx, dir)
		if err != nil {
			return err
		}
		// A revision amends the ratification its amendment captured, until
		// another ratification of the project lands on the base branch or
		// the issue names another project. The captured one could then never
		// be ratified against, so the revision amends the project as it is
		// now.
		if p := req.Previous; p != nil && p.Target != nil && p.Target.Baseline != nil {
			if cur != nil && p.Target.Dir == dir && p.Target.Amends == project.ProposalHash(cur.Lock.Bounds, cur.Lock.Statements) {
				cur = formalize.Request{Previous: p, Current: cur}.CapturedCurrent()
			} else {
				moved := *p
				moved.Target = nil
				req.Previous = &moved
			}
		}
		if cur != nil {
			req.Current, req.Language = cur, cur.Manifest.Language
			base = project.ProposalHash(cur.Lock.Bounds, cur.Lock.Statements)
		} else {
			newDir = dir
		}
	}
	// Code: lines name existing code for the factory to check as it is
	// (D-0054). The formalizer reads it as it is on the base branch.
	if paths := codeLines(t.Issue.Body); !prd && len(paths) > 0 {
		for _, p := range paths {
			if problem := badDir(p); problem != "" {
				return stuck("The issue names the code `" + p + "`, but " + problem + ".")
			}
		}
		if req.Current != nil && len(req.Current.Manifest.Existing) == 0 {
			return stuck("The issue names code to check, but `" + req.Current.Dir + "` is a project that holds its own code. Name a new directory with the Project: line instead.")
		}
		root, err := os.MkdirTemp("", "invariant-existing-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(root)
		if err := f.Repo.Fetch(ctx); err != nil {
			return err
		}
		if err := f.Repo.Export(ctx, "origin/"+f.Base, paths, root); err != nil {
			return stuck(fmt.Sprintf("The issue names code to check, and I couldn't read it on `%s`: %v", f.Base, err))
		}
		req.Existing, req.Language = &formalize.Existing{Paths: paths, Root: root}, "typescript"
	}
	// The issue's agent drafts it (#173). One the watcher can't run, or
	// doesn't know, gets an answer instead, with nothing drafted.
	line, named := agentLine(t.Issue.Body), ""
	if req.Current != nil {
		named = req.Current.Manifest.Agent
	}
	agent := f.agentOf(line, named)
	run, ok := f.runners(agent)
	if !ok || run.Formalizer == nil {
		m := retainedDraft(t, Marker{Kind: KindUnsupported, ReplyTo: replyTo, Answers: answers, Proposal: previous})
		return f.drafted(ctx, t, cause, base, m, unrunnableComment(agent, f.runnable(), m), LabelHumanReview)
	}
	// The draft's agent run is recorded before it starts, where every
	// watcher sees it (D-0069). One recorded and never finished stopped
	// partway, and only a writer's command starts another.
	publication := draftPublication{Agent: agent, Dir: newDir, ProposalAgent: line, Base: base,
		Answers: answers, Previous: previous, Authority: authority}
	res, err := f.draft(ctx, n, draftStepName(cause), agent, run.Formalizer, req, publication)
	if errors.Is(err, errResumed) {
		return nil
	}
	if err != nil {
		return err
	}
	if res == nil {
		if err := f.recovers(ctx, n, "say the draft stopped", f.canReportStopped(drafting(RunRecorded), recovery.Solve)); err != nil {
			return err
		}
		// The issue's agent ran the draft that stopped, so its post names it
		// (#179).
		m := retainedDraft(t, Marker{Kind: KindStuck, ReplyTo: replyTo, Answers: answers, Proposal: previous, Agent: agent})
		return f.drafted(ctx, t, cause, base, m, stuckComment("The draft's agent run stopped partway, when the watcher running it stopped or lost the repository's lease.", m,
			" Comment `/invariant revise` to draft again."), LabelHumanReview)
	}
	if res.Publication == nil {
		// Legacy completed records predate effective publication metadata.
		res.Publication = &publication
		applyDraftPublication(res)
		if res.CheckContext == nil {
			c := req.CheckContext()
			res.CheckContext = &c
		}
	}
	return f.publishDraft(ctx, t, cause, replyTo, res)
}

func (f *Factory) publishDraft(ctx context.Context, t Thread, cause Command, replyTo []int64, res *savedDraftResult) error {
	n := t.Issue.Number
	publication := res.Publication
	if publication == nil || res.CheckContext == nil {
		return fmt.Errorf("completed draft is missing its publication context")
	}
	if res.Proposal == nil && res.Problem == "" {
		return fmt.Errorf("completed draft is missing its outcome")
	}
	base, agent := publication.Base, publication.Agent
	previous, authority := publication.Previous, publication.Authority
	answers := publication.Answers
	checkContext := *res.CheckContext
	parent, prd := checkContext.ParentPlan, checkContext.Planning == "prd"
	if err := f.recovers(ctx, n, "post the draft", f.canPost(drafting(RunDone), recovery.Solve)); err != nil {
		return err
	}
	// A returned or recovered draft must obey the same limit, even when
	// its formalizer did not check it or it was saved before the limit.
	if parent != 0 && res.Proposal != nil && res.Proposal.IssuePlan != nil {
		res.Problem, res.Proposal = formalize.StepPlanProblem(parent), nil
	}
	if res.Proposal != nil {
		answers = revise(t, answers, res.Proposal.Revised)
	}
	// A failed native revision keeps the original execution base, so the
	// next revision cannot recapture changed guidance or gate authority.
	// A draft that came back with a problem is what the next revision starts
	// from, as it was before saved records: the person revising sees what
	// failed, and the agent mends it. Only a draft that returned nothing
	// falls back to the proposal before it.
	failed := res.Proposal
	if authority != nil {
		failed = authority
	} else if failed == nil {
		failed = previous
	}
	// The post names the agent that drafted it, beside what it spent (#179).
	m := Marker{ReplyTo: replyTo, Answers: answers, Spend: res.Usage.CostUSD, Agent: agent}
	// stuck composes the post about a draft that came back with a problem.
	// A post leaves out a failed draft it can't hold, and with it the saved
	// record the thread's state names, which no other post would then
	// carry. So when the failed draft doesn't fit, the post keeps the
	// proposal before it, with its record: the next revision still has a
	// draft and the baseline it compares with (#790).
	stuck := func(comment func(Marker) string) (Marker, string) {
		m.Kind, m.Proposal = KindStuck, failed
		m = retainedDraft(t, m)
		body := comment(m)
		if kept, _ := DecodeMarker(body); authority == nil && previous != nil && failed != previous && kept.Proposal == nil && kept.ProposalRef == nil {
			m.Proposal = previous
			m = retainedDraft(t, m)
			body = comment(m)
		}
		return m, body
	}
	switch p := res.Proposal; {
	case res.Problem != "" && synth.ProviderUnavailable(errors.New(res.Problem)):
		// A draft its agent's provider never let finish needs only drafting
		// again, and says so, naming what it was drafting (#261).
		what := "statements"
		switch {
		case prd:
			what = "plan of issues"
		case checkContext.Planning == "plumbing":
			what = "plan"
		}
		m, body := stuck(func(m Marker) string { return providerStuckComment(res.Problem, what, m) })
		return f.drafted(ctx, t, cause, base, m, body, LabelHumanReview)
	case res.Problem != "":
		m, body := stuck(func(m Marker) string { return stuckComment(res.Problem, m) })
		return f.drafted(ctx, t, cause, base, m, body, LabelHumanReview)
	case p.Unsupported != "":
		m.Kind, m.Proposal = KindUnsupported, previous
		m = retainedDraft(t, m)
		return f.drafted(ctx, t, cause, base, m, unsupportedComment(p.Unsupported, m), "")
	case len(p.Forks) > 0:
		if checkContext.Planning == "" || p.HasPreviews() {
			if err := p.ValidatePreviewContext(checkContext); err != nil {
				m.Kind, m.Proposal = KindStuck, previous
				m = retainedDraft(t, m)
				return f.drafted(ctx, t, cause, base, m, stuckComment(err.Error(), m), LabelHumanReview)
			}
		}
		m.Kind, m.Forks, m.Proposal = KindForks, p.Forks, p
		body := forksComment(p.Forks, m)
		// A question carries its previews, and the baseline and target the
		// draft after it compares with, so any of them can outgrow a post.
		if _, ok := DecodeMarker(m.encode()); !ok || len(body) > maxComment {
			m.Kind, m.Forks, m.Proposal = KindStuck, nil, previous
			m = retainedDraft(t, m)
			return f.drafted(ctx, t, cause, base, m, stuckComment("The complete question, with its alternative previews and the baseline it carries, exceeds the comment or expanded marker size limit. Revise the presentation and check every option again before publication.", m), LabelHumanReview)
		}
		return f.drafted(ctx, t, cause, base, m, body, LabelAsking)
	case p.IssuePlan != nil:
		m.Kind, m.Proposal = KindProposal, p
		return f.drafted(ctx, t, cause, base, m, issuePlanComment(p, m), LabelProposal)
	case p.Plan != nil:
		m.Kind, m.Proposal = KindProposal, p
		return f.drafted(ctx, t, cause, base, m, planComment(p, m), LabelProposal)
	default:
		// A refused draft isn't a selected model, so its post keeps the
		// previous one, and with it the baseline a revision compares with.
		if err := p.ValidateReader(); err != nil {
			m.Kind, m.Proposal = KindStuck, previous
			m = retainedDraft(t, m)
			return f.drafted(ctx, t, cause, base, m, stuckComment(err.Error(), m), LabelHumanReview)
		}
		if err := p.ValidateComparisonContext(checkContext); err != nil {
			m.Kind, m.Proposal = KindStuck, previous
			m = retainedDraft(t, m)
			return f.drafted(ctx, t, cause, base, m, stuckComment(err.Error(), m), LabelHumanReview)
		}
		m.Kind, m.Proposal = KindProposal, p
		// A compressed marker can fit the comment but exceed the reader's
		// expanded size limit, leaving nothing recoverable or ratifiable.
		if _, ok := DecodeMarker(m.encode()); !ok {
			m.Kind, m.Proposal = KindStuck, previous
			m = retainedDraft(t, m)
			return f.drafted(ctx, t, cause, base, m, stuckComment("The complete modeled proposal cannot be read back from its saved marker, which has a 4 MiB expanded size limit. Reduce the saved payload and check the draft again before publishing for ratification.", m), LabelHumanReview)
		}
		body := proposalComment(p, res.Report, m)
		if p.Target != nil {
			body = amendmentComment(p, res.Report, res.Changes, m)
		}
		if len(body) > maxComment {
			m.Kind, m.Proposal = KindStuck, previous
			m = retainedDraft(t, m)
			return f.drafted(ctx, t, cause, base, m, stuckComment("The complete modeled proposal and its reader evidence exceed the comment size limit; it cannot be published for ratification. Revise the presentation before publishing again.", m), LabelHumanReview)
		}
		return f.drafted(ctx, t, cause, base, m, body, LabelProposal)
	}
}

// draftStepName names a draft's record after the command that asked for it.
func draftStepName(cause Command) string {
	if cause.Comment == 0 {
		return "draft-issue" // the /invariant solve that opened the issue
	}
	return fmt.Sprintf("draft-%d", cause.Comment)
}

// draft returns what the draft's agent run made of the request: running it
// with form, the formalizer of agent, the issue's agent, when nothing was
// recorded, or reading what a finished run left, whichever watcher ran it.
// It returns nil for a run recorded and never finished. The run's record
// and its result name the agent (#179).
func (f *Factory) draft(ctx context.Context, n int, step, agent string, form Formalizer, req formalize.Request, publication draftPublication) (*savedDraftResult, error) {
	authority := req.CheckContext()
	// The formalizer receives the request, not ownership of the publication
	// metadata and previous proposal retained for recovery.
	b, err := json.Marshal(publication)
	if err != nil {
		return nil, err
	}
	var captured draftPublication
	if err := json.Unmarshal(b, &captured); err != nil {
		return nil, err
	}
	publication = captured
	state, result, err := f.Repo.Run(ctx, n, step)
	switch {
	case err != nil:
		return nil, err
	case state == RunRecorded:
		return nil, nil
	case state == RunDone:
		return f.loadDraft(ctx, result)
	}
	release, err := f.agentSlot(ctx, n)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := f.recovers(ctx, n, "start a draft's agent run", f.canStartRun(drafting(RunNone), recovery.Solve)); err != nil {
		return nil, err
	}
	out, err := f.newRun(n, "formalize")
	if err != nil {
		return nil, err
	}
	if err := f.Repo.Record(ctx, n, step, fmt.Sprintf("a draft for #%d by %s", n, agent)); err != nil {
		return nil, err
	}
	f.runsAgent(n, agent, out)
	agentCtx, closed := f.whileOpen(ctx, n)
	res, runErr := form.Formalize(agentCtx, req, out)
	if closed() {
		return nil, errIssueClosed
	}
	// A watcher that lost the lease during the run drops it. The holder will
	// find it recorded, and say it stopped. One that took it back since, in
	// a new tenure, records the run's result, and its next step posts it.
	if !f.holds() {
		return nil, errLeaseLost
	}
	ctx, resumed := f.resumed(ctx)
	switch {
	case runErr != nil:
		spent := synth.Usage{}
		if res != nil {
			spent = res.Usage
		}
		res = &formalize.Result{Problem: "the formalizer couldn't run: " + runErr.Error(), Usage: spent}
	case res == nil:
		res = &formalize.Result{Problem: "the formalizer left no draft"}
	case res.Proposal == nil && res.Problem == "":
		res.Problem = "the formalizer left no draft"
	}
	// Anyone can read the problem, in a post or the record, so it names the
	// paths on this machine as localPaths does, whether it's the formalizer's
	// own or its run's error.
	res.Problem = localPaths(res.Problem, f.Work)
	res.CheckContext = &authority
	complete := &savedDraftResult{Result: *res, Publication: &publication}
	applyDraftPublication(complete)
	saved, err := f.saveResult(ctx, complete, fmt.Sprintf("invariant: the result of a draft for #%d by %s", n, agent), "")
	if err != nil {
		return nil, err
	}
	if err := f.recovers(ctx, n, "record the draft's result", f.canFinishRun(drafting(RunRecorded), recovery.Solve)); err != nil {
		return nil, err
	}
	if err := f.Repo.Finish(ctx, n, step, saved); err != nil {
		return nil, err
	}
	if resumed {
		return nil, errResumed
	}
	return complete, nil
}

// drafted posts what the formalizer made of an issue, once the protocol
// allows it.
func (f *Factory) drafted(ctx context.Context, t Thread, cause Command, base string, m Marker, body, label string) error {
	from, to := f.draftStep(t, cause, base, m)
	if err := f.allowed(ctx, t.Issue.Number, "post a draft", from, to); err != nil {
		return err
	}
	return f.say(ctx, t.Issue.Number, body, label)
}

// projectLine finds the project an issue names, in a line such as
// "Project: examples/04-api-rate-limiter".
func projectLine(body string) string {
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if name, dir, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "project") {
			return strings.Trim(strings.TrimSpace(dir), "`/")
		}
	}
	return ""
}

// codeLines reads the existing code an issue names for the factory to
// check, one `Code: <path>` line each (D-0054).
func codeLines(body string) []string {
	var paths []string
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if name, p, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "code") {
			if p = strings.Trim(strings.TrimSpace(p), "`/"); p != "" {
				paths = append(paths, p)
			}
		}
	}
	return paths
}

// badDir says what's wrong with a project directory an issue names, or a
// proposal carries.
func badDir(dir string) string {
	top, _, _ := strings.Cut(dir, "/")
	switch {
	case path.IsAbs(dir) || path.Clean(dir) != dir || dir == "." || dir == ".." || strings.HasPrefix(dir, "../") || strings.Contains(dir, `\`):
		return "that isn't a clean path inside the repository"
	case strings.EqualFold(top, ".github"):
		// Any spelling: a checkout that folds case reads .GitHub as .github.
		return "the factory never touches CI configuration"
	case strings.EqualFold(top, ".git") || strings.EqualFold(top, ".invariant") || strings.EqualFold(top, "decisions"):
		return "that's where the repository keeps its own records"
	case strings.ContainsAny(dir, " \t"):
		return "a project's directory can't contain spaces"
	case strings.ContainsFunc(dir, unicode.IsControl):
		return "a project's directory can't contain control characters"
	}
	for _, part := range strings.Split(dir, "/") {
		if strings.HasPrefix(part, "-") {
			return "a name in it starts with a dash, which a command would read as an option"
		}
	}
	return ""
}

// branchable says whether a proposal's slug can end its branch's name: one
// name, that git takes there. An amendment's slug is its project's
// directory's name, which an issue's Project line may spell any way badDir
// takes, so the rule is git's and no narrower.
func branchable(slug string) bool {
	switch {
	case slug == "" || strings.ContainsAny(slug, " ~^:?*[\\/") || strings.ContainsFunc(slug, unicode.IsControl):
		return false
	case strings.Contains(slug, "..") || strings.Contains(slug, "@{") || strings.HasSuffix(slug, ".") || strings.HasSuffix(slug, ".lock"):
		return false
	}
	return true
}

// packageName is what formalize requires of a draft's package.
var packageName = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// destination holds everything a proposal says about where ratification
// writes to the rules an issue's own Project line meets. A proposal is read
// from the comment that carries it or from a saved record a comment names,
// and either one's bytes can say anything: ratification joins its directory
// and its module to the worktree, removes model files beside the module of
// an amendment, and names a branch after its slug, and the build that
// follows makes a new project's package its code's directory (#790). So
// it's checked on any proposal about to be ratified, however it was read.
// A plan has its own check, planDestination, and an amendment's manifest is
// held to its project's where the project is read.
func destination(p *formalize.Proposal) error {
	if p == nil || p.Plan != nil || p.IssuePlan != nil {
		return nil
	}
	if p.Dir != "" {
		if problem := badDir(p.Dir); problem != "" {
			return fmt.Errorf("project directory %q: %s", p.Dir, problem)
		}
	}
	if p.Target != nil {
		if problem := badDir(p.Target.Dir); problem != "" {
			return fmt.Errorf("amendment directory %q: %s", p.Target.Dir, problem)
		}
	}
	if len(p.Forks) > 0 {
		// A question's carrier is never written: only a selected proposal is.
		return nil
	}
	switch {
	case !branchable(p.Slug):
		return fmt.Errorf("proposal slug %q can't name a branch", p.Slug)
	case p.Target == nil && !savedSlug.MatchString(p.Slug):
		// A new project's slug also names its directory, as a draft's must.
		return fmt.Errorf("proposal slug %q must be kebab case", p.Slug)
	}
	// A draft's module is named as it's made, and the name is the model's
	// file under the project. An amendment keeps its project's manifest,
	// whose module path is where the amended model is written, and beside
	// which other model files are removed. Those are what Write writes, so
	// the rule is Write's own, and it refuses them whoever calls it (#895).
	if err := p.ValidateDestinations(); err != nil {
		return err
	}
	if p.Target == nil && !packageName.MatchString(p.Package) {
		// A new project's package is its manifest's code directory, which
		// the build makes in its agent's workspace.
		return fmt.Errorf("package %q must be a short lowercase package name", p.Package)
	}
	return nil
}

// planDestination holds a plan to its lock's own check before any of it is
// written. A lock is written by joining each acceptance test file's path to
// the worktree, and the check that refuses a path outside it, with whatever
// else a ratified plan must hold, is made when the lock is read back. A
// plan carried in a comment can name any path, so the same check is made
// first, on the bytes the lock would hold (#790). A plan that ratifies
// passes it either way. A configured repository's plan already meets it
// before its lock is written, and one that fails is held for a person by
// its repository's own rules, so it's left to them.
func planDestination(p *formalize.Proposal) error {
	if p.Plan.Native() {
		return nil
	}
	b, err := json.Marshal(plumbing.Lock{Ratified: plumbing.Ratification{Proposal: p.Hash}, Plan: *p.Plan})
	if err != nil {
		return err
	}
	_, err = plumbing.ReadLock(b)
	return err
}

// refusedDestination is a proposal ratification won't write: what it says
// about where its files go breaks a rule, or a plan fails its lock's check.
// What a person does next is to draft again, unless next says otherwise.
type refusedDestination struct {
	err  error
	next string
}

func (r refusedDestination) Error() string { return r.err.Error() }
func (r refusedDestination) Unwrap() error { return r.err }

// inCheckout says that a project's directory is reached from its worktree
// without leaving it, and holds no link that leads out of it. The rules for
// a directory judge the text of its path, so a link a person committed on
// the base branch was followed where it led: the second review of #799 had
// a whole project written into a link's target outside the worktree
// (#895). The worktree's own root follows no link out of it, so it's asked,
// for the directory, or as much of the way to it as exists, and for every
// link inside it.
func inCheckout(wt, dir string) error {
	root, err := os.OpenRoot(wt)
	if err != nil {
		return err
	}
	defer root.Close()
	at := filepath.FromSlash(dir)
	for ; at != "."; at = filepath.Dir(at) {
		_, err := root.Stat(at)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("a link on the way to `%s` leads out of the repository", dir)
		}
	}
	if at != filepath.FromSlash(dir) {
		return nil
	}
	return fs.WalkDir(root.FS(), dir, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		if _, err := root.Stat(filepath.FromSlash(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("`%s` is a link that leads out of the repository", name)
		}
		return nil
	})
}

// buildDestination holds a ratified post's own branch and project to what
// ratification writes there: one of its issue's branches, and a project's
// directory an issue could name, or the issue's own plan. A build checks
// out the branch and works under the project, removing and copying files,
// and both come from the post's marker, which destination doesn't cover
// (#895).
func buildDestination(issue int, m Marker) error {
	prefix := fmt.Sprintf("invariant/issue-%d-", issue)
	if !strings.HasPrefix(m.Branch, prefix) || !branchable(strings.TrimPrefix(m.Branch, prefix)) {
		return fmt.Errorf("its branch, `%s`, isn't one of #%d's", m.Branch, issue)
	}
	if isPlan(m.Project) {
		if m.Project != plumbing.LockPath(issue) {
			return fmt.Errorf("its plan, `%s`, isn't #%d's", m.Project, issue)
		}
		return nil
	}
	if problem := badDir(m.Project); problem != "" {
		return fmt.Errorf("its project directory, `%s`: %s", m.Project, problem)
	}
	return nil
}

// current reads the project in dir as it stands on the base branch, or
// returns nil when there's no project there.
func (f *Factory) current(ctx context.Context, dir string) (*formalize.Current, error) {
	if err := f.Repo.Fetch(ctx); err != nil {
		return nil, err
	}
	ref, err := f.Repo.RevParse(ctx, "origin/"+f.Base)
	if err != nil {
		return nil, err
	}
	b, err := f.Repo.Show(ctx, ref, dir+"/.invariant/invariant.json")
	if err != nil {
		return nil, nil
	}
	c := &formalize.Current{Dir: dir, Commit: ref}
	if err := json.Unmarshal(b, &c.Manifest); err != nil {
		return nil, fmt.Errorf("%s's manifest: %w", dir, err)
	}
	if b, err = f.Repo.Show(ctx, ref, dir+"/.invariant/ratified.lock"); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &c.Lock); err != nil {
		return nil, fmt.Errorf("%s's lock: %w", dir, err)
	}
	if b, err = f.Repo.Show(ctx, ref, dir+"/"+c.Manifest.Module); err != nil {
		return nil, err
	}
	c.ModuleText = string(b)
	root, err := os.MkdirTemp("", "invariant-current-reader-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	if err := f.Repo.Export(ctx, ref, []string{dir + "/.invariant", path.Join(dir, path.Dir(c.Manifest.Module))}, root); err != nil {
		return nil, err
	}
	if err := c.ReadReader(filepath.Join(root, dir)); err != nil {
		return nil, fmt.Errorf("%s's reader inputs: %w", dir, err)
	}
	return c, nil
}

// language is the language an issue's label asks for, or the repository's
// default (D-0040).
func (f *Factory) language(issue github.Issue) string {
	for label, lang := range map[string]string{LabelGo: "go", LabelTypeScript: "typescript", LabelPython: "python"} {
		if issue.HasLabel(label) {
			return lang
		}
	}
	if f.Language != "" {
		return f.Language
	}
	return "go"
}

// request is what people have said on the issue, for the formalizer and the
// project's request.md.
func (f *Factory) request(t Thread, answers []formalize.Answer, previous *formalize.Proposal) formalize.Request {
	req := formalize.Request{Repo: f.Repository, Language: f.language(t.Issue), Issue: t.Issue.Number, Title: t.Issue.Title,
		Body: withoutCommands(t.Issue.Body), Author: t.Issue.User.Login, Answers: answers, Previous: previous}
	for _, c := range t.People {
		if body := withoutCommands(c.Body); body != "" {
			req.Thread = append(req.Thread, formalize.Message{By: c.User.Login, Body: body})
			for _, a := range answers {
				if fork, ok := askedFork(t, a.Fork); ok {
					for _, o := range fork.Options {
						req.RevisionChoices = append(req.RevisionChoices, formalize.Revision{Fork: fork.ID, Option: o.ID, By: c.User.Login})
					}
				}
			}
		}
	}
	return req
}

// revise records the decided forks a draft says a person's later comment
// changed (D-0094). On copythis-ad#36, a writer's comment moved F1 and F2
// from C and B to A and A, the draft followed it, and the proposal still
// listed C and B as decided. A change counts only if its fork was decided,
// its option is one of that fork's, and its person is a writer who
// commented; it then cites that person's latest comment that says more
// than commands, as the draft read it. Otherwise the answer stands.
func revise(t Thread, answers []formalize.Answer, changes []formalize.Revision) []formalize.Answer {
	if len(changes) == 0 {
		return answers
	}
	out := append([]formalize.Answer(nil), answers...)
	for _, ch := range changes {
		i := slices.IndexFunc(out, func(a formalize.Answer) bool { return strings.EqualFold(a.Fork, ch.Fork) })
		fork, asked := askedFork(t, ch.Fork)
		if i < 0 || !asked {
			continue
		}
		opt, ok := fork.Option(ch.Option)
		if !ok {
			continue
		}
		var said *github.Comment
		for j := len(t.People) - 1; j >= 0 && said == nil; j-- {
			if c := &t.People[j]; strings.EqualFold(c.User.Login, ch.By) && withoutCommands(c.Body) != "" {
				said = c
			}
		}
		if said == nil {
			continue
		}
		out[i].Option, out[i].Says, out[i].By, out[i].Comment = opt.ID, opt.Says, said.User.Login, said.URL
	}
	return out
}

// askedFork finds the fork the factory last asked with this id.
func askedFork(t Thread, id string) (formalize.Fork, bool) {
	for i := len(t.Posts) - 1; i >= 0; i-- {
		for _, f := range t.Posts[i].Marker.Forks {
			if strings.EqualFold(f.ID, id) {
				return f, true
			}
		}
	}
	return formalize.Fork{}, false
}

// withoutCommands drops command lines, which are for the factory, not about
// the system.
func withoutCommands(body string) string {
	var keep []string
	for _, line := range strings.Split(body, "\n") {
		if len(ParseCommands(line)) == 0 {
			keep = append(keep, line)
		}
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}

// ratify commits the ratified proposal as a new project on the issue's
// branch, then builds it. A plan of issues is recorded on the issue instead,
// and builds nothing.
func (f *Factory) ratify(ctx context.Context, t Thread, state Post, c Command) error {
	f.doing(t.Issue.Number, "ratifying")
	if isIssuePlan(state.Marker) {
		return f.ratifyIssuePlan(ctx, t, state, c)
	}
	posted, err := f.commitRatification(ctx, t, state, c)
	var authority repositoryAuthority
	if errors.As(err, &authority) {
		return f.holdRepository(ctx, t, err, LabelProposal)
	}
	if errors.Is(err, errStale) {
		return f.note(ctx, t, c, "The project changed after I drafted this amendment, so I haven't ratified anything.",
			" Comment `/invariant revise` and I'll draft it again from the project as it is now.")
	}
	// A proposal that would write somewhere it mustn't is refused where the
	// person who ratified it reads the answer, once, not in the log at
	// every poll.
	var refused refusedDestination
	if errors.As(err, &refused) {
		f.logf("#%d: not ratified: %v", t.Issue.Number, refused)
		next := refused.next
		if next == "" {
			next = " Comment `/invariant revise` and I'll draft it again."
		}
		return f.note(ctx, t, c, "I can't ratify this proposal as it stands, so I haven't written anything: "+refused.Error()+".", next)
	}
	if err != nil {
		return err
	}
	return f.build(ctx, t, posted)
}

// errStale means an amendment no longer amends the project as it is.
var errStale = errors.New("the project changed since the amendment was drafted")

// commitRatification pushes the ratification commit and says so on the
// issue.
func (f *Factory) commitRatification(ctx context.Context, t Thread, state Post, c Command) (Post, error) {
	n, p := t.Issue.Number, state.Marker.Proposal
	// Whatever carried the proposal here, it's held to where it may write
	// before anything is fetched, written or said to be ratified.
	check := destination
	if p.Plan != nil {
		check = planDestination
	}
	if err := check(p); err != nil {
		return Post{}, refusedDestination{err: err}
	}
	f.logf("#%d: ratified by @%s", n, c.By)
	if err := f.Repo.Fetch(ctx); err != nil {
		return Post{}, err
	}
	if p.Plan != nil {
		posted, err := f.commitPlan(ctx, t, state, c)
		return posted, f.effectResult(ctx, n, "post the plan's ratification", err)
	}
	if p.Target != nil {
		cur, err := f.current(ctx, p.Target.Dir)
		if err != nil {
			return Post{}, err
		}
		if cur == nil || project.ProposalHash(cur.Lock.Bounds, cur.Lock.Statements) != p.Target.Amends {
			return Post{}, errStale
		}
		// An amendment keeps its project's manifest, and writes the one it
		// carries. Held to the project's own as it stands on the base, it
		// can't send the build's code or its conformance driver somewhere
		// else, or put back what the project has changed since the draft.
		if !draftEqual(p.Target.Manifest, cur.Manifest) {
			return Post{}, errStale
		}
	}
	branch, dir, from, err := f.branchFor(ctx, n, p)
	if err != nil {
		return Post{}, err
	}
	base, err := f.lockOn(ctx, dir)
	if err != nil {
		return Post{}, err
	}
	named := p.Hash
	if !matches(c.Args, p.Hash) {
		named = "the proposal " + strings.Join(c.Args, " ")
	}
	before, after := f.ratifyStep(t, state, c, named, base)
	if err := f.allowed(ctx, n, "ratify "+named, before, after); err != nil {
		return Post{}, err
	}
	wt, err := f.Repo.Worktree(ctx, branch, from)
	if err != nil {
		return Post{}, err
	}
	defer f.Repo.RemoveWorktree(ctx, wt)
	rat := &project.Ratification{By: c.By, At: c.At, Issue: n, Comment: c.URL, Proposal: p.Hash}
	if p.Target != nil {
		rat.Amends, rat.Previous = p.Target.Amends, p.Target.Previous
	}
	req := f.request(t, state.Marker.Answers, nil)
	root := filepath.Join(wt, filepath.FromSlash(dir))
	// Nothing is written through a link that leaves the worktree. Only a
	// person can have committed one, and drafting again wouldn't remove it.
	if err := inCheckout(wt, dir); err != nil {
		return Post{}, refusedDestination{err, " Once the link is gone from `" + f.Base + "`, ratify the proposal again."}
	}
	if err := p.Write(root, req.Markdown(), rat); err != nil {
		return Post{}, err
	}
	if p.Target == nil {
		if err := scaffold(root, f.Repository, dir, p); err != nil {
			return Post{}, err
		}
		readme := projectReadme(t, dir, p, state.Marker.Answers, c.By, c.URL)
		if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(readme), 0o644); err != nil {
			return Post{}, err
		}
	}
	// What's committed must be exactly what was proposed.
	written, err := project.Load(root)
	if err != nil {
		return Post{}, err
	}
	if got := project.ProposalHash(written.Lock.Bounds, written.Lock.Statements); got != p.Hash {
		return Post{}, fmt.Errorf("the project written for ratification hashes to %s, not the proposal's %s", got, p.Hash)
	}
	msg := fmt.Sprintf("Ratify the statements for #%d\n\nRatified by @%s in %s.\nProposal %s.", n, c.By, c.URL, p.Hash)
	if p.Target != nil {
		msg = fmt.Sprintf("Ratify the amendment for #%d\n\nRatified by @%s in %s.\nProposal %s, amending %s (ratified in %s).",
			n, c.By, c.URL, p.Hash, p.Target.Amends, p.Target.Previous)
	}
	// Push the ratification, unless the branch already holds it: then
	// there's nothing to commit on top of it.
	_, err = f.Repo.Commit(ctx, wt, dir, msg)
	switch {
	case errors.Is(err, ErrNothingToCommit) && from == "origin/"+branch:
	case err != nil && !errors.Is(err, ErrNothingToCommit):
		return Post{}, err
	default:
		if err := f.recovers(ctx, n, "push the ratification", f.canPushRatification(ratifying(false))); err != nil {
			return Post{}, err
		}
		if err := f.Repo.Push(ctx, wt, branch); err != nil {
			return Post{}, err
		}
	}
	if err := f.recovers(ctx, n, "say it's ratified", f.canPost(ratifying(true), recovery.Ratify)); err != nil {
		return Post{}, err
	}
	m := Marker{Kind: KindRatified, ReplyTo: []int64{c.Comment}, Answers: state.Marker.Answers, Proposal: p, ProposalRef: state.Marker.ProposalRef, Project: dir, Branch: branch, Hash: p.Hash}
	body := ratifiedComment(c.By, dir, branch, p.Hash, len(p.Statements), m)
	posted, err := f.GitHub.PostComment(ctx, n, body)
	if err != nil {
		return Post{}, f.effect(ctx, n, "post the ratification", err, nil)
	}
	return Post{Comment: posted, Marker: m}, f.status(ctx, n, LabelBuilding)
}

// branchFor picks the branch and project directory for a ratified proposal,
// and the ref to start from. If an earlier attempt already pushed this
// proposal's branch in the directory it names, it picks that up where it
// stopped; a different proposal or destination gets a numbered successor.
func (f *Factory) branchFor(ctx context.Context, issue int, p *formalize.Proposal) (branch, dir, from string, err error) {
	named := p.Dir
	if p.Target != nil {
		named = p.Target.Dir
	}
	for i := 1; ; i++ {
		branch = fmt.Sprintf("invariant/issue-%d-%s", issue, p.Slug)
		if i > 1 {
			branch += fmt.Sprintf("-%d", i)
		}
		if _, err := f.Repo.RevParse(ctx, "origin/"+branch); err != nil {
			dir, err := f.dirFor(ctx, p)
			return branch, dir, "origin/" + f.Base, err
		}
		sc, err := f.Repo.Scope(ctx, "origin/"+f.Base, "origin/"+branch, issue)
		if err != nil {
			return "", "", "", err
		}
		if sc.Project == "" || named != "" && sc.Project != named {
			continue
		}
		b, err := f.Repo.Show(ctx, "origin/"+branch, sc.Project+"/.invariant/ratified.lock")
		var lock project.Lock
		if err == nil && json.Unmarshal(b, &lock) == nil && lock.Ratified != nil && lock.Ratified.Proposal == p.Hash {
			return branch, sc.Project, "origin/" + branch, nil
		}
	}
}

// scaffold writes the language's own project file: a go.mod for Go, and a
// package.json with no dependencies for TypeScript. Python needs none.
func scaffold(root, repo, dir string, p *formalize.Proposal) error {
	switch p.Manifest().Language {
	case "go":
		gomod := fmt.Sprintf("module github.com/%s/%s\n\ngo 1.27.1\n", repo, dir)
		return os.WriteFile(filepath.Join(root, "go.mod"), []byte(gomod), 0o644)
	case "typescript":
		pkg, err := json.MarshalIndent(map[string]any{"name": p.Slug, "private": true, "type": "module",
			"description": capitalize(p.Name) + ", written by Invariant and checked against its ratified TLA+ model."}, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, "package.json"), append(pkg, '\n'), 0o644)
	}
	return nil
}

// dirFor is where a proposal's project lives: the project an amendment
// changes, the directory its issue named, or a new numbered directory.
func (f *Factory) dirFor(ctx context.Context, p *formalize.Proposal) (string, error) {
	switch {
	case p.Target != nil:
		return p.Target.Dir, nil
	case p.Dir != "":
		return p.Dir, nil
	}
	return f.projectDir(ctx, p.Slug)
}

// projectDir picks the directory for a new project: the next number in
// f.Projects, then the slug, like examples/03-bounded-buffer.
func (f *Factory) projectDir(ctx context.Context, slug string) (string, error) {
	dirs, err := f.Repo.Dirs(ctx, "origin/"+f.Base, f.Projects)
	if err != nil {
		return "", err
	}
	next := 1
	for _, d := range dirs {
		if m := numbered.FindStringSubmatch(d); m != nil {
			if n, _ := strconv.Atoi(m[1]); n >= next {
				next = n + 1
			}
		}
	}
	return path.Join(f.Projects, fmt.Sprintf("%02d-%s", next, slug)), nil
}

var numbered = regexp.MustCompile(`^(\d+)-`)

// build takes the next step of a build: the one a ratification started, or
// a writer's retry. Each effect is looked for before it's taken (D-0069):
// the agent run's record, its code on the branch, and the pull request from
// the branch. A run recorded with no result stopped partway, and the factory
// says so instead of paying for another.
func (f *Factory) build(ctx context.Context, t Thread, ratified Post) error {
	n, m := t.Issue.Number, ratified.Marker
	f.logf("#%d: building %s", n, m.Project)
	f.doing(n, "building")
	next := Marker{Kind: KindFailed, Answers: m.Answers, Proposal: m.Proposal, ProposalRef: m.ProposalRef, Project: m.Project, Branch: m.Branch, Hash: m.Hash,
		Replaces: m.Replaces, From: m.From}
	stops := t.Stops()
	// failed posts that the build failed before it made a pull request: it
	// stopped, or the factory wouldn't start it (#13).
	failed := func(why, body string) error {
		from, to := buildStep(ratified, stops, pullRequest{})
		to.kind, to.failure = protocol.KindFailed, why
		if why == FailStopped {
			to.stops++
		}
		if err := f.allowed(ctx, n, "say the build failed: "+why, from, to); err != nil {
			return err
		}
		return f.say(ctx, n, body, LabelHumanReview)
	}
	step := buildStepName(ratified)
	state, result, err := f.Repo.Run(ctx, n, step)
	if err != nil {
		return err
	}
	switch state {
	case RunRecorded:
		if err := f.recovers(ctx, n, "say the build stopped", f.canReportStopped(building(RunRecorded, false, false), recovery.Build)); err != nil {
			return err
		}
		// The ratified proposal settles the agent whose run stopped, so its
		// post names it (#179).
		next.Agent, _, _ = f.buildAgent(m)
		if said, err := f.sayUnavailable(ctx, n, step, next, failed); said || err != nil {
			return err
		}
		if said, err := f.sayStopped(ctx, n, step, next, failed); said || err != nil {
			return err
		}
		return failed(FailStopped, buildFailedComment(nil, nil, errors.New("the build's agent run stopped partway, when the watcher running it stopped or lost the repository's lease"), withFailure(next, FailStopped)))
	case RunNone:
		// A build works on the branch and under the project its ratified
		// post names, so they're held to what ratification writes there
		// before anything is checked out or an agent runs.
		if err := buildDestination(n, m); err != nil {
			return failed(FailStopped, post("needs a person", "I haven't started this build. The post that says the proposal was ratified no longer says where ratification put it: "+err.Error()+".",
				withFailure(next, FailStopped), "\n\nA person needs to look at this. Restore that comment, or comment `/invariant revise` to draft again."))
		}
		// Each build costs an agent run, so after two stop, a writer decides
		// whether to try again.
		if stops >= maxStops {
			return failed(FailLimit, buildFailedComment(nil, nil, fmt.Errorf("%d builds stopped before they made a pull request, so I haven't started another.", stops), withFailure(next, FailLimit),
				" Comment `/invariant retry` to try again"))
		}
		if isPlan(m.Project) {
			built, agent, res, runErr, err := f.runPlanBuild(ctx, t, ratified, step)
			if errors.Is(err, errSuperseded) || errors.Is(err, errResumed) {
				return nil
			}
			if err != nil {
				return f.holdRepository(ctx, t, err, LabelBuilding)
			}
			if built == "" {
				if err := f.recovers(ctx, n, "say the build stopped", f.canReportStopped(building(RunRecorded, false, false), recovery.Build)); err != nil {
					return err
				}
				next.Agent = agent
				if res != nil && res.ProviderError != "" {
					if said, err := f.sayUnavailable(ctx, n, step, next, failed); said || err != nil {
						return err
					}
				}
				var gone ratificationGone
				if errors.As(runErr, &gone) {
					next.Gone = true
					return failed(FailStopped, post("needs a person", "I can't build the plan again: "+gone.missing+".", withFailure(next, FailStopped), cantRebuildFrom()))
				}
				return failed(FailStopped, planFailedComment(nil, res, runErr, withFailure(next, FailStopped)))
			}
			return f.publish(ctx, t, ratified, built, next, stops)
		}
		built, agent, res, runErr, err := f.runBuild(ctx, t, ratified, step)
		if errors.Is(err, errSuperseded) || errors.Is(err, errResumed) {
			return nil
		}
		if err != nil {
			return err
		}
		if built == "" {
			// The run gave no result, so its record stays as a run that
			// stopped.
			if err := f.recovers(ctx, n, "say the build stopped", f.canReportStopped(building(RunRecorded, false, false), recovery.Build)); err != nil {
				return err
			}
			if res != nil {
				next.Spend, next.GateRuns = res.Spend(), len(res.GateRuns)
			}
			next.Agent = agent
			if synth.ProviderUnavailable(runErr) {
				if said, err := f.sayUnavailable(ctx, n, step, next, failed); said || err != nil {
					return err
				}
			}
			return failed(FailStopped, buildFailedComment(nil, res, runErr, withFailure(next, FailStopped)))
		}
		result = built
	}
	return f.publish(ctx, t, ratified, result, next, stops)
}

// unavailable is why a build's agent run stopped partway when its agent's
// provider, or its review's, stayed unavailable through every retry (#261),
// with what the build spent. The factory records it beside the run, which
// stays recorded with no result, before it says so, so whichever watcher
// says so, after a restart too, says why. A run that gave no result after
// its step's tenure ended leaves the same record of its own error, as
// stoppedStep names it.
type unavailable struct {
	Error    string  `json:"error"`
	Agent    string  `json:"agent,omitempty"`
	Spend    float64 `json:"spend,omitempty"`
	GateRuns int     `json:"gate_runs,omitempty"`
}

// unavailableStep names the record of why a build's run stopped, after the
// run's own.
func unavailableStep(step string) string { return step + "-unavailable" }

// stoppedStep names the record of what a build's run left when it gave no
// result after its step's tenure ended: its error and what it spent. Only
// the watcher back in the later tenure records it, so the next step's post
// says why the run stopped, as the earlier step's would have (#369's
// review).
func stoppedStep(step string) string { return step + "-stopped" }

// recordUnavailable records why a build's run stopped, unless it's already
// recorded.
func (f *Factory) recordUnavailable(ctx context.Context, n int, step string, u unavailable) error {
	return f.recordWhy(ctx, n, unavailableStep(step), u)
}

// recordWhy records u beside a build's run, in the record named name,
// unless it's already recorded.
func (f *Factory) recordWhy(ctx context.Context, n int, name string, u unavailable) error {
	b, err := json.Marshal(u)
	if err != nil {
		return err
	}
	if err := f.Repo.Record(ctx, n, name, string(b)); err != nil && !errors.Is(err, ErrRecorded) {
		return err
	}
	return nil
}

// sayStopped says a build stopped partway with the error its run left, when
// a watcher back in a later tenure recorded it, and says whether it did.
func (f *Factory) sayStopped(ctx context.Context, n int, step string, next Marker, failed func(why, body string) error) (bool, error) {
	what, err := f.Repo.Recorded(ctx, n, stoppedStep(step))
	if err != nil || what == "" {
		return false, err
	}
	var u unavailable
	if err := json.Unmarshal([]byte(what), &u); err != nil {
		return false, fmt.Errorf("the record of how #%d's build stopped can't be read: %w", n, err)
	}
	next.Spend, next.GateRuns = u.Spend, u.GateRuns
	if u.Agent != "" {
		next.Agent = u.Agent
	}
	runErr, next := errors.New(u.Error), withFailure(next, FailStopped)
	if isPlan(next.Project) {
		return true, failed(FailStopped, planFailedComment(nil, nil, runErr, next))
	}
	return true, failed(FailStopped, buildFailedComment(nil, nil, runErr, next))
}

// noResultError is what a run that gave no result left as its error.
func noResultError(runErr error) string {
	if runErr == nil {
		return "the build's agent run gave no result"
	}
	return runErr.Error()
}

// sayUnavailable says a build stopped because its provider stayed
// unavailable, when its record says so, and says whether it did. It
// publishes no pull request, so the issue goes the way of any build that
// stopped before it made one: a writer's retry builds the ratified proposal
// again, review included (#261).
func (f *Factory) sayUnavailable(ctx context.Context, n int, step string, next Marker, failed func(why, body string) error) (bool, error) {
	what, err := f.Repo.Recorded(ctx, n, unavailableStep(step))
	if err != nil || what == "" {
		return false, err
	}
	var u unavailable
	if err := json.Unmarshal([]byte(what), &u); err != nil {
		return false, fmt.Errorf("the record of why #%d's build stopped can't be read: %w", n, err)
	}
	next.Spend, next.GateRuns = u.Spend, u.GateRuns
	if u.Agent != "" {
		next.Agent = u.Agent
	}
	return true, failed(FailStopped, providerFailedComment(u.Error, withFailure(next, FailStopped)))
}

// buildStepName names a build's record after the post that started it: a
// ratification, or the answer to a writer's retry.
func buildStepName(ratified Post) string {
	return fmt.Sprintf("build-%d", ratified.Comment.ID)
}

// builtResult is what a build's run leaves in its record, on top of the
// code it wrote: what synthesis reported, what went wrong, if anything, and
// the agent that built it (#179).
type builtResult struct {
	Result   *synth.Result         `json:"result"`
	Plumbing *plumbing.BuildResult `json:"plumbing,omitempty"` // a plan's build, in place of a project's (D-0105)
	Error    string                `json:"error,omitempty"`
	Agent    string                `json:"agent,omitempty"`
}

// runBuild records the build's agent run, runs it, and commits the code it
// wrote on top of the branch, without pushing it. Then the run's record
// moves to its result: a commit on top of that code, holding what the run
// reported. runBuild returns that commit, or "" when the run gave no result,
// and the agent that ran, or "" when none did. The run's record and its
// result name the agent (#179).
func (f *Factory) runBuild(ctx context.Context, t Thread, ratified Post, step string) (string, string, *synth.Result, error, error) {
	n, m := t.Issue.Number, ratified.Marker
	// The issue's agent builds it, and no other, so a build whose agent the
	// watcher can't run fails before anything is recorded (#173).
	agent, run, ok := f.buildAgent(m)
	if !ok || run.Builder == nil {
		return "", "", nil, cantRun(agent), nil
	}
	release, err := f.agentSlot(ctx, n)
	if err != nil {
		return "", "", nil, nil, err
	}
	defer release()
	if err := f.Repo.Fetch(ctx); err != nil {
		return "", "", nil, nil, err
	}
	wt, err := f.Repo.Worktree(ctx, m.Branch, "origin/"+m.Branch)
	if err != nil {
		return "", "", nil, nil, err
	}
	defer f.Repo.RemoveWorktree(ctx, wt)
	root := filepath.Join(wt, filepath.FromSlash(m.Project))
	out, err := f.newRun(n, "build")
	if err != nil {
		return "", "", nil, nil, err
	}
	// An agent run is an effect: only the lease's holder records and starts
	// one, and it's recorded before it starts.
	if err := f.recovers(ctx, n, "start the build's agent run", f.canStartRun(building(RunNone, false, false), recovery.Build)); err != nil {
		return "", "", nil, nil, err
	}
	if err := f.Repo.Record(ctx, n, step, fmt.Sprintf("the build of %s for #%d by %s", m.Project, n, agent)); err != nil {
		return "", "", nil, nil, err
	}
	f.runsAgent(n, agent, out)
	amend := m.Proposal != nil && m.Proposal.Target != nil
	agentCtx, closed := f.whileOpen(ctx, n)
	res, runErr := run.Builder.Build(agentCtx, root, out, amend)
	if closed() {
		return "", "", nil, nil, errIssueClosed
	}
	// A watcher that lost the lease during the run drops it. The holder will
	// find it recorded, and say it stopped. One that took it back since, in
	// a new tenure, records the run's result, and its next step publishes
	// it, if the build still stands.
	if !f.holds() {
		return "", "", nil, nil, errLeaseLost
	}
	ctx, resumed := f.resumed(ctx)
	// Anyone can read the run's error and review, in a post or the record, so
	// they name the paths on this machine as localPaths does.
	runErr = localError(runErr, f.Work)
	if res != nil && res.Review != nil {
		res.Review.Text = localPaths(res.Review.Text, f.Work)
	}
	// A build whose agent's provider, or its review's, never let it finish
	// stopped partway, whatever its leftover code would pass, so it gives
	// no result and publishes nothing (#261). Its record says why.
	if synth.ProviderUnavailable(runErr) {
		u := unavailable{Error: runErr.Error(), Agent: agent}
		if res != nil {
			u.Spend, u.GateRuns = res.Spend(), len(res.GateRuns)
		}
		if err := f.recordUnavailable(ctx, n, step, u); err != nil {
			return "", "", nil, nil, err
		}
		if resumed {
			return "", "", nil, nil, errResumed
		}
		return "", agent, res, runErr, nil
	}
	if res == nil || res.Final == nil {
		if resumed {
			u := unavailable{Error: noResultError(runErr), Agent: agent}
			if res != nil {
				u.Spend, u.GateRuns = res.Spend(), len(res.GateRuns)
			}
			if err := f.recordWhy(ctx, n, stoppedStep(step), u); err != nil {
				return "", "", nil, nil, err
			}
			return "", "", nil, nil, errResumed
		}
		return "", agent, res, runErr, nil
	}
	// The receipt the pull request carries names the agent that built the
	// code, which its fingerprint leaves out (#179).
	res.Final.Agent = agent
	// The agent's files replace the project's, so a file it removed is gone.
	if err := removeOwned(root); err != nil {
		return "", "", nil, nil, err
	}
	dir := res.Dir
	if dir == "" {
		dir = filepath.Join(out, "result")
	}
	if err := copyResult(dir, root); err != nil {
		return "", "", nil, nil, err
	}
	verdict := "passed the gate"
	if !res.Final.Passed {
		verdict = "didn't pass the gate"
	}
	msg := fmt.Sprintf("Implement #%d: %s\n\nWritten by Invariant against the statements ratified in the previous commit. It %s: %s.",
		n, t.Issue.Title, verdict, res.Final.Claim())
	if amend {
		msg = fmt.Sprintf("Implement #%d: %s\n\nChanged by Invariant to meet the amended statements ratified in the previous commit. It %s: %s.",
			n, t.Issue.Title, verdict, res.Final.Claim())
	}
	code, err := f.Repo.Commit(ctx, wt, m.Project, msg)
	if errors.Is(err, ErrNothingToCommit) {
		// The branch already holds exactly this code.
		code, err = f.Repo.RevParse(ctx, m.Branch)
	}
	if err != nil {
		return "", "", nil, nil, err
	}
	// Anyone can fetch the record, so the result it holds names its directory
	// by where it is under the work directory: the watcher's own path names
	// its user.
	kept := *res
	kept.Dir = f.workRelative(res.Dir)
	saved := builtResult{Result: &kept, Agent: agent}
	if runErr != nil {
		saved.Error = runErr.Error()
	}
	result, err := f.saveResult(ctx, saved, fmt.Sprintf("invariant: the result of the build for #%d by %s", n, agent), code)
	if err != nil {
		return "", "", nil, nil, err
	}
	if why, err := f.supersededAfterBuild(ctx, n, ratified); err != nil {
		return "", "", nil, nil, err
	} else if why != current {
		return "", "", nil, nil, f.finishSuperseded(ctx, n, why, step, result)
	}
	if err := f.recovers(ctx, n, "record the build's result", f.canFinishRun(building(RunRecorded, false, false), recovery.Build)); err != nil {
		return "", "", nil, nil, err
	}
	if err := f.Repo.Finish(ctx, n, step, result); err != nil {
		return "", "", nil, nil, err
	}
	if resumed {
		return "", "", nil, nil, errResumed
	}
	return result, agent, res, runErr, nil
}

// workRelative names dir by where it is under the factory's work directory,
// in slash form, such as issue-1/build-20260928-120000/result, or "" when
// it isn't under it. Synthesis reports an absolute path even when the work
// directory is relative, so both are made absolute first.
func (f *Factory) workRelative(dir string) string {
	if dir == "" {
		return ""
	}
	work, err := filepath.Abs(f.Work)
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(work, abs)
	if err != nil || !filepath.IsLocal(rel) {
		return ""
	}
	return filepath.ToSlash(rel)
}

// saveResult commits a run's result as result.json, on top of parent.
func (f *Factory) saveResult(ctx context.Context, v any, message, parent string) (string, error) {
	dir, err := os.MkdirTemp("", "invariant-result-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "result.json"), b, 0o644); err != nil {
		return "", err
	}
	return f.Repo.Save(ctx, dir, message, parent)
}

// loadResult reads a run's result.json from its result commit.
func (f *Factory) loadResult(ctx context.Context, commit string, v any) error {
	dir, err := os.MkdirTemp("", "invariant-result-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := f.Repo.Load(ctx, commit, dir); err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(dir, "result.json"))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// publish takes a finished build to its post: it pushes the build's code,
// unless the branch has it, opens the pull request, unless the build opened
// one from its branch already, open, closed or merged, and says how the
// build went. Whichever watcher ran the build, its record holds everything
// this needs, and a build a later post answered or replaced publishes
// nothing.
func (f *Factory) publish(ctx context.Context, t Thread, ratified Post, result string, next Marker, stops int) error {
	n, m := t.Issue.Number, ratified.Marker
	var built builtResult
	if err := f.loadResult(ctx, result, &built); err != nil {
		return err
	}
	// A watcher back from a pause publishes nothing of a build a later post
	// answered or replaced (#271).
	if ok, err := f.stillBuilding(ctx, n, ratified); err != nil || !ok {
		return err
	}
	if built.Plumbing != nil {
		err := f.publishPlan(ctx, t, ratified, result, built, next, stops)
		var authority repositoryAuthority
		if errors.As(err, &authority) {
			return f.holdRepository(ctx, t, err, LabelBuilding)
		}
		return f.effectResult(ctx, n, "publish the plumbing pull request", err)
	}
	res := built.Result
	if res == nil || res.Final == nil {
		return fmt.Errorf("the build's record at %s holds no result", result)
	}
	var runErr error
	if built.Error != "" {
		runErr = errors.New(built.Error)
	}
	// The post and the pull request name the agent the record names (#179).
	next.Spend, next.GateRuns, next.Agent = res.Spend(), len(res.GateRuns), built.Agent
	if err := f.Repo.Fetch(ctx); err != nil {
		return err
	}
	code, err := f.Repo.RevParse(ctx, result+"^")
	if err != nil {
		return err
	}
	head := "origin/" + m.Branch
	if has, err := f.Repo.Holds(ctx, head, code); err != nil {
		return err
	} else if !has {
		if err := f.recovers(ctx, n, "push the code", f.canPushCode(building(RunDone, false, false))); err != nil {
			return err
		}
		if err := f.Repo.PushCommit(ctx, code, m.Branch); err != nil {
			return err
		}
		if err := f.Repo.Fetch(ctx); err != nil {
			return err
		}
	}
	// The pull request this makes, in the protocol's terms: its head, the
	// lock there, and whether it changes only its project.
	at := pullRequest{head: code, gate: protocol.GatePending}
	b, lockErr := f.Repo.Show(ctx, code, lockFileOf(m.Project))
	at.lock = lockAt(m.Project, b, lockErr)
	sc, err := f.Repo.Scope(ctx, "origin/"+f.Base, head, n)
	if err != nil {
		return err
	}
	at.scopeMany = len(sc.Problems) > 0 || sc.Project != m.Project
	from, to := buildStep(ratified, stops, at)
	if !res.Final.Passed {
		to.kind, to.failure = protocol.KindFailed, FailGate
	}
	if err := f.allowed(ctx, n, "open a pull request", from, to); err != nil {
		return err
	}
	if ok, err := f.stillBuilding(ctx, n, ratified); err != nil || !ok {
		return err
	}
	pr, found, err := f.buildPullRequest(ctx, m.Branch, ratified)
	if err != nil {
		return err
	}
	if !found {
		if err := f.recovers(ctx, n, "open a pull request", f.canOpenPullRequest(building(RunDone, true, false), res.Final.Passed)); err != nil {
			return err
		}
		if pr, err = f.GitHub.CreatePullRequest(ctx, github.NewPullRequest{
			Title: t.Issue.Title, Head: m.Branch, Base: f.Base, Draft: !res.Final.Passed,
			Body: pullRequestBody(t, next, res, m.Proposal, sc.Trusted),
		}); err != nil {
			return f.effect(ctx, n, "create the pull request", err, nil)
		}
	}
	var refusal error
	if found && res.Final.Passed && pr.State == "open" {
		if pr, refusal, err = f.markReady(ctx, n, ratified, pr); err != nil {
			return err
		}
	}
	next.PR = pr.Number
	if ok, err := f.stillBuilding(ctx, n, ratified); err != nil || !ok {
		return err
	}
	if err := f.recovers(ctx, n, "say how the build went", f.canPost(openedAs(pr), recovery.Build)); err != nil {
		return err
	}
	if refusal != nil {
		return f.unready(ctx, t, ratified, stops, at, pr, refusal, next)
	}
	if !res.Final.Passed {
		if err := f.GitHub.AddLabels(ctx, pr.Number, LabelHumanReview); err != nil {
			return f.effect(ctx, n, "label the pull request for a person", err, nil)
		}
		next.Failure = FailGate
		return f.say(ctx, n, buildFailedComment(&pr, res, runErr, next), LabelHumanReview)
	}
	next.Kind = KindPR
	return f.say(ctx, n, prComment(pr, res, sc.Trusted, next), LabelPR)
}

// removeOwned deletes the files in a project that the agent owns, before
// its result is copied in.
func removeOwned(root string) error {
	p, err := project.Load(root)
	if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(file string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, file)
		if synth.Owned(p.Manifest, filepath.ToSlash(rel)) {
			return os.Remove(file)
		}
		return nil
	})
}

func withFailure(m Marker, why string) Marker {
	m.Failure = why
	return m
}

// copyResult copies the finished project over the ratified one. The model,
// the code and anything else the agent wrote come across; the people's files
// are the same in both, because synthesis assembles the result from the
// originals.
func copyResult(from, to string) error {
	return filepath.WalkDir(from, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
}

// closedIssue records an external close without taking another ordinary
// step. It neither resumes queued commands nor changes an open pull request.
// A final post is durable before label cleanup, so another poll can finish
// that cleanup without repeating the observation.
func (f *Factory) closedIssue(ctx context.Context, t Thread, state Post) error {
	if len(t.Posts) == 0 {
		return nil
	}
	m := state.Marker
	finished := m.Kind == KindMerged || m.Kind == KindClosed
	// A closure leaves the working state intact for a reopened issue. Until
	// another working post follows it, the close isn't recorded again, but a
	// person can still merge the issue's pull request, and that merge is
	// recorded (#434's review).
	noted := false
	for i := len(t.Posts) - 1; i >= 0; i-- {
		p := t.Posts[i]
		if p.Comment.ID == state.Comment.ID {
			break
		}
		if p.Marker.Kind == KindNote && p.Marker.IssueClosed {
			noted = true
			break
		}
	}
	if m.Kind == KindRatified && isIssuePlan(m) {
		for _, p := range t.Posts {
			if p.Marker.Kind == KindPlanDone && p.Marker.Hash == m.Proposal.Hash {
				finished = true
				break
			}
		}
	}
	if finished {
		return f.clearActive(ctx, t, m)
	}
	var pr github.PullRequest
	var err error
	switch {
	case m.PR != 0:
		pr, err = f.GitHub.PullRequest(ctx, m.PR)
	case m.Kind == KindRatified && m.Branch != "" && !isIssuePlan(m):
		// Publication may have opened the pull request before its post was
		// interrupted. Recover only this build's pull request, including one
		// a person merged before the next poll, without resuming the build.
		var found bool
		pr, found, err = f.buildPullRequest(ctx, m.Branch, state)
		if found {
			state.Marker.Kind, state.Marker.PR = KindPR, pr.Number
			m.PR = pr.Number
		}
	}
	if err != nil {
		return err
	}
	if pr.Merged {
		return f.watchMerged(ctx, t, state, pr)
	}
	if noted {
		return f.clearActive(ctx, t, m)
	}
	next := m.carried()
	next.Kind, next.ReplyTo, next.Failure = KindNote, nil, ""
	next.IssueClosed = true
	return f.say(ctx, t.Issue.Number, post("issue closed", "This issue was closed, so I've stopped work on it.", next), "")
}

// clearActive removes a closed issue's active labels, if it has any, once
// its final post is durable. A merge's label stays.
func (f *Factory) clearActive(ctx context.Context, t Thread, m Marker) error {
	for _, label := range activeLabels {
		if t.Issue.HasLabel(label) {
			keep := ""
			if m.Kind == KindMerged || t.Issue.HasLabel(LabelMerged) {
				keep = LabelMerged
			}
			return f.status(ctx, t.Issue.Number, keep)
		}
	}
	return nil
}

// watch follows an open pull request, and merges it once CI's gate passes on
// its head, it stays in scope, and its lock is what was ratified.
func (f *Factory) watch(ctx context.Context, t Thread, state Post) error {
	n, m := t.Issue.Number, state.Marker
	pr, err := f.GitHub.PullRequest(ctx, m.PR)
	if err != nil {
		return err
	}
	next := m.carried()
	next.ReplyTo, next.Failure = nil, ""
	switch {
	case pr.Merged:
		return f.watchMerged(ctx, t, state, pr)
	case pr.State == "closed":
		from, to := prStep(t, state, pullRequest{head: pr.Head.SHA}, protocol.KindClosed, protocol.Nobody)
		if err := f.allowed(ctx, n, fmt.Sprintf("record that #%d was closed", pr.Number), from, to); err != nil {
			return err
		}
		next.Kind = KindClosed
		return f.say(ctx, n, closedComment(pr, next), "")
	}
	runs, err := f.GitHub.CheckRuns(ctx, pr.Head.SHA, f.Check)
	if err != nil {
		return err
	}
	run, done := latest(runs)
	if !done {
		return nil
	}
	next.Kind = KindFailed
	if run.Conclusion != "success" {
		started := gateRan
		if run.Conclusion == "failure" {
			job, err := f.GitHub.Job(ctx, run.ID)
			switch {
			case errors.Is(err, github.ErrNoPermission):
				// Without the Actions permission, the App can't read the job,
				// so it can't tell a job that ran from one GitHub never
				// started. It says so, rather than asking again every poll
				// (copythis-ad#38).
				started = gateUnknown
			case err != nil:
				return err
			case !job.Started():
				started = gateNeverStarted
			}
		}
		from, to := prStep(t, state, pullRequest{head: pr.Head.SHA, gate: gateOf(run, done)}, protocol.KindFailed, protocol.Nobody)
		to.failure = FailCI
		if err := f.allowed(ctx, n, fmt.Sprintf("say CI's gate failed on #%d", pr.Number), from, to); err != nil {
			return err
		}
		next.Failure = FailCI
		return f.say(ctx, n, ciFailedComment(pr, run, started, next), LabelHumanReview)
	}
	if err := f.Repo.Fetch(ctx); err != nil {
		return err
	}
	head := "origin/" + m.Branch
	if sha, err := f.Repo.RevParse(ctx, head); err != nil {
		return err
	} else if sha != pr.Head.SHA {
		return nil // the branch moved; wait for CI on its new head
	}
	// A draft no writer's retry watches as that draft, such as one a person
	// made of the factory's pull request to stop it, before any retry
	// watched it or after a writer's retry, the factory never marks ready,
	// and says so once (#369's review, #371). GitHub failing for a moment to
	// show the draft's events is asked again on the next poll, under #376's
	// bound, and past it, or when GitHub refuses, the factory can't tell the
	// draft is the one a retry watched, so it says so once in the same post
	// (#389's review).
	watched := false
	if pr.Draft {
		w, err := f.watchOf(ctx, t, pr)
		var unread error
		if err != nil {
			var retry error
			if unread, retry = f.gitHubsAnswer(ctx, n, failingEventsStep(state, pr.Number, pr.Head.SHA), pr, err); retry != nil {
				return retry
			}
			f.logf("#%d: GitHub didn't show draft #%d's events, so I won't mark it ready: %v", n, pr.Number, unread)
		} else if err := f.failingEnded(ctx, n, failingEventsStep(state, pr.Number, pr.Head.SHA)); err != nil {
			return err // the read is asked again on the next poll, which ends the run then
		}
		if watched = w == watchDraft; !watched {
			// A configured trusted draft is already held for a person. Read
			// its actual paths before explaining what a retry could do.
			if _, proceed, err := f.checkMergeScope(ctx, t, state, pr, pullRequest{head: pr.Head.SHA, gate: gateOf(run, done)}, false, next); err != nil || !proceed {
				return err
			}
			return f.unwatched(ctx, t, state, pr, pullRequest{head: pr.Head.SHA, gate: gateOf(run, done)}, w, unread, next)
		}
	}
	at, proceed, err := f.checkMergeScope(ctx, t, state, pr, pullRequest{head: pr.Head.SHA, gate: gateOf(run, done)}, watched, next)
	if err != nil || !proceed {
		return err
	}
	// The protocol has the last word on merging.
	from, to := prStep(t, state, at, protocol.KindMerged, protocol.ByFactory)
	if err := f.allowed(ctx, n, fmt.Sprintf("merge #%d", pr.Number), from, to); err != nil {
		return err
	}
	// The factory marks ready only a failed draft a writer's retry watches
	// again (#354), and only once GitHub says it can merge it: it waits for
	// GitHub to work that out, and reports one it can't merge without
	// marking it ready. The core refuses any other draft, such as one a
	// person made of the factory's pull request to stop it, for a person.
	if pr.Draft {
		switch {
		case pr.Mergeable == nil:
			f.waitingToMerge(n, pr)
			return nil
		case !*pr.Mergeable:
			return f.refusedMerge(ctx, t, state, pr, at, watched, false, errCantMerge, next)
		}
		if err := f.recovers(ctx, n, fmt.Sprintf("mark #%d ready", pr.Number), f.canMarkReady(mergingPR(watched, pr, true, false))); err != nil {
			return err
		}
	}
	if err := f.recovers(ctx, n, fmt.Sprintf("merge #%d", pr.Number), f.canMerge(merging(true, false))); err != nil {
		return err
	}
	// The merge is recorded before it happens, with what the protocol
	// checked, so a watcher that finds the pull request merged knows the
	// factory merged it. A record left by an attempt that stopped stands.
	if err := f.recordMerge(ctx, n, pr.Number, at); err != nil && !errors.Is(err, ErrRecorded) {
		return err
	}
	// GitHub won't merge a draft, such as a failed build's that a writer
	// retried, so the factory marks it ready for review first, and merges it
	// only if GitHub did (#145). It reads the draft's head again just
	// before, so a push since it checked can't have it mark ready a head
	// whose gate hasn't passed, and reads the pull request again after.
	if pr.Draft {
		now, err := f.GitHub.PullRequest(ctx, pr.Number)
		if err != nil {
			return err
		}
		if now.State != "open" || now.Head.SHA != pr.Head.SHA {
			return nil
		}
		if proceed, err := f.refreshMergeScope(ctx, t, state, pr, at, watched, next); err != nil || !proceed {
			return err
		}
		if err := f.GitHub.MarkReady(ctx, pr.Number); err != nil {
			return f.refusedMerge(ctx, t, state, pr, at, watched, true, err, next)
		}
		if pr, err = f.GitHub.PullRequest(ctx, pr.Number); err != nil || pr.Head.SHA != at.head {
			return err // a push while it was marked ready waits for CI on its new head
		}
	}
	// The merge is pinned to the head whose gate, scope, lock and trusted
	// files the factory checked, so GitHub merges nothing pushed since.
	if proceed, err := f.refreshMergeScope(ctx, t, state, pr, at, watched, next); err != nil || !proceed {
		return err
	}
	sha, err := f.GitHub.Merge(ctx, pr.Number, at.head, "merge")
	if err != nil {
		return f.refusedMerge(ctx, t, state, pr, at, watched, false, err, next)
	}
	f.logf("#%d: merged #%d as %s", n, pr.Number, sha)
	if err := f.recovers(ctx, n, "say it merged", f.canPost(merging(true, true), recovery.Merge)); err != nil {
		return err
	}
	return f.saidMerged(ctx, t, pr, run, sha, next)
}

// checkMergeScope keeps trust and lock authority in the actual diff, including
// for recovered drafts. Display metadata never grants or denies a merge.
func (f *Factory) checkMergeScope(ctx context.Context, t Thread, state Post, pr github.PullRequest, at pullRequest, watched bool, next Marker) (pullRequest, bool, error) {
	n, m := t.Issue.Number, state.Marker
	sc, err := f.Repo.Scope(ctx, "origin/"+f.Base, at.head, n)
	if err != nil {
		return at, false, err
	}
	if len(sc.Files) == 0 {
		// An empty diff alone cannot tell whether a person merged it.
		fresh, err := f.GitHub.PullRequest(ctx, pr.Number)
		if err != nil {
			return at, false, err
		}
		if fresh.Merged {
			return at, false, f.watchMerged(ctx, t, state, fresh)
		}
	}
	problems := sc.Problems
	if sc.Project != m.Project {
		problems = append(problems, fmt.Sprintf("it changes %q, not the ratified project %q", sc.Project, m.Project))
	}
	b, lockErr := f.Repo.Show(ctx, at.head, lockFileOf(m.Project))
	at.lock = lockAt(m.Project, b, lockErr)
	if lockErr != nil {
		problems = append(problems, "its lock can't be read: "+lockErr.Error())
	} else if at.lock != m.Hash {
		problems = append(problems, "its lock isn't the proposal that was ratified")
	}
	if isPlan(m.Project) && lockErr == nil {
		if lock, err := plumbing.ReadLock(b); err == nil {
			if _, err := f.lockedRepository(ctx, m, &lock.Plan); err != nil {
				var authority repositoryAuthority
				if !errors.As(err, &authority) {
					return at, false, err
				}
				problems = append(problems, err.Error()+". Review and commit the original contract and capable gate on the base branch. Restoring authority alone does not resume the merge: a person with write access must then comment `/invariant retry`.")
			}
		}
	}
	// The existing protocol hold covers changes only a person may merge.
	at.scopeMany = len(problems) > 0 || len(sc.Trusted) > 0
	if len(problems) > 0 {
		from, to := prStep(t, state, at, protocol.KindFailed, protocol.Nobody)
		to.failure = FailUnmergeable
		if err := f.allowed(ctx, n, fmt.Sprintf("say #%d can't merge", pr.Number), from, to); err != nil {
			return at, false, err
		}
		if !pr.Draft || watched {
			if err := f.recovers(ctx, n, "say it can't merge", f.canPost(mergingPR(watched, pr, false, false), recovery.Merge)); err != nil {
				return at, false, err
			}
		}
		next.Kind, next.Failure = KindFailed, FailUnmergeable
		return at, false, f.say(ctx, n, scopeFailedComment(pr, problems, next), LabelHumanReview)
	}
	if len(sc.Trusted) > 0 {
		return at, false, f.waitForPerson(ctx, t, state, pr, at, watched, sc.Trusted, next)
	}
	return at, true, nil
}

// refreshMergeScope fetches immediately before readiness or merge. A base
// advance must still honor the ratified execution contract; a new head needs
// its own CI result. Compatible base movement keeps the original novelty rule.
func (f *Factory) refreshMergeScope(ctx context.Context, t Thread, state Post, pr github.PullRequest, at pullRequest, watched bool, next Marker) (bool, error) {
	if err := f.Repo.Fetch(ctx); err != nil {
		return false, err
	}
	head, err := f.Repo.RevParse(ctx, "origin/"+state.Marker.Branch)
	if err != nil || head != at.head {
		return false, err
	}
	_, proceed, err := f.checkMergeScope(ctx, t, state, pr, at, watched, next)
	return proceed, err
}

// watchMerged records a merge already observed on GitHub, using the same
// protocol and recovery checks wherever the watcher noticed it.
func (f *Factory) watchMerged(ctx context.Context, t Thread, state Post, pr github.PullRequest) error {
	n, m := t.Issue.Number, state.Marker
	next := m.carried()
	next.ReplyTo, next.Failure = nil, ""
	// A merge the factory recorded, at this head, is the factory's: it
	// stopped after merging and before it said so (D-0069). Only while
	// its latest post is the pull request's, though: the record of an
	// attempt GitHub refused stays, and a person may merge after that
	// (#145).
	if at, ok, err := f.mergeRecord(ctx, n, pr.Number, pr.Head.SHA); err != nil {
		return err
	} else if ok && at.head == pr.Head.SHA && m.Kind == KindPR && !mergedByOther(f.Self, pr) {
		runs, err := f.GitHub.CheckRuns(ctx, pr.Head.SHA, f.Check)
		if err != nil {
			return err
		}
		run, _ := latest(runs)
		from, to := prStep(t, state, at, protocol.KindMerged, protocol.ByFactory)
		if err := f.allowed(ctx, n, fmt.Sprintf("record that the factory merged #%d", pr.Number), from, to); err != nil {
			return err
		}
		if err := f.recovers(ctx, n, "say it merged", f.canPost(merging(true, true), recovery.Merge)); err != nil {
			return err
		}
		return f.saidMerged(ctx, t, pr, run, pr.MergeCommitSHA, next)
	}
	from, to := prStep(t, state, pullRequest{head: pr.Head.SHA}, protocol.KindMerged, protocol.ByOther)
	if err := f.allowed(ctx, n, fmt.Sprintf("record that #%d was merged", pr.Number), from, to); err != nil {
		return err
	}
	next.Kind = KindMerged
	return f.say(ctx, n, post("merged", fmt.Sprintf("#%d was merged.", pr.Number), next), LabelMerged)
}

// saidMerged deletes a merged pull request's branch and says the factory
// merged it, with the issue's numbers.
func (f *Factory) saidMerged(ctx context.Context, t Thread, pr github.PullRequest, run github.CheckRun, sha string, next Marker) error {
	n := t.Issue.Number
	if err := f.GitHub.DeleteBranch(ctx, next.Branch); err != nil {
		f.logf("#%d: deleting %s: %v", n, next.Branch, err)
		return f.effect(ctx, n, "delete branch "+next.Branch, err, nil)
	}
	next.Kind = KindMerged
	numbers := NumbersOf(t, f.now())
	next.Numbers = &numbers
	return f.say(ctx, n, mergedComment(pr, run, sha, &next), LabelMerged)
}

// refusedMerge says once, on the issue, that GitHub refused to merge the
// pull request, or, when unready, to mark its draft ready for review, and
// quotes GitHub's answer (#145). Nothing is tried again until a writer's
// retry has the factory watch the pull request again. The protocol sees a
// pull request the factory can't merge, as it sees a change to the trusted
// base (D-0105), and the recovery core sees one GitHub can't merge. A lost
// lease or an ended context isn't GitHub's answer, and is returned instead.
// GitHub failing for a moment, as with a 502, is returned too, so the next
// poll tries again, until it has kept failing for longer than the factory
// asks for, which is said in the refusal's place (#369's review, #376).
// watched says a draft is one a writer's retry watches as that draft
// (#371).
func (f *Factory) refusedMerge(ctx context.Context, t Thread, state Post, pr github.PullRequest, at pullRequest, watched, unready bool, answer error, next Marker) error {
	n := t.Issue.Number
	answer, retry := f.gitHubsAnswer(ctx, n, failingStep(state, unready, pr.Number, at.head), pr, answer)
	if retry != nil {
		return retry
	}
	var kept keptFailing
	switch {
	case !errors.As(answer, &kept):
		f.logf("#%d: GitHub refused the merge of #%d: %v", n, pr.Number, answer)
	case unready:
		f.logf("#%d: GitHub kept failing to mark #%d ready for review, for %s, so I've stopped asking: %v", n, pr.Number, kept.span(), answer)
	default:
		f.logf("#%d: GitHub kept failing to merge #%d, for %s, so I've stopped asking: %v", n, pr.Number, kept.span(), answer)
	}
	at.scopeMany = true
	from, to := prStep(t, state, at, protocol.KindFailed, protocol.Nobody)
	to.failure = FailMerge
	if err := f.allowed(ctx, n, fmt.Sprintf("say GitHub refused the merge of #%d", pr.Number), from, to); err != nil {
		return err
	}
	if err := f.recovers(ctx, n, "say GitHub refused the merge", f.canPost(mergingPR(watched, pr, false, false), recovery.Merge)); err != nil {
		return err
	}
	next.Kind, next.Failure = KindFailed, FailMerge
	operation := fmt.Sprintf("merge #%d", pr.Number)
	if unready {
		operation = fmt.Sprintf("mark #%d ready for review", pr.Number)
	}
	return f.sayRefused(ctx, n, operation, answer, next, func(m Marker) string {
		return mergeRefusedComment(pr, unready, answer, m)
	}, 0)
}

// errCantMerge is GitHub's answer for a draft it says it can't merge, which
// the factory reports without marking it ready.
var errCantMerge = errors.New("GitHub says it can't merge it as it is, as when it conflicts with its base branch, so I haven't marked it ready for review")

// markReady marks a draft ready for review when a build that passed finds it
// open from its branch, opened without the build's result, before the build
// says the pull request is open (#145). ratified is the post that started
// the build. The core has the build open its pull request ready, so it
// checks that. It returns the pull request as GitHub has it then, and
// GitHub's answer when GitHub refused, which the build posts as a failure
// that names the draft, so a writer's retry watches it again, rather than
// leave it a draft no retry watches. An error GitHub may not repeat, such as
// a 502, the next poll tries again (#369's review), until GitHub has kept
// failing for longer than the factory asks for, which the build posts in
// the refusal's place (#376).
func (f *Factory) markReady(ctx context.Context, n int, ratified Post, pr github.PullRequest) (github.PullRequest, error, error) {
	if !pr.Draft {
		return pr, nil, nil
	}
	if err := f.recovers(ctx, n, fmt.Sprintf("mark #%d ready", pr.Number), f.canOpenPullRequest(building(RunDone, true, false), true)); err != nil {
		return pr, nil, err
	}
	if err := f.GitHub.MarkReady(ctx, pr.Number); err != nil {
		answer, retry := f.gitHubsAnswer(ctx, n, failingStep(ratified, true, pr.Number, pr.Head.SHA), pr, err)
		if retry != nil {
			return pr, nil, retry
		}
		var kept keptFailing
		if errors.As(answer, &kept) {
			f.logf("#%d: GitHub kept failing to mark draft #%d ready for review, for %s, so I've stopped asking: %v", n, pr.Number, kept.span(), answer)
		} else {
			f.logf("#%d: GitHub refused to mark draft #%d ready for review: %v", n, pr.Number, answer)
		}
		return pr, answer, nil
	}
	pr, err := f.GitHub.PullRequest(ctx, pr.Number)
	return pr, nil, err
}

// unready posts that a build that passed found a draft open from its branch
// that GitHub refused to mark ready, or kept failing to, as the protocol's
// build that left a draft, which a writer's retry watches again (#369's
// review, #376).
func (f *Factory) unready(ctx context.Context, t Thread, ratified Post, stops int, at pullRequest, pr github.PullRequest, answer error, next Marker) error {
	n := t.Issue.Number
	from, to := buildStep(ratified, stops, at)
	to.kind, to.failure = protocol.KindFailed, FailUnready
	if err := f.allowed(ctx, n, fmt.Sprintf("say GitHub refused to mark #%d ready", pr.Number), from, to); err != nil {
		return err
	}
	next.Kind, next.Failure = KindFailed, FailUnready
	if permanentEffect(answer) {
		return f.sayRefused(ctx, n, fmt.Sprintf("mark #%d ready for review", pr.Number), answer, next, func(m Marker) string {
			return unreadyComment(pr, answer, m)
		}, pr.Number)
	}
	if err := f.GitHub.AddLabels(ctx, pr.Number, LabelHumanReview); err != nil {
		return f.effect(ctx, n, "label the pull request for a person", err, nil)
	}
	return f.say(ctx, n, unreadyComment(pr, answer, next), LabelHumanReview)
}

// unwatched posts once that CI's gate passed on a draft no writer's retry
// watches as that draft, such as one a person made of the factory's pull
// request, before any retry watched it or, as w says, after a retry: the
// factory won't mark it ready. When GitHub didn't show its events, unread
// is GitHub's answer, a refusal or its having kept failing (#389's review).
// The issue leaves the pull request's state, so the factory hears a
// writer's commands again, and a retry watches the draft. The post is the
// protocol's alone, since the core's model has no draft a retry didn't
// leave, as it has no CI failure (#369's review, #371).
func (f *Factory) unwatched(ctx context.Context, t Thread, state Post, pr github.PullRequest, at pullRequest, w watchState, unread error, next Marker) error {
	n := t.Issue.Number
	at.scopeMany = true
	from, to := prStep(t, state, at, protocol.KindFailed, protocol.Nobody)
	to.failure = FailDraft
	if err := f.allowed(ctx, n, fmt.Sprintf("say #%d is a draft no retry watches", pr.Number), from, to); err != nil {
		return err
	}
	next.Kind, next.Failure = KindFailed, FailDraft
	return f.say(ctx, n, unwatchedComment(pr, w, unread, next), LabelHumanReview)
}

// waitingToMerge logs once, for each draft and head, that the factory waits
// for GitHub to work out whether it can merge a watched draft before it
// marks it ready (#369's review).
func (f *Factory) waitingToMerge(n int, pr github.PullRequest) {
	key := fmt.Sprintf("%d@%s", pr.Number, pr.Head.SHA)
	f.mu.Lock()
	if f.mergeWait == nil {
		f.mergeWait = map[string]bool{}
	}
	said := f.mergeWait[key]
	f.mergeWait[key] = true
	f.mu.Unlock()
	if !said {
		f.logf("#%d: waiting for GitHub to say whether it can merge draft #%d before I mark it ready", n, pr.Number)
	}
}

// mergeRecordJSON is a pull request as the protocol saw it just before the
// factory merged it.
type mergeRecordJSON struct {
	Head      string `json:"head"`
	Gate      int8   `json:"gate"`
	Lock      string `json:"lock"`
	ScopeMany bool   `json:"scope_many,omitempty"`
}

// mergeStepName names a merge's record after the pull request and the head
// it merges, so a merge at a head that moved has a record of its own.
func mergeStepName(pr int, head string) string {
	if len(head) > 12 {
		head = head[:12]
	}
	return fmt.Sprintf("merge-%d-%s", pr, head)
}

// recordMerge records a merge before it happens.
func (f *Factory) recordMerge(ctx context.Context, n, pr int, at pullRequest) error {
	b, err := json.Marshal(mergeRecordJSON{Head: at.head, Gate: at.gate, Lock: at.lock, ScopeMany: at.scopeMany})
	if err != nil {
		return err
	}
	return f.Repo.Record(ctx, n, mergeStepName(pr, at.head), string(b))
}

// mergeRecord reads the merge the factory recorded at a head, if it did.
func (f *Factory) mergeRecord(ctx context.Context, n, pr int, head string) (pullRequest, bool, error) {
	what, err := f.Repo.Recorded(ctx, n, mergeStepName(pr, head))
	if err != nil || what == "" {
		return pullRequest{}, false, err
	}
	var r mergeRecordJSON
	if err := json.Unmarshal([]byte(what), &r); err != nil {
		return pullRequest{}, false, fmt.Errorf("the record of merging #%d can't be read: %w", pr, err)
	}
	return pullRequest{head: r.Head, gate: r.Gate, lock: r.Lock, scopeMany: r.ScopeMany}, true, nil
}

// latest is the newest check run, and whether it has completed.
func latest(runs []github.CheckRun) (github.CheckRun, bool) {
	if len(runs) == 0 {
		return github.CheckRun{}, false
	}
	newest := runs[0]
	for _, r := range runs[1:] {
		if r.ID > newest.ID {
			newest = r
		}
	}
	return newest, newest.Status == "completed"
}

// note answers a command without changing anything.
func (f *Factory) note(ctx context.Context, t Thread, c Command, text string, closing ...string) error {
	if err := f.recovers(ctx, t.Issue.Number, "answer a command with a note", f.canPost(noting(), recovery.Note)); err != nil {
		return err
	}
	_, err := f.GitHub.PostComment(ctx, t.Issue.Number, noteComment(text, Marker{Kind: KindNote, ReplyTo: []int64{c.Comment}}, closing...))
	return f.effect(ctx, t.Issue.Number, "post a command's answer", err, nil)
}

// say posts a comment and sets the issue's status label. An empty label
// clears it.
func (f *Factory) say(ctx context.Context, issue int, body, label string) error {
	if err := f.mayAct(ctx); err != nil {
		return err
	}
	if _, err := f.GitHub.PostComment(ctx, issue, body); err != nil {
		return f.effect(ctx, issue, "post the issue's update", err, nil)
	}
	return f.status(ctx, issue, label)
}

func (f *Factory) status(ctx context.Context, issue int, label string) error {
	for _, l := range statusLabels {
		if l != label {
			if err := f.mayAct(ctx); err != nil {
				return err
			}
			if err := f.GitHub.RemoveLabel(ctx, issue, l); err != nil {
				return f.effect(ctx, issue, "remove label "+l, err, &label)
			}
		}
	}
	if label == "" {
		return nil
	}
	if err := f.mayAct(ctx); err != nil {
		return err
	}
	return f.effect(ctx, issue, "add label "+label, f.GitHub.AddLabels(ctx, issue, label), &label)
}

func hasVerb(cmds []Command, verb string) bool {
	for _, c := range cmds {
		if c.Verb == verb {
			return true
		}
	}
	return false
}

func (f *Factory) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

// safeStep takes one step on an issue, and turns a panic into an error,
// so that one issue's bug can't stop the factory's work on the others. It
// reads the issue again first, so a close since the poll listed it stops
// ordinary work and records the external outcome instead.
func (f *Factory) safeStep(ctx context.Context, n int) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the factory's step panicked: %v\n%s", r, debug.Stack())
		}
	}()
	ctx = f.inTenure(ctx)
	issue, err := f.GitHub.Issue(ctx, n)
	if err != nil {
		// Only a merge's unlisted placeholder can disappear. A 404 for an
		// issue the lists returned is a failed read, and must be retried.
		if next, ok := ctx.Value(pollStepKey{}).(*pollContinuation); ok && next.unlisted && errors.Is(err, github.ErrNotFound) {
			next.again, next.gone = false, true
		}
		return err
	}
	err = f.step(ctx, issue)
	if errors.Is(err, errDraining) {
		return nil // no run started; leave the request for the next watcher
	}
	if errors.Is(err, errIssueClosed) {
		// The queued operation released its slot without starting a run.
		// Read the thread afresh to record the close and clean up labels.
		issue, err = f.GitHub.Issue(ctx, n)
		if err != nil || issue.State != "closed" {
			return err
		}
		return f.step(ctx, issue)
	}
	return err
}

// doing records what the step on an issue starts doing, with no agent or
// run directory yet. A run it had already started has ended.
func (f *Factory) doing(issue int, what string) {
	f.running.mu.Lock()
	defer f.running.mu.Unlock()
	if s, ok := f.running.steps[issue]; ok && (s.Doing != what || s.Run != "" || s.Agent != "") {
		now := f.now()
		f.finished(s, now)
		s.Doing, s.Since, s.Agent, s.Run = what, now, "", ""
		f.running.steps[issue] = s
		f.tell()
	}
}

// checkStatus changes only the current build's waiting words. Its run, clock
// and completion belong to the build, including the time it spends queued.
func (f *Factory) checkStatus(issue int, run string, waiting bool) {
	f.running.mu.Lock()
	defer f.running.mu.Unlock()
	s, ok := f.running.steps[issue]
	if !ok || s.Run != run || (s.Doing != "building" && s.Doing != "waiting for checks") {
		return
	}
	what := "building"
	if waiting {
		what = "waiting for checks"
	}
	if s.Doing != what {
		s.Doing = what
		f.running.steps[issue] = s
		f.tell()
	}
}

// newRun reserves a fresh output directory for one invocation, including
// retries in the same second. Its basename has no timestamp semantics.
func (f *Factory) newRun(issue int, kind string) (string, error) {
	root := filepath.Join(f.Work, fmt.Sprintf("issue-%d", issue))
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(root, kind+"-")
}

// runsAgent records the coding agent and its already-created output
// directory before the run starts, so the watcher's status names both. A
// run the step had already started has ended.
func (f *Factory) runsAgent(issue int, agent, out string) {
	f.running.mu.Lock()
	defer f.running.mu.Unlock()
	run := filepath.Base(out)
	if s, ok := f.running.steps[issue]; ok && (s.Agent != agent || s.Run != run) {
		if s.Run != run {
			f.finished(s, f.now())
		}
		s.Agent, s.Run = agent, run
		f.running.steps[issue] = s
		f.tell()
	}
}

// tell lets Activity hear the steps running. Its caller holds their lock,
// so Activity hears one change at a time, in order.
func (f *Factory) tell() {
	if f.Activity != nil {
		f.Activity(f.running.list())
	}
}

func (f *Factory) logf(format string, args ...any) {
	if f.Log != nil {
		f.Log(format, args...)
	}
}
