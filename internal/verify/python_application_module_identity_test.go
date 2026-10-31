package verify

import (
	"os"
	"testing"
)

func TestPythonApplicationExecutionModuleRepeatedSource(t *testing.T) {
	const source = `values = []
def advance():
    values.append('done')
def submit():
    advance()
`
	const driver = `import importlib.util, sys
from invariant_observe import module
from tasks import lifecycle as app
spec = importlib.util.spec_from_file_location('tasks.alternate', app.__file__)
second = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = second
spec.loader.exec_module(second)
def drive():
    with module(app.advance, fields={'value':'values'}) as a:
        with module(second.advance, fields={'value':'values'}) as b:
            a.step(app.submit, expected_after={'value':['done']})
            b.step(second.submit, expected_before={'value':[]}, expected_after={'value':['done']})
if __name__ == '__main__': drive()
`
	for _, real := range []bool{false, true} {
		if real && os.Getenv("INVARIANT_PYTHON_APPLICATION_EXECUTION_REAL") != "1" {
			continue
		}
		_, p := pa321Fixture(t, source, driver)
		s := pa315Prepare(t, p, pa318Options(t, real, new(int)))
		r := pa318Success(t, pa321Context(t), s)
		if len(r.Observations) != 4 {
			t.Fatal(r.Observations)
		}
		for i, identity := range []int{1, 2, 1, 2} {
			if r.Observations[i].Instance != identity || r.Observations[i].Target != r.Observations[0].Target {
				t.Fatal("module identity was confused with source identity", r.Observations)
			}
		}
	}
}
