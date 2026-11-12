package verify

import (
	"fmt"
	"io"
	"os"
	"syscall"
)

// What a sandboxed run leaves in the directory it shares with the gate
// belongs to the code under test, which can put anything there: a link to a
// file on the host, or a pipe. The gate takes a file back only through the
// directory itself, so nothing outside it is ever read (#816).

// openSandboxOutput opens name, a file directly inside dir, the directory a
// sandbox shared with the gate. A symbolic link that leaves the directory is
// refused and never followed. One to a file inside it resolves, since the
// sandbox wrote whatever is there. Opening never waits on a pipe, so the
// caller must check that what it opened is a regular file.
func openSandboxOutput(dir, name string) (*os.File, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

// readSandboxOutput reads name from a sandbox's output directory, if it's a
// regular file there. It reads the file's last limit bytes, which for a log
// are the ones that say how the run ended.
func readSandboxOutput(dir, name string, limit int64) ([]byte, error) {
	f, err := openSandboxOutput(dir, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s in the sandbox's output isn't a regular file", name)
	}
	if info.Size() > limit {
		if _, err := f.Seek(info.Size()-limit, io.SeekStart); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(io.LimitReader(f, limit))
}
