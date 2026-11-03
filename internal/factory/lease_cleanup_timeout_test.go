package factory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

type leaseCleanupHub struct {
	GitHub
	dir    string
	cancel context.CancelFunc
}

func (h leaseCleanupHub) OpenIssues(context.Context) ([]github.Issue, error) {
	err := os.WriteFile(filepath.Join(h.dir, "stall"), nil, 0o600)
	h.cancel()
	if err != nil {
		return nil, err
	}
	return nil, context.Canceled
}

// A real release's read and push must both stop at the cleanup deadline.
// The Git stand-in leaves a child holding stderr; in the second case Git
// itself has already exited, so Wait must also bound pipe collection.
func TestLeaseCleanupBoundsTransport422(t *testing.T) {
	for i, arg := range os.Args {
		if arg != "lease-cleanup-422" || i+2 >= len(os.Args) {
			continue
		}
		mode, dir := os.Args[i+1], os.Args[i+2]
		name := "git"
		if mode == "child" {
			name = "child"
		}
		if err := os.WriteFile(filepath.Join(dir, name+".pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			t.Fatal(err)
		}
		if mode != "child" {
			child := exec.Command(os.Args[0], "-test.run=^TestLeaseCleanupBoundsTransport422$", "--", "lease-cleanup-422", "child", dir)
			child.Stderr = os.Stderr
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			if mode == "exited" {
				deadline := time.Now().Add(5 * time.Second)
				for {
					if _, err := os.Stat(filepath.Join(dir, "child.pid")); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("transport child did not start")
					}
					time.Sleep(10 * time.Millisecond)
				}
				os.Exit(0)
			}
		}
		for {
			time.Sleep(time.Hour)
		}
	}
	for _, command := range []string{"ls-remote", "push"} {
		for _, mode := range []string{"running", "exited"} {
			t.Run(command+"/"+mode, func(t *testing.T) {
				origin, inspect := gitRepos(t)
				now := time.Now()
				f := leaseFactory(t, origin, "stopping", &now)
				f.Work = t.TempDir()
				dir := t.TempDir()
				bin, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				realGit, err := exec.LookPath("git")
				if err != nil {
					t.Fatal(err)
				}
				quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
				script := fmt.Sprintf("#!/bin/sh\nif [ -f %s ]; then\n  for arg in \"$@\"; do\n    if [ \"$arg\" = %s ]; then\n      exec %s -test.run=^TestLeaseCleanupBoundsTransport422$ -- lease-cleanup-422 %s %s\n    fi\n  done\nfi\nexec %s \"$@\"\n",
					quote(filepath.Join(dir, "stall")), quote(command), quote(bin), quote(mode), quote(dir), quote(realGit))
				if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				f.GitHub = leaseCleanupHub{dir: dir, cancel: cancel}
				logs := make(chan string, 16)
				f.Log = func(format string, args ...any) { logs <- fmt.Sprintf(format, args...) }
				done := make(chan error, 1)
				go func() { done <- f.Watch(ctx, time.Hour) }()
				ended := false
				t.Cleanup(func() {
					cancel()
					// Disarm before killing, so an error readback cannot start
					// a second fixture after this cleanup has inspected its PID.
					_ = os.Remove(filepath.Join(dir, "stall"))
					for _, name := range []string{"git", "child"} {
						b, _ := os.ReadFile(filepath.Join(dir, name+".pid"))
						if pid, err := strconv.Atoi(string(b)); err == nil && pid > 1 {
							if name == "git" {
								_ = syscall.Kill(-pid, syscall.SIGKILL)
							}
							_ = syscall.Kill(pid, syscall.SIGKILL)
						}
					}
					if !ended {
						select {
						case <-done:
						case <-time.After(5 * time.Second):
							t.Error("watcher did not return after transport cleanup")
						}
					}
				})
				pids := map[string]int{}
				deadline := time.Now().Add(10 * time.Second)
				for len(pids) < 2 {
					for _, name := range []string{"git", "child"} {
						b, _ := os.ReadFile(filepath.Join(dir, name+".pid"))
						if pid, err := strconv.Atoi(string(b)); err == nil && pid > 1 {
							pids[name] = pid
						}
					}
					if time.Now().After(deadline) {
						t.Fatal("cleanup never reached its transport fixture")
					}
					time.Sleep(10 * time.Millisecond)
				}
				select {
				case err := <-done:
					ended = true
					if !errors.Is(err, context.Canceled) {
						t.Errorf("Watch = %v; want cancellation", err)
					}
				case <-time.After(6 * time.Second):
					t.Fatal("lease cleanup outlived its five-second deadline and pipe wait bound")
				}
				close(logs)
				failed := false
				for log := range logs {
					failed = failed || strings.HasPrefix(log, "release lease:")
				}
				if !failed {
					t.Error("stalled transport was not reported as a release failure")
				}
				deadline = time.Now().Add(2 * time.Second)
				for name, pid := range pids {
					for shutdown422Alive(pid) && time.Now().Before(deadline) {
						time.Sleep(10 * time.Millisecond)
					}
					if shutdown422Alive(pid) {
						t.Errorf("%s process %d survived cleanup", name, pid)
					}
				}
				if err := os.Remove(filepath.Join(dir, "stall")); err != nil {
					t.Fatal(err)
				}
				sha, rec, err := inspect.Lease(context.Background())
				if err != nil || sha == "" || rec.Holder != "stopping" {
					t.Fatalf("failed cleanup changed the lease: %s %+v %v", sha, rec, err)
				}
			})
		}
	}
}
