package plumbing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func TestAcceptanceSelectionCannotChangeCapturedCommandsOrArguments(t *testing.T) {
	f := newFixture515(t)
	argv := []string{"trusted-fixture", ""}
	f.adapter.selectHook = func(ctx context.Context, part repository.Part, test Test) (RepositoryAcceptanceSelection, error) {
		part.Commands["format"].Argv[0] = "replacement"
		argv[1] = test.Selector
		return RepositoryAcceptanceSelection{Package: "./api", Argv: argv}, ctx.Err()
	}
	f.runner.accept = func(_ context.Context, _ DependencyRequest, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
		if in.Argv[1] != in.Binding.Selector {
			t.Fatal("a later selection changed an earlier invocation's arguments")
		}
		in.Argv[0] = "environment-owned mutation"
		return evidence515(in, "test", "passed", "completed"), nil, nil
	}
	result, err := f.sandbox.Run(context.Background(), f.candidate, f.plan)
	if err != nil || !result.Passed() {
		t.Fatal(describe515(result, err))
	}
	for _, call := range f.runner.calls {
		if call.argv[0] != "check-go" {
			t.Fatal("selection replaced captured suite policy")
		}
	}
	closed484(t, f.runner.runner484)
}

func TestAcceptanceRefusesUnusableSelectionsBeforePreparation(t *testing.T) {
	for _, mode := range []string{"empty argv", "empty executable", "missing package", "NUL argument", "canceled selection"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture515(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.adapter.selectHook = func(context.Context, repository.Part, Test) (RepositoryAcceptanceSelection, error) {
				s := RepositoryAcceptanceSelection{Package: "./api", Argv: []string{"trusted-fixture"}}
				switch mode {
				case "empty argv":
					s.Argv = nil
				case "empty executable":
					s.Argv[0] = ""
				case "missing package":
					s.Package = ""
				case "NUL argument":
					s.Argv = append(s.Argv, "bad\x00argument")
				case "canceled selection":
					cancel()
				}
				return s, nil
			}
			checked := base515(t, f, ctx)
			if checked.Problem == "" || len(f.runner.installs) != 0 || len(f.runner.opens) != 0 {
				t.Fatal("unusable selection reached preparation or qualified")
			}
		})
	}
}

func TestAcceptanceBaseSuiteInfrastructureFailureVetoesQualification(t *testing.T) {
	for _, phase := range []string{"build", "test"} {
		t.Run(phase, func(t *testing.T) {
			f := newFixture515(t)
			allFail515(f.runner)
			f.runner.hook = func(_ context.Context, _ DependencyRequest, argv []string) ([]byte, error) {
				if argv[1] == phase {
					return []byte("exit status 1"), errors.New("cannot launch command")
				}
				return nil, nil
			}
			checked := base515(t, f, context.Background())
			if checked.Problem == "" || len(f.runner.invocations) != 0 {
				t.Fatal("suite infrastructure failure was treated as an ordinary exit")
			}
			for _, test := range f.plan.Tests {
				if r := result515(t, checked.Run, test); r.Status != "not_run" || !strings.Contains(r.Reason, "cannot launch") {
					t.Fatalf("unfinished acceptance lost the launch reason: %+v", r)
				}
			}
			closed484(t, f.runner.runner484)
		})
	}
}

func TestAcceptanceRunningEvidenceCanCompleteButTransportStillVetoes(t *testing.T) {
	for _, transport := range []bool{false, true} {
		f := newFixture515(t)
		f.runner.accept = func(_ context.Context, _ DependencyRequest, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
			e := evidence515(in, "test", "passed", "completed after running")
			e.Records = append(e.Records[:1], RepositoryAcceptanceRecord{Binding: in.Binding, Kind: "test", Status: "running", Reason: "started"}, e.Records[1])
			var err error
			if transport {
				err = errors.New("transport closed abnormally after terminal evidence")
			}
			return e, nil, err
		}
		result, err := f.sandbox.Run(context.Background(), f.candidate, f.plan)
		if result.Passed() == transport || (err != nil) != transport {
			t.Fatal(describe515(result, err))
		}
		if r := result515(t, result, f.plan.Tests[0]); r.Status != "passed" || r.Reason != "completed after running" {
			t.Fatalf("transport erased completed evidence: %+v", r)
		}
		closed484(t, f.runner.runner484)
	}
}
