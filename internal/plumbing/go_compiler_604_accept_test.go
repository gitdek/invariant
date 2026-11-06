package plumbing

// These fixtures reuse existing real-process transports without editing them.
// Only dependency image preparation is a stand-in. Package loading, compilation,
// production code and test execution use the installed Go toolchain offline.
import (
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func compilerSource604(body string) string {
	return "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T) { " + body + " }\n"
}

func compilerCase604(pkg string) repositoryCase582 {
	return repositoryCase582{"api", pkg, "TestReady", compilerSource604(`if Future604()!=7 { t.Fatal("API value") }; t.Log("new API executed 604")`)}
}

func compilerFix604(t *testing.T, f *repositoryFixture582) {
	t.Helper()
	for _, file := range f.plan.Files {
		write468(t, f.candidate, file, "package fixture\nconst Value=7\nfunc Future604() int { return 7 }\nfunc OtherFuture604() int { return 8 }\n")
	}
}

// TestResult.evidence is the reporting contract added by this step. Decode it
// through JSON so the old code fails for missing behavior, not a missing field.
func compilerReport604(t *testing.T, result TestResult) RepositoryAcceptanceEvidence {
	t.Helper()
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Evidence *RepositoryAcceptanceEvidence `json:"evidence"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Evidence == nil {
		t.Fatalf("compile report lost bound evidence: %s", data)
	}
	return *report.Evidence
}

func compilerRecord604(t *testing.T, result Result, plan *Plan, test Test, symbol string) RepositoryAcceptanceEvidence {
	t.Helper()
	r := result515(t, result, test)
	if r.Kind != "compile" || r.Status != "failed" || r.Package == "" || r.Reason == "" {
		t.Fatalf("selected proposed API did not receive compile/failed: %+v\n%s", r, result.Summary())
	}
	e := compilerReport604(t, r)
	if !e.Complete || e.ExitCode <= 0 || len(e.Records) != 2 || e.Records[0].Kind != "selection" || e.Records[0].Status != "" || e.Records[1].Kind != "compile" || e.Records[1].Status != "failed" {
		t.Fatalf("not one completed compiler failure distinct from runtime: %+v", e)
	}
	b := e.Records[0].Binding
	if b.InvocationID == "" || b.Part != test.Part || b.Framework != test.Framework || b.File != test.File || b.Selector != test.Selector || b.Package != r.Package || b.SourceDigest != dependencyHash([]byte(plan.Sources[test.File])) || b.ExecutionDigest != plan.Execution.Digest || e.Records[1].Binding != b {
		t.Fatalf("lost full compiler invocation/source/execution binding: %+v", e)
	}
	for _, want := range []string{symbol, path.Base(test.File), test.Selector} {
		if !strings.Contains(e.Records[1].Reason, want) || !strings.Contains(r.Reason, want) {
			t.Fatalf("attribution must explain %q in both evidence and report: %+v / %+v", want, e, r)
		}
	}
	copied := repositoryRoundTrip582(t, result)
	if got := compilerReport604(t, result515(t, copied, test)); !reflect.DeepEqual(e, got) {
		t.Fatal("JSON transport or clipped diagnostics lost compiler evidence")
	}
	return e
}

func compilerBase604(t *testing.T, f *repositoryFixture582) Result {
	t.Helper()
	r, problem, err := f.run(t.Context(), true)
	if err != nil || problem != "" {
		t.Fatalf("attributable completed base failures did not qualify: %v %s\n%s", err, problem, r.Summary())
	}
	if r.Passed() {
		t.Fatal("base failure was reported as candidate success")
	}
	return r
}

func compilerControl604(t *testing.T, cache string) {
	t.Helper()
	f := newRepositoryFixture582(t, cache, []repositoryCase582{compilerCase604("nested")}, false, nil, nil)
	r := compilerBase604(t, f)
	compilerRecord604(t, r, f.plan, f.plan.Tests[0], "Future604")
}

func compilerNoCompletion604(t *testing.T, r Result, test Test) {
	t.Helper()
	got := result515(t, r, test)
	if got.Status == "passed" || got.Status == "failed" || got.Status == "skipped" || got.Reason == "" {
		t.Fatalf("setup or ambiguous compilation invented a completion: %+v\n%s", got, r.Summary())
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var report struct{ Evidence *RepositoryAcceptanceEvidence }
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Evidence != nil {
		for _, record := range report.Evidence.Records {
			if record.Kind == "compile" || record.Kind == "test" && (record.Status == "passed" || record.Status == "failed") {
				t.Fatalf("non-evidence retained a qualifying record: %+v", report.Evidence)
			}
		}
	}
}

func TestGoCompilerProductionCorrection604(t *testing.T) {
	cache := t.TempDir()
	for _, tc := range []struct{ name, source, symbol, production string }{
		{"function", compilerCase604("nested").source, "Future604", "func Future604() int { return 7 }"},
		{"type", compilerSource604(`v:=FutureType604{N:7}; if v.N!=7 {t.Fatal(v)};t.Log("new API executed 604")`), "FutureType604", "type FutureType604 struct { N int }"},
		{"method", compilerSource604(`if (Box604{}).Future604()!=7 {t.Fatal("method")};t.Log("new API executed 604")`), "Future604", "func (Box604) Future604() int { return 7 }"},
		{"external", "package fixture_test\nimport (\"testing\"; api \"example.invalid/api582/nested\")\nfunc TestReady(t *testing.T){if api.Future604()!=7{t.Fatal(\"external\")};t.Log(\"new API executed 604\")}\n", "Future604", "func Future604() int { return 7 }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := compilerCase604("nested")
			c.source = strings.ReplaceAll("//go:build accept604\n\n"+tc.source, "\n", "\r\n")
			f := newRepositoryFixture582(t, cache, []repositoryCase582{c}, false, map[string]string{
				"api/nested/box.go":      "package fixture\ntype Box604 struct{}\n",
				"api/nested/excluded.go": "//go:build !accept604\n\npackage fixture\nvar Broken = NotInCapturedBuild604\n",
			}, func(config *repository.Config) {
				config.Parts[0].Commands["test_one"] = repository.Command{Argv: []string{"go", "test", "./...", "-tags=accept604", "-run", "^{test}$", "-count=1", "-mod=readonly", "-trimpath", "-short", "-shuffle=31", "-parallel=2", "-p=2", "-cpu=1", "-v=false", "-timeout=0"}}
			})
			base := compilerBase604(t, f)
			first := compilerRecord604(t, base, f.plan, f.plan.Tests[0], tc.symbol)
			// A second base check is a fresh compiler invocation, never a cached verdict.
			again := compilerBase604(t, f)
			second := compilerRecord604(t, again, f.plan, f.plan.Tests[0], tc.symbol)
			if first.Records[0].Binding.InvocationID == second.Records[0].Binding.InvocationID {
				t.Fatal("base compilation reused an invocation ID")
			}
			write468(t, f.candidate, "api/nested/value.go", "package fixture\nconst Value=7\n"+tc.production+"\n")
			before := len(f.runner.observed)
			candidate, _, err := f.run(t.Context(), false)
			if err != nil || !candidate.Passed() {
				t.Fatalf("production-only correction did not execute the identical acceptance: %v\n%s", err, candidate.Summary())
			}
			repositoryResult582(t, candidate, f.plan.Tests[0], "passed", "new API executed 604")
			started := false
			for _, observation := range f.runner.observed[before:] {
				if observation.stage == "start" && runtimeTestStart567(observation.argv) {
					started = true
				}
			}
			if !started {
				t.Fatal("candidate used compile success or a cached verdict without a new runtime")
			}
			if f.plan.Sources[c.file()] != c.source {
				t.Fatal("ratified acceptance bytes changed")
			}
			for _, root := range []string{f.root, f.candidate} {
				if _, err := os.Stat(filepath.Join(root, c.file())); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("execution changed a caller checkout instead of private staging")
				}
			}
		})
	}
}

func TestGoCompilerIndependentSelectors604(t *testing.T) {
	cache := t.TempDir()
	// Two separate direct API uses in one compiled file each need their own
	// compiler attempt and attribution, alongside an actual runtime assertion.
	source := compilerCase604("nested").source + "func TestOther(t *testing.T) { if OtherFuture604()!=8 {t.Fatal(\"other API\")} }\n"
	cases := []repositoryCase582{
		{"api", "earlier", "TestReady", compilerSource604(`t.Fatal("earlier runtime 604")`)},
		{"api", "nested", "TestReady", source},
		{"api", "nested", "TestOther", source},
	}
	f := newRepositoryFixture582(t, cache, cases, true, nil, nil)
	base := compilerBase604(t, f)
	repositoryResult582(t, base, f.plan.Tests[0], "failed", "earlier runtime 604")
	one := compilerRecord604(t, base, f.plan, f.plan.Tests[1], "Future604")
	two := compilerRecord604(t, base, f.plan, f.plan.Tests[2], "OtherFuture604")
	if one.Records[0].Binding.InvocationID == two.Records[0].Binding.InvocationID {
		t.Fatal("two selectors shared a compiler invocation")
	}
	// A selector with no API use cannot inherit a neighboring compilation error.
	for _, neighbor := range []string{"same file", "same package", "other package", "nested module", "other part"} {
		t.Run(neighbor, func(t *testing.T) {
			first := compilerCase604("nested")
			second := repositoryCase582{"api", "nested", "TestNeighbor", ""}
			files := map[string]string{}
			switch neighbor {
			case "same file":
				first.source += "func TestNeighbor(t *testing.T) {}\n"
				second.source = first.source
			case "same package":
				// The selected file is pinned, but the only error is in another
				// included file in the same ordinary test package.
				first.source = compilerSource604(`t.Log("healthy selected function")`)
				files["api/nested/neighbor_test.go"] = "package fixture\nimport \"testing\"\nfunc TestNeighbor(t *testing.T){MissingNeighbor604()}\n"
			case "other package":
				second.pkg = "elsewhere"
			case "nested module":
				second.part = "api/child"
			case "other part":
				second.part = "web"
			}
			selected := []repositoryCase582{first}
			if neighbor != "same package" {
				if second.source == "" {
					second.selector, second.source = "TestReady", compilerSource604(`t.Log("healthy neighbor 604")`)
				}
				selected = append(selected, second)
			}
			g := newRepositoryFixture582(t, cache, selected, false, files, nil)
			r, problem, _ := g.run(t.Context(), true)
			if problem == "" {
				t.Fatal("a selector inherited another selector's failure")
			}
			if neighbor == "same package" {
				compilerNoCompletion604(t, r, g.plan.Tests[0])
				return
			}
			compilerRecord604(t, r, g.plan, g.plan.Tests[0], "Future604")
			if neighbor == "same file" {
				compilerNoCompletion604(t, r, g.plan.Tests[1])
			} else {
				repositoryResult582(t, r, g.plan.Tests[1], "passed", "healthy neighbor 604")
			}
		})
	}
	for _, tc := range []struct{ name, selector, source string }{
		{"shared helper", "TestReady", compilerSource604(`helper604(t)`) + "func helper604(t *testing.T){Future604()}\nfunc TestNeighbor(t *testing.T){helper604(t)}\n"},
		{"package initializer", "TestReady", compilerSource604(`t.Log("not attributable")`) + "var Shared604 = Future604()\n"},
		{"ambiguous child", "TestReady/left", compilerSource604(`for _, name := range []string{"left","right"} {t.Run(name,func(t *testing.T){Future604()})}`)},
		{"missing child", "TestReady/absent", compilerSource604(`t.Run("present",func(t *testing.T){Future604()})`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newRepositoryFixture582(t, cache, []repositoryCase582{{"api", "nested", tc.selector, tc.source}}, false, nil, nil)
			r, problem, _ := g.run(t.Context(), true)
			if problem == "" {
				t.Fatal("ambiguous attribution qualified base")
			}
			compilerNoCompletion604(t, r, g.plan.Tests[0])
		})
	}
}
