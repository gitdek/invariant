package toolchain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type dockerCleanupRemoval struct {
	PID         int
	Name        string
	ClientAlive bool
}

func init() {
	if os.Getenv("INVARIANT_CLEANUP_BUDGET_HELPER") == "1" {
		if err := dockerCleanupBudgetHelper(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
}

func dockerCleanupBudgetHelper() error {
	dir := os.Getenv("INVARIANT_CLEANUP_BUDGET_STATE")
	switch os.Args[1] {
	case "run":
		// A descendant keeps the output pipes open after the client is killed,
		// exercising WaitDelay as well as both removal timeouts.
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		child := exec.Command(exe, "hold-pipes")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "descendant"), []byte(strconv.Itoa(child.Process.Pid)), 0600); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "client"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			return err
		}
	case "rm":
		data, err := os.ReadFile(filepath.Join(dir, "client"))
		if err != nil {
			return err
		}
		pid, err := strconv.Atoi(string(data))
		if err != nil {
			return err
		}
		f, err := os.OpenFile(filepath.Join(dir, "removals"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return err
		}
		err = json.NewEncoder(f).Encode(dockerCleanupRemoval{
			PID: os.Getpid(), Name: os.Args[len(os.Args)-1], ClientAlive: syscall.Kill(pid, 0) == nil,
		})
		f.Close()
		if err != nil {
			return err
		}
	case "hold-pipes":
	default:
		return fmt.Errorf("unexpected Docker command %q", os.Args[1:])
	}
	time.Sleep(time.Minute)
	return nil
}

func TestDockerCancellationFitsCleanupReserveWithBlockedRemovals(t *testing.T) {
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := Docker(ctx, "run", "--rm", "local:recipe", "wait")
	cmd.Path, cmd.Err = exe, nil
	cmd.Env = append(os.Environ(), "INVARIANT_CLEANUP_BUDGET_HELPER=1", "INVARIANT_CLEANUP_BUDGET_STATE="+dir)
	t.Cleanup(func() {
		if data, err := os.ReadFile(filepath.Join(dir, "descendant")); err == nil {
			if pid, err := strconv.Atoi(string(data)); err == nil {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	done := make(chan error, 1)
	go func() {
		_, err := cmd.CombinedOutput()
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "client")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Docker client never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	started := time.Now()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("cancelled run succeeded")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("blocked removals and client shutdown exceeded the fifteen-second cleanup reserve")
	}
	if elapsed := time.Since(started); elapsed >= 15*time.Second {
		t.Errorf("cancellation took %s, exceeding the cleanup reserve", elapsed)
	}
	data, err := os.ReadFile(filepath.Join(dir, "removals"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("removals = %q, want two", data)
	}
	for i, line := range lines {
		var removal dockerCleanupRemoval
		if err := json.Unmarshal([]byte(line), &removal); err != nil {
			t.Fatal(err)
		}
		if removal.Name != cmd.name || removal.ClientAlive != (i == 0) {
			t.Errorf("removal %d = %+v; want %s bracketing client exit", i+1, removal, cmd.name)
		}
		if err := syscall.Kill(removal.PID, 0); err == nil {
			t.Errorf("removal %d still running after command completion", i+1)
		}
	}
}
