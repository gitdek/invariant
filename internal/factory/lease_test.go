package factory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
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

// The watcher's renewal takes the lease lock before using the clone. A
// leased run write leaves both available during its wait, and its next
// push still belongs to the same tenure after an ordinary renewal.
func TestALeasedRunPushAllowsRenewalDuringBackoff(t *testing.T) {
	for _, operation := range []string{"Record", "Finish"} {
		t.Run(operation, func(t *testing.T) {
			fixture := newRun545Fixture(t, operation, run545Reply{"fail", run545ServerError}, run545Reply{"ok", ""})
			synctest.Test(t, func(t *testing.T) {
				f := &Factory{Repo: fixture.clone, Holder: "watcher-a", LeaseFor: 2 * time.Minute, Now: time.Now}
				if held, err := f.hold(context.Background()); err != nil || !held {
					t.Fatal(held, err)
				}
				ctx := f.inTenure(context.Background())
				tenure := f.lease.tenure
				first := f.lease.sha
				time.Sleep(55 * time.Second)
				timer := time.AfterFunc(10*time.Second, func() {
					if held, err := f.hold(context.Background()); err != nil || !held {
						t.Errorf("renewal = %v, %v", held, err)
					}
				})
				defer timer.Stop()
				if err := fixture.call(ctx, leasedRepo{Repo: fixture.clone, f: f}, operation); err != nil {
					t.Fatal(err)
				}
				if f.lease.sha == first || f.lease.tenure != tenure {
					t.Error("the lease must renew in the same tenure")
				}
				fixture.checkAttempts(t, 2, 1)
				fixture.checkPublished(t, operation)
			})
		})
	}
}

// A retry can pass its early permission check while another operation
// owns the clone. Permission lost before admission must still stop it.
func TestALeasedRunPushRechecksPermissionAfterCloneContention(t *testing.T) {
	for _, operation := range []string{"Record", "Finish"} {
		for _, change := range []string{"expiry", "loss", "tenure"} {
			t.Run(operation+"/"+change, func(t *testing.T) {
				fixture := newRun545Fixture(t, operation, run545Reply{"fail", run545ServerError}, run545Reply{"ok", ""})
				synctest.Test(t, func(t *testing.T) {
					now := time.Now()
					waiting := false
					checked := make(chan struct{}, 1)
					f := &Factory{Repo: fixture.clone, Holder: "watcher-a", LeaseFor: 5 * time.Minute, Now: func() time.Time {
						if waiting {
							checked <- struct{}{}
						}
						return now
					}}
					if held, err := f.hold(context.Background()); err != nil || !held {
						t.Fatal(held, err)
					}
					ctx := f.inTenure(context.Background())
					done := make(chan struct{})
					timer := time.AfterFunc(10*time.Second, func() {
						defer close(done)
						unlock := fixture.clone.lock()
						defer unlock()
						f.lease.mu.Lock()
						waiting = true
						f.lease.mu.Unlock()
						<-checked
						// Taking the lease lock waits for the retry's early
						// check to finish, while the clone stays unavailable.
						f.lease.mu.Lock()
						waiting = false
						switch change {
						case "expiry":
							now = f.lease.until.Add(-leaseSkew)
						case "loss":
							f.lease.until = time.Time{}
						case "tenure":
							f.lease.tenure++
						}
						f.lease.mu.Unlock()
					})
					defer timer.Stop()
					err := fixture.call(ctx, leasedRepo{Repo: fixture.clone, f: f}, operation)
					<-done
					want := errLeaseLost
					if change == "tenure" {
						want = errStaleTenure
					}
					if !errors.Is(err, want) {
						t.Errorf("%s = %v; want %v", operation, err, want)
					}
					fixture.checkAttempts(t, 1, 1)
					if got := fixture.remote(t); got != fixture.recorded {
						t.Errorf("an unauthorized retry changed the ref to %s", got)
					}
				})
			})
		}
	}
}

// A resumed run may record its result in the tenure it resumed in. A retry
// must not silently refresh that tenure again if another takeover happens
// while the result push waits.
func TestAResumedRunPushKeepsItsResumedTenure(t *testing.T) {
	for _, takeover := range []bool{false, true} {
		t.Run(map[bool]string{false: "same tenure", true: "another takeover"}[takeover], func(t *testing.T) {
			fixture := newRun545Fixture(t, "Finish", run545Reply{"fail", run545ServerError}, run545Reply{"ok", ""})
			synctest.Test(t, func(t *testing.T) {
				f := &Factory{Repo: fixture.clone, Holder: "watcher-a", LeaseFor: 5 * time.Minute, Now: time.Now}
				if held, err := f.hold(context.Background()); err != nil || !held {
					t.Fatal(held, err)
				}
				ctx := f.inTenure(context.Background())
				f.lease.tenure++
				ctx, resumed := f.resumed(ctx)
				if !resumed {
					t.Fatal("the run must resume in the new tenure")
				}
				if takeover {
					timer := time.AfterFunc(5*time.Second, func() {
						f.lease.mu.Lock()
						defer f.lease.mu.Unlock()
						f.lease.tenure++
					})
					defer timer.Stop()
				}
				err := fixture.call(ctx, leasedRepo{Repo: fixture.clone, f: f}, "Finish")
				if takeover {
					if !errors.Is(err, errStaleTenure) {
						t.Fatalf("Finish = %v; want errStaleTenure", err)
					}
					fixture.checkAttempts(t, 1, 1)
					if got := fixture.remote(t); got != fixture.recorded {
						t.Errorf("a stale resumed run changed the ref to %s", got)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					fixture.checkAttempts(t, 2, 1)
					fixture.checkPublished(t, "Finish")
				}
			})
		})
	}
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
	// With the lease, the same post goes through for a step in its tenure,
	// and for nothing else (D-0237).
	if held, err := f.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	if _, err := g.PostComment(ctx, 1, "hello"); !errors.Is(err, errNoStep) {
		t.Fatalf("PostComment with the lease, outside any step = %v; want errNoStep", err)
	}
	if _, err := g.PostComment(f.inTenure(ctx), 1, "hello"); err != nil {
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

// A push of the lease that lands though git reports it failed, as when
// receive-pack stops before it answers, pushed the lease: PushLease's own
// read shows its commit there, so it says so (#357's review). The origin's
// reference-transaction hook stops receive-pack once the update is
// committed, before it can answer.
func TestALeasePushWhoseAnswerIsLostPushedTheLease(t *testing.T) {
	ctx := context.Background()
	origin, clone := gitRepos(t)
	armed := filepath.Join(t.TempDir(), "armed")
	hook := "#!/bin/sh\n" +
		"[ \"$1\" = committed ] || exit 0\n" +
		"grep -q ' " + leaseRef + "$' || exit 0\n" +
		"[ -f '" + armed + "' ] || exit 0\n" +
		"rm -f '" + armed + "'\n" +
		"kill -9 $PPID\n"
	if err := os.WriteFile(filepath.Join(origin, "hooks", "reference-transaction"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	arm := func() {
		t.Helper()
		if err := os.WriteFile(armed, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The hook does lose git's answer: git says the push failed, and it
	// landed.
	first := LeaseRecord{Holder: "watcher-b", Until: time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)}
	msg, _ := json.Marshal(first)
	commit, err := clone.emptyCommit(ctx, string(msg))
	if err != nil {
		t.Fatal(err)
	}
	arm()
	if err := clone.pushRef(ctx, leaseRef, "", commit); err == nil {
		t.Fatal("git says the push it never answered succeeded, so the hook loses no answer")
	}
	if sha, _, err := clone.Lease(ctx); err != nil || sha != commit {
		t.Fatalf("the lease is at %s, %v; the push must have landed at %s", sha, err, commit)
	}
	arm()
	rec := LeaseRecord{Holder: "watcher-a", Until: time.Date(2026, 10, 31, 9, 5, 0, 0, time.UTC)}
	pushed, err := clone.PushLease(ctx, commit, rec)
	if _, statErr := os.Stat(armed); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("the hook never stopped the push")
	}
	if err != nil {
		t.Fatalf("PushLease = %v, though its own read shows the push landed", err)
	}
	sha, got, err := clone.Lease(ctx)
	if err != nil || sha != pushed || got.Holder != rec.Holder || !got.Until.Equal(rec.Until) {
		t.Fatalf("the lease is %s %+v, %v; want %s %+v", sha, got, err, pushed, rec)
	}
}
