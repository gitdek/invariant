package verify

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPythonApplicationExecutionModuleReturnEvents(t *testing.T) {
	// Exercise the 3.9 and 3.13 trace offsets even when the test host uses
	// another interpreter. These are CPython wordcode bytes, not executable
	// code, under a stand-in opcode table.
	const probe = `import sys, types
observer = {}
exec(compile(sys.stdin.read(), 'invariant_observe.py', 'exec'), observer)
observer['dis'] = types.SimpleNamespace(opname={
    72: 'YIELD_FROM', 83: 'RETURN_VALUE', 100: 'LOAD_CONST',
    130: 'RAISE_VARARGS', 131: 'CALL_FUNCTION',
    86: 'YIELD_VALUE', 121: 'RETURN_CONST', 149: 'RESUME',
})
class Frame:
    pass
for version, code, offset, outcome in [
    ((3, 9), [100, 0, 72, 0, 83, 0], 0, 'suspended'),
    ((3, 9), [100, 0, 72, 0, 83, 0], 2, 'suspended'),
    ((3, 10), [100, 0, 72, 0, 83, 0], 2, 'suspended'),
    ((3, 10), [100, 0, 72, 0, 83, 0], 0, 'failed'),
    ((3, 9), [100, 0, 83, 0], 2, 'completed'),
    ((3, 10), [100, 0, 83, 0], 2, 'completed'),
    ((3, 9), [100, 0, 130, 1], 2, 'failed'),
    ((3, 10), [100, 0, 130, 1], 2, 'failed'),
    ((3, 9), [131, 0, 83, 0], 0, 'failed'),
    ((3, 10), [131, 0, 83, 0], 0, 'failed'),
    # 3.13 reports a yield at the RESUME after it: oparg 1 follows a yield,
    # 3 an await. RESUME 0 starts a function, so it isn't a suspension.
    ((3, 13), [86, 0, 149, 1], 2, 'suspended'),
    ((3, 13), [86, 0, 149, 3], 2, 'suspended'),
    ((3, 13), [149, 0, 121, 0], 0, 'failed'),
    ((3, 13), [149, 0, 121, 0], 2, 'completed'),
    ((3, 14), [86, 0, 149, 1], 0, 'suspended'),
]:
    observer['sys'] = types.SimpleNamespace(version_info=version)
    frame = Frame()
    frame.f_code = types.SimpleNamespace(co_code=bytes(code), co_flags=0x80)
    frame.f_lasti = offset
    life = object.__new__(observer['_ModuleLifecycle'])
    life.check_target = lambda: None
    life.entry = life.target = types.SimpleNamespace(code=object())
    life.entry_frame = life.target_frame = None
    life.coroutines = {frame}
    try:
        life.observe(frame, 'return', None)
    except ValueError as error:
        assert outcome == 'failed', (version, code, offset, error)
        assert str(error) == 'module coroutine failed or was cancelled'
    else:
        assert outcome != 'failed', (version, code, offset)
        assert (frame in life.coroutines) == (outcome == 'suspended')
`
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(pa321Context(t), python, "-I", "-S", "-c", probe)
	cmd.Stdin = bytes.NewReader(pythonApplicationObserver)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("coroutine return events: %v\n%s", err, out)
	}
}

func TestPythonApplicationExecutionModuleSuspension(t *testing.T) {
	const body = `async def pause():
    await asyncio.sleep(0)
    await asyncio.sleep(0.001)
    await asyncio.sleep(0)
async def advance(request, *, status):
    await pause()
    finish(reserve(request), status)
    await pause()
async def submit(request, *, status):
    await advance(request, status=status)
`
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		t.Run(map[bool]string{false: "host", true: "Docker"}[real], func(t *testing.T) {
			// Repeated suspensions in the target and its helper must still
			// produce one completed call per step, with portable copied state.
			pa321Positive(t, real, pa321State+body)
			for name, failure := range map[string]string{
				"exception":    "raise RuntimeError('after suspension')",
				"cancellation": "raise asyncio.CancelledError()",
			} {
				t.Run(name, func(t *testing.T) {
					// Even a helper failure caught by the target poisons the
					// execution; a return with None is not always suspension.
					source := strings.Replace(body, "    await asyncio.sleep(0.001)", "    "+failure, 1)
					source = strings.ReplaceAll(source, "    await pause()", "    try:\n        await pause()\n    except BaseException:\n        pass")
					_, p := pa321Fixture(t, pa321State+source, pa321Driver)
					s := pa315Prepare(t, p, pa318Options(t, real, new(int)))
					r, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
					pa318Reject(t, r, err)
				})
			}
		})
	}
}
