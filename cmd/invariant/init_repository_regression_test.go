package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/setup"
)

func TestInitRefusesLinkedWorkflowBeforeWritingConfiguration(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	initWrite727(t, root, "go.mod", "module fixture\ngo 1.27.1\n")
	initWrite727(t, outside, "gate.yml", "sentinel\n")
	if err := os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "gate.yml"), filepath.Join(root, setup.WorkflowPath)); err != nil {
		t.Fatal(err)
	}
	if code, printed := initPrints(t, "-invariant", initPin, "-repo", "acme/widgets", "-force", root); code == 0 {
		t.Fatalf("linked workflow accepted: %s", printed)
	}
	if got := initRead727(t, outside, "gate.yml"); string(got) != "sentinel\n" {
		t.Fatal("changed linked workflow target")
	}
	if _, err := os.Lstat(filepath.Join(root, repository.Path)); !os.IsNotExist(err) {
		t.Fatalf("wrote configuration before destination validation: %v", err)
	}
}
