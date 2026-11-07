package setup_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Pytest takes its configuration from the common ancestor of the paths it's
// given, upward, so a file below the part can replace the part's own: inside
// the suite's targets, or beside any test a test_one node ID names (#727's
// review). A file pytest wouldn't read there leaves the draft alone.
func TestRepositoryDraftReviewsNestedPytestConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		reached    bool
	}{
		{"tests/pytest.ini", "[pytest]\naddopts = --collect-only\n", true},
		{"tests/.pytest.ini", "[pytest]\naddopts = --collect-only\n", true},
		{"tests/pytest.toml", "[pytest]\naddopts = ['--collect-only']\n", true},
		{"tests/tox.ini", "[pytest]\naddopts = --collect-only\n", true},
		{"tests/setup.cfg", "[tool:pytest]\naddopts = --collect-only\n", true},
		{"tests/pyproject.toml", "[tool.pytest.ini_options]\naddopts = '--collect-only'\n", true},
		{"tests/unit/pytest.ini", "[pytest]\naddopts = --collect-only\n", true},
		{"src/pytest.ini", "[pytest]\naddopts = --collect-only\n", true},
		{"tests/tox.ini", "[tox]\nenvlist = py313\n", false},
		{"tests/setup.cfg", "[metadata]\nname = helpers\n", false},
		{"tools/pyproject.toml", "[project]\nname = \"tools\"\n[tool.pytest.ini_options]\naddopts = '--collect-only'\n", false},
	} {
		t.Run(tc.name+map[bool]string{true: "", false: " without effect"}[tc.reached], func(t *testing.T) {
			root := t.TempDir()
			python727(t, root, "requirements.lock")
			write727(t, root, tc.name, tc.body)
			b, c, issues := draft727(t, root)
			p := part727(t, c, ".", "python")
			if !tc.reached {
				argv727(t, p, "test", "python3", "-m", "pytest", "tests")
				argv727(t, p, "test_one", "python3", "-m", "pytest", "{test}")
				for _, issue := range issues {
					if strings.HasPrefix(issue, ". (python)") && strings.Contains(issue, tc.name) {
						t.Errorf("a configuration pytest doesn't read there held the draft: %q", issue)
					}
				}
				return
			}
			missing727(t, p, "test")
			missing727(t, p, "test_one")
			issue727(t, issues, tc.name, "test")
			has727(t, p.Inputs, tc.name)
			refused727(t, root, b)
		})
	}
}

// Collection naming decides whether the test_ functions native selectors
// name are collected at all, and a [tool.pytest] table isn't read by the
// draft, so neither yields test commands.
func TestRepositoryDraftReviewsPytestCollectionSettings(t *testing.T) {
	for _, tc := range []struct{ setting, line string }{
		{"python_functions", `python_functions = ["check_*"]`},
		{"python_classes", `python_classes = ["Check"]`},
		{"python_files", `python_files = ["check_*.py"]`},
	} {
		t.Run(tc.setting, func(t *testing.T) {
			root := t.TempDir()
			python727(t, root, "requirements.lock")
			write727(t, root, "pyproject.toml", "[project]\nname = \"worker\"\nversion = \"1.0.0\"\nrequires-python = \">=3.13,<3.14\"\n[tool.pytest.ini_options]\ntestpaths = [\"tests\"]\n"+tc.line+"\n[tool.ruff]\nline-length = 88\n")
			b, c, issues := draft727(t, root)
			p := part727(t, c, ".", "python")
			missing727(t, p, "test")
			missing727(t, p, "test_one")
			issue727(t, issues, "pyproject.toml", tc.setting)
			refused727(t, root, b)
		})
	}
	t.Run("tool.pytest", func(t *testing.T) {
		root := t.TempDir()
		python727(t, root, "requirements.lock")
		write727(t, root, "pyproject.toml", "[project]\nname = \"worker\"\nversion = \"1.0.0\"\nrequires-python = \">=3.13,<3.14\"\n[tool.pytest.ini_options]\ntestpaths = [\"tests\"]\n[tool.pytest]\naddopts = [\"--collect-only\"]\n")
		b, c, issues := draft727(t, root)
		p := part727(t, c, ".", "python")
		missing727(t, p, "test")
		missing727(t, p, "test_one")
		issue727(t, issues, "pyproject.toml", "tool.pytest.addopts")
		refused727(t, root, b)
	})
}

// A part always has its own package.json, where Jest's configuration search
// stops, with or without a "jest" key (jest-config's
// resolveConfigPathByTraversing, 29 and 30). So an ancestor's Jest
// configuration can't reach a nested part's suite or selector (#727's review).
func TestRepositoryDraftNestedJestPartIgnoresAncestorConfiguration(t *testing.T) {
	root := t.TempDir()
	web := filepath.Join(root, "web")
	node727(t, web, "jest", 3)
	if err := os.Remove(filepath.Join(web, "jest.config.json")); err != nil {
		t.Fatal(err)
	}
	mutatePackage727(t, web, func(pkg map[string]any) { pkg["scripts"].(map[string]any)["test"] = "jest --runInBand" })
	write727(t, root, "jest.config.json", `{"testEnvironment":"jsdom"}`)
	_, c, issues := draft727(t, root)
	p := part727(t, c, "web", "typescript")
	argv727(t, p, "test", "jest", "--runInBand")
	argv727(t, p, "test_one", "jest", "--runInBand", "--testNamePattern", "{test}")
	if slices.Contains(p.Inputs, "jest.config.json") {
		t.Errorf("the nested part captured an ancestor Jest configuration Jest never reads for it: %q", p.Inputs)
	}
	for _, issue := range issues {
		if strings.HasPrefix(issue, "web (typescript) test") {
			t.Errorf("an unreachable ancestor Jest configuration held the nested part's tests: %q", issue)
		}
	}
}
