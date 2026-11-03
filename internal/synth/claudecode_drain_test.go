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

// The agent exits with stdout closed, while its child keeps stderr open.
// Cancel only after Wait has reaped the agent: CommandContext's own
// cancellation watcher has already ended by then.
func TestAgentCancellationAfterAgentExit422(t *testing.T) {
	for i, arg := range os.Args {
		if arg != "agent-drain-422" || i+2 >= len(os.Args) {
			continue
		}
		mode, dir := os.Args[i+1], os.Args[i+2]
		if err := os.WriteFile(filepath.Join(dir, mode+".pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			t.Fatal(err)
		}
		if mode == "agent" {
			child := exec.Command(os.Args[0], "-test.run=^TestAgentCancellationAfterAgentExit422$", "--", "agent-drain-422", "child", dir)
			child.Stderr = os.Stderr
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			os.Exit(0)
		}
		for {
			time.Sleep(time.Hour)
		}
	}
	for _, backend := range []string{"claude-code", "codex"} {
		t.Run(backend, func(t *testing.T) { agentCancellationAfterExit422(t, backend) })
	}
}

func agentCancellationAfterExit422(t *testing.T, backend string) {
	dir := t.TempDir()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := fmt.Sprintf("#!/bin/sh\nexec %s -test.run=^TestAgentCancellationAfterAgentExit422$ -- agent-drain-422 agent %s\n", quote(bin), quote(dir))
	path := filepath.Join(dir, "agent")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	// The re-executed test binary's TestMain reads fixtures relative to
	// this package, before it reaches the stand-in agent above.
	work, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var agent Backend = ClaudeCode{Binary: path, BudgetUSD: 1, MaxTurns: 1}
	if backend == "codex" {
		agent = Codex{Binary: path, Home: t.TempDir(), MaxTurns: 1}
	}
	done := make(chan error, 1)
	go func() {
		_, err := agent.Run(ctx, Job{Workspace: work, ReadOnly: true})
		done <- err
	}()
	ended := false
	t.Cleanup(func() {
		cancel()
		for _, name := range []string{"agent", "child"} {
			b, _ := os.ReadFile(filepath.Join(dir, name+".pid"))
			if pid, err := strconv.Atoi(string(b)); err == nil && pid > 1 {
				if name == "agent" {
					_ = syscall.Kill(-pid, syscall.SIGKILL)
				}
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
		if !ended {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("agent run did not finish after fixture cleanup")
			}
		}
	})
	var childPID int
	deadline := time.Now().Add(10 * time.Second)
	for {
		agent, _ := os.ReadFile(filepath.Join(dir, "agent.pid"))
		child, _ := os.ReadFile(filepath.Join(dir, "child.pid"))
		agentPID, _ := strconv.Atoi(string(agent))
		childPID, _ = strconv.Atoi(string(child))
		if agentPID > 1 && childPID > 1 && errors.Is(syscall.Kill(agentPID, 0), syscall.ESRCH) {
			break
		}
		select {
		case err := <-done:
			ended = true
			t.Fatalf("run ended before cancellation: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("agent was not reaped while its child retained stderr")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		ended = true
		if !errors.Is(err, context.Canceled) {
			t.Errorf("run = %v; want cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not end stderr collection after the agent exited")
	}
	deadline = time.Now().Add(2 * time.Second)
	for agentDrainAlive(childPID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if agentDrainAlive(childPID) {
		t.Errorf("child %d survived cancellation", childPID)
	}
}

func agentDrainAlive(pid int) bool {
	if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return false
	}
	// An orphan may await reaping, but a zombie cannot run or hold a pipe.
	if runtime.GOOS == "linux" {
		b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if at := strings.LastIndexByte(string(b), ')'); at >= 0 {
			fields := strings.Fields(string(b[at+1:]))
			if len(fields) > 0 && fields[0] == "Z" {
				return false
			}
		}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		b, err := exec.CommandContext(ctx, "ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		if err == nil && strings.HasPrefix(strings.TrimSpace(string(b)), "Z") {
			return false
		}
	}
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
