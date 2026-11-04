package factory

import (
	"context"
	"errors"
	"testing"
	"time"
)

type leaseFailure544 struct {
	Repo
	readErr, pushErr error
}

func (r *leaseFailure544) Lease(ctx context.Context) (string, LeaseRecord, error) {
	if r.readErr != nil {
		return "", LeaseRecord{}, r.readErr
	}
	return r.Repo.Lease(ctx)
}

func (r *leaseFailure544) PushLease(ctx context.Context, old string, rec LeaseRecord) (string, error) {
	if r.pushErr != nil {
		return "", r.pushErr
	}
	return r.Repo.PushLease(ctx, old, rec)
}

// An outage after a successful renewal keeps that renewal's window, and
// a successor still waits through its skew before taking over.
func TestLeaseOutageAfterConfirmedRenewal544(t *testing.T) {
	for _, failure := range []string{"read", "push"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			origin, _ := gitRepos(t)
			start := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
			now := start
			a, b := leaseFactory(t, origin, "watcher-a", &now), leaseFactory(t, origin, "watcher-b", &now)
			a.LeaseFor, b.LeaseFor = 15*time.Minute, 15*time.Minute
			r := &leaseFailure544{Repo: a.Repo}
			a.Repo = r
			if held, err := a.hold(ctx); err != nil || !held {
				t.Fatal(held, err)
			}
			step := a.inTenure(ctx)
			now = start.Add(8 * time.Minute)
			if held, err := a.hold(ctx); err != nil || !held {
				t.Fatal(held, err)
			}
			sha, confirmed, err := r.Repo.Lease(ctx)
			if err != nil || !confirmed.Until.Equal(start.Add(23*time.Minute)) {
				t.Fatalf("confirmed renewal = %+v, %v", confirmed, err)
			}
			outage := errors.New("lease request timed out")
			if failure == "read" {
				r.readErr = outage
			} else {
				r.pushErr = outage
			}
			for _, elapsed := range []time.Duration{16 * time.Minute, 22*time.Minute - time.Nanosecond, 22 * time.Minute} {
				now = start.Add(elapsed)
				if _, err := a.hold(ctx); !errors.Is(err, outage) {
					t.Fatalf("outage at %s = %v", elapsed, err)
				}
				if want := elapsed < 22*time.Minute; a.holdsFor(step) != want {
					t.Errorf("original step may act at %s = %v, want %v", elapsed, a.holdsFor(step), want)
				}
			}
			if got, rec, err := r.Repo.Lease(ctx); err != nil || got != sha || !rec.Until.Equal(confirmed.Until) {
				t.Fatalf("outage changed the confirmed lease: %s %+v, %v", got, rec, err)
			}
			now = start.Add(24*time.Minute - time.Nanosecond)
			if held, err := b.hold(ctx); err != nil || held {
				t.Fatalf("successor before expiry plus skew = %v, %v", held, err)
			}
			now = start.Add(24 * time.Minute)
			if held, err := b.hold(ctx); err != nil || !held {
				t.Fatalf("successor at expiry plus skew = %v, %v", held, err)
			}
		})
	}
}

// A late renewal remains recoverable through a moved refusal and a failed
// read, but neither authorizes effects before the watcher reads it back.
func TestLeaseLateRenewalSurvivesReadOutage544(t *testing.T) {
	ctx := context.Background()
	origin, _ := gitRepos(t)
	start := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	now := start
	f := leaseFactory(t, origin, "watcher-a", &now)
	f.LeaseFor = 15 * time.Minute
	late := &lateState{}
	r := &leaseFailure544{Repo: lateLanding{Repo: f.Repo, s: late}}
	f.Repo = r
	if held, err := f.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	step := f.inTenure(ctx)
	now = start.Add(8 * time.Minute)
	late.armed = true
	if _, err := f.hold(ctx); err == nil || !f.holdsFor(step) {
		t.Fatalf("uncertain renewal = %v; original step may act = %v", err, f.holdsFor(step))
	}
	now = start.Add(9 * time.Minute)
	if held, err := f.hold(ctx); err != nil || held || f.holdsFor(step) {
		t.Fatalf("ref-moved refusal = %v, %v; original step may act = %v", held, err, f.holdsFor(step))
	}
	r.readErr = context.DeadlineExceeded
	if _, err := f.hold(ctx); !errors.Is(err, r.readErr) || f.holdsFor(step) {
		t.Fatalf("read after refusal = %v; original step may act = %v", err, f.holdsFor(step))
	}
	r.readErr = nil
	if held, err := f.hold(ctx); err != nil || !held || !f.holdsFor(step) {
		t.Fatalf("adoption = %v, %v; original step may act = %v", held, err, f.holdsFor(step))
	}
	now = start.Add(22*time.Minute - time.Nanosecond)
	if !f.holdsFor(step) {
		t.Fatal("adopted renewal ended before its confirmed window")
	}
	now = start.Add(22 * time.Minute)
	if f.holdsFor(step) {
		t.Fatal("adopted renewal authorized effects at its confirmed cutoff")
	}
}
