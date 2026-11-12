package verify

import (
	"bytes"
	"os/exec"
	"testing"
)

// The scan for coroutines a module entry left unfinished calls nothing an
// application can rebind after the collector loads (#882). Read through its
// module at each use, operator.is_ was the application's own function by
// then, called once for every object on the heap, and one that answers False
// hid every coroutine.
func TestPythonApplicationCoroutineScanCallsNothingRebound882(t *testing.T) {
	const probe = `import builtins, gc, inspect, itertools, operator, sys, types
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

pending = [native(), legacy(), plain()]
calls = []

def never(*args):
    calls.append('called')
    return False

def nothing(*args):
    calls.append('called')
    return []

# What an application could leave behind, in the modules the scan read from
# and in the collector's own namespace.
rebound = [(operator, 'is_', never), (gc, 'get_objects', nothing), (types, 'CoroutineType', int), (types, 'GeneratorType', int),
           (inspect, 'CO_ITERABLE_COROUTINE', 0), (itertools, 'compress', nothing), (itertools, 'repeat', nothing),
           (builtins, 'type', never), (builtins, 'map', nothing), (builtins, 'list', nothing), (builtins, 'set', nothing)]
kept = [(module, name, getattr(module, name)) for module, name, _ in rebound]
names = dict(observer)
try:
    for module, name, value in rebound:
        setattr(module, name, value)
    for name in ('compress', 'repeat'):
        observer[name] = nothing
    found = scan()
finally:
    for module, name, value in kept:
        setattr(module, name, value)
    observer.update(names)

assert not calls, len(calls)
assert pending[0] in found and pending[1] in found and pending[2] not in found, found
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
