package factory

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/synth"
)

// These tests run several issues' steps at once (D-0113): one step per
// issue, and up to Parallel of them.

// parRig is a rig whose watcher runs steps at once. The fakes aren't safe to
// use from several steps, so every call into them, and every read the test
// makes of them, takes one lock. Each issue's agent runs can be held open
// until the test lets them go.
type parRig struct {
	*rig
	ctx   context.Context
	mu    *sync.Mutex
	hub   *syncHub
	held  *heldRuns
	clone Clone
}

// newParRig is a rig whose watcher runs up to parallel steps at once, with
// no forks to decide.
func newParRig(t *testing.T, parallel int) *parRig {
	t.Helper()
	r := newRig(t)
	r.form.forks = nil
	mu := &sync.Mutex{}
	ctx, stop := context.WithCancel(context.Background())
	p := &parRig{rig: r, ctx: ctx, mu: mu, hub: &syncHub{GitHub: r.gh, mu: mu, gh: r.gh}, clone: r.f.Repo.(Clone)}
	p.held = &heldRuns{mu: mu, hold: make(map[int]chan struct{}), runs: map[string]int{}}
	r.f.GitHub = p.hub
	r.f.Formalizer = heldFormalizer{r.form, p.held}
	r.f.Builder = heldBuilder{r.build, p.held}
	r.f.Parallel = parallel
	t.Cleanup(func() {
		p.held.letAll()
		r.f.Wait()
	})
	t.Cleanup(stop)
	return p
}

// syncHub is the fake GitHub, one call at a time. It counts the polls by the
// lists of open issues they read.
type syncHub struct {
	GitHub
	mu    *sync.Mutex
	gh    *fakeGitHub
	lists int
}

func (s *syncHub) OpenIssues(ctx context.Context) ([]github.Issue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lists++
	return s.gh.OpenIssues(ctx)
}

// Issue reads one issue as it is now, open or closed.
func (s *syncHub) Issue(_ context.Context, n int) (github.Issue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if is, ok := s.gh.issues[n]; ok {
		return *is, nil
	}
	return github.Issue{}, github.ErrNotFound
}

func (s *syncHub) Comments(ctx context.Context, n int) ([]github.Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gh.Comments(ctx, n)
}

func (s *syncHub) Events(ctx context.Context, n int) ([]github.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gh.Events(ctx, n)
}

func (s *syncHub) Permission(ctx context.Context, login string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gh.Permission(ctx, login)
}

func (s *syncHub) PostComment(ctx context.Context, n int, body string) (github.Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gh.PostComment(ctx, n, body)
}

func (s *syncHub) EnsureLabel(ctx context.Context, name, color, description string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gh.EnsureLabel(ctx, name, color, description)
}

func (s *syncHub) AddLabels(ctx context.Context, n int, labels ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gh.AddLabels(ctx, n, labels...)
}

func (s *syncHub) RemoveLabel(ctx context.Context, n int, label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gh.RemoveLabel(ctx, n, label)
}

func (s *syncHub) CreatePullRequest(ctx context.Context, pr github.NewPullRequest) (github.PullRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gh.CreatePullRequest(ctx, pr)
}

func (s *syncHub) PullRequest(ctx context.Context, n int) (github.PullRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gh.PullRequest(ctx, n)
}

func (s *syncHub) OpenPullRequest(ctx context.Context, branch string) (github.PullRequest, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gh.OpenPullRequest(ctx, branch)
}

func (s *syncHub) CheckRuns(ctx context.Context, sha, name string) ([]github.CheckRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gh.CheckRuns(ctx, sha, name)
}

func (s *syncHub) Job(ctx context.Context, id int64) (github.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gh.Job(ctx, id)
}

func (s *syncHub) Merge(ctx context.Context, n int, sha, method string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gh.Merge(ctx, n, sha, method)
}

func (s *syncHub) DeleteBranch(ctx context.Context, branch string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gh.DeleteBranch(ctx, branch)
}

// heldRuns holds agent runs open, by issue, until the test lets them go. It
// counts the runs each issue started, and the most going at once.
type heldRuns struct {
	mu   *sync.Mutex
	hold map[int]chan struct{}
	runs map[string]int
	now  int
	most int
}

// holdOpen holds issue n's agent runs open until let(n).
func (h *heldRuns) holdOpen(n int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hold[n] = make(chan struct{})
}

// let lets issue n's held runs go on.
func (h *heldRuns) let(n int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.hold[n]; ok {
		close(c)
		delete(h.hold, n)
	}
}

// letAll lets every held run go on.
func (h *heldRuns) letAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for n, c := range h.hold {
		close(c)
		delete(h.hold, n)
	}
}

// started is how many runs of kind, draft or build, issue n started.
func (h *heldRuns) started(kind string, n int) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runs[fmt.Sprintf("%s %d", kind, n)]
}

// going is how many runs are going now, and the most that went at once.
func (h *heldRuns) going() (now, most int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now, h.most
}

// run is one agent run of kind for issue n. It waits while n is held open,
// or until ctx ends, then does what the fake does, under the fakes' lock.
func (h *heldRuns) run(ctx context.Context, kind string, n int, fake func()) error {
	h.mu.Lock()
	gate := h.hold[n]
	h.runs[fmt.Sprintf("%s %d", kind, n)]++
	h.now++
	h.most = max(h.most, h.now)
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.now--
		h.mu.Unlock()
	}()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	fake()
	return nil
}

// heldFormalizer drafts through heldRuns. Each draft names its issue, so a
// draft that lands in another issue's record shows.
type heldFormalizer struct {
	Formalizer
	h *heldRuns
}

func (f heldFormalizer) Formalize(ctx context.Context, req formalize.Request, out string) (*formalize.Result, error) {
	var res *formalize.Result
	var err error
	held := f.h.run(ctx, "draft", req.Issue, func() {
		res, err = f.Formalizer.Formalize(ctx, req, out)
	})
	if held != nil {
		return nil, held
	}
	if err == nil && res != nil && res.Proposal != nil {
		res.Proposal.Name = fmt.Sprintf("%s for #%d", res.Proposal.Name, req.Issue)
	}
	return res, err
}

// heldBuilder builds through heldRuns.
type heldBuilder struct {
	Builder
	h *heldRuns
}

func (b heldBuilder) Build(ctx context.Context, dir, out string, amend bool) (*synth.Result, error) {
	var res *synth.Result
	var err error
	held := b.h.run(ctx, "build", issueIn(out), func() {
		res, err = b.Builder.Build(ctx, dir, out, amend)
	})
	if held != nil {
		return nil, held
	}
	return res, err
}

// issueIn is the issue a step's directory is for: the watcher's work
// directory, then issue-N.
func issueIn(dir string) int {
	for _, part := range strings.Split(filepath.ToSlash(dir), "/") {
		if rest, ok := strings.CutPrefix(part, "issue-"); ok {
			if n, err := strconv.Atoi(rest); err == nil {
				return n
			}
		}
	}
	return 0
}

// open opens issue n as @gitdek, asking the factory to solve it.
func (p *parRig) open(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gh.open(n, "gitdek", fmt.Sprintf("Add buffer %d", n), "A buffer.\n\n/invariant solve")
}

// say comments on issue n as @gitdek.
func (p *parRig) say(n int, body string) github.Comment {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.gh.say(n, "gitdek", body)
}

// shut closes issue n, as a person does.
func (p *parRig) shut(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gh.issues[n].State = "closed"
}

// ci records a gate that passed on pull request pr's head.
func (p *parRig) ci(pr int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gh.ci(pr, "success")
}

// posts is issue n's factory posts.
func (p *parRig) posts(n int) []Post {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.gh.posts(n)
}

// last is issue n's latest factory post, or none.
func (p *parRig) last(n int) Post {
	ps := p.posts(n)
	if len(ps) == 0 {
		return Post{}
	}
	return ps[len(ps)-1]
}

// labels is issue n's labels.
func (p *parRig) labels(n int) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.gh.labelsOf(n)
}

// polls is how many polls have read the open issues.
func (p *parRig) polls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hub.lists
}

// expect is rig.expect, under the fakes' lock.
func (p *parRig) expect(n int, kind string, labels ...string) Post {
	p.t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rig.expect(n, kind, labels...)
}

// pollAsync polls in the background.
func (p *parRig) pollAsync() chan error {
	return pollOf(p.ctx, p.f)
}

// settle polls, and waits for the poll and every step it started to end.
func (p *parRig) settle() {
	p.t.Helper()
	returned(p.t, p.pollAsync(), "a poll")
	p.f.Wait()
}

// proposed opens issue n and takes it to a proposal, and returns the
// command that ratifies it.
func (p *parRig) proposed(n int) string {
	p.t.Helper()
	p.open(n)
	p.settle()
	last := p.last(n)
	if last.Marker.Kind != KindProposal {
		p.t.Fatalf("#%d's last post is %q; want a proposal", n, last.Marker.Kind)
	}
	return "/invariant ratify " + strings.TrimPrefix(last.Marker.Proposal.Hash, "sha256:")[:hashChars]
}

// pollOf polls f in the background, and returns where the poll's error goes
// once it returns.
func pollOf(ctx context.Context, f *Factory) chan error {
	done := make(chan error, 1)
	go func() { done <- f.Poll(ctx) }()
	return done
}

// soon waits up to a minute for ok, and fails the test if it never holds.
func soon(t *testing.T, what string, ok func() bool) {
	t.Helper()
	end := time.Now().Add(time.Minute)
	for !ok() {
		if time.Now().After(end) {
			t.Fatalf("waited a minute for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// returned waits up to a minute for a poll or a watch to return, and fails
// the test if it doesn't. What it returns is only logged: a step's error is
// the step's to say.
func returned(t *testing.T, done chan error, what string) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Logf("%s returned %v", what, err)
		}
	case <-time.After(time.Minute):
		t.Fatalf("%s hadn't returned after a minute", what)
	}
}

// While one issue's build is held open, the same poll answers another
// issue's command: it starts a step on each, and the build holds up nothing
// but its own issue.
func TestAnotherIssueIsAnsweredDuringABuild(t *testing.T) {
	p := newParRig(t, 3)
	ratify := p.proposed(1)
	p.held.holdOpen(1)
	p.say(1, ratify)
	p.open(2)
	before := p.polls()
	done := p.pollAsync()
	soon(t, "#1's build to start", func() bool { return p.held.started("build", 1) == 1 })
	soon(t, "#2's proposal, while #1's build is held open", func() bool { return p.last(2).Marker.Kind == KindProposal })
	if got := p.polls() - before; got != 1 {
		t.Fatalf("%d polls ran; want #2 answered within the one poll", got)
	}
	if kind := p.last(1).Marker.Kind; kind != KindRatified {
		t.Fatalf("#1's last post is %q while its build is held open; want the ratification", kind)
	}
	p.held.let(1)
	returned(t, done, "the poll")
	p.f.Wait()
	p.expect(1, KindPR, LabelPR)
	p.expect(2, KindProposal, LabelProposal)
}

// A step never starts twice on one issue, however many polls pass while it
// runs: polls return while #1's build is held open, and start nothing on #1.
func TestAStepNeverStartsTwiceOnOneIssue(t *testing.T) {
	p := newParRig(t, 3)
	ratify := p.proposed(1)
	p.held.holdOpen(1)
	p.say(1, ratify)
	returned(t, p.pollAsync(), "the poll that starts #1's build")
	soon(t, "#1's build to start", func() bool { return p.held.started("build", 1) == 1 })
	posted := len(p.posts(1))
	for i := 0; i < 5; i++ {
		returned(t, p.pollAsync(), "a poll while #1's build is held open")
	}
	if got := len(p.posts(1)); got != posted {
		t.Fatalf("#1 got %d posts from polls while its build's step ran; want none", got-posted)
	}
	p.held.let(1)
	p.f.Wait()
	p.settle()
	p.expect(1, KindPR, LabelPR)
	if got := p.held.started("build", 1); got != 1 {
		t.Errorf("%d agent runs built #1; want one", got)
	}
	if got := len(p.posts(1)); got != posted+1 {
		t.Errorf("#1 got %d posts once its build ended; want one, its pull request", got-posted)
	}
}

// Losing the lease while two steps run stops both before their next
// effect, and drops both agent runs, as one is dropped now: neither issue
// hears anything more, and neither run's result is recorded.
func TestLosingTheLeaseStopsEveryStep(t *testing.T) {
	p := newParRig(t, 3)
	start := time.Date(2026, 9, 29, 9, 0, 0, 0, time.UTC)
	p.f.Holder = "watcher-a"
	p.f.LeaseFor = 5 * time.Minute
	p.f.Now = func() time.Time { return start }
	if held, err := p.f.hold(p.ctx); err != nil || !held {
		t.Fatalf("hold = %v, %v; want the free lease", held, err)
	}
	p.f.GitHub, p.f.Repo = leasedGitHub{p.f.GitHub, p.f}, leasedRepo{p.f.Repo, p.f}
	ratify := p.proposed(1)
	p.held.holdOpen(1)
	p.held.holdOpen(2)
	p.say(1, ratify)
	p.open(2)
	done := p.pollAsync()
	soon(t, "#1's build and #2's draft to start", func() bool {
		return p.held.started("build", 1) == 1 && p.held.started("draft", 2) == 1
	})
	ratified := p.last(1)
	if ratified.Marker.Kind != KindRatified {
		t.Fatalf("#1's last post is %q while it builds; want the ratification", ratified.Marker.Kind)
	}
	posts1, posts2 := len(p.posts(1)), len(p.posts(2))
	labels1, labels2 := p.labels(1), p.labels(2)

	// Another watcher takes the lease, once it has run out by that watcher's
	// clock, and this one sees that it did.
	later := start.Add(10 * time.Minute)
	other := leaseFactory(t, p.origin, "watcher-b", &later)
	if held, err := other.hold(p.ctx); err != nil || !held {
		t.Fatalf("the other watcher's hold = %v, %v; want the lease that ran out", held, err)
	}
	if held, _ := p.f.hold(p.ctx); held || p.f.holds() {
		t.Fatal("this watcher still holds the lease the other took")
	}
	p.held.letAll()
	returned(t, done, "the poll")
	p.f.Wait()

	if len(p.posts(1)) != posts1 || len(p.posts(2)) != posts2 {
		t.Errorf("an issue heard more after the lease was lost: #1 %d posts, then %d; #2 %d, then %d", posts1, len(p.posts(1)), posts2, len(p.posts(2)))
	}
	if !sameSet(p.labels(1), labels1) || !sameSet(p.labels(2), labels2) {
		t.Errorf("labels changed after the lease was lost: #1 %v, then %v; #2 %v, then %v", labels1, p.labels(1), labels2, p.labels(2))
	}
	if state, _, err := other.Repo.Run(p.ctx, 1, buildStepName(ratified)); err != nil || state != RunRecorded {
		t.Errorf("#1's build is %v, %v; want it recorded, with no result", state, err)
	}
	if state, _, err := other.Repo.Run(p.ctx, 2, "draft-issue"); err != nil || state != RunRecorded {
		t.Errorf("#2's draft is %v, %v; want it recorded, with no result", state, err)
	}
	if code := git(t, p.origin, "log", "--format=%s", "main..invariant/issue-1-bounded-buffer"); code != "Ratify the statements for #1" {
		t.Errorf("#1's branch holds more than its ratification after the lease was lost:\n%s", code)
	}
}

// No more than Parallel steps run at once. A poll that finds that many
// running waits for one to end before it starts the next, and reads each
// issue again just before its step, so it skips one closed while it waited.
func TestNoMoreStepsRunThanParallel(t *testing.T) {
	p := newParRig(t, 2)
	for n := 1; n <= 4; n++ {
		p.held.holdOpen(n)
		p.open(n)
	}
	done := p.pollAsync()
	soon(t, "#1's and #2's drafts to start", func() bool {
		return p.held.started("draft", 1) == 1 && p.held.started("draft", 2) == 1
	})
	time.Sleep(200 * time.Millisecond)
	if now, _ := p.held.going(); now != 2 {
		t.Fatalf("%d drafts are going with two steps at most; want two", now)
	}
	p.shut(3)
	p.held.let(1)
	soon(t, "#4's draft, once #1's step ended", func() bool { return p.held.started("draft", 4) == 1 })
	p.held.let(2)
	p.held.let(4)
	returned(t, done, "the poll")
	p.f.Wait()
	for _, n := range []int{1, 2, 4} {
		p.expect(n, KindProposal, LabelProposal)
	}
	if got := p.held.started("draft", 3); got != 0 || len(p.posts(3)) != 0 {
		t.Errorf("#3 was closed before its step, and still got %d drafts and %d posts", got, len(p.posts(3)))
	}
	if _, most := p.held.going(); most != 2 {
		t.Errorf("%d drafts went at once; want two at most", most)
	}
}

// closing is a builder during whose build a person closes an issue.
type closing struct {
	Builder
	during func()
}

func (b closing) Build(ctx context.Context, dir, out string, amend bool) (*synth.Result, error) {
	b.during()
	return b.Builder.Build(ctx, dir, out, amend)
}

// With one step at a time, as the watcher took them before (-parallel 1), a
// poll still reads each issue again just before its step: an issue closed
// while an earlier issue built gets no step from the same poll.
func TestAnIssueClosedDuringAPollGetsNoStep(t *testing.T) {
	r := newRig(t)
	r.form.forks = nil
	r.f.Parallel = 1
	r.gh.open(1, "gitdek", "Add a bounded buffer", "A buffer.\n\n/invariant solve")
	r.poll()
	p := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Marker.Proposal.Hash, "sha256:")[:hashChars])
	r.gh.open(2, "gitdek", "Add a queue", "A queue.\n\n/invariant solve")
	r.f.Builder = closing{r.f.Builder, func() { r.gh.issues[2].State = "closed" }}
	r.poll()
	r.expect(1, KindPR, LabelPR)
	if posts := r.gh.posts(2); len(posts) != 0 {
		t.Fatalf("#2 was closed during #1's build, and the same poll still posted on it:\n%s", posts[0].Comment.Body)
	}
	for _, req := range r.form.requests {
		if req.Issue == 2 {
			t.Fatal("#2 was closed during #1's build, and the same poll still drafted it")
		}
	}
}

// Polls go on while a step runs, and none of them clears the worktree the
// step builds in: the watcher clears its clone's worktrees once, when it
// takes the lease, before it starts any step.
func TestPollsKeepARunningStepsWorktree(t *testing.T) {
	p := newParRig(t, 3)
	ratify := p.proposed(1)
	p.held.holdOpen(1)
	p.say(1, ratify)
	ctx, stop := context.WithTimeout(p.ctx, 2*time.Minute)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- p.f.Watch(ctx, 20*time.Millisecond) }()
	soon(t, "#1's build to start", func() bool { return p.held.started("build", 1) == 1 })
	polls := p.polls()
	soon(t, "five polls while #1's build is held open", func() bool { return p.polls() >= polls+5 })
	p.held.let(1)
	soon(t, "#1's pull request", func() bool { return p.last(1).Marker.Kind == KindPR })
	stop()
	returned(t, done, "the watch")
	p.f.Wait()
	p.expect(1, KindPR, LabelPR)
	if got := p.held.started("build", 1); got != 1 {
		t.Errorf("%d agent runs built #1; want one", got)
	}
}

// The watcher lists every step it's running, with its issue, what it's
// doing and since when, for its status file. A step that ended leaves it.
func TestStepsListsEveryRunningStep(t *testing.T) {
	p := newParRig(t, 3)
	ratify := p.proposed(1)
	p.held.holdOpen(1)
	p.held.holdOpen(2)
	p.say(1, ratify)
	p.open(2)
	before := time.Now()
	done := p.pollAsync()
	soon(t, "#1's build and #2's draft to start", func() bool {
		return p.held.started("build", 1) == 1 && p.held.started("draft", 2) == 1
	})
	doing := map[int]string{}
	for _, s := range p.f.Steps() {
		if _, twice := doing[s.Issue]; twice {
			t.Errorf("#%d is listed twice", s.Issue)
		}
		doing[s.Issue] = s.Doing
		if s.Since.Before(before.Add(-time.Minute)) || s.Since.After(time.Now().Add(time.Minute)) {
			t.Errorf("#%d has been %s since %v; want since the poll", s.Issue, s.Doing, s.Since)
		}
	}
	if len(doing) != 2 || doing[1] != "building" || doing[2] != "formalizing" {
		t.Errorf("steps = %v; want #1 building and #2 formalizing", doing)
	}
	p.held.letAll()
	returned(t, done, "the poll")
	p.f.Wait()
	if steps := p.f.Steps(); len(steps) != 0 {
		t.Errorf("steps = %+v once every step ended; want none", steps)
	}
}

// Two drafts that end at the same moment each record their own result,
// where any watcher finds it: the steps share the clone, and nothing of one
// lands in the other's record.
func TestTwoDraftsAtOnceKeepTheirOwnRecords(t *testing.T) {
	p := newParRig(t, 3)
	for n := 1; n <= 2; n++ {
		p.held.holdOpen(n)
		p.open(n)
	}
	done := p.pollAsync()
	soon(t, "both drafts to start", func() bool {
		return p.held.started("draft", 1) == 1 && p.held.started("draft", 2) == 1
	})
	p.held.letAll()
	returned(t, done, "the poll")
	p.f.Wait()
	for n := 1; n <= 2; n++ {
		p.expect(n, KindProposal, LabelProposal)
		state, sha, err := p.clone.Run(p.ctx, n, "draft-issue")
		if err != nil || state != RunDone {
			t.Fatalf("#%d's draft is recorded as %v, %v; want it done", n, state, err)
		}
		var res formalize.Result
		if err := json.Unmarshal([]byte(git(t, p.origin, "show", sha+":result.json")), &res); err != nil {
			t.Fatal(err)
		}
		name := "no proposal"
		if res.Proposal != nil {
			name = res.Proposal.Name
		}
		if want := fmt.Sprintf("bounded buffer for #%d", n); name != want {
			t.Errorf("#%d's record holds the draft %q; want %q", n, name, want)
		}
	}
}
