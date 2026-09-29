// Package regular reads what a coding agent leaves in its workspace, and
// takes regular files only (#153). An agent with a shell can leave a link in
// its workspace to a file on the host that the agent itself can't read but
// the factory can, such as the factory's App key (D-0044). So a symbolic
// link anywhere on a file's path, or a hard link to a file elsewhere, is
// refused and never followed, and the refusal names the file.
package regular

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

// Refused is a file the factory didn't take from an agent's workspace.
type Refused struct {
	File string // the file, by its slash path from the workspace
	Why  string // why, as a clause about the file
}

func (r *Refused) Error() string {
	return fmt.Sprintf("the factory didn't take %s from the agent's workspace, since %s; it takes only regular files", r.File, r.Why)
}

// ReadFile reads the file at rel, a path inside the workspace dir, if it's a
// regular file with no other hard link, reached through no symbolic link.
func ReadFile(dir, rel string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return read(root, rel)
}

// Walk calls fn with each file in the workspace dir that take says the
// factory takes, by its slash path, and its text. It refuses such a file as
// ReadFile does. What take leaves alone is never read, whatever it is, so a
// link the factory doesn't take, such as a tool's, stops nothing. Walk
// doesn't enter a linked directory, so it takes nothing through one.
func Walk(dir string, take func(rel string) bool, fn func(rel string, text []byte) error) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !take(p) {
			return nil
		}
		text, err := read(root, p)
		if err != nil {
			return err
		}
		return fn(p, text)
	})
}

func read(root *os.Root, rel string) ([]byte, error) {
	rel = path.Clean(filepath.ToSlash(rel))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
		return nil, fmt.Errorf("%s isn't a path inside the agent's workspace", rel)
	}
	// Every step on the file's path is what it looks like. os.Root keeps a
	// link swapped in meanwhile from reaching outside the workspace, and
	// the file opened must be the file checked.
	parts := strings.Split(rel, "/")
	var last fs.FileInfo
	for i := range parts {
		at := strings.Join(parts[:i+1], "/")
		fi, err := root.Lstat(filepath.FromSlash(at))
		if err != nil {
			return nil, err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			why := "it's a symbolic link"
			if at != rel {
				why = at + ", on its path, is a symbolic link"
			}
			return nil, &Refused{File: rel, Why: why}
		}
		last = fi
	}
	// A pipe or a device is refused before it's opened, since opening one
	// can wait forever.
	if !last.Mode().IsRegular() {
		return nil, &Refused{File: rel, Why: "it isn't a regular file"}
	}
	f, err := root.Open(filepath.FromSlash(rel))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	switch {
	case err != nil:
		return nil, err
	case !os.SameFile(fi, last):
		return nil, &Refused{File: rel, Why: "it changed while the factory read it"}
	case !fi.Mode().IsRegular():
		return nil, &Refused{File: rel, Why: "it isn't a regular file"}
	case links(fi) > 1:
		return nil, &Refused{File: rel, Why: "it's a hard link to another file"}
	}
	return io.ReadAll(f)
}

// links is how many hard links a file has, or 1 where the system doesn't
// say.
func links(fi fs.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink)
	}
	return 1
}
