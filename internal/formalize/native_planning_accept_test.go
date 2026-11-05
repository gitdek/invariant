package formalize

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/synth"
)

const draft503 = `{"name":"native plan","summary":"Pin native identity.","files":["app/main.py"],"tests":[{"name":"ready","part":"app","framework":"pytest","file":"app/test_ready.py","selector":"test_ready.py::test_ready[case one]","says":"The selected case is ready."}]}`
const source503 = "def test_ready():\r\n    assert False\r\n"

func write503(t *testing.T, root, path, body string) {
	t.Helper()
	name := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

type drafter503 struct {
	t        *testing.T
	previous *plumbing.Plan
	checked  *plumbing.Checked
}

func (*drafter503) Name() string { return "native planning fixture" }
func (a *drafter503) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	data, err := os.ReadFile(filepath.Join(job.Workspace, "previous", "plan.json"))
	if err != nil {
		return synth.Usage{}, err
	}
	var previous plumbing.Plan
	if err := json.Unmarshal(data, &previous); err != nil {
		return synth.Usage{}, err
	}
	if !reflect.DeepEqual(&previous, a.previous) || previous.Hash() != a.previous.Hash() {
		return synth.Usage{}, fmt.Errorf("revision lost its exact previous plan")
	}
	write503(a.t, job.Workspace, "plan.json", draft503)
	write503(a.t, job.Workspace, "tests/app/test_ready.py", source503)
	// Candidate edits must not redefine the captured snapshot used by either
	// process. The tool request contains no policy; its queue owns authority.
	write503(a.t, job.Workspace, "repo/"+repository.Path, `{"candidate":"not authority"}`)
	client := &plumbing.CheckClient{}
	for i, flag := range job.GateServer {
		if i+1 == len(job.GateServer) {
			continue
		}
		switch flag {
		case "-check-queue":
			client.Address = job.GateServer[i+1]
		case "-check-build":
			client.Build = job.GateServer[i+1]
		}
	}
	if client.Address == "" || client.Build == "" {
		return synth.Usage{}, fmt.Errorf("configured planner did not carry captured authority to its check process")
	}
	a.checked, err = plumbing.Check(ctx, job.Workspace, filepath.Join(job.Workspace, "repo"), plumbing.Sandbox{Checks: client})
	if a.checked == nil || a.checked.Plan == nil {
		return synth.Usage{}, fmt.Errorf("check process could not read and bind native draft: %v", err)
	}
	if a.checked.Plan.Hash() != a.previous.Hash() {
		return synth.Usage{}, fmt.Errorf("check process used a different execution contract")
	}
	if a.checked.Problem == "" {
		return synth.Usage{}, fmt.Errorf("unsupported pytest wrapper became proposable")
	}
	return synth.Usage{}, nil
}

func TestConfiguredPlanningCarriesCapturedAuthorityThroughChecksAndRevisions(t *testing.T) {
	root := t.TempDir()
	commands := map[string]repository.Command{}
	for _, phase := range []string{"install", "format", "lint", "build", "test"} {
		commands[phase] = repository.Command{NotApplicable: "No execution in this fixture."}
	}
	// Pytest itself is supported; an opaque wrapper must still refuse before
	// preparation while preserving captured authority through both checks.
	commands["test_one"] = repository.Command{Argv: []string{"native-runner", "pytest", "{test}"}}
	config := repository.Config{Version: 1, Parts: []repository.Part{{Name: "app", Directory: "app", Language: "python", Image: "python@sha256:" + strings.Repeat("a", 64), Lockfiles: []string{}, Inputs: []string{}, Commands: commands}}, AgentRules: []string{}, CurrentState: []string{}, TrustedPaths: []string{}, SharedPaths: []repository.SharedPath{}}
	data, _ := json.Marshal(config)
	write503(t, root, repository.Path, string(data))
	write503(t, root, "app/main.py", "value = 1\n")
	for _, args := range [][]string{{"init", "-q"}, {"add", "--all"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.com", "commit", "-qm", "captured policy"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v: %s", err, out)
		}
	}
	snapshot, err := repository.Load(context.Background(), root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	write503(t, ws, "plan.json", draft503)
	write503(t, ws, "tests/app/test_ready.py", source503)
	previous, err := plumbing.ReadRepositoryDraft(context.Background(), ws, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	agent := &drafter503{t: t, previous: previous}
	f := Formalizer{Backend: agent, Binary: "unused-fixture-binary", CheckRuns: 2, Timeout: 10 * time.Second, Sandbox: plumbing.Sandbox{Original: root, Repository: &plumbing.RepositoryChecks{Snapshot: snapshot}}}
	out := t.TempDir()
	result, err := f.Plan(context.Background(), Request{Issue: 503, Previous: &Proposal{Draft: Draft{Name: previous.Name}, Plan: previous, Hash: previous.Hash()}}, root, out)
	if err != nil {
		t.Fatal(err)
	}
	if agent.checked == nil {
		t.Fatal("configured check process was never reached")
	}
	if result == nil || result.Proposal != nil || result.Problem == "" {
		t.Fatalf("final proposal ignored missing native evidence: %+v", result)
	}
	data, err = os.ReadFile(filepath.Join(out, "formalization.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved Result
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Proposal != nil || saved.Problem == "" {
		t.Fatal("saved final result requalified an unsupported pytest wrapper")
	}
}
