package plumbing

import (
	"strings"
	"testing"
)

func TestGoCompilerCompositeLiteralBoundary604(t *testing.T) {
	cache := t.TempDir()
	for _, tc := range []struct{ name, source, symbol, production string }{
		{"missing local type", compilerSource604(`_ = FutureType604{}; t.Log("literal boundary executed 604")`), "FutureType604", "type FutureType604 struct{}"},
		{"missing package type", "package fixture_test\nimport (\"testing\"; api \"example.invalid/api582/nested\")\nfunc TestReady(t *testing.T){_ = api.FutureType604{}; t.Log(\"literal boundary executed 604\")}\n", "FutureType604", "type FutureType604 struct{}"},
		{"missing method expression", compilerSource604(`Box604.Future604(Box604{}); t.Log("literal boundary executed 604")`), "Future604", "func (Box604) Future604() {}"},
		{"nested type", compilerSource604(`_ = Box604.Future604{}`), "", ""},
		{"value as type qualifier", compilerSource604(`box := Box604{}; _ = box.Future604{}`), "", ""},
		{"nested type beside missing call", compilerSource604(`Future604(); _ = Box604.Future604{}`), "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := repositoryCase582{"api", "nested", "TestReady", tc.source}
			f := newRepositoryFixture582(t, cache, []repositoryCase582{c}, false, map[string]string{
				"api/nested/box.go": "package fixture\ntype Box604 struct{}\n",
			}, nil)
			if tc.symbol == "" {
				r, problem, _ := f.run(t.Context(), true)
				if problem == "" || r.Passed() {
					t.Fatal("invalid literal type supplied compiler authority")
				}
				compilerNoCompletion604(t, r, f.plan.Tests[0])
				reason := result515(t, r, f.plan.Tests[0]).Reason
				if !strings.Contains(reason, "type error outside attributable production API uses") || !strings.Contains(reason, "not a type") {
					t.Fatalf("literal was not refused by type analysis: %s", reason)
				}
				repositoryRoundTrip582(t, r)
				return
			}
			base := compilerBase604(t, f)
			compilerRecord604(t, base, f.plan, f.plan.Tests[0], tc.symbol)
			write468(t, f.candidate, "api/nested/value.go", "package fixture\nconst Value=7\n"+tc.production+"\n")
			candidate, _, err := f.run(t.Context(), false)
			if err != nil || !candidate.Passed() {
				t.Fatalf("valid API correction did not execute unchanged acceptance: %v\n%s", err, candidate.Summary())
			}
			repositoryResult582(t, candidate, f.plan.Tests[0], "passed", "literal boundary executed 604")
		})
	}
}
