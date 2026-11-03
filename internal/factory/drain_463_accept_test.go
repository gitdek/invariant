package factory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the ordinary issue flow, real local Git run records and lease
// CAS, with the existing fake agents and concurrent GitHub fixture.
// A missing control is a behavioral test failure on the original checkout,
// rather than making every test in this package fail to compile.
func drain463Control(t *testing.T, f *Factory) func() {
	t.Helper()
	d, ok := any(f).(interface{ Drain() })
	if !ok {
		t.Fatal("Factory needs Drain() to finish active work without admitting another agent operation")
	}
	return d.Drain
}

func drain463Soon(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !ok() {
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", what)
		case <-tick.C:
		}
	}
}

type drain463Watch struct {
	done   chan struct{}
	cancel context.CancelFunc
	err    error // read only after done closes
}

func drain463Start(t *testing.T, f *Factory, every time.Duration) *drain463Watch {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	w := &drain463Watch{done: make(chan struct{}), cancel: cancel}
	go func() {
		w.err = f.Watch(ctx, every)
		close(w.done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-w.done:
		case <-time.After(15 * time.Second):
			t.Error("watcher did not stop during fixture cleanup")
		}
	})
	return w
}

func (w *drain463Watch) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-w.done:
		return w.err
	case <-time.After(15 * time.Second):
		t.Fatal("watcher did not finish promptly after its work ended")
		return nil
	}
}

func (w *drain463Watch) stillRunning(t *testing.T) {
	t.Helper()
	select {
	case <-w.done:
		t.Fatalf("watcher exited with an agent operation still held open: %v", w.err)
	default:
	}
}

func drain463Deferred(t *testing.T, p *parRig) {
	t.Helper()
	for _, n := range []int{2, 6} {
		post := p.last(n)
		if post.Marker.Kind != KindRatified {
			t.Fatalf("deferred build #%d became %q instead of retaining its ratification", n, post.Marker.Kind)
		}
		slots455NoRun(t, p, n, buildStepName(post))
	}
	for _, n := range []int{3, 4, 5} {
		slots455NoRun(t, p, n, "draft-issue")
		if len(p.posts(n)) != 0 {
			t.Fatalf("deferring draft #%d must not answer it as a stopped or failed run", n)
		}
	}
	for _, run := range []struct {
		n    int
		kind string
	}{{2, "build"}, {3, "draft"}, {4, "draft"}, {5, "draft"}, {6, "plan-build"}} {
		if got := p.held.started(run.kind, run.n); got != 0 {
			t.Fatalf("draining started %d %s operations on #%d", got, run.kind, run.n)
		}
	}
}

func TestDrainFinishesBuildsAndKeepsQuickSteps463(t *testing.T) {
	drain463Control(t, &Factory{})
	for _, capacity := range []int{1, 3} {
		for _, active := range []string{"modeled", "plumbing"} {
			t.Run(fmt.Sprintf("slots-%d/%s", capacity, active), func(t *testing.T) {
				p, h := slots455Rig(t, capacity)
				body, kind := slots455Model, "build"
				if active == "plumbing" {
					body, kind = slots455Plumbing, "plan-build"
				}
				build := slots455Proposal(t, p, 1, body)
				queuedBuild := slots455Proposal(t, p, 2, slots455Model)
				queuedPlan := slots455Proposal(t, p, 6, slots455Plumbing)
				merge := slots455Proposal(t, p, 10, slots455Plumbing)
				p.say(10, merge)
				p.settle()
				pr := p.expect(10, KindPR, LabelPR).Marker.PR
				p.f.Holder, p.f.LeaseFor = "draining-463", 5*time.Minute
				var beats atomic.Int64
				p.f.Activity = func([]Step) { beats.Add(1) }
				p.held.holdOpen(1)
				p.say(1, build)
				w := drain463Start(t, p.f, 20*time.Millisecond)
				drain463Soon(t, "the active build", func() bool { return p.held.started(kind, 1) == 1 })
				ratified := p.last(1)
				queue := func() {
					p.say(2, queuedBuild)
					p.say(6, queuedPlan)
					slots455Open(p, 3, slots455Model)
					slots455Open(p, 4, slots455Plumbing)
					slots455Open(p, 5, "Plan a decision graph.\n\n/invariant plan")
				}
				if capacity == 1 {
					// These steps were already waiting before drain began.
					queue()
					drain463Soon(t, "queued ratifications and all three draft requests", func() bool {
						return p.last(2).Marker.Kind == KindRatified && p.last(6).Marker.Kind == KindRatified &&
							h.readCount(3) > 0 && h.readCount(4) > 0 && h.readCount(5) > 0
					})
				}
				drain := drain463Control(t, p.f)
				drain()
				drain() // repeated requests neither cancel nor reopen admission
				if capacity == 3 {
					// Two slots are free: the drain itself must refuse this work.
					queue()
				}
				slots455Open(p, 12, "An issue without a proposal.")
				command := p.say(12, "/invariant ratify ffffffffffff")
				p.ci(pr)
				polls, heartbeats := p.polls(), beats.Load()
				drain463Soon(t, "quick steps and repeated heartbeats during drain", func() bool {
					return p.last(10).Marker.Kind == KindMerged && slots455Replies(p, 12, command.ID) == 1 &&
						p.last(2).Marker.Kind == KindRatified && p.last(6).Marker.Kind == KindRatified &&
						p.polls() >= polls+3 && beats.Load() >= heartbeats+3
				})
				w.stillRunning(t)
				if active, _ := p.held.going(); active != 1 {
					t.Fatalf("drain canceled the held operation or admitted another: active=%d", active)
				}
				drain463Deferred(t, p)
				_, lease, err := p.clone.Lease(context.Background())
				if err != nil || lease.Holder != p.f.Holder {
					t.Fatalf("lease released before build finished: %+v, %v", lease, err)
				}
				p.held.let(1)
				if err := w.wait(t); err != nil {
					t.Fatalf("a graceful drain returned %v", err)
				}
				p.expect(1, KindPR, LabelPR)
				if state, _, err := p.clone.Run(context.Background(), 1, buildStepName(ratified)); err != nil || state != RunDone {
					t.Fatalf("active build was lost: run=%v, error=%v", state, err)
				}
				drain463Deferred(t, p)
				if len(p.f.Steps()) != 0 {
					t.Fatal("drained watcher left steps running or waiting")
				}
				_, lease, err = p.clone.Lease(context.Background())
				if err != nil || lease.Holder != "" || !lease.Until.IsZero() {
					t.Fatalf("completed drain did not relinquish its lease: %+v, %v", lease, err)
				}
			})
		}
	}
}

type drain463LeaseReader struct {
	Repo
	reads atomic.Int64
}

func (r *drain463LeaseReader) Lease(ctx context.Context) (string, LeaseRecord, error) {
	sha, rec, err := r.Repo.Lease(ctx)
	r.reads.Add(1)
	return sha, rec, err
}

func TestDrainRenewsLeaseAndHandsBuildToSuccessor463(t *testing.T) {
	drain463Control(t, &Factory{})
	p, h := slots455Rig(t, 1)
	command := slots455Proposal(t, p, 1, slots455Model)
	var nanos atomic.Int64
	nanos.Store(time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC).UnixNano())
	now := func() time.Time { return time.Unix(0, nanos.Load()).UTC() }
	p.f.Now, p.f.Holder, p.f.LeaseFor = now, "old-463", 5*time.Minute
	p.held.holdOpen(1)
	p.say(1, command)
	old := drain463Start(t, p.f, 20*time.Millisecond)
	drain463Soon(t, "the old watcher's build", func() bool { return p.held.started("build", 1) == 1 })
	ratified := p.last(1)
	drain463Control(t, p.f)()
	clone := Clone{Dir: filepath.Join(t.TempDir(), "clone"), Remote: p.origin, Name: "Fixture", Email: "fixture@example.invalid"}
	if err := clone.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	reader := &drain463LeaseReader{Repo: clone}
	next := &Factory{Repository: p.f.Repository, GitHub: h, Repo: reader, Formalizer: p.f.Formalizer,
		Builder: p.f.Builder, Plumbing: p.f.Plumbing, Base: p.f.Base, Projects: p.f.Projects, Work: t.TempDir(),
		Check: p.f.Check, Holder: "new-463", LeaseFor: 5 * time.Minute, Parallel: 1, Now: now, Log: t.Logf}
	successor := drain463Start(t, next, 20*time.Millisecond)
	drain463Soon(t, "the successor waiting for the lease", func() bool { return reader.reads.Load() >= 2 })
	// Cross the original expiry while never letting either clock approach
	// the current renewed expiry. No wall-clock sleeps for a lease duration.
	for range 2 {
		nanos.Add(int64(3 * time.Minute))
		drain463Soon(t, "renewal during drain", func() bool {
			_, rec, err := clone.Lease(context.Background())
			return err == nil && rec.Holder == p.f.Holder && rec.Until.After(now().Add(4*time.Minute))
		})
		old.stillRunning(t)
		if steps := next.Steps(); len(steps) != 0 {
			t.Fatalf("successor acted while the old build still held the lease: %+v", steps)
		}
	}
	p.held.let(1)
	if err := old.wait(t); err != nil {
		t.Fatalf("draining old watcher: %v", err)
	}
	pr := p.expect(1, KindPR, LabelPR).Marker.PR
	state, result, err := clone.Run(context.Background(), 1, buildStepName(ratified))
	if err != nil || state != RunDone || result == "" {
		t.Fatalf("successor cannot read the completed build: %v, %q, %v", state, result, err)
	}
	// Freeze the clock: taking the lease now cannot depend on expiry.
	drain463Soon(t, "successor acquiring the released lease", func() bool {
		_, rec, err := clone.Lease(context.Background())
		return err == nil && rec.Holder == next.Holder
	})
	p.ci(pr)
	drain463Soon(t, "successor merging the completed build", func() bool { return p.last(1).Marker.Kind == KindMerged })
	if p.held.started("build", 1) != 1 {
		t.Fatal("successor rebuilt work the draining watcher had finished")
	}
	state, after, err := clone.Run(context.Background(), 1, buildStepName(ratified))
	if err != nil || state != RunDone || after != result {
		t.Fatalf("handoff replaced the build's saved result: %v, %q, %v", state, after, err)
	}
	p.mu.Lock()
	merges := append([]int(nil), p.gh.merged...)
	p.mu.Unlock()
	if len(merges) != 1 || merges[0] != pr {
		t.Fatalf("merges = %v; want the build merged exactly once", merges)
	}
	successor.cancel()
	if err := successor.wait(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceling the successor returned %v", err)
	}
}

func TestDrainWakesIdleAndWaitingWatchers463(t *testing.T) {
	drain463Control(t, &Factory{})
	for _, mode := range []string{"idle-holder", "without-lease", "before-watch", "waiting-for-lease"} {
		t.Run(mode, func(t *testing.T) {
			p, _ := slots455Rig(t, 1)
			p.f.Holder, p.f.LeaseFor = "idle-463", 5*time.Minute
			reader := &drain463LeaseReader{Repo: p.clone}
			p.f.Repo = reader
			var beats atomic.Int64
			p.f.Activity = func([]Step) { beats.Add(1) }
			var foreign string
			if mode == "without-lease" {
				p.f.LeaseFor = 0
			}
			if mode == "waiting-for-lease" {
				var err error
				foreign, err = p.clone.PushLease(context.Background(), "", LeaseRecord{Holder: "other-463", Until: time.Now().Add(time.Hour)})
				if err != nil {
					t.Fatal(err)
				}
			}
			drain := drain463Control(t, p.f)
			if mode == "before-watch" {
				drain()
			}
			w := drain463Start(t, p.f, time.Hour)
			if mode == "waiting-for-lease" {
				drain463Soon(t, "the initial foreign lease read", func() bool { return reader.reads.Load() > 0 })
			} else if mode != "before-watch" {
				drain463Soon(t, "the idle watcher's first poll", func() bool { return beats.Load() > 0 })
			}
			drain()
			drain()
			if err := w.wait(t); err != nil {
				t.Fatalf("idle drain: %v", err)
			}
			sha, rec, err := p.clone.Lease(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if foreign != "" {
				if sha != foreign || rec.Holder != "other-463" {
					t.Fatalf("a waiting watcher changed another holder's lease: %s %+v", sha, rec)
				}
			} else if rec.Holder != "" {
				t.Fatalf("idle drain retained its lease: %+v", rec)
			}
		})
	}
}

func TestCancellationStillStopsADrainingWatcher463(t *testing.T) {
	drain463Control(t, &Factory{})
	p, h := slots455Rig(t, 1)
	p.f.Holder, p.f.LeaseFor = "canceled-drain-463", 5*time.Minute
	p.held.holdOpen(1)
	p.open(1)
	w := drain463Start(t, p.f, 20*time.Millisecond)
	drain463Soon(t, "an active draft", func() bool { return p.held.started("draft", 1) == 1 })
	p.open(2)
	drain463Soon(t, "a queued draft", func() bool { return h.readCount(2) > 0 })
	drain463Control(t, p.f)()
	w.cancel()
	if err := w.wait(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation during drain returned %v", err)
	}
	if len(p.f.Steps()) != 0 || p.held.started("draft", 2) != 0 {
		t.Fatal("cancellation left steps behind or started the waiting agent")
	}
	if active, _ := p.held.going(); active != 0 {
		t.Fatal("cancellation failed to stop the active agent")
	}
	slots455NoRun(t, p, 2, "draft-issue")
	_, rec, err := p.clone.Lease(context.Background())
	if err != nil || rec.Holder != "" {
		t.Fatalf("cancellation during drain left a lease: %+v, %v", rec, err)
	}
}
