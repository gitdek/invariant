package verify

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
)

// A driver's lifecycle handle holds no state, so nothing reachable from it
// can stand in for the collector's records. Touching anything but step() and
// the context protocol poisons the execution, even when the driver catches
// the error. These run the production collector, in Docker when opted in.
func TestPythonApplicationExecutionHandleTampering(t *testing.T) {
	const head = "from invariant_observe import functional\nfrom tasks.lifecycle import advance\nfrom tasks import application\ndef drive():\n    with functional(advance, application.start, argument='state', field='task') as life:\n"
	const tail = "if __name__ == '__main__':\n    drive()\n"
	cases := []struct{ name, body, says string }{
		// The second review's reproduction: a substituted callback claims the
		// selected call while the entry never reaches it.
		{"replaced observation callback", "        def fake(frame, event, arg):\n            life.calls, life.after = 1, {'task': 'running'}\n        try:\n            life.observe = fake\n        except AttributeError:\n            pass\n        life.step(application.unused)\n", "lifecycle handles are read-only"},
		{"rebound target binding", "        try:\n            target = life.target\n            target.code, target.function = application.unused.__code__, application.unused\n        except AttributeError:\n            pass\n        life.step(application.tick)\n", "lifecycle handles expose no state"},
		{"replaced handle step", "        type(life).step = lambda self, entry, **kwargs: None\n        life.step(application.tick)\n", "empty lifecycle"},
		{"fabricated handle state", "        life.step(application.tick)\n        try:\n            life.state = {'task': 'pending'}\n        except AttributeError:\n            pass\n        life.step(application.tick)\n", "lifecycle handles are read-only"},
		{"deleted handle attribute", "        try:\n            del life.step\n        except AttributeError:\n            pass\n        life.step(application.tick)\n", "lifecycle handles are read-only"},
	}
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		t.Run(map[bool]string{false: "host", true: "Docker"}[real], func(t *testing.T) {
			pa318Positive(t, real, false, false)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					root, p := pa318Fixture(t, false, head+tc.body+tail, false)
					before := pa315Tree(t, root)
					selected := 0
					s := pa315Prepare(t, p, pa318Options(t, real, &selected))
					r, err := s.Run(context.Background(), pa318Driver(t, s).Key)
					pa318Reject(t, r, err)
					if r == nil || !strings.Contains(strings.Join(r.Diagnostics, "\n"), tc.says) {
						t.Fatalf("tampering rejected for an unrelated reason, want %q: %+v, %v", tc.says, r, err)
					}
					if selected != 1 || !reflect.DeepEqual(before, pa315Tree(t, root)) {
						t.Fatal("tampering refusal changed originals or reselected image")
					}
				})
			}
		})
	}
}
