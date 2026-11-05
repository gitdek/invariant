package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Use main's real signal wiring in a separate process. The first GitHub
// request is held by a local stand-in CLI, before credentials, Docker,
// agents or network are needed. The factory tests cover the actual drain
// of active builds and handoff; this pins the operator's signal entry.
func TestWatchAcceptsSIGUSR1AsGracefulDrain463(t *testing.T) {
	for i, arg := range os.Args {
		if arg != "drain-463-cli" || i+2 >= len(os.Args) {
			continue
		}
		mode, dir := os.Args[i+1], os.Args[i+2]
		if mode == "gh" {
			if err := os.WriteFile(filepath.Join(dir, "ready"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			for {
				time.Sleep(time.Hour)
			}
		}
		os.Args = []string{"invariant", "watch", "-repo", "fixture/repository", "-work", dir}
		main()
		return
	}
	dir := t.TempDir()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := fmt.Sprintf("#!/bin/sh\nexec %s -test.run=^TestWatchAcceptsSIGUSR1AsGracefulDrain463$ -- drain-463-cli gh %s\n", quote(bin), quote(dir))
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(dir, "watch.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command(bin, "-test.run=^TestWatchAcceptsSIGUSR1AsGracefulDrain463$", "--", "drain-463-cli", "watch", dir)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ended := false
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if !ended {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("fixture watcher did not stop after cleanup killed its group")
			}
		}
	})
	wait := func(what string, ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for !ready() {
			select {
			case err := <-done:
				ended = true
				body, _ := os.ReadFile(filepath.Join(dir, "watch.log"))
				t.Fatalf("watcher exited while waiting for %s: %v\n%s", what, err, body)
			default:
			}
			if time.Now().After(deadline) {
				body, _ := os.ReadFile(filepath.Join(dir, "watch.log"))
				t.Fatalf("timed out waiting for %s\n%s", what, body)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	wait("the first GitHub request", func() bool {
		_, err := os.Stat(filepath.Join(dir, "ready"))
		return err == nil
	})
	if err := cmd.Process.Signal(syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		select {
		case err := <-done:
			ended = true
			if err != nil {
				t.Fatalf("SIGUSR1 must be a successful drain, not termination: %v", err)
			}
			return // There is no active agent, so an immediate drain is valid.
		default:
		}
		body, _ := os.ReadFile(filepath.Join(dir, "watch.log"))
		if strings.Contains(strings.ToLower(string(body)), "drain") {
			break // The request is latched while startup finishes its read.
		}
		if time.Now().After(deadline) {
			t.Fatalf("SIGUSR1 neither drained the command nor acknowledged a pending drain:\n%s", body)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		ended = true
		if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); !ok || status.Signaled() {
			t.Fatalf("SIGTERM terminated the watcher directly (%v), instead of canceling its command after drain", cmd.ProcessState)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SIGTERM no longer unwinds a watcher that was asked to drain")
	}
}
