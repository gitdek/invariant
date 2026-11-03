package factory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
)

// slotFailure fails the operation after heldRuns has let it go. Other
// issues can still draft, so the next waiting operation must get its slot.
type slotFailure struct {
	Formalizer
	panics bool
}

func (s slotFailure) fail() error {
	if s.panics {
		panic("agent panicked")
	}
	return errors.New("agent failed")
}

func (s slotFailure) Formalize(ctx context.Context, req formalize.Request, out string) (*formalize.Result, error) {
	if req.Issue == 10 {
		return nil, s.fail()
	}
	return s.Formalizer.Formalize(ctx, req, out)
}

type slotBuildFailure struct{ slotFailure }

func (s slotBuildFailure) Build(context.Context, string, string, bool) (*synth.Result, error) {
	return nil, s.fail()
}

type slotPlanFailure struct{ slotFailure }

func (s slotPlanFailure) Build(context.Context, string, int, string) (*plumbing.BuildResult, error) {
	return nil, s.fail()
}

func TestAgentSlotReleasedAfterErrorOrPanic455(t *testing.T) {
	for _, kind := range []string{"draft", "build", "plan-build"} {
		for _, outcome := range []string{"error", "panic"} {
			t.Run(kind+"/"+outcome, func(t *testing.T) {
				p, _ := slots455Rig(t, 1)
				if kind == "draft" {
					p.open(10)
				} else {
					body := slots455Model
					if kind == "plan-build" {
						body = slots455Plumbing
					}
					p.say(10, slots455Proposal(t, p, 10, body))
				}
				failure := slotFailure{Formalizer: p.form, panics: outcome == "panic"}
				p.f.Formalizer = heldFormalizer{failure, p.held}
				p.f.Builder = heldBuilder{slotBuildFailure{failure}, p.held}
				p.f.Plumbing = slots455PlanBuilder{slotPlanFailure{failure}, p.held}
				p.held.holdOpen(10)
				slots455Returned(t, p.pollAsync())
				slots455Soon(t, "the failing operation to start", func() bool { return p.held.started(kind, 10) == 1 })
				p.held.holdOpen(11)
				p.open(11)
				slots455Returned(t, p.pollAsync())
				p.held.let(10)
				slots455Soon(t, "the next draft to acquire the released slot", func() bool { return p.held.started("draft", 11) == 1 })
				p.held.let(11)
				slots455Wait(t, p.f)
				p.expect(11, KindProposal, LabelProposal)
				if now, most := p.held.going(); now != 0 || most != 1 {
					t.Fatalf("active=%d peak=%d; want the slot released without overlapping operations", now, most)
				}
			})
		}
	}
}

func TestQueuedAgentChecksLeaseTenure455(t *testing.T) {
	for _, change := range []string{"lost", "new-tenure"} {
		t.Run(change, func(t *testing.T) {
			p, h := slots455Rig(t, 1)
			p.f.Holder, p.f.LeaseFor = "watcher-a", 5*time.Minute
			if held, err := p.f.hold(p.ctx); err != nil || !held {
				t.Fatalf("hold = %v, %v", held, err)
			}
			p.f.GitHub, p.f.Repo = leasedGitHub{p.f.GitHub, p.f}, leasedRepo{p.f.Repo, p.f}
			slots455Fill(t, p, 1)
			p.open(10)
			slots455Returned(t, p.pollAsync())
			slots455Soon(t, "the waiting step to read its issue in the old tenure", func() bool { return h.readCount(10) > 0 })
			// Model the lease keeper observing a loss, or taking the lease
			// back from another watcher, while the agent slot is occupied.
			p.f.lease.mu.Lock()
			if change == "lost" {
				p.f.lease.until = time.Time{}
			} else {
				p.f.lease.tenure++
			}
			p.f.lease.mu.Unlock()
			p.held.letAll()
			slots455Wait(t, p.f)
			slots455NoRun(t, p, 10, "draft-issue")
			if p.held.started("draft", 10) != 0 || len(p.posts(10)) != 0 {
				t.Fatal("a queued step acted after its lease or tenure ended")
			}
		})
	}
}

func TestRecordedResultsNeedNoAgentSlot455(t *testing.T) {
	for _, kind := range []string{"draft", "build", "plan-build"} {
		t.Run(kind, func(t *testing.T) {
			p, _ := slots455Rig(t, 1)
			body := slots455Model
			if kind == "plan-build" {
				body = slots455Plumbing
			}
			command := slots455Proposal(t, p, 10, body)
			want := KindProposal
			var keep int64
			if kind != "draft" {
				p.say(10, command)
				p.settle()
				p.expect(10, KindPR, LabelPR)
				want = KindPR
				for _, post := range p.posts(10) {
					if post.Marker.Kind == KindRatified {
						keep = post.Comment.ID
					}
				}
			}
			slots455Fill(t, p, 1)
			slots455Soon(t, "the completed issue's step to end", func() bool {
				for _, step := range p.f.Steps() {
					if step.Issue == 10 {
						return false
					}
				}
				return true
			})
			// The result is durable, but its post was lost. Recover it
			// while another issue keeps the only agent slot occupied.
			p.mu.Lock()
			comments := p.gh.comments[10]
			for i, c := range comments {
				if c.ID > keep {
					p.gh.comments[10] = comments[:i]
					break
				}
			}
			p.mu.Unlock()
			slots455Returned(t, p.pollAsync())
			slots455Soon(t, "the recorded result to be posted with every slot full", func() bool { return p.last(10).Marker.Kind == want })
			if got := p.held.started(kind, 10); got != 1 {
				t.Fatalf("the recorded result started %d agents; want only its original run", got)
			}
		})
	}
}

func TestClosedQueuedBuildKeepsItsRatification455(t *testing.T) {
	for _, body := range []string{slots455Model, slots455Plumbing} {
		t.Run(kindLine(body), func(t *testing.T) {
			p, _ := slots455Rig(t, 1)
			command := slots455Proposal(t, p, 10, body)
			slots455Fill(t, p, 1)
			p.say(10, command)
			slots455Returned(t, p.pollAsync())
			slots455Soon(t, "the ratification before the slot wait", func() bool { return p.last(10).Marker.Kind == KindRatified })
			ratified := p.last(10)
			p.shut(10)
			p.held.letAll()
			slots455Wait(t, p.f)
			slots455NoRun(t, p, 10, buildStepName(ratified))
			last := p.last(10)
			if !last.Marker.IssueClosed || last.Marker.Hash != ratified.Marker.Hash {
				t.Fatalf("the closed build lost its ratification or closure: %+v", last.Marker)
			}
			if p.held.started("build", 10) != 0 || p.held.started("plan-build", 10) != 0 {
				t.Fatal("the closed issue started its queued build")
			}
		})
	}
}
