package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// invariant verify refuses a project that declares components, as the gate
// does, before it sets up Docker or TLC, so the refusal needs neither
// (#212's review). Stand-ins for docker and go record any call.
func TestVerifyRefusesComponentsBeforeItSetsUpTools(t *testing.T) {
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
	write(".invariant/invariant.json", `{"name": "explicit components", "language": "go", "code": "core",
		"module": ".invariant/specs/Counter.tla", "components": {"scope": ["core"]}}`, 0o644)
	write(".invariant/ratified.lock", `{"bounds":{"Limit":"2"},"statements":[{"name":"Spec","kind":"spec"},{"name":"Safe","kind":"invariant"}]}`, 0o644)
	write(".invariant/specs/Counter.tla", "---- MODULE Counter ----\nEXTENDS Naturals\nCONSTANT Limit\nVARIABLE n\nInit == n = 0\nNext == n' = n\nSpec == Init /\\ [][Next]_n\nSafe == n >= 0\n====\n", 0o644)
	write("go.mod", "module example.test/explicit\n\ngo 1.27.1\n", 0o644)
	write("core/core.go", "package core\n\nfunc Step(n int) int { return n }\n", 0o644)
	called := filepath.Join(dir, "called")
	for _, tool := range []string{"docker", "go"} {
		write("bin/"+tool, "#!/bin/sh\necho "+tool+" >> "+called+"\nexit 73\n", 0o755)
	}
	t.Setenv("PATH", filepath.Join(dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))

	code, out := agentCLI(t, "verify", dir)
	if code != 2 || !strings.Contains(out, "component verification is not supported yet") {
		t.Fatalf("verify exited %d, saying:\n%s", code, out)
	}
	if b, err := os.ReadFile(called); err == nil {
		t.Errorf("verify set up tools before it refused the project: %s", b)
	}
}
