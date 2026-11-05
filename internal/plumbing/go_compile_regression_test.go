package plumbing

import (
	"strings"
	"testing"
)

func TestGoCompilerConservativeSources(t *testing.T) {
	cache := t.TempDir()
	for _, tc := range []struct {
		name, source string
		files        map[string]string
	}{
		{"testing helper", compilerSource604(`MissingHelper604(t)`), nil},
		{"neighbor source", compilerSource604(`Future604()`), map[string]string{"api/nested/neighbor_test.go": "package fixture\nimport \"testing\"\nfunc TestNeighbor(t *testing.T){OtherFuture604()}\n"}},
		{"invalid testing signature", compilerSource604(`Future604()`) + "func TestNeighbor(n int) {}\n", nil},
		{"test-only receiver", compilerSource604(`(TestBox604{}).Future604()`) + "type TestBox604 struct{}\n", nil},
		{"dependency API", strings.Replace(compilerSource604(`Future604(); strings.Future604()`), `import "testing"`, `import ("testing"; "strings")`, 1), nil},
		{"later local binding", compilerSource604(`Future604(); Future604 := func() {}; Future604()`), nil},
		{"child callback", compilerSource604(`t.Run("child", func(t *testing.T){Future604()})`), nil},
		{"missing helper body", compilerSource604(`Future604()`) + "func another604()\n", nil},
		{"ambiguous member", compilerSource604(`(Box604{}).Future604()`), map[string]string{"api/nested/box.go": "package fixture\ntype Left604 struct{Future604 func()}\ntype Right604 struct{Future604 func()}\ntype Box604 struct{Left604;Right604}\n"}},
		{"unaddressable receiver", compilerSource604(`(Box604{}).Future604()`), map[string]string{"api/nested/box.go": "package fixture\ntype Box604 struct{}\nfunc (*Box604) Future604() {}\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRepositoryFixture582(t, cache, []repositoryCase582{{"api", "nested", "TestReady", tc.source}}, false, tc.files, nil)
			r, problem, _ := f.run(t.Context(), true)
			if problem == "" {
				t.Fatal("unrelated or ambiguous source supplied compiler authority")
			}
			compilerNoCompletion604(t, r, f.plan.Tests[0])
		})
	}
}

func TestGoCompilerEvidenceNeedsFailingExitWithoutRuntime(t *testing.T) {
	binding := RepositoryAcceptanceBinding{InvocationID: "fresh", Part: "api", Framework: "go", File: "api/accept_test.go", Selector: "TestReady", Package: "example.invalid/api", SourceDigest: dependencyHash([]byte("source")), ExecutionDigest: dependencyHash([]byte("execution"))}
	for _, mode := range []string{"zero exit", "running", "no attribution"} {
		t.Run(mode, func(t *testing.T) {
			e := RepositoryAcceptanceEvidence{Complete: true, ExitCode: 1, Records: []RepositoryAcceptanceRecord{
				{Binding: binding, Kind: "selection"}, {Binding: binding, Kind: "compile", Status: "failed", Reason: "TestReady directly uses Future604 in accept_test.go"},
			}}
			switch mode {
			case "zero exit":
				e.ExitCode = 0
			case "running":
				e.Records = append(e.Records[:1], RepositoryAcceptanceRecord{Binding: binding, Kind: "test", Status: "running"}, e.Records[1])
			case "no attribution":
				e.Records[1].Reason = ""
			}
			got, err := repositoryEvidence(binding, e, TestResult{Phase: "accept"})
			if err == nil || got.Status == "failed" || got.Evidence != nil {
				t.Fatalf("invalid compiler completion retained evidence: %+v %v", got, err)
			}
		})
	}
}
