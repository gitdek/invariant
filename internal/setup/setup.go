// Package setup prepares another repository for the factory (D-0054): it
// writes the gate workflow that CI runs there, pinned to one commit of
// Invariant.
package setup

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed gate.yml
var gate string

// WorkflowPath is where the gate workflow goes in a repository.
const WorkflowPath = ".github/workflows/gate.yml"

var commit = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Workflow is the gate workflow for another repository, building
// Invariant at ref: a full commit hash, so every run uses the same gate.
func Workflow(ref string) (string, error) {
	if !commit.MatchString(ref) {
		return "", fmt.Errorf("the gate is pinned to a full commit hash of Invariant, and %q isn't one", ref)
	}
	return strings.ReplaceAll(gate, "{{REF}}", ref), nil
}

// ErrExists is what Write returns when the repository already has a
// different gate workflow.
var ErrExists = errors.New("the repository already has a different gate workflow")

// Write writes the gate workflow into the repository in dir. It won't
// replace a different workflow unless force is set.
func Write(dir, ref string, force bool) (string, error) {
	text, err := Workflow(ref)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, filepath.FromSlash(WorkflowPath))
	if old, err := os.ReadFile(path); err == nil && !bytes.Equal(old, []byte(text)) && !force {
		return path, ErrExists
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, []byte(text), 0o644)
}
