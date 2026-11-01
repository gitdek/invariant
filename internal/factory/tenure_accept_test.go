package factory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
)

// A watcher's lease has tenures (D-0237): a step it started before it took
// the lease back from another watcher takes no effect, but a lost answer to
// its own push starts no tenure, and an agent run that outlives its tenure
// still has its result recorded. These are #357's second review's X1, X2
// and X3.

// lostAnswer pushes the lease, then once reports the push failed, as a git
// push whose answer is lost after the server applied it.
type lostAnswer struct {
	Repo
	armed *bool
}

func (r lostAnswer) PushLease(ctx context.Context, old string, rec LeaseRecord) (string, error) {
	pushed, err := r.Repo.PushLease(ctx, old, rec)
	if err == nil && *r.armed {
		*r.armed = false
		return "", errors.New("git push: the remote end hung up unexpectedly")
	}
	return pushed, err
}

// heldBuild holds a plan's build until the test lets it go.
type heldBuild struct {
	inner   PlanBuilder
	block   bool
	started chan struct{}
	release chan struct{}
}

func (g *heldBuild) Build(ctx context.Context, root string, n int, out string) (*plumbing.BuildResult, error) {
	if g.block {
		g.block = false
		close(g.started)
		<-g.release
	}
	return g.inner.Build(ctx, root, n, out)
}

// One watcher, and no other: a renewal whose push landed but whose answer
// was lost is the watcher's own lease, so the next renewal starts no tenure,
// and the build under way records its result and is published, not reported
// as stopped.
func TestARenewalWhoseAnswerIsLostKeepsTheTenure(t *testing.T) {
	ctx := context.Background()
	r, base := plumbingRig(t, "page/page.go")
	held := &heldBuild{inner: base, started: make(chan struct{}), release: make(chan struct{})}
	r.f.Plumbing = held
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	armed := false
	raw := r.f.Repo
	r.f.Holder, r.f.LeaseFor, r.f.Parallel = "watcher-a", 5*time.Minute, 2
	r.f.Now = func() time.Time { return now }
	r.f.GitHub, r.f.Repo = leasedGitHub{r.f.GitHub, r.f}, leasedRepo{lostAnswer{Repo: raw, armed: &armed}, r.f}
	if held, err := r.f.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	r.gh.open(1, "gitdek", "Explain retries", "Kind: plumbing\n\n/invariant solve")
	r.poll()
	r.f.Wait()
	proposal := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(proposal.Marker.Proposal.Hash, "sha256:")[:hashChars])
	held.block = true
	r.poll()
	<-held.started
	ratified := r.gh.last(1)
	if ratified.Marker.Kind != KindRatified {
		t.Fatalf("want the build under way: %+v", ratified.Marker)
	}
	tenure := r.f.lease.tenure
	// An ordinary renewal, past half the lease, whose answer is lost, then
	// the next poll's renewal.
	now = now.Add(3 * time.Minute)
	armed = true
	r.f.hold(ctx)
	now = now.Add(20 * time.Second)
	if held, err := r.f.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	if r.f.lease.tenure != tenure {
		t.Fatalf("the watcher's own renewal started a new tenure: %d, was %d", r.f.lease.tenure, tenure)
	}
	close(held.release)
	r.f.Wait()
	state, _, err := raw.Run(ctx, 1, buildStepName(ratified))
	if err != nil || state != RunDone {
		t.Fatalf("the build's run must record its result: state %d, %v", state, err)
	}
	r.poll()
	r.f.Wait()
	if last := r.gh.last(1); last.Marker.Kind != KindPR || last.Marker.Failure == FailStopped {
		t.Fatalf("the build must be published, not reported as stopped: %+v", last.Marker)
	}
}

// A watcher back in a build that another watcher's retry replaced, with the
// lease taken back in a new tenure, records its run's result on the run's
// own ref and publishes nothing, as SPEC says.
func TestAWatcherBackInAReplacedBuildRecordsItsRun(t *testing.T) {
	ctx := context.Background()
	r, base := plumbingRig(t, "page/page.go")
	held := &heldBuild{inner: base, started: make(chan struct{}), release: make(chan struct{})}
	r.f.Plumbing = held
	start := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	nowA, nowB := start, start
	a := r.f
	rawA := a.Repo
	a.Holder, a.LeaseFor, a.Parallel = "watcher-a", 5*time.Minute, 2
	a.Now = func() time.Time { return nowA }
	a.GitHub, a.Repo = leasedGitHub{r.gh, a}, leasedRepo{rawA, a}
	cloneB := rawA.(Clone)
	cloneB.Dir = filepath.Join(t.TempDir(), "clone-b")
	if err := cloneB.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	b := &Factory{Repository: a.Repository, Formalizer: a.Formalizer, Builder: a.Builder, Plumbing: base, Base: a.Base, Projects: a.Projects,
		Work: t.TempDir(), Check: a.Check, Self: a.Self, Log: t.Logf, Holder: "watcher-b", LeaseFor: 5 * time.Minute, Now: func() time.Time { return nowB }}
	b.GitHub, b.Repo = leasedGitHub{r.gh, b}, leasedRepo{cloneB, b}
	pollB := func() {
		t.Helper()
		if err := b.Poll(ctx); err != nil {
			t.Fatalf("b: %v", err)
		}
		b.Wait()
	}
	if held, err := a.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	r.gh.open(1, "gitdek", "Explain retries", "Kind: plumbing\n\n/invariant solve")
	r.poll()
	a.Wait()
	proposal := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(proposal.Marker.Proposal.Hash, "sha256:")[:hashChars])
	held.block = true
	r.poll()
	<-held.started
	ratified := r.gh.last(1)
	// a stalls past its lease; b takes it, says the build stopped, a writer
	// retries, and b builds the plan again and opens its pull request.
	nowA, nowB = start.Add(7*time.Minute), start.Add(7*time.Minute)
	if held, err := b.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	pollB()
	r.gh.say(1, "gitdek", "/invariant retry")
	pollB()
	pollB()
	prs, posts := len(r.gh.prs), len(r.gh.posts(1))
	// b stops; a takes the lease back, and its run finishes.
	nowA = start.Add(14 * time.Minute)
	if held, err := a.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	close(held.release)
	a.Wait()
	state, _, err := rawA.Run(ctx, 1, buildStepName(ratified))
	if len(r.gh.prs) != prs || len(r.gh.posts(1)) != posts {
		t.Errorf("the watcher back in the replaced build published something: pull requests %d to %d, posts %d to %d", prs, len(r.gh.prs), posts, len(r.gh.posts(1)))
	}
	if err != nil || state != RunDone {
		t.Errorf("the watcher back in the replaced build must record its run's result: state %d, %v", state, err)
	}
}

// A watcher that takes the lease back from another with a push whose answer
// is lost still starts a new tenure once it learns the push landed, since
// the other watcher held the lease in between.
func TestATakeoverWhoseAnswerIsLostStillStartsATenure(t *testing.T) {
	ctx := context.Background()
	origin, _ := gitRepos(t)
	start := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	nowA, nowB := start, start
	a, b := leaseFactory(t, origin, "watcher-a", &nowA), leaseFactory(t, origin, "watcher-b", &nowB)
	armed := false
	a.Repo = lostAnswer{Repo: a.Repo, armed: &armed}
	gh := a.GitHub.(*fakeGitHub)
	gh.open(1, "gitdek", "a buffer", "/invariant solve")
	g := leasedGitHub{a.GitHub, a}
	if held, err := a.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	step := a.inTenure(ctx)
	nowA, nowB = start.Add(7*time.Minute), start.Add(7*time.Minute)
	if held, err := b.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	nowA = start.Add(14 * time.Minute)
	armed = true
	a.hold(ctx)
	nowA = nowA.Add(20 * time.Second)
	if held, err := a.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	if _, err := g.PostComment(step, 1, "stale"); !errors.Is(err, errStaleTenure) {
		t.Fatalf("a step from before b's tenure = %v; want errStaleTenure", err)
	}
	if _, err := g.PostComment(a.inTenure(ctx), 1, "fresh"); err != nil {
		t.Fatalf("a step in the new tenure = %v", err)
	}
}

// A watcher whose agent run outlives its tenure, when another watcher held
// the lease in between and did nothing on the issue, records the run's
// result and publishes nothing then, and its next step publishes it, since
// no post answered or replaced the build.
func TestARunThatOutlivesItsTenureIsPublishedByTheNextStep(t *testing.T) {
	ctx := context.Background()
	r, base := plumbingRig(t, "page/page.go")
	held := &heldBuild{inner: base, started: make(chan struct{}), release: make(chan struct{})}
	r.f.Plumbing = held
	start := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	nowA, nowB := start, start
	a := r.f
	rawA := a.Repo
	a.Holder, a.LeaseFor, a.Parallel = "watcher-a", 5*time.Minute, 2
	a.Now = func() time.Time { return nowA }
	a.GitHub, a.Repo = leasedGitHub{r.gh, a}, leasedRepo{rawA, a}
	b := leaseFactory(t, r.origin, "watcher-b", &nowB)
	if held, err := a.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	r.gh.open(1, "gitdek", "Explain retries", "Kind: plumbing\n\n/invariant solve")
	r.poll()
	a.Wait()
	proposal := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(proposal.Marker.Proposal.Hash, "sha256:")[:hashChars])
	held.block = true
	r.poll()
	<-held.started
	ratified := r.gh.last(1)
	// a stalls past its lease, and b holds it for a while, taking no step.
	nowA, nowB = start.Add(7*time.Minute), start.Add(7*time.Minute)
	if held, err := b.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	nowA = start.Add(14 * time.Minute)
	if held, err := a.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	posts := len(r.gh.posts(1))
	close(held.release)
	a.Wait()
	if state, _, err := rawA.Run(ctx, 1, buildStepName(ratified)); err != nil || state != RunDone {
		t.Fatalf("the run must record its result: state %d, %v", state, err)
	}
	if len(r.gh.posts(1)) != posts || len(r.gh.prs) != 0 {
		t.Fatalf("the step from the earlier tenure published something: %d posts, was %d; %d pull requests", len(r.gh.posts(1)), posts, len(r.gh.prs))
	}
	r.poll()
	a.Wait()
	if last := r.gh.last(1); last.Marker.Kind != KindPR || len(r.gh.prs) != 1 {
		t.Fatalf("the next step must publish the build: %+v, %d pull requests", last.Marker, len(r.gh.prs))
	}
}

// lateLanding loses one push of the lease, which lands only once the
// watcher pushes the lease again, just before that push, as a push the
// server applied late.
type lateLanding struct {
	Repo
	s *lateState
}

type lateState struct {
	armed bool
	late  *latePush
}

type latePush struct {
	old string
	rec LeaseRecord
}

func (r lateLanding) PushLease(ctx context.Context, old string, rec LeaseRecord) (string, error) {
	if r.s.armed {
		r.s.armed = false
		r.s.late = &latePush{old, rec}
		return "", errors.New("git push: the remote end hung up unexpectedly")
	}
	if p := r.s.late; p != nil {
		r.s.late = nil
		if _, err := r.Repo.PushLease(ctx, p.old, p.rec); err != nil {
			return "", err
		}
	}
	return r.Repo.PushLease(ctx, old, rec)
}

// A push of the lease whose answer is lost, and which lands only once the
// watcher's next push is refused as moved, is still the watcher's own: the
// renewal after that starts no tenure (#357's review).
func TestAPushThatLandsLateIsStillTheWatchers(t *testing.T) {
	ctx := context.Background()
	origin, _ := gitRepos(t)
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	a := leaseFactory(t, origin, "watcher-a", &now)
	late := &lateState{}
	a.Repo = lateLanding{Repo: a.Repo, s: late}
	if held, err := a.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	tenure := a.lease.tenure
	// A renewal past half the lease, whose answer is lost, and which hasn't
	// landed when the next renewal reads the lease.
	now = now.Add(3 * time.Minute)
	late.armed = true
	a.hold(ctx)
	now = now.Add(20 * time.Second)
	if held, _ := a.hold(ctx); held {
		t.Fatal("a renewal refused as moved holds the lease")
	}
	now = now.Add(20 * time.Second)
	if held, err := a.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	if a.lease.tenure != tenure {
		t.Fatalf("a push that landed late started a new tenure: %d, was %d", a.lease.tenure, tenure)
	}
}

// logLines keeps what a watcher logs, as its steps log their errors.
type logLines struct {
	t     *testing.T
	mu    sync.Mutex
	lines []string
}

func (l *logLines) add(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	l.mu.Lock()
	l.lines = append(l.lines, line)
	l.mu.Unlock()
	l.t.Log(line)
}

func (l *logLines) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

// leasedWatcher has r's watcher hold the lease on a clock of its own,
// running up to two steps at once, and keeps what it logs.
func leasedWatcher(t *testing.T, r *rig) (*time.Time, *logLines) {
	t.Helper()
	now := new(time.Time)
	*now = time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	logs := &logLines{t: t}
	a := r.f
	a.Holder, a.LeaseFor, a.Parallel, a.Log = "watcher-a", 5*time.Minute, 2, logs.add
	a.Now = func() time.Time { return *now }
	a.GitHub, a.Repo = leasedGitHub{r.gh, a}, leasedRepo{a.Repo, a}
	if held, err := a.hold(context.Background()); err != nil || !held {
		t.Fatal(held, err)
	}
	return now, logs
}

// outliveTheTenure has another watcher hold the lease, taking no step,
// while r's watcher's build is held, then has r's watcher take the lease
// back in a new tenure and lets the build end. It returns how many posts
// issue 1 had before the build ended.
func outliveTheTenure(t *testing.T, r *rig, now *time.Time, release chan struct{}) int {
	t.Helper()
	ctx := context.Background()
	nowB := now.Add(7 * time.Minute)
	b := leaseFactory(t, r.origin, "watcher-b", &nowB)
	*now = nowB
	if held, err := b.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	*now = now.Add(7 * time.Minute)
	if held, err := r.f.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	posts := len(r.gh.posts(1))
	close(release)
	r.f.Wait()
	return posts
}

// startHeldPlanBuild has a writer solve issue 1 and ratify its plan, and
// returns the ratification, once the build it starts is held.
func startHeldPlanBuild(t *testing.T, r *rig, held *heldBuild) Post {
	t.Helper()
	r.gh.open(1, "gitdek", "Explain retries", "Kind: plumbing\n\n/invariant solve")
	r.poll()
	r.f.Wait()
	proposal := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(proposal.Marker.Proposal.Hash, "sha256:")[:hashChars])
	held.block = true
	r.poll()
	<-held.started
	return r.gh.last(1)
}

// endedCleanly checks that a build whose run outlived its tenure and gave
// no result ended its step with nothing posted and no effect refused, and
// that the next step says, once, that the build stopped, with said.
func endedCleanly(t *testing.T, r *rig, ratified Post, posts int, logs *logLines, said string) {
	t.Helper()
	if state, _, err := r.f.Repo.Run(context.Background(), 1, buildStepName(ratified)); err != nil || state != RunRecorded {
		t.Fatalf("the run must stay recorded with no result: state %d, %v", state, err)
	}
	if n := len(r.gh.posts(1)); n != posts {
		t.Fatalf("the step from the earlier tenure posted: %d posts, was %d", n, posts)
	}
	for _, line := range logs.all() {
		if strings.Contains(line, errStaleTenure.Error()) || strings.Contains(line, errNoStep.Error()) {
			t.Errorf("the step from the earlier tenure tried an effect: %s", line)
		}
	}
	r.poll()
	r.f.Wait()
	var stopped []Post
	for _, p := range r.gh.posts(1)[posts:] {
		if p.Marker.Kind == KindFailed && p.Marker.Failure == FailStopped {
			stopped = append(stopped, p)
		}
	}
	if len(stopped) != 1 || !strings.Contains(stopped[0].Comment.Body, said) {
		t.Fatalf("the next step must say once that the build stopped, with %q: %+v", said, stopped)
	}
}

// providerDown is a plan's build whose provider stayed unavailable (#261).
type providerDown struct {
	PlanBuilder
	why string
}

func (p providerDown) Build(ctx context.Context, root string, n int, out string) (*plumbing.BuildResult, error) {
	res, err := p.PlanBuilder.Build(ctx, root, n, out)
	if res != nil {
		res.ProviderError = p.why
	}
	return res, err
}

// noResult is a plan's build whose agent run ends without one.
type noResult struct{}

func (noResult) Build(context.Context, string, int, string) (*plumbing.BuildResult, error) {
	return nil, errors.New("the agent ended its run without a build")
}

// heldModeled holds a modeled project's build until the test lets it go.
type heldModeled struct {
	inner   Builder
	block   bool
	started chan struct{}
	release chan struct{}
}

func (g *heldModeled) Build(ctx context.Context, dir, out string, amend bool) (*synth.Result, error) {
	if g.block {
		g.block = false
		close(g.started)
		<-g.release
	}
	return g.inner.Build(ctx, dir, out, amend)
}

// A plan's build its provider stopped, in a run that outlived its tenure,
// ends its step once the watcher records why beside the run, posting
// nothing, and the next step says the build stopped, quoting that record
// (#357's review).
func TestAProviderStoppedRunThatOutlivesItsTenureEndsItsStep(t *testing.T) {
	const why = "codex stopped: we're currently experiencing high demand, which may cause temporary errors"
	r, base := plumbingRig(t, "page/page.go")
	held := &heldBuild{inner: providerDown{PlanBuilder: base, why: why}, started: make(chan struct{}), release: make(chan struct{})}
	r.f.Plumbing = held
	now, logs := leasedWatcher(t, r)
	ratified := startHeldPlanBuild(t, r, held)
	posts := outliveTheTenure(t, r, now, held.release)
	if what, err := r.f.Repo.Recorded(context.Background(), 1, unavailableStep(buildStepName(ratified))); err != nil || !strings.Contains(what, "high demand") {
		t.Fatalf("the record of why the build stopped is %q, %v", what, err)
	}
	endedCleanly(t, r, ratified, posts, logs, "high demand")
}

// A plan's build whose run gave no result, and outlived its tenure, ends
// its step posting nothing, and the next step says it stopped.
func TestARunWithNoResultThatOutlivesItsTenureEndsItsStep(t *testing.T) {
	r, _ := plumbingRig(t, "page/page.go")
	held := &heldBuild{inner: noResult{}, started: make(chan struct{}), release: make(chan struct{})}
	r.f.Plumbing = held
	now, logs := leasedWatcher(t, r)
	ratified := startHeldPlanBuild(t, r, held)
	posts := outliveTheTenure(t, r, now, held.release)
	if what, err := r.f.Repo.Recorded(context.Background(), 1, stoppedStep(buildStepName(ratified))); err != nil || !strings.Contains(what, "without a build") {
		t.Fatalf("the record of how the build stopped is %q, %v", what, err)
	}
	endedCleanly(t, r, ratified, posts, logs, "the agent ended its run without a build")
}

// noFinal is a modeled project's build whose agent run ends with what it
// spent but no code.
type noFinal struct{}

func (noFinal) Build(context.Context, string, string, bool) (*synth.Result, error) {
	return &synth.Result{Usage: synth.Usage{CostUSD: 0.42}}, errors.New("the agent ended its run without code")
}

// A modeled project's build whose run gave no result, with what it spent,
// and outlived its tenure, ends its step posting nothing, and the next step
// says it stopped with the run's own error and spend (#369's review).
func TestAProjectRunWithNoResultThatOutlivesItsTenureKeepsItsCause(t *testing.T) {
	r := newRig(t)
	r.form.forks = nil
	held := &heldModeled{inner: noFinal{}, started: make(chan struct{}), release: make(chan struct{})}
	r.f.Builder = held
	now, logs := leasedWatcher(t, r)
	r.gh.open(1, "gitdek", "Add a bounded buffer", "A buffer.\n\n/invariant solve")
	r.poll()
	r.f.Wait()
	proposal := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(proposal.Marker.Proposal.Hash, "sha256:"))
	held.block = true
	r.poll()
	<-held.started
	ratified := r.gh.last(1)
	posts := outliveTheTenure(t, r, now, held.release)
	endedCleanly(t, r, ratified, posts, logs, "the agent ended its run without code")
	if last := r.gh.last(1); last.Marker.Spend != 0.42 {
		t.Errorf("the post must count what the run spent: %+v", last.Marker)
	}
}

// As for a plan: a modeled project's build its provider stopped, in a run
// that outlived its tenure, ends its step once the watcher records why, and
// the next step says the build stopped, quoting it.
func TestAProviderStoppedProjectRunThatOutlivesItsTenureEndsItsStep(t *testing.T) {
	const last = "Selected model is at capacity. Please try a different model. (last attempt)"
	r := newRig(t)
	r.form.forks = nil
	held := &heldModeled{inner: providerFailedBuilder{fakeBuilder: r.build, err: errors.New("codex provider unavailable after 4 attempts: " + last)},
		started: make(chan struct{}), release: make(chan struct{})}
	r.f.Builder = held
	now, logs := leasedWatcher(t, r)
	r.gh.open(1, "gitdek", "Add a bounded buffer", "A buffer.\n\n/invariant solve")
	r.poll()
	r.f.Wait()
	proposal := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(proposal.Marker.Proposal.Hash, "sha256:"))
	held.block = true
	r.poll()
	<-held.started
	ratified := r.gh.last(1)
	posts := outliveTheTenure(t, r, now, held.release)
	endedCleanly(t, r, ratified, posts, logs, "at capacity")
}
