package factory

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A renewal that is still unwinding must finish before Watch returns or
// releases its lease. Cleanup must have its own deadline and live context.
func TestShutdownWaitsForRenewalAndBoundsCleanup(t *testing.T) {
	p := newParRig(t, 2)
	p.f.Holder, p.f.LeaseFor = "draining-renewal", 5*time.Minute
	r := &shutdownRenewalRepo{
		Repo: p.f.Repo, t: t,
		started: make(chan struct{}), canceled: make(chan struct{}),
		allow: make(chan struct{}), cleaned: make(chan struct{}),
	}
	p.f.Repo = r
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.f.Watch(ctx, 10*time.Millisecond) }()
	allowed, ended := false, false
	t.Cleanup(func() {
		cancel()
		if !allowed {
			close(r.allow)
		}
		if !ended {
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("watcher did not finish cleanup")
			}
		}
	})
	wait := func(ch <-chan struct{}, what string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out waiting for %s", what)
		}
	}
	wait(r.started, "lease renewal")
	cancel()
	wait(r.canceled, "canceled renewal")
	select {
	case err := <-done:
		ended = true
		t.Fatalf("Watch returned before renewal ended: %v", err)
	case <-r.cleaned:
		t.Fatal("lease cleanup started before renewal ended")
	default:
	}
	close(r.allow)
	allowed = true
	select {
	case err := <-done:
		ended = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Watch = %v; want cancellation", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Watch did not return after renewal ended")
	}
	wait(r.cleaned, "lease cleanup")
	_, rec, err := r.Repo.Lease(context.Background())
	if err != nil || rec.Holder != "" || !rec.Until.IsZero() {
		t.Fatalf("cleanup left lease %+v, %v", rec, err)
	}
}

type shutdownRenewalRepo struct {
	Repo
	t                                 *testing.T
	reads                             int
	started, canceled, allow, cleaned chan struct{}
}

func (r *shutdownRenewalRepo) Lease(ctx context.Context) (string, LeaseRecord, error) {
	r.reads++ // Factory serializes every lease call under its lease lock.
	switch r.reads {
	case 2:
		close(r.started)
		<-ctx.Done()
		close(r.canceled)
		<-r.allow
		return "", LeaseRecord{}, ctx.Err()
	case 3:
		if ctx.Err() != nil {
			r.t.Errorf("cleanup inherited cancellation: %v", ctx.Err())
		}
		deadline, bounded := ctx.Deadline()
		if left := time.Until(deadline); !bounded || left <= 0 || left > 5*time.Second {
			r.t.Errorf("cleanup has no live deadline within five seconds: %v, %v", deadline, bounded)
		}
		close(r.cleaned)
	}
	return r.Repo.Lease(ctx)
}

// A successor may replace the lease after shutdown reads it. The release
// must compare the old ref, and never retry by overwriting that successor.
func TestShutdownLeaseReleaseLosesRaceToSuccessor(t *testing.T) {
	origin, _ := gitRepos(t)
	now := time.Now()
	f := leaseFactory(t, origin, "stopping", &now)
	ctx := context.Background()
	if held, err := f.hold(ctx); err != nil || !held {
		t.Fatalf("initial lease = %v, %v", held, err)
	}
	r := &shutdownLeaseRaceRepo{Repo: f.Repo, replacement: LeaseRecord{Holder: "successor", Until: now.Add(time.Hour)}}
	f.Repo = r
	if err := f.releaseLease(ctx); err != nil {
		t.Fatal(err)
	}
	sha, rec, err := r.Repo.Lease(ctx)
	if err != nil || sha != r.replaced || rec.Holder != r.replacement.Holder || !rec.Until.Equal(r.replacement.Until) || f.holds() {
		t.Fatalf("release displaced the successor: %s %+v, %v", sha, rec, err)
	}
}

type shutdownLeaseRaceRepo struct {
	Repo
	replacement LeaseRecord
	replaced    string
}

func (r *shutdownLeaseRaceRepo) PushLease(ctx context.Context, old string, rec LeaseRecord) (string, error) {
	var err error
	r.replaced, err = r.Repo.PushLease(ctx, old, r.replacement)
	if err != nil {
		return "", err
	}
	return r.Repo.PushLease(ctx, old, rec)
}
