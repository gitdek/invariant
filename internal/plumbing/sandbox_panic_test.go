package plumbing

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// A panic emits a real testing-package FAIL frame before its stack trace,
// but stdin-mode test2json need not emit a terminal fail action afterward.
// That reported failure is completed evidence, including for the parent of
// a panicking subtest. The process's nonzero exit alone is not evidence.
func TestSandboxCheckAllowsAcceptancePanic(t *testing.T) {
	for _, subtest := range []bool{false, true} {
		t.Run(fmt.Sprintf("subtest_%t", subtest), func(t *testing.T) {
			f := s397New(t)
			f.pkg(t, "pending", "")
			body := `panic("the proposed behavior is missing")`
			if subtest {
				body = `t.Run("missing_behavior", func(t *testing.T) { ` + body + ` })`
			}
			f.accept(t, "pending", "TestNeedsChange", fmt.Sprintf(`package pending
import "testing"
func TestNeedsChange(t *testing.T) { %s }
`, body))
			f.warm(t)
			c, err := Check(context.Background(), s397Draft(t, f), f.root, s397Checker(f, 30*time.Second))
			if err != nil || c == nil {
				t.Fatalf("Check: result=%+v, error=%v", c, err)
			}
			if c.Problem != "" {
				t.Errorf("a panic with a completed failure report must remain eligible: %s\n%s", c.Problem, c.Run.Output)
			}
			if c.Run.Passed() || c.Run.Tests || c.Run.DeadlineExceeded || c.Run.TimedOut || c.Run.Interrupted {
				t.Errorf("a completed panic was misclassified: %+v", c.Run)
			}
			if accepted, found := c.Run.Accepted["pending/pinned_accept_test.go:TestNeedsChange"]; !found || accepted {
				t.Errorf("acceptance panic = %v, present=%v; want a recorded failure", accepted, found)
			}
			if !strings.Contains(c.Run.Output, "panic: the proposed behavior is missing") {
				t.Errorf("panic diagnostic was lost:\n%s", c.Run.Output)
			}
			for _, phase := range []string{"test", "accept"} {
				s397Status(t, c.Run, "pending", "TestNeedsChange", phase, "failed")
				if subtest {
					s397Status(t, c.Run, "pending", "TestNeedsChange/missing_behavior", phase, "failed")
				}
			}
		})
	}
}
