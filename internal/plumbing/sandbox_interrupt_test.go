package plumbing

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSandboxDeadlineStartsBeforeCheckoutPreparation(t *testing.T) {
	p := &Plan{Tests: []Test{{Name: "TestPending", File: "pending_test.go"}}}
	s := Sandbox{Timeout: time.Nanosecond}
	r, err := s.Run(context.Background(), "a checkout which does not exist", p)
	if err != nil || !r.DeadlineExceeded || r.Passed() || len(r.Failing()) != 1 {
		t.Fatalf("expired before staging: %+v, %v", r, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, err = (Sandbox{}).Run(ctx, "a checkout which does not exist", p)
	if err != nil || r.DeadlineExceeded || !r.Interrupted || r.Passed() || !strings.Contains(r.Summary(), "interrupted") {
		t.Fatalf("canceled before staging: %+v, %v", r, err)
	}
}

func TestSandboxDiscoveryDeadlineKeepsKnownTests(t *testing.T) {
	f := s397New(t)
	f.pkg(t, "alpha", "package alpha\nimport \"testing\"\nfunc TestKnown(t *testing.T) {}\n")
	f.pkg(t, "zulu", `package zulu
import ("flag"; "os"; "testing"; "time")
func TestHidden(t *testing.T) {}
func TestMain(m *testing.M) {
  flag.Parse()
  if flag.Lookup("test.list").Value.String() != "" { time.Sleep(60*time.Second) }
  os.Exit(m.Run())
}
`)
	f.contract(t, false)
	f.warm(t)
	r := f.run(t, 8*time.Second, 30*time.Second)
	if r.Passed() || !r.DeadlineExceeded {
		t.Fatalf("discovery was not stopped: %s\n%s", r.Summary(), r.Output)
	}
	s397Status(t, r, "alpha", "TestKnown", "test", "not_run")
	s397Summary(t, r, "alpha", "TestKnown", "unstarted")
}

func TestSandboxLegacyParentPassSurvivesSkippedSubtest(t *testing.T) {
	p := &Plan{Tests: []Test{{Name: "TestBudget", File: "budget_test.go"}}}
	r := parse(`@@invariant vet=0
@@invariant test=0
--- PASS: TestBudget (0.01s)
    --- SKIP: TestBudget/real_image (0.00s)
@@invariant accept=0
`, p)
	if !r.Passed() || !r.Accepted["budget_test.go:TestBudget"] {
		t.Fatalf("skipped child replaced its passing parent: %s", r.Summary())
	}
}

// TestMain may outlive m.Run, after Go has stopped its test timeout timer.
// The shard's wall deadline still applies, and other shards and acceptance
// packages finish independently.
func TestSandboxShardDeadlineIncludesTestMain(t *testing.T) {
	f := s397New(t)
	f.pkg(t, "stuck", `package stuck
import ("flag"; "os"; "strings"; "testing"; "time")
func TestFinished(t *testing.T) {}
func TestMain(m *testing.M) {
  code := m.Run()
  if strings.Contains(flag.Lookup("test.run").Value.String(), "TestFinished") { time.Sleep(60*time.Second) }
  os.Exit(code)
}
`)
	f.contract(t, false)
	f.warm(t)
	r := f.run(t, 30*time.Second, time.Second)
	if r.Passed() || r.Tests || r.DeadlineExceeded || !r.Accepted["contract/pinned_accept_test.go:TestRatified"] {
		t.Fatalf("TestMain timeout lost independent results: %s\n%s", r.Summary(), r.Output)
	}
	s397Summary(t, r, "stuck", "TestFinished", "shard", "timeout")
}

// A passing function cannot satisfy acceptance if TestMain fails afterward.
func TestSandboxAcceptanceIncludesThePackageExitStatus(t *testing.T) {
	f := s397New(t)
	f.accept(t, "bad", "TestRatified", `package bad
import ("flag"; "os"; "strings"; "testing")
func TestRatified(t *testing.T) {}
func TestMain(m *testing.M) {
  code := m.Run()
  if strings.Contains(flag.Lookup("test.run").Value.String(), "TestRatified") { code = 3 }
  os.Exit(code)
}
`)
	f.contract(t, false)
	f.warm(t)
	r := f.run(t, 30*time.Second, 20*time.Second)
	if r.Passed() || r.Accepted["bad/pinned_accept_test.go:TestRatified"] || !r.Accepted["contract/pinned_accept_test.go:TestRatified"] {
		t.Fatalf("package exit status did not decide acceptance: %s\n%s", r.Summary(), r.Output)
	}
	s397Summary(t, r, "accept", "bad", "TestRatified")
}
