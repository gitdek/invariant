package verify

// Issue #321 contract. Add only this new form:
//   with module(target, fields={"status":"tasks[*].status"},
//               expected_initial={"status":[]}, timeout=5.0) as life:
//       life.step(entry, *args, expected_before=None, expected_after=None, **kwargs)
// The target is a staged module-level function; entry is a distinct staged
// application callable outside the driver. Both may be async and may have
// registration decorators only if their live binding is the lexical function
// itself. Never unwrap. Existing code/globals checks still apply. For module
// entries only, positional and keyword-only defaults may be any objects,
// including values from calls such as Form(''). Like explicit arguments they
// are inputs, never evidence: do not require literal syntax or JSON values.
// They must remain the live function's own original defaults, unreplaced,
// including before the first step and between steps; equality with a replacement
// is insufficient. The target retains literal-scalar defaults, and functional
// and instance retain all their existing restrictions. Document this distinction.
// Read initial state from target.__globals__, exactly the staged module dict;
// every selected frame must have that very globals object. Use module identity
// for execution-local instance numbering. Initial entry is the target.
//
// Path grammar: identifier root, then zero or more .identifier dict-key,
// [nonnegative decimal index] list-index, or [*] projection segments. Wildcards
// traverse exact dict values in insertion order or exact list elements, and
// project the remaining suffix into a list, including [] for no elements.
// No eval, attribute access, coercion, subclasses or callbacks. Missing keys,
// wrong container types, malformed paths and cycles fail. Leaves are deeply
// copied exact dicts (string keys), lists and finite JSON scalars. Expectations
// assert the entire copied JSON state with type-sensitive equality; they never
// provide evidence. Read before entry, verify continuity, then read after entry
// and all its owned threads finish. Entry reservations and worker mutations
// thus belong to one step; no equality with an intermediate target-entry or
// target-return snapshot is required. Caller arguments are inputs only.
//
// Run returned awaitables in a fresh event loop, close it afterward, and count
// each async frame once across suspension/resumption, accepting only completed
// calls. Exactly one nonrecursive selected invocation per step is required,
// either beneath the entry or on a thread it starts (including descendants).
// Ownership follows Thread objects/start events, never reusable thread IDs or
// merely the interval in which a thread happens to run. Drain all owned threads,
// not just the target thread; unrelated thread events poison execution. No
// worker/unfinished coroutine may leak accepted evidence into a later step.
// timeout is a positive finite number of seconds (default 5, maximum 60), one
// total deadline for entry/awaitable and draining threads. Timeout poisons the
// execution with a diagnostic and returns promptly even for non-daemon workers;
// it cannot be converted to success by catching cancellation or errors.
//
// Module rows add form:"module" (Go Form string, omitted for old forms) and
// retain exact paths in readback plus all existing identities and hashes.
// Raw state remains JSON. Go independently validates form, paths, recursive
// JSON, ordered stream and continuity without relaxing scalar-only legacy
// forms. Authentication, fresh execution identity, stateless handles, snapshot
// checks, sampling limits and rejection of caught observation errors stay intact.
// Generator targets, wrappers, closures and substitutions remain unsupported.
// Update docs for all these rules, with a complete stdlib web-shaped example.
// New policy refining D-0183 is merger work, not a fabricated decision ID.
// Regression tests must also cover changed staged inputs/observer, interleaved
// handles on the same/different modules, cancellation and cleanup, and hostile
// authenticated protocol fields. Existing #318 and #327 suites remain intact.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
)

const pa321State = `import asyncio, threading, time, uuid
tasks = {}
lock = threading.Lock()
meta = {'count': 9007199254740993, 'values': []}
rows = [{'value': 'fixed'}]
def register(fn):
    return fn
def reserve(request):
    with lock:
        key = uuid.uuid4().hex
        tasks[key] = {'status': 'reserved'}
    assert request['upload'] is not None
    return key
def finish(key, status):
    time.sleep(0.03)
    tasks[key]['status'] = status
    meta['values'].append(status)
`

const pa321Worker = `@register
def advance(key, status):
    finish(key, status)
@register
async def submit(request, *, status):
    await asyncio.sleep(0)
    key = reserve(request)
    worker = threading.Thread(target=advance, args=(key, status))
    worker.start()
    await asyncio.sleep(0.001)
`

const pa321Async = `@register
async def advance(request, *, status):
    await asyncio.sleep(0)
    key = reserve(request)
    worker = threading.Thread(target=finish, args=(key, status))
    worker.start()
    await asyncio.sleep(0.001)
    await asyncio.sleep(0)
@register
async def submit(request, *, status):
    await advance(request, status=status)
`

const pa321Driver = `from invariant_observe import module
from tasks import lifecycle as app
def drive():
    with module(app.advance, fields={'status':'tasks[*].status', 'details':'meta', 'first':'rows[0].value'}, timeout=2) as life:
        life.step(app.submit, {'upload': object()}, status='done')
        life.step(app.submit, {'upload': object()}, status='other')
if __name__ == '__main__':
    drive()
`

func pa321Fixture(t *testing.T, source, driver string) (string, *project.Project) {
	t.Helper()
	root, p := pa318Fixture(t, false, driver, false)
	pa310Write(t, root, pa315Target, source)
	pa310Save(t, p)
	return root, p
}

func pa321Context(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func pa321JSON(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func pa321Positive(t *testing.T, real bool, source string) {
	t.Helper()
	root, p := pa321Fixture(t, source, pa321Driver)
	before := pa315Tree(t, root)
	selected := 0
	s := pa315Prepare(t, p, pa318Options(t, real, &selected))
	first := pa318Success(t, pa321Context(t), s)
	if len(first.Observations) != 3 {
		t.Fatalf("want initial and two steps: %+v", first)
	}
	for i, row := range first.Observations {
		var wire map[string]any
		if err := json.Unmarshal([]byte(pa321JSON(t, row)), &wire); err != nil {
			t.Fatal(err)
		}
		if wire["form"] != "module" || row.Sequence != i || row.Run != 1 || row.Instance != 1 || row.Key != first.Key || row.Target != first.Key.Target || row.Driver != first.Key.Check || row.Symbol != "advance" || row.SourceSHA256 != before[pa315Target] || row.DriverSHA256 != before[pa315Project+"/driver.py"] || row.EntrySHA256 != before[pa315Target] {
			t.Fatalf("unbound module observation: %+v", row)
		}
		if !reflect.DeepEqual(row.Readback, map[string]string{"status": "tasks[*].status", "details": "meta", "first": "rows[0].value"}) {
			t.Fatal(row.Readback)
		}
		state := row.State
		entry := "advance"
		if i > 0 {
			state, entry = row.After, "submit"
			previous := first.Observations[i-1].After
			if i == 1 {
				previous = first.Observations[0].State
			}
			if !reflect.DeepEqual(row.Before, previous) {
				t.Fatal("broken continuity")
			}
		}
		values := []string{}
		if i > 0 {
			values = append(values, "done")
		}
		if i > 1 {
			values = append(values, "other")
		}
		want := map[string]any{"status": values, "details": map[string]any{"count": json.Number("9007199254740993"), "values": values}, "first": "fixed"}
		if pa321JSON(t, state) != pa321JSON(t, want) || row.Entry != pa310ID(pa315Target, entry) {
			t.Fatalf("state not copied after all workers, or wrong entry: %s want %s", pa321JSON(t, row), pa321JSON(t, want))
		}
	}
	second := pa318Success(t, pa321Context(t), s)
	if pa321JSON(t, first) != pa321JSON(t, second) || first.Fingerprint == "" || selected != 1 {
		t.Fatal("module repeat is not portable or image was reselected")
	}
	_, q := pa321Fixture(t, source, pa321Driver)
	other := pa315Prepare(t, q, pa318Options(t, real, new(int)))
	third := pa318Success(t, pa321Context(t), other)
	if pa321JSON(t, first) != pa321JSON(t, third) {
		t.Fatal("module relocation changed evidence")
	}
	if !reflect.DeepEqual(before, pa315Tree(t, root)) {
		t.Fatal("original application changed")
	}
}

func TestPythonApplicationExecutionModuleObservations(t *testing.T) {
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		t.Run(map[bool]string{false: "local", true: "Docker"}[real], func(t *testing.T) {
			variants := map[string]string{
				"worker target":      pa321Worker,
				"async target":       pa321Async,
				"returned awaitable": strings.Replace(pa321Async, "async def submit(request, *, status):\n    await advance(request, status=status)", "def submit(request, *, status):\n    return advance(request, status=status)", 1),
				"synchronous":        strings.Replace(strings.Replace(pa321Worker, "async def submit", "def submit", 1), "    await asyncio.sleep(0)\n", "", 1),
				"descendant worker":  strings.Replace(pa321Worker, "target=advance", "target=spawn", 1) + "\ndef spawn(key, status):\n    threading.Thread(target=advance, args=(key, status)).start()\n",
			}
			variants["synchronous"] = strings.Replace(variants["synchronous"], "    await asyncio.sleep(0.001)\n", "", 1)
			for name, body := range variants {
				t.Run(name, func(t *testing.T) { pa321Positive(t, real, pa321State+body) })
			}
			t.Run("replay", func(t *testing.T) {
				_, p := pa321Fixture(t, pa321State+pa321Worker, pa321Driver)
				opts := pa318Options(t, real, new(int))
				execute := opts.Execute
				if execute == nil {
					execute = RunPythonApplicationCommand
				}
				var saved []byte
				opts.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
					r, err := execute(ctx, c)
					if saved == nil {
						saved = bytes.Clone(r.Events)
					} else {
						r.Events = bytes.Clone(saved)
					}
					return r, err
				}
				s := pa315Prepare(t, p, opts)
				pa318Success(t, pa321Context(t), s)
				r, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
				pa318Reject(t, r, err)
			})
		})
	}
}

func TestPythonApplicationExecutionModuleEntryDefaults(t *testing.T) {
	// Both kinds of defaults are opaque, non-JSON objects created by a call,
	// as with framework parameter declarations. Neither affects read-back.
	defaults := "def Form(default):\n    return object()\n" + strings.Replace(pa321Worker,
		"async def submit(request, *, status):",
		"async def submit(request, engine=Form(''), *, status, image_back=Form('')):\n    assert type(engine) is object and type(image_back) is object", 1)
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		t.Run(map[bool]string{false: "local", true: "Docker"}[real], func(t *testing.T) {
			// Reuse the full copied-state, binding, repeatability and relocation
			// assertions. A blanket refusal cannot satisfy this acceptance test.
			pa321Positive(t, real, pa321State+defaults)
			syncDefaults := strings.Replace(defaults, "async def submit", "def submit", 1)
			syncDefaults = strings.ReplaceAll(syncDefaults, "    await asyncio.sleep(0)\n", "")
			syncDefaults = strings.ReplaceAll(syncDefaults, "    await asyncio.sleep(0.001)\n", "")
			t.Run("sync entry", func(t *testing.T) { pa321Positive(t, real, pa321State+syncDefaults) })
			// Replacement values still satisfy the entry's type assertions and
			// produce the same lifecycle. Only default integrity can refuse them.
			for _, replacement := range []struct{ name, statement string }{
				{"positional", "app.submit.__defaults__ = (object(),)"},
				{"keyword mapping", "app.submit.__kwdefaults__ = {'image_back': object()}"},
				{"keyword value", "app.submit.__kwdefaults__['image_back'] = object()"},
			} {
				for _, before := range []bool{true, false} {
					phase, anchor := "before first step", "        life.step(app.submit, {'upload': object()}, status='done')"
					if !before {
						phase, anchor = "between steps", "        life.step(app.submit, {'upload': object()}, status='other')"
					}
					t.Run(replacement.name+" "+phase, func(t *testing.T) {
						driver := strings.Replace(pa321Driver, anchor, "        "+replacement.statement+"\n"+anchor, 1)
						_, p := pa321Fixture(t, pa321State+defaults, driver)
						s := pa315Prepare(t, p, pa318Options(t, real, new(int)))
						r, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
						pa318Reject(t, r, err)
					})
				}
			}
			for _, declaration := range []string{
				"def advance(key, status, unused=Form('')):",
				"def advance(key, status, *, unused=Form('')):",
			} {
				t.Run("target still refuses "+declaration, func(t *testing.T) {
					source := strings.Replace(defaults, "def advance(key, status):", declaration, 1)
					_, p := pa321Fixture(t, pa321State+source, pa321Driver)
					s := pa315Prepare(t, p, pa318Options(t, real, new(int)))
					r, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
					pa318Reject(t, r, err)
				})
			}
		})
	}
}

func TestPythonApplicationExecutionModuleRefusals(t *testing.T) {
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		t.Run(map[bool]string{false: "local", true: "Docker"}[real], func(t *testing.T) {
			// A blanket rejection of the new form is not a passing refusal test.
			pa321Positive(t, real, pa321State+pa321Worker)
			base := pa321State + pa321Worker
			type bad struct{ name, source, driver string }
			cases := []bad{
				{"wrapper with wraps", strings.Replace(base, "    return fn", "    import functools\n    @functools.wraps(fn)\n    def wrapper(*args, **kwargs): return fn(*args, **kwargs)\n    return wrapper", 1), pa321Driver},
				{"foreign frame globals", strings.Replace(base, "target=advance", "target=foreign", 1) + "\nimport types\nforeign = types.FunctionType(advance.__code__, dict(globals()))\n", pa321Driver},
				{"foreign binding globals", base + "\nimport types\nadvance = types.FunctionType(advance.__code__, dict(globals()))\n", pa321Driver},
				{"another module path", base + "\nfrom tasks import application as other\n", strings.Replace(pa321Driver, "tasks[*].status", "other.tasks[*].status", 1)},
				{"attribute reader", base + "\nclass Box:\n    @property\n    def status(self): return 'done'\ntasks['bad'] = Box()\n", pa321Driver},
				{"non JSON leaf", base + "\nmeta['bad'] = object()\n", pa321Driver},
				{"cycle", base + "\nmeta['cycle'] = meta\n", pa321Driver},
				{"dict subclass", base + "\nclass Dict(dict): pass\ntasks = Dict()\n", pa321Driver},
				{"missing path", base, strings.Replace(pa321Driver, "rows[0].value", "rows[1].value", 1)},
				{"malformed path", base, strings.Replace(pa321Driver, "rows[0].value", "rows[-1].value", 1)},
				{"two targets", strings.Replace(base, "    finish(key, status)", "    finish(key, status)", 1) + "\nasync def twice(request, *, status):\n    key = reserve(request)\n    advance(key, status)\n    advance(key, status)\n", strings.ReplaceAll(pa321Driver, "app.submit", "app.twice")},
				{"two async targets", pa321State + strings.Replace(pa321Async, "    await advance(request, status=status)", "    await advance(request, status=status)\n    await advance(request, status=status)", 1), pa321Driver},
				{"no target", strings.Replace(base, "target=advance", "target=finish", 1), pa321Driver},
				{"preexisting worker", base + "\nready = threading.Event()\ndef waiting():\n    ready.wait()\n    advance(reserve({'upload':1}), 'done')\nthreading.Thread(target=waiting, daemon=True).start()\nasync def release(request, *, status):\n    ready.set()\n    await asyncio.sleep(0.03)\n", strings.ReplaceAll(pa321Driver, "app.submit", "app.release")},
				{"driver started worker", base, strings.Replace(pa321Driver, "def drive():", "def drive():\n    import threading\n    threading.Thread(target=lambda: None).start()", 1)},
				{"worker still running", strings.Replace(base, "time.sleep(0.03)", "time.sleep(30)", 1), strings.Replace(pa321Driver, "timeout=2", "timeout=0.1", 1)},
				{"non target worker still running", strings.Replace(base, "    worker.start()", "    worker.start()\n    threading.Thread(target=time.sleep, args=(30,)).start()", 1), strings.Replace(pa321Driver, "timeout=2", "timeout=0.1", 1)},
				{"awaitable never completes", base + "\nasync def forever(request, *, status):\n    await asyncio.Event().wait()\n", strings.ReplaceAll(strings.Replace(pa321Driver, "timeout=2", "timeout=0.1", 1), "app.submit", "app.forever")},
				{"state changed between steps", base, strings.Replace(pa321Driver, "        life.step(app.submit, {'upload': object()}, status='other')", "        app.meta['values'].append('forged')\n        life.step(app.submit, {'upload': object()}, status='other')", 1)},
				{"caught handle write", base, strings.Replace(pa321Driver, "        life.step(app.submit", "        try:\n            life.state = {}\n        except AttributeError:\n            pass\n        life.step(app.submit", 1)},
				{"caught expectation mismatch", base, strings.Replace(pa321Driver, "        life.step(app.submit, {'upload': object()}, status='done')", "        try:\n            life.step(app.submit, {'upload': object()}, status='done', expected_after={'status':['forged']})\n        except Exception:\n            pass", 1)},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					root, p := pa321Fixture(t, tc.source, tc.driver)
					before := pa315Tree(t, root)
					s := pa315Prepare(t, p, pa318Options(t, real, new(int)))
					ctx := pa321Context(t)
					r, err := s.Run(ctx, pa318Driver(t, s).Key)
					pa318Reject(t, r, err)
					if ctx.Err() != nil {
						t.Fatal("collector did not enforce its own bound before outer process cancellation")
					}
					if !reflect.DeepEqual(before, pa315Tree(t, root)) {
						t.Fatal("refusal changed originals")
					}
				})
			}
			// Existing actual-Python regressions cover scalar readers and blanket
			// async/decorator/concurrency refusals on functional and instance.
			pa318ReaderFailures(t, real)
		})
	}
}
