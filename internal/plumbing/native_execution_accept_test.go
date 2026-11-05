package plumbing

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

func TestNativeExecutionRefusesBeforePreparationAndKeepsIdentity(t *testing.T) {
	// Go and pytest are supported, but this fixture's native-runner wrapper
	// is not. The TypeScript frameworks still have no production adapter.
	for _, framework := range []string{"go", "pytest", "vitest", "jest"} {
		t.Run(framework, func(t *testing.T) {
			language, file, selector, src := "typescript", "app/ready.test.ts", "outer handles $() ; [case one]", "// exact native source\n"
			if framework == "pytest" {
				language, file, selector, src = "python", "app/test_ready.py", "test_ready.py::test_ready[case one]", "def test_ready():\n    assert False\n"
			}
			if framework == "go" {
				language, file, selector, src = "go", "app/ready_accept_test.go", "TestReady", "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T) {}\n"
			}
			root, candidate, snapshot := fixture484(t, []repository.Part{n503Part(language, "app")}, nil)
			p := n503Read(t, n503Plan(t, framework, "app", file, selector, src), snapshot)
			for f, data := range p.Sources {
				write468(t, candidate, f, data)
			}
			// A candidate's replacement contract is never execution policy.
			write468(t, candidate, repository.Path, `{"candidate":"must never be authority"}`)
			runner := newRunner484(t)
			sb := sandbox484(t, root, snapshot, runner)
			result, err := sb.Run(context.Background(), candidate, p)
			if result.Passed() {
				t.Fatal("unsupported adapter passed")
			}
			if len(runner.installs) != 0 || len(runner.inspections) != 0 || len(runner.opens) != 0 {
				t.Fatal("unsupported acceptance prepared dependencies")
			}
			found := 0
			for _, r := range result.TestResults {
				if r.Phase != "accept" {
					continue
				}
				if r.Part != "app" || r.Framework != framework || r.File != file || r.Selector != selector {
					t.Fatalf("lost exact execution identity: %+v", r)
				}
				if r.Status != "not_run" && r.Status != "refused" {
					t.Fatalf("unsupported adapter manufactured completion: %+v", r)
				}
				found++
			}
			if found != 1 {
				t.Fatalf("want one explicit unstarted acceptance result, got %d: %+v; %v", found, result, err)
			}
			text := result.Summary()
			for _, piece := range []string{"app", framework, file, selector} {
				if !strings.Contains(text, piece) {
					t.Fatalf("diagnostics hide %q: %s", piece, text)
				}
			}
			checked, err := Check(context.Background(), n503Draft(t, p), root, sb)
			if err == nil && (checked == nil || checked.Problem == "") {
				t.Fatal("unsupported draft became proposable")
			}
			// Native metadata can never enter the built-in Go runner without
			// captured authority. This must be refused before Docker is needed.
			missing, err := (Sandbox{}).Run(context.Background(), candidate, p)
			if err == nil && missing.Passed() {
				t.Fatal("missing authority accepted")
			}
			if err != nil && strings.Contains(strings.ToLower(err.Error()), "executable file not found") {
				t.Fatal("missing authority fell through to a runtime")
			}
		})
	}
}

func TestNativeBuildCannotBecomeReadyWithUnsupportedInvocation(t *testing.T) {
	root, _, snapshot := fixture484(t, []repository.Part{n503Part("go", "api")}, nil)
	p := n503Read(t, n503Plan(t, "go", "api", "api/native_accept_test.go", "TestReady", "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T) {}\n"), snapshot)
	lock := Lock{Plan: *p, Ratified: Ratification{Issue: 503, Proposal: p.Hash()}}
	if err := lock.Write(root); err != nil {
		t.Fatal(err)
	}
	agent := &builder484{}
	runner := newRunner484(t)
	b := Builder{Backend: agent, Reviewer: agent, Binary: "unused", TestRuns: 2, Timeout: 10 * time.Second, Sandbox: sandbox484(t, root, snapshot, runner)}
	built, err := b.Build(context.Background(), root, 503, t.TempDir())
	if built == nil {
		if err == nil {
			t.Fatal("build returned no result or refusal")
		}
		return
	}
	if built.Built() || built.Passed || built.Approved {
		t.Fatalf("unsupported native build became ready: %+v", built)
	}
	if len(runner.installs) != 0 {
		t.Fatal("unsupported build reached dependency preparation")
	}
}

func TestConfiguredAcceptanceResultsNeverTreatSkippedEvidenceAsPass(t *testing.T) {
	for _, status := range []string{"not_run", "refused", "skipped", "running"} {
		var r TestResult
		data, _ := json.Marshal(map[string]any{"part": "app", "framework": "jest", "file": "app/ready.test.ts", "selector": "outer ready [one]", "name": "ready", "phase": "accept", "status": status})
		if err := json.Unmarshal(data, &r); err != nil {
			t.Fatal(err)
		}
		result := Result{CommandResults: []CommandResult{{Part: "app", Phase: "test", Status: "passed"}}, Accepted: map[string]bool{"a legacy-looking key": true}, TestResults: []TestResult{r}}
		if result.Passed() {
			t.Fatalf("suite success and %s counted as individual evidence", status)
		}
	}
}

func TestConfiguredDraftNeedsIndividualEvidenceEvenAfterSuiteSuccess(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "suite success", true: "preparation failure"}[failure], func(t *testing.T) {
			root, _, snapshot := fixture484(t, []repository.Part{part484("go", "api")}, nil)
			file := "api/unproved_accept_test.go"
			p := &Plan{Name: "legacy shape with configured execution", Summary: "Require individual evidence.", Files: []string{"api/main.go"}, Tests: []Test{{Name: "TestUnproved", File: file, Says: "This test fails on the base."}}, Sources: map[string]string{file: "package fixture\nimport \"testing\"\nfunc TestUnproved(t *testing.T) { t.Fatal(\"pending\") }\n"}}
			runner := newRunner484(t)
			if failure {
				runner.installHook = func(context.Context, DependencyRequest) (DependencyArtifact, error) {
					return DependencyArtifact{}, errors.New("setup failed before tests")
				}
			}
			runner.hook = func(context.Context, DependencyRequest, []string) ([]byte, error) {
				return []byte("--- PASS: TestUnproved (0.00s)\n@@invariant test=0\n"), nil
			}
			sb := sandbox484(t, root, snapshot, runner)
			checked, err := Check(context.Background(), n503Draft(t, p), root, sb)
			if err == nil && (checked == nil || checked.Problem == "") {
				t.Fatal("no individual failure evidence qualified the draft")
			}
			candidate := t.TempDir()
			if err := CopyCheckout(context.Background(), root, candidate); err != nil {
				t.Fatal(err)
			}
			write468(t, candidate, file, p.Sources[file])
			result, _ := sb.Run(context.Background(), candidate, p)
			if result.Passed() {
				t.Fatal("suite or setup supplied an individual pass")
			}
			found := false
			for _, r := range result.TestResults {
				if r.Phase == "accept" && r.Name == "TestUnproved" && r.File == file {
					found = true
					if r.Status != "not_run" && r.Status != "refused" {
						t.Fatalf("missing evidence marked complete: %+v", r)
					}
				}
			}
			if !found {
				t.Fatal("configured execution omitted its unstarted acceptance result")
			}
		})
	}
}

func TestNativeQueueUsesRegisteredAuthorityAndCannotReplaceItsPlan(t *testing.T) {
	root, snapshot, p := n503BindFixture(t)
	runner := newRunner484(t)
	sb := sandbox484(t, root, snapshot, runner)
	queue, err := NewCheckQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	registered, err := queue.register(context.Background(), sb, p)
	if err != nil {
		t.Fatal(err)
	}
	defer registered.finish()
	client := &CheckClient{Address: queue.listener.Addr().String(), Build: registered.id}
	// A child can supply a candidate root, but not a replacement policy/plan.
	forged := &Plan{Files: []string{"worker/main.go"}}
	result, _ := (Sandbox{Checks: client, Repository: &RepositoryChecks{}}).Run(context.Background(), root, forged)
	if result.Passed() {
		t.Fatal("IPC replaced registered native acceptance")
	}
	if len(result.TestResults) != 1 || result.TestResults[0].Selector != p.Tests[0].Selector {
		t.Fatalf("queue lost its registered identity: %+v", result)
	}
	if len(runner.installs) != 0 {
		t.Fatal("unsupported queued acceptance reached preparation")
	}
}

func TestNativeExtensionPreservesPreChangeGoHashesAndCommittedLocks(t *testing.T) {
	// These constants were computed from the original Plan.Hash before #503.
	for summary, expected := range map[string]string{
		"Keep the old hash.":      "sha256:38f5e36ab3f3bec9291b281ee1b870b92a389f6070140a657cbf090fac5ce2b2",
		"Keep a second old hash.": "sha256:255cbad2107ad56ca18e8ead3fbdb2e65e2345d0f699d4cecd3ed5a41229f92c",
	} {
		p := &Plan{Name: "irrelevant display name", Summary: summary, Files: []string{"app/main.go"}, Tests: []Test{{Name: "TestReady", File: "app/ready_accept_test.go", Says: "The behavior holds."}}, Sources: map[string]string{"app/ready_accept_test.go": "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T) {}\n"}}
		if err := p.Validate(); err != nil {
			t.Fatal(err)
		}
		if p.Hash() != expected {
			t.Fatalf("legacy canonical bytes changed: %s != %s", p.Hash(), expected)
		}
		data, _ := json.Marshal(p)
		for _, key := range []string{`"part":`, `"framework":`, `"selector":`, `"execution":`} {
			if strings.Contains(string(data), key) {
				t.Fatalf("legacy plan inferred native metadata: %s", data)
			}
		}
	}
	for _, name := range []string{"issue-100.json", "issue-103.json", "issue-484.json"} {
		data, err := os.ReadFile(filepath.Join("..", "..", LockDir, name))
		if err != nil {
			t.Fatal(err)
		}
		lock, err := ReadLock(data)
		if err != nil {
			t.Fatalf("committed Go lock %s invalidated: %v", name, err)
		}
		if lock.Plan.Hash() != lock.Ratified.Proposal {
			t.Fatal("committed lock needs migration")
		}
		encoded, _ := json.Marshal(lock)
		if _, err := ReadLock(encoded); err != nil {
			t.Fatal(err)
		}
	}
	// In the same executable test, native metadata must actually be retained
	// and hashed, so an unchanged implementation cannot pass compatibility
	// merely by discarding all the new fields.
	_, snapshot, p := n503BindFixture(t)
	copy := n503Clone(t, p)
	copy.Tests[0].Selector += " another case"
	if copy.Hash() == p.Hash() {
		t.Fatal("native selectors were discarded to preserve Go hashes")
	}
	if err := p.ValidateRepository(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
}
