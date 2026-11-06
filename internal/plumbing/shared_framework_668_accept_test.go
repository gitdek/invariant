package plumbing

// lookupAcceptanceFramework(name) returns (definition, found) for the four
// explicit native frameworks; the empty legacy spelling is not a fifth one.
// Definitions provide sourceLanguage(file), validateFileSelector(Test),
// validateOwner(repository.Part, Test), checkDeclarations(file, source, tests)
// and defaultAdapter(). These are the shared implementation consumed by the
// existing presentation, validation and adapter entry points. validateOwner
// checks language, applicable test_one and pytest's part-relative file; the
// snapshot still determines deepest ownership. Neither structural operation
// implies supported argv, discovery, compilation or execution evidence.

import (
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func TestSharedFrameworkDefinitions668(t *testing.T) {
	for _, name := range []string{"", "unittest", "mocha", "Go", " pytest"} {
		if _, ok := lookupAcceptanceFramework(name); ok {
			t.Fatalf("unknown framework %q admitted", name)
		}
	}
	for _, tc := range []struct {
		framework, language, file, selector, source string
	}{
		{"go", "go", "app/nested/ready_accept_test.go", "TestReady/case.one", "package fixture\nimport check \"testing\"\nfunc TestReady(t *check.T) {}\n"},
		{"pytest", "python", "app/nested/test_ready.py", "nested/test_ready.py::TestGroup::test_ready[a b;$(literal).+|x]", "# UTF-8: café\r\n# dynamic declaration, discovered by pytest"},
		{"vitest", "typescript", "app/nested/ready.test.ts", "outer ready [one].+ $(literal); | ^ $", "// UTF-8: café\r\n// discovered by the prepared framework"},
		{"jest", "typescript", "app/nested/ready.spec.tsx", "outer ready [one].+ $(literal); | ^ $", "// UTF-8: café\r\n// discovered by the prepared framework"},
	} {
		t.Run(tc.framework, func(t *testing.T) {
			definition, ok := lookupAcceptanceFramework(tc.framework)
			if !ok {
				t.Fatal("missing shared definition")
			}
			test := Test{Name: "same display label", Part: "app", Framework: tc.framework, File: tc.file, Selector: tc.selector, Says: "The selected behavior holds."}
			part := n503Part(tc.language, "app") // Opaque command, structurally valid only.
			if got := definition.sourceLanguage(tc.file); got != tc.language {
				t.Fatalf("display=%s", got)
			}
			if err := definition.validateFileSelector(test); err != nil {
				t.Fatal(err)
			}
			if err := definition.validateOwner(part, test); err != nil {
				t.Fatal(err)
			}
			if err := definition.checkDeclarations(tc.file, tc.source, []Test{test}); err != nil {
				t.Fatal(err)
			}
			if err := definition.checkDeclarations(tc.file, tc.source+string([]byte{0xff}), []Test{test}); err == nil {
				t.Fatal("invalid UTF-8 source admitted")
			}
			bad := test
			bad.File = "app/readme.txt"
			if err := definition.validateFileSelector(bad); err == nil {
				t.Fatal("wrong source convention admitted")
			}
			bad = test
			bad.Selector = ""
			if err := bad.validateNative(); err == nil {
				t.Fatal("empty selector admitted")
			}
			bad.Framework = ""
			if err := bad.validateNative(); err == nil {
				t.Fatal("partial native identity downgraded")
			}
			wrongLanguage := part
			wrongLanguage.Language = "wrong"
			if err := definition.validateOwner(wrongLanguage, test); err == nil {
				t.Fatal("wrong owner language admitted")
			}
			inapplicable := n503Part(tc.language, "app")
			inapplicable.Commands["test_one"] = repository.Command{NotApplicable: "No individual tests."}
			if err := definition.validateOwner(inapplicable, test); err == nil {
				t.Fatal("inapplicable individual command admitted")
			}
			inapplicable.Commands["test_one"] = repository.Command{}
			if err := definition.validateOwner(inapplicable, test); err == nil {
				t.Fatal("missing individual command admitted")
			}
			if tc.framework == "pytest" {
				bad = test
				bad.Selector = "other.py::test_ready"
				if err := definition.validateOwner(part, bad); err == nil {
					t.Fatal("pytest file prefix mismatch admitted")
				}
				rootPart := part
				rootPart.Directory = "."
				bad.Selector = test.File + "::test_ready"
				if err := definition.validateOwner(rootPart, bad); err != nil {
					t.Fatal("root part rejected:", err)
				}
				for _, selector := range []string{"nested/test_ready.py", "nested/test_ready.py::TestGroup"} {
					bad.Selector = selector
					if err := definition.validateFileSelector(bad); err == nil {
						t.Fatal("file or class selector admitted")
					}
				}
			}
			if tc.framework == "go" {
				bad = test
				bad.Selector = "TestReady//empty"
				if err := definition.validateFileSelector(bad); err == nil {
					t.Fatal("empty Go subtest segment admitted")
				}
				if err := definition.checkDeclarations(tc.file, "package fixture\nfunc TestReady() {}\n", []Test{test}); err == nil {
					t.Fatal("native Go signature was not checked")
				}
			}
			root, _, snapshot := fixture484(t, []repository.Part{part}, nil)
			p := n503Read(t, n503Plan(t, tc.framework, "app", tc.file, tc.selector, tc.source), snapshot)
			if p.Sources[tc.file] != tc.source || p.SourceLanguage(tc.file) != tc.language {
				t.Fatal("caller changed verbatim source or display")
			}
			if err := p.ValidateRepository(t.Context(), snapshot); err != nil {
				t.Fatal(err)
			}
			_, _, mismatched := fixture484(t, []repository.Part{n503Part(map[string]string{"go": "python", "python": "typescript", "typescript": "go"}[tc.language], "app")}, nil)
			if _, err := ReadRepositoryDraft(t.Context(), n503Draft(t, p), mismatched); err == nil {
				t.Fatal("repository binding bypassed framework language")
			}
			if tc.framework == "pytest" {
				other := cloneRepositoryPlan(p)
				other.Tests[0].Selector = "other.py::test_ready"
				if _, err := ReadRepositoryDraft(t.Context(), n503Draft(t, other), snapshot); err == nil {
					t.Fatal("repository binding bypassed pytest relative file")
				}
			}
			if tc.language == "typescript" {
				for _, ext := range []string{"js", "jsx", "mjs", "cjs", "ts", "tsx", "mts", "cts"} {
					want := "typescript"
					if slices.Contains([]string{"js", "jsx", "mjs", "cjs"}, ext) {
						want = "javascript"
					}
					for _, file := range []string{"app/nested/ready.test." + ext, "app/nested/ready.spec." + ext, "app/__tests__/ready." + ext} {
						selected := test
						selected.File = file
						if err := definition.validateFileSelector(selected); err != nil {
							t.Fatal(err)
						}
						if got := definition.sourceLanguage(file); got != want {
							t.Errorf("%s: display=%s", file, got)
						}
						view := &Plan{Tests: []Test{selected}}
						if view.SourceLanguage(file) != want {
							t.Fatal("presentation bypassed shared extension rule")
						}
					}
				}
			}
			// Default lookup preserves concrete adapters and host overrides. A
			// valid schema with an unsupported invocation still refuses cheaply.
			adapter := definition.defaultAdapter()
			builtin := (Sandbox{Repository: &RepositoryChecks{}}).repositoryAdapter(tc.framework)
			if adapter == nil || reflect.TypeOf(adapter) != reflect.TypeOf(builtin) {
				t.Fatal("default lookup disagrees")
			}
			if _, err := adapter.Select(t.Context(), part, test); err == nil {
				t.Fatal("opaque wrapper became executable")
			}
			runner := newRunner484(t)
			checked, err := Check(t.Context(), n503Draft(t, p), root, sandbox484(t, root, snapshot, runner))
			if err != nil || checked == nil || checked.Problem == "" {
				t.Fatalf("unsupported wrapper qualified: %+v %v", checked, err)
			}
			if len(runner.installs)+len(runner.inspections)+len(runner.opens) != 0 {
				t.Fatal("unsupported command reached preparation")
			}
			override := &adapter515{}
			s := Sandbox{Repository: &RepositoryChecks{Adapters: map[string]RepositoryAcceptanceAdapter{tc.framework: override}}}
			if s.repositoryAdapter(tc.framework) != override {
				t.Fatal("host override replaced")
			}

			var argv, want []string
			switch tc.framework {
			case "go":
				argv = []string{"go", "test", "./...", "-run", "{test}", "-short"}
				want = []string{"go", "test", "./nested", "-run", "^TestReady$/^case\\.one$", "-short", "-count=1"}
			case "pytest":
				argv = []string{"python3", "-m", "pytest", "--tb=short", "{test}"}
				want = []string{"python3", "-m", "pytest", "--tb=short", tc.selector}
			default:
				argv = []string{tc.framework}
				if tc.framework == "vitest" {
					argv = append(argv, "run")
				} else {
					argv = append(argv, "--runInBand")
				}
				argv = append(argv, "--config", tc.framework+".config.cjs", "-t", "{test}", "--testTimeout=5000")
				want = slices.Clone(argv)
				for i := range want {
					if want[i] == "{test}" {
						want[i] = "^" + regexp.QuoteMeta(tc.selector) + "$"
					}
				}
				if tc.framework == "jest" {
					want = append(want, "--runTestsByPath")
				}
				want = append(want, strings.TrimPrefix(tc.file, "app/"))
			}
			part.Commands["test_one"] = repository.Command{Argv: slices.Clone(argv)}
			got, err := adapter.Select(t.Context(), part, test)
			if err != nil || !reflect.DeepEqual(got.Argv, want) {
				t.Fatalf("literal selection/flags changed: %q want %q (%v)", got.Argv, want, err)
			}
			got.Argv[0] = "caller mutation"
			if !reflect.DeepEqual(part.Commands["test_one"].Argv, argv) {
				t.Fatal("adapter mutated policy")
			}
		})
	}
	legacy := &Plan{Tests: []Test{{Name: "TestReady", File: "app/ready_accept_test.go"}}}
	if legacy.SourceLanguage("app/ready_accept_test.go") != "go" || legacy.SourceLanguage("unknown.file") != "go" {
		t.Fatal("legacy display fallback changed")
	}
}
