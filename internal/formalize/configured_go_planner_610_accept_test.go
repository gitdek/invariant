package formalize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/synth"
)

const plannerDraft610 = `{"name":"configured Go plan","summary":"Value becomes seven.","files":["api/value.go"],"tests":[{"name":"seven","part":"api","framework":"go","file":"api/value_accept_test.go","selector":"TestValue","says":"Value is seven."}]}`

func plannerSource610(want int) string {
	return fmt.Sprintf("package fixture\nimport \"testing\"\nfunc TestValue(t *testing.T){if Value!=%d{t.Fatal(\"value\",Value)}}\n", want)
}

type plannerRunner610 struct {
	t                 *testing.T
	part              repository.Part
	plan              *plumbing.Plan
	artifact          plumbing.DependencyArtifact
	cache, executable string
	opens, closes     int
}

func (r *plannerRunner610) Install(_ context.Context, q plumbing.DependencyRequest) (plumbing.DependencyArtifact, error) {
	r.artifact = plumbing.DependencyArtifact{Image: "sha256:" + strings.Repeat("b", 64), BaseImage: "sha256:" + strings.Repeat("c", 64), Base: q.Image, Platform: q.Platform, Key: q.Key, Inputs: q.Inputs, Complete: true}
	return r.artifact, nil
}
func (r *plannerRunner610) Inspect(context.Context, string, string) (plumbing.DependencyArtifact, error) {
	return r.artifact, nil
}
func (*plannerRunner610) Cleanup(context.Context, string) error { return nil }
func (*plannerRunner610) Run(context.Context, plumbing.DependencyRequest) ([]byte, error) {
	return nil, errors.New("one-shot execution is not the shared lifecycle")
}
func (r *plannerRunner610) OpenChecks(ctx context.Context, q plumbing.DependencyRequest) (plumbing.RepositoryCheckEnvironment, error) {
	if q.Network || q.Pull || q.Env["GOPROXY"] != "off" || q.Env["GOTOOLCHAIN"] != "local" {
		return nil, errors.New("planner lost offline policy")
	}
	if b, err := os.ReadFile(filepath.Join(q.SourceDir, "api/value.go")); err != nil || string(b) != "package fixture\nconst Value=0\n" {
		return nil, errors.New("planner checked the agent's replacement base")
	}
	r.opens++
	test := r.plan.Tests[0]
	// Read the staged draft anew: the planner may revise its tests after a tool
	// call. This is source setup only; the lifecycle chooses its own invocation.
	data, err := os.ReadFile(filepath.Join(q.SourceDir, test.File))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	selection, err := (plumbing.GoRepositoryAcceptanceAdapter{}).Select(ctx, r.part, test)
	if err != nil {
		return nil, err
	}
	in := plumbing.RepositoryAcceptanceInvocation{Source: string(data), Argv: selection.Argv, Binding: plumbing.RepositoryAcceptanceBinding{
		InvocationID: "transport-setup-610", Part: test.Part, Framework: test.Framework, File: test.File, Selector: test.Selector, Package: selection.Package, SourceDigest: "sha256:" + hex.EncodeToString(sum[:]), ExecutionDigest: r.plan.Execution.Digest,
	}}
	e, err := plumbing.OpenLocalGoProcessTransport(ctx, plumbing.LocalGoProcessOptions{SourceDir: q.SourceDir, GoExecutable: r.executable, CacheDir: r.cache, Part: r.part, Invocation: in})
	if e == nil {
		return nil, err
	}
	return &plannerEnvironment610{RepositoryProcessTransport: e, runner: r}, err
}

type plannerEnvironment610 struct {
	plumbing.RepositoryProcessTransport
	runner *plannerRunner610
}

func (e *plannerEnvironment610) Close(ctx context.Context) error {
	e.runner.closes++
	return e.RepositoryProcessTransport.Close(ctx)
}

type plannerAgent610 struct {
	t    *testing.T
	mode string
	tool *plumbing.Checked
}

func (*plannerAgent610) Name() string { return "configured Go planner fixture" }
func (a *plannerAgent610) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	write503(a.t, job.Workspace, "plan.json", plannerDraft610)
	want := 7
	if a.mode == "already passes" {
		want = 0
	}
	write503(a.t, job.Workspace, "tests/api/value_accept_test.go", plannerSource610(want))
	write503(a.t, job.Workspace, "repo/api/value.go", "package fixture\nconst Value=7\n")
	write503(a.t, job.Workspace, "repo/"+repository.Path, `{"version":999,"parts":[]}`)
	client := &plumbing.CheckClient{}
	for i, flag := range job.GateServer {
		if i+1 >= len(job.GateServer) {
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
		return synth.Usage{}, errors.New("planner did not register captured checks")
	}
	var err error
	a.tool, err = plumbing.Check(ctx, job.Workspace, filepath.Join(job.Workspace, "repo"), plumbing.Sandbox{Checks: client})
	if err != nil {
		return synth.Usage{}, err
	}
	if a.mode == "changes after tool" {
		write503(a.t, job.Workspace, "tests/api/value_accept_test.go", plannerSource610(0))
	}
	return synth.Usage{}, nil
}

func TestConfiguredGoPlannerToolAndProposalAgree610(t *testing.T) {
	cache := t.TempDir()
	for _, mode := range []string{"propose", "already passes", "changes after tool"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			part := repository.Part{Name: "api", Directory: "api", Language: "go", Image: "go@sha256:" + strings.Repeat("a", 64), Inputs: []string{"api/go.mod"}, Lockfiles: []string{"api/go.sum"}, Commands: map[string]repository.Command{}}
			for _, phase := range []string{"install", "format", "lint", "build", "test"} {
				part.Commands[phase] = repository.Command{NotApplicable: "Empty dependency graph and selector-only fixture."}
			}
			part.Commands["test_one"] = repository.Command{Argv: []string{"go", "test", "./...", "-run", "{test}", "-count=1"}}
			config := repository.Config{Version: 1, Parts: []repository.Part{part}, AgentRules: []string{}, CurrentState: []string{}, TrustedPaths: []string{}, SharedPaths: []repository.SharedPath{}}
			data, _ := json.Marshal(config)
			write503(t, root, repository.Path, string(data))
			write503(t, root, "api/value.go", "package fixture\nconst Value=0\n")
			write503(t, root, "api/go.mod", "module example.invalid/planner610\n\ngo 1.27.1\n")
			write503(t, root, "api/go.sum", "")
			for _, args := range [][]string{{"init", "-q"}, {"add", "--all"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "base"}} {
				cmd := exec.Command("git", args...)
				cmd.Dir = root
				cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git: %v %s", err, out)
				}
			}
			snapshot, err := repository.Load(t.Context(), root, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			ws := t.TempDir()
			write503(t, ws, "plan.json", plannerDraft610)
			write503(t, ws, "tests/api/value_accept_test.go", plannerSource610(7))
			plan, err := plumbing.ReadRepositoryDraft(t.Context(), ws, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			exe, err := exec.LookPath("go")
			if err != nil {
				t.Fatal(err)
			}
			exe, err = filepath.EvalSymlinks(exe)
			if err != nil {
				t.Fatal(err)
			}
			runner := &plannerRunner610{t: t, part: part, plan: plan, cache: cache, executable: exe}
			agent := &plannerAgent610{t: t, mode: mode}
			f := Formalizer{Backend: agent, Binary: "unused", CheckRuns: 2, Timeout: 4 * time.Minute, Sandbox: plumbing.Sandbox{Repository: &plumbing.RepositoryChecks{Snapshot: snapshot, Dependencies: plumbing.DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: runner}}}}
			out := t.TempDir()
			r, err := f.Plan(t.Context(), Request{Issue: 610, ParentPlan: 564}, root, out)
			if err != nil || r == nil || agent.tool == nil {
				t.Fatalf("planner did not complete: %v %+v", err, r)
			}
			if (agent.tool.Problem == "") != (mode != "already passes") {
				t.Fatalf("tool gave wrong Go base verdict: %s", agent.tool.Feedback())
			}
			if (r.Proposal != nil) != (mode == "propose") || (r.Problem == "") != (mode == "propose") {
				t.Fatalf("fresh proposal validation gave wrong verdict: %+v", r)
			}
			if runner.opens != 2 || runner.closes != 2 {
				t.Fatalf("tool/final did not execute and clean up independently: opens=%d closes=%d", runner.opens, runner.closes)
			}
			if r.Proposal != nil && (r.Proposal.Hash != agent.tool.Plan.Hash() || r.Proposal.Plan.Sources["api/value_accept_test.go"] != plannerSource610(7)) {
				t.Fatal("proposal lost pinned source or authority")
			}
			data, err = os.ReadFile(filepath.Join(out, "formalization.json"))
			if err != nil {
				t.Fatal(err)
			}
			var saved Result
			if err = json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			if (saved.Proposal != nil) != (mode == "propose") {
				t.Fatal("persisted proposal disagrees with final checks")
			}
		})
	}
}
