package verify_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A fake Docker run that cancellation kills inside the event lock must not
// strand it. On a slow runner the gate's removal after the client exited then
// waited out its 10-second timeout and never logged the container's end, so
// TestTLC497MemoryFailureKeepsOtherResults saw a container still active (#641).
// Killing a holder on purpose stands in for that runner, whatever its speed.
func TestTLC497KilledHolderReleasesLock641(t *testing.T) {
	root := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	holder := exec.Command(exe, "-test.run=^TestTLC497LockHolder641$")
	holder.Env = append(os.Environ(), "TLC497_HOLD_LOCK="+root)
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "held")); err == nil {
			break
		}
		if time.Now().After(until) {
			holder.Process.Kill()
			holder.Wait()
			t.Fatal("the holder never took the lock")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	holder.Wait()
	logged := make(chan struct{})
	go func() {
		defer close(logged)
		tlc497Event(root, "end after a killed holder")
	}()
	select {
	case <-logged:
	case <-time.After(5 * time.Second):
		t.Fatal("a killed holder stranded the fake Docker event lock")
	}
}

// TestTLC497LockHolder641 is TestTLC497KilledHolderReleasesLock641's holder
// process: it takes the event lock and keeps it until it is killed.
func TestTLC497LockHolder641(t *testing.T) {
	root := os.Getenv("TLC497_HOLD_LOCK")
	if root == "" {
		t.Skip("the lock holder runs only as TestTLC497KilledHolderReleasesLock641's process")
	}
	tlc497Locked(root, func() {
		if err := os.WriteFile(filepath.Join(root, "held"), nil, 0600); err != nil {
			panic(err)
		}
		time.Sleep(time.Minute)
	})
}
