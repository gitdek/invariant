package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
)

// A run is an opaque directory basename in the watcher's status, not a
// timestamp the dashboard interprets. Read the added field through JSON so
// these tests compile before Step gains it, and fail on the missing behavior.
type live236Event struct {
	step Step
	run  string
	dirs map[string]bool
}

type live236Call struct {
	doing, out, transcript string
}

type live236Watch struct {
	t         *testing.T
	r         *rig
	events    []live236Event
	nextEvent int
	calls     []live236Call
}

func live236Observe(t *testing.T, r *rig) *live236Watch {
	t.Helper()
	w := &live236Watch{t: t, r: r}
	// Every draft, answer, revision and retry below starts in the same
	// second, in a timezone different from the dashboard's UTC status.
	now := time.Date(2026, 10, 30, 9, 15, 12, 123000000, time.FixedZone("watcher", 9*60*60))
	r.f.Now = func() time.Time { return now }
	r.f.Activity = func(steps []Step) {
		for _, step := range steps {
			if step.Issue != 1 {
				continue
			}
			b, err := json.Marshal(step)
			if err != nil {
				t.Fatal(err)
			}
			var status struct{ Run string }
			if err := json.Unmarshal(b, &status); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(r.f.Work, "issue-1")
			entries, err := os.ReadDir(root)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			dirs := map[string]bool{}
			for _, entry := range entries {
				if entry.IsDir() {
					dirs[entry.Name()] = true
				}
			}
			if status.Run != "" {
				if filepath.Base(status.Run) != status.Run || status.Run == "." || status.Run == ".." {
					t.Errorf("published run %q is not a directory basename", status.Run)
				}
				if !dirs[status.Run] {
					t.Errorf("published run %q before its directory existed", status.Run)
				}
			}
			w.events = append(w.events, live236Event{step: step, run: status.Run, dirs: dirs})
		}
	}
	r.f.Formalizer = live236Formalizer{r.f.Formalizer, w}
	r.f.Builder = live236Builder{r.f.Builder, w}
	return w
}

// starting runs inside the fake agent, before it writes any transcript.
// Both the most recent Activity notification and the directory must already
// identify this exact invocation. Its first notification must precede that
// directory, even when an earlier run's transcript is still present.
func (w *live236Watch) starting(doing, out string) {
	w.t.Helper()
	name := filepath.Base(out)
	want := filepath.Join(w.r.f.Work, "issue-1", name)
	if filepath.Clean(out) != want {
		w.t.Errorf("agent output %q is outside its issue's run directory %q", out, want)
	}
	var current []live236Event
	for _, event := range w.events[w.nextEvent:] {
		if event.step.Doing == doing {
			current = append(current, event)
		}
	}
	w.nextEvent = len(w.events)
	if len(current) == 0 {
		w.t.Errorf("%s agent started without an Activity notification", doing)
	} else {
		first := current[0]
		if first.run != "" {
			w.t.Errorf("%s began with run %q; want no run until its new directory exists", doing, first.run)
		}
		if first.dirs[name] {
			w.t.Errorf("%s output %q already existed at its first status notification; a new run must get a fresh directory", doing, out)
		}
		for _, event := range current {
			if event.run != "" && event.run != name {
				w.t.Errorf("%s published run %q for output %q", doing, event.run, out)
			}
		}
		last := current[len(current)-1]
		if last.run != name {
			w.t.Errorf("%s agent started with published run %q; want output directory basename %q", doing, last.run, name)
		}
	}
	if info, err := os.Stat(out); err != nil || !info.IsDir() {
		w.t.Errorf("%s agent started before output directory %q existed: %v", doing, out, err)
	}
	for _, earlier := range w.calls {
		if earlier.out == out {
			w.t.Errorf("%s reused earlier run directory %q within the same second", doing, out)
		}
	}
	// Real agents write their transcripts here. Give each invocation a
	// distinct marker so the final check detects overwriting or deletion.
	if err := os.MkdirAll(out, 0o755); err != nil {
		w.t.Fatal(err)
	}
	transcript := fmt.Sprintf("{\"test_run\":%d}\n", len(w.calls)+1)
	if err := os.WriteFile(filepath.Join(out, "transcript.jsonl"), []byte(transcript), 0o600); err != nil {
		w.t.Fatal(err)
	}
	w.calls = append(w.calls, live236Call{doing: doing, out: out, transcript: transcript})
}

func (w *live236Watch) check(drafts, builds int) {
	w.t.Helper()
	counts := map[string]int{}
	for _, call := range w.calls {
		counts[call.doing]++
		got, err := os.ReadFile(filepath.Join(call.out, "transcript.jsonl"))
		if err != nil || string(got) != call.transcript {
			w.t.Errorf("run %q lost its own transcript: got %q, %v; want %q", call.out, got, err, call.transcript)
		}
	}
	if counts["formalizing"] != drafts || counts["building"] != builds {
		w.t.Errorf("observed %d drafts and %d builds; want %d drafts and %d builds", counts["formalizing"], counts["building"], drafts, builds)
	}
}

type live236Formalizer struct {
	Formalizer
	watch *live236Watch
}

func (a live236Formalizer) Formalize(ctx context.Context, req formalize.Request, out string) (*formalize.Result, error) {
	a.watch.starting("formalizing", out)
	return a.Formalizer.Formalize(ctx, req, out)
}

type live236Builder struct {
	Builder
	watch *live236Watch
}

func (a live236Builder) Build(ctx context.Context, dir, out string, amend bool) (*synth.Result, error) {
	a.watch.starting("building", out)
	return a.Builder.Build(ctx, dir, out, amend)
}

type live236PlanBuilder struct {
	PlanBuilder
	watch *live236Watch
	stop  bool
}

func (a *live236PlanBuilder) Build(ctx context.Context, root string, issue int, out string) (*plumbing.BuildResult, error) {
	a.watch.starting("building", out)
	if a.stop {
		return nil, errors.New("the plan's agent stopped before finishing")
	}
	return a.PlanBuilder.Build(ctx, root, issue, out)
}

// A first draft, the draft after a fork's answer, and an explicit revision
// each publish a fresh run, even though the watcher's clock has not moved.
func TestFactoryPublishesExplicitRunBeforeEveryDraft(t *testing.T) {
	r := newRig(t)
	w := live236Observe(t, r)
	r.gh.open(1, "gitdek", "Add a bounded buffer", "A buffer.\n\n/invariant solve")
	r.poll()
	r.expect(1, KindForks, LabelAsking)
	r.gh.say(1, "gitdek", "/invariant choose F1 A")
	r.poll()
	r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant revise")
	r.poll()
	r.expect(1, KindProposal, LabelProposal)
	w.check(3, 0)
}

// A stopped modeled build and its retry have distinct output directories
// and status identities, while the first build's transcript remains intact.
func TestFactoryRetryUsesFreshRunForModeledBuild(t *testing.T) {
	r := newRig(t)
	w := live236Observe(t, r)
	r.build.stop = true
	stopped := ratified(t, r)
	if stopped.Marker.Kind != KindFailed || stopped.Marker.Failure != FailStopped {
		t.Fatalf("first build should stop: %+v", stopped.Marker)
	}
	r.build.stop = false
	r.gh.say(1, "gitdek", "/invariant retry")
	r.poll()
	r.expect(1, KindRatified, LabelBuilding)
	r.poll()
	r.expect(1, KindPR, LabelPR)
	w.check(1, 2)
}

// Plumbing uses a different builder entry point; it must publish the same
// run contract and reserve a fresh directory for a retry in the same second.
func TestFactoryRetryUsesFreshRunForPlumbingBuild(t *testing.T) {
	r, build := plumbingRig(t, "page/page.go")
	w := live236Observe(t, r)
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
	planner.stop = false
	r.gh.say(1, "gitdek", "/invariant retry")
	r.poll()
	r.expect(1, KindRatified, LabelBuilding)
	r.poll()
	r.expect(1, KindPR, LabelPR)
	w.check(1, 2)
}
