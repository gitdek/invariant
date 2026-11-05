package plumbing

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/synth"
)

type pyBuilder617 struct {
	t       *testing.T
	f       *pyFixture617
	regress bool
	tool    Result
	reviews int
}

func (*pyBuilder617) Name() string { return "pytest builder fixture" }
func (a *pyBuilder617) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	if job.ReadOnly {
		a.reviews++
		if read484(a.t, filepath.Join(job.Workspace, "base"), "worker/product.py") != "VALUE = 0\n" {
			return synth.Usage{}, errors.New("review lost original base")
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
		return synth.Usage{}, errors.New("builder omitted captured check registration")
	}
	write468(a.t, job.Workspace, "worker/product.py", "VALUE = 7\n")
	write468(a.t, job.Workspace, repository.Path, `{"version":999,"parts":[]}`)
	write468(a.t, job.Workspace, "worker/conftest.py", "raise RuntimeError('unplanned configuration must not enter checks')\n")
	write468(a.t, job.Workspace, LockPath(617), `{"plan":{"files":["worker/conftest.py"]}}`)
	forged := cloneRepositoryPlan(a.f.plan)
	forged.Tests[0].Selector = "test_ready.py::test_fake"
	forged.Sources[forged.Tests[0].File] = "def test_fake(): assert True\n"
	forged.Execution.Digest = hash468("untrusted child policy")
	var err error
	a.tool, err = (Sandbox{Checks: client, Repository: &RepositoryChecks{}}).Run(ctx, job.Workspace, forged)
	if err != nil || !a.tool.Passed() {
		return synth.Usage{}, fmt.Errorf("real pytest tool failed under retained authority: %v\n%s", err, a.tool.Summary())
	}
	if a.regress {
		write468(a.t, job.Workspace, "worker/product.py", "VALUE = 0\n")
	}
	return synth.Usage{Summary: "candidate checked"}, nil
}

func TestPytestOfflineBuilder617(t *testing.T) {
	image, platform, enabled := offline617(t)
	if !enabled {
		return
	}
	for _, shared := range []bool{false, true} {
		for _, regress := range []bool{false, true} {
			t.Run(fmt.Sprintf("shared=%v/regress=%v", shared, regress), func(t *testing.T) {
				f := fixture617(t, image, platform, control617(), nil, nil)
				lock := Lock{Plan: *f.plan, Ratified: Ratification{Issue: 617, Proposal: f.plan.Hash()}}
				if err := lock.Write(f.root); err != nil {
					t.Fatal(err)
				}
				git468(t, f.root, "add", "--all")
				git468(t, f.root, "commit", "-qm", "ratified pytest fixture")
				agent := &pyBuilder617{t: t, f: f, regress: regress}
				builder := Builder{Backend: agent, Reviewer: agent, Binary: "unused", TestRuns: 3, Timeout: 6 * time.Minute, Sandbox: f.sandbox}
				if shared {
					q, err := NewCheckQueue(1)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = q.Close() })
					builder.Checks = q
				}
				r, err := builder.Build(t.Context(), f.root, 617, t.TempDir())
				if r == nil || !regress && err != nil || !agent.tool.Passed() || r.AgentError != "" {
					t.Fatalf("builder failed: %v %+v", err, r)
				}
				want := !regress
				if r.Built() != want || r.Passed != want || r.Approved != want || (agent.reviews == 1) != want {
					t.Fatalf("fresh pytest checks failed to gate review: %+v reviews=%d", r, agent.reviews)
				}
				if len(f.runner.opens) != 2 {
					t.Fatalf("tool and final checks must execute separately, got %d environments", len(f.runner.opens))
				}
				if read484(t, f.root, f.plan.Tests[0].File) != f.plan.Sources[f.plan.Tests[0].File] {
					t.Fatal("builder changed pinned source")
				}
			})
		}
	}
}
