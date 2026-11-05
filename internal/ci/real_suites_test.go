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
		{"the gate's command", []string{"cmd/invariant/main.go"}, true, false},
		{"running TLC", []string{"internal/tlc/tlc.go"}, true, true},
		{"reader statecharts", []string{"internal/statechart/chart.go"}, true, true},
		{"the decision store", []string{"internal/decisions/store.go"}, true, true},
		{"a core the store runs on", []string{"factory/ids/ids/ids.go"}, true, true},
		{"the TLAPS example's README", []string{"examples/05-connection-pool/README.md"}, false, false},
		{"a fixture's README", []string{"internal/verify/testdata/examples/02-twophase-commit/README.md"}, false, false},
		{"the workflow", []string{".github/workflows/gate.yml"}, true, true},
		{"the module's sums", []string{"go.sum"}, true, true},
		{"docs alone", []string{"README.md", "SPEC.md", "decisions/log.md"}, false, false},
		{"the factory", []string{"internal/factory/factory.go", "internal/plumbing/sandbox.go", "factory/protocol/protocol/protocol.go"}, false, false},
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

// The rules cover every package of the module that the real suites' tests
// import, as Go's import graph says, so a change to one runs them (#436).
// internal/verify's own files have rules of their own, file by file.
func TestRealSuitesCoverEveryPackageTheirTestsImport(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "..", ".github", "real-suites.sh"))
	if err != nil {
		t.Fatal(err)
	}
	const module = "github.com/gitdek/invariant/"
	list := exec.Command("go", "list", "-deps", "-test", "./internal/verify", "./internal/conformance")
	list.Dir = filepath.Join("..", "..")
	out, err := list.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	dirs := map[string]bool{}
	for _, pkg := range strings.Fields(string(out)) {
		if !strings.HasPrefix(pkg, module) || strings.HasSuffix(pkg, ".test") || strings.Contains(pkg, "[") || strings.HasSuffix(pkg, "_test") {
			continue
		}
		dirs[strings.TrimPrefix(pkg, module)] = true
	}
	if !dirs["internal/tlc"] || !dirs["internal/verify"] {
		t.Fatalf("go list didn't name the packages it should: %v", dirs)
	}
	for dir := range dirs {
		cmd := exec.Command("bash", script)
		cmd.Stdin = strings.NewReader(dir + "/x.go")
		got, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: %v", script, err)
		}
		if string(got) != "python=true\ntlaps=true\n" {
			t.Errorf("a change to %s, which the real suites' tests import, says %q", dir, got)
		}
	}
}
