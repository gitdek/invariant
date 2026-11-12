package verify

import (
	"strings"
	"testing"
)

// A module step fails when its entry leaves a callback scheduled on the
// event loop (#871). Closing the loop discards it, so the step's
// after-state would leave out what a server's running loop goes on to do.
// A callback that runs before the entry's awaitable finishes is part of
// the step, and a cancelled timer does nothing.
func TestPythonApplicationModuleScheduledCallbacks871(t *testing.T) {
	t.Parallel()
	source := func(schedule string) string {
		return pa321State + `def late(key):
    tasks[key]['status'] = 'expired'
def again(key):
    asyncio.get_running_loop().call_soon(late, key)
def advance(key, status):
    finish(key, status)
async def submit(request, *, status):
    key = reserve(request)
    loop = asyncio.get_running_loop()
` + schedule + `
    advance(key, status)
`
	}
	for _, tc := range []struct {
		name, schedule string
		left           bool
		status         string
	}{
		{"a timer that fires after the entry returns", "    loop.call_later(0.05, late, key)", true, ""},
		{"a timer set for a moment that never comes in the step", "    loop.call_at(loop.time() + 3600, late, key)", true, ""},
		{"a callback scheduled from the loop's last batch", "    loop.call_soon(again, key)", true, ""},
		// The timer is due by the time the entry returns, so it runs in the
		// step, after the selected call, and the step's state holds its effect.
		{"a timer that fires inside the step", "    loop.call_later(0, late, key)", false, "expired"},
		{"a cancelled timer", "    loop.call_later(3600, late, key).cancel()", false, "done"},
		{"no callback", "    pass", false, "done"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			driver := strings.Replace(pa321Driver, "        life.step(app.submit, {'upload': object()}, status='other')\n", "", 1)
			_, p := pa321Fixture(t, source(tc.schedule), driver)
			s := pa315Prepare(t, p, pa318Options(t, false, new(int)))
			r, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
			if tc.left {
				pa318Reject(t, r, err)
				if r.HarnessError != "module entry left scheduled callbacks on its event loop" {
					t.Fatalf("want the step refused for the callback it left, got %q: %q, %v", r.HarnessError, r.Diagnostics, err)
				}
				return
			}
			if err != nil || !r.Success || len(r.Observations) != 2 {
				t.Fatalf("a step that leaves nothing on its loop didn't pass: %+v, %v", r, err)
			}
			after := pa321JSON(t, r.Observations[1].After)
			if !strings.Contains(after, `"`+tc.status+`"`) {
				t.Fatalf("the step's after-state is %s, want its task %s", after, tc.status)
			}
		})
	}
}
