package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/synth"
)

type toolRunner654 struct {
	*toolRunner484
	seen   int
	policy string
}

func (r *toolRunner654) OpenChecks(_ context.Context, q plumbing.DependencyRequest) (plumbing.RepositoryCheckEnvironment, error) {
	if q.Directory != "api" || q.Network || q.Pull || q.Image != r.artifact.Image {
		return nil, errors.New("tool changed captured execution")
	}
	if toolRead654(q.SourceDir, repository.Path) != r.policy {
		return nil, errors.New("candidate supplied repository policy")
	}
	if toolRead654(q.SourceDir, "api/service.py") != "value = 2\n" {
		return nil, errors.New("planned code missing")
	}
	if _, err := os.Stat(filepath.Join(q.SourceDir, "api/unplanned.py")); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("unnamed production file reached execution")
	}
	return &toolEnvironment654{r: r, root: q.SourceDir}, nil
}

type toolEnvironment654 struct {
	r    *toolRunner654
	root string
}

func (e *toolEnvironment654) Run(ctx context.Context, argv []string) ([]byte, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("command lacks deadline")
	}
	if !slices.Equal(argv, []string{"pytest", "-q", "tests"}) {
		return nil, errors.New("tool changed suite argv")
	}
	if toolRead654(e.root, "api/tests/test_regression.py") == "def test_regression():\n    assert False\n" {
		e.r.seen++
		return []byte("regression654 reached captured pytest suite\n"), errors.New("exit status 1")
	}
	return nil, nil
}
func (*toolEnvironment654) Close(ctx context.Context) error { return ctx.Err() }

func toolRead654(root, name string) string {
	b, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	return string(b)
}
func toolWrite654(t *testing.T, root, name, text string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

type toolAgent654 struct {
	t         *testing.T
	root      string
	out       string
	toolError bool
}

func (*toolAgent654) Name() string { return "CLI regression fixture" }
func (a *toolAgent654) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	if job.ReadOnly {
		return synth.Usage{}, errors.New("failing regression must prevent review")
	}
	client := &plumbing.CheckClient{}
	for i := 0; i+1 < len(job.GateServer); i++ {
		switch job.GateServer[i] {
		case "-check-queue":
			client.Address = job.GateServer[i+1]
		case "-check-build":
			client.Build = job.GateServer[i+1]
		}
	}
	if client.Address == "" || client.Build == "" {
		return synth.Usage{}, errors.New("missing check endpoint")
	}
	toolCandidate654(a.t, job.Workspace)
	tool := testTool(a.root, 654, job.Workspace, plumbing.Sandbox{Checks: client}, 2, "")
	a.out, a.toolError = tool.Call(ctx, json.RawMessage(`{}`))
	return synth.Usage{Summary: "wrote failing regression"}, nil
}

func toolCandidate654(t *testing.T, root string) {
	toolWrite654(t, root, "api/service.py", "value = 2\n")
	toolWrite654(t, root, "api/tests/test_regression.py", "def test_regression():\n    assert False\n")
	toolWrite654(t, root, "api/unplanned.py", "raise RuntimeError('unplanned')\n")
	toolWrite654(t, root, repository.Path, `{"version":999}`)
}

func TestConfiguredRegressionTool654(t *testing.T) {
	for _, queued := range []bool{false, true} {
		name := "direct"
		if queued {
			name = "registered"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			commands := map[string]repository.Command{}
			for _, phase := range []string{"install", "format", "lint", "build"} {
				commands[phase] = repository.Command{NotApplicable: "Not needed by fixture."}
			}
			commands["test"] = repository.Command{Argv: []string{"pytest", "-q", "tests"}}
			commands["test_one"] = repository.Command{Argv: []string{"pytest", "{test}"}}
			config := repository.Config{Version: 1, Parts: []repository.Part{{Name: "api", Directory: "api", Language: "python", Image: "python@sha256:" + strings.Repeat("a", 64), Inputs: []string{}, Lockfiles: []string{}, Commands: commands}}, AgentRules: []string{}, CurrentState: []string{}, TrustedPaths: []string{}, SharedPaths: []repository.SharedPath{}}
			data, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			toolWrite654(t, root, repository.Path, string(data))
			toolWrite654(t, root, "api/service.py", "value = 1\n")
			toolWrite654(t, root, "api/tests/test_existing.py", "def test_existing():\n    assert True\n")
			for _, args := range [][]string{{"init", "-q"}, {"add", "--all"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-q", "--no-verify", "-m", "base"}} {
				cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
				cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git: %v %s", err, out)
				}
			}
			snapshot, err := repository.Load(ctx, root, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			draft := t.TempDir()
			toolWrite654(t, draft, "plan.json", `{"name":"regression tool","summary":"Change service and test it.","files":["api/service.py"],"tests":[{"name":"contract","file":"api/tests/test_contract.py","part":"api","framework":"pytest","selector":"tests/test_contract.py::test_contract","says":"The contract holds."}]}`)
			toolWrite654(t, draft, "tests/api/tests/test_contract.py", "def test_contract():\n    assert True\n")
			plan, err := plumbing.ReadRepositoryDraft(ctx, draft, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if err := (plumbing.Lock{Plan: *plan, Ratified: plumbing.Ratification{Issue: 654, Proposal: plan.Hash()}}).Write(root); err != nil {
				t.Fatal(err)
			}
			runner := &toolRunner654{toolRunner484: &toolRunner484{t: t}, policy: string(data)}
			sandbox := plumbing.Sandbox{Original: root, Timeout: 30 * time.Second, Repository: &plumbing.RepositoryChecks{Snapshot: snapshot, Dependencies: plumbing.DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: runner}}}
			var out string
			var toolError bool
			if queued {
				agent := &toolAgent654{t: t, root: root}
				result, err := (plumbing.Builder{Backend: agent, Reviewer: agent, Binary: "unused", TestRuns: 2, Timeout: time.Minute, Sandbox: sandbox}).Build(ctx, root, 654, t.TempDir())
				if err != nil || result == nil {
					t.Fatalf("builder: %v %+v", err, result)
				}
				if result.Built() || result.Passed {
					t.Error("failing added regression made a ready build")
				}
				out, toolError = agent.out, agent.toolError
				if runner.seen != 2 {
					t.Errorf("tool and fresh final must both execute added regression; saw %d", runner.seen)
				}
			} else {
				candidate := t.TempDir()
				if err := plumbing.CopyCheckout(ctx, root, candidate); err != nil {
					t.Fatal(err)
				}
				toolCandidate654(t, candidate)
				tool := testTool(root, 654, candidate, sandbox, 2, "")
				out, toolError = tool.Call(ctx, json.RawMessage(`{}`))
				if runner.seen != 1 {
					t.Errorf("CLI staging discarded regression before suite; saw %d", runner.seen)
				}
			}
			if toolError || !strings.Contains(out, "regression654 reached captured pytest suite") || strings.Contains(out, "Everything passed") {
				t.Errorf("tool lost normal regression failure: error=%v output=%s", toolError, out)
			}
		})
	}
}
