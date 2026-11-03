package plumbing

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func TestNativeExecutionCanonicalizesContractDeclarations(t *testing.T) {
	parts := []repository.Part{n503Part("python", "worker"), n503Part("typescript", "web")}
	root, _, snapshot := fixture484(t, parts, nil)
	p := n503Read(t, n503Plan(t, "pytest", "worker", "worker/test_order.py", "test_order.py::test_ready", "def test_ready():\n    assert False\n"), snapshot)
	config := snapshot.Config()
	slices.Reverse(config.Parts)
	for i := range config.Parts {
		slices.Reverse(config.Parts[i].Inputs)
		slices.Reverse(config.Parts[i].Lockfiles)
	}
	data, err := json.MarshalIndent(config, "", "    ")
	if err != nil {
		t.Fatal(err)
	}
	write468(t, root, repository.Path, string(data)+"\n")
	git468(t, root, "add", "--all")
	git468(t, root, "commit", "-qm", "reorder declarations")
	reordered, err := repository.Load(t.Context(), root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ValidateRepository(t.Context(), reordered); err != nil {
		t.Fatal(err)
	}
	if fresh := n503Read(t, p, reordered); fresh.Hash() != p.Hash() {
		t.Fatal("equivalent captured declarations changed ratification")
	}
}

func TestNativeQueueCapturesPlanAndRejectsPlanningRequests(t *testing.T) {
	root, snapshot, p := n503BindFixture(t)
	want := p.Tests[0].Selector
	runner := newRunner484(t)
	sb := sandbox484(t, root, snapshot, runner)
	q, err := NewCheckQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	b, err := q.register(t.Context(), sb, p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.finish()
	client := &CheckClient{Address: q.listener.Addr().String(), Build: b.id}
	p.Tests[0].Selector = "test_accept.py::test_other"
	p.Sources[p.Tests[0].File] = "assert True\n"
	sb.Repository.Snapshot = nil
	result, err := client.run(t.Context(), root)
	if err != nil || result.Passed() || len(result.TestResults) != 1 || result.TestResults[0].Selector != want {
		t.Fatalf("registration changed with its caller: %+v, %v", result, err)
	}
	if _, err := client.check(t.Context(), n503Draft(t, p)); err == nil {
		t.Fatal("build registration accepted a replacement draft")
	}
	if len(runner.installs) != 0 {
		t.Fatal("unsupported acceptance reached preparation")
	}
}

func TestNativeLockWritingRefusesLinkedAndExistingSources(t *testing.T) {
	_, _, p := n503BindFixture(t)
	lock := Lock{Plan: *p, Ratified: Ratification{Issue: 503, Proposal: p.Hash()}}
	for _, kind := range []string{"different bytes", "hard link", "symbolic ancestor", "same bytes"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			file := p.Tests[0].File
			source := p.Sources[file]
			switch kind {
			case "different bytes":
				write468(t, root, file, "existing source\n")
			case "hard link":
				write468(t, root, file, source)
				if err := os.Link(filepath.Join(root, file), filepath.Join(t.TempDir(), "linked.py")); err != nil {
					t.Fatal(err)
				}
			case "symbolic ancestor":
				if err := os.Symlink(t.TempDir(), filepath.Join(root, "worker")); err != nil {
					t.Fatal(err)
				}
			case "same bytes":
				write468(t, root, file, source)
			}
			err := lock.Write(root)
			if (err == nil) != (kind == "same bytes") {
				t.Fatalf("write %s: %v", kind, err)
			}
			if kind == "different bytes" {
				data, err := os.ReadFile(filepath.Join(root, file))
				if err != nil || string(data) != "existing source\n" {
					t.Fatal("lock write overwrote existing source")
				}
			}
		})
	}
}

func TestConfiguredResultsRequireEachExactAcceptanceIdentity(t *testing.T) {
	first := Test{Name: "same", Part: "web", Framework: "jest", File: "web/a.test.ts", Selector: "outer ready"}
	second := first
	second.File = "web/b.test.ts"
	r := unstartedAcceptance(&Plan{Tests: []Test{first, second}})
	r.CommandResults = []CommandResult{{Part: "web", Phase: "test", Status: "passed"}}
	r.Accepted[first.Identity()], r.Accepted[second.Identity()] = true, true
	r.TestResults = r.TestResults[:1]
	r.TestResults[0].Status = "passed"
	if r.Passed() {
		t.Fatal("another file's suite or legacy boolean supplied individual evidence")
	}
	r.TestResults = append(r.TestResults, TestResult{Name: second.Name, Part: second.Part, Framework: second.Framework, File: second.File, Selector: second.Selector, Phase: "accept", Status: "passed"})
	if !r.Passed() {
		t.Fatal("complete exact individual evidence was refused")
	}
}

func TestNativeAuthorityCannotEnterLegacyExecution(t *testing.T) {
	_, _, p := n503BindFixture(t)
	p.Tests = []Test{{Name: "TestLegacy", File: "app/accept_test.go"}}
	result, err := (Sandbox{}).Run(context.Background(), t.TempDir(), p)
	if err != nil || result.Passed() || len(result.Problems) == 0 {
		t.Fatalf("native execution metadata fell through to the legacy runtime: %+v, %v", result, err)
	}
}

func TestNativeMissingObjectIsNotAnAbsentAcceptanceFile(t *testing.T) {
	root, _, _ := fixture484(t, []repository.Part{n503Part("python", "worker")}, nil)
	file := "worker/test_is not a committed regular file.py"
	source := "def test_ready():\n    assert False\n"
	write468(t, root, file, source)
	git468(t, root, "add", "--all")
	git468(t, root, "commit", "-qm", "existing acceptance")
	snapshot, err := repository.Load(t.Context(), root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	blob := strings.TrimSpace(git468(t, root, "rev-parse", "HEAD:"+file))
	if len(blob) != 40 && len(blob) != 64 {
		t.Fatalf("invalid fixture blob: %q", blob)
	}
	if err := os.Remove(filepath.Join(root, ".git", "objects", blob[:2], blob[2:])); err != nil {
		t.Fatal(err)
	}
	p := n503Plan(t, "pytest", "worker", file, "test_is not a committed regular file.py::test_ready", source)
	if _, err := ReadRepositoryDraft(t.Context(), n503Draft(t, p), snapshot); err == nil {
		t.Fatal("an unreadable committed source was admitted as a new file")
	}
}
