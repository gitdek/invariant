package verify

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/toolchain"
)

func TestPythonApplicationPerCheckDriverStartup884(t *testing.T) {
	pa342Modes(t, func(t *testing.T, tc toolchain.Toolchain, real bool) {
		f := fixture884(t, real, false)
		// Neither selected driver is the manifest driver. Tests still need
		// its directory at startup, while each driver needs its own sibling.
		f.p.Manifest.Conformance = "startup/manifest.py"
		pa342Write(t, f.p.Dir, "startup/manifest.py", "raise AssertionError('unselected manifest driver ran')\n")
		pa342Write(t, f.p.Dir, "startup/startup_support884.py", "NAME = 'manifest startup'\n")
		tests := string(read884(t, filepath.Join(f.root, "tests/test_counter.py")))
		pa342Write(t, f.root, "tests/test_counter.py", "import startup_support884\nassert startup_support884.NAME == 'manifest startup'\n"+tests)
		pa342Save(t, f)
		admit884(t, f)
		assertReport884(t, pa342Unchanged(t, f, tc), false)
	})
}

func TestPythonApplicationPerCheckDriverPartialBounds884(t *testing.T) {
	pa342Modes(t, func(t *testing.T, tc toolchain.Toolchain, real bool) {
		f := fixture884(t, real, false)
		model := strings.Replace(pa342Model, "CONSTANT Limit", "CONSTANT Limit, Other", 1)
		pa342Write(t, f.root, pa342ModelPath, model)
		f.p.Lock.Bounds["Other"] = "2"
		for i := range f.p.Lock.Statements {
			s := &f.p.Lock.Statements[i]
			h, err := tla.PinHashWith(model, map[string]string{"Local.tla": "---- MODULE Local ----\nHelper == TRUE\n====\n"}, s.Name, project.Model)
			if err != nil {
				t.Fatal(err)
			}
			s.SHA256 = h
		}
		pa342Edit(t, f, secondDriver884, "Limit = 2", "Limit = 2\nOther = 2")
		pa342Save(t, f)
		r := pa342Unchanged(t, f, tc)
		if !r.Passed || r.Larger == nil || !r.Larger.Passed || len(r.Application.Larger) != 1 {
			t.Fatal("partial bounds blocked a sibling or acquired a larger record")
		}
		l := r.Application.Larger[0]
		if l.Key.ID != "second" || l.Original.Path != secondDriver884 || l.Bounds["Other"] != "3" || l.Execution == nil || !l.Execution.Success {
			t.Fatalf("wrong invocation ran with all larger bounds: %+v", l)
		}
	})
}
