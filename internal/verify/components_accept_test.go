package verify_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// This first slice resolves configuration; it does not implement selective
// proof execution or component evidence reconciliation. A new configuration
// must not silently run under the old package-wide assurance semantics.
func TestVerifyDoesNotSilentlyIgnoreExplicitComponents(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) {
		t.Helper()
		file := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	manifest := map[string]any{
		"name": "explicit components", "language": "go", "code": "core",
		"module": ".invariant/specs/Counter.tla",
		"components": map[string]any{
			"scope": []string{"core"},
			"checks": []any{map[string]any{
				"target": map[string]string{"source": "core/core.go", "symbol": "Step"},
				"proof":  map[string]any{"contracts": []any{map[string]string{"source": "core/core.go", "symbol": "Step"}}},
			}},
		},
	}
	writeManifest := func() {
		t.Helper()
		b, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		write(".invariant/invariant.json", string(b), 0o644)
	}
	writeManifest()
	write(".invariant/ratified.lock", `{"bounds":{"Limit":"2"},"statements":[{"name":"Spec","kind":"spec"},{"name":"Safe","kind":"invariant"}]}`, 0o644)
	write(".invariant/specs/Counter.tla", "---- MODULE Counter ----\nEXTENDS Naturals\nCONSTANT Limit\nVARIABLE n\nInit == n = 0\nNext == n' = n\nSpec == Init /\\ [][Next]_n\nSafe == n >= 0\n====\n", 0o644)
	write("go.mod", "module example.test/explicit\n\ngo 1.27.1\n", 0o644)
	write("core/core.go", "package core\n//@ requires n >= 0\n//@ ensures result >= 0\nfunc Step(n int) (result int) { return n }\nfunc Neighbor() {}\n", 0o644)
	// Neither proof nor a build sandbox may start for this mode yet. Stub
	// external tools so this acceptance test needs no Docker or verifier.
	log := filepath.Join(dir, "tool-called")
	t.Setenv("COMPONENT_ACCEPT_TOOL_LOG", log)
	for _, tool := range []string{"docker", "go"} {
		write("bin/"+tool, "#!/bin/sh\nprintf 'called\\n' >> \"$COMPONENT_ACCEPT_TOOL_LOG\"\nexit 73\n", 0o755)
	}
	t.Setenv("PATH", filepath.Join(dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := project.Load(dir); err != nil {
		t.Fatalf("the explicit configuration itself must load successfully: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := verify.Run(ctx, dir, "", toolchain.Toolchain{})
	if r != nil || err == nil || !strings.Contains(strings.ToLower(err.Error()), "component verification is not supported yet") {
		t.Fatalf("want an explicit unsupported-execution error, not legacy evidence; report=%#v err=%v", r, err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("verification started a tool before rejecting unsupported component execution: %v", err)
	}
	// Explicit component metadata must not disable the model-only path.
	// The stub TLC fails, but the report must still describe a model check.
	model, modelErr := verify.RunModel(ctx, dir, toolchain.Toolchain{})
	if modelErr != nil || model == nil || !model.ModelOnly {
		t.Fatalf("component metadata blocked model-only checking: report=%#v err=%v", model, modelErr)
	}
	if err := os.Remove(log); err != nil {
		t.Fatalf("model-only checking never invoked its tool: %v", err)
	}
	// The guard belongs to explicit code verification. Legacy manifests
	// still enter the existing gate instead of being rejected by the guard.
	delete(manifest, "components")
	writeManifest()
	_, legacyErr := verify.Run(ctx, dir, "", toolchain.Toolchain{})
	if legacyErr != nil && strings.Contains(strings.ToLower(legacyErr.Error()), "component verification is not supported yet") {
		t.Fatalf("the new mode disabled legacy verification: %v", legacyErr)
	}
	if _, err := os.Stat(log); err != nil {
		t.Fatalf("legacy verification no longer invokes its existing tools: %v (gate error %v)", err, legacyErr)
	}
}
