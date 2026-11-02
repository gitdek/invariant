package plumbing

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Go's test2json omits OutputType for ordinary diagnostics. Failure-looking
// text followed by a premature exit must not become completed evidence.
func TestSandboxCheckRejectsDiagnosticFailureBeforeExit(t *testing.T) {
	for _, stream := range []string{"Stdout", "Stderr"} {
		t.Run(stream, func(t *testing.T) {
			f := s397New(t)
			f.pkg(t, "pending", "")
			f.accept(t, "pending", "TestNeedsChange", fmt.Sprintf(`package pending
import ("fmt"; "os"; "testing")
func TestNeedsChange(t *testing.T) {
  fmt.Fprintln(os.%s, "--- FAIL: TestNeedsChange (0.00s)")
  os.Exit(2)
}
`, stream))
			f.warm(t)
			c, err := Check(context.Background(), s397Draft(t, f), f.root, s397Checker(f, 30*time.Second))
			if err != nil || c == nil {
				t.Fatalf("Check: result=%+v, error=%v", c, err)
			}
			if c.Run.Passed() || c.Run.Tests || !c.Run.Vet || c.Run.DeadlineExceeded || c.Run.TimedOut || c.Run.Interrupted {
				t.Errorf("a premature test exit was misclassified: %+v", c.Run)
			}
			for _, phase := range []string{"test", "accept"} {
				s397Status(t, c.Run, "pending", "TestNeedsChange", phase, "running")
			}
			if accepted, found := c.Run.Accepted["pending/pinned_accept_test.go:TestNeedsChange"]; !found || accepted {
				t.Errorf("unfinished acceptance = %v, present=%v; want false", accepted, found)
			}
			if !strings.Contains(c.Problem, "did not complete with failure evidence") || !strings.Contains(c.Problem, "TestNeedsChange (running)") {
				t.Errorf("Check must reject the unfinished acceptance evidence: %q", c.Problem)
			}
			if !strings.Contains(c.Run.Output, "--- FAIL: TestNeedsChange (0.00s)") {
				t.Errorf("failure-looking diagnostic was lost:\n%s", c.Run.Output)
			}
		})
	}
}
