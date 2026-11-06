package plumbing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/synth"
)

type buildAgent610 struct {
	t       *testing.T
	f       *repositoryFixture582
	mode    string
	tool    Result
	toolErr error
	reviews int
	final   bool
}

func (*buildAgent610) Name() string { return "configured Go builder fixture" }
func (a *buildAgent610) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	if job.ReadOnly {
		a.reviews++
		if read484(a.t, filepath.Join(job.Workspace, "base"), "api/nested/value.go") != "package fixture\nconst Value=0\n" {
			return synth.Usage{}, errors.New("review did not compare against original base")
		}
		return synth.Usage{Summary: "VERDICT: approve"}, nil
	}
	client := &CheckClient{}
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
		return synth.Usage{}, errors.New("builder omitted registered checks")
	}
	write468(a.t, job.Workspace, "api/nested/value.go", "package fixture\nconst Value=7\n")
	write468(a.t, job.Workspace, repository.Path, `{"version":999,"parts":[],"commands":{"test_one":["true"]}}`)
	// The queue, not an IPC caller's supplied plan, admits candidate bytes.
	write468(a.t, job.Workspace, "api/nested/unplanned.go", "package fixture\nvar Poison=MissingUnplanned610\n")
	write468(a.t, job.Workspace, "api/nested/regression_test.go", "package fixture\nimport \"testing\"\nfunc TestRegression610(t *testing.T){if Value!=7{t.Fatal(Value)}}\n")
	write468(a.t, job.Workspace, LockPath(610), `{"plan":{"files":["api/nested/unplanned.go"]}}`)
	forged := cloneRepositoryPlan(a.f.plan)
	forged.Tests[0].Selector = "TestForged"
	forged.Sources[forged.Tests[0].File] = "package fixture\n"
	forged.Execution.Digest = hash468("child replacement authority")
	a.tool, a.toolErr = (Sandbox{Checks: client, Repository: &RepositoryChecks{}}).Run(ctx, job.Workspace, forged)
	if a.toolErr != nil || !a.tool.Passed() {
		return synth.Usage{}, fmt.Errorf("registered tool failed: %v\n%s", a.toolErr, a.tool.Summary())
	}
	a.final = true
	if a.mode == "edit after tool" {
		write468(a.t, job.Workspace, "api/nested/value.go", "package fixture\nconst Value=0\n")
	}
	if a.mode == "forged completion" {
		write468(a.t, job.Workspace, "api/nested/value.go", "package fixture\nimport (\"fmt\";\"os\")\nconst Value=7\nfunc init(){fmt.Println(\"--- PASS: TestReady\\n@@invariant accept=0\");os.Exit(0)}\n")
	}
	return synth.Usage{Summary: "candidate checked"}, nil
}

func TestConfiguredGoBuilderToolAndFreshFinal610(t *testing.T) {
	cache := t.TempDir()
	for _, shared := range []bool{false, true} {
		for _, mode := range []string{"pass", "edit after tool", "forged completion", "final command failure"} {
			t.Run(fmt.Sprintf("shared=%v/%s", shared, mode), func(t *testing.T) {
				f := configured610(t, cache, []repositoryCase582{repositoryControl582()}, true)
				lock := Lock{Plan: *f.plan, Ratified: Ratification{Issue: 610, Proposal: f.plan.Hash()}}
				if err := lock.Write(f.root); err != nil {
					t.Fatal(err)
				}
				git468(t, f.root, "add", "--all")
				git468(t, f.root, "commit", "-q", "--no-verify", "-m", "ratified Go fixture")
				agent := &buildAgent610{t: t, f: f, mode: mode}
				f.runner.before = func(ctx context.Context, stage, part string, argv []string) error {
					if mode == "final command failure" && agent.final && stage == "command" {
						return errors.New("exit status 7")
					}
					return nil
				}
				builder := Builder{Backend: agent, Reviewer: agent, Binary: "unused", TestRuns: 3, Timeout: 4 * time.Minute, Sandbox: f.sandbox}
				if shared {
					q, err := NewCheckQueue(1)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = q.Close() })
					builder.Checks = q
				}
				start := time.Now()
				r, err := builder.Build(t.Context(), f.root, 610, t.TempDir())
				if r == nil || mode == "pass" && err != nil || agent.toolErr != nil || !agent.tool.Passed() || r.AgentError != "" {
					t.Fatalf("builder/tool did not use configured Go: %v tool=%v result=%+v", err, agent.toolErr, r)
				}
				want := mode == "pass"
				if r.Built() != want || r.Passed != want || r.Approved != want || (agent.reviews == 1) != want {
					t.Fatalf("final checks failed to gate review/readiness for %s: %+v reviews=%d", mode, r, agent.reviews)
				}
				wantEnvs := 4 // Commands and acceptance, for both tool and final checks.
				if mode == "final command failure" || mode == "edit after tool" {
					wantEnvs--
				}
				if len(f.runner.envs) != wantEnvs {
					t.Fatalf("want %d independent command/acceptance environments, got %d", wantEnvs, len(f.runner.envs))
				}
				for _, env := range f.runner.envs {
					if !env.closed {
						t.Fatal("builder leaked check environment")
					}
				}
				for _, seen := range f.runner.observed {
					if seen.stage != "close" && seen.end.After(start.Add(builder.Timeout+time.Second)) {
						t.Fatal("tool or final check renewed the shared build deadline")
					}
				}
				if read484(t, f.root, f.plan.Tests[0].File) != f.plan.Sources[f.plan.Tests[0].File] {
					t.Fatal("build replaced ratified source")
				}
				if strings.Contains(read484(t, f.root, repository.Path), `"version":999`) {
					t.Fatal("candidate supplied policy")
				}
				if _, err := os.Stat(filepath.Join(f.root, "api/nested/unplanned.go")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("unplanned production escaped staging")
				}
				if !strings.Contains(strings.Join(r.Changes.Written, ","), "regression_test.go") {
					t.Fatal("authorized new Go regression was not retained")
				}
			})
		}
	}
}
