package verify

// #342's qualification, larger-bound, misuse, form and fingerprint cases,
// on the counter fixture in python_application_explore_accept_test.go.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/toolchain"
)

const pa342SampledDriver = `from invariant_observe import functional
from counter.core import increment
from counter import service


def drive():
    with functional(increment, service.start, argument='count', field='count') as life:
        life.step(service.inc)
        life.step(service.inc)


if __name__ == '__main__':
    drive()
`

// pa342Edit replaces old with new in an application file, which must hold it.
func pa342Edit(t *testing.T, f pa342Fixture, name, old, new string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(name)))
	if err != nil || !strings.Contains(string(b), old) {
		t.Fatalf("%s holds no %q: %v", name, old, err)
	}
	pa342Write(t, f.root, name, strings.Replace(string(b), old, new, 1))
}

// pa342Unchanged runs the gate and checks the application, driver included,
// is byte for byte what it was.
func pa342Unchanged(t *testing.T, f pa342Fixture, tc toolchain.Toolchain) *Report {
	t.Helper()
	before := pa342Tree(t, f.root)
	r := pa342Run(t, f, tc)
	if after := pa342Tree(t, f.root); !reflect.DeepEqual(before, after) {
		t.Fatal("the gate changed the application or its driver")
	}
	return r
}

func TestPythonApplicationGateExplorationQualifications(t *testing.T) {
	pa342Modes(t, func(t *testing.T, tc toolchain.Toolchain, real bool) {
		t.Run("a sampled run of the same states has no exhaustive label", func(t *testing.T) {
			f := pa342New(t, true, real)
			f.p.Manifest.Exhaustive = false
			pa342Save(t, f)
			pa342Write(t, f.root, pa342DriverPath, pa342SampledDriver)
			r := pa342Unchanged(t, f, tc)
			x, c := pa342Explored(t, r)
			row := pa342Row(t, r)
			if !r.Passed || x.ObservationMode != "sampled" || x.Exploration != nil || c.Mode != "" || c.Result.Exhaustive || c.Result.Tried != nil || c.Result.States != 3 {
				t.Fatalf("sampled run: passed=%v %+v %+v", r.Passed, x.Exploration, c)
			}
			if row.Result.Mode != "sampled" || row.ExhaustiveStates || row.EveryStep || !row.TransitionsChecked || r.Larger != nil || len(r.Application.Larger) != 0 {
				t.Fatalf("sampled run earned exhaustive or larger evidence: %+v %+v", row, r.Larger)
			}
		})
		t.Run("a step never tried fails the attempt check and keeps checked transitions", func(t *testing.T) {
			f := pa342New(t, true, real)
			pa342Edit(t, f, pa342DriverPath, "STEPS = [Step('Inc', inc), Step('Done', done)]", "STEPS = [Step('Inc', inc)]")
			r := pa342Unchanged(t, f, tc)
			_, c := pa342Explored(t, r)
			row := pa342Row(t, r)
			if r.Passed || !c.Result.Passed || c.Result.Tried == nil || c.Result.Tried.Passed || !strings.Contains(c.Result.Tried.Untried, `"Done"`) || c.passed() {
				t.Fatalf("missing step: passed=%v %+v", r.Passed, c.Result.Tried)
			}
			if !row.TransitionsChecked || row.EveryStep || row.ExhaustiveStates || row.Result.Attempts == nil || row.Result.Attempts.Outcome != "failed" {
				t.Fatalf("missing step qualifications: %+v", row)
			}
		})
		t.Run("a step the application refuses misses a state the model reaches", func(t *testing.T) {
			f := pa342New(t, true, real)
			pa342Write(t, f.root, pa342Source, "from .step import next_count\n\n\ndef increment(count):\n    if count >= 1:\n        return count\n    return next_count(count)\n")
			r := pa342Unchanged(t, f, tc)
			_, c := pa342Explored(t, r)
			row := pa342Row(t, r)
			if r.Passed || !c.Result.Passed || c.Result.States != 2 || c.Result.ModelStates != 3 || c.Result.Tried == nil || !c.Result.Tried.Passed || !strings.Contains(c.Result.Message, "2 of the model's 3") || c.passed() {
				t.Fatalf("refused step: passed=%v %+v", r.Passed, c.Result)
			}
			if !row.TransitionsChecked || !row.EveryStep || row.ExhaustiveStates {
				t.Fatalf("refused step qualifications: %+v", row)
			}
		})
		t.Run("a step left untried where more than the environment rules it out fails", func(t *testing.T) {
			f := pa342New(t, true, real)
			pa342Edit(t, f, pa342DriverPath, "if state['count'] >= Limit:", "if state['count'] >= 1:")
			r := pa342Unchanged(t, f, tc)
			x, c := pa342Explored(t, r)
			row := pa342Row(t, r)
			if r.Passed || x.Exploration == nil || x.Exploration.Omitted != 1 || c.Result.States != 2 || c.Result.Tried == nil || c.Result.Tried.Passed || !strings.Contains(c.Result.Tried.Untried, `"Inc"`) {
				t.Fatalf("omitted step: passed=%v %+v %+v", r.Passed, x.Exploration, c.Result)
			}
			if !row.TransitionsChecked || row.EveryStep || row.ExhaustiveStates {
				t.Fatalf("omitted step qualifications: %+v", row)
			}
		})
	})
}

func TestPythonApplicationGateExplorationLarger(t *testing.T) {
	pa342Modes(t, func(t *testing.T, tc toolchain.Toolchain, real bool) {
		t.Run("a size the application hard-codes fails one size larger", func(t *testing.T) {
			f := pa342New(t, true, real)
			pa342Write(t, f.root, pa342Source, "from .step import next_count\n\n\ndef increment(count):\n    if count < 2:\n        return next_count(count)\n    return count\n")
			r := pa342Unchanged(t, f, tc)
			_, c := pa342Explored(t, r)
			row := pa342Row(t, r)
			l := r.Larger
			if r.Passed || !c.passed() || l == nil || !l.Required || l.Passed || l.States != 3 || l.WantStates != 4 || !strings.Contains(l.Message, "one size larger") {
				t.Fatalf("hard-coded size: passed=%v larger=%+v", r.Passed, l)
			}
			// The ordinary component evidence stands at the ratified bounds.
			if !row.ExhaustiveStates || !row.EveryStep || row.Result.Bounds == nil || row.Result.Bounds.Values["Limit"] != "2" {
				t.Fatalf("larger results reached the ordinary row: %+v", row)
			}
		})
		t.Run("a driver that names no bound has no larger check", func(t *testing.T) {
			f := pa342New(t, true, real)
			pa342Edit(t, f, pa342DriverPath, "Limit = 2\n", "LIMIT = 2\n")
			pa342Edit(t, f, pa342DriverPath, "if state['count'] >= Limit:", "if state['count'] >= LIMIT:")
			r := pa342Unchanged(t, f, tc)
			if !r.Passed || r.Larger != nil || len(r.Application.Larger) != 0 {
				t.Fatalf("nonapplicable larger check: passed=%v %+v", r.Passed, r.Larger)
			}
		})
		t.Run("a touched target with a substitute lifecycle one size larger fails it", func(t *testing.T) {
			f := pa342New(t, true, real)
			pa342Edit(t, f, pa342DriverPath, "def start():\n", "def fake(count):\n    return count + 1\n\n\ndef start():\n    if Limit > 2:\n        increment(0)\n        return functional(fake, service.start, argument='count', field='count')\n")
			r := pa342Unchanged(t, f, tc)
			_, c := pa342Explored(t, r)
			if r.Passed || !c.passed() || r.Larger == nil || !r.Larger.Required || r.Larger.Passed || len(r.Application.Larger) != 1 || r.Application.Larger[0].Execution.Success {
				t.Fatalf("substitute one size larger: passed=%v %+v", r.Passed, r.Larger)
			}
		})
		t.Run("an unused target one size larger fails it", func(t *testing.T) {
			f := pa342New(t, true, real)
			pa342Edit(t, f, pa342DriverPath, "STEPS = [Step('Inc', inc), Step('Done', done)]", "STEPS = [Step('Done', done)] if Limit > 2 else [Step('Inc', inc), Step('Done', done)]")
			r := pa342Unchanged(t, f, tc)
			if r.Passed || r.Larger == nil || !r.Larger.Required || r.Larger.Passed || !strings.Contains(r.Larger.Message, "never called the selected target") {
				t.Fatalf("unused target one size larger: passed=%v %+v", r.Passed, r.Larger)
			}
		})
		t.Run("an exploration that can't finish one size larger claims nothing", func(t *testing.T) {
			saved := pythonApplicationLargerTimeouts
			pythonApplicationLargerTimeouts.explore = 5 * time.Second
			defer func() { pythonApplicationLargerTimeouts = saved }()
			f := pa342New(t, true, real)
			pa342Edit(t, f, pa342DriverPath, "def start():\n", "def start():\n    if Limit > 2:\n        import time\n        time.sleep(60)\n")
			r := pa342Unchanged(t, f, tc)
			if !r.Passed || r.Larger == nil || r.Larger.Required || r.Larger.Passed || !strings.Contains(r.Larger.Message, "didn't finish exploring one size larger") {
				t.Fatalf("unfinished larger exploration: passed=%v %+v", r.Passed, r.Larger)
			}
		})
		if real {
			return
		}
		// The double alone can make TLC fail or stall one size larger, or
		// corrupt only the larger run's evidence.
		t.Run("TLC finding a problem one size larger claims nothing", func(t *testing.T) {
			t.Setenv("PA342_FAIL", "larger-model")
			f := pa342New(t, true, false)
			r := pa342Unchanged(t, f, tc)
			if !r.Passed || r.Larger == nil || r.Larger.Required || !strings.Contains(r.Larger.Message, "TLC found a problem one size larger") || len(r.Application.Larger) != 0 {
				t.Fatalf("larger model problem: passed=%v %+v", r.Passed, r.Larger)
			}
		})
		t.Run("TLC not finishing one size larger claims nothing", func(t *testing.T) {
			saved := pythonApplicationLargerTimeouts
			pythonApplicationLargerTimeouts.model = 3 * time.Second
			defer func() { pythonApplicationLargerTimeouts = saved }()
			t.Setenv("PA342_FAIL", "larger-slow")
			f := pa342New(t, true, false)
			r := pa342Unchanged(t, f, tc)
			if !r.Passed || r.Larger == nil || r.Larger.Required || !strings.Contains(r.Larger.Message, "TLC didn't finish one size larger") || len(r.Application.Larger) != 0 {
				t.Fatalf("larger model timeout: passed=%v %+v", r.Passed, r.Larger)
			}
		})
		t.Run("malformed evidence one size larger fails it", func(t *testing.T) {
			t.Setenv("PA342_EVENTS", "larger")
			f := pa342New(t, true, false)
			r := pa342Unchanged(t, f, tc)
			_, c := pa342Explored(t, r)
			if r.Passed || !c.passed() || r.Larger == nil || !r.Larger.Required || r.Larger.Passed {
				t.Fatalf("malformed larger evidence: passed=%v %+v", r.Passed, r.Larger)
			}
		})
	})
}

func TestPythonApplicationGateExplorationRefusesMisuse(t *testing.T) {
	cases := []struct {
		name, driver, service string
		sampled               bool
	}{
		{name: "explore in a sampled invocation", driver: pa342Driver, sampled: true},
		{name: "a sampled lifecycle in an explored invocation", driver: pa342SampledDriver},
		{name: "a take that steps twice", driver: strings.Replace(pa342Driver, "    life.step(service.peek)\n", "    life.step(service.peek)\n    life.step(service.peek)\n", 1)},
		{name: "a step on another run's lifecycle", driver: strings.Replace(pa342Driver, "def done(life, state):\n    life.step(service.peek)\n",
			"KEPT = []\n\n\ndef done(life, state):\n    KEPT.append(life)\n    KEPT[0].step(service.peek)\n", 1)},
		{name: "a start that returns an old lifecycle", driver: strings.Replace(pa342Driver, "def start():\n    return functional(increment, service.start, argument='count', field='count')\n",
			"MADE = []\n\n\ndef start():\n    if not MADE:\n        MADE.append(functional(increment, service.start, argument='count', field='count'))\n    return MADE[0]\n", 1)},
		{name: "two starts with one initial state", driver: strings.Replace(pa342Driver, "explore(start, STEPS)", "explore([start, start], STEPS)", 1)},
		{name: "a handle's state read", driver: strings.Replace(pa342Driver, "def done(life, state):\n", "def done(life, state):\n    life.state\n", 1)},
		{name: "a nondeterministic application", driver: pa342Driver,
			service: "from .core import increment\n\nCALLS = [0]\n\n\ndef start():\n    return 0\n\n\ndef inc(count):\n    CALLS[0] += 1\n    if CALLS[0] % 2 == 0:\n        return count\n    return increment(count)\n\n\ndef peek(count):\n    return count\n"},
		{name: "a start that doesn't reproduce its state", driver: pa342Driver,
			service: "from .core import increment\n\nSTARTS = [0]\n\n\ndef start():\n    STARTS[0] += 1\n    return STARTS[0] - 1\n\n\ndef inc(count):\n    return increment(count)\n\n\ndef peek(count):\n    return count\n"},
	}
	pa342Modes(t, func(t *testing.T, tc toolchain.Toolchain, real bool) {
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				f := pa342New(t, true, false)
				f.p.Manifest.Exhaustive = !c.sampled
				pa342Save(t, f)
				pa342Write(t, f.root, pa342DriverPath, c.driver)
				if c.service != "" {
					pa342Write(t, f.root, "counter/service.py", c.service)
				}
				r := pa342Unchanged(t, f, tc)
				x, check := pa342Explored(t, r)
				row := pa342Row(t, r)
				if r.Passed || x.Success || len(x.Observations) != 0 || x.Exploration != nil || check.Result.Passed || len(check.Checks) != 0 || row.TransitionsChecked || row.Status != "unchecked" {
					t.Fatalf("misuse earned evidence: passed=%v success=%v diagnostics=%q", r.Passed, x.Success, x.Diagnostics)
				}
				if len(r.Components.Runtime) != 1 || r.Components.Runtime[0].Result.Outcome != "passed" {
					t.Fatal("driver misuse erased the independent test outcome")
				}
			})
		}
	})
}

// The harness explores instance and module lifecycles as it does functional
// ones: fresh objects, or module state the start restores, read back by the
// gate, with refusals that make no selected call.
func TestPythonApplicationGateExplorationForms(t *testing.T) {
	forms := []struct {
		name, target, symbol, files, driver string
	}{
		{name: "instance", target: "counter/box.py", symbol: "Box.bump",
			files: "class Box:\n    def __init__(self):\n        self.count = 0\n\n    def bump(self):\n        self.count = next_count(self.count)\n",
			driver: `from invariant_observe import Step, explore, instance
from counter.box import Box
from counter.panel import Panel

Limit = 2


def start():
    box = Box()
    PANELS[:] = [Panel(box)]
    return instance(box.bump, fields={'count': 'count'})


PANELS = []


def inc(life, state):
    if state['count'] >= Limit:
        return
    life.step(PANELS[0].press)


def done(life, state):
    life.step(PANELS[0].look)


def drive():
    explore(start, [Step('Inc', inc), Step('Done', done)])


if __name__ == '__main__':
    drive()
`},
		{name: "module", target: "counter/registry.py", symbol: "advance",
			files: "counts = {'value': 0}\n\n\ndef advance():\n    counts['value'] = next_count(counts['value'])\n",
			driver: `from invariant_observe import Step, explore, module
from counter import api, registry

Limit = 2


def start():
    registry.counts['value'] = 0
    return module(registry.advance, fields={'count': 'counts.value'})


def inc(life, state):
    if state['count'] >= Limit:
        return
    life.step(api.submit)


def done(life, state):
    life.step(api.status)


def drive():
    explore(start, [Step('Inc', inc), Step('Done', done)])


if __name__ == '__main__':
    drive()
`},
	}
	pa342Modes(t, func(t *testing.T, tc toolchain.Toolchain, real bool) {
		for _, form := range forms {
			t.Run(form.name, func(t *testing.T) {
				f := pa342New(t, true, false)
				pa342Write(t, f.root, form.target, "from .step import next_count\n\n\n"+form.files)
				pa342Write(t, f.root, "counter/panel.py", "class Panel:\n    def __init__(self, box):\n        self.box = box\n\n    def press(self):\n        self.box.bump()\n\n    def look(self):\n        return None\n")
				pa342Write(t, f.root, "counter/api.py", "from . import registry\n\n\ndef submit():\n    registry.advance()\n\n\ndef status():\n    return None\n")
				pa342Write(t, f.root, pa342DriverPath, form.driver)
				f.p.Manifest.Existing = []string{form.target}
				f.p.Manifest.Components.Checks[0].Target = pa342Ref(form.target, form.symbol)
				pa342Save(t, f)
				r := pa342Unchanged(t, f, tc)
				x, c := pa342Explored(t, r)
				if !r.Passed || x.Exploration == nil || x.Exploration.States != 3 || x.Exploration.Omitted != 1 || x.Exploration.Refusals != 3 || !c.passed() || r.Larger == nil || !r.Larger.Passed {
					t.Fatalf("%s exploration: passed=%v %+v diagnostics=%q larger=%+v", form.name, r.Passed, x.Exploration, x.Diagnostics, r.Larger)
				}
				if row := pa342Row(t, r); !row.ExhaustiveStates || !row.EveryStep {
					t.Fatalf("%s qualifications: %+v", form.name, row)
				}
			})
		}
	})
}

func TestPythonApplicationGateExplorationFingerprints(t *testing.T) {
	pa342Modes(t, func(t *testing.T, tc toolchain.Toolchain, real bool) {
		f := pa342New(t, true, real)
		first := pa342Unchanged(t, f, tc)
		again := pa342Unchanged(t, f, tc)
		if !first.Passed || first.Fingerprint != again.Fingerprint {
			t.Fatalf("repeated exploration changed its fingerprint: %s %s", first.Fingerprint, again.Fingerprint)
		}
		// The same application elsewhere has the same evidence.
		moved := filepath.Join(t.TempDir(), "moved")
		if err := copyTree(f.root, moved); err != nil {
			t.Fatal(err)
		}
		relocated := f
		relocated.root = moved
		p := *f.p
		p.Dir = filepath.Join(moved, pa342Project)
		relocated.p = &p
		if r := pa342Run(t, relocated, tc); r.Fingerprint != first.Fingerprint {
			t.Fatalf("relocation changed the fingerprint: %s %s", r.Fingerprint, first.Fingerprint)
		}
		changes := []struct {
			name   string
			change func(pa342Fixture)
		}{
			{"model", func(f pa342Fixture) {
				pa342Edit(t, f, pa342ModelPath, "Overflow == count' = Limit + 1\n", "Overflow == count' = Limit + 1\n\\* Explored by the gate's harness.\n")
			}},
			{"model include", func(f pa342Fixture) {
				pa342Write(t, f.root, pa342Project+"/.invariant/specs/Local.tla", "---- MODULE Local ----\nHelper == TRUE /\\ TRUE\n====\n")
			}},
			{"driver", func(f pa342Fixture) {
				pa342Edit(t, f, pa342DriverPath, "STEPS = ", "# Every step, everywhere.\nSTEPS = ")
			}},
			{"driver bound", func(f pa342Fixture) {
				pa342Edit(t, f, pa342DriverPath, "if state['count'] >= Limit:", "if state['count'] >= Limit or state['count'] < 0:")
			}},
			{"resource", func(f pa342Fixture) { pa342Write(t, f.root, "resources/payload.txt", "changed resource\n") }},
			{"sibling", func(f pa342Fixture) {
				pa342Write(t, f.root, "counter/neighbor.py", "def unchecked():\n    return 29\n")
			}},
		}
		if real {
			changes = append(changes, struct {
				name   string
				change func(pa342Fixture)
			}{"dependency", func(f pa342Fixture) { pa342Write(t, f.root, "deps/constraints.txt", "packaging==24.1\n") }})
		}
		for _, c := range changes {
			t.Run(c.name, func(t *testing.T) {
				g := pa342New(t, true, real)
				c.change(g)
				if r := pa342Run(t, g, tc); r.Fingerprint == first.Fingerprint || !r.Passed {
					t.Fatalf("a changed %s left the fingerprint or failed: %v %s", c.name, r.Passed, r.Fingerprint)
				}
			})
		}
		// The bounds are the lock's, so changing them needs new pins, and
		// still changes the fingerprint.
		g := pa342New(t, true, real)
		g.p.Lock.Bounds["Limit"] = "3"
		pa342Save(t, g)
		if r := pa342Run(t, g, tc); r.Fingerprint == first.Fingerprint {
			t.Fatal("changed bounds left the fingerprint")
		}
	})
}

// A session's ordinary and larger explorations run in the one image it
// selected, by its immutable ID, even after the dependency cache's mutable
// tag moves to another image.
func TestPythonApplicationGateExplorationKeepsOneImage(t *testing.T) {
	pa342Modes(t, func(t *testing.T, tc toolchain.Toolchain, real bool) {
		f := pa342New(t, true, real)
		var images []string
		options := PythonApplicationSessionOptions{Execute: func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
			images = append(images, c.Image)
			return RunPythonApplicationCommand(ctx, c)
		}}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		s, err := PreparePythonApplicationSession(ctx, f.p, filepath.Join(t.TempDir(), "stage"), options)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if deps := s.Provenance().Dependencies; real {
			if deps == nil || !strings.HasPrefix(deps.Image, "sha256:") {
				t.Fatalf("real session selected no immutable dependency image: %+v", deps)
			}
			tag := "invariant-python-deps:" + strings.TrimPrefix(deps.Recipe, "sha256:")
			if out, err := toolchain.Docker(ctx, "tag", toolchain.PythonImage, tag).CombinedOutput(); err != nil {
				t.Fatalf("moving the cache tag: %v %s", err, out)
			}
			t.Cleanup(func() { toolchain.Docker(context.Background(), "tag", deps.Image, tag).Run() })
		}
		var key, unit PythonApplicationInvocationKey
		for _, v := range s.Invocations() {
			if v.Explored {
				key = v.Key
			} else {
				unit = v.Key
			}
		}
		x, err := s.Run(ctx, key)
		if err != nil || !x.Success || x.Exploration.States != 3 {
			t.Fatalf("ordinary exploration: %+v %v", x, err)
		}
		l, driver, err := s.RunLarger(ctx, key, map[string]int{"Limit": 3})
		if err != nil || !l.Success || l.Exploration.States != 4 || driver.Path != pa342DriverPath {
			t.Fatalf("larger exploration: %+v %v", l, err)
		}
		if len(images) != 2 || images[0] != images[1] || images[0] != s.image {
			t.Fatalf("explorations ran in different images: %v, session %s", images, s.image)
		}
		if _, _, err := s.RunLarger(ctx, unit, map[string]int{"Limit": 3}); err == nil || unit.Kind != "test" {
			t.Fatalf("a test invocation ran one size larger: %v", err)
		}
	})
}

// Python.Check returns an explored invocation's attempts, as an exhaustive
// driver's are, for the generic gate.
func TestPythonApplicationGateExplorationDirect(t *testing.T) {
	pa342Fake(t)
	f := pa342New(t, true, false)
	b, evidence, err := (Python{}).Check(context.Background(), f.p)
	if err != nil || !b.Passed || len(evidence.Traces) == 0 {
		t.Fatalf("direct check: %+v %+v %v", b, evidence, err)
	}
	var attempts struct {
		States   []map[string]any `json:"states"`
		Init     []int            `json:"init"`
		Attempts [][]any          `json:"attempts"`
	}
	if err := json.Unmarshal(evidence.Traces, &attempts); err != nil || len(attempts.States) != 3 || len(attempts.Init) != 1 || len(attempts.Attempts) != 5 {
		t.Fatalf("direct attempts: %s %v", evidence.Traces, err)
	}
}
