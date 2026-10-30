package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
)

// Decode the new completion field through JSON so the acceptance tests
// compile before the watcher publishes it, and fail on missing behavior.
type finished269Step struct {
	Issue int
	Doing string
	Since time.Time
	Agent string
	Run   string
	Ended *time.Time
}

type finished269Clock struct {
	mu sync.Mutex
	at time.Time
}

func (c *finished269Clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *finished269Clock) set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = at
}

type finished269Observer struct {
	t       *testing.T
	mu      sync.Mutex
	active  map[string]finished269Step
	ended   []finished269Step
	last    []finished269Step
	removed map[string]bool
}

func finished269Observe(t *testing.T, f *Factory) *finished269Observer {
	t.Helper()
	o := &finished269Observer{t: t, active: map[string]finished269Step{}, removed: map[string]bool{}}
	f.Activity = func(steps []Step) {
		o.mu.Lock()
		defer o.mu.Unlock()
		b, err := json.Marshal(steps)
		if err != nil {
			t.Errorf("marshal watcher activity: %v", err)
			return
		}
		var snapshot []finished269Step
		if err := json.Unmarshal(b, &snapshot); err != nil {
			t.Errorf("decode watcher activity: %v", err)
			return
		}
		present := map[string]bool{}
		for _, step := range snapshot {
			if step.Run == "" {
				if step.Ended != nil {
					t.Errorf("a step with no agent run published an end: %+v", step)
				}
				continue
			}
			key := fmt.Sprintf("%d/%s", step.Issue, step.Run)
			present[key] = true
			if step.Ended == nil {
				o.active[key] = step
				continue
			}
			before, ok := o.active[key]
			if !ok || o.removed[key] {
				t.Errorf("completion did not precede removal of its running step: %+v", step)
			}
			if before.Issue != step.Issue || before.Run != step.Run || before.Doing != step.Doing || before.Agent != step.Agent || !before.Since.Equal(step.Since) {
				t.Errorf("completion changed its step identity: before %+v; ended %+v", before, step)
			}
			o.ended = append(o.ended, step)
		}
		for key := range o.active {
			if !present[key] {
				o.removed[key] = true
			}
		}
		o.last = snapshot
	}
	return o
}

func (o *finished269Observer) check(f *Factory, doing string, since, ended time.Time) {
	o.t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.ended) != 1 {
		o.t.Fatalf("watcher published %d completed runs; want one %s run with its actual end time", len(o.ended), doing)
	}
	got := o.ended[0]
	if got.Issue != 1 || got.Doing != doing || got.Run == "" || got.Agent == "" || !got.Since.Equal(since) || got.Ended == nil || !got.Ended.Equal(ended) {
		o.t.Errorf("completed step = %+v; want #1 %s from %s until %s with its run and agent", got, doing, since, ended)
	}
	if len(o.last) != 0 {
		o.t.Errorf("last activity still lists completed steps: %+v", o.last)
	}
	if got := f.Steps(); len(got) != 0 {
		o.t.Errorf("Steps still lists completed steps: %+v", got)
	}
}

type finished269Formalizer struct {
	Formalizer
	clock *finished269Clock
	end   time.Time
}

func (a finished269Formalizer) Formalize(ctx context.Context, req formalize.Request, out string) (*formalize.Result, error) {
	a.clock.set(a.end)
	return a.Formalizer.Formalize(ctx, req, out)
}

// The end is recorded by the watcher while the step ends, without any
// dashboard read. A later poll doing no agent work cannot invent another.
func TestFactoryPublishesTheDraftsActualEndTime(t *testing.T) {
	r := newRig(t)
	r.form.forks = nil
	start := time.Date(2026, 10, 30, 0, 21, 0, 123456789, time.FixedZone("watcher", -4*60*60))
	end := start.Add(24 * time.Minute)
	clock := &finished269Clock{at: start}
	r.f.Now = clock.now
	r.f.Formalizer = finished269Formalizer{r.f.Formalizer, clock, end}
	o := finished269Observe(t, r.f)
	r.gh.open(1, "gitdek", "Add a bounded buffer", "A buffer.\n\n/invariant solve")
	r.poll()
	r.expect(1, KindProposal, LabelProposal)
	o.check(r.f, "formalizing", start, end)
	clock.set(end.Add(7 * time.Hour))
	r.poll()
	o.check(r.f, "formalizing", start, end)
}

type finished269Builder struct {
	Builder
	clock  *finished269Clock
	end    time.Time
	mode   string
	cancel context.CancelFunc
}

func (a finished269Builder) Build(ctx context.Context, dir, out string, amend bool) (*synth.Result, error) {
	a.clock.set(a.end)
	switch a.mode {
	case "error":
		return nil, errors.New("the build agent stopped")
	case "panic":
		panic("the build agent panicked")
	case "cancel":
		a.cancel()
		return nil, ctx.Err()
	}
	return a.Builder.Build(ctx, dir, out, amend)
}

// Both scheduling paths must publish a terminal step: normal completion,
// an agent error, recovered panic, and context cancellation all reach it.
func TestFactoryPublishesEveryModeledBuildEnd(t *testing.T) {
	for _, parallel := range []int{1, 2} {
		for _, mode := range []string{"success", "error", "panic", "cancel"} {
			t.Run(fmt.Sprintf("parallel-%d/%s", parallel, mode), func(t *testing.T) {
				r := newRig(t)
				r.form.forks = nil
				r.gh.open(1, "gitdek", "Add a bounded buffer", "A buffer.\n\n/invariant solve")
				r.poll()
				proposal := r.expect(1, KindProposal, LabelProposal)
				r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(proposal.Marker.Proposal.Hash, "sha256:"))
				start := time.Date(2026, 10, 30, 0, 21, 0, 123456789, time.UTC)
				end := start.Add(24 * time.Minute)
				clock := &finished269Clock{at: start}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				r.f.Now, r.f.Parallel = clock.now, parallel
				r.f.Builder = finished269Builder{r.f.Builder, clock, end, mode, cancel}
				o := finished269Observe(t, r.f)
				err := r.f.Poll(ctx)
				r.f.Wait()
				if mode == "success" && err != nil {
					t.Fatalf("successful build: %v", err)
				}
				o.check(r.f, "building", start, end)
			})
		}
	}
}

type finished269PlanBuilder struct {
	PlanBuilder
	clock  *finished269Clock
	end    time.Time
	mode   string
	cancel context.CancelFunc
}

func (a finished269PlanBuilder) Build(ctx context.Context, root string, issue int, out string) (*plumbing.BuildResult, error) {
	a.clock.set(a.end)
	switch a.mode {
	case "error":
		return nil, errors.New("the plan's build agent stopped")
	case "panic":
		panic("the plan's build agent panicked")
	case "cancel":
		a.cancel()
		return nil, ctx.Err()
	}
	return a.PlanBuilder.Build(ctx, root, issue, out)
}

// Plumbing runs through a separate builder, including its review, and
// must retain an end time on the same ordinary and exceptional paths.
func TestFactoryPublishesEveryPlumbingBuildEnd(t *testing.T) {
	for _, mode := range []string{"success", "error", "panic", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			r, builder := plumbingRig(t, "page/page.go")
			r.gh.open(1, "gitdek", "Add a hello page", "A page that says hello.\n\nKind: plumbing\n\n/invariant solve")
			r.poll()
			proposal := r.expect(1, KindProposal, LabelProposal)
			r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(proposal.Marker.Proposal.Hash, "sha256:"))
			start := time.Date(2026, 10, 30, 0, 21, 0, 123456789, time.UTC)
			end := start.Add(24 * time.Minute)
			clock := &finished269Clock{at: start}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r.f.Now = clock.now
			r.f.Plumbing = finished269PlanBuilder{builder, clock, end, mode, cancel}
			o := finished269Observe(t, r.f)
			err := r.f.Poll(ctx)
			r.f.Wait()
			if mode == "success" && err != nil {
				t.Fatalf("successful plumbing build: %v", err)
			}
			o.check(r.f, "building", start, end)
		})
	}
}
