package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

func pa327CLIFixture(t *testing.T, explicit bool) (string, project.Manifest) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "app")
	dir := filepath.Join(root, "verification/lifecycle")
	write := func(name, body string) {
		t.Helper()
		f := filepath.Join(root, name)
		if e := os.MkdirAll(filepath.Dir(f), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(f, []byte(body), 0644); e != nil {
			t.Fatal(e)
		}
	}
	model := `---- MODULE Lifecycle ----
EXTENDS Naturals
CONSTANT Limit
VARIABLE task
vars == <<task>>
Init == task = "pending"
Next == /\ task' = "running" /\ task \in {"pending", "running"}
Spec == Init /\ [][Next]_vars
Safe == task \in {"pending", "running"} /\ Limit >= 1
====
`
	m := project.Manifest{Name: "manual lifecycle", Module: ".invariant/specs/Lifecycle.tla", Code: ".", Language: "python", Conformance: "driver.py", Existing: []string{"tasks/lifecycle.py"}}
	if explicit {
		target := project.ComponentRef{Source: "tasks/lifecycle.py", Symbol: "advance"}
		test := project.ComponentRef{Source: "tests/test_tasks.py", Symbol: "TestTask.test_step"}
		driver := project.ComponentRef{Source: "verification/lifecycle/driver.py", Symbol: "drive"}
		m.Components = &project.ComponentsConfig{Scope: []string{"tasks"}, Checks: []project.ComponentCheck{{Target: target, Tests: []project.ComponentRef{test}, Conformance: []project.ComponentRef{driver}, Invocations: []project.ComponentInvocation{{ID: "unit", Kind: "test", Check: test, Command: "python", WorkingDir: ".", Args: []string{"-m", "unittest", "tests.test_tasks.TestTask.test_step"}}, {ID: "sample", Kind: "conformance", Check: driver, Command: "python", WorkingDir: "verification/lifecycle", Args: []string{"driver.py"}, Inputs: []string{"verification/lifecycle/.invariant/specs/Lifecycle.tla", "verification/lifecycle/.invariant/ratified.lock"}}}}}}
	}
	lock := project.Lock{Decision: "test-fixture-only", Bounds: map[string]string{"Limit": "2"}}
	for _, s := range []project.Statement{{Name: "Spec", Kind: project.Spec}, {Name: "Safe", Kind: project.Invariant}} {
		h, e := tla.PinHash(model, s.Name, project.Model)
		if e != nil {
			t.Fatal(e)
		}
		s.SHA256 = h
		lock.Statements = append(lock.Statements, s)
	}
	mb, _ := json.Marshal(m)
	lb, _ := json.Marshal(lock)
	for name, body := range map[string]string{"tasks/__init__.py": "", "tasks/lifecycle.py": "# +nagini\ndef advance(state):\n    return 'running'\n", "tasks/application.py": "from .lifecycle import advance\ndef start(): return 'pending'\ndef tick(state): return advance(state)\n", "tests/__init__.py": "", "tests/test_tasks.py": "import unittest\nfrom tasks.application import tick\nclass TestTask(unittest.TestCase):\n    def test_step(self): self.assertEqual(tick('pending'), 'running')\n", "verification/lifecycle/driver.py": "from invariant_observe import functional\nfrom tasks.lifecycle import advance\nfrom tasks import application\ndef drive():\n    with functional(advance, application.start, argument='state', field='task') as life:\n        life.step(application.tick)\nif __name__=='__main__': drive()\n", "verification/lifecycle/.invariant/specs/Lifecycle.tla": model, "verification/lifecycle/.invariant/invariant.json": string(mb), "verification/lifecycle/.invariant/ratified.lock": string(lb)} {
		write(name, body)
	}
	return dir, m
}
func TestPythonApplicationGateCLI(t *testing.T) {
	t.Run("admission and unsupported preflight without Docker", func(t *testing.T) {
		bin := t.TempDir()
		log := filepath.Join(t.TempDir(), "tools")
		t.Setenv("PA327_CLI_LOG", log)
		for _, name := range []string{"docker", "nagini", "pip", "pip3", "java"} {
			p := filepath.Join(bin, name)
			if e := os.WriteFile(p, []byte("#!/bin/sh\nprintf '%s\\n' \"$0 $*\" >> \"$PA327_CLI_LOG\"\necho 'pa327 tool setup sentinel' >&2\nexit 73\n"), 0755); e != nil {
				t.Fatal(e)
			}
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		for _, explicit := range []bool{true, false} {
			dir, m := pa327CLIFixture(t, explicit)
			// Exhaustive applications are admitted since #342.
			for _, exhaustive := range []bool{false, true} {
				m.Exhaustive = exhaustive
				b, _ := json.Marshal(m)
				if e := os.WriteFile(filepath.Join(dir, ".invariant/invariant.json"), b, 0644); e != nil {
					t.Fatal(e)
				}
				code, out := agentCLI(t, "verify", dir)
				if code != 2 || !strings.Contains(out, "docker isn't available") {
					t.Fatalf("supported application did not reach tool preparation: exit=%d %s", code, out)
				}
				b, e := os.ReadFile(log)
				if e != nil || !strings.Contains(string(b), "docker info") {
					t.Fatalf("no admitted CLI tool preparation: %s %v", b, e)
				}
				if strings.Contains(string(b), "nagini") || strings.Contains(string(b), "pip") {
					t.Fatal("prepared proof or dependencies before capability check")
				}
				if e := os.Remove(log); e != nil {
					t.Fatal(e)
				}
			}
			// A proof selection, or without components an existing selection
			// no root holds, still fails before tools.
			want := "contract"
			if explicit {
				m.Components.Checks[0].Proof = &project.ComponentProof{Contracts: []project.ComponentRef{m.Components.Checks[0].Target}}
			} else {
				m.Existing = []string{"tasks/missing.py"}
				want = "existing selection"
			}
			b, _ := json.Marshal(m)
			if e := os.WriteFile(filepath.Join(dir, ".invariant/invariant.json"), b, 0644); e != nil {
				t.Fatal(e)
			}
			code, out := agentCLI(t, "verify", dir)
			if code != 2 || !strings.Contains(strings.ToLower(out), want) {
				t.Fatalf("unsupported mode lacks capability diagnostic: %d %s", code, out)
			}
			if b, e := os.ReadFile(log); e == nil {
				t.Fatalf("unsupported mode invoked tools: %s", b)
			}
		}
	})
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") == "1" {
		t.Run("real verify receipt and failure exit", func(t *testing.T) {
			if _, e := toolchain.Ensure(context.Background()); e != nil {
				t.Fatalf("requested real prerequisites: %v", e)
			}
			for _, explicit := range []bool{true, false} {
				dir, _ := pa327CLIFixture(t, explicit)
				outDir := filepath.Join(t.TempDir(), "receipt")
				code, out := agentCLI(t, "verify", "-out", outDir, dir)
				if code != 0 {
					t.Fatalf("real CLI failed %d: %s", code, out)
				}
				b, e := os.ReadFile(filepath.Join(outDir, "receipt.json"))
				if e != nil {
					t.Fatal(e)
				}
				var r verify.Report
				if e := json.Unmarshal(b, &r); e != nil {
					t.Fatal(e)
				}
				if !r.Passed || r.Code != nil || r.Toolchain.NaginiRecipe != "" || !strings.Contains(string(b), `"application"`) {
					t.Fatalf("CLI receipt is not sampled execution: %s", b)
				}
				if md, e := os.ReadFile(filepath.Join(outDir, "receipt.md")); e != nil || !strings.Contains(string(md), "sampled") {
					t.Fatalf("CLI Markdown: %s %v", md, e)
				}
				root := filepath.Clean(filepath.Join(dir, "../.."))
				if e := os.WriteFile(filepath.Join(root, "tasks/lifecycle.py"), []byte("# +nagini\ndef advance(state): return 'broken'\n"), 0644); e != nil {
					t.Fatal(e)
				}
				code, out = agentCLI(t, "verify", "-out", outDir, dir)
				if code != 1 {
					t.Fatalf("real invalid transition should produce failing receipt and exit 1: %d %s", code, out)
				}
			}
		})
	}
}
