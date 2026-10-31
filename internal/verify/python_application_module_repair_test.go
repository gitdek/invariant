package verify

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPythonApplicationExecutionModuleDefinitionCapture(t *testing.T) {
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		for _, decorator := range []string{
			"def register(*args):\n    fn = args[0]\n",
			"class Registry:\n    def __call__(self, *args):\n        return change(args[0])\nregister = Registry()\ndef change(*args):\n    fn = args[0]\n",
			"import functools\nregister = functools.partial(lambda *args: change(*args))\ndef change(*args):\n    fn = args[0]\n",
		} {
			for name, change := range map[string]string{
				"identity":   "    return fn\n",
				"clone":      "    import types\n    return types.FunctionType(fn.__code__, fn.__globals__, fn.__name__, fn.__defaults__)\n",
				"positional": "    fn.__defaults__ = (object(),)\n    return fn\n",
				"keyword":    "    fn.__kwdefaults__ = {'extra': object()}\n    return fn\n",
				"value":      "    fn.__kwdefaults__['extra'] = object()\n    return fn\n",
			} {
				t.Run(name, func(t *testing.T) {
					body := strings.Replace(pa321Worker, "@register\ndef advance", "def advance", 1)
					body = strings.Replace(body, "async def submit(request, *, status):", "async def submit(request, upload=object(), *, status, extra=object()):", 1)
					source := pa321State + decorator + change + body
					if name == "identity" {
						pa321Positive(t, real, source)
						return
					}
					_, p := pa321Fixture(t, source, pa321Driver)
					s := pa315Prepare(t, p, pa318Options(t, real, new(int)))
					r, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
					pa318Reject(t, r, err)
				})
			}
		}
		// Targets also require the exact object made by the declaration.
		source := strings.Replace(pa321State+pa321Worker, "def register(fn):\n    return fn", "def register(*args):\n    import types\n    fn = args[0]\n    return types.FunctionType(fn.__code__, fn.__globals__, fn.__name__)", 1)
		_, p := pa321Fixture(t, source, pa321Driver)
		s := pa315Prepare(t, p, pa318Options(t, real, new(int)))
		r, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
		pa318Reject(t, r, err)
	}
}

func TestPythonApplicationExecutionModuleCoroutineLifetimes(t *testing.T) {
	const auxiliary = `saved = None
async def later():
    await asyncio.sleep(0)
    meta['values'].append('escaped')
async def broken():
    raise RuntimeError('auxiliary failed')
`
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		for name, work := range map[string]string{
			"manually suspended": `    global saved
    if saved is None:
        saved = later()
        saved.send(None)
    else:
        try:
            saved.send(None)
        except StopIteration:
            pass
`,
			"unstarted":                     "    global saved\n    saved = later()\n",
			"generator coroutine suspended": "    global saved\n    saved = later()\n    saved.send(None)\n",
			"generator coroutine unstarted": "    global saved\n    saved = later()\n",
			"failed task":                   "    asyncio.create_task(broken())\n    await asyncio.sleep(0)\n    await asyncio.sleep(0)\n",
			"failed direct task":            "    asyncio.Task(broken())\n    await asyncio.sleep(0)\n    await asyncio.sleep(0)\n",
			"failed callback":               "    asyncio.get_running_loop().call_soon(lambda: 1 / 0)\n    await asyncio.sleep(0)\n",
		} {
			t.Run(name, func(t *testing.T) {
				source := pa321State + "def advance(key, status):\n    finish(key, status)\n" + auxiliary + "async def submit(request, *, status):\n    advance(reserve(request), status)\n" + work
				if strings.HasPrefix(name, "generator coroutine") {
					source = strings.Replace(source, "async def later():\n    await asyncio.sleep(0)", "import types\n@types.coroutine\ndef later():\n    yield", 1)
				}
				_, p := pa321Fixture(t, source, pa321Driver)
				s := pa315Prepare(t, p, pa318Options(t, real, new(int)))
				r, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
				pa318Reject(t, r, err)
			})
		}
		// Completion in the same step is allowed for tasks and manually driven
		// coroutines. Neither may be confused with an additional target call.
		body := strings.Replace(pa321Async, "    await advance(request, status=status)", `    complete_manual()
    await asyncio.create_task(asyncio.sleep(0))
    await advance(request, status=status)`, 1)
		body += `
def complete_manual():
    pending = asyncio.sleep(0)
    pending.send(None)
    try:
        pending.send(None)
    except StopIteration:
        pass
`
		pa321Positive(t, real, pa321State+body)
	}
}

func TestPythonApplicationExecutionModuleGILDeadline(t *testing.T) {
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		for name, entry := range map[string]string{
			"sync":   "def submit(request, *, status):\n    busy()\n",
			"async":  "async def submit(request, *, status):\n    busy()\n",
			"worker": "def submit(request, *, status):\n    threading.Thread(target=busy).start()\n",
		} {
			t.Run(name, func(t *testing.T) {
				source := pa321State + `def advance(key, status):
    finish(key, status)
def busy():
    print('gil-work-start', flush=True)
    sum(range(2000000000))
` + entry
				_, p := pa321Fixture(t, source, strings.Replace(pa321Driver, "timeout=2", "timeout=0.05", 1))
				opts := pa318Options(t, real, new(int))
				execute := opts.Execute
				if execute == nil {
					execute = RunPythonApplicationCommand
				}
				opts.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
					start := time.Now()
					r, err := execute(ctx, c)
					limit := 2 * time.Second
					if real {
						limit = 5 * time.Second // Includes Docker startup and removal.
					}
					if time.Since(start) > limit {
						t.Fatal("deadline waited for the application GIL")
					}
					if !strings.Contains(r.Stdout, "gil-work-start") || !strings.Contains(r.Stderr, "module observation timed out") {
						t.Fatalf("did not exercise GIL timeout: %+v, %v", r, err)
					}
					return r, err
				}
				s := pa315Prepare(t, p, opts)
				r, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
				pa318Reject(t, r, err)
			})
		}
	}
}
