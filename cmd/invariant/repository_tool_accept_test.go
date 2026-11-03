package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/repository"
)

type toolRunner484 struct {
	t        *testing.T
	artifact plumbing.DependencyArtifact
	commands []string
	opens    int
}

func (r *toolRunner484) Install(_ context.Context, q plumbing.DependencyRequest) (plumbing.DependencyArtifact, error) {
	r.artifact = plumbing.DependencyArtifact{Image: "sha256:" + strings.Repeat("b", 64), BaseImage: "sha256:" + strings.Repeat("c", 64), Base: q.Image, Platform: q.Platform, Key: q.Key, Inputs: q.Inputs, Complete: true}
	return r.artifact, nil
}
func (r *toolRunner484) Inspect(context.Context, string, string) (plumbing.DependencyArtifact, error) {
	return r.artifact, nil
}
func (*toolRunner484) Cleanup(context.Context, string) error { return nil }
func (*toolRunner484) Run(context.Context, plumbing.DependencyRequest) ([]byte, error) {
	return nil, errors.New("used one-shot dependency consumer")
}
func (r *toolRunner484) OpenChecks(_ context.Context, q plumbing.DependencyRequest) (plumbing.RepositoryCheckEnvironment, error) {
	r.opens++
	if q.Directory != "api" || q.Image != r.artifact.Image || q.Network || q.Pull {
		return nil, errors.New("tool lost captured offline policy")
	}
	b, err := os.ReadFile(filepath.Join(q.SourceDir, "api/main.go"))
	if err != nil || !strings.Contains(string(b), "Candidate = true") {
		return nil, errors.New("tool did not stage the candidate")
	}
	return &toolEnvironment484{r: r}, nil
}

type toolEnvironment484 struct{ r *toolRunner484 }

func (e *toolEnvironment484) Run(ctx context.Context, argv []string) ([]byte, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("tool command has no deadline")
	}
	if len(argv) != 2 {
		return nil, errors.New("tool changed declared argv")
	}
	e.r.commands = append(e.r.commands, argv[1])
	if argv[1] == "lint" {
		return []byte("tool-lint-detail\n"), errors.New("exit status 7")
	}
	return []byte("tool-format-complete\n"), nil
}
func (*toolEnvironment484) Close(ctx context.Context) error { return ctx.Err() }

func TestRepositoryTestToolUsesConfiguredSandbox(t *testing.T) {
	root, candidate := t.TempDir(), t.TempDir()
	write := func(dir, file, text string) {
		t.Helper()
		name := filepath.Join(dir, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	part := repository.Part{Name: "api", Directory: "api", Language: "go", Image: "registry.example/go@sha256:" + strings.Repeat("a", 64), Inputs: []string{}, Lockfiles: []string{}, Commands: map[string]repository.Command{}}
	for _, phase := range []string{"install", "test_one"} {
		part.Commands[phase] = repository.Command{NotApplicable: "Not required by this fixture."}
	}
	for _, phase := range []string{"format", "lint", "build", "test"} {
		part.Commands[phase] = repository.Command{Argv: []string{"fixture-check", phase}}
	}
	config := repository.Config{Version: 1, Parts: []repository.Part{part}, AgentRules: []string{}, CurrentState: []string{}, TrustedPaths: []string{}, SharedPaths: []repository.SharedPath{}}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	write(root, repository.Path, string(data))
	write(root, "api/main.go", "package fixture\n")
	const accept = "package fixture\n\nimport \"testing\"\n\nfunc TestToolNative(t *testing.T) {}\n"
	plan := plumbing.Plan{Name: "configured tool", Summary: "Run the captured checks on candidate files.", Files: []string{"api/main.go"}, Tests: []plumbing.Test{{Name: "TestToolNative", File: "api/tool_native_accept_test.go", Says: "Individual evidence is added in the next issue."}}, Sources: map[string]string{"api/tool_native_accept_test.go": accept}}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	lock := plumbing.Lock{Plan: plan, Ratified: plumbing.Ratification{By: "fixture", Issue: 484, Comment: "https://github.com/o/r/issues/484#issuecomment-1", Proposal: plan.Hash()}}
	if err := lock.Write(root); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "--all"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-q", "--no-verify", "-m", "fixture"}} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	snapshot, err := repository.Load(context.Background(), root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := plumbing.CopyCheckout(context.Background(), root, candidate); err != nil {
		t.Fatal(err)
	}
	write(candidate, "api/main.go", "package fixture\n\nconst Candidate = true\n")
	runner := &toolRunner484{t: t}
	sandbox := plumbing.Sandbox{Original: root, Timeout: 5 * time.Second, Repository: &plumbing.RepositoryChecks{Snapshot: snapshot, Dependencies: plumbing.DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: runner}}}
	tool := testTool(root, 484, candidate, sandbox, 2, "")
	if !strings.Contains(strings.ToLower(tool.Description), "repository") {
		t.Error("configured tool still advertises only Invariant's built-in Go checks")
	}
	out, toolError := tool.Call(context.Background(), json.RawMessage(`{}`))
	if toolError || strings.Contains(out, "Everything passed") || !strings.Contains(out, "api") || !strings.Contains(out, "lint") || !strings.Contains(out, "tool-lint-detail") {
		t.Fatalf("configured tool lost phase failure: toolError=%v\n%s", toolError, out)
	}
	if runner.opens != 1 || strings.Join(runner.commands, ",") != "format,lint" {
		t.Fatalf("tool executed wrong checks: opens=%d commands=%v", runner.opens, runner.commands)
	}
	staged := t.TempDir()
	if err := plumbing.Stage(context.Background(), root, candidate, &plan, staged); err != nil {
		t.Fatal(err)
	}
	final, err := sandbox.Run(context.Background(), staged, &plan)
	if err != nil || final.Passed() || !strings.Contains(final.Summary(), "api") || !strings.Contains(final.Summary(), "lint") {
		t.Fatalf("final check disagrees with tool: %v %s", err, final.Summary())
	}
	if runner.opens != 2 || strings.Join(runner.commands, ",") != "format,lint,format,lint" {
		t.Fatalf("tool and factory paths differ: %v", runner.commands)
	}
}
