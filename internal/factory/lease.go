package factory

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

// The lease (D-0069): one watcher acts on a repository at a time. It lives
// in the ref refs/invariant/lease, as a commit whose message says who holds
// the lease and until when. Taking it or renewing it pushes a new commit in
// place of the one the watcher read, and the push names that commit, so if
// another watcher moved the ref first, the push fails. Git checks that on
// the server, under the ref's lock.
//
// The holder renews it on every poll, agent runs included, and checks that
// it still holds it just before each effect. It stops acting leaseSkew
// before its lease runs out, by its own clock, and other watchers wait until
// leaseSkew after, by theirs, so clocks that disagree by less than that
// can't let two act at once. Each of its running steps checks for itself, so
// losing the lease stops every one before its next effect, and each drops
// its agent run (D-0113). A watcher that takes the lease back after another
// held it starts a new tenure, and a step it started in an earlier one takes
// no effect, since what it read of the issue may be older than the other
// watcher's effects, but for recording what its agent run left; nor does an
// effect outside any step. A push of the lease whose answer was lost is the
// watcher's own once it reads it back, and starts a tenure only if it took
// the lease from elsewhere (D-0237). The proved cores see only whether the
// watcher holds the lease: the tenure check is here alone.

const leaseRef = "refs/invariant/lease"

// leaseSkew is how far apart two watchers' clocks may be.
const leaseSkew = time.Minute

// LeaseRecord is what a lease says.
type LeaseRecord struct {
	Holder string    `json:"holder"`
	Until  time.Time `json:"until"`
}

// ErrLeaseMoved means another watcher moved the lease between reading it and
// pushing a new one.
var ErrLeaseMoved = errors.New("another watcher moved the lease")

// errLeaseLost stops an effect when the watcher no longer holds the lease.
var errLeaseLost = errors.New("this watcher no longer holds the repository's lease")

// errStaleTenure stops an effect of a step this watcher started before it
// took the lease back from another watcher, or of none of its steps.
var errStaleTenure = errors.New("this watcher took the lease back after another watcher held it, so a step it started before then takes no effect")

// NewHolder names a watcher for its lease: random, so it says nothing about
// the machine it runs on.
func NewHolder() string {
	b := make([]byte, 6)
	rand.Read(b)
	return "watcher-" + hex.EncodeToString(b)
}

// Lease reads the repository's lease: the commit its ref points to, empty
// when there's none, and what that commit says.
func (c Clone) Lease(ctx context.Context) (sha string, rec LeaseRecord, err error) {
	defer c.lock()()
	return c.readLease(ctx)
}

// readLease is Lease, for a call that holds the clone's lock.
func (c Clone) readLease(ctx context.Context) (sha string, rec LeaseRecord, err error) {
	sha, err = c.remoteRef(ctx, leaseRef)
	if err != nil || sha == "" {
		return "", rec, err
	}
	msg, err := c.git(ctx, "log", "-1", "--format=%B", sha)
	if err != nil {
		return "", rec, err
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(msg)), &rec); err != nil {
		return sha, rec, fmt.Errorf("the lease at %s can't be read: %w", sha, err)
	}
	return sha, rec, nil
}

// PushLease puts rec in place of the lease at old, or where there's no lease
// when old is empty. It fails with ErrLeaseMoved if the ref isn't at old any
// more. A push git reports failed that its own read shows landed, as when
// its answer was lost, pushed the lease.
func (c Clone) PushLease(ctx context.Context, old string, rec LeaseRecord) (string, error) {
	defer c.lock()()
	msg, err := json.Marshal(rec)
	if err != nil {
		return "", err
	}
	commit, err := c.emptyCommit(ctx, string(msg))
	if err != nil {
		return "", err
	}
	if err := c.pushRef(ctx, leaseRef, old, commit); err != nil {
		now, _, readErr := c.readLease(ctx)
		switch {
		case readErr == nil && now == commit:
			return commit, nil
		case readErr == nil && now != old:
			return "", ErrLeaseMoved
		}
		return "", err
	}
	return commit, nil
}

// auth is the environment that lets git push as the factory's App, when it
// has one.
func (c Clone) auth(ctx context.Context) ([]string, error) {
	if c.Token == nil {
		return nil, nil
	}
	token, err := c.Token(ctx)
	if err != nil {
		return nil, err
	}
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.https://github.com/.extraheader", "GIT_CONFIG_VALUE_0=AUTHORIZATION: basic " + basic}, nil
}

// lease is a watcher's hold on its repository's lease.
type lease struct {
	mu      sync.Mutex
	sha     string    // the lease it last pushed
	until   time.Time // when its lease runs out, by its own clock; zero when it holds none
	waiting string    // the holder it last said it was waiting for
	// tenure counts each time it took the lease from somewhere other than
	// where it last left it, as after another watcher held it.
	tenure uint64
	// tried is a lease it pushed without learning whether the push landed,
	// and whether that push took the lease from somewhere else: a lease that
	// says what it tried is its own, from then.
	tried *triedLease
}

// triedLease is a push of the lease whose outcome a watcher never learned.
type triedLease struct {
	rec      LeaseRecord
	takeover bool
}

// tenureKey is the context key for the tenure a step started in.
type tenureKey struct{}

// inTenure is ctx for a step that starts now, in this watcher's tenure.
func (f *Factory) inTenure(ctx context.Context) context.Context {
	f.lease.mu.Lock()
	defer f.lease.mu.Unlock()
	return context.WithValue(ctx, tenureKey{}, f.lease.tenure)
}

// hold takes the lease, or renews it, unless another watcher holds it. It
// says whether this watcher holds it now.
func (f *Factory) hold(ctx context.Context) (bool, error) {
	f.lease.mu.Lock()
	defer f.lease.mu.Unlock()
	now := f.now()
	sha, rec, err := f.Repo.Lease(ctx)
	if err != nil {
		f.lease.until = time.Time{}
		return false, err
	}
	// A lease that says what this watcher last tried to push, when it never
	// learned whether the push landed, is the one it pushed: its own, and a
	// new tenure only if that push took the lease from elsewhere.
	if t := f.lease.tried; t != nil && sha != "" && rec.Holder == t.rec.Holder && rec.Until.Equal(t.rec.Until) {
		if t.takeover {
			f.lease.tenure++
		}
		f.lease.sha, f.lease.until, f.lease.waiting, f.lease.tried = sha, t.rec.Until, "", nil
	}
	// It renews only once half its lease has gone, to push less often.
	if sha != "" && sha == f.lease.sha && rec.Holder == f.Holder && f.lease.until.Sub(now) > f.LeaseFor/2 {
		return true, nil
	}
	if sha != "" && rec.Holder != f.Holder && now.Before(rec.Until.Add(leaseSkew)) {
		f.lease.until = time.Time{}
		if f.lease.waiting != rec.Holder {
			f.logf("waiting: %s holds the lease on %s until %s", rec.Holder, f.Repository, rec.Until.Format(time.RFC3339))
			f.lease.waiting = rec.Holder
		}
		return false, nil
	}
	next := LeaseRecord{Holder: f.Holder, Until: now.Add(f.LeaseFor)}
	pushed, err := f.Repo.PushLease(ctx, sha, next)
	if err != nil {
		f.lease.until = time.Time{}
		if errors.Is(err, ErrLeaseMoved) {
			f.lease.tried = nil
			return false, nil
		}
		f.lease.tried = &triedLease{rec: next, takeover: sha != f.lease.sha}
		return false, err
	}
	f.lease.tried = nil
	if rec.Holder != f.Holder || sha == "" {
		f.logf("holding the lease on %s as %s", f.Repository, f.Holder)
	}
	// The lease isn't where this watcher left it, so another watcher may
	// have acted on its issues since: a new tenure begins.
	if sha != f.lease.sha {
		f.lease.tenure++
	}
	f.lease.sha, f.lease.until, f.lease.waiting = pushed, next.Until, ""
	return true, nil
}

// holds says whether this watcher may act: it holds the lease, with time to
// spare by its own clock. Without a lease, as in a one-off run, it may.
func (f *Factory) holds() bool {
	if f.LeaseFor == 0 {
		return true
	}
	f.lease.mu.Lock()
	defer f.lease.mu.Unlock()
	return f.now().Before(f.lease.until.Add(-leaseSkew))
}

// mayAct says why the step ctx belongs to may not act, if it may not: this
// watcher doesn't hold the lease, or holds it in a later tenure than the
// step started in, or ctx belongs to no step at all.
func (f *Factory) mayAct(ctx context.Context) error {
	if f.LeaseFor == 0 {
		return nil
	}
	f.lease.mu.Lock()
	defer f.lease.mu.Unlock()
	if !f.now().Before(f.lease.until.Add(-leaseSkew)) {
		return errLeaseLost
	}
	if tenure, ok := ctx.Value(tenureKey{}).(uint64); !ok || tenure != f.lease.tenure {
		return errStaleTenure
	}
	return nil
}

// errResumed is an agent run that outlived its step's tenure: the watcher
// recorded its result on the run's own ref, and its next step publishes it.
var errResumed = errors.New("this watcher took the lease back while the agent ran, so it recorded the run's result and leaves the rest to its next step")

// resumed is ctx for recording an agent run's result once the run ends: as
// it was, or, when this watcher took the lease back in a new tenure while
// the run went, in that tenure, with resumed set. A run's ref only moves
// from the commit the watcher recorded there, which no other watcher
// writes, so its result can be recorded in any tenure, unlike any other
// effect of a step from an earlier one (D-0237).
func (f *Factory) resumed(ctx context.Context) (context.Context, bool) {
	if f.holdsFor(ctx) {
		return ctx, false
	}
	return f.inTenure(ctx), true
}

// holdsFor says whether the step ctx belongs to may act.
func (f *Factory) holdsFor(ctx context.Context) bool {
	return f.mayAct(ctx) == nil
}

// keepLease renews the lease every poll while ctx lasts, so it holds through
// agent runs, which take longer than a lease lasts.
func (f *Factory) keepLease(ctx context.Context, every time.Duration) {
	for {
		if _, err := f.hold(ctx); err != nil && ctx.Err() == nil {
			f.logf("lease: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// leasedGitHub checks the lease before each effect on GitHub.
type leasedGitHub struct {
	GitHub
	f *Factory
}

func (g leasedGitHub) PostComment(ctx context.Context, issue int, body string) (c github.Comment, err error) {
	if err := g.f.mayAct(ctx); err != nil {
		return c, err
	}
	return g.GitHub.PostComment(ctx, issue, body)
}

func (g leasedGitHub) CreateIssue(ctx context.Context, is github.NewIssue) (i github.Issue, err error) {
	if err := g.f.mayAct(ctx); err != nil {
		return i, err
	}
	return g.GitHub.CreateIssue(ctx, is)
}

func (g leasedGitHub) AddLabels(ctx context.Context, issue int, labels ...string) error {
	if err := g.f.mayAct(ctx); err != nil {
		return err
	}
	return g.GitHub.AddLabels(ctx, issue, labels...)
}

func (g leasedGitHub) RemoveLabel(ctx context.Context, issue int, label string) error {
	if err := g.f.mayAct(ctx); err != nil {
		return err
	}
	return g.GitHub.RemoveLabel(ctx, issue, label)
}

func (g leasedGitHub) CreatePullRequest(ctx context.Context, pr github.NewPullRequest) (p github.PullRequest, err error) {
	if err := g.f.mayAct(ctx); err != nil {
		return p, err
	}
	return g.GitHub.CreatePullRequest(ctx, pr)
}

func (g leasedGitHub) Merge(ctx context.Context, n int, sha, method string) (string, error) {
	if err := g.f.mayAct(ctx); err != nil {
		return "", err
	}
	return g.GitHub.Merge(ctx, n, sha, method)
}

func (g leasedGitHub) MarkReady(ctx context.Context, n int) error {
	if err := g.f.mayAct(ctx); err != nil {
		return err
	}
	return g.GitHub.MarkReady(ctx, n)
}

func (g leasedGitHub) ClosePullRequest(ctx context.Context, n int) (github.PullRequest, error) {
	if err := g.f.mayAct(ctx); err != nil {
		return github.PullRequest{}, err
	}
	return g.GitHub.ClosePullRequest(ctx, n)
}

func (g leasedGitHub) DeleteBranch(ctx context.Context, branch string) error {
	if err := g.f.mayAct(ctx); err != nil {
		return err
	}
	return g.GitHub.DeleteBranch(ctx, branch)
}

// leasedRepo checks the lease before each push.
type leasedRepo struct {
	Repo
	f *Factory
}

func (r leasedRepo) Push(ctx context.Context, worktree, branch string) error {
	if err := r.f.mayAct(ctx); err != nil {
		return err
	}
	return r.Repo.Push(ctx, worktree, branch)
}

func (r leasedRepo) PushCommit(ctx context.Context, commit, branch string) error {
	if err := r.f.mayAct(ctx); err != nil {
		return err
	}
	return r.Repo.PushCommit(ctx, commit, branch)
}

func (r leasedRepo) Record(ctx context.Context, issue int, step, what string) error {
	if err := r.f.mayAct(ctx); err != nil {
		return err
	}
	return r.Repo.Record(ctx, issue, step, what)
}

func (r leasedRepo) Finish(ctx context.Context, issue int, step, result string) error {
	if err := r.f.mayAct(ctx); err != nil {
		return err
	}
	return r.Repo.Finish(ctx, issue, step, result)
}
