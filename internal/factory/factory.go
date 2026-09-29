package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"time"

	"github.com/gitdek/invariant/factory/protocol/protocol"
	"github.com/gitdek/invariant/factory/recovery/recovery"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/scope"
	"github.com/gitdek/invariant/internal/synth"
)

// GitHub is what the factory does on GitHub. github.Client is the real one.
type GitHub interface {
	OpenIssues(ctx context.Context) ([]github.Issue, error)
	// Issue reads one issue as it is now, open or closed.
	Issue(ctx context.Context, n int) (github.Issue, error)
	Comments(ctx context.Context, issue int) ([]github.Comment, error)
	Events(ctx context.Context, issue int) ([]github.Event, error)
	Permission(ctx context.Context, login string) (string, error)
	PostComment(ctx context.Context, issue int, body string) (github.Comment, error)
	EnsureLabel(ctx context.Context, name, color, description string) error
	AddLabels(ctx context.Context, issue int, labels ...string) error
	RemoveLabel(ctx context.Context, issue int, label string) error
	CreatePullRequest(ctx context.Context, pr github.NewPullRequest) (github.PullRequest, error)
	PullRequest(ctx context.Context, n int) (github.PullRequest, error)
	// OpenPullRequest finds the open pull request from a branch, if there is one.
	OpenPullRequest(ctx context.Context, branch string) (github.PullRequest, bool, error)
	CheckRuns(ctx context.Context, sha, name string) ([]github.CheckRun, error)
	// Job reads the job behind a check run, to tell a gate that failed from
	// one GitHub never started.
	Job(ctx context.Context, id int64) (github.Job, error)
	Merge(ctx context.Context, n int, sha, method string) (string, error)
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
	Commit(ctx context.Context, worktree, dir, message string) (string, error)
	Push(ctx context.Context, worktree, branch string) error
	RevParse(ctx context.Context, ref string) (string, error)
	Show(ctx context.Context, ref, file string) ([]byte, error)
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
	// Holder names this watcher, and LeaseFor is how long its lease on the
	// repository lasts (D-0069). A watcher acts only while it holds the
	// lease. Zero means no lease, as in a one-off run.
	Holder   string
	LeaseFor time.Duration
	// Parallel is how many issues the watcher takes steps on at once, one
	// step per issue (D-0113). 0 or 1 takes one step at a time.
	Parallel int

	// mu guards the cache of who can write, which steps share: writers is
	// what GitHub last said of each login, and asked is whom the latest poll
	// has asked. Each poll asks again, and a step that runs across polls
	// keeps the answers it read.
	mu      sync.Mutex
	writers map[string]bool
	asked   map[string]bool
	running running
	lease   lease
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

// Prepare creates the labels the factory uses.
func (f *Factory) Prepare(ctx context.Context) error {
	for _, l := range labels {
		if err := f.GitHub.EnsureLabel(ctx, l.name, l.color, l.description); err != nil {
			return fmt.Errorf("label %s: %w", l.name, err)
		}
	}
	return nil
}

// Watch polls until ctx ends, and then waits for its steps to end. With a
// lease, it polls only while it holds the lease, and checks it before each
// effect. The first time it may act, before it starts any step, it clears
// the clone's worktrees: the clone is the watcher's own, so any worktree
// there is left over from a watcher that stopped mid-build. It clears them
// only then, since after that its steps run across polls, each in a
// worktree of its own (D-0113). A one-off run doesn't clear them, since it
// may share the clone with a running watcher.
func (f *Factory) Watch(ctx context.Context, every time.Duration) error {
	if f.LeaseFor > 0 {
		f.GitHub, f.Repo = leasedGitHub{f.GitHub, f}, leasedRepo{f.Repo, f}
		if _, err := f.hold(ctx); err != nil {
			f.logf("lease: %v", err)
		}
		go func() {
			select {
			case <-ctx.Done():
			case <-time.After(every):
				f.keepLease(ctx, every)
			}
		}()
	}
	cleared := false
	clearOnce := func() error {
		if cleared {
			return nil
		}
		err := f.Repo.ClearWorktrees(ctx)
		cleared = err == nil
		return err
	}
	for {
		if !f.holds() {
			// Another watcher holds the lease. Wait for it to run out.
		} else if err := clearOnce(); err != nil {
			f.logf("worktrees: %v", err)
		} else if err := f.Poll(ctx); err != nil {
			f.logf("poll: %v", err)
		}
		select {
		case <-ctx.Done():
			f.Wait()
			return ctx.Err()
		case <-time.After(every):
		}
	}
}

// Poll starts a step on every open issue that has none running, in order of
// number, and each step reads its issue again first. With Parallel above 1,
// up to that many run at once (D-0113): Poll waits for a free slot whenever
// that many are running, and returns without waiting for its steps, so
// they run across polls, and each logs its error as it ends. Otherwise Poll
// takes one step at a time, and returns their errors once they've ended.
func (f *Factory) Poll(ctx context.Context) error {
	f.mu.Lock()
	if f.writers == nil {
		f.writers = map[string]bool{}
	}
	f.asked = map[string]bool{}
	f.mu.Unlock()
	issues, err := f.GitHub.OpenIssues(ctx)
	if err != nil {
		return err
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	most := max(f.Parallel, 1)
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
		case most == 1:
			err := f.safeStep(ctx, n)
			f.end(n)
			if err != nil {
				errs = append(errs, fmt.Errorf("#%d: %w", n, err))
			}
		default:
			go func() {
				defer f.end(n)
				if err := f.safeStep(ctx, n); err != nil {
					f.logf("#%d: %v", n, err)
				}
			}()
		}
	}
	// Activity hears the end of every poll, as the watcher's heartbeat.
	f.running.mu.Lock()
	f.tell()
	f.running.mu.Unlock()
	return errors.Join(errs...)
}

// Step is a step the watcher is running on an issue: what it's doing,
// formalizing, answering, ratifying or building, or "" before it starts
// one of those, and since when.
type Step struct {
	Issue int
	Doing string
	Since time.Time
}

// running is the steps a watcher is running, one per issue (D-0113).
type running struct {
	mu    sync.Mutex
	steps map[int]Step
	ended chan struct{} // closed, and made anew, each time a step ends
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

// Wait waits until no step is running.
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
// started one, and stops waiting when ctx ends.
func (f *Factory) start(ctx context.Context, n, most int) (bool, error) {
	for {
		f.running.mu.Lock()
		if f.running.steps == nil {
			f.running.steps, f.running.ended = map[int]Step{}, make(chan struct{})
		}
		_, busy := f.running.steps[n]
		free := !busy && len(f.running.steps) < most
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
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
}

// end ends the step on issue n.
func (f *Factory) end(n int) {
	f.running.mu.Lock()
	defer f.running.mu.Unlock()
	delete(f.running.steps, n)
	f.tell()
	close(f.running.ended)
	f.running.ended = make(chan struct{})
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
		if m, ok := DecodeMarker(c.Body); ok && (f.Self == "" || c.User.Login == f.Self) {
			t.Posts = append(t.Posts, Post{Comment: c, Marker: m})
		}
	}
	// A new issue is the factory's if a writer opened it with /invariant solve,
	// or a writer gave it the invariant label.
	if len(t.Posts) == 0 {
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
	state, started := t.State()
	switch state.Marker.Kind {
	case KindPR:
		return f.watch(ctx, t, state)
	case KindRatified:
		// A build that stopped partway, when the factory stopped. Pick it up.
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
				return f.watch(ctx, t, state)
			}
		}
	}
	pending := t.Pending()
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
	case Revise:
		switch {
		case kind == KindForks, kind == KindProposal, kind == KindStuck, kind == KindUnsupported, kind == KindClosed, stoppedBuild(state):
			return f.formalize(ctx, t, c, state.Marker.Answers, state.Marker.Proposal, []int64{c.Comment})
		case kind == KindFailed && state.Marker.PR != 0:
			n := state.Marker.PR
			return f.note(ctx, t, c, fmt.Sprintf("#%d is still open, so I can't draft again yet. To change what must be true, close #%d and comment `/invariant revise` again. "+
				"To build the same statements again, once the cause is fixed, comment `/invariant retry`.", n, n))
		case kind == KindPR:
			n := state.Marker.PR
			return f.note(ctx, t, c, fmt.Sprintf("#%d is still open, so I can't draft again yet. To change what must be true, close #%d and comment `/invariant revise` again.", n, n))
		}
		return f.note(ctx, t, c, "There's no draft to revise right now.")
	case Choose:
		if kind != KindForks {
			return f.note(ctx, t, c, "There's no open question to answer right now.")
		}
		return f.choose(ctx, t, state, pending)
	case Retry:
		if kind != KindFailed {
			return f.note(ctx, t, c, "There's nothing that failed to try again right now.")
		}
		if state.Marker.PR == 0 {
			// The build stopped before it made a pull request. Build the
			// ratified proposal again, and start counting stops afresh.
			m := state.Marker.carried()
			m.Kind, m.ReplyTo, m.Failure = KindRatified, []int64{c.Comment}, ""
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
		// Watch the pull request again. Nothing merges unless CI's gate passes
		// on its current head, it stays in scope, and its lock is still the
		// ratified proposal.
		m := state.Marker.carried()
		m.Kind, m.ReplyTo, m.Failure = KindPR, []int64{c.Comment}, ""
		from, to := f.retryStep(t, c)
		if err := f.allowed(ctx, t.Issue.Number, fmt.Sprintf("look at #%d again", m.PR), from, to); err != nil {
			return err
		}
		if err := f.recovers(ctx, t.Issue.Number, "answer the retry", f.canPost(noting(), recovery.Note)); err != nil {
			return err
		}
		return f.say(ctx, t.Issue.Number, post("pull request", fmt.Sprintf("Watching #%d again. I'll merge it once CI's `invariant/gate` passes on its current head.", m.PR), m), LabelPR)
	case Ratify:
		if kind != KindProposal {
			return f.note(ctx, t, c, "There's no proposal waiting for ratification right now.")
		}
		p := state.Marker.Proposal
		if !matches(c.Args, p.Hash) {
			return f.note(ctx, t, c, fmt.Sprintf("That doesn't name the current proposal, so I haven't ratified anything. "+
				"To ratify it, comment `/invariant ratify %s`.", strings.TrimPrefix(p.Hash, "sha256:")[:hashChars]))
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
// answered, it drafts again with the answers.
func (f *Factory) choose(ctx context.Context, t Thread, state Post, pending []Command) error {
	f.doing(t.Issue.Number, "answering")
	forks := state.Marker.Forks
	chosen := map[string]formalize.Answer{}
	var replyTo []int64
	var last Command
	for _, c := range pending {
		if c.Verb != Choose {
			continue
		}
		last = c
		fork, option, problem := findChoice(forks, c.Args)
		if problem != "" {
			return f.note(ctx, t, c, problem)
		}
		chosen[strings.ToUpper(fork.ID)] = formalize.Answer{Fork: fork.ID, Question: fork.Question, Option: option.ID, Says: option.Says, By: c.By, Comment: c.URL}
		replyTo = append(replyTo, c.Comment)
	}
	for _, fork := range forks {
		if _, ok := chosen[strings.ToUpper(fork.ID)]; !ok {
			return nil // wait for the rest
		}
	}
	answers := append([]formalize.Answer(nil), state.Marker.Answers...)
	for _, fork := range forks {
		answers = append(answers, chosen[strings.ToUpper(fork.ID)])
	}
	return f.formalize(ctx, t, last, answers, nil, replyTo)
}

func findChoice(forks []formalize.Fork, args []string) (formalize.Fork, formalize.Option, string) {
	if len(args) != 2 {
		return formalize.Fork{}, formalize.Option{}, "To answer a question, comment `/invariant choose` with its number and an option, like `/invariant choose F1 A`."
	}
	for _, fork := range forks {
		if strings.EqualFold(fork.ID, args[0]) {
			if o, ok := fork.Option(args[1]); ok {
				return fork, o, ""
			}
			return fork, formalize.Option{}, fmt.Sprintf("%s has no option %s.", fork.ID, args[1])
		}
	}
	return formalize.Fork{}, formalize.Option{}, fmt.Sprintf("There's no open question %s.", args[0])
}

// formalize drafts statements for the issue and posts what came of it:
// forks to decide, a proposal to ratify, or why neither. cause is the
// command that asked for the draft.
func (f *Factory) formalize(ctx context.Context, t Thread, cause Command, answers []formalize.Answer, previous *formalize.Proposal, replyTo []int64) error {
	n := t.Issue.Number
	// base is the project's lock on the base branch, which a draft amends.
	var base string
	stuck := func(problem string) error {
		m := Marker{Kind: KindStuck, ReplyTo: replyTo}
		return f.drafted(ctx, t, cause, base, m, stuckComment(problem, m), LabelHumanReview)
	}
	f.logf("#%d: formalizing", n)
	f.doing(n, "formalizing")
	out := filepath.Join(f.Work, fmt.Sprintf("issue-%d", n), "formalize-"+f.now().Format("20060102-150405"))
	req := f.request(t, answers, previous)
	// A plumbing issue gets a plan instead of statements, checked against
	// the base branch as it is (D-0105). Only on Invariant's own repository,
	// for now: anywhere else, it gets an answer and is left for a person,
	// with nothing drafted (#103).
	if kindLine(t.Issue.Body) == "plumbing" {
		if !f.plansPlumbing() {
			m := Marker{Kind: KindUnsupported, ReplyTo: replyTo}
			return f.drafted(ctx, t, cause, base, m, plumbingElsewhereComment(m), LabelHumanReview)
		}
		if projectLine(t.Issue.Body) != "" || len(codeLines(t.Issue.Body)) > 0 {
			return stuck("A plumbing issue's plan names the files it changes, so it names no project or code to check. Take out the Project: and Code: lines, or the Kind: plumbing line.")
		}
		root, err := f.planBase(ctx)
		if err != nil {
			return err
		}
		defer os.RemoveAll(root)
		req.Plumbing, req.Language = root, "go"
	}
	// A Project: line names the project the issue changes, or where a new
	// one goes (D-0045).
	var newDir string
	if dir := projectLine(t.Issue.Body); dir != "" {
		if problem := badDir(dir); problem != "" {
			return stuck("The issue names the project `" + dir + "`, but " + problem + ".")
		}
		cur, err := f.current(ctx, dir)
		if err != nil {
			return err
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
	if paths := codeLines(t.Issue.Body); len(paths) > 0 {
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
	// The draft's agent run is recorded before it starts, where every
	// watcher sees it (D-0069). One recorded and never finished stopped
	// partway, and only a writer's command starts another.
	res, err := f.draft(ctx, n, draftStepName(cause), req, out)
	if err != nil {
		return err
	}
	if res == nil {
		if err := f.recovers(ctx, n, "say the draft stopped", f.canReportStopped(drafting(RunRecorded), recovery.Solve)); err != nil {
			return err
		}
		return stuck("The draft's agent run stopped partway, when the factory stopped. Comment `/invariant revise` to draft again.")
	}
	if err := f.recovers(ctx, n, "post the draft", f.canPost(drafting(RunDone), recovery.Solve)); err != nil {
		return err
	}
	if res.Proposal != nil && newDir != "" {
		res.Proposal.Dir = newDir
	}
	if res.Proposal != nil {
		answers = revise(t, answers, res.Proposal.Revised)
	}
	m := Marker{ReplyTo: replyTo, Answers: answers, Spend: res.Usage.CostUSD}
	switch p := res.Proposal; {
	case res.Problem != "":
		m.Kind, m.Proposal = KindStuck, p
		return f.drafted(ctx, t, cause, base, m, stuckComment(res.Problem, m), LabelHumanReview)
	case p.Unsupported != "":
		m.Kind = KindUnsupported
		return f.drafted(ctx, t, cause, base, m, unsupportedComment(p.Unsupported, m), "")
	case len(p.Forks) > 0:
		m.Kind, m.Forks, m.Proposal = KindForks, p.Forks, p
		return f.drafted(ctx, t, cause, base, m, forksComment(p.Forks, m), LabelAsking)
	case p.Plan != nil:
		m.Kind, m.Proposal = KindProposal, p
		return f.drafted(ctx, t, cause, base, m, planComment(p, m), LabelProposal)
	case p.Target != nil:
		m.Kind, m.Proposal = KindProposal, p
		return f.drafted(ctx, t, cause, base, m, amendmentComment(p, res.Report, res.Changes, m), LabelProposal)
	default:
		m.Kind, m.Proposal = KindProposal, p
		return f.drafted(ctx, t, cause, base, m, proposalComment(p, res.Report, m), LabelProposal)
	}
}

// draftStepName names a draft's record after the command that asked for it.
func draftStepName(cause Command) string {
	if cause.Comment == 0 {
		return "draft-issue" // the /invariant solve that opened the issue
	}
	return fmt.Sprintf("draft-%d", cause.Comment)
}

// draft returns what the draft's agent run made of the request: running it,
// when nothing was recorded, or reading what a finished run left, whichever
// watcher ran it. It returns nil for a run recorded and never finished.
func (f *Factory) draft(ctx context.Context, n int, step string, req formalize.Request, out string) (*formalize.Result, error) {
	state, result, err := f.Repo.Run(ctx, n, step)
	switch {
	case err != nil:
		return nil, err
	case state == RunRecorded:
		return nil, nil
	case state == RunDone:
		var res formalize.Result
		return &res, f.loadResult(ctx, result, &res)
	}
	if err := f.recovers(ctx, n, "start a draft's agent run", f.canStartRun(drafting(RunNone), recovery.Solve)); err != nil {
		return nil, err
	}
	if err := f.Repo.Record(ctx, n, step, fmt.Sprintf("a draft for #%d", n)); err != nil {
		return nil, err
	}
	res, runErr := f.Formalizer.Formalize(ctx, req, out)
	// A watcher that lost the lease during the run drops it. The holder will
	// find it recorded, and say it stopped.
	if !f.holds() {
		return nil, errLeaseLost
	}
	switch {
	case runErr != nil:
		spent := synth.Usage{}
		if res != nil {
			spent = res.Usage
		}
		res = &formalize.Result{Problem: "the formalizer couldn't run: " + runErr.Error(), Usage: spent}
	case res.Proposal == nil && res.Problem == "":
		res.Problem = "the formalizer left no draft"
	}
	saved, err := f.saveResult(ctx, res, fmt.Sprintf("invariant: the result of a draft for #%d", n), "")
	if err != nil {
		return nil, err
	}
	if err := f.recovers(ctx, n, "record the draft's result", f.canFinishRun(drafting(RunRecorded), recovery.Solve)); err != nil {
		return nil, err
	}
	return res, f.Repo.Finish(ctx, n, step, saved)
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

// badDir says what's wrong with a project directory an issue names.
func badDir(dir string) string {
	switch {
	case path.IsAbs(dir) || path.Clean(dir) != dir || dir == "." || strings.HasPrefix(dir, "../"):
		return "that isn't a clean path inside the repository"
	case dir == ".github" || strings.HasPrefix(dir, ".github/"):
		return "the factory never touches CI configuration"
	case strings.ContainsAny(dir, " \t"):
		return "a project's directory can't contain spaces"
	}
	return ""
}

// current reads the project in dir as it stands on the base branch, or
// returns nil when there's no project there.
func (f *Factory) current(ctx context.Context, dir string) (*formalize.Current, error) {
	if err := f.Repo.Fetch(ctx); err != nil {
		return nil, err
	}
	ref := "origin/" + f.Base
	b, err := f.Repo.Show(ctx, ref, dir+"/.invariant/invariant.json")
	if err != nil {
		return nil, nil
	}
	c := &formalize.Current{Dir: dir}
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
// branch, then builds it.
func (f *Factory) ratify(ctx context.Context, t Thread, state Post, c Command) error {
	f.doing(t.Issue.Number, "ratifying")
	posted, err := f.commitRatification(ctx, t, state, c)
	if errors.Is(err, errStale) {
		return f.note(ctx, t, c, "The project changed after I drafted this amendment, so I haven't ratified anything. Comment `/invariant revise` and I'll draft it again from the project as it is now.")
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
	f.logf("#%d: ratified by @%s", n, c.By)
	if err := f.Repo.Fetch(ctx); err != nil {
		return Post{}, err
	}
	if p.Plan != nil {
		return f.commitPlan(ctx, t, state, c)
	}
	if p.Target != nil {
		cur, err := f.current(ctx, p.Target.Dir)
		if err != nil {
			return Post{}, err
		}
		if cur == nil || project.ProposalHash(cur.Lock.Bounds, cur.Lock.Statements) != p.Target.Amends {
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
	m := Marker{Kind: KindRatified, ReplyTo: []int64{c.Comment}, Answers: state.Marker.Answers, Proposal: p, Project: dir, Branch: branch, Hash: p.Hash}
	body := ratifiedComment(c.By, dir, branch, p.Hash, len(p.Statements), m)
	posted, err := f.GitHub.PostComment(ctx, n, body)
	if err != nil {
		return Post{}, err
	}
	return Post{Comment: posted, Marker: m}, f.status(ctx, n, LabelBuilding)
}

// branchFor picks the branch and project directory for a ratified proposal,
// and the ref to start from. If an earlier attempt already pushed this
// proposal's branch, it picks that up where it stopped; a branch left from a
// different proposal gets a numbered successor.
func (f *Factory) branchFor(ctx context.Context, issue int, p *formalize.Proposal) (branch, dir, from string, err error) {
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
		if sc.Project == "" {
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
	next := Marker{Kind: KindFailed, Answers: m.Answers, Proposal: m.Proposal, Project: m.Project, Branch: m.Branch, Hash: m.Hash}
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
		return failed(FailStopped, buildFailedComment(nil, nil, errors.New("the build's agent run stopped partway, when the factory stopped"), withFailure(next, FailStopped)))
	case RunNone:
		// Each build costs an agent run, so after two stop, a writer decides
		// whether to try again.
		if stops >= maxStops {
			return failed(FailLimit, buildFailedComment(nil, nil, fmt.Errorf("%d builds stopped before they made a pull request, so I haven't started another. Comment `/invariant retry` to try again", stops), withFailure(next, FailLimit)))
		}
		if isPlan(m.Project) {
			built, res, runErr, err := f.runPlanBuild(ctx, t, ratified, step)
			if err != nil {
				return err
			}
			if built == "" {
				if err := f.recovers(ctx, n, "say the build stopped", f.canReportStopped(building(RunRecorded, false, false), recovery.Build)); err != nil {
					return err
				}
				return failed(FailStopped, planFailedComment(nil, res, runErr, withFailure(next, FailStopped)))
			}
			return f.publish(ctx, t, ratified, built, next, stops)
		}
		built, res, runErr, err := f.runBuild(ctx, t, ratified, step)
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
			return failed(FailStopped, buildFailedComment(nil, res, runErr, withFailure(next, FailStopped)))
		}
		result = built
	}
	return f.publish(ctx, t, ratified, result, next, stops)
}

// buildStepName names a build's record after the post that started it: a
// ratification, or the answer to a writer's retry.
func buildStepName(ratified Post) string {
	return fmt.Sprintf("build-%d", ratified.Comment.ID)
}

// builtResult is what a build's run leaves in its record, on top of the
// code it wrote: what synthesis reported, and what went wrong, if anything.
type builtResult struct {
	Result   *synth.Result         `json:"result"`
	Plumbing *plumbing.BuildResult `json:"plumbing,omitempty"` // a plan's build, in place of a project's (D-0105)
	Error    string                `json:"error,omitempty"`
}

// runBuild records the build's agent run, runs it, and commits the code it
// wrote on top of the branch, without pushing it. Then the run's record
// moves to its result: a commit on top of that code, holding what the run
// reported. runBuild returns that commit, or "" when the run gave no result.
func (f *Factory) runBuild(ctx context.Context, t Thread, ratified Post, step string) (string, *synth.Result, error, error) {
	n, m := t.Issue.Number, ratified.Marker
	if err := f.Repo.Fetch(ctx); err != nil {
		return "", nil, nil, err
	}
	wt, err := f.Repo.Worktree(ctx, m.Branch, "origin/"+m.Branch)
	if err != nil {
		return "", nil, nil, err
	}
	defer f.Repo.RemoveWorktree(ctx, wt)
	root := filepath.Join(wt, filepath.FromSlash(m.Project))
	out := filepath.Join(f.Work, fmt.Sprintf("issue-%d", n), "build-"+f.now().Format("20060102-150405"))
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", nil, nil, err
	}
	// An agent run is an effect: only the lease's holder records and starts
	// one, and it's recorded before it starts.
	if err := f.recovers(ctx, n, "start the build's agent run", f.canStartRun(building(RunNone, false, false), recovery.Build)); err != nil {
		return "", nil, nil, err
	}
	if err := f.Repo.Record(ctx, n, step, fmt.Sprintf("the build of %s for #%d", m.Project, n)); err != nil {
		return "", nil, nil, err
	}
	amend := m.Proposal != nil && m.Proposal.Target != nil
	res, runErr := f.Builder.Build(ctx, root, out, amend)
	// A watcher that lost the lease during the run drops it. The holder will
	// find it recorded, and say it stopped.
	if !f.holds() {
		return "", nil, nil, errLeaseLost
	}
	if res == nil || res.Final == nil {
		return "", res, runErr, nil
	}
	// The agent's files replace the project's, so a file it removed is gone.
	if err := removeOwned(root); err != nil {
		return "", nil, nil, err
	}
	dir := res.Dir
	if dir == "" {
		dir = filepath.Join(out, "result")
	}
	if err := copyResult(dir, root); err != nil {
		return "", nil, nil, err
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
		return "", nil, nil, err
	}
	// Anyone can fetch the record, so the result it holds names its directory
	// by where it is under the work directory: the watcher's own path names
	// its user.
	kept := *res
	kept.Dir = f.workRelative(res.Dir)
	saved := builtResult{Result: &kept}
	if runErr != nil {
		saved.Error = runErr.Error()
	}
	result, err := f.saveResult(ctx, saved, fmt.Sprintf("invariant: the result of the build for #%d", n), code)
	if err != nil {
		return "", nil, nil, err
	}
	if err := f.recovers(ctx, n, "record the build's result", f.canFinishRun(building(RunRecorded, false, false), recovery.Build)); err != nil {
		return "", nil, nil, err
	}
	if err := f.Repo.Finish(ctx, n, step, result); err != nil {
		return "", nil, nil, err
	}
	return result, res, runErr, nil
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
// unless the branch has it, opens the pull request, unless one is open from
// the branch, and says how the build went. Whichever watcher ran the build,
// its record holds everything this needs.
func (f *Factory) publish(ctx context.Context, t Thread, ratified Post, result string, next Marker, stops int) error {
	n, m := t.Issue.Number, ratified.Marker
	var built builtResult
	if err := f.loadResult(ctx, result, &built); err != nil {
		return err
	}
	if built.Plumbing != nil {
		return f.publishPlan(ctx, t, ratified, result, built, next, stops)
	}
	res := built.Result
	if res == nil || res.Final == nil {
		return fmt.Errorf("the build's record at %s holds no result", result)
	}
	var runErr error
	if built.Error != "" {
		runErr = errors.New(built.Error)
	}
	next.Spend, next.GateRuns = res.Spend(), len(res.GateRuns)
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
	pr, open, err := f.GitHub.OpenPullRequest(ctx, m.Branch)
	if err != nil {
		return err
	}
	if !open {
		if err := f.recovers(ctx, n, "open a pull request", f.canOpenPullRequest(building(RunDone, true, false))); err != nil {
			return err
		}
		if pr, err = f.GitHub.CreatePullRequest(ctx, github.NewPullRequest{
			Title: t.Issue.Title, Head: m.Branch, Base: f.Base, Draft: !res.Final.Passed,
			Body: pullRequestBody(t, m, res, m.Proposal),
		}); err != nil {
			return err
		}
	}
	next.PR = pr.Number
	if err := f.recovers(ctx, n, "say how the build went", f.canPost(building(RunDone, true, true), recovery.Build)); err != nil {
		return err
	}
	if !res.Final.Passed {
		if err := f.GitHub.AddLabels(ctx, pr.Number, LabelHumanReview); err != nil {
			return err
		}
		next.Failure = FailGate
		return f.say(ctx, n, buildFailedComment(&pr, res, runErr, next), LabelHumanReview)
	}
	next.Kind = KindPR
	return f.say(ctx, n, prComment(pr, res.Final, next), LabelPR)
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
		// A merge the factory recorded, at this head, is the factory's: it
		// stopped after merging and before it said so (D-0069).
		if at, ok, err := f.mergeRecord(ctx, n, pr.Number, pr.Head.SHA); err != nil {
			return err
		} else if ok && at.head == pr.Head.SHA {
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
	sc, err := f.Repo.Scope(ctx, "origin/"+f.Base, head, n)
	if err != nil {
		return err
	}
	problems := sc.Problems
	if sc.Project != m.Project {
		problems = append(problems, fmt.Sprintf("it changes %q, not the ratified project %q", sc.Project, m.Project))
	}
	b, lockErr := f.Repo.Show(ctx, head, lockFileOf(m.Project))
	if lockErr != nil {
		problems = append(problems, "its lock can't be read: "+lockErr.Error())
	} else if lockAt(m.Project, b, nil) != m.Hash {
		problems = append(problems, "its lock isn't the proposal that was ratified")
	}
	// A change to the trusted base is outside what the factory may merge,
	// so the protocol sees it as out of scope (D-0105).
	at := pullRequest{head: pr.Head.SHA, gate: gateOf(run, done), lock: lockAt(m.Project, b, lockErr),
		scopeMany: len(sc.Problems) > 0 || sc.Project != m.Project || len(sc.Trusted) > 0}
	if len(problems) > 0 {
		from, to := prStep(t, state, at, protocol.KindFailed, protocol.Nobody)
		to.failure = FailUnmergeable
		if err := f.allowed(ctx, n, fmt.Sprintf("say #%d can't merge", pr.Number), from, to); err != nil {
			return err
		}
		if err := f.recovers(ctx, n, "say it can't merge", f.canPost(merging(false, false), recovery.Merge)); err != nil {
			return err
		}
		next.Failure = FailUnmergeable
		return f.say(ctx, n, scopeFailedComment(pr, problems, next), LabelHumanReview)
	}
	// Only a person merges a change to the trusted base (D-0105).
	if sc.Plumbing && len(sc.Trusted) > 0 {
		return f.waitForPerson(ctx, t, state, pr, at, sc.Trusted, next)
	}
	// The protocol has the last word on merging.
	from, to := prStep(t, state, at, protocol.KindMerged, protocol.ByFactory)
	if err := f.allowed(ctx, n, fmt.Sprintf("merge #%d", pr.Number), from, to); err != nil {
		return err
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
	sha, err := f.GitHub.Merge(ctx, pr.Number, pr.Head.SHA, "merge")
	if err != nil {
		return err
	}
	f.logf("#%d: merged #%d as %s", n, pr.Number, sha)
	if err := f.recovers(ctx, n, "say it merged", f.canPost(merging(true, true), recovery.Merge)); err != nil {
		return err
	}
	return f.saidMerged(ctx, t, pr, run, sha, next)
}

// saidMerged deletes a merged pull request's branch and says the factory
// merged it, with the issue's numbers.
func (f *Factory) saidMerged(ctx context.Context, t Thread, pr github.PullRequest, run github.CheckRun, sha string, next Marker) error {
	n := t.Issue.Number
	if err := f.GitHub.DeleteBranch(ctx, next.Branch); err != nil {
		f.logf("#%d: deleting %s: %v", n, next.Branch, err)
	}
	next.Kind = KindMerged
	numbers := NumbersOf(t, f.now())
	next.Numbers = &numbers
	return f.say(ctx, n, mergedComment(pr, run, sha, &next), LabelMerged)
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
func (f *Factory) note(ctx context.Context, t Thread, c Command, text string) error {
	if err := f.recovers(ctx, t.Issue.Number, "answer a command with a note", f.canPost(noting(), recovery.Note)); err != nil {
		return err
	}
	_, err := f.GitHub.PostComment(ctx, t.Issue.Number, noteComment(text, Marker{Kind: KindNote, ReplyTo: []int64{c.Comment}}))
	return err
}

// say posts a comment and sets the issue's status label. An empty label
// clears it.
func (f *Factory) say(ctx context.Context, issue int, body, label string) error {
	if _, err := f.GitHub.PostComment(ctx, issue, body); err != nil {
		return err
	}
	return f.status(ctx, issue, label)
}

func (f *Factory) status(ctx context.Context, issue int, label string) error {
	for _, l := range statusLabels {
		if l != label {
			if err := f.GitHub.RemoveLabel(ctx, issue, l); err != nil {
				return err
			}
		}
	}
	if label == "" {
		return nil
	}
	return f.GitHub.AddLabels(ctx, issue, label)
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
// reads the issue again first, and takes no step on one closed since the
// poll listed it.
func (f *Factory) safeStep(ctx context.Context, n int) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the factory's step panicked: %v\n%s", r, debug.Stack())
		}
	}()
	issue, err := f.GitHub.Issue(ctx, n)
	if err != nil {
		return err
	}
	if issue.State == "closed" {
		return nil
	}
	return f.step(ctx, issue)
}

// doing records what the step on an issue starts doing.
func (f *Factory) doing(issue int, what string) {
	f.running.mu.Lock()
	defer f.running.mu.Unlock()
	if s, ok := f.running.steps[issue]; ok && s.Doing != what {
		s.Doing, s.Since = what, f.now()
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
