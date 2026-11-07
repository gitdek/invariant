package plumbing

import (
	"context"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These checks use the operational API documented in
// sandbox_shards_accept_test.go. Check must distinguish a completed failing
// acceptance test from one that did not finish. Cancellation may be reported
// as an error or as a rejected Checked result; neither may approve the plan.

// A draft's test files are proposed additions, so they must be absent from the
// repository that Check copies. Keep their sources in the draft workspace.
func s397Draft(t *testing.T, f *s397Fixture) string {
	t.Helper()
	if err := f.plan.Validate(); err != nil {
		t.Fatal("invalid fixture plan: ", err)
	}
	ws := t.TempDir()
	draft := struct {
		Name    string   `json:"name"`
		Summary string   `json:"summary"`
		Files   []string `json:"files"`
		Tests   []Test   `json:"tests"`
	}{f.plan.Name, f.plan.Summary, f.plan.Files, f.plan.Tests}
	b, err := json.MarshalIndent(draft, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	s397Write(t, filepath.Join(ws, "plan.json"), string(b)+"\n", 0o644)
	for file, src := range f.plan.Sources {
		s397Write(t, filepath.Join(ws, "tests", filepath.FromSlash(file)), src, 0o644)
		if err := os.Remove(filepath.Join(f.root, filepath.FromSlash(file))); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}

func s397Checker(f *s397Fixture, timeout time.Duration) Sandbox {
	return Sandbox{Image: "local-fixture", ModCache: f.mod, Cache: f.cache,
		Timeout: timeout, ShardTimeout: 30 * time.Second}
}

func TestSandboxAcceptanceResultsArePerTestWithinPackage(t *testing.T) {
	f := s397New(t)
	f.pkg(t, "mixed", "")
	f.accept(t, "mixed", "TestAlreadyPasses", `package mixed
import "testing"
func TestAlreadyPasses(t *testing.T) {}
func TestNeedsChange(t *testing.T) { t.Fatal("the proposed behavior is missing") }
`)
	f.plan.Tests = append(f.plan.Tests, Test{Name: "TestNeedsChange", File: "mixed/pinned_accept_test.go", Says: "The proposed behavior is present."})
	f.warm(t)
	r := f.run(t, 30*time.Second, 20*time.Second)
	for name, want := range map[string]bool{"TestAlreadyPasses": true, "TestNeedsChange": false} {
		key := "mixed/pinned_accept_test.go:" + name
		if got, found := r.Accepted[key]; !found || got != want {
			t.Errorf("Accepted[%q] = %v, present=%v, want %v; package failure must not overwrite another test's result", key, got, found, want)
		}
	}
	s397Status(t, r, "mixed", "TestAlreadyPasses", "accept", "passed")
	s397Status(t, r, "mixed", "TestNeedsChange", "accept", "failed")
	if r.Passed() {
		t.Error("one passing acceptance test hid the failing test in its package")
	}

	c, err := Check(context.Background(), s397Draft(t, f), f.root, s397Checker(f, 30*time.Second))
	if err != nil || c == nil {
		t.Fatalf("Check: result=%+v, error=%v", c, err)
	}
	s397Status(t, c.Run, "mixed", "TestAlreadyPasses", "accept", "passed")
	s397Status(t, c.Run, "mixed", "TestNeedsChange", "accept", "failed")
	if !c.Run.Accepted["mixed/pinned_accept_test.go:TestAlreadyPasses"] || c.Run.Accepted["mixed/pinned_accept_test.go:TestNeedsChange"] {
		t.Errorf("Check lost individual acceptance outcomes: %+v", c.Run.Accepted)
	}
	if c.Problem == "" || !strings.Contains(c.Problem, "TestAlreadyPasses") {
		t.Errorf("Check must reject and identify a test that already passes even when its neighbor fails: %q", c.Problem)
	}
}

// The same fixture can stop in vet, an ordinary test, or the acceptance replay.
// Observation files let cancellation happen after the intended phase starts.
func s397IncompleteFixture(t *testing.T, phase string) (*s397Fixture, string) {
	t.Helper()
	f := s397New(t)
	started := filepath.Join(f.observe, "phase-started")
	ordinarySource := fmt.Sprintf(`package pending
import ("os"; "testing"; "time")
func TestOrdinary(t *testing.T) {
  if %t {
    if err := os.WriteFile(%q, []byte("ordinary test started"), 0600); err != nil { t.Fatal(err) }
    time.Sleep(60*time.Second)
  }
}
`, phase == "test", started)
	if phase == "after_acceptance" {
		// Only one ordinary invocation may write the replay marker, even
		// when workers start different top-level tests in separate processes.
		ordinarySource = ""
	}
	f.pkg(t, "pending", ordinarySource)
	acceptSource := fmt.Sprintf(`package pending
import ("os"; "testing"; "time")
func TestNeedsChange(t *testing.T) {
  if %t {
    prior, err := os.ReadFile(%q)
    if err != nil && !os.IsNotExist(err) { t.Fatal(err) }
    if len(prior) > 0 {
      if err := os.WriteFile(%q, []byte("acceptance test started"), 0600); err != nil { t.Fatal(err) }
      time.Sleep(60*time.Second)
    }
    if err := os.WriteFile(%q, []byte("ordinary execution completed"), 0600); err != nil { t.Fatal(err) }
  }
  if %t {
    if err := os.WriteFile(%q, []byte("a completed failure"), 0600); err != nil { t.Fatal(err) }
  }
  t.Fatal("the proposed behavior is missing")
}
`, phase == "acceptance", filepath.Join(f.observe, "first-execution"), started, filepath.Join(f.observe, "first-execution"), phase == "after_acceptance", filepath.Join(f.observe, "completed-failure"))
	if phase == "after_acceptance" {
		acceptSource += fmt.Sprintf(`
func TestMain(m *testing.M) {
  before, _ := os.ReadFile(%q)
  code := m.Run()
  if len(before) > 0 {
    if err := os.WriteFile(%q, []byte("acceptance emitted its failure"), 0600); err != nil { panic(err) }
    time.Sleep(60*time.Second)
  }
  os.Exit(code)
}
`, filepath.Join(f.observe, "completed-failure"), started)
	}
	f.accept(t, "pending", "TestNeedsChange", acceptSource)
	f.warm(t)
	if phase == "vet" {
		t.Setenv("S397_HANG_VET", "1")
		started = filepath.Join(f.control, "vet-started")
	}
	return f, started
}

func TestSandboxCheckRejectsDeadlineEvidence(t *testing.T) {
	for _, phase := range []string{"vet", "test", "acceptance", "after_acceptance"} {
		t.Run(phase, func(t *testing.T) {
			f, started := s397IncompleteFixture(t, phase)
			ws := s397Draft(t, f)
			begin := time.Now()
			c, err := Check(context.Background(), ws, f.root, s397Checker(f, 8*time.Second))
			if err != nil || c == nil {
				t.Fatalf("the sandbox's own deadline must deliver its partial result: result=%+v, error=%v", c, err)
			}
			if elapsed := time.Since(begin); elapsed > 13*time.Second {
				t.Errorf("deadline check took %s, want at most 8s plus 5s cleanup", elapsed)
			}
			if _, err := os.Stat(started); err != nil {
				t.Fatalf("the intended %s phase never started: %v\n%s", phase, err, c.Run.Output)
			}
			if !c.Run.DeadlineExceeded || c.Run.Passed() {
				t.Errorf("deadline must remain a failed run: %+v", c.Run)
			}
			want := "not_run"
			if phase == "acceptance" {
				want = "running"
			} else if phase == "after_acceptance" {
				want = "failed"
			}
			s397Status(t, c.Run, "pending", "TestNeedsChange", "accept", want)
			if c.Problem == "" {
				t.Errorf("Check approved a plan using an acceptance test that was %s when %s hit the deadline", want, phase)
			}
		})
	}
	t.Run("context_cancellation", s397CheckRejectsInterruptedRun)
	t.Run("completed_failure_remains_eligible", s397CheckAllowsCompletedFailures)
}

func s397CheckRejectsInterruptedRun(t *testing.T) {
	for _, phase := range []string{"vet", "test", "acceptance", "after_acceptance"} {
		t.Run(phase, func(t *testing.T) {
			f, marker := s397IncompleteFixture(t, phase)
			ws := s397Draft(t, f)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan bool, 1)
			go func() {
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				limit := time.NewTimer(20 * time.Second)
				defer limit.Stop()
				for {
					select {
					case <-ticker.C:
						if _, err := os.Stat(marker); err == nil {
							started <- true
							cancel()
							return
						}
					case <-limit.C:
						started <- false
						cancel()
						return
					case <-ctx.Done():
						started <- false
						return
					}
				}
			}()
			begin := time.Now()
			c, err := Check(ctx, ws, f.root, s397Checker(f, 30*time.Second))
			cancel()
			if !<-started {
				t.Fatalf("Check returned or setup expired before the %s phase could be interrupted: result=%+v, error=%v", phase, c, err)
			}
			if elapsed := time.Since(begin); elapsed > 25*time.Second {
				t.Errorf("cancelled check did not return promptly: %s", elapsed)
			}
			if err == nil && (c == nil || c.Problem == "") {
				t.Errorf("Check must fail closed after context cancellation during %s: result=%+v", phase, c)
			}
		})
	}
}

func TestSandboxCheckRejectsUnfinishedAcceptance(t *testing.T) {
	for _, status := range []string{"not_run", "running"} {
		t.Run(status, func(t *testing.T) {
			f := s397New(t)
			f.pkg(t, "unfinished", "")
			// The normal test-phase invocation fails completely. During the
			// acceptance replay, either TestMain omits m.Run or the test exits
			// the process before reporting a result. Neither is failure evidence.
			f.accept(t, "unfinished", "TestNeedsChange", fmt.Sprintf(`package unfinished
import ("os"; "testing")
const seen = %q
func TestMain(m *testing.M) {
  if %t { if _, err := os.Stat(seen); err == nil { os.Exit(0) } }
  os.Exit(m.Run())
}
func TestNeedsChange(t *testing.T) {
  if %t { if _, err := os.Stat(seen); err == nil { os.Exit(2) } }
  if err := os.WriteFile(seen, []byte("normal test completed"), 0600); err != nil { t.Fatal(err) }
  t.Fatal("the proposed behavior is missing")
}
`, filepath.Join(f.observe, "normal-test-finished"), status == "not_run", status == "running"))
			f.warm(t)
			c, err := Check(context.Background(), s397Draft(t, f), f.root, s397Checker(f, 30*time.Second))
			if err != nil || c == nil {
				t.Fatalf("Check should report the incomplete acceptance run: result=%+v, error=%v", c, err)
			}
			if c.Run.DeadlineExceeded {
				t.Fatal("this fixture exits on its own; it must exercise incomplete evidence independently of the outer deadline")
			}
			s397Status(t, c.Run, "unfinished", "TestNeedsChange", "test", "failed")
			s397Status(t, c.Run, "unfinished", "TestNeedsChange", "accept", status)
			if c.Problem == "" {
				t.Errorf("Check approved a plan whose acceptance test was %s, with no completed failing result", status)
			}
		})
	}
}

func s397CheckAllowsCompletedFailures(t *testing.T) {
	for _, ordinaryFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("ordinary_failure_%t", ordinaryFails), func(t *testing.T) {
			f := s397New(t)
			f.pkg(t, "pending", fmt.Sprintf(`package pending
import "testing"
func TestOrdinary(t *testing.T) { if %t { t.Fatal("existing regression") } }
`, ordinaryFails))
			f.accept(t, "pending", "TestNeedsChange", `package pending
import "testing"
func TestNeedsChange(t *testing.T) { t.Fatal("the proposed behavior is missing") }
`)
			f.warm(t)
			c, err := Check(context.Background(), s397Draft(t, f), f.root, s397Checker(f, 30*time.Second))
			if err != nil || c == nil {
				t.Fatalf("Check: result=%+v, error=%v", c, err)
			}
			if c.Problem != "" {
				t.Errorf("a completed failing acceptance test must remain eligible: %s\n%s", c.Problem, c.Run.Output)
			}
			if c.Run.DeadlineExceeded || c.Run.Passed() || c.Run.Accepted["pending/pinned_accept_test.go:TestNeedsChange"] {
				t.Errorf("completed failure was misclassified: %+v", c.Run)
			}
			s397Status(t, c.Run, "pending", "TestNeedsChange", "accept", "failed")
			want := "passed"
			if ordinaryFails {
				want = "failed"
			}
			s397Status(t, c.Run, "pending", "TestOrdinary", "test", want)
		})
	}
	t.Run("new_api_does_not_build_yet", func(t *testing.T) {
		// Plans may introduce an API the original checkout cannot compile.
		// A completed compiler failure qualifies only when every source error
		// is attributable to production APIs, without running initialization.
		f := s397New(t)
		f.pkg(t, "pending", "")
		f.accept(t, "pending", "TestNeedsChange", `package pending
import "testing"
func TestNeedsChange(t *testing.T) { RequireNewAPI397() }
`)
		c, err := Check(context.Background(), s397Draft(t, f), f.root, s397Checker(f, 30*time.Second))
		if err != nil || c == nil {
			t.Fatalf("Check: result=%+v, error=%v", c, err)
		}
		if c.Problem != "" || c.Run.DeadlineExceeded || c.Run.Accepted["pending/pinned_accept_test.go:TestNeedsChange"] {
			t.Errorf("a completed compiler failure must remain eligible evidence for a new API: problem=%q result=%+v", c.Problem, c.Run)
		}
	})
}

func TestSandboxCheckAttributesCompilerErrors(t *testing.T) {
	for _, tc := range []struct {
		name, production, declarations, body, diagnostic string
	}{
		{"unused beside API", "", "", `RequireNewAPI397(); unused := 1`, "declared and not used"},
		{"bad operand beside API", "", "", `RequireNewAPI397(1 + "bad")`, "mismatched types"},
		{"nested local call", "func Value397(n int) {}", "func helper397(n int) int { return n }", `Value397(helper397("bad"))`, "cannot use"},
		{"nested local literal", "func Value397(n interface{}) {}", "type local397 struct { Value int }", `Value397(local397{Value: "bad"})`, "cannot use"},
		{"local result type", "func Value397() int { return 0 }", "", `var local int = Value397(); var other string = local; _ = other`, "cannot use"},
		{"local missing member", "", "type local397 struct{}", `RequireNewAPI397(); local397{}.Missing397()`, "undefined"},
		{"division by zero", "func Value397() int { return 0 }", "", `_ = Value397() / 0`, "division by zero"},
		{"local shift count", "func Value397() int { return 1 }", "", `shift := "bad"; _ = Value397() << shift`, "shift count"},
		{"external local shift count", "func Value397() int { return 1 }", "", `shift := "bad"; _ = product.Value397() << shift`, "shift count"},
		{"local shifted operand", "func Value397() int { return 1 }", "", `value := "bad"; _ = value >> Value397()`, "shifted operand"},
		{"external local shifted operand", "func Value397() int { return 1 }", "", `value := "bad"; _ = value >> product.Value397()`, "shifted operand"},
		{"external negative shift count", "func Value397() int { return 1 }", "", `_ = product.Value397() << -1`, "negative shift count"},
		{"address of call result", "func Value397() int { return 0 }", "", `_ = &Value397()`, "cannot take address"},
		{"address of parenthesized call result", "func Value397() int { return 0 }", "", `_ = &(Value397())`, "cannot take address"},
		{"external address of call result", "func Value397() int { return 0 }", "", `_ = &product.Value397()`, "cannot take address"},
		{"test method on production receiver", "type Box397 struct{}", "func (Box397) helper397(n int) {}", `Box397{}.helper397("wrong")`, "cannot use"},
		{"test method arity", "type Box397 struct{}", "func (Box397) helper397(n int) {}", `Box397{}.helper397()`, "not enough arguments"},
		{"test method value", "type Box397 struct{}", "func (Box397) helper397(n int) {}", `helper := Box397{}.helper397; helper("wrong")`, "cannot use"},
		{"test method expression", "type Box397 struct{}", "func (Box397) helper397(n int) {}", `Box397.helper397(Box397{}, "wrong")`, "cannot use"},
		{"test method result count", "type Box397 struct{}", "func (Box397) helper397() int { return 0 }", `var a, b = Box397{}.helper397(); _, _ = a, b`, "assignment mismatch"},
		{"test method missing result", "type Box397 struct{}", "func (Box397) helper397() {}", `if (Box397{}).helper397() == 7 { t.Fatal("wrong result") }`, "(no value) used as value"},
		{"test helper missing result", "func Value397(n int) {}", "func helper397() {}", `Value397(helper397())`, "(no value) used as value"},
		{"external test helper missing result", "func Value397(n int) {}", "func helper397() {}", `product.Value397(helper397())`, "(no value) used as value"},
		{"standard library call", "", `import "fmt"`, `RequireNewAPI397(); _ = fmt.Sprintf(7)`, "cannot use"},
		{"changed inferred result", "func Value397() int { return 0 }", "", `got := Value397(); var want string = got; _ = want`, ""},
		{"changed outer call arity", "func Value397() {}", "func helper397() int { return 1 }", `Value397(helper397())`, ""},
		{"changed outer argument type", "func Value397(s string) {}", "func helper397() int { return 1 }", `Value397(helper397())`, ""},
		{"changed field with local value", "type Options397 struct{ Value string }", "func helper397() int { return 1 }", `_ = Options397{Value: helper397()}`, ""},
		{"changed inferred receiver", "func Value397() int { return 0 }", "", `got := Value397(); got.Future397()`, ""},
		{"changed production member", "type Options397 struct{}", "", `_ = Options397{NewField397: "value"}`, ""},
		{"external changed argument", "func Value397(n int) {}", "", `product.Value397("changed")`, ""},
		{"diagnostic words in value", "func Value397(n int) {}", "", `Value397("not used; arguments in call; division by zero; undefined (")`, ""},
		{"external changed field", "type Options397 struct{ Value int }", "", `_ = product.Options397{Value: "changed"}`, ""},
		{"external private function", "", "", `product.hidden397()`, "undefined"},
		{"external private member", "type Options397 struct{}", "", `product.Options397{}.hidden397()`, "undefined"},
		{"external private field", "type Options397 struct{}", "", `_ = product.Options397{hidden397: 1}`, "unknown field"},
		{"external nested local call", "func Value397(n int) {}", "func helper397(n int) int { return n }", `product.Value397(helper397("bad"))`, "cannot use"},
		{"production init stays unexecuted", `func init() { panic("must not run") }`, "", `RequireNewAPI397()`, ""},
		// An operand the test fixes must admit the operator with some type.
		{"local boolean sum", "func Value397() int { return 1 }", "", `var local bool; _ = local + Value397()`, "mismatched types"},
		{"external local boolean sum", "func Value397() int { return 1 }", "", `var local bool; _ = local + product.Value397()`, "mismatched types"},
		{"local slice comparison", "func Value397() []int { return nil }", "", `var local []int; _ = local == Value397()`, "slice can only be compared to nil"},
		{"changed result beside local sum", `func Value397() string { return "" }`, "", `var local int; _ = local + Value397()`, ""},
		// Builtins, ranges and assertions name the operand they refuse.
		{"local length", "", "", `RequireNewAPI397(); var local int; _ = len(local)`, "invalid argument"},
		{"local range", "", "", `RequireNewAPI397(); var local bool; for range local {}`, "cannot range over"},
		{"local assertion", "", "", `RequireNewAPI397(); var local int; _, _ = local.(string)`, "is not an interface"},
		{"changed result length", "func Value397() int { return 0 }", "", `_ = len(Value397())`, ""},
		{"external changed result length", "func Value397() int { return 0 }", "", `_ = len(product.Value397())`, ""},
		{"changed result range", "func Value397() bool { return false }", "", `for range Value397() {}`, ""},
		{"changed result assertion", "func Value397() int { return 0 }", "", `got := Value397(); _, _ = got.(string)`, ""},
		// A declared type naming a production type changes with it.
		{"local declared result", "", "func helper397() int { return \"bad\" }", `RequireNewAPI397(); _ = helper397()`, "cannot use"},
		{"changed declared result", "type Type397 int", "func helper397() Type397 { return \"changed\" }", `_ = helper397()`, ""},
		{"external changed declared result", "type Type397 int", "func helper397() product.Type397 { return \"changed\" }", `_ = helper397()`, ""},
		{"changed helper parameter", "type Type397 int", "func helper397(v Type397) {}", `helper397("changed")`, ""},
		{"changed local field type", "type Type397 int", "type local397 struct{ V Type397 }", `_ = local397{V: "changed"}`, ""},
		{"changed element type", "type Type397 int", "", `_ = []Type397{"changed"}`, ""},
		{"local field beside production field", "type Type397 int", "type local397 struct{ V Type397; W int }", `_ = local397{W: "bad"}`, "cannot use"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := s397New(t)
			f.pkg(t, "pending", "")
			s397Write(t, filepath.Join(f.root, "pending", "code.go"), "package pending\n"+tc.production+"\n", 0o644)
			header := "package pending\nimport \"testing\"\n"
			if strings.HasPrefix(tc.name, "external ") {
				header = "package pending_test\nimport (\"testing\"; product \"example.com/shards397/pending\")\n"
			}
			f.accept(t, "pending", "TestNeedsChange", header+tc.declarations+"\nfunc TestNeedsChange(t *testing.T) { "+tc.body+" }\n")
			c, err := Check(context.Background(), s397Draft(t, f), f.root, s397Checker(f, 90*time.Second))
			if err != nil || c == nil || c.Run.DeadlineExceeded || c.Run.Interrupted {
				t.Fatalf("compiler check did not complete: result=%+v error=%v", c, err)
			}
			if tc.diagnostic == "" {
				if c.Problem != "" {
					t.Fatal(c.Feedback())
				}
				s397Status(t, c.Run, "pending", "TestNeedsChange", "accept", "failed")
				return
			}
			s397Status(t, c.Run, "pending", "TestNeedsChange", "accept", "refused")
			// Feedback must retain the reason even when the raw transcript's
			// tail no longer contains the compiler output.
			c.Run.Output = strings.Repeat("later output\n", 1000)
			if c.Problem == "" || !strings.Contains(c.Feedback(), "pinned_accept_test.go:") || !strings.Contains(c.Feedback(), tc.diagnostic) {
				t.Fatalf("lost compiler refusal: %s", c.Feedback())
			}
		})
	}
}

func TestSandboxCheckProductionSignatureCorrections(t *testing.T) {
	for _, tc := range []struct {
		name, base, fixed, declarations, body string
	}{
		{"missing result", "func Value397() {}", "func Value397() int { return 7 }", "", `if Value397() != 7 { t.Fatal("wrong result") }`},
		{"changed shift count", `func Value397() string { return "bad" }`, "func Value397() int { return 7 }", "", `shift := Value397(); if 128 >> shift != 1 { t.Fatal("wrong shift") }`},
		{"changed shifted operand", `func Value397() string { return "bad" }`, "func Value397() int { return 7 }", "", `value := Value397(); if value << 1 != 14 { t.Fatal("wrong shift") }`},
		{"local result count", "func Value397() int { return 7 }", "func Value397() (int, int) { return 7, 8 }", "", `var a, b = Value397(); if a != 7 || b != 8 { t.Fatal(a, b) }`},
		{"package result count", "func Value397() int { return 7 }", "func Value397() (int, int) { return 7, 8 }", "var a, b = Value397()", `if a != 7 || b != 8 { t.Fatal(a, b) }`},
		{"explicit result count", "func Value397() int { return 7 }", "func Value397() (int, int) { return 7, 8 }", "", `var a, b int = Value397(); if a != 7 || b != 8 { t.Fatal(a, b) }`},
		{"production method", "type Box397 struct{}\nfunc (Box397) Value397(n int) int { return n }", "type Box397 struct{}\nfunc (Box397) Value397(s string) int { return len(s) }", "", `if (Box397{}).Value397("changed") != 7 { t.Fatal("wrong result") }`},
		{"result length", "func Value397() int { return 0 }", "func Value397() []int { return []int{1, 2, 3} }", "", `if len(Value397()) != 3 { t.Fatal("wrong length") }`},
		{"result range", "func Value397() bool { return false }", "func Value397() []int { return []int{7} }", "", `for _, v := range Value397() { if v != 7 { t.Fatal(v) } }`},
		{"result assertion", "func Value397() int { return 0 }", `func Value397() any { return "x" }`, "", `got := Value397(); if s, ok := got.(string); !ok || s != "x" { t.Fatal(got) }`},
		{"result beside local sum", `func Value397() string { return "" }`, "func Value397() int { return 7 }", "", `local := 1; if local+Value397() != 8 { t.Fatal("wrong sum") }`},
		{"declared result type", "type Type397 int", "type Type397 string", `func helper397() Type397 { return "changed" }`, `if helper397() != "changed" { t.Fatal("wrong result") }`},
		{"helper parameter type", "type Type397 int", "type Type397 string", "func helper397(v Type397) Type397 { return v }", `if helper397("changed") != "changed" { t.Fatal("wrong result") }`},
		{"literal field type", "type Type397 int", "type Type397 string", "type local397 struct{ V Type397 }", `if (local397{V: "changed"}).V != "changed" { t.Fatal("wrong field") }`},
	} {
		for _, external := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/external_%t", tc.name, external), func(t *testing.T) {
				f := s397New(t)
				f.pkg(t, "pending", "")
				production := filepath.Join(f.root, "pending", "code.go")
				s397Write(t, production, "package pending\n"+tc.base+"\n", 0o644)
				header := "package pending\nimport \"testing\"\n"
				declarations, body := tc.declarations, tc.body
				if external {
					header = "package pending_test\nimport (\"testing\"; product \"example.com/shards397/pending\")\n"
					qualify := strings.NewReplacer("Value397()", "product.Value397()", "Box397{}", "product.Box397{}", "Type397", "product.Type397")
					declarations, body = qualify.Replace(declarations), qualify.Replace(body)
				}
				f.accept(t, "pending", "TestNeedsChange", header+declarations+"\nfunc TestNeedsChange(t *testing.T) { "+body+" }\n")
				c, err := Check(context.Background(), s397Draft(t, f), f.root, s397Checker(f, 90*time.Second))
				if err != nil || c == nil {
					t.Fatalf("Check: result=%+v error=%v", c, err)
				}
				if c.Problem != "" {
					t.Fatal(c.Feedback())
				}
				s397Status(t, c.Run, "pending", "TestNeedsChange", "accept", "failed")

				// Only production changes; the pinned test must now pass at runtime.
				fixed, err := format.Source([]byte("package pending\n" + tc.fixed + "\n"))
				if err != nil {
					t.Fatal(err)
				}
				s397Write(t, production, string(fixed), 0o644)
				file := "pending/pinned_accept_test.go"
				s397Write(t, filepath.Join(f.root, file), f.plan.Sources[file], 0o644)
				r := f.run(t, 90*time.Second, 30*time.Second)
				if !r.Passed() {
					t.Fatalf("production correction did not pass: %s\n%s", r.Summary(), r.Output)
				}
				s397Status(t, r, "pending", "TestNeedsChange", "accept", "passed")
			})
		}
	}
}
