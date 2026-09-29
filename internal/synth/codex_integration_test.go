//go:build integration

package synth

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Codex's sandbox holds as D-0138 needs, on this machine, with the real
// codex CLI: a command under the factory's profile builds Go offline and
// writes in its workspace, and can't read a file standing in for the key in
// the home directory, read Codex's own home, write outside its workspace or
// reach the network. It runs codex sandbox, so no agent runs, and nothing is
// signed in or spent. A person runs it:
//
//	go test -tags integration -run Sandbox ./internal/synth/
func TestCodexsSandboxHolds(t *testing.T) {
	bin, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex isn't installed")
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	// The home directory is where the key and Codex's home really live, so
	// the stand-ins go there too, not in a temporary directory its commands
	// may read.
	home, err := os.MkdirTemp(cacheDir, "invariant-codex-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	key := filepath.Join(home, "factory.pem")
	if err := os.WriteFile(key, []byte("stand-in for the App key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := Codex{Binary: bin, Home: filepath.Join(home, "codex")}
	var goBin string
	if goBin, err = exec.LookPath("go"); err == nil {
		out, err := exec.Command(goBin, "env", "GOROOT", "GOMODCACHE").Output()
		if err != nil {
			t.Fatal(err)
		}
		var env [2]string
		copy(env[:], splitLines(string(out)))
		c.Reads, c.GoModCache = []string{env[0]}, env[1]
	}
	ctx := context.Background()
	if err := c.Probe(ctx); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	other := filepath.Join(t.TempDir(), "other-run.go")
	if err := os.WriteFile(other, []byte("package other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		command []string
		allowed bool
	}{
		{"write in its workspace", []string{"/usr/bin/touch", filepath.Join(ws, "ok")}, true},
		{"read the key", []string{"/bin/cat", key}, false},
		{"list the home directory", []string{"/bin/ls", home}, false},
		{"write outside its workspace", []string{"/usr/bin/touch", filepath.Join(home, "written")}, false},
		{"reach the network", []string{"/usr/bin/curl", "-sS", "-m", "8", "-o", "/dev/null", "https://example.com"}, false},
		{"read another run's workspace", []string{"/bin/cat", other}, false},
	}
	if goBin != "" {
		if err := os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module probe\n\ngo 1.21\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Its commands' temporary directory is under /tmp, since the user's
		// own is denied.
		cache, err := os.MkdirTemp("/tmp", "invariant-codex-test-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(cache) })
		cases = append(cases, struct {
			name    string
			command []string
			allowed bool
		}{"build Go offline", []string{"/usr/bin/env", "TMPDIR=" + cache, "GOCACHE=" + cache, "GOMODCACHE=" + c.GoModCache, "GOPROXY=off", "GOTOOLCHAIN=local", goBin, "build", "-C", ws, "-o", filepath.Join(ws, "probe"), "."}, true})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := c.sandboxed(ctx, ws, tc.command...)
			if (err == nil) != tc.allowed {
				t.Errorf("allowed %v, want %v (%v)", err == nil, tc.allowed, err)
			}
		})
	}
}

func splitLines(s string) []string {
	var out []string
	for _, l := range filepath.SplitList(s) {
		out = append(out, l)
	}
	if len(out) == 1 {
		out = nil
		start := 0
		for i, r := range s {
			if r == '\n' {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return out
}
