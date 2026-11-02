package factory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Cleanup must finish, not just cancel its context, when a transport helper
// retains stderr. Exercise both a git waiting for its helper and a git that
// exits first, disabling exec's context watcher before the pipe drains.
func TestShutdownBoundsCleanupWithLingeringTransport(t *testing.T) {
	for _, exitFirst := range []bool{false, true} {
		name := "waiting-git"
		if exitFirst {
			name = "exited-git"
		}
		t.Run(name, func(t *testing.T) {
			origin, _ := gitRepos(t)
			now := time.Now()
			f := leaseFactory(t, origin, "transport-cleanup", &now)
			clone := f.Repo.(Clone)
			ready := make(chan struct{})
			f.Repo = cleanupTransportRepo{Repo: f.Repo, ready: ready}
			dir := t.TempDir()
			quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
			script := "#!/bin/sh\n" +
				"printf '%s\\n' \"$$\" > " + quote(filepath.Join(dir, "helper.pid")) + "\n" +
				"sleep 600 >/dev/null &\n" +
				"printf '%s\\n' \"$!\" > " + quote(filepath.Join(dir, "child.pid")) + "\n"
			if exitFirst {
				script += "exit 0\n"
			} else {
				script += "wait\n"
			}
			if err := os.WriteFile(filepath.Join(dir, "git-remote-linger422"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- f.Watch(ctx, time.Hour) }()
			ended := false
			t.Cleanup(func() {
				cancel()
				for _, name := range []string{"helper", "child"} {
					if pid, err := cleanupTransportPID(dir, name); err == nil {
						_ = syscall.Kill(pid, syscall.SIGKILL)
					}
				}
				if !ended {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("watcher did not finish after fixture cleanup")
					}
				}
			})
			select {
			case <-ready:
			case err := <-done:
				ended = true
				t.Fatalf("watcher stopped before acquiring its lease: %v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("watcher did not acquire its lease")
			}
			// Only shutdown sees the broken transport; taking the lease used
			// the real local origin. No network is involved in either path.
			git(t, clone.Dir, "config", "remote.origin.url", "linger422::fixture")
			cancel()
			select {
			case err := <-done:
				ended = true
				if !errors.Is(err, context.Canceled) {
					t.Errorf("Watch = %v; want cancellation", err)
				}
			case <-time.After(7 * time.Second):
				t.Fatal("cleanup remained blocked on a transport pipe beyond its five-second deadline")
			}
			deadline := time.Now().Add(2 * time.Second)
			for _, name := range []string{"helper", "child"} {
				pid, err := cleanupTransportPID(dir, name)
				if err != nil {
					t.Fatalf("cleanup never started its %s fixture: %v", name, err)
				}
				for shutdown422Alive(pid) && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				if shutdown422Alive(pid) {
					t.Errorf("transport %s %d survived cleanup", name, pid)
				}
			}
		})
	}
}

type cleanupTransportRepo struct {
	Repo
	ready chan struct{}
}

func (r cleanupTransportRepo) ClearWorktrees(ctx context.Context) error {
	close(r.ready)
	<-ctx.Done()
	return ctx.Err()
}

func cleanupTransportPID(dir, name string) (int, error) {
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
