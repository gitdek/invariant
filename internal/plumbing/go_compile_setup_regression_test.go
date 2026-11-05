package plumbing

import (
	"strings"
	"testing"
)

func TestGoCompilerTestPackageSetup604(t *testing.T) {
	cache := t.TempDir()
	main := "func TestMain(m *testing.M) { m.Run() }\n"
	for _, external := range []bool{false, true} {
		name := "internal acceptance"
		if external {
			name = "external acceptance"
		}
		t.Run(name, func(t *testing.T) {
			c := compilerCase604("nested")
			files := map[string]string{}
			if external {
				c.source = "package fixture_test\nimport (\"testing\"; api \"example.invalid/api582/nested\")\nfunc TestReady(t *testing.T) { api.Future604() }\n"
				files["api/nested/main_test.go"] = "package fixture\nimport \"testing\"\n" + main
			} else {
				files["api/nested/main_test.go"] = "package fixture_test\nimport \"testing\"\n" + main
			}
			// Either package may own the one main. Its presence alone must not
			// hide a real API failure or prevent a fresh corrected runtime.
			control := newRepositoryFixture582(t, cache, []repositoryCase582{c}, false, files, nil)
			base := compilerBase604(t, control)
			compilerRecord604(t, base, control.plan, control.plan.Tests[0], "Future604")
			compilerFix604(t, control)
			candidate, _, err := control.run(t.Context(), false)
			if err != nil || !candidate.Passed() {
				t.Fatalf("one TestMain prevented fresh runtime: %v\n%s", err, candidate.Summary())
			}
			repositoryResult582(t, candidate, control.plan.Tests[0], "passed", "")

			// Each main type-checks separately, but Go refuses the combined
			// setup before compiling the acceptance's missing production API.
			c.source += main
			earlier := repositoryCase582{"api", "earlier", "TestReady", compilerSource604(`t.Fatal("earlier setup control 604")`)}
			f := newRepositoryFixture582(t, cache, []repositoryCase582{earlier, c}, false, files, nil)
			r, problem, _ := f.run(t.Context(), true)
			if problem == "" || r.Passed() {
				t.Fatal("multiple TestMain declarations supplied compiler authority")
			}
			repositoryResult582(t, r, f.plan.Tests[0], "failed", "earlier setup control 604")
			compilerNoCompletion604(t, r, f.plan.Tests[1])
			if reason := result515(t, r, f.plan.Tests[1]).Reason; !strings.Contains(reason, "test package setup failed") {
				t.Fatalf("combined test setup was not validated: %s", reason)
			}
			if !strings.Contains(r.Output, "multiple definitions of TestMain") {
				t.Fatalf("fixture did not reach Go's setup refusal: %s", r.Output)
			}
			repositoryRoundTrip582(t, r)
		})
	}
}

func TestGoCompilerPointerInterfaceReceiver604(t *testing.T) {
	cache := t.TempDir()
	// Pointers to concrete production types still support missing methods.
	c := repositoryCase582{"api", "nested", "TestReady", compilerSource604(`b := Box604{}; (&b).Future604(); t.Log("pointer method executed 604")`)}
	control := newRepositoryFixture582(t, cache, []repositoryCase582{c}, false, map[string]string{
		"api/nested/box.go": "package fixture\ntype Box604 struct{}\n",
	}, nil)
	base := compilerBase604(t, control)
	compilerRecord604(t, base, control.plan, control.plan.Tests[0], "Future604")
	write468(t, control.candidate, "api/nested/value.go", "package fixture\nconst Value=7\nfunc (*Box604) Future604() {}\n")
	candidate, _, err := control.run(t.Context(), false)
	if err != nil || !candidate.Passed() {
		t.Fatalf("concrete receiver correction did not execute: %v\n%s", err, candidate.Summary())
	}
	repositoryResult582(t, candidate, control.plan.Tests[0], "passed", "pointer method executed 604")

	for _, tc := range []struct{ name, source string }{
		{"interface pointer", compilerSource604(`var b Box604; (&b).Future604()`)},
		{"interface alias pointer", compilerSource604(`var b BoxAlias604; (&b).Future604()`)},
		{"pointer alias", compilerSource604(`var b BoxPointer604; b.Future604()`)},
		{"beside missing API", compilerSource604(`OtherFuture604(); var b Box604; (&b).Future604()`)},
		{"external interface pointer", "package fixture_test\nimport (\"testing\"; api \"example.invalid/api582/nested\")\nfunc TestReady(t *testing.T) { var b api.Box604; (&b).Future604() }\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			earlier := repositoryCase582{"api", "earlier", "TestReady", compilerSource604(`t.Fatal("earlier receiver control 604")`)}
			bad := repositoryCase582{"api", "nested", "TestReady", tc.source}
			f := newRepositoryFixture582(t, cache, []repositoryCase582{earlier, bad}, false, map[string]string{
				"api/nested/box.go": "package fixture\ntype Box604 interface { Future604() }\ntype BoxAlias604 = Box604\ntype BoxPointer604 = *Box604\n",
			}, nil)
			r, problem, _ := f.run(t.Context(), true)
			if problem == "" || r.Passed() {
				t.Fatal("invalid pointer-to-interface receiver supplied compiler authority")
			}
			repositoryResult582(t, r, f.plan.Tests[0], "failed", "earlier receiver control 604")
			compilerNoCompletion604(t, r, f.plan.Tests[1])
			if reason := result515(t, r, f.plan.Tests[1]).Reason; !strings.Contains(reason, "type error outside attributable production API uses") || !strings.Contains(reason, "Future604") {
				t.Fatalf("receiver was not refused by type analysis: %s", reason)
			}
			repositoryRoundTrip582(t, r)
		})
	}
}
