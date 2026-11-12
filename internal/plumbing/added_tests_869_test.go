package plumbing

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A build may add test files, and what it adds can't decide how the tests
// that judge it run (#869). The plan's acceptance tests and a package's
// existing tests run from a binary built without the added files; only the
// tests those files declare run from the one that holds them. These run the
// embedded runner and the real Go compiler through the local Docker
// stand-in, with the original checkout beside the candidate, as a build has.
func added869(t *testing.T) (*s397Fixture, func() Result) {
	t.Helper()
	f := s397New(t)
	original := t.TempDir()
	run := func() Result {
		t.Helper()
		s := Sandbox{Image: "local-fixture", ModCache: f.mod, Cache: f.cache, Timeout: 90 * time.Second, ShardTimeout: 30 * time.Second, Original: original}
		r, err := s.Run(context.Background(), f.root, f.plan)
		if err != nil {
			t.Fatalf("the sandbox must deliver a result: %v\n%s", err, r.Output)
		}
		return r
	}
	// The original is the checkout as it is now, before the build's files.
	t.Cleanup(func() {})
	snapshot := func() {
		t.Helper()
		if err := os.CopyFS(original, os.DirFS(f.root)); err != nil {
			t.Fatal(err)
		}
	}
	f.pkg(t, "foo", "")
	s397Write(t, filepath.Join(f.root, "foo", "code.go"), "package foo\n\nvar mode = \"old\"\n\n// Mode is how foo behaves.\nfunc Mode() string { return mode }\n", 0o644)
	f.accept(t, "foo", "TestModeIsNew", "package foo\n\nimport \"testing\"\n\nfunc TestModeIsNew(t *testing.T) {\n\tif Mode() != \"new\" {\n\t\tt.Fatalf(\"mode is %q\", Mode())\n\t}\n}\n")
	f.pkg(t, "bar", "package bar\n\nimport \"testing\"\n\nfunc TestDouble(t *testing.T) {\n\tif Double(2) != 4 {\n\t\tt.Fatal(\"double is wrong\")\n\t}\n}\n")
	s397Write(t, filepath.Join(f.root, "bar", "code.go"), "package bar\n\n// Double doubles.\nfunc Double(n int) int { return n * 2 }\n", 0o644)
	snapshot()
	// The original holds no acceptance file: the plan's files are the plan's.
	if err := os.Remove(filepath.Join(original, "foo", "pinned_accept_test.go")); err != nil {
		t.Fatal(err)
	}
	return f, run
}

func TestAddedTestFilesCantPassAcceptance869(t *testing.T) {
	const starts = "package foo\n\n// The test binary sets the mode as it starts, and production is unchanged.\nvar _ = func() bool { mode = \"new\"; return true }()\n"
	for name, added := range map[string]string{
		"an initializer that runs as the binary starts": starts,
		"an init":    "package foo\n\nfunc init() { mode = \"new\" }\n",
		"a TestMain": "package foo\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestMain(m *testing.M) {\n\tmode = \"new\"\n\tos.Exit(m.Run())\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			f, run := added869(t)
			s397Write(t, filepath.Join(f.root, "foo", "zz_regression_test.go"), added, 0o644)
			r := run()
			if r.Passed() || r.Accepted["foo/pinned_accept_test.go:TestModeIsNew"] {
				t.Fatalf("a test file the build added passed the plan's acceptance test, with production unchanged: %s\n%s", r.Summary(), r.Output)
			}
			s397Status(t, r, "foo", "TestModeIsNew", "accept", "failed")
		})
	}

	t.Run("a helper the acceptance test would need", func(t *testing.T) {
		f, run := added869(t)
		// The pinned test calls render, which only an added test file declares.
		f.plan.Tests, f.plan.Sources = nil, map[string]string{}
		f.accept(t, "foo", "TestRenders", "package foo\n\nimport \"testing\"\n\nfunc TestRenders(t *testing.T) {\n\tif render(\"a\") != \"hello, a\" {\n\t\tt.Fatal(\"wrong greeting\")\n\t}\n}\n")
		s397Write(t, filepath.Join(f.root, "foo", "zz_regression_test.go"), "package foo\n\nfunc render(n string) string { return \"hello, \" + n }\n", 0o644)
		r := run()
		if r.Passed() || r.Accepted["foo/pinned_accept_test.go:TestRenders"] {
			t.Fatalf("an acceptance test passed on a helper only an added test file declares: %s\n%s", r.Summary(), r.Output)
		}
		if !strings.Contains(r.Output, "don't build without the test files the build added") || !strings.Contains(r.Output, "zz_regression_test.go") {
			t.Errorf("the result doesn't say why, or which file:\n%s", r.Output)
		}
	})

	t.Run("production changed, with a regression test beside it", func(t *testing.T) {
		f, run := added869(t)
		s397Write(t, filepath.Join(f.root, "foo", "code.go"), "package foo\n\nvar mode = \"new\"\n\n// Mode is how foo behaves.\nfunc Mode() string { return mode }\n", 0o644)
		s397Write(t, filepath.Join(f.root, "foo", "zz_regression_test.go"), "package foo\n\nimport \"testing\"\n\nfunc TestModeStaysNew(t *testing.T) {\n\tif Mode() == \"old\" {\n\t\tt.Fatal(\"mode went back\")\n\t}\n}\n", 0o644)
		r := run()
		if !r.Passed() {
			t.Fatalf("a real change with its own regression test didn't pass: %s\n%s", r.Summary(), r.Output)
		}
		s397Status(t, r, "foo", "TestModeIsNew", "accept", "passed")
		s397Status(t, r, "foo", "TestModeStaysNew", "test", "passed")
		// And a regression test that fails still fails the check.
		s397Write(t, filepath.Join(f.root, "foo", "zz_regression_test.go"), "package foo\n\nimport \"testing\"\n\nfunc TestModeStaysNew(t *testing.T) { t.Fatal(\"it didn't\") }\n", 0o644)
		if r := run(); r.Passed() {
			t.Fatalf("a failing regression test passed the check: %s", r.Summary())
		}
	})
}

// An added test file can't switch a package's existing tests off. Here the
// build breaks Double and adds a TestMain that exits before any test runs:
// the existing test still runs, from the binary without that file, and fails.
func TestAddedTestFilesCantSwitchExistingTestsOff869(t *testing.T) {
	f, run := added869(t)
	f.plan.Files = append(f.plan.Files, "bar/code.go")
	s397Write(t, filepath.Join(f.root, "bar", "code.go"), "package bar\n\n// Double doubles.\nfunc Double(n int) int { return n * 3 }\n", 0o644)
	s397Write(t, filepath.Join(f.root, "foo", "code.go"), "package foo\n\nvar mode = \"new\"\n\n// Mode is how foo behaves.\nfunc Mode() string { return mode }\n", 0o644)
	s397Write(t, filepath.Join(f.root, "bar", "zz_regression_test.go"), "package bar\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestMain(m *testing.M) { os.Exit(0) }\n", 0o644)
	r := run()
	if r.Passed() {
		t.Fatalf("a TestMain the build added switched the package's existing test off: %s\n%s", r.Summary(), r.Output)
	}
	s397Status(t, r, "bar", "TestDouble", "test", "failed")
}

// An added Go test file that declares TestMain or init is refused where a
// build is staged and in CI's scope check, which both ask the same policy:
// CI's own go test runs every file together. A test file the checkout
// already held, the plan's own files and files that aren't Go tests are
// left alone.
func TestAddedTestFilesCantStartTheTestBinary869(t *testing.T) {
	plan := &Plan{Files: []string{"foo/code.go", "foo/planned_test.go"}, Tests: []Test{{File: "foo/pinned_accept_test.go", Name: "TestPinned"}}}
	base := []string{"foo/code.go", "foo/existing_test.go", "go.mod"}
	testMain := "package foo\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestMain(m *testing.M) { os.Exit(m.Run()) }\n"
	initial := "package foo\n\nfunc init() { mode = \"new\" }\n"
	plain := "package foo\n\nimport \"testing\"\n\ntype suite struct{}\n\n// A method named init, or TestMain, starts nothing.\nfunc (suite) init() {}\n\nfunc TestRegression(t *testing.T) { suite{}.init() }\n"
	for _, tc := range []struct {
		name, file, source, refused string
	}{
		{"TestMain in an added test file", "foo/zz_test.go", testMain, "TestMain"},
		{"init in an added test file", "foo/zz_test.go", initial, "init"},
		{"init in an added external test file", "foo/zz_external_test.go", "package foo_test\n\nfunc init() {}\n", "init"},
		{"an ordinary added test file", "foo/zz_test.go", plain, ""},
		{"a test file the checkout held", "foo/existing_test.go", testMain, ""},
		{"a file the plan names", "foo/planned_test.go", testMain, ""},
		{"the plan's acceptance file", "foo/pinned_accept_test.go", testMain, ""},
		{"an added file that doesn't parse", "foo/zz_test.go", "package foo\n\nfunc init( {\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope, err := NewRegressionScope(plan, base, func(string) ([]byte, error) { return nil, os.ErrNotExist })
			if err != nil {
				t.Fatal(err)
			}
			candidates := append([]string{tc.file}, base...)
			err = scope.CheckPolicy(candidates, func(file string) ([]byte, error) {
				if file == tc.file {
					return []byte(tc.source), nil
				}
				return nil, os.ErrNotExist
			})
			if tc.refused == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.file+" declares "+tc.refused) {
				t.Fatalf("want %s refused for %s, got %v", tc.file, tc.refused, err)
			}
		})
	}
}
