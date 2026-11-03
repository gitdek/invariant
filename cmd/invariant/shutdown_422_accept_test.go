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

// Exercise main's real signal registration in an isolated watcher process.
// Stopping during the first gh call avoids Docker, credentials and network;
// the factory acceptance tests cover a running agent and lease handoff.
func TestSIGTERMCancelsTheWatcherCommand422(t *testing.T) {
	for i, arg := range os.Args {
		if arg != "shutdown-422-cli" || i+2 >= len(os.Args) {
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
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nexec %s -test.run=^TestSIGTERMCancelsTheWatcherCommand422$ -- shutdown-422-cli gh %s\n", quote(bin), quote(dir))
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(dir, "watch.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command(bin, "-test.run=^TestSIGTERMCancelsTheWatcherCommand422$", "--", "shutdown-422-cli", "watch", dir)
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
		// The startup fixture shares the watcher's group. Always remove it,
		// including when the baseline watcher dies without canceling gh.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if !ended {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("fixture watcher was not reaped after cleanup killed its group")
			}
		}
	})
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
			break
		}
		select {
		case err := <-done:
			ended = true
			body, _ := os.ReadFile(filepath.Join(dir, "watch.log"))
			t.Fatalf("watcher never reached its fixture: %v\n%s", err, body)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("watcher did not reach its first gh request")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		ended = true
		if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); !ok || status.Signaled() {
			t.Fatalf("SIGTERM terminated the watcher directly (%v); it must cancel its command context and unwind", cmd.ProcessState)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watcher did not unwind its canceled command after SIGTERM")
	}
}
