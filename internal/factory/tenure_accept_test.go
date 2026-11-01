package factory

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/plumbing"
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
