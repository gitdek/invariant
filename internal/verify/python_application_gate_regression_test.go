package verify_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

func TestPythonApplicationGateRegression(t *testing.T) {
	t.Run("local transport", func(t *testing.T) {
		tc, _ := pa327Fake(t)
		pa327Additional(t, tc, false)
	})
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") == "1" {
		t.Run("real transport and dependency resolution", func(t *testing.T) {
			tc, err := toolchain.Ensure(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			pa327Additional(t, tc, true)
			f := pa327New(t, false, true)
			first := pa327Run(t, f, tc)
			pa327Assert(t, f, first, true)
			pa327Write(t, f.root, "deps/constraints.txt", "packaging==24.1\n")
			second := pa327Run(t, f, tc)
			if !second.Passed || first.Fingerprint == second.Fingerprint {
				t.Fatal("actual dependency resolution not bound")
			}
			for _, x := range pa327Application(t, second).Executions {
				if x.Provenance.Dependencies.Artifacts[0].Version != "24.1" {
					t.Fatal("stale dependency resolution")
				}
			}
		})
	}
}

func pa327Additional(t *testing.T, tc toolchain.Toolchain, wheel bool) {
	t.Helper()
	t.Run("existing instance wrapper binds its delegate", func(t *testing.T) {
		f := pa327New(t, true, wheel)
		app := "from .lifecycle import advance\ndef start(): return 'pending'\ndef tick(state): return advance(state)\nclass Task:\n    def __init__(self): self.state = 'pending'\n    def advance(self): self.state = advance(self.state)\nclass Client:\n    def __init__(self, task): self.task = task\n    def tick(self): self.task.advance()\n"
		pa327Write(t, f.root, "tasks/application.py", app)
		pa327Write(t, f.root, pa327DriverPath, "from invariant_observe import instance\nfrom tasks.application import Task, Client\ndef drive():\n    task = Task()\n    with instance(task.advance, fields={'task':'state'}) as life:\n        life.step(Client(task).tick)\nif __name__ == '__main__': drive()\n")
		f.p.Manifest.Components.Checks[0].Target = pa327Ref("tasks/application.py", "Task.advance")
		pa327Save(t, f)
		if r := pa327Run(t, f, tc); !r.Passed {
			t.Fatalf("existing wrapper rejected: %s", pa327JSON(t, r))
		}
		pa327Write(t, f.root, "tasks/application.py", strings.Replace(app, "self.state = advance(self.state)", "self.state = 'running'", 1))
		if r := pa327Run(t, f, tc); r.Passed {
			t.Fatal("import-only delegate received evidence")
		}
	})
	t.Run("legacy directories and multiple selected sources", func(t *testing.T) {
		for _, selected := range [][]string{{"tasks"}, {pa327Source, "tasks/constants.py"}} {
			f := pa327New(t, false, wheel)
			f.p.Manifest.Existing = selected
			pa327Save(t, f)
			before := pa327Tree(t, f.root)
			r := pa327Run(t, f, tc)
			pa327Assert(t, f, r, true)
			if !reflect.DeepEqual(before, pa327Tree(t, f.root)) {
				t.Fatal("selected sources changed")
			}
		}
	})
	t.Run("independent targets and invocations", func(t *testing.T) {
		f := pa327New(t, true, wheel)
		pa327Write(t, f.root, "tasks/second.py", "def advance(state): return 'running' if state == 'pending' else 'done'\n")
		pa327Write(t, f.root, "tasks/second_application.py", "from .second import advance\ndef start(): return 'pending'\ndef tick(state): return advance(state)\n")
		second := strings.ReplaceAll(strings.ReplaceAll(pa327Driver, "tasks.lifecycle", "tasks.second"), "from tasks import application", "from tasks import second_application as application")
		second = strings.ReplaceAll(second, "drive", "drive_second")
		// Each declared invocation executes only its selected lexical driver.
		first := strings.Split(pa327Driver, "if __name__")[0]
		second = strings.ReplaceAll(strings.Split(second, "if __name__")[0], "application", "second_app")
		second = strings.ReplaceAll(second, "second_second_app", "second_application")
		// Separate globals prevent the second import from replacing the first target.
		second = strings.ReplaceAll(second, "import advance", "import advance as advance_second")
		second = strings.ReplaceAll(second, "functional(advance,", "functional(advance_second,")
		pa327Write(t, f.root, pa327DriverPath, first+second+"\nif __name__ == '__main__':\n    import sys\n    if sys.argv[1] == 'second': drive_second()\n    else: drive()\n")
		f.p.Manifest.Existing = []string{pa327Source, "tasks/second.py"}
		c := f.p.Manifest.Components.Checks[0]
		c.Target = pa327Ref("tasks/second.py", "advance")
		c.Tests, c.Assumptions = nil, nil
		c.Conformance = []project.ComponentRef{pa327Ref(pa327DriverPath, "drive_second")}
		c.Invocations = []project.ComponentInvocation{c.Invocations[1]}
		c.Invocations[0].Check = c.Conformance[0]
		c.Invocations[0].Args = []string{"driver.py", "second"}
		f.p.Manifest.Components.Checks = append(f.p.Manifest.Components.Checks, c)
		pa327Save(t, f)
		r := pa327Run(t, f, tc)
		if !r.Passed || len(r.Components.Conformance) != 2 {
			t.Fatalf("independent targets: %s", pa327JSON(t, r))
		}
		pa327Write(t, f.root, "tasks/second.py", "def advance(state): return 'broken'\n")
		r = pa327Run(t, f, tc)
		if r.Passed || len(r.Components.Conformance) != 2 {
			t.Fatal("failed target lost records")
		}
		checked := 0
		for _, c := range r.Components.Conformance {
			if c.TransitionsChecked {
				checked++
			}
		}
		if checked != 1 || len(r.Components.Runtime) != 1 {
			t.Fatal("independent success erased")
		}
	})
	t.Run("prepared snapshot and cancellation cleanup", func(t *testing.T) {
		f := pa327New(t, false, wheel)
		stage := filepath.Join(t.TempDir(), "stage")
		var runtime, output string
		s, err := verify.PreparePythonApplicationSession(context.Background(), f.p, stage, verify.PythonApplicationSessionOptions{
			Execute: func(ctx context.Context, c verify.PythonApplicationCommand) (verify.PythonApplicationProcess, error) {
				runtime, output = c.RuntimeDir, c.OutputDir
				pa327Write(t, f.root, "tasks/next.txt", "broken\n")
				return verify.RunPythonApplicationCommand(ctx, c)
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		for _, v := range s.Invocations() {
			x, err := s.Run(context.Background(), v.Key)
			if err != nil || !x.Success {
				t.Fatalf("staged execution: %+v %v", x, err)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("output leaked")
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := s.Run(ctx, s.Invocations()[0].Key); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(runtime); !os.IsNotExist(err) {
			t.Fatal("private runtime leaked")
		}
		if _, err := os.Stat(stage); err != nil {
			t.Fatal("caller-owned snapshot removed")
		}
	})
	t.Run("unsupported binding cannot earn conformance", func(t *testing.T) {
		f := pa327New(t, false, wheel)
		pa327Write(t, f.root, pa327Source, "async def advance(state): return 'running'\n")
		r := pa327Run(t, f, tc)
		if r.Passed {
			t.Fatal("async binding passed")
		}
		for _, c := range r.Application.Conformance {
			if c.Result.Passed {
				t.Fatal("unsupported binding received TLC credit")
			}
		}
	})
	t.Run("attempted and exhaustive output cannot replace readback", func(t *testing.T) {
		for _, output := range []string{`{"attempts": [[0,"Next",[],1]],"states":[{"task":"pending"},{"task":"running"}],"init":[0]}`, `{"exhaustive":true,"traces":[[{"task":"pending"},{"task":"running"}]]}`} {
			f := pa327New(t, true, wheel)
			literal, _ := json.Marshal(output)
			pa327Write(t, f.root, pa327DriverPath, "def drive():\n    print("+string(literal)+")\nif __name__ == '__main__': drive()\n")
			r := pa327Run(t, f, tc)
			if r.Passed || len(r.Components.Runtime) != 1 {
				t.Fatal("unsupported stream gained evidence or erased tests")
			}
			for _, c := range r.Application.Conformance {
				if c.Result.Passed || len(c.Checks) != 0 {
					t.Fatal("unchecked output reached TLC")
				}
			}
		}
	})
	t.Run("missing selected image cannot fall back", func(t *testing.T) {
		f := pa327New(t, false, wheel)
		calls := 0
		s, err := verify.PreparePythonApplicationSession(context.Background(), f.p, filepath.Join(t.TempDir(), "stage"), verify.PythonApplicationSessionOptions{
			Execute: func(ctx context.Context, c verify.PythonApplicationCommand) (verify.PythonApplicationProcess, error) {
				calls++
				if !wheel {
					return verify.PythonApplicationProcess{ExitCode: 125}, errors.New("selected image unavailable")
				}
				c.Image = "sha256:" + strings.Repeat("0", 64)
				return verify.RunPythonApplicationCommand(ctx, c)
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		x, err := s.Run(context.Background(), s.Invocations()[0].Key)
		if err == nil || x == nil || x.Success || x.Complete || calls != 1 {
			t.Fatalf("missing image fallback: %+v %v calls=%d", x, err, calls)
		}
	})
	t.Run("every application field renders literally", func(t *testing.T) {
		f := pa327New(t, false, wheel)
		r := pa327Run(t, f, tc)
		var raw map[string]any
		if err := json.Unmarshal(pa327JSON(t, r), &raw); err != nil {
			t.Fatal(err)
		}
		hostile := "<script>gate327</script> [link](https://evil.invalid) | `value`\n<!-- injected -->"
		var replace func(any)
		replace = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				for k, v := range x {
					if _, ok := v.(string); ok {
						x[k] = hostile
					} else {
						replace(v)
					}
				}
			case []any:
				for i, v := range x {
					if _, ok := v.(string); ok {
						x[i] = hostile
					} else {
						replace(v)
					}
				}
			}
		}
		replace(raw["application"])
		var decoded verify.Report
		if err := json.Unmarshal(pa327JSON(t, raw), &decoded); err != nil {
			t.Fatal(err)
		}
		md := receipt.Markdown(&decoded)
		if strings.Contains(md, "<script>") || strings.Contains(md, "[link](https://evil.invalid)") || strings.Contains(md, "<!-- injected -->") || !strings.Contains(md, "gate327") {
			t.Fatal("unsafe or hidden application fields")
		}
	})
}
