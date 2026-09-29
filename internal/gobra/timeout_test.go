package gobra

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A Gobra run that doesn't finish within Timeout fails, saying so, instead
// of holding up the gate for good.
func TestAGobraRunThatDoesntFinishFails(t *testing.T) {
	bin := t.TempDir()
	// A docker that never finishes a run, and removes containers at once.
	stand := "#!/bin/sh\n[ \"$1\" = rm ] && exit 0\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(stand), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(Header+"\n\npackage a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	was := Timeout
	Timeout = time.Second
	t.Cleanup(func() { Timeout = was })
	start := time.Now()
	_, err := Run(context.Background(), "gobra", dir, false)
	if err == nil || !strings.Contains(err.Error(), "didn't finish within 1s") {
		t.Fatalf("got %v; want the run stopped at its limit", err)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("the run took %s after its limit", d)
	}
}
