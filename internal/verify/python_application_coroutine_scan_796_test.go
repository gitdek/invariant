package verify

import (
	"bytes"
	"os/exec"
	"testing"
)

func TestPythonApplicationCoroutineScan796(t *testing.T) {
	// Every module step scans the heap for leftover coroutines while the
	// collector's hooks are installed. The scan must cost no hook event per
	// heap object, find the same coroutines an exact type test does, and run
	// no Python code of the objects it passes over.
	const probe = `import sys, types
observer = {}
exec(compile(sys.stdin.read(), 'invariant_observe.py', 'exec'), observer)
scan = observer['coroutine_objects']

async def native():
    pass

@types.coroutine
def legacy():
    yield

def plain():
    yield

class Watched:
    reads = 0
    def __getattribute__(self, name):
        type(self).reads += 1
        return object.__getattribute__(self, name)

class Disguised:
    reads = 0
    @property
    def __class__(self):
        Disguised.reads += 1
        return types.CoroutineType

pending = [native(), legacy(), plain()]
watched, disguised = Watched(), Disguised()
events = []

def trace(frame, event, arg):
    events.append(event)
    return trace

def profile(frame, event, arg):
    events.append(event)

def traced_scan(extra):
    padding = [[i] for i in range(extra)]
    del events[:]
    sys.settrace(trace)
    sys.setprofile(profile)
    try:
        found = scan()
    finally:
        sys.setprofile(None)
        sys.settrace(None)
    return found, len(events)

small, few = traced_scan(0)
large, many = traced_scan(100000)
for found in (small, large):
    assert pending[0] in found and pending[1] in found, found
    assert pending[2] not in found and disguised not in found, found
assert many - few < 20, (few, many)
assert Watched.reads == 0 and Disguised.reads == 0, (Watched.reads, Disguised.reads)
for coroutine in pending:
    coroutine.close()
`
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(pa321Context(t), python, "-I", "-S", "-c", probe)
	cmd.Stdin = bytes.NewReader(pythonApplicationObserver)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("coroutine scan: %v\n%s", err, out)
	}
}
