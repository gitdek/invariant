package verify

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPythonApplicationExecutionDeclaredRefusalLegacyForms(t *testing.T) {
	for _, instance := range []bool{false, true} {
		name := "functional"
		source := `class Refused(Exception):
    pass
def initial():
    return 0
def increment(count):
    return 1
def submit(count):
    if count:
        raise Refused()
    return increment(count)
def read(count):
    return count
`
		driver := `from invariant_observe import Refusal, Step, explore, functional
from counter import core as app
def start():
    return functional(app.increment, app.initial, argument='count', field='count')
def submit(life, state):
    life.step(app.submit)
def read(life, state):
    life.step(app.read)
def drive():
    explore(start, [Step('Submit', submit, refusal=Refusal(app.Refused)), Step('Read', read, refusal=Refusal(app.Refused))])
if __name__ == '__main__': drive()
`
		if instance {
			name = "instance"
			source = `class Refused(Exception):
    pass
class Counter:
    def __init__(self):
        self.count = 0
    def increment(self):
        self.count = 1
    def submit(self):
        if self.count:
            raise Refused()
        self.increment()
    def read(self):
        return self.count
`
			driver = strings.ReplaceAll(driver, "explore, functional", "explore, instance")
			driver = strings.Replace(driver, "def start():\n    return functional(app.increment, app.initial, argument='count', field='count')", "CURRENT = []\ndef start():\n    counter = app.Counter()\n    CURRENT[:] = [counter]\n    return instance(counter.increment, fields={'count':'count'})", 1)
			driver = strings.ReplaceAll(strings.ReplaceAll(driver, "app.submit", "CURRENT[0].submit"), "app.read", "CURRENT[0].read")
		}
		t.Run(name, func(t *testing.T) {
			f := refusal392Fixture(t, true, source, driver, refusal392Model)
			if instance {
				f.p.Manifest.Components.Checks[0].Target.Symbol = "Counter.increment"
				pa342Save(t, f)
			}
			s := pa315Prepare(t, f.p, pa318Options(t, false, new(int)))
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			x, err := s.Run(ctx, pa318Driver(t, s).Key)
			if err != nil || x == nil || !x.Success || !x.Complete || x.Exploration == nil {
				t.Fatalf("declared %s exploration: %+v, %v", name, x, err)
			}
			wire := refusal392Wire(t, x.Exploration)
			if x.Exploration.States != 2 || x.Exploration.Attempts != 4 || x.Exploration.Refusals != 1 || wire["declared_refusals"] != json.Number("1") {
				t.Fatalf("wrong strict outcomes: %v", wire)
			}
		})
	}
}

func TestPythonApplicationExecutionDeclaredRefusalCaughtAndDetached(t *testing.T) {
	t.Run("caught entry exception is normal acceptance", func(t *testing.T) {
		source := strings.Replace(refusal392Source, "raise Refused(404)", "try:\n                raise Refused(404)\n            except Refused:\n                return counts['value']", 1)
		x := refusal392Execution(t, false, source, refusal392Driver, false)
		if !x.Success || x.Exploration == nil || x.Exploration.Refusals != 5 {
			t.Fatalf("caught exception is not a refusal: %+v", x)
		}
		for _, row := range x.Observations {
			if row.Kind == "transition" && row.Step == "Read" {
				wire := refusal392Wire(t, row)
				if wire["outcome"] != "accepted" || wire["refusal"] != nil {
					t.Fatalf("caught exception became refusal: %v", wire)
				}
			}
		}
	})
	getter := strings.Replace(refusal392Source, "self.status_code = status_code", "self._status_code = status_code", 1)
	getter = strings.Replace(getter, "class Child", "    @property\n    def status_code(self):\n        try:\n            increment(0)\n        except Exception:\n            pass\n        return self._status_code\nclass Child", 1)
	for _, c := range []struct{ name, source, driver string }{
		{"failed detached task", strings.Replace(refusal392Source, "raise Refused(409)", "asyncio.create_task(deny())\n            await asyncio.sleep(0)\n            raise Refused(409)", 1) + "\nasync def deny():\n    raise Refused(409)\n", refusal392Driver},
		{"scalar subclass", strings.Replace(refusal392Source, "raise Refused(409)", "raise Refused(Status(409))", 1) + "\nclass Status(int):\n    pass\n", refusal392Driver},
		{"caught collector error", strings.Replace(refusal392Source, "counts['value'] = value", "raise Refused(409)", 1), strings.Replace(refusal392Driver, "    life.step(app.submit, value)", "    try:\n        life.step(app.submit, value)\n    except Exception:\n        pass", 1)},
		{"attribute getter catches collector error", getter, refusal392Driver},
	} {
		t.Run(c.name, func(t *testing.T) {
			x := refusal392Execution(t, false, c.source, c.driver, false)
			if x.Success || x.Complete || x.Exploration != nil || len(x.Observations) != 0 || len(x.Diagnostics) == 0 {
				t.Fatalf("invalid refusal earned evidence: %+v", x)
			}
		})
	}
}
