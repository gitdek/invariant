package dashboard

import (
	"os"
	"path/filepath"
	"testing"
)

// ripgrep's -h prints its help, where grep's hides file names, so rg -h is
// other, with no files, and grep -h still searches (#226's review).
func TestRipgrepHelpIsOther(t *testing.T) {
	shell211CheckForms(t, []shell211Form{
		{"grep hides file names", "grep -h PATTERN app.go", "search", []string{"app.go"}},
		{"ripgrep prints its help", "rg -h needle app.go", "other", nil},
	})
}

// A file that doesn't exist, as one an agent made and removed, still
// counts in its workspace when the workspace is reached through a link, as
// macOS's temporary directories are, under /var, a link to /private/var.
func TestAGoneFileStaysInALinkedWorkspace(t *testing.T) {
	real := t.TempDir()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.Symlink(real, ws); err != nil {
		t.Fatal(err)
	}
	tr := newTranscript()
	tr.workspace(ws)
	tr.touch(filepath.Join(ws, "internal", "gone.go"), "edit")
	tr.touch("internal/also-gone.go", "read")
	for _, rel := range []string{"internal/gone.go", "internal/also-gone.go"} {
		if tr.files[rel] == nil {
			t.Errorf("%s isn't counted in the workspace; files: %v", rel, tr.files)
		}
	}
	// A link inside the workspace that leads out of it still leads out.
	if err := os.Symlink(t.TempDir(), filepath.Join(real, "out")); err != nil {
		t.Fatal(err)
	}
	tr.touch(filepath.Join(ws, "out", "gone.go"), "edit")
	if f := tr.files["out/gone.go"]; f != nil {
		t.Errorf("a file through a link out of the workspace counts as in it")
	}
}
