package verify

import (
	"os"
	"testing"
)

// The observer registers a worker before Thread.start, so a step's join can
// meet one that another worker is still starting, and Thread.join refuses a
// thread that hasn't started. A loaded machine shows it (#405). Here each of
// eight workers starts one more, held unstarted for a different time, so the
// entry's thread reaches its join with registered workers yet to start.
func TestPythonApplicationExecutionModuleJoinsOnlyStartedWorkers(t *testing.T) {
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		t.Run(map[bool]string{false: "local", true: "Docker"}[real], func(t *testing.T) {
			pa321Positive(t, real, pa321State+pa405HeldStarts)
		})
	}
}

const pa405HeldStarts = `@register
def advance(key, status):
    finish(key, status)
def idle():
    pass
class Held(threading.Thread):
    def __init__(self, delay, **kwargs):
        super().__init__(**kwargs)
        self.delay = delay
    def _bootstrap_inner(self):
        time.sleep(self.delay)
        super()._bootstrap_inner()
def spawn(key, status, i):
    if i == 1:
        Held(0.02 * i, target=advance, args=(key, status)).start()
    else:
        Held(0.02 * i, target=idle).start()
@register
async def submit(request, *, status):
    await asyncio.sleep(0)
    key = reserve(request)
    for i in range(1, 9):
        threading.Thread(target=spawn, args=(key, status, i)).start()
    await asyncio.sleep(0.001)
`
