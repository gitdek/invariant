package factory

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

// Use the existing local Git fixture for real compare-and-swap leases.
// Only the boundary where a remote read or push fails is substituted.
type outageRepo544 struct {
	Repo
	readErr, pushErr error
	landOnError      bool
	pushes           []string
}

func (r *outageRepo544) Lease(ctx context.Context) (string, LeaseRecord, error) {
	if r.readErr != nil {
		return "", LeaseRecord{}, r.readErr
	}
	return r.Repo.Lease(ctx)
}

func (r *outageRepo544) PushLease(ctx context.Context, old string, rec LeaseRecord) (string, error) {
	r.pushes = append(r.pushes, old)
	if r.pushErr != nil {
		if r.landOnError {
			if _, err := r.Repo.PushLease(ctx, old, rec); err != nil {
				return "", err
			}
		}
		return "", r.pushErr
	}
	return r.Repo.PushLease(ctx, old, rec)
}

func leaseFixture544(t *testing.T, duration time.Duration) (*Factory, *outageRepo544, *time.Time, context.Context) {
	t.Helper()
	origin, _ := gitRepos(t)
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	f := leaseFactory(t, origin, "holder-544", &now)
	f.LeaseFor = duration
	r := &outageRepo544{Repo: f.Repo}
	f.Repo = r
	f.GitHub.(*fakeGitHub).open(1, "gitdek", "Keep this build", "Kind: plumbing")
	if held, err := f.hold(context.Background()); err != nil || !held {
		t.Fatalf("initial acquisition = %v, %v", held, err)
	}
	return f, r, &now, f.inTenure(context.Background())
}

// Observe the authority used by both running steps and their effects,
// rather than inspecting the holder's private lease fields.
func leaseActs544(t *testing.T, f *Factory, step context.Context, want bool) {
	t.Helper()
	if got := f.holds(); got != want {
		t.Errorf("may act at %s = %v, want %v", f.now(), got, want)
	}
	gh := f.GitHub.(*fakeGitHub)
	before := len(gh.comments[1])
	g := leasedGitHub{GitHub: gh, f: f}
	_, err := g.PostComment(step, 1, "effect in the original step")
	if want {
		if err != nil || len(gh.comments[1]) != before+1 {
			t.Errorf("original step's effect = %v; want one published comment", err)
		}
	} else if !errors.Is(err, errLeaseLost) || len(gh.comments[1]) != before {
		t.Errorf("effect without authority = %v; posts %d -> %d", err, before, len(gh.comments[1]))
	}
}

func outageErrors544() []struct {
	name string
	err  error
} {
	return []struct {
		name string
		err  error
	}{
		{"server_500", errors.New("git push: remote: Internal Server Error (HTTP 500)")},
		{"timeout", &net.OpError{Op: "write", Net: "tcp", Err: context.DeadlineExceeded}},
		{"connection_dropped", fmt.Errorf("git push: remote end hung up unexpectedly: %w", io.ErrUnexpectedEOF)},
	}
}

func TestLeasePushFailuresKeepOnlyTheConfirmedWindow544(t *testing.T) {
	for _, duration := range []time.Duration{5 * time.Minute, 15 * time.Minute} {
		for _, failure := range outageErrors544() {
			t.Run(duration.String()+"/"+failure.name, func(t *testing.T) {
				f, r, now, step := leaseFixture544(t, duration)
				start := *now
				sha, confirmed, err := r.Repo.Lease(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				r.pushErr = failure.err
				// No early renewal; after half-life, each poll tries again.
				*now = start.Add(duration/2 - time.Nanosecond)
				before := len(r.pushes)
				if held, err := f.hold(context.Background()); err != nil || !held || len(r.pushes) != before {
					t.Fatalf("before half-life: hold = %v, %v; pushes %d -> %d", held, err, before, len(r.pushes))
				}
				for _, at := range []time.Time{
					start.Add(duration / 2),
					start.Add(duration/2 + time.Minute),
					confirmed.Until.Add(-time.Minute - time.Nanosecond),
					confirmed.Until.Add(-time.Minute),
					confirmed.Until.Add(time.Minute),
				} {
					*now = at
					before := len(r.pushes)
					if _, err := f.hold(context.Background()); !errors.Is(err, failure.err) {
						t.Errorf("renewal hid its failure: %v", err)
					}
					if len(r.pushes) != before+1 || r.pushes[len(r.pushes)-1] != sha {
						t.Fatalf("renewal must retry against confirmed commit %s: %v", sha, r.pushes)
					}
					leaseActs544(t, f, step, at.Before(confirmed.Until.Add(-time.Minute)))
				}
				if got, rec, err := r.Repo.Lease(context.Background()); err != nil || got != sha || !rec.Until.Equal(confirmed.Until) {
					t.Fatalf("failed pushes changed the remote lease: %s %+v, %v", got, rec, err)
				}
				// A failed first acquisition grants no authority at all.
				fresh := &Factory{Repo: r, Holder: "fresh-544", LeaseFor: duration, Now: f.Now}
				if _, err := fresh.hold(context.Background()); !errors.Is(err, failure.err) || fresh.holds() {
					t.Errorf("failed acquisition: error %v, holds %v", err, fresh.holds())
				}
			})
		}
	}
}

func TestLeaseReadFailuresKeepOnlyTheConfirmedWindow544(t *testing.T) {
	for _, failure := range outageErrors544() {
		t.Run(failure.name, func(t *testing.T) {
			f, r, now, step := leaseFixture544(t, 15*time.Minute)
			start := *now
			r.readErr = failure.err
			pushes := len(r.pushes)
			for _, elapsed := range []time.Duration{time.Minute, 8 * time.Minute, 14*time.Minute - time.Nanosecond, 14 * time.Minute, 16 * time.Minute} {
				*now = start.Add(elapsed)
				if _, err := f.hold(context.Background()); !errors.Is(err, failure.err) {
					t.Errorf("read failure was hidden: %v", err)
				}
				leaseActs544(t, f, step, elapsed < 14*time.Minute)
			}
			fresh := &Factory{Repo: r, Holder: "fresh-544", LeaseFor: 15 * time.Minute, Now: f.Now}
			if _, err := fresh.hold(context.Background()); !errors.Is(err, failure.err) || fresh.holds() {
				t.Errorf("failed first read: error %v, holds %v", err, fresh.holds())
			}
			if len(r.pushes) != pushes {
				t.Errorf("pushed a lease without a successful read: %v", r.pushes)
			}
		})
	}
}

func TestLeaseRecoveryKeepsTheRunningStep544(t *testing.T) {
	for _, landed := range []bool{false, true} {
		t.Run(fmt.Sprintf("failed_push_landed_%v", landed), func(t *testing.T) {
			f, r, now, step := leaseFixture544(t, 15*time.Minute)
			start := *now
			first, _, err := r.Repo.Lease(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			*now = start.Add(8 * time.Minute)
			r.pushErr, r.landOnError = io.ErrUnexpectedEOF, landed
			if _, err := f.hold(context.Background()); !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal(err)
			}
			leaseActs544(t, f, step, true)
			// The push's result is still unknown while reads fail. Only the
			// original window authorizes effects until it is read back.
			r.readErr = context.DeadlineExceeded
			*now = start.Add(10 * time.Minute)
			f.hold(context.Background())
			leaseActs544(t, f, step, true)
			*now = start.Add(14 * time.Minute)
			f.hold(context.Background())
			leaseActs544(t, f, step, false)
			// At this point the next read either adopts the lost answer or
			// renews the unchanged commit; both continue the original step.
			r.readErr, r.pushErr = nil, nil
			before := len(r.pushes)
			if held, err := f.hold(context.Background()); err != nil || !held {
				t.Fatalf("recovery = %v, %v", held, err)
			}
			wantUntil := start.Add(29 * time.Minute)
			if landed {
				wantUntil = start.Add(23 * time.Minute)
				if len(r.pushes) != before {
					t.Error("adopting a lease with more than half remaining pushed another renewal")
				}
			} else if len(r.pushes) != before+1 || r.pushes[len(r.pushes)-1] != first {
				t.Errorf("recovery did not renew the original commit: %v", r.pushes)
			}
			if _, rec, err := r.Repo.Lease(context.Background()); err != nil || !rec.Until.Equal(wantUntil) {
				t.Fatalf("recovered lease = %+v, %v; want until %s", rec, err, wantUntil)
			}
			leaseActs544(t, f, step, true)
			*now = wantUntil.Add(-time.Minute - time.Nanosecond)
			leaseActs544(t, f, step, true)
			*now = wantUntil.Add(-time.Minute)
			leaseActs544(t, f, step, false)
		})
	}
}

func TestLeaseDefiniteLossOverridesErrorRetention544(t *testing.T) {
	for _, cause := range []string{"ref_moved", "another_holder"} {
		t.Run(cause, func(t *testing.T) {
			f, r, now, step := leaseFixture544(t, 15*time.Minute)
			*now = now.Add(8 * time.Minute)
			r.pushErr = io.ErrUnexpectedEOF
			f.hold(context.Background())
			leaseActs544(t, f, step, true)
			*now = now.Add(time.Minute)
			if cause == "ref_moved" {
				r.pushErr = fmt.Errorf("push refused: %w", ErrLeaseMoved)
			} else {
				sha, _, err := r.Repo.Lease(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if _, err := r.Repo.PushLease(context.Background(), sha, LeaseRecord{Holder: "successor-544", Until: now.Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
				r.pushErr = nil
			}
			before := len(r.pushes)
			if held, err := f.hold(context.Background()); err != nil || held {
				t.Errorf("definite loss = %v, %v; want false, nil", held, err)
			}
			if cause == "another_holder" && len(r.pushes) != before {
				t.Error("tried to overwrite another holder's unexpired lease")
			}
			leaseActs544(t, f, step, false)
			r.readErr = context.DeadlineExceeded
			f.hold(context.Background())
			leaseActs544(t, f, step, false)
		})
	}
}

func TestBuildFinishesDuringLeaseRenewalOutage544(t *testing.T) {
	ctx := context.Background()
	r, base := plumbingRig(t, "page/page.go")
	held := &heldBuild{inner: base, started: make(chan struct{}), release: make(chan struct{})}
	r.f.Plumbing = held
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	r.f.Now = func() time.Time { return now }
	r.f.Holder, r.f.LeaseFor, r.f.Parallel = "building-544", 15*time.Minute, 2
	raw := r.f.Repo
	outage := &outageRepo544{Repo: raw}
	r.f.Repo = leasedRepo{Repo: outage, f: r.f}
	r.f.GitHub = leasedGitHub{GitHub: r.gh, f: r.f}
	if held, err := r.f.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	released := false
	t.Cleanup(func() {
		if !released {
			close(held.release)
		}
		r.f.Wait()
	})
	ratified := startHeldPlanBuild(t, r, held)
	now = now.Add(8 * time.Minute)
	outage.pushErr = errors.New("git push: HTTP 500 Internal Server Error")
	if _, err := r.f.hold(ctx); !errors.Is(err, outage.pushErr) {
		t.Fatal(err)
	}
	close(held.release)
	released = true
	r.f.Wait()
	if state, _, err := raw.Run(ctx, 1, buildStepName(ratified)); err != nil || state != RunDone {
		t.Errorf("build finishing during renewal failure was discarded: state %d, %v", state, err)
	}
	r.poll()
	r.f.Wait()
	if last := r.gh.last(1); last.Marker.Kind != KindPR || last.Marker.Failure == FailStopped {
		t.Errorf("build must be published while its confirmed lease lasts: %+v", last.Marker)
	}
}
