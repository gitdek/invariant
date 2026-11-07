package synth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

var errUnwanted677 = errors.New("the issue closed")

// A run waiting for this process's turn, or for another process's hold on
// Codex's home, stops waiting once its work is no longer wanted. Its agent
// never starts, and it gives back what it held, so the next run gets its
// turn (#677).
func TestCodexStopsWaitingForWorkNoLongerWanted677(t *testing.T) {
	for _, wait := range []string{"this process's turn", "Codex's home"} {
		t.Run(wait, func(t *testing.T) {
			c, dir, ws := codexRig(t, codexRun)
			job := Job{Workspace: ws, Prompt: "build", GateServer: []string{"/bin/invariant"}}
			release := func() {}
			if wait == "Codex's home" {
				other := lockCodexHome(t, c)
				release = func() { other.Close() }
			} else {
				codexTurn <- struct{}{}
				held := true
				release = func() {
					if held {
						held = false
						<-codexTurn
					}
				}
			}
			defer release()
			var gone atomic.Bool
			var checks atomic.Int32
			ctx := WhileWanted(context.Background(), 10*time.Millisecond, func(context.Context) error {
				checks.Add(1)
				if gone.Load() {
					return errUnwanted677
				}
				return nil
			})
			done := make(chan error, 1)
			go func() {
				_, err := c.Run(ctx, job)
				done <- err
			}()
			for deadline := time.Now().Add(5 * time.Second); checks.Load() < 2; time.Sleep(5 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("the waiting run never checked that its work was wanted")
				}
			}
			gone.Store(true)
			select {
			case err := <-done:
				if !errors.Is(err, errUnwanted677) {
					t.Fatalf("a run whose work stopped being wanted = %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the run kept waiting for work no longer wanted")
			}
			if _, err := os.Stat(filepath.Join(dir, "args")); err == nil {
				t.Fatal("codex ran for work no longer wanted")
			}
			release()
			next, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := c.Run(next, job); err != nil {
				t.Fatalf("the next run didn't get its turn: %v", err)
			}
		})
	}
}

// When its turn comes, a run checks once more before its agent starts, so
// work that stopped being wanted since the last check starts no agent, and
// the turn goes back (#677).
func TestCodexChecksOnceMoreBeforeItsAgentStarts677(t *testing.T) {
	c, dir, ws := codexRig(t, codexRun)
	job := Job{Workspace: ws, Prompt: "build", GateServer: []string{"/bin/invariant"}}
	ctx := WhileWanted(context.Background(), time.Hour, func(context.Context) error { return errUnwanted677 })
	if _, err := c.Run(ctx, job); !errors.Is(err, errUnwanted677) {
		t.Fatalf("a run whose work isn't wanted = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "args")); err == nil {
		t.Fatal("codex ran for work no longer wanted")
	}
	next, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Run(next, job); err != nil {
		t.Fatalf("the next run didn't get its turn: %v", err)
	}
}

// A run whose work is wanted starts as before. Once its agent has started,
// the work's later runs, a review or a repair, don't check again (#677).
func TestCodexChecksOnlyBeforeTheFirstAgentStarts677(t *testing.T) {
	c, dir, ws := codexRig(t, codexRun)
	job := Job{Workspace: ws, Prompt: "build", GateServer: []string{"/bin/invariant"}}
	var checks atomic.Int32
	ctx := WhileWanted(context.Background(), time.Hour, func(context.Context) error {
		checks.Add(1)
		return nil
	})
	if _, err := c.Run(ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "args")); err != nil {
		t.Fatal("codex didn't run for work that's wanted")
	}
	if n := checks.Load(); n != 1 {
		t.Fatalf("an uncontended run checked %d times; want once, before its agent started", n)
	}
	if _, err := c.Run(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := StillWanted(ctx); err != nil || checks.Load() != 1 {
		t.Fatalf("a later run checked again: %d checks, %v", checks.Load(), err)
	}
}

// Claude Code waits for no turn, but its agent starts only while the work is
// wanted too (#677).
func TestClaudeCodeStartsOnlyWhileWanted677(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ntouch '"+filepath.Join(dir, "ran")+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := WhileWanted(context.Background(), time.Minute, func(context.Context) error { return errUnwanted677 })
	if _, err := (ClaudeCode{Binary: bin, BudgetUSD: 1, MaxTurns: 1}).Run(ctx, Job{Workspace: dir, Prompt: "fixture", ReadOnly: true}); !errors.Is(err, errUnwanted677) {
		t.Fatalf("a Claude Code run whose work isn't wanted = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ran")); err == nil {
		t.Fatal("Claude Code ran for work no longer wanted")
	}
}
