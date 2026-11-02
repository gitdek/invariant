package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/toolchain"
)

// This transport double executes the actual Python runner and records TLC
// inputs. Its three-state answers are fixture answers, not real TLC evidence.
// The opt-in subtest below checks those obligations with the pinned tools.
func hold393GateFake(t *testing.T) (toolchain.Toolchain, string) {
	t.Helper()
	tc, log := refusal392GateFake(t)
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(filepath.Dir(docker), "docker.py")
	b, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(string(b), "passed(2,2)", "passed(3,3)", 1)
	if body == string(b) {
		t.Fatal("three-state transport fixture not installed")
	}
	if err := os.WriteFile(filename, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return tc, log
}

func TestPythonApplicationGateHeldCalls(t *testing.T) {
	hold393API(t)
	run := func(t *testing.T, tc toolchain.Toolchain, log string, real bool) {
		for _, explicit := range []bool{true, false} {
			t.Run(fmt.Sprintf("explicit=%v", explicit), func(t *testing.T) {
				f := hold393Fixture(t, explicit, hold393Source, hold393Driver)
				r := pa342Unchanged(t, f, tc)
				if !r.Passed {
					b, _ := json.Marshal(r)
					t.Fatalf("held-call gate failed: %s", b)
				}
				x, c := pa342Explored(t, r)
				hold393Assert(t, x)
				if !c.passed() || !c.Result.Exhaustive || c.Result.States != 3 || c.Result.ModelStates != 3 || c.Result.Tried == nil || !c.Result.Tried.Passed || c.Result.Tried.Attempts != 9 {
					t.Fatalf("queued state or attempt coverage lost: %+v", c)
				}
				if explicit {
					row := pa342Row(t, r)
					if !row.TransitionsChecked || !row.EveryStep || !row.ExhaustiveStates {
						t.Fatalf("held exploration lost qualification: %+v", row)
					}
				}
				if r.EverySize() || r.Claim() == "proved at every size" {
					t.Fatal("held exploration claimed proof")
				}
				if !real {
					b, err := os.ReadFile(log)
					if err != nil {
						t.Fatal(err)
					}
					var inputs strings.Builder
					for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
						var row struct{ Kind, Body string }
						if err := json.Unmarshal([]byte(line), &row); err != nil {
							t.Fatal(err)
						}
						if row.Kind == "obligation" {
							inputs.WriteString(row.Body)
							inputs.WriteByte('\n')
						}
					}
					for _, fragment := range []string{"active \\in 0..1", "Full == count = 1 /\\ active = 1", "Submit(", "Finish", "ENABLED"} {
						if !strings.Contains(inputs.String(), fragment) {
							t.Fatalf("TLC never received %q", fragment)
						}
					}
				}
			})
		}
		if real {
			t.Run("TLC rejects a model that forbids Finish", func(t *testing.T) {
				f := hold393Fixture(t, true, hold393Source, hold393Driver)
				// The application still releases to finished; this model
				// forbids that transition and must fail actual conformance.
				pa342Edit(t, f, pa342ModelPath, "Finish == active = 1", "Finish == FALSE /\\ active = 1")
				// Changing pinned fixture bytes deliberately fails the gate;
				// re-pin fixture statements so the test reaches model checking.
				b, err := os.ReadFile(filepath.Join(f.root, pa342ModelPath))
				if err != nil {
					t.Fatal(err)
				}
				f = refusal392Fixture(t, true, hold393Source, hold393Driver, string(b))
				pa342Write(t, f.root, "tests/test_counter.py", "import unittest\nfrom counter import core\nclass CounterTests(unittest.TestCase):\n    def test_inc(self):\n        core.reset()\n        self.assertEqual(core.counts['active'], 0)\n")
				r := pa342Unchanged(t, f, tc)
				x, c := pa342Explored(t, r)
				if !x.Success {
					t.Fatalf("model mismatch must preserve successful execution: %+v", x)
				}
				if r.Passed || c.passed() {
					t.Fatal("real TLC accepted forbidden Finish")
				}
			})
		}
	}
	t.Run("actual Python and recording TLC transport", func(t *testing.T) { tc, log := hold393GateFake(t); run(t, tc, log, false) })
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") == "1" {
		t.Run("real Docker Python and TLC", func(t *testing.T) {
			tc, err := toolchain.Ensure(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			run(t, tc, "", true)
		})
	}
}
