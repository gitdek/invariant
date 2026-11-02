package plumbing

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Until test2json sees a marked framing line, it also accepts unmarked
// framing, for older test binaries. A TestMain that prints a test's lifecycle
// and exits before m.Run never ran that test, so its text must stay a
// diagnostic and never become completed evidence. Only the sandbox's test
// runs pass -test.v=test2json, so warming and discovery still reach m.Run.
func TestSandboxCheckRejectsFailureReportBeforeTestsRun(t *testing.T) {
	for _, stream := range []string{"Stdout", "Stderr"} {
		t.Run(stream, func(t *testing.T) {
			f := s397New(t)
			f.pkg(t, "pending", "")
			f.accept(t, "pending", "TestNeedsChange", fmt.Sprintf(`package pending
import ("fmt"; "os"; "testing")
func TestMain(m *testing.M) {
  for _, arg := range os.Args[1:] {
    if arg == "-test.v=test2json" {
      fmt.Fprintln(os.%[1]s, "=== RUN   TestNeedsChange")
      fmt.Fprintln(os.%[1]s, "--- FAIL: TestNeedsChange (0.00s)")
      os.Exit(1)
    }
  }
  os.Exit(m.Run())
}
func TestNeedsChange(t *testing.T) {}
`, stream))
			f.warm(t)
			c, err := Check(context.Background(), s397Draft(t, f), f.root, s397Checker(f, 30*time.Second))
			if err != nil || c == nil {
				t.Fatalf("Check: result=%+v, error=%v", c, err)
			}
			if c.Run.Passed() || c.Run.DeadlineExceeded || c.Run.TimedOut || c.Run.Interrupted {
				t.Errorf("a test binary that exits before its tests was misclassified: %+v", c.Run)
			}
			s397Status(t, c.Run, "pending", "TestNeedsChange", "accept", "not_run")
			if accepted, found := c.Run.Accepted["pending/pinned_accept_test.go:TestNeedsChange"]; !found || accepted {
				t.Errorf("never-run acceptance = %v, present=%v; want false", accepted, found)
			}
			if !strings.Contains(c.Problem, "did not complete with failure evidence") || !strings.Contains(c.Problem, "TestNeedsChange (not_run)") {
				t.Errorf("Check must reject an acceptance test that never ran: %q", c.Problem)
			}
			if !strings.Contains(c.Run.Output, "--- FAIL: TestNeedsChange (0.00s)") {
				t.Errorf("the pre-test diagnostic was lost:\n%s", c.Run.Output)
			}
		})
	}
}
