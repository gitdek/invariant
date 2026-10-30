package toolchain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Cleaning a path must not make an unopenable reference appear valid. The
// same directory component is valid once it really is a directory, even
// though .. removes it from the final input's provenance path.
func TestPythonPreflightRequiresTraversedDirectories(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		"requirements.txt": "-r hop/../constraints.txt\n",
		"constraints.txt":  "packaging==24.2\n",
		"hop":              "not a directory\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	preflight := func() error {
		p := &pythonInputs{root: root, bytes: map[string][]byte{}, files: map[pythonFile]bool{}, includes: map[pythonInclude]bool{}}
		return p.preflight("requirements.txt", false, map[string]bool{})
	}
	if err := preflight(); err == nil || !strings.Contains(err.Error(), "non-directory") {
		t.Fatalf("file/../target accepted: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "hop")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "hop"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := preflight(); err != nil {
		t.Fatalf("directory/../target refused: %v", err)
	}
}

func TestPythonResolutionRequiresEvidenceEvenWhenEmpty(t *testing.T) {
	for _, report := range []string{`{}`, `{"version":"1"}`, `{"version":"1","install":null}`, `{"version":"2","install":[]}`} {
		if _, err := pythonArtifacts([]byte(report)); err == nil {
			t.Errorf("accepted incomplete installation evidence: %s", report)
		}
	}
	artifacts, err := pythonArtifacts([]byte(`{"version":"1","install":[]}`))
	if err != nil || artifacts == nil || len(artifacts) != 0 {
		t.Fatalf("explicit empty resolution: %#v %v", artifacts, err)
	}
}
