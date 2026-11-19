package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/toolchain"
)

// todaysGate929 is the SHA-256 of the gate workflow Workflow wrote for ref
// on main before #929, taken before any of #929's changes. A repository
// whose contract declares no macOS part gets these bytes still.
const todaysGate929 = "03abc156bf539cc980edaccc169623cf3880fab7462efd88a953cd3dd9f9f438"

func TestAGateWithoutAMacOSPartIsTodays929(t *testing.T) {
	for name, render := range map[string]func() (string, error){
		"Workflow":    func() (string, error) { return Workflow(ref) },
		"WorkflowFor": func() (string, error) { return WorkflowFor(ref, false) },
		"WriteFor": func() (string, error) {
			dir := t.TempDir()
			path, err := WriteFor(dir, ref, false, false)
			if err != nil {
				return "", err
			}
			b, err := os.ReadFile(path)
			return string(b), err
		},
	} {
		w, err := render()
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(w))
		if got := hex.EncodeToString(sum[:]); got != todaysGate929 {
			t.Errorf("%s: the gate without a macOS part changed: sha256 %s, was %s", name, got, todaysGate929)
		}
		if strings.Contains(w, "macos") || strings.Contains(w, MacOSCheck) {
			t.Errorf("%s: a gate without a macOS part has a macOS job", name)
		}
	}
	if ProtectionFor(false) != Protection {
		t.Error("a repository without a macOS part is protected differently")
	}
}

// The macOS job comes after the gate's own, whole: the gate's bytes are
// today's, and the job is pinned as the gate is, to the same commit, with
// the runner and the Go it checks with named exactly.
func TestTheMacOSJobFollowsTheGate929(t *testing.T) {
	gate, err := Workflow(ref)
	if err != nil {
		t.Fatal(err)
	}
	w, err := WorkflowFor(ref, true)
	if err != nil {
		t.Fatal(err)
	}
	job, ok := strings.CutPrefix(w, gate)
	if !ok {
		t.Fatal("the workflow with a macOS job doesn't start with the gate's own bytes")
	}
	for _, want := range []string{
		"\n  gate-macos:\n",
		"\n    name: " + MacOSCheck + "\n",
		"\n    runs-on: macos-26\n",
		"\n    timeout-minutes: 30\n",
		"repository: gitdek/invariant\n          ref: " + ref + "\n",
		"persist-credentials: false",
		"go-version-file: .invariant-gate/go.mod",
		"go-version: '" + toolchain.GoVersion + "'\n",
		"\"$RUNNER_TEMP/invariant\" check-macos -base \"$BASE\" HEAD\n",
		"BASE: ${{ github.event.pull_request.base.sha || github.sha }}",
	} {
		if !strings.Contains(job, want) {
			t.Errorf("the macOS job lacks %q:\n%s", want, job)
		}
	}
	// Nothing that could skip the job, or hand the part's code a token: a
	// skipped required check counts as passed.
	var code []string
	for _, line := range strings.Split(job, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			code = append(code, line)
		}
	}
	for _, bad := range []string{"{{REF}}", "{{GO}}", "macos-latest", "\n    if:", "\n    needs:", "continue-on-error", "token", "TOKEN", "secrets.", "contents: write", "\n    env:", "\n    container:", "\n    defaults:"} {
		if strings.Contains("\n"+strings.Join(code, "\n"), bad) {
			t.Errorf("the macOS job holds %q:\n%s", bad, job)
		}
	}
	if Pinned(w) != ref {
		t.Errorf("the workflow's pin reads as %q", Pinned(w))
	}
	if strings.Count(w, "ref: "+ref) != 2 {
		t.Error("each job doesn't build the gate at the one pinned commit")
	}
	for _, bad := range []string{"main", "61eb049", ""} {
		if _, err := WorkflowFor(bad, true); err == nil {
			t.Errorf("WorkflowFor(%q) should insist on a full commit hash", bad)
		}
	}
}

// Preflight reads the gate's job as it did: a capable gate with the macOS
// job after it is still a capable gate, and the macOS job is never taken
// for invariant/gate, nor makes a workflow a second gate.
func TestPreflightReadsBothRenderings929(t *testing.T) {
	for _, macos := range []bool{false, true} {
		w, err := WorkflowFor(ref, macos)
		if err != nil {
			t.Fatal(err)
		}
		if !hasGateJob(w) || !hasRepositoryGate(w) {
			t.Errorf("macos %t: preflight doesn't find the capable gate", macos)
		}
		g := &capabilityGitHub736{workflows: map[string]string{WorkflowPath: w}}
		if err := CheckRepository(t.Context(), g, "fixture/repository", "main", true); err != nil {
			t.Errorf("macos %t: %v", macos, err)
		}
		if gates := RepositoryGates(map[string]string{WorkflowPath: w}); len(gates) != 1 {
			t.Errorf("macos %t: %d gates", macos, len(gates))
		}
	}
	w, err := WorkflowFor(ref, true)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := Workflow(ref)
	if err != nil {
		t.Fatal(err)
	}
	// The macOS job alone, under a workflow of its own, is no gate.
	alone := "name: macos\n\non:\n  push:\n    branches: [main]\n  pull_request:\n\njobs:" + strings.TrimPrefix(w, gate)
	if hasGateJob(alone) || hasRepositoryGate(alone) {
		t.Error("the macOS job reads as invariant/gate")
	}
}

func TestProtectionRequiresTheMacOSJobToo929(t *testing.T) {
	var p struct {
		Required struct {
			Strict bool `json:"strict"`
			Checks []struct {
				Context string `json:"context"`
				AppID   int    `json:"app_id"`
			} `json:"checks"`
		} `json:"required_status_checks"`
		Admins bool `json:"enforce_admins"`
	}
	if err := json.Unmarshal([]byte(ProtectionFor(true)), &p); err != nil {
		t.Fatal(err)
	}
	var contexts []string
	for _, c := range p.Required.Checks {
		if c.AppID != 15368 {
			t.Errorf("%s is required from app %d, not GitHub Actions", c.Context, c.AppID)
		}
		contexts = append(contexts, c.Context)
	}
	if !slices.Equal(contexts, []string{"invariant/gate", MacOSCheck}) || !p.Admins {
		t.Errorf("the protection requires %q", contexts)
	}
	// It's Protection with one more check, and otherwise the same.
	if strings.Replace(ProtectionWithMacOS, `,{"context":"invariant/gate-macos","app_id":15368}`, "", 1) != Protection {
		t.Error("the protection with the macOS job differs from Protection by more than the one check")
	}
}

// Init writes the macOS job only for a retained contract that declares a
// macOS part, whatever else it holds.
func TestInitFindsARetainedContractsMacOSParts929(t *testing.T) {
	dir := t.TempDir()
	if names, err := InitMacOS(dir); err != nil || names != nil {
		t.Fatalf("with no contract: %q, %v", names, err)
	}
	write := func(text string) {
		if err := os.MkdirAll(filepath.Join(dir, ".invariant"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(repository.Path)), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"version":1,"parts":[{"name":"web","language":"typescript"},{"name":"go:.","language":"go"}],"unresolved":["an unfinished draft"]}`)
	if names, err := InitMacOS(dir); err != nil || names != nil {
		t.Fatalf("without a macOS part: %q, %v", names, err)
	}
	write(`{"version":1,"parts":[{"name":"web","language":"typescript"},{"name":"go:.","language":"go","macos":{"build":{"argv":["go","build","./..."]}}}],"unresolved":["an unfinished draft"]}`)
	if names, err := InitMacOS(dir); err != nil || !slices.Equal(names, []string{"go:."}) {
		t.Fatalf("with a macOS part: %q, %v", names, err)
	}
	write(`{"version":1,"parts":[`)
	if _, err := InitMacOS(dir); err == nil {
		t.Fatal("a contract that isn't JSON read as one without a macOS part")
	}
}
