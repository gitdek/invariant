package plumbing

import (
	"path/filepath"
	"testing"
)

// resolvedTempDir516 is t.TempDir with its symbolic links resolved. macOS
// links /var to /private/var: Go and the kernel report the resolved path,
// and the source guard refuses a linked ancestor, so a fixture that maps
// paths into its temporary directory starts from the resolved one (#516).
func resolvedTempDir516(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
