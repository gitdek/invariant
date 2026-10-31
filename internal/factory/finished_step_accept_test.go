package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
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
	t        *testing.T
	mu       sync.Mutex
	active   map[string]finished269Step
	ended    []finished269Step
	last     []finished269Step
	removed  map[string]bool
	finished map[string]bool
}

func finished269Observe(t *testing.T, f *Factory) *finished269Observer {
	t.Helper()
	o := &finished269Observer{t: t, active: map[string]finished269Step{}, removed: map[string]bool{}, finished: map[string]bool{}}
	// Finished is additive: Activity and Steps retain their running-only
	// contract. Reflection keeps the base compilable and reports the missing
	// completion channel as a behavioral failure.
	field := reflect.ValueOf(f).Elem().FieldByName("Finished")
	if !field.IsValid() || !field.CanSet() || field.Type() != reflect.TypeOf((func(Step))(nil)) {
		t.Fatal("watcher has no Finished func(Step): completions must be separate from running Activity snapshots")
	}
	field.Set(reflect.ValueOf(func(value Step) {
		o.mu.Lock()
		defer o.mu.Unlock()
		b, err := json.Marshal(value)
		if err != nil {
			t.Errorf("marshal watcher completion: %v", err)
			return
		}
		var step finished269Step
		if err := json.Unmarshal(b, &step); err != nil {
			t.Errorf("decode watcher completion: %v", err)
			return
		}
		if step.Run == "" || step.Ended == nil {
			t.Errorf("completion lacks its agent run or recorded end: %+v", step)
			return
		}
		key := fmt.Sprintf("%d/%s", step.Issue, step.Run)
		before, ok := o.active[key]
		if !ok || o.removed[key] {
			t.Errorf("completion did not precede removal of its running step: %+v", step)
		}
		if before.Issue != step.Issue || before.Run != step.Run || before.Doing != step.Doing || before.Agent != step.Agent || !before.Since.Equal(step.Since) {
			t.Errorf("completion changed its step identity: before %+v; ended %+v", before, step)
		}
		if o.finished[key] {
			t.Errorf("watcher completed the same run twice: %+v", step)
		}
		o.finished[key] = true
		o.ended = append(o.ended, step)
	}))
	activity := f.Activity
	f.Activity = func(steps []Step) {
		if activity != nil {
			activity(steps)
		}
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
			if step.Ended != nil {
				t.Errorf("Activity contains a finished record instead of only running steps: %+v", step)
			}
			if step.Run == "" {
				continue
			}
			key := fmt.Sprintf("%d/%s", step.Issue, step.Run)
			present[key] = true
			if o.finished[key] {
				t.Errorf("Activity carried a completed run into a later snapshot: %+v", step)
			}
			o.active[key] = step
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

func (o *finished269Observer) checkRuns(w *live236Watch, drafts, builds int) {
	o.t.Helper()
	w.check(drafts, builds)
	o.mu.Lock()
	ended := append([]finished269Step(nil), o.ended...)
	active := len(o.last)
	o.mu.Unlock()
	if len(ended) != len(w.calls) {
		o.t.Fatalf("watcher published %d completions for %d agent invocations", len(ended), len(w.calls))
	}
	for i, call := range w.calls {
		got := ended[i]
		if got.Issue != 1 || got.Doing != call.doing || got.Run != filepath.Base(call.out) || got.Agent == "" || !got.Since.Equal(w.r.f.Now()) || got.Ended == nil || !got.Ended.Equal(w.r.f.Now()) {
			o.t.Errorf("completion %d = %+v; want the original identity and clock of %s run %q", i, got, call.doing, call.out)
		}
	}
	if active != 0 || len(w.r.f.Steps()) != 0 {
		o.t.Error("finished records remain in Activity or Steps after the attempts ended")
	}
}

// Combine the unchanged #236 observers with completion recording. Each
// attempt is on the same issue and in the same second: retained finished
// metadata must never become a new step's run before its directory exists.
func TestFactoryKeepsFinishedRecordsSeparateFromRepeatedRuns269(t *testing.T) {
	t.Run("draft-answer-revision", func(t *testing.T) {
		r := newRig(t)
		w := live236Observe(t, r)
		o := finished269Observe(t, r.f)
		r.gh.open(1, "gitdek", "Add a bounded buffer", "A buffer.\n\n/invariant solve")
		r.poll()
		r.expect(1, KindForks, LabelAsking)
		o.checkRuns(w, 1, 0)
		r.gh.say(1, "gitdek", "/invariant choose F1 A")
		r.poll()
		r.expect(1, KindProposal, LabelProposal)
		o.checkRuns(w, 2, 0)
		r.gh.say(1, "gitdek", "/invariant revise")
		r.poll()
		r.expect(1, KindProposal, LabelProposal)
		o.checkRuns(w, 3, 0)
	})
	t.Run("modeled-retry", func(t *testing.T) {
		r := newRig(t)
		w := live236Observe(t, r)
		o := finished269Observe(t, r.f)
		r.build.stop = true
		stopped := ratified(t, r)
		if stopped.Marker.Kind != KindFailed || stopped.Marker.Failure != FailStopped {
			t.Fatalf("first build should stop: %+v", stopped.Marker)
		}
		o.checkRuns(w, 1, 1)
		r.build.stop = false
		r.gh.say(1, "gitdek", "/invariant retry")
		r.poll()
		r.expect(1, KindRatified, LabelBuilding)
		o.checkRuns(w, 1, 1)
		r.poll()
		r.expect(1, KindPR, LabelPR)
		o.checkRuns(w, 1, 2)
	})
	t.Run("plumbing-retry", func(t *testing.T) {
		r, build := plumbingRig(t, "page/page.go")
		w := live236Observe(t, r)
		o := finished269Observe(t, r.f)
		planner := &live236PlanBuilder{PlanBuilder: build, watch: w, stop: true}
		r.f.Plumbing = planner
		r.gh.open(1, "gitdek", "Add a hello page", "A page that says hello.\n\nKind: plumbing\n\n/invariant solve")
		r.poll()
		proposal := r.expect(1, KindProposal, LabelProposal)
		r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(proposal.Marker.Proposal.Hash, "sha256:"))
		r.poll()
		stopped := r.expect(1, KindFailed, LabelHumanReview)
		if stopped.Marker.Failure != FailStopped {
			t.Fatalf("first plan build should stop: %+v", stopped.Marker)
		}
		o.checkRuns(w, 1, 1)
		planner.stop = false
		r.gh.say(1, "gitdek", "/invariant retry")
		r.poll()
		r.expect(1, KindRatified, LabelBuilding)
		o.checkRuns(w, 1, 1)
		r.poll()
		r.expect(1, KindPR, LabelPR)
		o.checkRuns(w, 1, 2)
	})
}
