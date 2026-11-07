package plumbing

import (
	"context"
	"encoding/json"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise the public built-in check and its real Go compiler through the
// existing local Docker stand-in. No fixture invents compiler diagnostics or
// acceptance records. Raw staging deliberately permits malformed Go sources.
func builtin733Pin(t *testing.T, f *s397Fixture, file, name, source string) {
	t.Helper()
	s397Write(t, filepath.Join(f.root, filepath.FromSlash(file)), source, 0o644)
	f.plan.Sources[file] = source
	f.plan.Tests = append(f.plan.Tests, Test{Name: name, File: file, Says: "The proposed behavior works."})
}

// Unlike the ordinary fixture's draft helper, let Check itself reject syntax
// errors instead of validating the draft while constructing it.
func builtin733Draft(t *testing.T, f *s397Fixture) string {
	t.Helper()
	ws := t.TempDir()
	draft := struct {
		Name    string   `json:"name"`
		Summary string   `json:"summary"`
		Files   []string `json:"files"`
		Tests   []Test   `json:"tests"`
	}{f.plan.Name, f.plan.Summary, f.plan.Files, f.plan.Tests}
	data, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	s397Write(t, filepath.Join(ws, "plan.json"), string(data), 0o644)
	for file, source := range f.plan.Sources {
		s397Write(t, filepath.Join(ws, "tests", filepath.FromSlash(file)), source, 0o644)
		if err := os.Remove(filepath.Join(f.root, filepath.FromSlash(file))); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}

func builtin733Check(t *testing.T, f *s397Fixture, ws string) *Checked {
	t.Helper()
	c, err := Check(context.Background(), ws, f.root, s397Checker(f, 90*time.Second))
	if err != nil || c == nil {
		t.Fatalf("Check did not deliver a result: result=%+v error=%v", c, err)
	}
	if c.Run.DeadlineExceeded || c.Run.TimedOut || c.Run.Interrupted {
		t.Fatalf("fixture did not complete normally: %s\n%s", c.Problem, c.Run.Output)
	}
	return c
}

func builtin733Refused(t *testing.T, c *Checked, file string, diagnostic ...string) {
	t.Helper()
	if c.Problem == "" {
		t.Errorf("Check proposed a plan with an immutable test error in %s:\n%s", file, c.Run.Output)
		return
	}
	feedback := c.Feedback()
	if !strings.Contains(feedback, filepath.Base(file)) {
		t.Errorf("refusal lost the offending acceptance file %s:\n%s", file, feedback)
	}
	for _, want := range diagnostic {
		if !strings.Contains(feedback, want) {
			t.Errorf("refusal lost diagnostic %q:\n%s", want, feedback)
		}
	}
}

func TestBuiltinGoRefusesUnfixableAcceptance733(t *testing.T) {
	const file = "pending/pinned_accept_test.go"
	for _, tc := range []struct {
		name, source string
		diagnostic   []string
	}{
		{"unused import", `package pending
import ("fmt"; "testing")
func TestNeedsChange(t *testing.T) { t.Fatal("behavior missing") }
`, []string{"fmt", "imported and not used"}},
		{"unused local", `package pending
import "testing"
func TestNeedsChange(t *testing.T) { unused733 := 1; t.Fatal("behavior missing") }
`, []string{"unused733", "declared and not used"}},
		{"syntax", `package pending
import "testing"
func TestNeedsChange(t *testing.T) { _ = }
`, []string{"expected operand"}},
		{"local assignment", `package pending
import "testing"
func TestNeedsChange(t *testing.T) { var local733 int = "wrong"; _ = local733 }
`, []string{"cannot use", "wrong"}},
		{"test helper signature", `package pending
import "testing"
func helper733(n int) {}
func TestNeedsChange(t *testing.T) { helper733("wrong") }
`, []string{"cannot use", "wrong"}},
		{"test type member", `package pending
import "testing"
type TestOnly733 struct{}
func TestNeedsChange(t *testing.T) { TestOnly733{}.Missing733() }
`, []string{"Missing733", "undefined"}},
		{"external unused import", `package pending_test
import ("fmt"; "testing")
func TestNeedsChange(t *testing.T) { t.Fatal("behavior missing") }
`, []string{"fmt", "imported and not used"}},
		{"external test-local type", `package pending_test
import "testing"
type TestOnly733 struct{ Value int }
func TestNeedsChange(t *testing.T) { _ = TestOnly733{Value: "wrong"} }
`, []string{"cannot use", "wrong"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := s397New(t)
			f.pkg(t, "pending", "")
			builtin733Pin(t, f, file, "TestNeedsChange", tc.source)
			c := builtin733Check(t, f, builtin733Draft(t, f))
			builtin733Refused(t, c, file, tc.diagnostic...)
		})
	}
}

func TestBuiltinGoRefusesMixedCompilerErrors733(t *testing.T) {
	const file = "pending/pinned_accept_test.go"
	for _, mode := range []string{"unused import", "unused local", "local operand", "helper declaration", "second pinned file"} {
		t.Run(mode, func(t *testing.T) {
			f := s397New(t)
			f.pkg(t, "pending", "")
			imports, body, extra := `import "testing"`, "Future733()", ""
			diagnostic := []string{"cannot use", "immutable733"}
			badFile := file
			switch mode {
			case "unused import":
				imports = `import ("fmt"; "testing")`
				diagnostic = []string{"fmt", "imported and not used"}
			case "unused local":
				body += "; unused733 := 1"
				diagnostic = []string{"unused733", "declared and not used"}
			case "local operand":
				body = `Future733(1 + "immutable733")`
				diagnostic = []string{"mismatched types"}
			case "helper declaration":
				extra = `func helper733() { var local733 int = "immutable733"; _ = local733 }`
			case "second pinned file":
				badFile = "pending/other_accept_test.go"
				builtin733Pin(t, f, badFile, "TestOtherChange", `package pending
import "testing"
func TestOtherChange(t *testing.T) { var local733 int = "immutable733"; _ = local733 }
`)
			}
			builtin733Pin(t, f, file, "TestNeedsChange", "package pending\n"+imports+"\nfunc TestNeedsChange(t *testing.T) { "+body+" }\n"+extra+"\n")
			c := builtin733Check(t, f, builtin733Draft(t, f))
			builtin733Refused(t, c, badFile, diagnostic...)
		})
	}
}

func TestBuiltinGoDistinguishesRepairableFailures733(t *testing.T) {
	const file = "pending/pinned_accept_test.go"
	for _, tc := range []struct {
		name, base, fixed, source string
	}{
		{"missing function", "", "func Future733() int { return 7 }", `package pending
import "testing"
func TestNeedsChange(t *testing.T) { if Future733() != 7 { t.Fatal("wrong result") } }
`},
		{"external missing function", "", "func Future733() int { return 7 }", `package pending_test
import ("testing"; product "example.com/shards397/pending")
func TestNeedsChange(t *testing.T) { if product.Future733() != 7 { t.Fatal("wrong result") } }
`},
		{"missing type", "", "type Future733 struct{}", `package pending
import "testing"
func TestNeedsChange(t *testing.T) { _ = Future733{} }
`},
		{"missing production method", "type Box733 struct{}", "type Box733 struct{}\nfunc (Box733) Future733() int { return 7 }", `package pending
import "testing"
func TestNeedsChange(t *testing.T) { box := Box733{}; if box.Future733() != 7 { t.Fatal("wrong result") } }
`},
		{"changed call arity", "func Value733() int { return 7 }", "func Value733(s string) int { return len(s) }", `package pending
import "testing"
func TestNeedsChange(t *testing.T) { if Value733("changed") != 7 { t.Fatal("wrong result") } }
`},
		{"changed argument type", "func Value733(n int) int { return n }", "func Value733(s string) int { return len(s) }", `package pending
import "testing"
func TestNeedsChange(t *testing.T) { if Value733("changed") != 7 { t.Fatal("wrong result") } }
`},
		{"changed result type", "func Value733() int { return 7 }", `func Value733() string { return "changed" }`, `package pending
import "testing"
func TestNeedsChange(t *testing.T) { var got string = Value733(); if got != "changed" { t.Fatal("wrong result") } }
`},
		{"changed production field type", "type Options733 struct{ Value int }", "type Options733 struct{ Value string }", `package pending
import "testing"
func TestNeedsChange(t *testing.T) { _ = Options733{Value: "changed"} }
`},
		{"compiled assertion failure", "func Value733() int { return 0 }", "func Value733() int { return 7 }", `package pending
import "testing"
func TestNeedsChange(t *testing.T) { if Value733() != 7 { t.Fatal("wrong result") } }
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := s397New(t)
			f.pkg(t, "pending", "")
			production := filepath.Join(f.root, "pending", "code.go")
			base := "package pending\n" + tc.base + "\n"
			s397Write(t, production, base, 0o644)
			f.accept(t, "pending", "TestNeedsChange", tc.source)
			c := builtin733Check(t, f, builtin733Draft(t, f))
			if c.Problem != "" {
				t.Fatalf("repairable %s no longer qualifies:\n%s", tc.name, c.Feedback())
			}
			s397Status(t, c.Run, "pending", "TestNeedsChange", "accept", "failed")

			// Only the named production file changes. The same pinned source
			// must then compile and complete a normal candidate runtime pass.
			fixed, err := format.Source([]byte("package pending\n" + tc.fixed + "\n"))
			if err != nil {
				t.Fatal(err)
			}
			s397Write(t, production, string(fixed), 0o644)
			s397Write(t, filepath.Join(f.root, file), f.plan.Sources[file], 0o644)
			r := f.run(t, 90*time.Second, 30*time.Second)
			if !r.Passed() {
				t.Fatalf("production-only correction did not pass: %s\n%s", r.Summary(), r.Output)
			}
			s397Status(t, r, "pending", "TestNeedsChange", "accept", "passed")

			// A valid API error (or an ordinary assertion) cannot excuse a
			// neighboring declaration that no production change can repair.
			s397Write(t, production, base, 0o644)
			bad := f.plan.Sources[file] + "\nfunc testOnlyMistake733() { var local733 int = \"immutable733\"; _ = local733 }\n"
			f.plan.Sources[file] = bad
			s397Write(t, filepath.Join(f.root, file), bad, 0o644)
			c = builtin733Check(t, f, builtin733Draft(t, f))
			builtin733Refused(t, c, file, "cannot use", "immutable733")
		})
	}
}
