package factory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/synth"
)

type shutdown422Drain struct {
	started, canceled chan int
	allow             map[int]chan struct{}
}

func (d shutdown422Drain) Formalize(ctx context.Context, req formalize.Request, _ string) (*formalize.Result, error) {
	d.started <- req.Issue
	<-ctx.Done()
	d.canceled <- req.Issue
	<-d.allow[req.Issue]
	return nil, ctx.Err()
}

// Lease release is a barrier after every running step has finished. The
// replacement watcher uses the same clock, so waiting for expiration (or
// merely setting Until to now despite the skew window) cannot pass.
func TestCanceledWatcherReleasesLeaseAfterAllSteps422(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprintf("foreign-replacement=%v", foreign), func(t *testing.T) {
			p := newParRig(t, 2)
			p.open(1)
			p.open(2)
			now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
			p.f.Holder, p.f.LeaseFor, p.f.Now = "stopping-422", 5*time.Minute, func() time.Time { return now }
			drain := shutdown422Drain{make(chan int, 2), make(chan int, 2), map[int]chan struct{}{1: make(chan struct{}), 2: make(chan struct{})}}
			p.f.Formalizer = drain
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- p.f.Watch(ctx, time.Hour) }()
			released := map[int]bool{}
			ended := false
			t.Cleanup(func() {
				cancel()
				for n, ch := range drain.allow {
					if !released[n] {
						close(ch)
					}
				}
				if !ended {
					select {
					case <-done:
					case <-time.After(10 * time.Second):
						t.Error("watcher did not finish fixture cleanup")
					}
				}
			})
			for range 2 {
				shutdown422Receive(t, drain.started, "a running step")
			}
			next := leaseFactory(t, p.origin, "successor-422", &now)
			cancel()
			for range 2 {
				shutdown422Receive(t, drain.canceled, "a step draining after cancellation")
			}
			if held, err := next.hold(context.Background()); err != nil || held {
				t.Fatalf("successor hold while canceled steps are draining = %v, %v", held, err)
			}
			close(drain.allow[1])
			released[1] = true
			if held, err := next.hold(context.Background()); err != nil || held {
				t.Fatalf("successor hold with another step still draining = %v, %v", held, err)
			}
			var replacement string
			if foreign {
				sha, _, err := next.Repo.Lease(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				replacement, err = next.Repo.PushLease(context.Background(), sha, LeaseRecord{Holder: "another-422", Until: now.Add(time.Hour)})
				if err != nil {
					t.Fatal(err)
				}
			}
			close(drain.allow[2])
			released[2] = true
			select {
			case err := <-done:
				ended = true
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("watcher did not return once both steps stopped")
			}
			if foreign {
				sha, rec, err := next.Repo.Lease(context.Background())
				if err != nil || sha != replacement || rec.Holder != "another-422" {
					t.Fatalf("shutdown replaced another holder's lease: %s %+v %v", sha, rec, err)
				}
				return
			}
			if held, err := next.hold(context.Background()); err != nil || !held {
				t.Fatalf("all steps stopped, but a new watcher cannot immediately take the lease: held=%v, err=%v", held, err)
			}
		})
	}
}

func shutdown422Receive(t *testing.T, ch <-chan int, what string) int {
	t.Helper()
	select {
	case n := <-ch:
		return n
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return 0
	}
}

type shutdown422Agent struct {
	backend synth.Backend
	work    string
}

func (a shutdown422Agent) Formalize(ctx context.Context, _ formalize.Request, _ string) (*formalize.Result, error) {
	_, err := a.backend.Run(ctx, synth.Job{Workspace: a.work, Prompt: "fixture", ReadOnly: true})
	return nil, err
}

// A real Watch and real agent backend run in a subprocess, with local Git
// and a stand-in CLI. SIGTERM reaches only the watcher, never its process
// group. The CLI's child and grandchild both inherit its output pipes, so
// killing only the CLI also leaves the step unable to finish reading.
func TestSIGTERMStopsAgentDescendantsAndReleasesLease422(t *testing.T) {
	if shutdown422Child(t) {
		return
	}
	for _, agent := range []string{"codex", "claude-code"} {
		t.Run(agent, func(t *testing.T) {
			origin, inspect := gitRepos(t)
			dir := t.TempDir()
			bin, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
			script := fmt.Sprintf("#!/bin/sh\nexec %s -test.run=^TestSIGTERMStopsAgentDescendantsAndReleasesLease422$ -- shutdown-422-fixture agent %s\n", quote(bin), quote(dir))
			if err := os.WriteFile(filepath.Join(dir, "agent"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			log, err := os.Create(filepath.Join(dir, "watch.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			cmd := exec.Command(bin, "-test.run=^TestSIGTERMStopsAgentDescendantsAndReleasesLease422$", "--", "shutdown-422-fixture", "watch", dir, agent, origin)
			cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			cmd.Stdout, cmd.Stderr = log, log
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			ended := false
			pids := map[string]int{}
			t.Cleanup(func() {
				// Codex (and fixed ClaudeCode) owns a separate group. The
				// baseline ClaudeCode inherits the watcher's group. Clean both,
				// plus recorded descendants, on every exit path.
				for _, name := range []string{"agent", "child", "grandchild"} {
					if b, err := os.ReadFile(filepath.Join(dir, name+".pid")); err == nil {
						if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 1 {
							if name == "agent" {
								_ = syscall.Kill(-pid, syscall.SIGKILL)
							}
							_ = syscall.Kill(pid, syscall.SIGKILL)
						}
					}
				}
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				if !ended {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("fixture watcher was not reaped after cleanup killed its group")
					}
				}
			})
			deadline := time.Now().Add(20 * time.Second)
			for len(pids) < 3 {
				for _, name := range []string{"agent", "child", "grandchild"} {
					if b, err := os.ReadFile(filepath.Join(dir, name+".pid")); err == nil {
						if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 1 {
							pids[name] = pid
						}
					}
				}
				select {
				case err := <-done:
					ended = true
					body, _ := os.ReadFile(filepath.Join(dir, "watch.log"))
					t.Fatalf("fixture watcher exited before its agent was ready: %v\n%s", err, body)
				default:
				}
				if time.Now().After(deadline) {
					body, _ := os.ReadFile(filepath.Join(dir, "watch.log"))
					t.Fatalf("fixture agent tree did not start: %v\n%s", pids, body)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if sha, rec, err := inspect.Lease(context.Background()); err != nil || sha == "" || rec.Holder != "watcher-422" {
				t.Fatalf("running watcher did not hold a lease: %s %+v %v", sha, rec, err)
			}
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				ended = true
				if err != nil {
					body, _ := os.ReadFile(filepath.Join(dir, "watch.log"))
					t.Errorf("watcher failed to unwind after SIGTERM: %v\n%s", err, body)
				}
			case <-time.After(5 * time.Second):
				t.Error("watcher did not finish its canceled agent run after SIGTERM")
			}
			deadline = time.Now().Add(2 * time.Second)
			for name, pid := range pids {
				for shutdown422Alive(pid) && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				if shutdown422Alive(pid) {
					t.Errorf("%s process %d survived SIGTERM to its watcher", name, pid)
				}
			}
			now := time.Now()
			next := leaseFactory(t, origin, "next-422", &now)
			if held, err := next.hold(context.Background()); err != nil || !held {
				t.Errorf("SIGTERM left the watcher's lease unavailable to its successor: held=%v, err=%v", held, err)
			}
		})
	}
}

func shutdown422Alive(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return false
	}
	// A killed descendant may briefly remain an orphan zombie before init
	// reaps it. It cannot execute or spend anything and is not alive.
	if runtime.GOOS == "linux" {
		if out, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
			// The command name can contain spaces and parentheses; the state
			// is the first field after its final closing parenthesis.
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
	// Failure to inspect the process is never evidence that it stopped.
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func shutdown422Child(t *testing.T) bool {
	var args []string
	for i, arg := range os.Args {
		if arg == "shutdown-422-fixture" {
			args = os.Args[i+1:]
			break
		}
	}
	if len(args) == 0 {
		return false
	}
	mode, dir := args[0], args[1]
	if mode != "watch" {
		if err := os.WriteFile(filepath.Join(dir, mode+".pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			t.Fatal(err)
		}
		if mode != "grandchild" {
			next := "child"
			if mode == "child" {
				next = "grandchild"
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestSIGTERMStopsAgentDescendantsAndReleasesLease422$", "--", "shutdown-422-fixture", next, dir)
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()
		}
		for {
			time.Sleep(time.Hour)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	clone := Clone{Dir: filepath.Join(dir, "clone"), Remote: args[3], Name: "Fixture", Email: "fixture@example.invalid"}
	if err := clone.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	gh := newFakeGitHub(t, args[3])
	gh.open(1, "gitdek", "Draft a fixture", "A fixture.\n\n/invariant solve")
	var backend synth.Backend = synth.ClaudeCode{Binary: filepath.Join(dir, "agent"), BudgetUSD: 1, MaxTurns: 1}
	if args[2] == "codex" {
		if err := os.Mkdir(filepath.Join(dir, "agent-home"), 0o700); err != nil {
			t.Fatal(err)
		}
		backend = synth.Codex{Binary: filepath.Join(dir, "agent"), Home: filepath.Join(dir, "agent-home"), MaxTurns: 1}
	}
	f := &Factory{Repository: "o/r", GitHub: gh, Repo: clone, Base: "main", Projects: "examples", Work: filepath.Join(dir, "work"),
		Holder: "watcher-422", LeaseFor: 5 * time.Minute, Formalizer: shutdown422Agent{backend, dir}, Log: t.Logf}
	if err := f.Watch(ctx, time.Hour); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	return true
}
