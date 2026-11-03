package factory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

// Publication happens after the operation releases its agent slot. Drain
// must keep polling and holding the lease until that step finishes too.
type drainPublicationHub struct {
	GitHub
	kind           string
	arrived, allow chan struct{}
	once           sync.Once
}

func (h *drainPublicationHub) PostComment(ctx context.Context, n int, body string) (github.Comment, error) {
	if m, ok := DecodeMarker(body); n == 1 && ok && m.Kind == h.kind {
		h.once.Do(func() { close(h.arrived) })
		select {
		case <-h.allow:
		case <-ctx.Done():
			return github.Comment{}, ctx.Err()
		}
	}
	return h.GitHub.PostComment(ctx, n, body)
}

func TestDrainKeepsPollingThroughPublication(t *testing.T) {
	for _, work := range []string{"draft", "modeled-build", "plumbing-build"} {
		t.Run(work, func(t *testing.T) {
			p, _ := slots455Rig(t, 1)
			kind, label, step := KindProposal, LabelProposal, "draft-issue"
			if work == "draft" {
				p.open(1)
			} else {
				body := slots455Model
				if work == "plumbing-build" {
					body = slots455Plumbing
				}
				p.say(1, slots455Proposal(t, p, 1, body))
				kind, label = KindPR, LabelPR
			}
			h := &drainPublicationHub{GitHub: p.f.GitHub, kind: kind, arrived: make(chan struct{}), allow: make(chan struct{})}
			release := sync.OnceFunc(func() { close(h.allow) })
			t.Cleanup(release)
			p.f.GitHub = h
			p.f.Holder, p.f.LeaseFor = "publishing-drain", 5*time.Minute
			w := drain463Start(t, p.f, 20*time.Millisecond)
			select {
			case <-h.arrived:
			case <-time.After(15 * time.Second):
				t.Fatal("step did not reach publication")
			}
			if work != "draft" {
				step = buildStepName(p.last(1))
			}
			if state, _, err := p.clone.Run(context.Background(), 1, step); err != nil || state != RunDone {
				t.Fatalf("publication needs a completed run: %v, %v", state, err)
			}
			p.f.Drain()
			p.open(2)
			slots455Open(p, 3, "An issue without a proposal.")
			command := p.say(3, "/invariant ratify ffffffffffff")
			polls := p.polls()
			drain463Soon(t, "commands and polling during publication", func() bool {
				return slots455Replies(p, 3, command.ID) == 1 && p.polls() >= polls+3
			})
			w.stillRunning(t)
			slots455NoRun(t, p, 2, "draft-issue")
			if p.held.started("draft", 2) != 0 || len(p.posts(2)) != 0 {
				t.Fatal("a free slot admitted a draft after drain")
			}
			_, lease, err := p.clone.Lease(context.Background())
			if err != nil || lease.Holder != p.f.Holder {
				t.Fatalf("publication lost its lease: %+v, %v", lease, err)
			}
			release()
			if err := w.wait(t); err != nil {
				t.Fatal(err)
			}
			p.expect(1, kind, label)
		})
	}
}

func TestDrainWakesAfterFinalDraft(t *testing.T) {
	for _, body := range []string{slots455Model, slots455Plumbing, "Plan a decision graph.\n\n/invariant plan"} {
		t.Run(body, func(t *testing.T) {
			p, _ := slots455Rig(t, 1)
			p.held.holdOpen(1)
			slots455Open(p, 1, body)
			w := drain463Start(t, p.f, time.Hour)
			drain463Soon(t, "the active draft", func() bool { return p.held.started("draft", 1) == 1 })
			p.f.Drain()
			p.held.let(1)
			if err := w.wait(t); err != nil {
				t.Fatal(err)
			}
			p.expect(1, KindProposal, LabelProposal)
			if state, _, err := p.clone.Run(context.Background(), 1, "draft-issue"); err != nil || state != RunDone {
				t.Fatalf("drain lost its draft: %v, %v", state, err)
			}
		})
	}
}

func TestDrainDoesNotWaitForActivity(t *testing.T) {
	p, _ := slots455Rig(t, 1)
	arrived, allow := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := sync.OnceFunc(func() { close(allow) })
	p.f.Activity = func([]Step) {
		once.Do(func() { close(arrived) })
		<-allow
	}
	w := drain463Start(t, p.f, time.Hour)
	t.Cleanup(release)
	select {
	case <-arrived:
	case <-time.After(15 * time.Second):
		t.Fatal("heartbeat did not start")
	}
	done := make(chan struct{})
	go func() {
		var calls sync.WaitGroup
		for range 8 {
			calls.Go(p.f.Drain)
		}
		calls.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Drain waited for an activity callback")
	}
	w.stillRunning(t)
	release()
	if err := w.wait(t); err != nil {
		t.Fatal(err)
	}
}
