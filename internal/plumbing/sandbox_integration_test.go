//go:build integration

package plumbing

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/toolchain"
)

// The sandbox runs a checkout's tests offline: an acceptance test fails
// until the code does what it says, and then everything passes.
func TestTheSandboxRunsAcceptanceTests(t *testing.T) {
	root := t.TempDir()
	write := func(p, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, p), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/double\n\ngo 1.27.1\n")
	write("double.go", "package double\n\n// Double is n twice.\nfunc Double(n int) int { return n }\n")
	accept := "package double\n\nimport \"testing\"\n\nfunc TestDoubleDoubles(t *testing.T) {\n\tif Double(2) != 4 {\n\t\tt.Fatal(\"Double(2) isn't 4\")\n\t}\n}\n"
	write("double_accept_test.go", accept)
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	p := &Plan{Name: "double", Summary: "Double doubles.", Files: []string{"double.go"},
		Tests:   []Test{{Name: "TestDoubleDoubles", File: "double_accept_test.go", Says: "Double(2) is 4."}},
		Sources: map[string]string{"double_accept_test.go": accept}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	mod, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	image, err := toolchain.PlumbingImage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s := Sandbox{Image: image, ModCache: strings.TrimSpace(string(mod)), Cache: filepath.Join(t.TempDir(), "cache")}
	r, err := s.Run(context.Background(), root, p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Passed() || r.Tests || r.Accepted["double_accept_test.go:TestDoubleDoubles"] {
		t.Fatalf("the acceptance test passed against code that doesn't double:\n%s", r.Output)
	}
	if got := r.Failing(); len(got) != 1 || got[0] != "double_accept_test.go:TestDoubleDoubles" {
		t.Errorf("failing = %v", got)
	}
	write("double.go", "package double\n\n// Double is n twice.\nfunc Double(n int) int { return 2 * n }\n")
	if r, err = s.Run(context.Background(), root, p); err != nil || !r.Passed() {
		t.Fatalf("with the fix: %v\n%s", err, r.Output)
	}
	write("double.go", "package double\n\n// Double is n twice.\nfunc Double(n int) int {   return 2 * n }\n")
	if r, _ = s.Run(context.Background(), root, p); r.Passed() || len(r.Gofmt) != 1 || r.Gofmt[0] != "double.go" {
		t.Errorf("gofmt: %v\n%s", r.Gofmt, r.Output)
	}
}

// Git in the sandbox names a committer at once. With no network, the
// container's hostname didn't resolve, and each lookup waited five seconds
// on DNS, so a run of the repository's tests outlived the agent's tool
// limit (#100).
func TestGitIsQuickInTheSandbox(t *testing.T) {
	root := t.TempDir()
	write := func(p, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, p), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/quick\n\ngo 1.27.1\n")
	write("quick.go", "package quick\n")
	accept := `package quick

import (
	"os/exec"
	"testing"
	"time"
)

func TestGitNamesACommitterQuickly(t *testing.T) {
	start := time.Now()
	for i := 0; i < 3; i++ {
		exec.Command("git", "var", "GIT_COMMITTER_IDENT").Run()
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("three identity lookups took %s: git is waiting on DNS", took)
	}
}
`
	write("quick_accept_test.go", accept)
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	p := &Plan{Name: "quick", Summary: "Git is quick.", Files: []string{"quick.go"},
		Tests:   []Test{{Name: "TestGitNamesACommitterQuickly", File: "quick_accept_test.go", Says: "Git names a committer at once."}},
		Sources: map[string]string{"quick_accept_test.go": accept}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	mod, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	image, err := toolchain.PlumbingImage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s := Sandbox{Image: image, ModCache: strings.TrimSpace(string(mod)), Cache: filepath.Join(t.TempDir(), "cache")}
	r, err := s.Run(context.Background(), root, p)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Accepted["quick_accept_test.go:TestGitNamesACommitterQuickly"] {
		t.Fatalf("git was slow in the sandbox:\n%s", r.Output)
	}
}
