package verify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPythonApplicationExecutionModuleReturnedAwaitable(t *testing.T) {
	source := pa321State + strings.Split(pa321Async, "@register\nasync def submit")[0] + `
class Pending:
    def __init__(self, request, status):
        self.request, self.status = request, status
    def __await__(self):
        return advance(self.request, status=self.status).__await__()
def submit(request, *, status):
    return Pending(request, status)
`
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		pa321Positive(t, real, source)
	}
}

func TestPythonApplicationExecutionModuleInterleaving(t *testing.T) {
	const source = `left, right = [], []
def advance(side, value):
    side.append(value)
def submit(side, value):
    advance(side, value)
`
	const driver = `from invariant_observe import module
from tasks import lifecycle as app
from tasks import second
def drive():
    with module(app.advance, fields={'value':'left'}) as a:
        with module(app.advance, fields={'value':'right'}) as b:
            with module(second.advance, fields={'value':'left'}) as c:
                a.step(app.submit, app.left, 'a')
                c.step(second.submit, second.left, 'c')
                b.step(app.submit, app.right, 'b')
                a.step(app.submit, app.left, 'd')
if __name__ == '__main__': drive()
`
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		root, p := pa321Fixture(t, source, driver)
		pa310Write(t, root, "tasks/second.py", source)
		p.Manifest.Components = nil
		p.Manifest.Existing = []string{pa315Target, "tasks/second.py"}
		pa310Save(t, p)
		s := pa315Prepare(t, p, pa318Options(t, real, new(int)))
		r := pa318Success(t, pa321Context(t), s)
		if len(r.Observations) != 7 {
			t.Fatal(r.Observations)
		}
		for i, identity := range []int{1, 1, 2, 1, 2, 1, 1} {
			if r.Observations[i].Instance != identity || r.Observations[i].Form != "module" {
				t.Fatal(r.Observations)
			}
		}
		if pa321JSON(t, r.Observations[6].After) != `{"value":["a","d"]}` || pa321JSON(t, r.Observations[0].State) != `{"value":[]}` {
			t.Fatal("interleaved state was not copied independently")
		}
	}
}

func TestPythonApplicationExecutionModuleFreshness(t *testing.T) {
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		for _, which := range []string{"staged before", "staged during", "observer before", "observer during"} {
			t.Run(which, func(t *testing.T) {
				root, p := pa321Fixture(t, pa321State+pa321Worker, pa321Driver)
				before := pa315Tree(t, root)
				opts := pa318Options(t, real, new(int))
				execute := opts.Execute
				if execute == nil {
					execute = RunPythonApplicationCommand
				}
				calls := 0
				var command PythonApplicationCommand
				change := func(c PythonApplicationCommand) {
					if strings.HasPrefix(which, "observer") {
						pa310Write(t, c.RuntimeDir, "invariant_observe.py", "raise RuntimeError('changed observer')\n")
					} else {
						pa310Write(t, c.Root, "tasks/next.txt", "changed\n")
					}
				}
				opts.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
					calls++
					command = c
					r, err := execute(ctx, c)
					if calls == 2 && strings.HasSuffix(which, "during") {
						change(c)
					}
					return r, err
				}
				s := pa315Prepare(t, p, opts)
				pa318Success(t, pa321Context(t), s)
				if strings.HasSuffix(which, "before") {
					change(command)
				}
				r, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
				pa318Reject(t, r, err)
				if strings.HasSuffix(which, "before") && calls != 1 {
					t.Fatal("stale input executed")
				}
				if !reflect.DeepEqual(before, pa315Tree(t, root)) {
					t.Fatal("originals changed")
				}
			})
		}
	}
}

func TestPythonApplicationExecutionModuleCancellation(t *testing.T) {
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		source := pa321State + pa321Worker + "\nasync def forever(request, *, status):\n    await asyncio.Event().wait()\n"
		driver := strings.ReplaceAll(strings.Replace(pa321Driver, "timeout=2", "timeout=60", 1), "app.submit", "app.forever")
		_, p := pa321Fixture(t, source, driver)
		opts := pa318Options(t, real, new(int))
		execute := opts.Execute
		if execute == nil {
			execute = RunPythonApplicationCommand
		}
		var command PythonApplicationCommand
		opts.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
			command = c
			ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
			defer cancel()
			return execute(ctx, c)
		}
		s := pa315Prepare(t, p, opts)
		r, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
		pa318Reject(t, r, err)
		if _, err := os.Stat(command.OutputDir); !os.IsNotExist(err) {
			t.Fatal("canceled output was not removed", err)
		}
		s.Close()
		if _, err := os.Stat(command.RuntimeDir); !os.IsNotExist(err) {
			t.Fatal("closed runtime was not removed", err)
		}
	}
}

func TestPythonApplicationExecutionModuleAdditionalRefusals(t *testing.T) {
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		for name, body := range map[string]string{
			"caught cancellation": `async def submit(request, *, status):
    key = reserve(request)
    advance(key, status)
    try:
        raise asyncio.CancelledError()
    except BaseException:
        pass
`,
			"unfinished task": `async def submit(request, *, status):
    key = reserve(request)
    advance(key, status)
    asyncio.create_task(asyncio.sleep(30))
`,
			"unfinished target coroutine": `async def submit(request, *, status):
    advance(request, status=status)
`,
			"synchronous timeout": `def submit(request, *, status):
    time.sleep(30)
`,
			"failed auxiliary worker": `def broken():
    raise RuntimeError('worker failed')
async def submit(request, *, status):
    key = reserve(request)
    advance(key, status)
    threading.Thread(target=broken).start()
`,
			"caught StopIteration": `def submit(request, *, status):
    advance(reserve(request), status)
    try:
        raise StopIteration()
    except StopIteration:
        pass
`,
			"replacement decorator": `def register(fn):
    import types
    return types.FunctionType(fn.__code__, fn.__globals__, fn.__name__)
@register
def submit(request, *, status):
    advance(reserve(request), status)
`,
			"decorator replaces entry defaults": `def register(fn):
    fn.__defaults__ = (object(),)
    return fn
@register
def submit(request, upload=object(), *, status):
    advance(reserve(request), status)
`,
			"cleanup suppresses cancellation": `async def stubborn():
    while True:
        try:
            await asyncio.Event().wait()
        except BaseException:
            pass
async def submit(request, *, status):
    advance(reserve(request), status)
    asyncio.create_task(stubborn())
    await asyncio.sleep(0)
`,
		} {
			t.Run(name, func(t *testing.T) {
				source := pa321State + "def advance(key, status):\n    finish(key, status)\n" + body
				if name == "unfinished target coroutine" {
					source = pa321State + strings.Split(pa321Async, "@register\nasync def submit")[0] + body
				}
				_, p := pa321Fixture(t, source, strings.Replace(pa321Driver, "timeout=2", "timeout=0.2", 1))
				s := pa315Prepare(t, p, pa318Options(t, real, new(int)))
				ctx := pa321Context(t)
				r, err := s.Run(ctx, pa318Driver(t, s).Key)
				pa318Reject(t, r, err)
				if ctx.Err() != nil {
					t.Fatal("collector did not bound unfinished work")
				}
			})
		}
	}
}

// Re-sign deliberate corruption with the actual bootstrap key in this trusted
// executor wrapper. These cases exercise independent Go validation after MAC
// verification, rather than merely proving that an invalid MAC is refused.
func TestPythonApplicationExecutionModuleAuthenticatedFields(t *testing.T) {
	for name, corrupt := range map[string]func([]byte) []byte{
		"unknown form": func(b []byte) []byte {
			return bytes.ReplaceAll(b, []byte(`"form": "module"`), []byte(`"form": "other"`))
		},
		"missing form": func(b []byte) []byte { return bytes.ReplaceAll(b, []byte(`"form": "module", `), nil) },
		"invalid path": func(b []byte) []byte {
			return bytes.ReplaceAll(b, []byte(`tasks[*].status`), []byte(`tasks[-1].status`))
		},
		"duplicate nested key": func(b []byte) []byte {
			return bytes.ReplaceAll(b, []byte(`"count": 9007199254740993`), []byte(`"count": 1, "count": 9007199254740993`))
		},
		"unknown field": func(b []byte) []byte {
			return bytes.ReplaceAll(b, []byte(`"form": "module"`), []byte(`"form": "module", "extra": 1`))
		},
		"null path": func(b []byte) []byte {
			return bytes.ReplaceAll(b, []byte(`"first": "rows[0].value"`), []byte(`"first": null`))
		},
		"nonfinite number": func(b []byte) []byte { return bytes.ReplaceAll(b, []byte(`9007199254740993`), []byte(`1e9999`)) },
		"broken continuity": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"status": ["done"]`), []byte(`"status": [true]`), 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, p := pa321Fixture(t, pa321State+pa321Worker, pa321Driver)
			opts := pa318Options(t, false, new(int))
			execute := opts.Execute
			opts.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
				secret, err := os.ReadFile(filepath.Join(c.OutputDir, "authentication.key"))
				if err != nil {
					t.Fatal(err)
				}
				r, err := execute(ctx, c)
				if err != nil || r.ExitCode != 0 {
					t.Fatalf("baseline execution failed: %v %s", err, r.Stderr)
				}
				end := bytes.LastIndexByte(r.Events[:len(r.Events)-1], '\n') + 1
				body := corrupt(bytes.Clone(r.Events[:end]))
				if bytes.Equal(body, r.Events[:end]) {
					t.Fatal("corruption did not match emitted JSON")
				}
				mac := hmac.New(sha256.New, secret)
				mac.Write(body)
				footer, _ := json.Marshal(map[string]string{"type": "authentication", "hmac_sha256": hex.EncodeToString(mac.Sum(nil))})
				r.Events = append(append(body, footer...), '\n')
				return r, nil
			}
			s := pa315Prepare(t, p, opts)
			r, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
			pa318Reject(t, r, err)
			if err != nil && strings.Contains(err.Error(), "authentication") {
				t.Fatal("did not reach independent field validation", err)
			}
		})
	}
}

func TestPythonApplicationModuleModelEncoding(t *testing.T) {
	state := map[string]any{"rows": []any{map[string]any{"n": json.Number("9007199254740993"), "items": []any{true, "x"}}}}
	before := pa321JSON(t, state)
	value, err := pythonApplicationModelValue(state)
	if err != nil || pa321JSON(t, value) != `{"rows":{"$seq":[{"items":{"$seq":[true,"x"]},"n":9007199254740993}]}}` {
		t.Fatal(value, err)
	}
	if before != pa321JSON(t, state) {
		t.Fatal("TLC conversion mutated raw evidence")
	}
	for _, value := range []any{nil, json.Number("1.5"), map[string]any{"$seq": []any{}}, map[string]any{"$mv": "done"}, map[string]any{"$fn": []any{}}, map[string]any{"$set": []any{}}, map[string]any{"bad-key": true}} {
		if _, err := pythonApplicationModelValue(value); err == nil {
			t.Fatal("application value gained conformance semantics", value)
		}
	}
}
