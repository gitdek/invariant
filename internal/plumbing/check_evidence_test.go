package plumbing

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSandboxCheckRejectsSkippedAcceptance(t *testing.T) {
	f := s397New(t)
	f.pkg(t, "pending", "")
	f.accept(t, "pending", "TestNeedsChange", `package pending
import "testing"
func TestNeedsChange(t *testing.T) { t.Skip("the proposed behavior was not checked") }
`)
	f.warm(t)
	c, err := Check(context.Background(), s397Draft(t, f), f.root, s397Checker(f, 30*time.Second))
	if err != nil || c == nil {
		t.Fatalf("Check: result=%+v, error=%v", c, err)
	}
	s397Status(t, c.Run, "pending", "TestNeedsChange", "accept", "skipped")
	if c.Problem == "" || !strings.Contains(c.Problem, "TestNeedsChange") {
		t.Errorf("a skipped acceptance test is not evidence that the proposed behavior is missing: %q", c.Problem)
	}
}

func TestSandboxCheckRejectsTimedOutOrdinaryPhase(t *testing.T) {
	f := s397New(t)
	f.pkg(t, "pending", `package pending
import ("testing"; "time")
func TestOrdinary(t *testing.T) { time.Sleep(60*time.Second) }
`)
	f.accept(t, "pending", "TestNeedsChange", `package pending
import "testing"
func TestNeedsChange(t *testing.T) { t.Fatal("the proposed behavior is missing") }
`)
	f.warm(t)
	s := s397Checker(f, 30*time.Second)
	s.ShardTimeout = time.Second
	c, err := Check(context.Background(), s397Draft(t, f), f.root, s)
	if err != nil || c == nil {
		t.Fatalf("Check: result=%+v, error=%v", c, err)
	}
	s397Status(t, c.Run, "pending", "TestNeedsChange", "accept", "failed")
	if !c.Run.TimedOut || c.Run.DeadlineExceeded {
		t.Errorf("the touched-test timeout must remain distinct from the whole-run deadline: %+v", c.Run)
	}
	if c.Problem == "" || !strings.Contains(c.Problem, "timed out") {
		t.Errorf("a completed acceptance failure must not hide an earlier phase timeout: %q", c.Problem)
	}
}

func TestSandboxCheckRejectsFailureBeforeRunnerRecords(t *testing.T) {
	f := s397New(t)
	f.pkg(t, "pending", "")
	f.accept(t, "pending", "TestNeedsChange", `package pending
import "testing"
func TestNeedsChange(t *testing.T) { t.Fatal("the proposed behavior is missing") }
`)
	s397Write(t, filepath.Join(f.bin, "docker"), "#!/bin/sh\necho 'fixture: container could not start' >&2\nexit 125\n", 0o755)
	c, err := Check(context.Background(), s397Draft(t, f), f.root, s397Checker(f, 30*time.Second))
	if err != nil || c == nil {
		t.Fatalf("Check must retain the failed startup result: result=%+v, error=%v", c, err)
	}
	if c.Problem == "" || !c.Run.Interrupted || c.Run.DeadlineExceeded {
		t.Errorf("an unsuccessful startup is not completed failure evidence: problem=%q result=%+v", c.Problem, c.Run)
	}
	if c.Run.Accepted["pending/pinned_accept_test.go:TestNeedsChange"] {
		t.Error("an acceptance test that never started was accepted")
	}
}
