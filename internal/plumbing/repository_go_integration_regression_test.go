package plumbing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func TestRepositoryGoMultipleCandidateSelectors(t *testing.T) {
	cache := t.TempDir()
	source := repositorySource582(`
		for _, name := range []string{"left", "right"} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				if Value != 7 { t.Fatal("independent assertion", name) }
				t.Log("independent completion", name)
			})
		}`, "")
	f := newRepositoryFixture582(t, cache, []repositoryCase582{
		{"api", "nested", "TestReady/left", source},
		{"api", "nested", "TestReady/right", source},
		{"web", "nested", "TestReady/left", source},
	}, false, nil, nil)
	base, problem, err := f.run(t.Context(), true)
	if err != nil || problem != "" {
		t.Fatalf("independent base failures: %v %s\n%s", err, problem, base.Summary())
	}
	f.candidateValue(7)
	candidate, _, err := f.run(t.Context(), false)
	if err != nil || !candidate.Passed() {
		t.Fatalf("multiple real selectors: %v\n%s", err, candidate.Summary())
	}
	for _, test := range f.plan.Tests {
		repositoryResult582(t, base, test, "failed", "independent assertion")
		repositoryResult582(t, candidate, test, "passed", "independent completion")
	}
	repositoryRoundTrip582(t, candidate)
}

func TestRepositoryGoTerminalSurvivesMissingProcessExit(t *testing.T) {
	cache := t.TempDir()
	for _, base := range []bool{true, false} {
		f := newRepositoryFixture582(t, cache, []repositoryCase582{repositoryControl582()}, false, nil, nil)
		f.candidateValue(7)
		f.runner.damage = func(_ string, out RepositoryProcessOutput) (RepositoryProcessOutput, error) {
			out.Exited, out.ExitCode = false, -1
			return out, errors.New("process exit unavailable")
		}
		r, problem, err := f.run(t.Context(), base)
		if err == nil || r.Passed() || base && problem == "" || !strings.Contains(r.Summary(), "process exit unavailable") {
			t.Fatalf("missing process exit did not veto: %v\n%s", err, r.Summary())
		}
		status := "passed"
		if base {
			status = "failed"
		}
		repositoryResult582(t, r, f.plan.Tests[0], status, "")
		repositoryRoundTrip582(t, r)
	}
}

func TestRepositoryGoDiscoveryTransportFailureStopsBase(t *testing.T) {
	cache := t.TempDir()
	for _, boundary := range []string{"process", "adapter"} {
		t.Run(boundary, func(t *testing.T) {
			first := repositoryControl582()
			first.source = repositorySource582(`MissingDeclaration(t)`, "")
			later := first
			later.part = "web"
			later.source = repositoryControl582().source
			f := newRepositoryFixture582(t, cache, []repositoryCase582{first, later}, false, nil, nil)
			if boundary == "adapter" {
				f.sandbox.Repository.Adapters["go"] = repositoryDiscoveryVeto{}
			} else {
				f.runner.before = func(_ context.Context, stage, part string, _ []string) error {
					if stage == "compile" && part == "api" {
						return errors.Join(errors.New("exit status 1"), errors.New("discovery transport unavailable"))
					}
					return nil
				}
			}
			r, problem, err := f.run(t.Context(), true)
			if err == nil || problem == "" || len(f.runner.envs) != 1 {
				t.Fatalf("transport failure allowed independent work: %v %s", err, problem)
			}
			repositoryResult582(t, r, f.plan.Tests[0], "refused", "discovery transport unavailable")
			repositoryResult582(t, r, f.plan.Tests[1], "not_run", "")
		})
	}
}

type repositoryDiscoveryVeto struct{ GoRepositoryAcceptanceAdapter }

func (a repositoryDiscoveryVeto) Discover(ctx context.Context, part repository.Part, in RepositoryAcceptanceInvocation, env RepositoryCheckEnvironment) (RepositoryAcceptanceDiscovery, error) {
	discovery, err := a.GoRepositoryAcceptanceAdapter.Discover(ctx, part, in, env)
	if err != nil {
		err = errors.Join(err, errors.New("discovery transport unavailable"))
	}
	return discovery, err
}
