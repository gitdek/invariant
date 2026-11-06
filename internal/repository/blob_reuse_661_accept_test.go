package repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Count actual Git object-read commands, without timing thresholds or a new
// production injection hook. This test is serial because it changes PATH.
func blobCounter661(t *testing.T) func() int {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "reads")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\nfor arg do\n  if [ \"$arg\" = cat-file ]; then\n    printf 'read\\n' >> " + quote(log) + " || exit 90\n    break\n  fi\ndone\nexec " + quote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		t.Helper()
		b, err := os.ReadFile(log)
		if os.IsNotExist(err) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(b), "\n")
	}
}

func TestSnapshotReusesBlobs661(t *testing.T) {
	root := repo461(t, contract461(part461("app", ".", "go")))
	want := []byte("captured\x00bytes\r\nwithout final newline")
	write461(t, root, "payload.bin", string(want))
	write461(t, root, "cold.bin", "concurrent cold bytes\n")
	write461(t, root, "retry.bin", "recoverable object bytes\n")
	write461(t, root, "cancel.bin", "live reader after cancellation\n")
	base := commit461(t, root)
	count := blobCounter661(t)
	s := load461(t, root, base)
	first, err := s.ReadFile("payload.bin")
	if err != nil || !bytes.Equal(first, want) {
		t.Fatalf("initial read: %q, %v", first, err)
	}
	first[0] ^= 1
	before := count()
	for i := 0; i < 3; i++ {
		b, err := s.ReadFileContext(t.Context(), "payload.bin")
		if err != nil || !bytes.Equal(b, want) {
			t.Fatalf("cached read shares returned bytes: %q, %v", b, err)
		}
		b[0] ^= 1
	}
	if got := count(); got != before {
		t.Fatalf("repeated captured reads ran Git again: %d -> %d", before, got)
	}
	// Warm and cold concurrency both exercise synchronization; a cold miss may
	// have more than one in-flight reader, but successful reuse must be stable.
	for file, expected := range map[string][]byte{"payload.bin": want, "cold.bin": []byte("concurrent cold bytes\n")} {
		beforeConcurrent := count()
		start := make(chan struct{})
		results := make(chan error, 24)
		var wg sync.WaitGroup
		for i := 0; i < cap(results); i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				b, err := s.ReadFileContext(t.Context(), file)
				if err != nil || !bytes.Equal(b, expected) {
					results <- fmt.Errorf("concurrent %s: %q, %v", file, b, err)
					return
				}
				b[0] ^= 1
				results <- nil
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Fatal(err)
			}
		}
		if file == "payload.bin" && count() != beforeConcurrent {
			t.Fatal("concurrent cache hits reread captured blobs")
		}
		after := count()
		b, err := s.ReadFile(file)
		if err != nil || !bytes.Equal(b, expected) || count() != after {
			t.Fatalf("read after concurrent success did not reuse independent bytes: %q, %v", b, err)
		}
	}
	// Empty successful blobs are hits too, not indistinguishable from misses.
	if _, err := s.ReadFile("go.sum"); err != nil {
		t.Fatal(err)
	}
	before = count()
	if b, err := s.ReadFile("go.sum"); err != nil || len(b) != 0 || count() != before {
		t.Fatalf("empty blob was not reused: %q, %v", b, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	expired, release := context.WithDeadline(t.Context(), time.Unix(1, 0))
	defer release()
	for _, ctx := range []context.Context{canceled, expired} {
		for _, file := range []string{"payload.bin", "cancel.bin"} {
			b, err := s.ReadFileContext(ctx, file)
			if b != nil || !errors.Is(err, ctx.Err()) {
				t.Fatalf("canceled cached/uncached read %s returned %q, %v", file, b, err)
			}
		}
	}
	if count() != before {
		t.Fatal("already canceled callers started object reads")
	}
	read461(t, s, "cancel.bin", "live reader after cancellation\n")
	read461(t, s, "payload.bin", string(want))
	// A failed read must be retried, rather than poisoning the snapshot cache.
	oid := git461(t, root, "rev-parse", base+":retry.bin")
	object := filepath.Join(root, ".git", "objects", oid[:2], oid[2:])
	objectBytes, err := os.ReadFile(object)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(object); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadFile("retry.bin"); err == nil {
		t.Fatal("unreadable object returned a successful cached result")
	}
	if err := os.WriteFile(object, objectBytes, 0o444); err != nil {
		t.Fatal(err)
	}
	read461(t, s, "retry.bin", "recoverable object bytes\n")
	before = count()
	read461(t, s, "retry.bin", "recoverable object bytes\n")
	if count() != before {
		t.Fatal("successful retry was not cached")
	}
	write461(t, root, "payload.bin", "new committed bytes\n")
	next := load461(t, root, commit461(t, root))
	read461(t, next, "payload.bin", "new committed bytes\n")
	read461(t, s, "payload.bin", string(want))
	write461(t, root, "payload.bin", "uncommitted replacement\n")
	read461(t, next, "payload.bin", "new committed bytes\n")
	if s.Commit() != base || next.Commit() == base {
		t.Fatal("cache changed or reused commit provenance")
	}
}
