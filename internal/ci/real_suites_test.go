// Package ci tests the repository's own CI scripts, which live under
// .github, where Go neither builds nor tests anything.
package ci

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// CI runs each opt-in real suite only on a pull request whose change can
// affect what it checks (#416), as .github/real-suites.sh decides from the
// change's paths.
func TestRealSuitesRunOnTheChangesTheyCover(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "..", ".github", "real-suites.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name          string
		paths         []string
		python, tlaps bool
	}{
		{"the application's executor", []string{"internal/verify/python_application_execution.go"}, true, false},
		{"the observer", []string{"internal/verify/python_application_observer.py"}, true, false},
		{"declared refusals", []string{"internal/verify/declared_refusal.go"}, true, false},
		{"TLAPS", []string{"internal/verify/tlaps.go"}, false, true},
		{"the TLAPS example", []string{"examples/05-connection-pool/pool/pool.go"}, false, true},
		{"staging", []string{"internal/verify/staging.go"}, true, true},
		{"conformance", []string{"internal/conformance/conformance.go"}, true, true},
		{"the toolchain", []string{"internal/toolchain/toolchain.go"}, true, true},
		{"the gate's command", []string{"cmd/invariant/main.go"}, true, true},
		{"the workflow", []string{".github/workflows/gate.yml"}, true, true},
		{"the module's sums", []string{"go.sum"}, true, true},
		{"docs alone", []string{"README.md", "SPEC.md", "decisions/log.md"}, false, false},
		{"the factory", []string{"internal/factory/factory.go", "internal/plumbing/sandbox.go"}, false, false},
		{"another example", []string{"examples/02-twophase-commit/twophase.go"}, false, false},
		{"nothing", nil, false, false},
		{"one of each", []string{"README.md", "internal/verify/tlaps.go", "internal/verify/python_application_gate.go"}, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command("bash", script)
			// Without a final newline, as a last line can come.
			cmd.Stdin = strings.NewReader(strings.Join(c.paths, "\n"))
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("%s: %v", script, err)
			}
			if want := fmt.Sprintf("python=%t\ntlaps=%t\n", c.python, c.tlaps); string(out) != want {
				t.Errorf("for %q it said %q, want %q", c.paths, out, want)
			}
		})
	}
}
