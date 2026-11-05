package plumbing

import (
	"maps"
	"strings"
	"testing"
)

// go test compiles external tests against the package with its internal
// tests' declarations, so a test-only export is not a missing production API.
func TestGoCompilerExternalTestsSeeInternalTestDeclarations604(t *testing.T) {
	cache := t.TempDir()
	const header = "package fixture_test\nimport (\"testing\"; api \"example.invalid/api582/nested\")\n"
	const ready = "func TestReady(t *testing.T){if api.Future604()!=7{t.Fatal(\"external\")};t.Log(\"new API executed 604\")}\n"
	files := map[string]string{
		"api/nested/box.go":         "package fixture\ntype Box604 struct{}\n",
		"api/nested/export_test.go": "package fixture\nimport \"testing\"\nfunc ExportedForTest604() int { return 7 }\nfunc HelperForTest604(t *testing.T) int { return 7 }\nfunc (Box604) ForTest604() int { return 7 }\ntype TestBox604 struct{}\n",
	}
	for _, tc := range []struct{ name, neighbor string }{
		{"test-only function", `api.ExportedForTest604()`},
		{"test-only helper", `api.HelperForTest604(t)`},
		{"test-only method", `(api.Box604{}).ForTest604()`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := header + ready + "func TestNeighbor(t *testing.T){if " + tc.neighbor + "!=7{t.Fatal(\"test-only export\")}}\n"
			f := newRepositoryFixture582(t, cache, []repositoryCase582{{"api", "nested", "TestReady", source}, {"api", "nested", "TestNeighbor", source}}, false, files, nil)
			r, problem, _ := f.run(t.Context(), true)
			if problem == "" {
				t.Fatal("a test-only export supplied compiler authority")
			}
			compilerRecord604(t, r, f.plan, f.plan.Tests[0], "Future604")
			compilerNoCompletion604(t, r, f.plan.Tests[1])
		})
	}
	t.Run("dependency's copy", func(t *testing.T) {
		// go test recompiles apitest against the package with its test
		// declarations; the analyzer has only apitest's own copy, so it refuses.
		source := strings.Replace(header, `api "example.invalid/api582/nested")`, `api "example.invalid/api582/nested"; "example.invalid/api582/nested/apitest")`, 1) + ready + "func TestNeighbor(t *testing.T){if apitest.New().ForTest604()!=7{t.Fatal(\"test-only export\")}}\n"
		files := maps.Clone(files)
		files["api/nested/apitest/apitest.go"] = "package apitest\nimport api \"example.invalid/api582/nested\"\nfunc New() api.Box604 { return api.Box604{} }\n"
		f := newRepositoryFixture582(t, cache, []repositoryCase582{{"api", "nested", "TestReady", source}, {"api", "nested", "TestNeighbor", source}}, false, files, nil)
		r, problem, _ := f.run(t.Context(), true)
		if problem == "" {
			t.Fatal("a dependency's copy of the package supplied compiler authority")
		}
		compilerNoCompletion604(t, r, f.plan.Tests[1])
	})
	t.Run("test-only type member", func(t *testing.T) {
		source := header + "func TestReady(t *testing.T){(api.TestBox604{}).Future604()}\n"
		f := newRepositoryFixture582(t, cache, []repositoryCase582{{"api", "nested", "TestReady", source}}, false, files, nil)
		r, problem, _ := f.run(t.Context(), true)
		if problem == "" {
			t.Fatal("a test-only type's member supplied compiler authority")
		}
		compilerNoCompletion604(t, r, f.plan.Tests[0])
	})
}
