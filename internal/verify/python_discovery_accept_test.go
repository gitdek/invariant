package verify_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

func TestExplicitPythonDiscoveryStillRefusesCodeVerification(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) {
		t.Helper()
		file := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	m := project.Manifest{
		Name: "Python discovery only", Language: "python", Code: "core", Module: ".invariant/specs/Counter.tla", Conformance: "driver.py",
		Components: &project.ComponentsConfig{Scope: []string{"core"}, Checks: []project.ComponentCheck{{
			Target: project.ComponentRef{Source: "core/ops.py", Symbol: "Counter.step"},
			Proof:  &project.ComponentProof{Contracts: []project.ComponentRef{{Source: "core/ops.py", Symbol: "Counter.step"}}},
		}}},
	}
	save := func() {
		t.Helper()
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		write(".invariant/invariant.json", string(b), 0644)
	}
	save()
	lock := `{"bounds":{"Limit":"2"},"statements":[{"name":"Spec","kind":"spec"},{"name":"Safe","kind":"invariant"}]}`
	write(".invariant/ratified.lock", lock, 0644)
	write(".invariant/specs/Counter.tla", "---- MODULE Counter ----\nEXTENDS Naturals\nCONSTANT Limit\nVARIABLE n\nInit == n = 0\nNext == n' = n\nSpec == Init /\\ [][Next]_n\nSafe == n >= 0\n====\n", 0644)
	write("core/ops.py", "# +nagini\nraise RuntimeError('must never execute during discovery')\nclass Counter:\n    def step(self, n):\n        Requires(n >= 0)\n        Ensures(Result() >= 0)\n        return n\n", 0644)
	write("driver.py", "raise RuntimeError('must not run a driver for explicit components')\n", 0644)
	log := filepath.Join(dir, "tool-called")
	t.Setenv("PY304_VERIFY_TOOL_LOG", log)
	// Parsing may use isolated Python's standard-library AST. Verification
	// tools, dependency installers and project commands must never start.
	for _, tool := range []string{"docker", "go", "java", "node", "pytest", "mypy", "nagini", "pip", "pip3"} {
		write("bin/"+tool, "#!/bin/sh\nprintf 'called\\n' >> \"$PY304_VERIFY_TOOL_LOG\"\nexit 73\n", 0755)
	}
	t.Setenv("PATH", filepath.Join(dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := project.Load(dir); err != nil {
		t.Fatalf("valid explicit Python discovery must load before checking the guard: %v", err)
	}
	if err := verify.Check(dir); !errors.Is(err, verify.ErrComponents) {
		t.Fatalf("Check must return ErrComponents, got %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	report, err := verify.Run(ctx, dir, "", toolchain.Toolchain{})
	if report != nil || !errors.Is(err, verify.ErrComponents) {
		t.Fatalf("explicit Python fell through to legacy verification: report=%#v err=%v", report, err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("verification tool started before explicit refusal: %v", err)
	}
	model, err := verify.RunModel(ctx, dir, toolchain.Toolchain{})
	if err != nil || model == nil || !model.ModelOnly {
		t.Fatalf("Python discovery blocked model-only checking: report=%#v err=%v", model, err)
	}
	if err := os.Remove(log); err != nil {
		t.Fatalf("model-only path never reached TLC: %v", err)
	}
	m.Components = nil
	save()
	if err := verify.Check(dir); err != nil {
		t.Fatalf("legacy Python preflight changed: %v", err)
	}
	_, err = verify.Run(ctx, dir, "", toolchain.Toolchain{})
	if errors.Is(err, verify.ErrComponents) {
		t.Fatalf("legacy Python hit explicit guard: %v", err)
	}
	if _, statErr := os.Stat(log); statErr != nil {
		t.Fatalf("legacy Python did not reach existing tools: %v (gate: %v)", statErr, err)
	}
	got, err := os.ReadFile(filepath.Join(dir, ".invariant/ratified.lock"))
	if err != nil || string(got) != lock {
		t.Fatalf("discovery or guard rewrote the ratified lock: %v", err)
	}
}
