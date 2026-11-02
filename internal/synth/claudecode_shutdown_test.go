package synth

import (
	"context"
	"errors"
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

// The CLI can exit before a tool that keeps only stderr open. stdout then
// reaches EOF and Wait reaps the CLI, but must remain cancellable while it
// drains the descendant's stderr. exec's own context watcher has stopped
// by then, so overriding cmd.Cancel alone does not cover this case.
func TestClaudeCodeCancelAfterLeaderExitDrainsDescendantStderr(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "agent")
	script := `#!/bin/sh
printf '%s\n' "$$" > leader.pid
sleep 600 >/dev/null &
printf '%s\n' "$!" > child.pid
exit 0
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var runErr error
	go func() {
		_, runErr = (ClaudeCode{Binary: bin, BudgetUSD: 1, MaxTurns: 1}).Run(ctx, Job{
			Workspace: dir, Prompt: "fixture", ReadOnly: true,
		})
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		// A failing implementation can leave Wait blocked on stderr. Kill
		// the fixture group and child before awaiting the run in cleanup.
		for _, name := range []string{"leader", "child"} {
			if pid, err := claudeShutdownPID(dir, name); err == nil {
				if name == "leader" {
					_ = syscall.Kill(-pid, syscall.SIGKILL)
				}
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("agent run did not finish after fixture cleanup")
		}
	})
	var leader, child int
	deadline := time.Now().Add(10 * time.Second)
	for {
		leader, _ = claudeShutdownPID(dir, "leader")
		child, _ = claudeShutdownPID(dir, "child")
		// ESRCH, rather than just the leader's pid file, establishes that
		// Wait has reaped it before we cancel the context.
		if leader > 1 && child > 1 && errors.Is(syscall.Kill(leader, 0), syscall.ESRCH) {
			break
		}
		select {
		case <-done:
			t.Fatalf("agent returned before its descendant's stderr drained: %v", runErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent leader was not reaped before cancellation: leader=%d, child=%d", leader, child)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !claudeShutdownAlive(child) {
		t.Fatal("stderr-holding descendant stopped before cancellation")
	}
	cancel()
	select {
	case <-done:
		if !errors.Is(runErr, context.Canceled) {
			t.Errorf("canceled agent run returned %v; want context.Canceled", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not drain stderr after the agent leader exited")
	}
	deadline = time.Now().Add(2 * time.Second)
	for claudeShutdownAlive(child) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if claudeShutdownAlive(child) {
		t.Errorf("stderr-holding descendant %d survived cancellation", child)
	}
}

func claudeShutdownPID(dir, name string) (int, error) {
	b, err := os.ReadFile(filepath.Join(dir, name+".pid"))
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err == nil && pid <= 1 {
		err = errors.New("invalid fixture PID")
	}
	return pid, err
}

func claudeShutdownAlive(pid int) bool {
	if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return false
	}
	// An orphan zombie has already stopped; init may not have reaped it.
	if runtime.GOOS == "linux" {
		if out, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
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
