package factory

import (
	"context"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
)

// turn677 is one agent turn, as Codex's is: a run waits for it, checking
// that its work is still wanted, and checks once more when it comes, before
// its agent starts.
type turn677 chan struct{}

func (turn turn677) take(ctx context.Context) (func(), error) {
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case turn <- struct{}{}:
			if err := synth.StillWanted(ctx); err != nil {
				<-turn
				return nil, err
			}
			return func() { <-turn }, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
			if err := synth.StillWanted(ctx); err != nil {
				return nil, err
			}
		}
	}
}

type turnFormalizer677 struct {
	Formalizer
	turn turn677
}

func (f turnFormalizer677) Formalize(ctx context.Context, req formalize.Request, out string) (*formalize.Result, error) {
	release, err := f.turn.take(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return f.Formalizer.Formalize(ctx, req, out)
}

type turnPlanBuilder677 struct {
	PlanBuilder
	turn turn677
}

func (b turnPlanBuilder677) Build(ctx context.Context, root string, n int, out string) (*plumbing.BuildResult, error) {
	release, err := b.turn.take(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return b.PlanBuilder.Build(ctx, root, n, out)
}

func stepping677(f *Factory, n int) bool {
	for _, s := range f.Steps() {
		if s.Issue == n {
			return true
		}
	}
	return false
}

// A step admitted while its issue is open, whose issue closes while another
// run holds the agent's turn, never starts its agent and posts no draft or
// build. It gives its slot and turn back, and the next issue's step starts as
// before (#677).
func TestClosedIssueNeverStartsItsWaitingAgent677(t *testing.T) {
	for _, kind := range []string{"draft", "plan-build"} {
		t.Run(kind, func(t *testing.T) {
			p, _ := slots455Rig(t, 2)
			turn := make(turn677, 1)
			p.f.Formalizer = turnFormalizer677{p.f.Formalizer, turn}
			p.f.Plumbing = turnPlanBuilder677{p.f.Plumbing, turn}
			const waiting = 10
			var ratify string
			if kind == "plan-build" {
				ratify = slots455Proposal(t, p, waiting, slots455Plumbing)
			}
			// #1's draft takes the turn and holds it.
			p.held.holdOpen(1)
			p.open(1)
			slots455Returned(t, p.pollAsync())
			slots455Soon(t, "#1's draft to take the turn", func() bool { return p.held.started("draft", 1) == 1 })
			// #10's run is admitted, recorded, and waits for the turn.
			step := "draft-issue"
			if ratify != "" {
				p.say(waiting, ratify)
			} else {
				slots455Open(p, waiting, slots455Model)
			}
			slots455Returned(t, p.pollAsync())
			if ratify != "" {
				slots455Soon(t, "#10's ratification to be recorded", func() bool { return p.last(waiting).Marker.Kind == KindRatified })
				step = buildStepName(p.last(waiting))
			}
			slots455Soon(t, "#10's run to be recorded", func() bool {
				state, _, err := p.clone.Run(p.ctx, waiting, step)
				return err == nil && state == RunRecorded
			})
			p.shut(waiting)
			slots455Soon(t, "#10's step to end while #1 holds the turn", func() bool { return !stepping677(p.f, waiting) })
			if p.held.started(kind, waiting) != 0 {
				t.Fatalf("#10's %s agent started after its issue closed", kind)
			}
			for _, post := range p.posts(waiting) {
				switch post.Marker.Kind {
				case KindProposal, KindForks, KindPR, KindFailed, KindStuck:
					if kind == "draft" || post.Marker.Kind != KindProposal {
						t.Fatalf("#10 got a %s post after its issue closed:\n%s", post.Marker.Kind, post.Comment.Body)
					}
				}
			}
			if ratify != "" && !p.last(waiting).Marker.IssueClosed {
				t.Fatalf("#10's close wasn't recorded: %+v", p.last(waiting).Marker)
			}
			// #1's draft goes on, and the next issue's draft gets the slot
			// and the turn #10 gave back.
			p.held.let(1)
			p.open(3)
			slots455Returned(t, p.pollAsync())
			slots455Wait(t, p.f)
			p.expect(1, KindProposal, LabelProposal)
			p.expect(3, KindProposal, LabelProposal)
		})
	}
}

// A step queued for capacity ends when its issue closes, without waiting for
// a slot first. It records no run, and the step holding the slot goes on
// (#677).
func TestQueuedStepEndsWhenItsIssueCloses677(t *testing.T) {
	p, h := slots455Rig(t, 1)
	p.f.recheck = 5 * time.Millisecond
	slots455Fill(t, p, 1)
	const queued = 10
	reads := h.readCount(queued)
	slots455Open(p, queued, slots455Model)
	slots455Returned(t, p.pollAsync())
	slots455Soon(t, "#10 to queue for a slot", func() bool { return stepping677(p.f, queued) && h.readCount(queued) > reads })
	p.shut(queued)
	slots455Soon(t, "#10's queued step to end without a slot", func() bool { return !stepping677(p.f, queued) })
	slots455NoRun(t, p, queued, "draft-issue")
	if p.held.started("draft", queued) != 0 {
		t.Fatal("#10's draft started after its issue closed")
	}
	if now, _ := p.held.going(); now != 1 {
		t.Fatalf("%d runs going; want #1's, still holding the slot", now)
	}
	p.held.let(1)
	slots455Wait(t, p.f)
	p.expect(1, KindProposal, LabelProposal)
}
