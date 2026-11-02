package plumbing

import (
	"context"
	"encoding/json"
	"fmt"
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
		// An observed compiler failure is conclusive evidence, distinct from
		// a deadline that prevented the compiler or acceptance test running.
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
