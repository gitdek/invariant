package regular

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// key stands in for a file on the host that the agent can't read but the
// factory can, such as its App key.
const key = "-----BEGIN PRIVATE KEY----- stand-in"

// rig is a workspace holding main.go, beside a key outside it.
func rig(t *testing.T) (ws, keyFile string) {
	t.Helper()
	host := t.TempDir()
	keyFile = filepath.Join(host, "factory.pem")
	ws = filepath.Join(host, "ws")
	if err := os.WriteFile(keyFile, []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws, keyFile
}

func refused(t *testing.T, err error, file, why string) {
	t.Helper()
	var r *Refused
	if !errors.As(err, &r) || r.File != file || !strings.Contains(r.Error(), why) {
		t.Fatalf("got %v; want %s refused, since %s", err, file, why)
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("the refusal carries the key's text: %v", err)
	}
}

// A regular file reads, and a link to the key, however it's made, is
// refused by name, and its text never comes back.
func TestOnlyRegularFilesAreTaken(t *testing.T) {
	ws, keyFile := rig(t)
	if b, err := ReadFile(ws, "src/main.go"); err != nil || string(b) != "package main\n" {
		t.Fatalf("a regular file: %q, %v", b, err)
	}
	for _, c := range []struct {
		name, file, why string
		link            func() error
	}{
		{"a symbolic link", "src/key.go", "it's a symbolic link", func() error {
			return os.Symlink(keyFile, filepath.Join(ws, "src", "key.go"))
		}},
		{"a relative symbolic link", "src/up.go", "it's a symbolic link", func() error {
			return os.Symlink("../../factory.pem", filepath.Join(ws, "src", "up.go"))
		}},
		{"a symbolic link inside the workspace", "src/again.go", "it's a symbolic link", func() error {
			return os.Symlink("main.go", filepath.Join(ws, "src", "again.go"))
		}},
		{"a linked directory on the path", "host/factory.pem", "host, on its path, is a symbolic link", func() error {
			return os.Symlink(filepath.Dir(keyFile), filepath.Join(ws, "host"))
		}},
		{"a hard link", "src/hard.go", "it's a hard link to another file", func() error {
			return os.Link(keyFile, filepath.Join(ws, "src", "hard.go"))
		}},
		{"a pipe", "src/pipe.go", "it isn't a regular file", func() error {
			return syscall.Mkfifo(filepath.Join(ws, "src", "pipe.go"), 0o644)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := c.link(); err != nil {
				t.Fatal(err)
			}
			b, err := ReadFile(ws, c.file)
			if strings.Contains(string(b), key) {
				t.Fatal("the key's text came back")
			}
			refused(t, err, c.file, c.why)
		})
	}
	for _, p := range []string{"../factory.pem", "/etc/hosts", "."} {
		if b, err := ReadFile(ws, p); err == nil || strings.Contains(string(b), key) {
			t.Errorf("%s read outside the workspace: %q, %v", p, b, err)
		}
	}
}

// Walk takes what take names, refusing a link there, and leaves anything
// else alone, links included, such as a Python environment's. It never
// enters a linked directory.
func TestAWalkTakesOnlyRegularFiles(t *testing.T) {
	ws, keyFile := rig(t)
	if err := os.MkdirAll(filepath.Join(ws, ".venv", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, l := range [][2]string{{"/usr/bin/true", ".venv/bin/python"}, {filepath.Dir(keyFile), "src/host"}} {
		if err := os.Symlink(l[0], filepath.Join(ws, filepath.FromSlash(l[1]))); err != nil {
			t.Fatal(err)
		}
	}
	src := func(rel string) bool { return strings.HasPrefix(rel, "src/") }
	var took []string
	err := Walk(ws, src, func(rel string, text []byte) error {
		if strings.Contains(string(text), key) {
			t.Fatalf("%s gave the key's text", rel)
		}
		took = append(took, rel)
		return nil
	})
	refused(t, err, "src/host", "it's a symbolic link")

	if err := os.Remove(filepath.Join(ws, "src", "host")); err != nil {
		t.Fatal(err)
	}
	took = nil
	if err := Walk(ws, src, func(rel string, _ []byte) error { took = append(took, rel); return nil }); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(took, []string{"src/main.go"}) {
		t.Errorf("took %v; want only src/main.go, and the environment's link left alone", took)
	}
	if err := os.Link(keyFile, filepath.Join(ws, "src", "hard.go")); err != nil {
		t.Fatal(err)
	}
	refused(t, Walk(ws, src, func(string, []byte) error { return nil }), "src/hard.go", "it's a hard link to another file")
}
