package verify

import (
	"bytes"
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

// These cases exercise refusals beyond the acceptance fixture through the
// production Python collector. The same cases run in Docker when opted in.
func TestPythonApplicationExecutionObservationReaders(t *testing.T) {
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		t.Run(map[bool]string{false: "host", true: "Docker"}[real], func(t *testing.T) {
			pa318ReaderFailures(t, real)
			pa318ScalarStates(t, real)
		})
	}
}

func pa318ReaderFailures(t *testing.T, real bool) {
	t.Helper()
	replace := func(from, to string) string { return strings.Replace(pa318Functional, from, to, 1) }
	cases := []pa318BadDriver{
		{name: "async", driver: pa318Functional, target: "async def advance(state):\n    return 'running'\n"},
		{name: "generator", driver: pa318Functional, target: "def advance(state):\n    yield 'running'\n"},
		{name: "decorated", driver: pa318Functional, target: "def identity(f): return f\n@identity\ndef advance(state):\n    return 'running'\n"},
		{name: "closure", driver: pa318Functional, target: "def advance(state): return state\ndef make():\n    def advance(state): return 'running'\n    return advance\nadvance = make()\n"},
		{name: "recursive selected call", driver: pa318Functional, target: "def advance(state):\n    if state == 'pending': return advance('running')\n    return state\n"},
		{name: "nan", driver: pa318Functional, target: "def advance(state):\n    return float('nan')\n"},
		{name: "mutable return", driver: pa318Functional, target: "def advance(state):\n    return ['running']\n"},
		{name: "keyword argument missing", driver: replace("argument='state'", "argument='missing'")},
		{name: "driver entry", driver: replace("def drive():", "def tick(state): return advance(state)\ndef drive():")},
		{name: "caught target exception", driver: pa318Functional, target: "def advance(state):\n    try:\n        raise ValueError('caught')\n    except ValueError:\n        return 'running'\n"},
		{name: "concurrency", driver: replace("    drive()", "    import threading\n    t = threading.Thread(target=lambda: None)\n    t.start()\n    t.join()\n    drive()")},
		{name: "unclosed context", driver: "from invariant_observe import functional\nfrom tasks.lifecycle import advance\nfrom tasks import application\ndef drive():\n    life = functional(advance, application.start, argument='state', field='task')\n    life.__enter__()\n    life.step(application.tick)\nif __name__ == '__main__': drive()\n"},
		{name: "disable trace", driver: replace("    drive()", "    import sys\n    sys.settrace(None)\n    drive()")},
		{name: "closed context exit", driver: replace("if __name__ == '__main__':", "    life.__exit__(None, None, None)\nif __name__ == '__main__':")},
		{name: "slots", method: true, driver: pa318Instance, app: strings.Replace(pa318App, "class Task:", "class Task:\n    __slots__ = ('state',)", 1)},
		{name: "callback reader", method: true, driver: strings.Replace(pa318Instance, "fields={'task':'state'}", "fields=lambda: {'task':'pending'}", 1)},
		{name: "static method", method: true, driver: pa318Instance, app: strings.Replace(pa318App, "    def advance(self):", "    @staticmethod\n    def advance(self):", 1)},
		{name: "class method", method: true, driver: pa318Instance, app: strings.Replace(pa318App, "    def advance(self):", "    @classmethod\n    def advance(self):", 1)},
		{name: "descriptor", method: true, driver: pa318Instance, app: strings.Replace(pa318App, "class Task:", "class Reader:\n    def __get__(self, obj, cls): return 'pending'\nclass Task:\n    state = Reader()", 1)},
		{name: "dictionary descriptor", method: true, driver: pa318Instance, app: strings.Replace(pa318App, "class Task:", "class Task:\n    @property\n    def __dict__(self): return {'state': 'pending'}", 1)},
		{name: "instance mutable state", method: true, driver: pa318Instance, app: strings.Replace(pa318App, "self.state = 'pending'", "self.state = ['pending']", 1)},
		{name: "replaced code with claimed filename", driver: replace("def drive():", "advance.__code__ = (lambda state: 'running').__code__.replace(co_filename=advance.__code__.co_filename, co_firstlineno=advance.__code__.co_firstlineno, co_name='advance')\ndef drive():")},
		{name: "substituted globals", driver: replace("def drive():", "import types, tasks.lifecycle\nf = advance\nadvance = types.FunctionType(f.__code__, dict(f.__globals__))\ntasks.lifecycle.advance = advance\napplication.advance = advance\ndef drive():")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "driver entry" {
				tc.driver = strings.Replace(tc.driver, "life.step(application.tick", "life.step(tick", 1)
			}
			root, p := pa318Fixture(t, false, tc.driver, tc.method)
			if tc.app != "" {
				pa310Write(t, root, "tasks/application.py", tc.app)
			}
			if tc.target != "" {
				pa310Write(t, root, pa315Target, tc.target)
			}
			before := pa315Tree(t, root)
			s := pa315Prepare(t, p, pa318Options(t, real, new(int)))
			r, err := s.Run(context.Background(), pa318Driver(t, s).Key)
			pa318Reject(t, r, err)
			if !reflect.DeepEqual(before, pa315Tree(t, root)) {
				t.Fatal("refusal changed originals")
			}
		})
	}
	for _, budget := range []string{"runs", "steps"} {
		t.Run(budget+" budget", func(t *testing.T) {
			_, p := pa318Fixture(t, false, pa318Instance, true)
			p.Manifest.Components.Checks[0].Invocations[1].Sampling[budget] = "1"
			pa310Save(t, p)
			s := pa315Prepare(t, p, pa318Options(t, real, new(int)))
			r, err := s.Run(context.Background(), pa318Driver(t, s).Key)
			pa318Reject(t, r, err)
		})
	}
}

func pa318ScalarStates(t *testing.T, real bool) {
	t.Helper()
	for _, value := range []string{"None", "False", "17", "1.25"} {
		t.Run(value, func(t *testing.T) {
			driver := `from invariant_observe import functional
from tasks.lifecycle import advance
from siblings import entry
def drive():
    import random, sys, os
    assert random.random() == 0.8444218515250481
    assert sys.argv[0] == 'driver.py' and sys.argv[2:] == ['--bound', '2']
    assert os.path.basename(os.getcwd()) == 'lifecycle'
    with functional(advance, entry.start, argument='state', field='value') as life:
        life.step(entry.tick)
if __name__ == '__main__': drive()
`
			root, p := pa318Fixture(t, false, driver, false)
			pa310Write(t, root, pa315Target, "def advance(state):\n    return state\n")
			pa310Write(t, root, "siblings/entry.py", "from tasks.lifecycle import advance\ndef start(): return "+value+"\ndef tick(state): return advance(state)\n")
			p.Manifest.Components.Checks[0].Invocations[1].Sampling["seed"] = "0"
			pa310Save(t, p)
			s := pa315Prepare(t, p, pa318Options(t, real, new(int)))
			r := pa318Success(t, context.Background(), s)
			rows := pa318Rows(t, r)
			if len(rows) != 2 || !reflect.DeepEqual(rows[0].State, rows[1].Before) || !reflect.DeepEqual(rows[1].Before, rows[1].After) {
				t.Fatalf("scalar state changed: %+v", rows)
			}
		})
	}
}

func TestPythonApplicationExecutionObservationTransport(t *testing.T) {
	for _, fault := range []string{"null run", "duplicate", "foreign entry", "wrong sequence", "truncated", "observer before", "observer during"} {
		t.Run(fault, func(t *testing.T) {
			_, p := pa318Fixture(t, false, pa318Functional, false)
			opts := pa318Options(t, false, new(int))
			execute := opts.Execute
			var runtime string
			calls := 0
			opts.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
				calls++
				runtime = c.RuntimeDir
				process, err := execute(ctx, c)
				if calls == 1 {
					return process, err
				}
				switch fault {
				case "observer during":
					pa310Write(t, c.RuntimeDir, "invariant_observe.py", "# changed\n")
				case "truncated":
					process.Events = process.Events[:len(process.Events)-1]
				default:
					lines := bytes.Split(process.Events, []byte("\n"))
					var row map[string]any
					if err := json.Unmarshal(lines[1], &row); err != nil {
						t.Fatal(err)
					}
					switch fault {
					case "null run":
						row["run"] = nil
					case "foreign entry":
						row["entry"] = pa310ID("siblings/unselected.py", "start")
					case "wrong sequence":
						row["sequence"] = 12
					}
					lines[1], _ = json.Marshal(row)
					if fault == "duplicate" {
						lines[1] = bytes.Replace(lines[1], []byte(`"run":1`), []byte(`"run":1,"run":1`), 1)
					}
					process.Events = bytes.Join(lines, []byte("\n"))
				}
				return process, err
			}
			s := pa315Prepare(t, p, opts)
			pa318Success(t, context.Background(), s)
			if fault == "observer before" {
				pa310Write(t, runtime, "invariant_observe.py", "# changed\n")
			}
			r, err := s.Run(context.Background(), pa318Driver(t, s).Key)
			pa318Reject(t, r, err)
			if fault == "observer before" && calls != 1 {
				t.Fatal("changed observer executed")
			}
		})
	}
}

func TestPythonApplicationExecutionObservationCancellation(t *testing.T) {
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		t.Run(map[bool]string{false: "host", true: "Docker"}[real], func(t *testing.T) {
			root, p := pa318Fixture(t, false, strings.Replace(pa318Functional, "    drive()", "    drive()\n    import time\n    time.sleep(300)", 1), false)
			before := pa315Tree(t, root)
			s := pa315Prepare(t, p, pa318Options(t, real, new(int)))
			containers := func() string {
				if !real {
					return ""
				}
				b, err := toolchain.Docker(context.Background(), "ps", "-aq", "--filter", "name=invariant-").CombinedOutput()
				if err != nil {
					t.Fatalf("list containers: %v %s", err, b)
				}
				return string(b)
			}
			oldContainers := containers()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			r, err := s.Run(ctx, pa318Driver(t, s).Key)
			pa318Reject(t, r, err)
			if ctx.Err() == nil {
				t.Fatal("execution was not cancelled")
			}
			for _, id := range strings.Fields(containers()) {
				if !strings.Contains(oldContainers, id) {
					t.Fatalf("cancelled container remains: %s", id)
				}
			}
			if !reflect.DeepEqual(before, pa315Tree(t, root)) {
				t.Fatal("cancellation changed originals")
			}
			if _, err := os.Stat(filepath.Join(root, "BAD")); !os.IsNotExist(err) {
				t.Fatal("literal argument executed")
			}
		})
	}
}

func TestPythonApplicationExecutionObservationIsolation(t *testing.T) {
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
		return
	}
	// Run the ordinary driver first: resource exhaustion must discard even
	// otherwise valid observations, while the same session keeps its image.
	for _, fault := range []string{"controls", "memory", "missing image"} {
		t.Run(fault, func(t *testing.T) {
			driver := pa318Functional
			if fault == "controls" {
				driver += `
import os, socket
from pathlib import Path
assert os.environ['PYTHONHASHSEED'] == '0'
assert all(v == '' for k, v in os.environ.items() if k.lower().endswith('_proxy'))
if Path('/sys/fs/cgroup/memory.max').exists():
    assert Path('/sys/fs/cgroup/memory.max').read_text().strip() == str(512 * 1024 * 1024)
    assert Path('/sys/fs/cgroup/pids.max').read_text().strip() == '128'
    quota, period = map(int, Path('/sys/fs/cgroup/cpu.max').read_text().split())
    assert quota == period
else:
    assert int(Path('/sys/fs/cgroup/memory/memory.limit_in_bytes').read_text()) == 512 * 1024 * 1024
    assert int(Path('/sys/fs/cgroup/pids/pids.max').read_text()) == 128
for name in ['/src/forbidden', '/runtime/forbidden']:
    try:
        Path(name).write_text('must fail')
    except OSError:
        pass
    else:
        raise AssertionError('input mount is writable')
try:
    socket.create_connection(('1.1.1.1', 443), timeout=0.3)
except OSError:
    pass
else:
    raise AssertionError('network available')
`
			} else if fault == "memory" {
				driver += "\nprint('allocating beyond limit', flush=True)\nallocation = bytearray(768 * 1024 * 1024)\n"
			}
			root, p := pa318Fixture(t, false, driver, false)
			before := pa315Tree(t, root)
			selected, calls := 0, 0
			opts := pa318Options(t, true, &selected)
			var process PythonApplicationProcess
			opts.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
				calls++
				if fault == "missing image" {
					c.Image = "sha256:" + strings.Repeat("0", 64)
				}
				var err error
				process, err = RunPythonApplicationCommand(ctx, c)
				return process, err
			}
			s := pa315Prepare(t, p, opts)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			r, err := s.Run(ctx, pa318Driver(t, s).Key)
			if fault == "controls" {
				if err != nil || r == nil || !r.Success {
					t.Fatalf("isolation controls: %+v %v", r, err)
				}
			} else {
				pa318Reject(t, r, err)
				if fault == "memory" && !strings.Contains(process.Stdout, "allocating beyond limit") {
					t.Fatalf("allocation did not run: %+v %v", process, err)
				}
			}
			if selected != 1 || calls != 1 || !reflect.DeepEqual(before, pa315Tree(t, root)) {
				t.Fatal("isolation changed originals or retried selection")
			}
		})
	}
}
