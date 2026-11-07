package setup_test

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func draftPolicyOwners(t *testing.T, c repository.Config, file string, dirs ...string) {
	t.Helper()
	var owners []string
	for _, p := range c.Parts {
		want := slices.Contains(dirs, p.Directory)
		if slices.Contains(p.Inputs, file) != want {
			t.Errorf("%s inputs do not capture the owners of %s: %q", p.Directory, file, p.Inputs)
		}
		if want {
			owners = append(owners, p.Name)
		}
	}
	slices.Sort(owners)
	var shared []string
	for _, p := range c.SharedPaths {
		if p.Path == file {
			shared = p.Parts
		}
	}
	if len(owners) > 1 && !slices.Equal(shared, owners) || len(owners) <= 1 && len(shared) != 0 {
		t.Errorf("shared owners of %s = %q, want %q when shared", file, shared, owners)
	}
}

func TestRepositoryDraftInheritedPythonTestPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, body string
	}{
		{"pytest.ini", "[pytest]\naddopts = --collect-only\n"},
		{".pytest.ini", "[pytest]\naddopts = --collect-only\n"},
		{"tox.ini", "[pytest]\naddopts = --collect-only\n"},
		{"setup.cfg", "[tool:pytest]\naddopts = --collect-only\n"},
		{"pyproject.toml", "[tool.pytest.ini_options]\naddopts = '--collect-only'\n"},
		{"pytest.toml", "[pytest]\naddopts = ['--collect-only']\n"},
		{".pytest.toml", "[pytest]\naddopts = ['--collect-only']\n"},
	} {
		for _, dir := range []string{".", "services"} {
			t.Run(path.Join(dir, tc.name), func(t *testing.T) {
				root := t.TempDir()
				for _, part := range []string{"services/one", "services/one/tools", "services/two", "other"} {
					python727(t, filepath.Join(root, part), "requirements.lock")
					if err := os.Remove(filepath.Join(root, part, "pyproject.toml")); err != nil {
						t.Fatal(err)
					}
				}
				file := path.Join(dir, tc.name)
				write727(t, root, file, tc.body)
				b, c, issues := draft727(t, root)
				owners := []string{"services/one", "services/one/tools", "services/two"}
				if dir == "." {
					owners = append(owners, "other")
				} else {
					p := part727(t, c, "other", "python")
					argv727(t, p, "test", "python3", "-m", "pytest")
					argv727(t, p, "test_one", "python3", "-m", "pytest", "{test}")
				}
				for _, owner := range owners {
					p := part727(t, c, owner, "python")
					missing727(t, p, "test")
					missing727(t, p, "test_one")
					issue727(t, issues, owner, file, "test")
				}
				if tc.name == "pyproject.toml" || tc.name == "setup.cfg" {
					owners = append(owners, dir)
				}
				draftPolicyOwners(t, c, file, owners...)
				paths727(t, root, c)
				refused727(t, root, b)
			})
		}
	}
}

func TestRepositoryDraftPythonFormatterPolicyOwnership(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"services/one", "services/one/tools", "services/two", "other"} {
		python727(t, filepath.Join(root, dir), "requirements.lock")
	}
	node727(t, filepath.Join(root, "web"), "vitest", 3)
	for _, name := range []string{".ruff.toml", "services/ruff.toml", "services/one/.ruff.toml", "other/ruff.toml"} {
		write727(t, root, name, "line-length = 88\n")
	}
	b, c, issues := draft727(t, root)
	draftPolicyOwners(t, c, ".ruff.toml", "services/one", "services/one/tools", "services/two", "other")
	draftPolicyOwners(t, c, "services/ruff.toml", "services/one", "services/one/tools", "services/two")
	draftPolicyOwners(t, c, "services/one/.ruff.toml", "services/one", "services/one/tools")
	draftPolicyOwners(t, c, "other/ruff.toml", "other")
	draftPolicyOwners(t, c, "services/one/pyproject.toml", "services/one", "services/one/tools")
	for _, p := range c.Parts {
		if p.Language == "python" {
			argv727(t, p, "format", "ruff", "format", "--check", ".")
			argv727(t, p, "lint", "ruff", "check", ".")
		}
	}
	p := part727(t, c, "services/one/tools", "python")
	missing727(t, p, "test")
	missing727(t, p, "test_one")
	issue727(t, issues, p.Directory, "services/one/pyproject.toml", "test")
	paths727(t, root, c)
	again, _, againIssues := draft727(t, root)
	if string(again) != string(b) || !slices.Equal(againIssues, issues) {
		t.Fatal("Python policy discovery is not deterministic")
	}
}

func TestRepositoryDraftVitestWorkspacePolicy(t *testing.T) {
	for _, ext := range []string{"ts", "js", "mts", "mjs", "cts", "cjs", "json"} {
		for _, explicit := range []bool{false, true} {
			name := "vitest.workspace." + ext
			t.Run(name+map[bool]string{false: "/implicit", true: "/explicit"}[explicit], func(t *testing.T) {
				root := t.TempDir()
				node727(t, root, "vitest", 3)
				mutatePackage727(t, root, func(pkg map[string]any) {
					command := "vitest run"
					if explicit {
						command += " --config checks.json"
					}
					pkg["scripts"].(map[string]any)["test"] = command
				})
				if explicit {
					write727(t, root, "checks.json", "{}")
				}
				body := "export default ['packages/*']\n"
				if ext == "json" {
					body = `["packages/*"]`
				}
				write727(t, root, name, body)
				b, c, issues := draft727(t, root)
				p := c.Parts[0]
				has727(t, p.Inputs, name, "vitest.config.json")
				if explicit {
					has727(t, p.Inputs, "checks.json")
				}
				missing727(t, p, "test")
				missing727(t, p, "test_one")
				issue727(t, issues, name, "workspace", "test")
				refused727(t, root, b)
			})
		}
	}
}

func TestRepositoryDraftJestConfigurationConflict(t *testing.T) {
	for _, config := range []string{"", " --config jest.config.json", " --config=jest.config.json"} {
		t.Run(config, func(t *testing.T) {
			root := t.TempDir()
			node727(t, root, "jest", 3)
			mutatePackage727(t, root, func(pkg map[string]any) {
				pkg["scripts"].(map[string]any)["test"] = "jest --runInBand" + config
				pkg["jest"] = map[string]any{"testMatch": []string{"**/*.test.ts"}}
			})
			b, c, issues := draft727(t, root)
			p := c.Parts[0]
			has727(t, p.Inputs, "package.json", "jest.config.json")
			if config == "" {
				missing727(t, p, "test")
				missing727(t, p, "test_one")
				issue727(t, issues, "package.json", "jest.config.json", "ambiguous", "--config")
				refused727(t, root, b)
			} else {
				if len(issues) != 0 {
					t.Fatal(issues)
				}
				selector727(t, p, "jest")
				if _, err := load727(t, root, b); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
