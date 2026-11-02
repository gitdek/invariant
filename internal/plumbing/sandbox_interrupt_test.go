package plumbing

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestSandboxCheckRejectsSignalAfterAcceptanceFailure(t *testing.T) {
	f := s397New(t)
	f.pkg(t, "pending", "")
	f.accept(t, "pending", "TestNeedsChange", fmt.Sprintf(`package pending
import ("os"; "syscall"; "testing")
var ran bool
func TestNeedsChange(t *testing.T) {
  ran = true
  t.Fatal("the proposed behavior is missing")
}
func TestMain(m *testing.M) {
  before, _ := os.ReadFile(%q)
  code := m.Run()
  if ran {
    if len(before) > 0 {
      if err := syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil { panic(err) }
      os.Exit(99)
    }
    if err := os.WriteFile(%q, []byte("ordinary failure completed"), 0600); err != nil { panic(err) }
  }
  os.Exit(code)
}
`, filepath.Join(f.observe, "ordinary-failure"), filepath.Join(f.observe, "ordinary-failure")))
	f.warm(t)
	c, err := Check(context.Background(), s397Draft(t, f), f.root, s397Checker(f, 30*time.Second))
	if err != nil || c == nil {
		t.Fatalf("Check: result=%+v, error=%v", c, err)
	}
	s397Status(t, c.Run, "pending", "TestNeedsChange", "test", "failed")
	s397Status(t, c.Run, "pending", "TestNeedsChange", "accept", "failed")
	if c.Problem == "" || !c.Run.Interrupted || c.Run.DeadlineExceeded {
		t.Errorf("a completed failure must not hide the acceptance process's later signal: problem=%q result=%+v", c.Problem, c.Run)
	}
}
