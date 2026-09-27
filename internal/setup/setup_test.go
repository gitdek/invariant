package setup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const ref = "61eb0495cd8a5ff84be5bbdb4efc3bb2b7c1ae23"

func TestWorkflowPinsInvariant(t *testing.T) {
	w, err := Workflow(ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ref: " + ref, "name: invariant/gate", "secrets.INVARIANT_DEPLOY_KEY", "persist-credentials: false", "invariant\" verify", "contents: read"} {
		if !strings.Contains(w, want) {
			t.Errorf("the workflow lacks %q", want)
		}
	}
	if strings.Contains(w, "{{REF}}") || strings.Contains(w, "contents: write") {
		t.Error("the workflow isn't pinned, or it can write")
	}
	for _, bad := range []string{"main", "v1.0", "61eb049", ""} {
		if _, err := Workflow(bad); err == nil {
			t.Errorf("Workflow(%q) should insist on a full commit hash", bad)
		}
	}
}

func TestWriteKeepsADifferentWorkflow(t *testing.T) {
	dir := t.TempDir()
	path, err := Write(dir, ref, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Write(dir, ref, false); err != nil {
		t.Errorf("writing the same workflow again: %v", err)
	}
	os.WriteFile(path, []byte("name: someone else's gate\n"), 0o644)
	if _, err := Write(dir, ref, false); !errors.Is(err, ErrExists) {
		t.Errorf("replaced a different workflow without -force: %v", err)
	}
	if _, err := Write(dir, ref, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, WorkflowPath)); !strings.Contains(string(b), ref) {
		t.Error("-force didn't write the workflow")
	}
}
