package factory

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// leaseFactory is a watcher on origin with its own clone and clock.
func leaseFactory(t *testing.T, origin, holder string, now *time.Time) *Factory {
	t.Helper()
	clone := Clone{Dir: filepath.Join(t.TempDir(), "clone"), Remote: origin, Name: "Joseph", Email: "7275925+gitdek@users.noreply.github.com"}
	if err := clone.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &Factory{Repository: "o/r", Repo: clone, GitHub: newFakeGitHub(t, origin), Holder: holder, LeaseFor: 5 * time.Minute,
		Now: func() time.Time { return *now }, Log: t.Logf}
}

// Two watchers on one repository: only one holds the lease, until it stops
// renewing it and the lease runs out.
func TestOneWatcherHoldsTheLease(t *testing.T) {
	ctx := context.Background()
	origin, _ := gitRepos(t)
	start := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	nowA, nowB := start, start
	a, b := leaseFactory(t, origin, "watcher-a", &nowA), leaseFactory(t, origin, "watcher-b", &nowB)

	if held, err := a.hold(ctx); err != nil || !held || !a.holds() {
		t.Fatalf("a.hold = %v, %v; want a to take the free lease", held, err)
	}
	if held, err := b.hold(ctx); err != nil || held || b.holds() {
		t.Fatalf("b.hold = %v, %v; want b to wait while a holds the lease", held, err)
	}
	// a renews on every poll, so b keeps waiting.
	nowA, nowB = start.Add(4*time.Minute), start.Add(4*time.Minute)
	if held, _ := a.hold(ctx); !held {
		t.Fatal("a couldn't renew its own lease")
	}
	if held, _ := b.hold(ctx); held {
		t.Fatal("b took a lease a had just renewed")
	}
	// a stops renewing. Its own clock stops it a minute before the lease
	// runs out, and b waits until a minute after.
	nowA = start.Add(4*time.Minute + 4*time.Minute + 30*time.Second)
	if a.holds() {
		t.Fatal("a still acts within a minute of its lease running out")
	}
	nowB = start.Add(4*time.Minute + 5*time.Minute + 30*time.Second)
	if held, _ := b.hold(ctx); held {
		t.Fatal("b took the lease within a minute of it running out")
	}
	nowB = start.Add(4*time.Minute + 6*time.Minute + time.Second)
	if held, err := b.hold(ctx); err != nil || !held {
		t.Fatalf("b.hold = %v, %v; want b to take the lease once it ran out", held, err)
	}
	if held, _ := a.hold(ctx); held || a.holds() {
		t.Fatal("a took its lease back from b")
	}
}

// Two watchers read the same lease and both try to take it: git lets only
// one push win.
func TestTheLeaseMovesOnlyFromWhatWasRead(t *testing.T) {
	ctx := context.Background()
	origin, _ := gitRepos(t)
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	a, b := leaseFactory(t, origin, "watcher-a", &now), leaseFactory(t, origin, "watcher-b", &now)
	shaA, _, err := a.Repo.Lease(ctx)
	if err != nil || shaA != "" {
		t.Fatalf("Lease = %q, %v; want none yet", shaA, err)
	}
	if _, err := b.Repo.PushLease(ctx, "", LeaseRecord{Holder: "watcher-b", Until: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Repo.PushLease(ctx, shaA, LeaseRecord{Holder: "watcher-a", Until: now.Add(time.Hour)}); !errors.Is(err, ErrLeaseMoved) {
		t.Fatalf("a's push over b's lease = %v; want ErrLeaseMoved", err)
	}
	sha, rec, err := a.Repo.Lease(ctx)
	if err != nil || rec.Holder != "watcher-b" {
		t.Fatalf("Lease = %s %+v, %v; want b's", sha, rec, err)
	}
	// Renewing from the lease it read works, and a push from one already
	// replaced fails.
	if _, err := b.Repo.PushLease(ctx, sha, LeaseRecord{Holder: "watcher-b", Until: now.Add(2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Repo.PushLease(ctx, sha, LeaseRecord{Holder: "watcher-b", Until: now.Add(3 * time.Hour)}); !errors.Is(err, ErrLeaseMoved) {
		t.Fatalf("a push from a lease that was replaced = %v; want ErrLeaseMoved", err)
	}
}

// A watcher without the lease takes no effect: no post, no label, no pull
// request, no merge and no push.
func TestNoEffectWithoutTheLease(t *testing.T) {
	ctx := context.Background()
	origin, _ := gitRepos(t)
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	f := leaseFactory(t, origin, "watcher-a", &now)
	gh := f.GitHub.(*fakeGitHub)
	gh.open(1, "gitdek", "a buffer", "/invariant solve")
	g, r := leasedGitHub{f.GitHub, f}, leasedRepo{f.Repo, f}
	if _, err := g.PostComment(ctx, 1, "hello"); !errors.Is(err, errLeaseLost) {
		t.Fatalf("PostComment without the lease = %v; want errLeaseLost", err)
	}
	if err := g.AddLabels(ctx, 1, LabelBuilding); !errors.Is(err, errLeaseLost) {
		t.Fatalf("AddLabels without the lease = %v", err)
	}
	if _, err := g.Merge(ctx, 2, "abc", "merge"); !errors.Is(err, errLeaseLost) {
		t.Fatalf("Merge without the lease = %v", err)
	}
	if err := r.Push(ctx, "", "main"); !errors.Is(err, errLeaseLost) {
		t.Fatalf("Push without the lease = %v", err)
	}
	if len(gh.posts(1)) != 0 || len(gh.labelsOf(1)) != 0 {
		t.Fatalf("a watcher without the lease changed the issue: %v, %v", gh.posts(1), gh.labelsOf(1))
	}
	// With the lease, the same post goes through.
	if held, err := f.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	if _, err := g.PostComment(ctx, 1, "hello"); err != nil {
		t.Fatalf("PostComment with the lease = %v", err)
	}
}

// A holder renews only once half its lease has gone, so a watcher that polls
// every 30 seconds pushes a few times an hour, not every poll.
func TestTheLeaseRenewsAtHalfLife(t *testing.T) {
	ctx := context.Background()
	origin, _ := gitRepos(t)
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	f := leaseFactory(t, origin, "watcher-a", &now)
	if held, err := f.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	first, _, _ := f.Repo.Lease(ctx)
	now = now.Add(2 * time.Minute)
	if held, _ := f.hold(ctx); !held {
		t.Fatal("lost the lease two minutes in")
	}
	if sha, _, _ := f.Repo.Lease(ctx); sha != first {
		t.Fatal("renewed with most of the lease left")
	}
	now = now.Add(time.Minute)
	if held, _ := f.hold(ctx); !held {
		t.Fatal("lost the lease three minutes in")
	}
	if sha, rec, _ := f.Repo.Lease(ctx); sha == first || !rec.Until.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("didn't renew with less than half the lease left: %s %+v", sha, rec)
	}
}
