package plumbing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/synth"
)

func TestSandboxDiscoveryUsesOrdinaryDeadline(t *testing.T) {
	record := v498Spy(t)
	bin := t.TempDir()
	completed := filepath.Join(t.TempDir(), "inventory-completed")
	s397Write(t, filepath.Join(bin, "git"), fmt.Sprintf(`#!/usr/bin/env python3
import pathlib, sys, time
time.sleep(1)
pathlib.Path(%q).write_text("completed")
sys.exit(1)
`, completed), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, dir := range []string{"internal/plumbing", "internal/verify"} {
		t.Run(dir, func(t *testing.T) {
			root := t.TempDir()
			s397Write(t, filepath.Join(root, dir, "code.go"), "package fixture\n", 0o644)
			plan := &Plan{Files: []string{dir + "/code.go"},
				Tests: []Test{{Name: "TestPending", File: dir + "/pending_test.go"}}}
			// Leave a quarter second of the production discovery allowance,
			// without shortening it through an override or parent deadline.
			started := time.Now().Add(-SandboxTimeout + 250*time.Millisecond)
			r, err := (Sandbox{Image: "fixture", ModCache: t.TempDir()}).run(v498Watchdog(t), root, plan, started)
			if err != nil || r.Passed() || !r.DeadlineExceeded || r.Interrupted {
				t.Fatalf("discovery deadline must return partial evidence: %+v, %v", r, err)
			}
			if len(r.Failing()) != 1 || r.Failing()[0] != dir+"/pending_test.go:TestPending" {
				t.Fatalf("lost unstarted acceptance test: %s", r.Summary())
			}
			for _, file := range []string{completed, record} {
				if _, err := os.Stat(file); !os.IsNotExist(err) {
					t.Fatalf("discovery outlived its ordinary deadline: %s: %v", file, err)
				}
			}
		})
	}
}

func TestVerifyDiscoveryPreservesEntryTime(t *testing.T) {
	record := v498Spy(t)
	root := t.TempDir()
	s397Write(t, filepath.Join(root, "internal/verify/code.go"), "package fixture\n", 0o644)
	started := time.Now().Add(-11 * time.Minute)
	r, err := (Sandbox{Image: "fixture", ModCache: t.TempDir()}).run(v498Watchdog(t), root,
		&Plan{Files: []string{"internal/verify/code.go"}}, started)
	if err != nil || !r.Passed() {
		t.Fatalf("verify check after discovery: %+v, %v", r, err)
	}
	observed := v498Read(t, record)
	if len(observed) != 1 {
		t.Fatalf("got %d checks, want one", len(observed))
	}
	v498Budgets(t, observed[0], started, 25*time.Minute, 20*time.Minute)
}

type v498DeletingAgent struct {
	plan    *Plan
	sandbox Sandbox
	job     synth.Job
	started time.Time
}

func (*v498DeletingAgent) Name() string { return "deleting-budget-fixture" }

func (a *v498DeletingAgent) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	a.job = job
	if err := os.Remove(filepath.Join(job.Workspace, a.plan.Files[0])); err != nil {
		return synth.Usage{}, err
	}
	a.started = time.Now()
	r, err := a.sandbox.Run(ctx, job.Workspace, a.plan)
	if err != nil {
		return synth.Usage{}, err
	}
	if !r.Passed() {
		return synth.Usage{}, fmt.Errorf("agent's test call failed: %s", r.Summary())
	}
	return synth.Usage{Outcome: "success"}, nil
}

func TestVerifyBuildAllowsDeletingChildPackage(t *testing.T) {
	for _, tc := range []struct {
		name       string
		childFile  string
		acceptance string
		verify     bool
	}{
		{"last_child_file", "", "internal/plumbing", true},
		{"child_keeps_go_file", "kept.go", "internal/plumbing", false},
		{"child_keeps_test", "kept_test.go", "internal/plumbing", false},
		{"child_keeps_acceptance", "", "internal/verify/pythongate", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := v498Spy(t)
			root := t.TempDir()
			child := "internal/verify/pythongate"
			acceptance := tc.acceptance + "/fixture_accept_test.go"
			p := &Plan{Name: "Remove a child file", Summary: "Delete the named Go file.",
				Files:   []string{child + "/code.go"},
				Tests:   []Test{{Name: "TestBudgetFixture", File: acceptance, Says: "The fixture succeeds."}},
				Sources: map[string]string{acceptance: "package fixture\n\nimport \"testing\"\n\nfunc TestBudgetFixture(t *testing.T) {}\n"}}
			if err := p.Validate(); err != nil {
				t.Fatal(err)
			}
			lock := Lock{Ratified: Ratification{By: "gitdek", At: "2026-11-03T15:00:00Z", Issue: 498,
				Comment: "https://github.com/gitdek/invariant/issues/498#issuecomment-1", Proposal: p.Hash()}, Plan: *p}
			if err := lock.Write(root); err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{child, "internal/verify"} {
				s397Write(t, filepath.Join(root, dir, "code.go"), "package fixture\n", 0o644)
			}
			if tc.childFile != "" {
				s397Write(t, filepath.Join(root, child, tc.childFile), "package fixture\n", 0o644)
			}
			s397Write(t, filepath.Join(root, "go.mod"), "module github.com/gitdek/invariant\n\ngo 1.27.1\n", 0o644)
			s := Sandbox{Image: "fixture", ModCache: t.TempDir()}
			a := &v498DeletingAgent{plan: p, sandbox: s}
			b := Builder{Backend: a, Reviewer: &q458Reviewer{}, Binary: "invariant", TestRuns: 4,
				Timeout: 45 * time.Minute, Sandbox: s}
			r, err := b.Build(v498Watchdog(t), root, 498, t.TempDir())
			if err != nil || r == nil || !r.Built() {
				t.Fatalf("deleting build: %+v, %v", r, err)
			}
			run, phase := 12*time.Minute, 10*time.Minute
			wantTool := time.Duration(0)
			if tc.verify {
				run, phase = 25*time.Minute, 20*time.Minute
				wantTool = run + SandboxStartupAllowance + SandboxCleanupAllowance
			}
			if a.job.ToolTimeout != wantTool {
				t.Errorf("direct tool timer = %s, want %s", a.job.ToolTimeout, wantTool)
			}
			observed := v498Read(t, record)
			if len(observed) != 2 {
				t.Fatalf("got %d checks, want agent and final checks", len(observed))
			}
			v498Budgets(t, observed[0], a.started, run, phase)
			for i, o := range observed {
				remaining := time.Duration((o.Deadline - o.Observed) * float64(time.Second))
				if o.Tests != phase.Seconds() || remaining > run || remaining < run-5*time.Second {
					t.Errorf("check %d got phase %.0fs and %s remaining, want %s inside %s", i+1, o.Tests, remaining, phase, run)
				}
			}
		})
	}
}
