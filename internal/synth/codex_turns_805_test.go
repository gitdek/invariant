package synth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tickets805 lists the live tickets in c's line, ignoring ones still being made.
func tickets805(t *testing.T, c Codex) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(c.Home, "invariant-turns"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".new") {
			names = append(names, e.Name())
		}
	}
	return names
}

func waitForTickets805(t *testing.T, c Codex, n int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); len(tickets805(t, c)) != n; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("want %d waiting tickets, have %v", n, tickets805(t, c))
		}
	}
}

func lockHome805(c Codex, ctx context.Context) <-chan *os.File {
	got := make(chan *os.File, 1)
	go func() {
		f, err := c.lockHome(ctx, nil)
		if err != nil {
			f = nil
		}
		got <- f
	}()
	return got
}

// Each lockHome call opens the lock file afresh, as another process would, so
// the calls here stand for processes sharing Codex's home. A process whose run
// frees the lock and at once wants it again must wait behind one that was
// already waiting, not take the lock back in the instant it's free.
func TestCodexTurnsGoInOrder805(t *testing.T) {
	c := Codex{Home: t.TempDir()}
	held, err := c.lockHome(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	waiting := lockHome805(c, t.Context())
	// Give the other process time to start waiting, however it waits.
	time.Sleep(300 * time.Millisecond)
	held.Close()
	again := lockHome805(c, t.Context())
	var first *os.File
	select {
	case first = <-waiting:
	case f := <-again:
		f.Close()
		t.Fatal("the process that just had the lock took it back ahead of one already waiting")
	case <-time.After(10 * time.Second):
		t.Fatal("no one took the freed lock")
	}
	if first == nil {
		t.Fatal("the waiting process failed to take its turn")
	}
	select {
	case <-again:
		t.Fatal("two processes held the lock at once")
	case <-time.After(1500 * time.Millisecond):
	}
	first.Close()
	select {
	case f := <-again:
		if f == nil {
			t.Fatal("the later process failed to take its turn")
		}
		f.Close()
	case <-time.After(10 * time.Second):
		t.Fatal("the later process never got its turn")
	}
	if left := tickets805(t, c); len(left) != 0 {
		t.Fatalf("taking a turn left tickets behind: %v", left)
	}
}

// A ticket nobody holds belongs to a process that died waiting. It mustn't
// hold up anyone, and the next waiter clears it.
func TestCodexTurnsSkipDeadTickets805(t *testing.T) {
	c := Codex{Home: t.TempDir()}
	dir := filepath.Join(c.Home, "invariant-turns")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	dead := filepath.Join(dir, "00000000000000000001-1-0000000000000000")
	if err := os.WriteFile(dead, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case f := <-lockHome805(c, t.Context()):
		if f == nil {
			t.Fatal("the waiter failed to take the lock")
		}
		f.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("a dead process's ticket held up the lock")
	}
	if _, err := os.Stat(dead); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the dead ticket wasn't cleared: %v", err)
	}
}

// A run called off while it waits leaves the line, so it holds no one up.
func TestCodexTurnsCalledOffLeaveTheLine805(t *testing.T) {
	c := Codex{Home: t.TempDir()}
	held, err := c.lockHome(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	waiting := lockHome805(c, ctx)
	waitForTickets805(t, c, 1)
	cancel()
	select {
	case f := <-waiting:
		if f != nil {
			t.Fatal("a run called off still took the lock")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a run called off kept waiting")
	}
	if left := tickets805(t, c); len(left) != 0 {
		t.Fatalf("a run called off left its ticket: %v", left)
	}
	held.Close()
	select {
	case f := <-lockHome805(c, t.Context()):
		if f == nil {
			t.Fatal("the next waiter failed to take the lock")
		}
		f.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("the next waiter didn't take the free lock at once")
	}
}
