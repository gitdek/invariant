package plumbing

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func regressionPolicyFixture(t *testing.T, language string, argv []string, files map[string]string) (string, *Plan) {
	t.Helper()
	root := t.TempDir()
	part := repository.Part{Name: "app", Directory: "app", Language: language, Commands: map[string]repository.Command{"test": {Argv: argv}}}
	config := repository.Config{Version: 1, Parts: []repository.Part{part, {Name: "nested", Directory: "app/vendor", Language: language}}}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	write468(t, root, repository.Path, string(data))
	write468(t, root, "app/src/orders/service.py", "value = 1\n")
	for file, text := range files {
		write468(t, root, file, text)
	}
	return root, &Plan{Files: []string{"app/src/orders/service.py"}}
}

func TestRegressionDiscoveryLiteralPolicies(t *testing.T) {
	for _, tc := range []struct {
		name, language, config, text string
		argv                         []string
		allowed, refused             []string
	}{
		{"pytest defaults", "python", "", "", []string{"pytest"},
			[]string{"app/tests/billing/test_refund.py", "app/src/orders/refund_test.py"}, []string{"app/tests/helper.py", "app/tests/conftest.py", "app/tests/__init__.py", "app/.hidden/test_refund.py", "app/vendor/test_refund.py"}},
		{"pytest targets intersect patterns", "python", "app/pytest.ini", "[pytest]\ntestpaths = other\npython_files = test_*.py helper.py\nnorecursedirs = excluded\n", []string{"python3", "-m", "pytest", "-q", "tests", "--ignore", "tests/ignored"},
			[]string{"app/tests/billing/test_refund.py"}, []string{"app/src/test_refund.py", "app/tests/refund_test.py", "app/tests/helper.py", "app/tests/excluded/test_refund.py", "app/tests/ignored/test_refund.py"}},
		{"pytest multiline ini", "python", "app/pytest.ini", "[pytest]\ntestpaths =\n    tests\n    src\n", []string{"pytest"},
			[]string{"app/tests/test_refund.py", "app/src/test_refund.py"}, []string{"app/other/test_refund.py"}},
		{"pytest ignore glob", "python", "", "", []string{"pytest", "--ignore-glob", "tests/*_test.py", "--ignore-glob", "*/excluded"},
			[]string{"app/tests/test_refund.py"}, []string{"app/tests/nested/refund_test.py", "app/tests/excluded/test_refund.py"}},
		{"pytest literal toml", "python", "app/pyproject.toml", "[project]\nname = 'fixture'\n[tool.pytest.ini_options]\ntestpaths = ['tests', 'src']\npython_files = ['*_test.py']\n", []string{"pytest"},
			[]string{"app/tests/refund_test.py"}, []string{"app/tests/test_refund.py", "app/other/refund_test.py"}},
		{"pytest setup cfg", "python", "app/setup.cfg", "[tool:pytest]\ntestpaths = tests\n", []string{"pytest"},
			[]string{"app/tests/test_refund.py"}, []string{"app/other/test_refund.py"}},
		{"vitest literal module", "typescript", "app/vitest.config.ts", "export default {test: {include: ['tests/**/*.test.ts', 'src/**/*.spec.ts'], exclude: ['tests/excluded/**']}};", []string{"vitest", "run"},
			[]string{"app/tests/billing/refund.test.ts", "app/src/orders/refund.spec.ts"}, []string{"app/tests/excluded/refund.test.ts", "app/tests/refund.spec.ts", "app/other/refund.test.ts"}},
		{"vitest suite target", "typescript", "", "", []string{"vitest", "run", "tests"},
			[]string{"app/tests/refund.test.tsx"}, []string{"app/src/refund.test.ts", "app/tests/refund.config.test.ts", "app/tests/dist/refund.test.ts"}},
		{"jest roots and ignores", "typescript", "app/jest.config.cjs", "module.exports = { roots: ['<rootDir>/tests'], testMatch: ['**/*.spec.ts'], testPathIgnorePatterns: ['/excluded/'] };", []string{"jest", "--runInBand"},
			[]string{"app/tests/billing/refund.spec.ts"}, []string{"app/tests/excluded/refund.spec.ts", "app/src/refund.spec.ts", "app/tests/refund.test.ts"}},
		{"jest package config", "typescript", "app/package.json", `{"jest":{"testMatch":["<rootDir>/__tests__/**/*.ts"]}}`, []string{"jest"},
			[]string{"app/__tests__/refund.ts"}, []string{"app/src/refund.test.ts", "app/__tests__/refund.d.ts"}},
		{"jest empty roots", "typescript", "app/jest.config.json", `{"roots":[]}`, []string{"jest"},
			nil, []string{"app/tests/refund.test.ts"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{}
			if tc.config != "" {
				files[tc.config] = tc.text
			}
			root, plan := regressionPolicyFixture(t, tc.language, tc.argv, files)
			scope, err := checkoutRegressionScope(t.Context(), root, plan)
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range append(slices.Clone(tc.allowed), tc.refused...) {
				got := scope.Files([]string{file})
				if (len(got) == 1) != slices.Contains(tc.allowed, file) {
					t.Errorf("%s: admitted %v", file, got)
				}
			}
		})
	}
}

func TestRegressionDiscoveryRefusesUncertainMembership(t *testing.T) {
	for _, tc := range []struct {
		name, language, config, text, file string
		argv                               []string
	}{
		{"pytest expression", "python", "", "", "app/tests/test_new.py", []string{"pytest", "-k", "ready"}},
		{"pytest node", "python", "", "", "app/tests/test_new.py", []string{"pytest", "tests/test_new.py::test_ready"}},
		{"pytest plugin", "python", "", "", "app/tests/test_new.py", []string{"pytest", "-p", "plugin"}},
		{"pytest override", "python", "", "", "app/tests/test_new.py", []string{"pytest", "-o", "python_files=*.py"}},
		{"pytest conftest", "python", "app/tests/conftest.py", "collect_ignore = ['test_new.py']\n", "app/tests/test_new.py", []string{"pytest"}},
		{"pytest package hooks", "python", "app/tests/__init__.py", "raise RuntimeError()", "app/tests/test_new.py", []string{"pytest"}},
		{"pytest addopts", "python", "app/pytest.ini", "[pytest]\naddopts = -k ready\n", "app/tests/test_new.py", []string{"pytest"}},
		{"vitest executable", "typescript", "app/vitest.config.ts", "import {defineConfig} from 'vitest/config'; export default defineConfig({});", "app/tests/new.test.ts", []string{"vitest", "run"}},
		{"vitest mutation", "typescript", "app/vitest.config.ts", "export default {test: {}}; hideTests();", "app/tests/new.test.ts", []string{"vitest", "run"}},
		{"vitest spread", "typescript", "app/vitest.config.ts", "export default {test: {...settings}}", "app/tests/new.test.ts", []string{"vitest", "run"}},
		{"vitest duplicate", "typescript", "app/vitest.config.ts", "export default {test: {}, test: {}}", "app/tests/new.test.ts", []string{"vitest", "run"}},
		{"vitest negated pattern", "typescript", "app/vitest.config.ts", "export default {test: {include: ['**/*.test.ts', '!**/new.test.ts']}}", "app/tests/new.test.ts", []string{"vitest", "run"}},
		{"jest regex", "typescript", "app/jest.config.json", `{"testRegex":"ready"}`, "app/tests/new.test.ts", []string{"jest"}},
		{"jest projects", "typescript", "app/jest.config.json", `{"projects":["other"]}`, "app/tests/new.test.ts", []string{"jest"}},
		{"jest config root", "typescript", "app/config/jest.json", `{}`, "app/tests/new.test.ts", []string{"jest", "--config", "config/jest.json"}},
		{"jest automatic transform", "typescript", "app/babel.config.js", "module.exports = require('./policy');", "app/tests/new.test.ts", []string{"jest"}},
		{"jest selection", "typescript", "", "", "app/tests/new.test.ts", []string{"jest", "-t", "ready"}},
		{"wrapper", "typescript", "", "", "app/tests/new.test.ts", []string{"npm", "test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{}
			if tc.config != "" {
				files[tc.config] = tc.text
			}
			root, plan := regressionPolicyFixture(t, tc.language, tc.argv, files)
			scope, err := checkoutRegressionScope(t.Context(), root, plan)
			if err != nil {
				t.Fatal(err)
			}
			if got := scope.Files([]string{tc.file}); len(got) != 0 {
				t.Fatalf("uncertain discovery admitted %v", got)
			}
		})
	}
}

func TestRegressionPolicyCannotHideAdditions(t *testing.T) {
	for _, config := range []string{"app/pytest.ini", "app/tests/conftest.py", "app/tests/__init__.py", repository.Path} {
		t.Run(config, func(t *testing.T) {
			root, plan := regressionPolicyFixture(t, "python", []string{"pytest"}, nil)
			// A named configuration edit remains subject to captured discovery
			// when the build also takes the implicit regression allowance.
			plan.Files = append(plan.Files, config)
			candidate := t.TempDir()
			if err := CopyCheckout(context.Background(), root, candidate); err != nil {
				t.Fatal(err)
			}
			write468(t, candidate, "app/tests/test_new.py", "def test_new():\n    assert False\n")
			write468(t, candidate, config, "candidate hides the test\n")
			err := Stage(t.Context(), root, candidate, plan, t.TempDir())
			if err == nil || !strings.Contains(err.Error(), config) {
				t.Fatalf("candidate policy accepted: %v", err)
			}
		})
	}
}

func TestRegressionPolicyUsesOriginalAfterCandidateEdits(t *testing.T) {
	root, plan := regressionPolicyFixture(t, "python", []string{"pytest", "tests"}, map[string]string{"app/tests/test_existing.py": "original\n"})
	candidate := t.TempDir()
	if err := CopyCheckout(t.Context(), root, candidate); err != nil {
		t.Fatal(err)
	}
	write468(t, candidate, "app/tests/test_existing.py", "replacement\n")
	write468(t, candidate, "app/tests/test_new.py", "new\n")
	write468(t, candidate, "app/pytest.ini", "[pytest]\npython_files = nothing.py\n")
	write468(t, candidate, repository.Path, "candidate authority\n")
	staged := t.TempDir()
	if err := Stage(t.Context(), root, candidate, plan, staged); err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{"app/tests/test_existing.py": "original\n", "app/tests/test_new.py": "new\n"} {
		data, err := os.ReadFile(filepath.Join(staged, file))
		if err != nil || string(data) != want {
			t.Errorf("%s: %q, %v", file, data, err)
		}
	}
	if _, err := os.Stat(filepath.Join(staged, "app/pytest.ini")); !os.IsNotExist(err) {
		t.Fatal("unnamed configuration was staged")
	}
}
