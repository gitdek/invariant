package synth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The leader exits and closes stdout, but its child still holds stderr.
// Cancellation must reach that child after exec.Wait has reaped the leader
// and stopped its own context watcher (#422).
func TestAgentCancellationAfterLeaderExit422(t *testing.T) {
	for _, agent := range []string{"codex", "claude-code"} {
		t.Run(agent, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "agent")
			script := `#!/bin/sh
printf '%s\n' "$$" > leader.pid
sleep 300 > /dev/null &
printf '%s\n' "$!" > child.pid
exit 0
`
			if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			var backend Backend = ClaudeCode{Binary: bin, BudgetUSD: 1, MaxTurns: 1}
			if agent == "codex" {
				backend = Codex{Binary: bin, Home: t.TempDir(), MaxTurns: 1}
			}
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() {
				_, err := backend.Run(ctx, Job{Workspace: dir, Prompt: "fixture", ReadOnly: true})
				done <- err
			}()
			readPID := func(name string) int {
				b, _ := os.ReadFile(filepath.Join(dir, name+".pid"))
				pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
				return pid
			}
			ended := false
			t.Cleanup(func() {
				cancel()
				// Remove the fixture even when a regression leaves Run stuck
				// copying stderr, and wait before removing its workspace.
				if pid := readPID("leader"); pid > 1 {
					_ = syscall.Kill(-pid, syscall.SIGKILL)
				}
				if pid := readPID("child"); pid > 1 {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
				if !ended {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("agent run did not finish after fixture cleanup")
					}
				}
			})
			deadline := time.Now().Add(10 * time.Second)
			var child int
			for {
				leader := readPID("leader")
				child = readPID("child")
				// ESRCH, rather than a zombie, means Wait has reaped it.
				if leader > 1 && child > 1 && errors.Is(syscall.Kill(leader, 0), syscall.ESRCH) {
					break
				}
				select {
				case err := <-done:
					ended = true
					t.Fatalf("agent run ended before the fixture was ready: %v", err)
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("agent leader was not reaped while its child held stderr")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !agentDrainAlive422(child) {
				t.Fatal("fixture child exited before cancellation")
			}
			// Let Wait finish exchanging its context result. It must still
			// be draining the inherited stderr, with stdout already closed.
			select {
			case err := <-done:
				ended = true
				t.Fatalf("agent run returned with a child still holding stderr: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			cancel()
			select {
			case err := <-done:
				ended = true
				if !errors.Is(err, context.Canceled) {
					t.Errorf("agent run returned %v; want cancellation", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancellation did not finish draining the exited leader's stderr")
			}
			deadline = time.Now().Add(2 * time.Second)
			for agentDrainAlive422(child) && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if agentDrainAlive422(child) {
				t.Fatal("child survived cancellation after its leader exited")
			}
		})
	}
}

func agentDrainAlive422(pid int) bool {
	if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return false
	}
	// An orphan zombie may wait for init to reap it, but cannot run.
	if runtime.GOOS == "linux" {
		if out, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
			if at := strings.LastIndexByte(string(out), ')'); at >= 0 {
				fields := strings.Fields(string(out[at+1:]))
				if len(fields) > 0 && fields[0] == "Z" {
					return false
				}
			}
		}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		if err == nil && strings.HasPrefix(strings.TrimSpace(string(out)), "Z") {
			return false
		}
	}
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
