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
// can't let two act at once.

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
	env, err := c.auth(ctx)
	if err != nil {
		return "", rec, err
	}
	out, err := runEnv(ctx, c.Dir, env, "git", "ls-remote", "origin", leaseRef)
	if err != nil {
		return "", rec, err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", rec, nil
	}
	sha = fields[0]
	// A watcher already has the lease it pushed, so it fetches only
	// another's.
	if _, err := c.git(ctx, "cat-file", "-e", sha+"^{commit}"); err != nil {
		if _, err := runEnv(ctx, c.Dir, env, "git", "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "origin", "+"+leaseRef+":"+leaseRef); err != nil {
			return "", rec, err
		}
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
// more.
func (c Clone) PushLease(ctx context.Context, old string, rec LeaseRecord) (string, error) {
	msg, err := json.Marshal(rec)
	if err != nil {
		return "", err
	}
	tree, err := runEnv(ctx, c.Dir, nil, "git", "hash-object", "-t", "tree", "-w", "--stdin")
	if err != nil {
		return "", err
	}
	commit, err := run(ctx, c.Dir, "git", "-c", "user.name="+c.Name, "-c", "user.email="+c.Email,
		"commit-tree", strings.TrimSpace(tree), "-m", string(msg))
	if err != nil {
		return "", err
	}
	commit = strings.TrimSpace(commit)
	env, err := c.auth(ctx)
	if err != nil {
		return "", err
	}
	if _, err := runEnv(ctx, c.Dir, env, "git", "push", "--quiet", "--force-with-lease="+leaseRef+":"+old, "origin", commit+":"+leaseRef); err != nil {
		now, _, readErr := c.Lease(ctx)
		if readErr == nil && now != old && now != commit {
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
			return false, nil
		}
		return false, err
	}
	if rec.Holder != f.Holder || sha == "" {
		f.logf("holding the lease on %s as %s", f.Repository, f.Holder)
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
	if !g.f.holds() {
		return c, errLeaseLost
	}
	return g.GitHub.PostComment(ctx, issue, body)
}

func (g leasedGitHub) AddLabels(ctx context.Context, issue int, labels ...string) error {
	if !g.f.holds() {
		return errLeaseLost
	}
	return g.GitHub.AddLabels(ctx, issue, labels...)
}

func (g leasedGitHub) RemoveLabel(ctx context.Context, issue int, label string) error {
	if !g.f.holds() {
		return errLeaseLost
	}
	return g.GitHub.RemoveLabel(ctx, issue, label)
}

func (g leasedGitHub) CreatePullRequest(ctx context.Context, pr github.NewPullRequest) (p github.PullRequest, err error) {
	if !g.f.holds() {
		return p, errLeaseLost
	}
	return g.GitHub.CreatePullRequest(ctx, pr)
}

func (g leasedGitHub) Merge(ctx context.Context, n int, sha, method string) (string, error) {
	if !g.f.holds() {
		return "", errLeaseLost
	}
	return g.GitHub.Merge(ctx, n, sha, method)
}

func (g leasedGitHub) DeleteBranch(ctx context.Context, branch string) error {
	if !g.f.holds() {
		return errLeaseLost
	}
	return g.GitHub.DeleteBranch(ctx, branch)
}

// leasedRepo checks the lease before each push.
type leasedRepo struct {
	Repo
	f *Factory
}

func (r leasedRepo) Push(ctx context.Context, worktree, branch string) error {
	if !r.f.holds() {
		return errLeaseLost
	}
	return r.Repo.Push(ctx, worktree, branch)
}
