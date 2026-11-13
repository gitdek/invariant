package plumbing

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestGoFilesInLiteralBracketedCheckout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "checkout[")
	for _, file := range []string{"app/close]/page.go", "app/close]/route_test.go", "app/close]/child/child_test.go", "app/s/neighbor_test.go"} {
		write468(t, root, file, "package route\n")
	}
	if err := os.Mkdir(filepath.Join(root, "app/close]/directory_test.go"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("route_test.go", filepath.Join(root, "app/close]/linked_test.go")); err != nil {
		t.Fatal(err)
	}
	plan := &Plan{Files: []string{"app/close]/page.go"}}
	got, err := goFiles(t.Context(), "", root, plan)
	want := []string{"app/close]/page.go", "app/close]/route_test.go"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("Go file list = %v, %v; want %v", got, err, want)
	}
}
