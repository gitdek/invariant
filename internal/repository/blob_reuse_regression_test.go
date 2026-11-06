package repository

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSnapshotBlobIdentityAndLifetime661(t *testing.T) {
	root := repo461(t, contract461(part461("app", ".", "go")))
	const payload = "shared captured blob\x00\r\n"
	write461(t, root, "first.bin", payload)
	write461(t, root, "second.bin", payload)
	base := commit461(t, root)
	count := blobCounter661(t)
	s := load461(t, root, base)
	before := count()
	if _, err := s.ReadFile(Path); err != nil || count() != before {
		t.Fatalf("contract read at load was not retained: %v", err)
	}
	read461(t, s, "first.bin", payload)
	before = count()
	read461(t, s, "second.bin", payload)
	if count() != before {
		t.Fatal("another path to the captured blob reread Git")
	}
	// Even a second snapshot of the same commit owns its reads independently.
	other := load461(t, root, base)
	oid := git461(t, root, "rev-parse", base+":first.bin")
	if err := os.Remove(filepath.Join(root, ".git", "objects", oid[:2], oid[2:])); err != nil {
		t.Fatal(err)
	}
	before = count()
	read461(t, s, "second.bin", payload)
	if count() != before {
		t.Fatal("cached success required the object again")
	}
	if _, err := other.ReadFile("first.bin"); err == nil || errors.Is(err, ErrFileMissing) || count() != before+1 {
		t.Fatalf("new snapshot borrowed another snapshot's cached success: %v", err)
	}
}

func TestSnapshotBlobCancellationDuringRead661(t *testing.T) {
	root := repo461(t, contract461(part461("app", ".", "go")))
	write461(t, root, "payload.bin", "successful retry\n")
	base := commit461(t, root)
	s := load461(t, root, base)
	oid := git461(t, root, "rev-parse", base+":payload.bin")
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	block, started := filepath.Join(bin, "block"), filepath.Join(bin, "started")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	// Signal entry before blocking in the object read. exec leaves no child
	// holding the command's output pipes when CommandContext kills it.
	script := "#!/bin/sh\nfor arg do\n  if [ \"$arg\" = " + quote(oid) + " ] && [ -f " + quote(block) + " ]; then\n    printf 'started\\n' > " + quote(started) + " || exit 90\n    exec sleep 60\n  fi\ndone\nexec " + quote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(block, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := s.ReadFileContext(ctx, "payload.bin")
		result <- err
	}()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(started); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			t.Fatalf("read ended before entering Git: %v", err)
		case <-timer.C:
			t.Fatal("object read did not start")
		case <-ticker.C:
		}
	}
	// A live caller must not wait for, or inherit cancellation from, the
	// blocked caller. Its success must survive that caller's later failure.
	if err := os.Remove(block); err != nil {
		t.Fatal(err)
	}
	live, release := context.WithTimeout(t.Context(), 5*time.Second)
	defer release()
	data, err := s.ReadFileContext(live, "payload.bin")
	if err != nil || string(data) != "successful retry\n" {
		t.Fatalf("independent live reader: %q, %v", data, err)
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) || errors.Is(err, ErrFileMissing) {
			t.Fatalf("interrupted object read: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled object read did not return")
	}
	count := blobCounter661(t)
	read461(t, s, "payload.bin", "successful retry\n")
	if count() != 0 {
		t.Fatal("canceled reader discarded another caller's cached success")
	}
}
