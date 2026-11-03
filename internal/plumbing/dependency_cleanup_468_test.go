package plumbing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A canceled check removes its container before it deletes the candidate's
// staged copy, which can take a while for a big candidate, and the copy is
// gone once Run returns (#468's review).
func TestCanceledCheckRemovesItsContainerBeforeItsStagedCopy468(t *testing.T) {
	bin, logs, src, stages := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("TMPDIR", stages)
	write468(t, src, "app/main.py", "print('fixture')\n")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	// cp drains the stream and records its private stage, start runs until
	// stopped, and rm records whether that stage was still there when it ran.
	script := "#!/bin/sh\nset -eu\ncase \"$1\" in\n" +
		"create) printf '%s\\n' fixture-container;;\n" +
		"cp) [ \"$#\" -eq 3 ] && [ \"$2\" = '-' ] && [ \"$3\" = 'invariant-deps-468-cleanup:/' ] || exit 1; cat > /dev/null; for stage in " + quote(stages) + "/invariant-dependency-copy-*; do [ -d \"$stage\" ]; printf '%s' \"$stage\" > " + quote(logs) + "/stage; done;;\n" +
		"start) touch " + quote(logs) + "/started; exec sleep 30;;\n" +
		"rm) if [ -d \"$(cat " + quote(logs) + "/stage)\" ]; then echo present; else echo gone; fi > " + quote(logs) + "/rm;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	q := DependencyRequest{
		Token: "invariant-deps-468-cleanup", Image: hash468("prepared image"),
		Platform: "linux/amd64", Directory: "app", SourceDir: src, CacheDir: t.TempDir(),
		Argv:    []string{"python3", "main.py"},
		Env:     map[string]string{"HOME": "/tmp"},
		Network: false, Pull: false, Init: true, MemoryBytes: 4 << 30, PidsLimit: 1024,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		// Cancel once the check has started.
		for {
			if _, err := os.Stat(filepath.Join(logs, "started")); err == nil {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	started := time.Now()
	_, err := (&DockerDependencyRunner{}).Run(ctx, q)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a canceled check returned %v; want the cancellation", err)
	}
	if took := time.Since(started); took > 10*time.Second {
		t.Fatalf("the canceled check took %v to return", took)
	}
	rm, err := os.ReadFile(filepath.Join(logs, "rm"))
	if err != nil {
		t.Fatalf("the canceled check never removed its container: %v", err)
	}
	if got := strings.TrimSpace(string(rm)); got != "present" {
		t.Errorf("the staged copy was %s when the container was removed; want it removed after the container", got)
	}
	stage, err := os.ReadFile(filepath.Join(logs, "stage"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(string(stage)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the staged copy %s is still there after Run returned: %v", stage, err)
	}
}

// Deleting a staged copy waits no longer than its allowance, and what's left
// is deleted in the background.
func TestRemoveStageKeepsToItsAllowance468(t *testing.T) {
	stage := t.TempDir()
	for i := range 50 {
		write468(t, stage, filepath.Join("work", "f"+strings.Repeat("x", i%7), "file"), "data")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	removeStage(ctx, stage)
	if took := time.Since(started); took > time.Second {
		t.Fatalf("removeStage waited %v past its allowance", took)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(stage); errors.Is(err, os.ErrNotExist) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the staged copy was never deleted in the background")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
