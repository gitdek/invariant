package pythongatetest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// GateFailures runs every local gate refusal case, and also runs the real
// Docker/Python/TLC cases when real is true. The suite must remain serial because
// its local transport changes the process environment.
func GateFailures(t *testing.T, real bool) {
	t.Helper()
	t.Run("local Python", func(t *testing.T) {
		tc, _ := Fake(t)
		failureCases(t, tc, false)
		for _, bad := range []string{"missing", "malformed"} {
			t.Run(bad, func(t *testing.T) {
				f := New(t, true, false)
				Admit(t, f)
				t.Setenv("PA327_EVENTS", bad)
				r := Run(t, f, tc)
				Assert(t, f, r, false)
				if len(r.Components.Runtime) == 0 {
					t.Fatal("invalid conformance erased valid runtime test")
				}
			})
		}
	})
	if real {
		t.Run("real Docker Python TLC", func(t *testing.T) {
			tc, e := toolchain.Ensure(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			failureCases(t, tc, true)
		})
	}
}
func failureCases(t *testing.T, tc toolchain.Toolchain, wheel bool) {
	t.Helper()
	cases := []struct {
		name    string
		change  func(*testing.T, Fixture)
		runtime bool
	}{
		{"invalid transition", func(t *testing.T, f Fixture) {
			Write(t, f.Root, "tasks/helper.py", "def next_state(state):\n    return 'broken' if state == 'running' else 'running'\n")
		}, false},
		{"invalid initial", func(t *testing.T, f Fixture) {
			Write(t, f.Root, "tasks/application.py", "from .lifecycle import advance\ndef start(): return 'alien'\ndef tick(state): return advance(state)\n")
		}, false},
		{"unused target", func(t *testing.T, f Fixture) {
			Write(t, f.Root, DriverPath, "def drive():\n    print('{\"traces\":[[{\"task\":\"pending\"},{\"task\":\"running\"}]]}')\nif __name__=='__main__': drive()\n")
		}, false},
		{"import only", func(t *testing.T, f Fixture) {
			Write(t, f.Root, DriverPath, "from tasks.lifecycle import advance\ndef drive():\n    pass\nif __name__=='__main__': drive()\n")
		}, false},
		{"call plus substitute lifecycle", func(t *testing.T, f Fixture) {
			Write(t, f.Root, DriverPath, "from tasks.lifecycle import advance\ndef drive():\n    advance('pending')\n    print('{\"traces\":[[{\"task\":\"pending\"},{\"task\":\"done\"}]]}')\nif __name__=='__main__': drive()\n")
		}, false},
		{"entry substitutes return", func(t *testing.T, f Fixture) {
			Write(t, f.Root, "tasks/application.py", "from .lifecycle import advance\ndef start(): return 'pending'\ndef tick(state):\n    advance(state)\n    return 'done'\n")
		}, false},
		{"handle tampering", func(t *testing.T, f Fixture) {
			Write(t, f.Root, DriverPath, strings.Replace(Driver, "        life.step(application.tick)", "        try:\n            life.state = 'running'\n        except Exception:\n            pass\n        life.step(application.tick)", 1))
		}, false},
		{"driver import failure", func(t *testing.T, f Fixture) {
			Write(t, f.Root, DriverPath, "import missing_application_dependency\n"+Driver)
		}, false},
		{"driver runtime failure", func(t *testing.T, f Fixture) {
			Write(t, f.Root, DriverPath, strings.Replace(Driver, "    with functional", "    raise RuntimeError('driver failed')\n    with functional", 1))
		}, false},
		{"driver not invoked", func(t *testing.T, f Fixture) {
			Write(t, f.Root, DriverPath, strings.Split(Driver, "if __name__")[0])
		}, false},
		{"malformed output", func(t *testing.T, f Fixture) {
			Write(t, f.Root, DriverPath, "def drive(): print('{malformed}')\nif __name__=='__main__': drive()\n")
		}, false},
		{"test fails beside passing test", func(t *testing.T, f Fixture) {
			Write(t, f.Root, "tests/test_tasks.py", strings.Replace(TestSource, "self.assertEqual(application.tick('pending'), 'running')", "self.fail('selected test failed')", 1))
			if f.Project.Manifest.Components != nil {
				f.Project.Manifest.Components.Checks[0].Invocations[0].Args = []string{"-m", "unittest", "tests.test_tasks"}
			}
		}, true},
		{"test skipped", func(t *testing.T, f Fixture) {
			Write(t, f.Root, "tests/test_tasks.py", strings.Replace(TestSource, "    def test_pass", "    @unittest.skip('not exercised')\n    def test_pass", 1))
		}, true},
		{"test expected failure", func(t *testing.T, f Fixture) {
			s := strings.Replace(TestSource, "    def test_pass", "    @unittest.expectedFailure\n    def test_pass", 1)
			s = strings.Replace(s, "self.assertEqual(application.tick('pending'), 'running')", "self.fail('expected')", 1)
			Write(t, f.Root, "tests/test_tasks.py", s)
		}, true},
		{"test unexpected success", func(t *testing.T, f Fixture) {
			Write(t, f.Root, "tests/test_tasks.py", strings.Replace(TestSource, "    def test_pass", "    @unittest.expectedFailure\n    def test_pass", 1))
		}, true},
		{"selected test never run", func(t *testing.T, f Fixture) {
			Write(t, f.Root, "tests/test_tasks.py", TestSource+"\ndef load_tests(loader, tests, pattern):\n    return unittest.TestSuite([TaskTests('test_other')])\n")
			if f.Project.Manifest.Components != nil {
				f.Project.Manifest.Components.Checks[0].Invocations[0].Args = []string{"-m", "unittest", "tests.test_tasks"}
			}
		}, true},
		{"test fixture error", func(t *testing.T, f Fixture) {
			Write(t, f.Root, "tests/test_tasks.py", TestSource+"\ndef setUpModule():\n    raise RuntimeError('fixture failed')\n")
		}, true},
	}
	for _, explicit := range []bool{true, false} {
		for _, c := range cases {
			if !explicit && c.name == "selected test never run" {
				continue
			} // No explicit required selection exists in discovery mode.
			t.Run(fmt.Sprintf("explicit=%v/%s", explicit, c.name), func(t *testing.T) {
				f := New(t, explicit, wheel)
				c.change(t, f)
				Save(t, f)
				Admit(t, f)
				before := Tree(t, f.Root)
				r := Run(t, f, tc)
				Assert(t, f, r, false)
				a := Application(t, r)
				if c.runtime {
					found := false
					for _, x := range a.Executions {
						found = found || (x.Key.Kind == "conformance" && x.Success)
					}
					if !found {
						t.Fatal("failed test erased independent conformance execution")
					}
					if explicit && (len(r.Components.Conformance) != 1 || !r.Components.Conformance[0].TransitionsChecked) {
						t.Fatal("failed runtime erased valid checked conformance")
					}
				}
				if c.name == "test fails beside passing test" {
					passing, failed := false, false
					for _, x := range a.Executions {
						for _, row := range x.Tests {
							passing = passing || row.Outcome == "passed"
							failed = failed || row.Outcome == "failed"
						}
					}
					if !passing || !failed {
						t.Fatal("independent test outcomes lost")
					}
				}
				if c.name == "invalid transition" {
					if explicit && (len(r.Components.Conformance) != 1 || r.Components.Conformance[0].TransitionsChecked) {
						t.Fatal("TLC violation credited as success")
					}
				}
				js := string(JSON(t, r))
				if !strings.Contains(js, "diagnostic") && !strings.Contains(js, "message") {
					t.Fatal("failure lacks diagnostic")
				}
				if !reflect.DeepEqual(before, Tree(t, f.Root)) {
					t.Fatal("failure changed original bytes")
				}
			})
		}
	}
}

// GateFingerprints checks the complete local fingerprint suite, and also checks
// real dependencies and TLC when real is true. It must remain serial because the
// local transport changes the process environment.
func GateFingerprints(t *testing.T, real bool) {
	t.Helper()
	t.Run("local Python", func(t *testing.T) { tc, _ := Fake(t); fingerprints(t, tc, false) })
	if real {
		t.Run("real dependencies and TLC", func(t *testing.T) {
			tc, e := toolchain.Ensure(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			fingerprints(t, tc, true)
		})
	}
}
func fingerprints(t *testing.T, tc toolchain.Toolchain, wheel bool) {
	t.Helper()
	for _, explicit := range []bool{true, false} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			f := New(t, explicit, wheel)
			Admit(t, f)
			before := Tree(t, f.Root)
			first := Run(t, f, tc)
			Assert(t, f, first, true)
			repeat := Run(t, f, tc)
			relocated := New(t, explicit, wheel)
			other := Run(t, relocated, tc)
			if first.Fingerprint == "" || first.Fingerprint != repeat.Fingerprint || first.Fingerprint != other.Fingerprint {
				t.Fatalf("identical repeated/relocated inputs changed fingerprint: %s %s %s", first.Fingerprint, repeat.Fingerprint, other.Fingerprint)
			}
			for _, host := range []string{f.Root, relocated.Root} {
				if strings.Contains(string(JSON(t, first)), host) {
					t.Fatal("portable receipt leaks host root")
				}
			}
			if !reflect.DeepEqual(before, Tree(t, f.Root)) {
				t.Fatal("gate changed original inputs")
			}
			names := []string{Source, "tasks/helper.py", "tasks/neighbor.py", "tasks/next.txt", "resources/payload.txt", "tests/test_tasks.py", DriverPath, ModelPath, ProjectPath + "/.invariant/specs/Local.tla"}
			if wheel {
				names = append(names, "requirements.txt", "deps/application.txt", "deps/constraints.txt")
			}
			for _, name := range names {
				t.Run(name, func(t *testing.T) {
					q := New(t, explicit, wheel)
					b, e := os.ReadFile(filepath.Join(q.Root, name))
					if e != nil {
						t.Fatal(e)
					}
					suffix := "\n"
					switch {
					case strings.HasSuffix(name, ".py"), strings.Contains(name, "requirements"), strings.HasPrefix(name, "deps/"):
						suffix = "# input identity changed\n"
					case strings.HasSuffix(name, ".tla"):
						suffix = "\\* input identity changed\n"
					}
					Write(t, q.Root, name, string(b)+suffix)
					r := Run(t, q, tc)
					Assert(t, q, r, true)
					if r.Fingerprint == first.Fingerprint {
						t.Fatalf("changed %s ignored by fingerprint", name)
					}
				})
			}
			for _, kind := range []string{"bounds", "sampling"} {
				if !explicit && kind == "sampling" {
					continue
				}
				t.Run(kind, func(t *testing.T) {
					q := New(t, explicit, wheel)
					if kind == "bounds" {
						q.Project.Lock.Bounds["Limit"] = "3"
					} else {
						q.Project.Manifest.Components.Checks[0].Invocations[1].Sampling["seed"] = "7"
					}
					Save(t, q)
					r := Run(t, q, tc)
					Assert(t, q, r, true)
					if r.Fingerprint == first.Fingerprint {
						t.Fatalf("changed %s ignored", kind)
					}
				})
			}
			// Fingerprints must bind evidence itself, not just outcomes/counts or a
			// cached nested fingerprint supplied alongside it. Decode to exercise the
			// same representation that external consumers and attribution round trips use.
			for _, field := range []string{"observation", "runner", "observer", "dependency"} {
				if field == "dependency" && !wheel {
					continue
				}
				t.Run("actual "+field, func(t *testing.T) {
					var top map[string]any
					if e := json.Unmarshal(JSON(t, first), &top); e != nil {
						t.Fatal(e)
					}
					app := top["application"].(map[string]any)
					executions := app["executions"].([]any)
					changed := false
					for _, raw := range executions {
						x := raw.(map[string]any)
						p := x["provenance"].(map[string]any)
						switch field {
						case "observation":
							if obs, ok := x["observations"].([]any); ok && len(obs) > 0 {
								obs[0].(map[string]any)["state"].(map[string]any)["task"] = "other observed scalar"
								changed = true
							}
						case "runner":
							p["runner_sha256"] = "sha256:" + strings.Repeat("b", 64)
							changed = true
						case "observer":
							p["observer_sha256"] = "sha256:" + strings.Repeat("c", 64)
							changed = true
						case "dependency":
							d := p["dependencies"].(map[string]any)
							arts := d["artifacts"].([]any)
							arts[0].(map[string]any)["sha256"] = "sha256:" + strings.Repeat("d", 64)
							changed = true
						}
						if changed {
							break
						}
					}
					if !changed {
						t.Fatal("missing evidence to mutate")
					}
					var r verify.Report
					if e := json.Unmarshal(JSON(t, top), &r); e != nil {
						t.Fatal(e)
					}
					if verify.Fingerprint(r) == first.Fingerprint {
						t.Fatalf("actual %s ignored by fingerprint", field)
					}
				})
			}
		})
	}
}
