package plumbing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func TestGoAcceptanceScopeBoundaries(t *testing.T) {
	a := GoRepositoryAcceptanceAdapter{}
	for _, scope := range []string{"./nested/../nested", "./nested//", "./nested/.../other", "./nested...", "./nested.go", "./nested/", "./nest/...", "all", "std"} {
		selection, err := a.Select(context.Background(), goPart527("go", "test", scope, "-run={test}"), goTest527("api/nested/ready_test.go", "TestReady"))
		if err == nil || len(selection.Argv) != 0 {
			t.Fatalf("scope %q yielded authority: %+v, %v", scope, selection, err)
		}
	}
	for _, dir := range []string{"vendor", "testdata", ".hidden", "_hidden"} {
		file := "api/nested/" + dir + "/ready_test.go"
		if _, err := a.Select(context.Background(), goPart527("go", "test", "./nested/...", "-run={test}"), goTest527(file, "TestReady")); err == nil {
			t.Fatalf("recursive scope included skipped descendant %s", dir)
		}
		if _, err := a.Select(context.Background(), goPart527("go", "test", "./nested/"+dir, "-run={test}"), goTest527(file, "TestReady")); err != nil {
			t.Fatalf("explicit scope was refused: %v", err)
		}
	}
}

func TestGoAcceptanceRealDeclarationAndCompileRefusals(t *testing.T) {
	cache := goCache527(t)
	for _, tc := range []struct{ name, source, kind string }{
		{"aliased testing", "package fixture\nimport check \"testing\"\nfunc TestReady(t *check.T) {}\n", ""},
		{"dot testing", "package fixture\nimport . \"testing\"\nfunc TestReady(t *T) {}\n", ""},
		{"malformed source", "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T) {\n", "refused"},
		{"missing candidate API", "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T) { MissingAPI() }\n", "setup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			part, _, in, env := goModule527(t, cache, "api", "example.invalid/regression527", ".", "ready_test.go", tc.source, []string{"go", "test", "-run={test}", "-p", "1", "-trimpath", "-mod=readonly"})
			got, err := discover527(t, part, in, env)
			if tc.kind != "" {
				assertDiscoveryError527(t, got, err, tc.kind)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				assertDiscovery527(t, got, in, "example.invalid/regression527")
			}
		})
	}
}

func TestGoAcceptanceNestedPreparationAuthority(t *testing.T) {
	cache := goCache527(t)
	for _, declared := range []bool{false, true} {
		f := newGoFixture527(t, cache, true, false)
		if declared {
			captured := &fixture515{root: f.root, snapshot: f.snapshot, plan: f.plan, sandbox: f.sandbox}
			recapture515(t, captured, func(c *repository.Config) {
				for i := range c.Parts {
					p := &c.Parts[i]
					p.Inputs = []string{p.Directory + "/go.mod"}
					p.Lockfiles = []string{p.Directory + "/go.sum"}
				}
			})
			f.plan = captured.plan
		}
		result, _ := f.sandbox.Run(context.Background(), f.candidate, f.plan)
		if declared {
			if len(f.adapter.discovered) != 1 || len(f.runner.opens) != 1 {
				t.Fatalf("declared nested module did not reach discovery: %s", result.Summary())
			}
			assertDiscovery527(t, f.adapter.discovered[0], f.adapter.inputs[0], f.module+"/nested")
		} else if len(f.runner.opens)+len(f.runner.installs) != 0 || !strings.Contains(result.Summary(), "Go preparation needs go.mod as a declared input") {
			t.Fatalf("discovery bypassed captured dependency admission: %s", result.Summary())
		}
		if result.Passed() {
			t.Fatal("discovery supplied acceptance completion")
		}
		closed484(t, f.runner.runner484)
	}
}

// This stand-in tests the optional lifecycle protocol, not Go discovery.
type discoveryAdapter527 struct {
	*adapter515
	change func(*RepositoryAcceptanceDiscovery)
	err    error
}

func (a discoveryAdapter527) Discover(_ context.Context, _ repository.Part, in RepositoryAcceptanceInvocation, _ RepositoryCheckEnvironment) (RepositoryAcceptanceDiscovery, error) {
	got := RepositoryAcceptanceDiscovery{Binding: in.Binding, TestName: strings.Split(in.Binding.Selector, "/")[0]}
	got.Binding.Package = "example.invalid/discovered527"
	if a.change != nil {
		a.change(&got)
	}
	in.Argv[0] = "mutated discovery argument"
	return got, a.err
}

func TestRepositoryDiscoveryBindingAndCollector(t *testing.T) {
	for _, mode := range []string{"success", "source", "execution", "identity", "invocation", "declaration", "empty package", "error with result"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture515(t)
			a := discoveryAdapter527{adapter515: f.adapter, change: func(got *RepositoryAcceptanceDiscovery) {
				switch mode {
				case "source":
					got.Binding.SourceDigest = hash468("wrong source")
				case "execution":
					got.Binding.ExecutionDigest = hash468("wrong policy")
				case "identity":
					got.Binding.File = "api/other_test.go"
				case "invocation":
					got.Binding.InvocationID = "stale"
				case "declaration":
					got.TestName = "TestOther"
				case "empty package":
					got.Binding.Package = ""
				}
			}}
			if mode == "error with result" {
				a.err = &RepositoryAcceptanceDiscoveryError{Kind: "setup", Reason: "discovery stopped", Err: errors.New("transport lost")}
			}
			f.sandbox.Repository.Adapters["go"] = a
			result, err := f.sandbox.Run(context.Background(), f.candidate, f.plan)
			if mode == "success" {
				if err != nil || !result.Passed() || len(f.runner.invocations) != len(f.plan.Tests) {
					t.Fatalf("valid discovery did not reach collector: %v\n%s", err, result.Summary())
				}
				for _, in := range f.runner.invocations {
					if in.Binding.Package != "example.invalid/discovered527" || in.Argv[0] != "trusted-fixture" {
						t.Fatalf("collector lost discovered binding or original arguments: %+v", in)
					}
				}
			} else if result.Passed() || len(f.runner.invocations) != 0 || result.TestResults[0].Package != "" || result.TestResults[0].Reason == "" {
				t.Fatalf("invalid discovery reached collector or retained authority: %s", result.Summary())
			}
			closed484(t, f.runner.runner484)
		})
	}
}
