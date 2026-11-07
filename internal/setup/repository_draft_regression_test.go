package setup_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryDraftPreservesQuotedCheckArguments(t *testing.T) {
	root := t.TempDir()
	node727(t, root, "vitest", 3)
	write727(t, root, "config with spaces.json", "{}")
	mutatePackage727(t, root, func(pkg map[string]any) {
		pkg["scripts"].(map[string]any)["test"] = `vitest run --config 'config with spaces.json' --testTimeout=2500`
		pkg["scripts"].(map[string]any)["format:check"] = `prettier --check "src/**/*.ts" 'test files/**/*.ts'`
	})
	b, c, issues := draft727(t, root)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	argv727(t, c.Parts[0], "format", "prettier", "--check", "src/**/*.ts", "test files/**/*.ts")
	argv727(t, c.Parts[0], "test", "vitest", "run", "--config", "config with spaces.json", "--testTimeout=2500")
	selector727(t, c.Parts[0], "vitest")
	has727(t, c.Parts[0].Inputs, "config with spaces.json")
	if _, err := load727(t, root, b); err != nil {
		t.Fatal(err)
	}
}

func TestRepositoryDraftRefusesChangedCommandMeaning(t *testing.T) {
	for _, tc := range []struct{ field, script string }{
		{"format:check", "prettier --check src/**/*.ts"},
		{"format:check", "prettier --check ../outside"},
		{"format:check", `prettier --check "$SOURCE"`},
		{"format:check", `prettier --check "src\ files/*.ts"`},
		{"lint", "eslint --fix ."},
		{"build", "tsc --watch"},
		{"test", "vitest run src"},
		{"test", "jest --passWithNoTests"},
		{"test", "vitest run --config ../../outside.json"},
		{"test", `vitest run --testTimeout 2500; echo done`},
	} {
		t.Run(tc.script, func(t *testing.T) {
			root := t.TempDir()
			node727(t, root, "vitest", 3)
			mutatePackage727(t, root, func(pkg map[string]any) { pkg["scripts"].(map[string]any)[tc.field] = tc.script })
			b, c, issues := draft727(t, root)
			field := strings.TrimSuffix(tc.field, ":check")
			missing727(t, c.Parts[0], field)
			issue727(t, issues, field)
			if field == "test" {
				missing727(t, c.Parts[0], "test_one")
			}
			refused727(t, root, b)
		})
	}
}

func TestRepositoryDraftRetainsInheritedPreparationPolicy(t *testing.T) {
	root := t.TempDir()
	node727(t, filepath.Join(root, "web"), "jest", 3)
	write727(t, root, ".npmrc", "ignore-scripts=false\n")
	write727(t, root, ".node-version", "99.0.0\n")
	b, c, issues := draft727(t, root)
	p := part727(t, c, "web", "typescript")
	has727(t, p.Inputs, ".npmrc", ".node-version")
	missing727(t, p, "install")
	if p.Image != "" {
		t.Fatal("inherited runtime constraint was ignored")
	}
	issue727(t, issues, "install", ".npmrc")
	issue727(t, issues, "toolchain")
	refused727(t, root, b)
}

func TestRepositoryDraftPythonConstraintsAndUnknownPolicy(t *testing.T) {
	for _, constraint := range []string{"==3.13", "==3.13.0", "~=3.99", ">=3.13 garbage", ",", ">=3.13,"} {
		t.Run(constraint, func(t *testing.T) {
			root := t.TempDir()
			python727(t, root, "requirements.lock")
			write727(t, root, "pyproject.toml", "[project]\nrequires-python = "+json727(t, constraint)+"\n")
			_, c, issues := draft727(t, root)
			if c.Parts[0].Image != "" {
				t.Fatal("guessed an incompatible or unsupported Python version")
			}
			issue727(t, issues, "toolchain")
		})
	}
	root := t.TempDir()
	python727(t, root, "requirements.lock")
	write727(t, root, "pytest.ini", "[pytest]\naddopts = --collect-only\n")
	_, c, issues := draft727(t, root)
	has727(t, c.Parts[0].Inputs, "pytest.ini")
	missing727(t, c.Parts[0], "test")
	missing727(t, c.Parts[0], "test_one")
	issue727(t, issues, "test")
}

func TestRepositoryDraftNodeRuntimeUsesNpmVersionGrammar(t *testing.T) {
	for _, constraint := range []string{",", ">=24,<25", "024", "latest"} {
		t.Run(constraint, func(t *testing.T) {
			root := t.TempDir()
			node727(t, root, "vitest", 3)
			mutatePackage727(t, root, func(pkg map[string]any) {
				pkg["engines"] = map[string]string{"node": constraint}
			})
			b, c, issues := draft727(t, root)
			if c.Parts[0].Image != "" {
				t.Fatal("unsupported npm runtime constraint became compatible")
			}
			issue727(t, issues, "toolchain")
			refused727(t, root, b)
		})
	}
}

func TestRepositoryDraftRejectsUnconfinedDeclaredInputs(t *testing.T) {
	root := t.TempDir()
	node727(t, root, "vitest", 3)
	write727(t, root, "AGENTS.md", "The current state is [outside](../outside.md).\n")
	write727(t, root, "tsconfig.json", `{"extends":"../outside.json"}`)
	b, c, issues := draft727(t, root)
	paths727(t, root, c)
	if len(c.CurrentState) != 0 {
		t.Fatal("escaped current-state declaration")
	}
	issue727(t, issues, "current_state")
	issue727(t, issues, "inputs", "extends")
	refused727(t, root, b)
}

func TestRepositoryDraftKeepsPytestOptionsInTheirConfiguration(t *testing.T) {
	root := t.TempDir()
	python727(t, root, "requirements.lock")
	write727(t, root, "pyproject.toml", "[tool.pytest.ini_options]\ntestpaths = ['tests']\naddopts = '-q --strict-markers'\n")
	_, c, issues := draft727(t, root)
	if len(issues) != 1 {
		t.Fatal(issues)
	}
	argv727(t, c.Parts[0], "test", "python3", "-m", "pytest", "tests")
	argv727(t, c.Parts[0], "test_one", "python3", "-m", "pytest", "{test}")
	has727(t, c.Parts[0].Inputs, "pyproject.toml")
}
