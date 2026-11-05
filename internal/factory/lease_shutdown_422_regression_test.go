package factory

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type drainingRenewal422 struct {
	Repo
	reads                    int
	started, canceled, allow chan struct{}
}

func (r *drainingRenewal422) Lease(ctx context.Context) (string, LeaseRecord, error) {
	r.reads++
	if r.reads == 2 {
		close(r.started)
		<-ctx.Done()
		close(r.canceled)
		<-r.allow
		return "", LeaseRecord{}, ctx.Err()
	}
	if r.reads > 2 {
		deadline, ok := ctx.Deadline()
		if ctx.Err() != nil || !ok || time.Until(deadline) > 10*time.Second {
			return "", LeaseRecord{}, fmt.Errorf("release needs a fresh bounded context")
		}
	}
	return r.Repo.Lease(ctx)
}

func TestWatcherJoinsLeaseRenewalBeforeReleasing(t *testing.T) {
	r := newRig(t)
	r.f.Holder, r.f.LeaseFor = "draining-renewal", 5*time.Minute
	repo := &drainingRenewal422{Repo: r.f.Repo, started: make(chan struct{}), canceled: make(chan struct{}), allow: make(chan struct{})}
	r.f.Repo = repo
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.f.Watch(ctx, 10*time.Millisecond) }()
	allowed, ended := false, false
	t.Cleanup(func() {
		cancel()
		if !allowed {
			close(repo.allow)
		}
		if !ended {
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("watcher did not drain")
			}
		}
	})
	select {
	case <-repo.started:
	case <-time.After(10 * time.Second):
		t.Fatal("renewal did not start")
	}
	cancel()
	select {
	case <-repo.canceled:
	case <-time.After(10 * time.Second):
		t.Fatal("renewal was not canceled")
	}
	now := time.Now()
	next := leaseFactory(t, r.origin, "after-renewal", &now)
	if held, err := next.hold(context.Background()); held || err != nil {
		t.Fatalf("lease while renewal drains = %v, %v", held, err)
	}
	select {
	case <-done:
		ended = true
		t.Fatal("watcher returned before renewal stopped")
	default:
	}
	close(repo.allow)
	allowed = true
	select {
	case err := <-done:
		ended = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("watcher returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("watcher did not return after renewal stopped")
	}
	if held, err := next.hold(context.Background()); !held || err != nil {
		t.Fatalf("lease after renewal stopped = %v, %v", held, err)
	}
}

type replacedLease422 struct {
	Repo
	replace func(context.Context, string) error
}

func (r replacedLease422) PushLease(ctx context.Context, old string, rec LeaseRecord) (string, error) {
	if rec.Holder == "" {
		if err := r.replace(ctx, old); err != nil {
			return "", err
		}
	}
	return r.Repo.PushLease(ctx, old, rec)
}

func TestLeaseReleaseDoesNotOverwriteAConcurrentTakeover(t *testing.T) {
	ctx := context.Background()
	origin, _ := gitRepos(t)
	now := time.Now()
	f := leaseFactory(t, origin, "releasing", &now)
	next := leaseFactory(t, origin, "successor", &now)
	if held, err := f.hold(ctx); !held || err != nil {
		t.Fatalf("initial hold = %v, %v", held, err)
	}
	var replacement string
	f.Repo = replacedLease422{Repo: f.Repo, replace: func(ctx context.Context, old string) (err error) {
		replacement, err = next.Repo.PushLease(ctx, old, LeaseRecord{Holder: next.Holder, Until: now.Add(time.Hour)})
		return err
	}}
	if err := f.releaseLease(ctx); err != nil {
		t.Fatal(err)
	}
	sha, rec, err := next.Repo.Lease(ctx)
	if err != nil || sha != replacement || rec.Holder != next.Holder {
		t.Fatalf("release overwrote the successor: %s %+v %v", sha, rec, err)
	}
	if f.holds() {
		t.Fatal("stopped watcher still considers its lease held")
	}
}
