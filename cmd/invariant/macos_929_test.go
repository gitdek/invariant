package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/setup"
)

// todaysInit929 is the SHA-256 of the files init wrote on main before #929
// for a Go module with no contract yet: the gate workflow, a NUL, the
// drafted contract and a NUL, taken before any of #929's changes. A draft
// declares no macOS part, so init writes these files still.
const todaysInit929 = "8f7962eede89a276cba096fbfd208e0a84248537e33cd852f48278d94fceb6ce"

func TestInitWithoutAMacOSPartWritesTodaysFiles929(t *testing.T) {
	dir := repoWithOrigin(t, "https://github.com/acme/widgets.git", "go.mod", "module example.com/widgets\n\ngo 1.27.1\n")
	code, printed := initPrints(t, "-invariant", initPin, dir)
	if code != 0 {
		t.Fatalf("init exited %d:\n%s", code, printed)
	}
	h := sha256.New()
	for _, name := range []string{setup.WorkflowPath, repository.Path} {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		h.Write(append(b, 0))
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != todaysInit929 {
		t.Errorf("init's files for a repository without a macOS part changed: sha256 %s, was %s", got, todaysInit929)
	}
	if strings.Contains(printed, "macOS") || strings.Contains(printed, setup.MacOSCheck) || !strings.Contains(printed, setup.Protection) {
		t.Errorf("init without a macOS part says something new:\n%s", printed)
	}
}

// A retained contract with a part that builds on macOS gets the gate's
// macOS job, kept byte for byte itself, and init asks for both checks to
// be required.
func TestInitWritesTheMacOSJobForAMacOSPart929(t *testing.T) {
	contract := `{"version":1,"parts":[{"name":"go:.","directory":".","language":"go","image":"golang@sha256:` + strings.Repeat("a", 64) + `","lockfiles":[],"inputs":["go.mod"],` +
		`"commands":{},"macos":{"build":{"argv":["go","build","./..."]},"vet":{"argv":["go","vet","./..."]},"test":{"argv":["go","test","./..."]},"checks":[{"name":"selftest","argv":["go","run","./cmd/app","selftest"]}]}}]}` + "\n"
	dir := repoWithOrigin(t, "https://github.com/acme/recorder.git", "go.mod", "module example.com/recorder\n\ngo 1.27.1\n")
	if err := os.MkdirAll(filepath.Join(dir, ".invariant"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(repository.Path)), []byte(contract), 0o644); err != nil {
		t.Fatal(err)
	}
	code, printed := initPrints(t, "-invariant", initPin, dir)
	if code != 0 {
		t.Fatalf("init exited %d:\n%s", code, printed)
	}
	want, err := setup.WorkflowFor(initPin, true)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(setup.WorkflowPath))); err != nil || string(b) != want {
		t.Errorf("init wrote a gate without its macOS job: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(repository.Path))); err != nil || string(b) != contract {
		t.Errorf("init changed the retained contract: %v", err)
	}
	for _, said := range []string{
		"Its invariant/gate-macos job builds, vets and tests the parts the contract says build on macOS (go:.) on a hosted macOS runner, since Linux skips the files only macOS builds.",
		"Require invariant/gate and invariant/gate-macos on main",
		"gh api -X PUT repos/acme/recorder/branches/main/protection --input - <<'EOF'\n" + setup.ProtectionWithMacOS + "\nEOF\n",
		"GitHub refuses that merge, and the factory leaves the pull request for a person",
	} {
		if !strings.Contains(printed, said) {
			t.Errorf("init doesn't say %q:\n%s", said, printed)
		}
	}
	// A gate init didn't write is kept, and the advice for a hand upgrade
	// says to bring the contract along, or its macOS job is left out.
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(setup.WorkflowPath)), []byte("name: a person's gate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, printed = initPrints(t, "-invariant", initPin, dir)
	if code == 0 || !strings.Contains(printed, "Copy .invariant/repository.json into that directory first") {
		t.Errorf("init replaced a person's gate, or didn't say how to upgrade it with its macOS job: %d\n%s", code, printed)
	}
}
