package plumbing

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

func TestGoDiscoveryDeclarationsAndCompilation(t *testing.T) {
	cache := goCache527(t)
	for _, tc := range []struct{ name, source, kind string }{
		{"renamed import", "package fixture\nimport check \"testing\"\nfunc TestReady(t *check.T) {}\n", ""},
		{"dot import", "package fixture\nimport . \"testing\"\nfunc TestReady(*T) {}\n", ""},
		{"malformed source", "package fixture\nfunc TestReady(\n", "refused"},
		{"multiple parameters", "package fixture\nimport \"testing\"\nfunc TestReady(t, u *testing.T) {}\n", "refused"},
		{"compiler failure", "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T) { missing() }\n", "setup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			part, _, in, env := goModule527(t, cache, "api", "example.invalid/regression527", ".", "ready_test.go", tc.source, []string{"go", "test", "-run={test}"})
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

type goCollectorRunner527 struct {
	*goRunner527
	inputs []RepositoryAcceptanceInvocation
}

func (r *goCollectorRunner527) OpenChecks(ctx context.Context, q DependencyRequest) (RepositoryCheckEnvironment, error) {
	env, err := r.goRunner527.OpenChecks(ctx, q)
	if err != nil {
		return env, err
	}
	return &goCollectorEnvironment527{env.(RepositoryAcceptanceFileEnvironment), r}, nil
}

type goCollectorEnvironment527 struct {
	RepositoryAcceptanceFileEnvironment
	runner *goCollectorRunner527
}

func (e *goCollectorEnvironment527) RunAcceptance(_ context.Context, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
	e.runner.inputs = append(e.runner.inputs, in)
	return RepositoryAcceptanceEvidence{Complete: true, Records: []RepositoryAcceptanceRecord{
		{Binding: in.Binding, Kind: "selection"},
		{Binding: in.Binding, Kind: "test", Status: "passed", Reason: "trusted collector fixture"},
	}}, nil, nil
}

type changedGoDiscovery527 struct {
	GoRepositoryAcceptanceAdapter
	change func(*RepositoryAcceptanceDiscovery)
}

func (a changedGoDiscovery527) Discover(ctx context.Context, part repository.Part, in RepositoryAcceptanceInvocation, env RepositoryCheckEnvironment) (RepositoryAcceptanceDiscovery, error) {
	got, err := a.GoRepositoryAcceptanceAdapter.Discover(ctx, part, in, env)
	if err == nil {
		a.change(&got)
	}
	// Neither a collector's arguments nor captured policy may alias this input.
	in.Argv[0] = "changed by discovery"
	return got, err
}

func TestGoDiscoveryCollectorBinding(t *testing.T) {
	cache := goCache527(t)
	for _, field := range []string{"", "invocation", "source", "execution", "file", "selector", "owner", "framework", "test name", "empty package"} {
		t.Run(field, func(t *testing.T) {
			f := newGoFixture527(t, cache, false, false)
			runner := &goCollectorRunner527{goRunner527: f.runner}
			f.sandbox.Repository.Dependencies.Runner = runner
			f.sandbox.Repository.Adapters["go"] = changedGoDiscovery527{change: func(got *RepositoryAcceptanceDiscovery) {
				switch field {
				case "invocation":
					got.Binding.InvocationID = "stale"
				case "source":
					got.Binding.SourceDigest = hash468("changed")
				case "execution":
					got.Binding.ExecutionDigest = hash468("changed")
				case "file":
					got.Binding.File = "other_test.go"
				case "selector":
					got.Binding.Selector = "TestOther"
				case "owner":
					got.Binding.Part = "other"
				case "framework":
					got.Binding.Framework = "pytest"
				case "test name":
					got.TestName = "TestOther"
				case "empty package":
					got.Binding.Package = ""
				}
			}}
			result, _ := f.sandbox.Run(context.Background(), f.candidate, f.plan)
			if field != "" {
				if result.Passed() || len(runner.inputs) != 0 || result.TestResults[0].Package != "" || result.TestResults[0].Reason == "" {
					t.Fatalf("invalid discovery reached collector: %s", result.Summary())
				}
			} else {
				if !result.Passed() || len(runner.inputs) != 1 {
					t.Fatal(result.Summary())
				}
				in := runner.inputs[0]
				if in.Binding.Package != f.module+"/nested" || in.Source != f.plan.Sources[in.Binding.File] || in.Argv[0] != "go" || !slices.Contains(in.Argv, "./nested") {
					t.Fatalf("collector lost discovered binding or literal arguments: %+v", in)
				}
			}
			closed484(t, f.runner.runner484)
		})
	}
}

func TestGoDiscoveryRejectsMalformedSourceArchives(t *testing.T) {
	_, archiveFile := sourceDocker527(t, "unused")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	env, err := (&DockerDependencyRunner{}).OpenChecks(ctx, request484(t, "go"))
	if err != nil {
		t.Fatal(err)
	}
	defer close484(t, env)
	for _, names := range [][]string{{"other_test.go"}, {"../ready_test.go"}, {"ready_test.go", "extra"}} {
		var buf bytes.Buffer
		archive := tar.NewWriter(&buf)
		for _, name := range names {
			if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
		}
		if err := archive.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(archiveFile, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := env.(RepositoryAcceptanceFileEnvironment).ReadFile(ctx, "ready_test.go"); err == nil {
			t.Fatalf("unsafe archive accepted: %q", names)
		}
	}
}
