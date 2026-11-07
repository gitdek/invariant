package plumbing

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRepositoryGate740IndependentTreeCleanup(t *testing.T) {
	// Host removal follows container shutdown. Exhausting that shutdown's
	// context must not prevent successful removal under the filesystem reserve;
	// shutdown errors and run cancellation are still recorded by the caller.
	containerCleanup, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	stage := filepath.Join(t.TempDir(), "stage")
	if err := os.MkdirAll(filepath.Join(stage, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "nested/file"), []byte("staged input"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "keep")
	if err := os.WriteFile(outside, []byte("unrelated input"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(stage, "link")); err != nil {
		t.Fatal(err)
	}
	if err := removeRepositoryStage(containerCleanup, stage); err != nil {
		t.Fatalf("host tree removal inherited the spent container reserve: %v", err)
	}
	if _, err := os.Lstat(stage); !os.IsNotExist(err) {
		t.Fatalf("returned before tree removal completed: %v", err)
	}
	if b, err := os.ReadFile(outside); err != nil || string(b) != "unrelated input" {
		t.Fatalf("cleanup followed a staged link: %q %v", b, err)
	}
	if err := removeRepositoryStage(context.Background(), "bad\x00path"); err == nil {
		t.Fatal("filesystem cleanup error was hidden")
	}
}
