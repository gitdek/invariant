package scope

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/repository"
)

type conventionScope654 struct {
	framework, language, source, body, accept, selector, acceptance string
	config, configText                                              string
	added, rejected                                                 []string
	suite, one                                                      []string
}

func scopeConventions654() []conventionScope654 {
	return []conventionScope654{
		{
			framework: "pytest", language: "python", source: "app/src/orders/service.py", body: "value = 1\n",
			accept: "app/tests/orders/test_contract.py", selector: "tests/orders/test_contract.py::test_contract", acceptance: "def test_contract():\n    assert True\n",
			config: "app/pytest.ini", configText: "[pytest]\ntestpaths = tests src\npython_files = test_*.py *_test.py\nnorecursedirs = excluded\n",
			added:    []string{"app/tests/orders/test_regression.py", "app/tests/billing/refund_test.py", "app/src/orders/test_service.py"},
			rejected: []string{"app/tests/excluded/test_hidden.py", "app/outside/test_hidden.py", "app/tests/helper.py", "app/tests/conftest.py", "app/tests/__init__.py", "app/tests/test_fake.py.txt", "other/tests/test_other.py", "app/vendor/tests/test_nested.py"},
			suite:    []string{"python3", "-m", "pytest", "-q", "tests", "src"}, one: []string{"python3", "-m", "pytest", "-q", "{test}"},
		},
		{
			framework: "vitest", language: "typescript", source: "app/src/orders/service.ts", body: "export const value = 1;\n",
			accept: "app/tests/orders/contract.test.ts", selector: "contract", acceptance: "import {test,expect} from 'vitest';\ntest('contract',()=>expect(1).toBe(1));\n",
			config: "app/vitest.config.ts", configText: "export default {test: {include: ['tests/**/*.test.ts', 'src/**/*.spec.ts'], exclude: ['tests/excluded/**']}};\n",
			added:    []string{"app/tests/orders/regression.test.ts", "app/tests/billing/refund.test.ts", "app/src/orders/service.spec.ts"},
			rejected: []string{"app/tests/excluded/hidden.test.ts", "app/outside/hidden.test.ts", "app/tests/helper.ts", "app/tests/fake.test.ts.map", "app/tsconfig.test.json", "other/tests/other.test.ts", "app/vendor/tests/nested.test.ts"},
			suite:    []string{"vitest", "run", "--config", "vitest.config.ts"}, one: []string{"vitest", "run", "--config", "vitest.config.ts", "-t", "{test}"},
		},
		{
			framework: "jest", language: "typescript", source: "app/src/orders/service.ts", body: "export const value = 1;\n",
			accept: "app/tests/orders/contract.spec.ts", selector: "contract", acceptance: "import {test,expect} from '@jest/globals';\ntest('contract',()=>expect(1).toBe(1));\n",
			config: "app/jest.config.json", configText: `{"testMatch":["<rootDir>/tests/**/*.spec.ts","<rootDir>/src/**/*.test.ts","<rootDir>/__tests__/**/*.ts"],"testPathIgnorePatterns":["/excluded/"]}` + "\n",
			added:    []string{"app/tests/orders/regression.spec.ts", "app/tests/billing/refund.spec.ts", "app/src/orders/service.test.ts", "app/__tests__/refund.ts"},
			rejected: []string{"app/tests/excluded/hidden.spec.ts", "app/outside/hidden.spec.ts", "app/tests/helper.ts", "app/tests/fake.spec.ts.map", "app/tsconfig.test.json", "other/tests/other.spec.ts", "app/vendor/tests/nested.spec.ts"},
			suite:    []string{"jest", "--config", "jest.config.json", "--runInBand"}, one: []string{"jest", "--config", "jest.config.json", "--runInBand", "-t", "{test}"},
		},
		{
			framework: "go", language: "go", source: "app/page/web/app.js", body: "const value = 1;\n",
			accept: "app/page/contract_accept_test.go", selector: "TestContract", acceptance: "package page\nimport \"testing\"\nfunc TestContract(t *testing.T) {}\n",
			added:    []string{"app/page/regression_test.go", "app/new/regression_test.go"},
			rejected: []string{"app/page/child/regression_test.go", "app/other/regression_test.go", "app/page/regression.go", "other/page/regression_test.go", "app/vendor/regression_test.go"},
			suite:    []string{"go", "test", "./..."}, one: []string{"go", "test", "./page", "-run", "{test}"},
		},
	}
}

func scopeRegressionText654(c conventionScope654) string {
	switch c.framework {
	case "go":
		return "package page\nimport \"testing\"\nfunc TestRegression654(t *testing.T) {}\n"
	case "pytest":
		return "def test_regression654():\n    assert True\n"
	case "vitest":
		return "import {test,expect} from 'vitest';\ntest('regression654',()=>expect(1).toBe(1));\n"
	default:
		return "import {test,expect} from '@jest/globals';\ntest('regression654',()=>expect(1).toBe(1));\n"
	}
}

func TestConfiguredRegressionScope654(t *testing.T) {
	for _, c := range scopeConventions654() {
		type scenario struct {
			name, file, mode string
			allowed          bool
		}
		var cases []scenario
		for _, file := range c.added {
			cases = append(cases, scenario{"allow/" + file, file, "add", true})
		}
		for _, file := range c.rejected {
			cases = append(cases, scenario{"refuse/" + file, file, "add", false})
		}
		cases = append(cases,
			scenario{"existing test deleted by target", c.added[0], "existing", false},
			scenario{"addition despite target deletion", c.added[0], "target deletes", true},
			scenario{"target independently adds test", c.added[0], "target adds", true},
			scenario{"repair removes its addition", c.added[0], "repair remove", true},
			scenario{"linked regression", c.added[0], "symlink", false},
			scenario{"changed acceptance", c.accept, "accept change", false},
			scenario{"removed acceptance", c.accept, "accept remove", false},
			scenario{"linked acceptance", c.accept, "accept link", false},
			scenario{"changed plan", plumbing.LockPath(7), "lock change", false},
			scenario{"candidate repository policy", repository.Path, "contract change", false},
		)
		if c.config != "" {
			cases = append(cases,
				scenario{"candidate widens discovery", c.rejected[0], "widen", false},
				scenario{"target changes discovery", c.added[0], "target config", true},
				scenario{"framework defaults", c.added[0], "defaults", true},
				scenario{"test configuration", c.config, "config edit", false},
			)
		}
		for _, tc := range cases {
			t.Run(c.framework+"/"+tc.name, func(t *testing.T) {
				ctx := context.Background()
				root := repo(t)
				commands := map[string]repository.Command{}
				for _, phase := range []string{"install", "format", "lint", "build"} {
					commands[phase] = repository.Command{NotApplicable: "Scope fixture does not execute commands."}
				}
				commands["test"], commands["test_one"] = repository.Command{Argv: c.suite}, repository.Command{Argv: c.one}
				if tc.mode == "defaults" {
					switch c.framework {
					case "pytest":
						commands["test"], commands["test_one"] = repository.Command{Argv: []string{"pytest"}}, repository.Command{Argv: []string{"pytest", "{test}"}}
					case "vitest":
						commands["test"], commands["test_one"] = repository.Command{Argv: []string{"vitest", "run"}}, repository.Command{Argv: []string{"vitest", "run", "-t", "{test}"}}
					case "jest":
						commands["test"], commands["test_one"] = repository.Command{Argv: []string{"jest"}}, repository.Command{Argv: []string{"jest", "-t", "{test}"}}
					}
				}
				config := repository.Config{Version: 1, AgentRules: []string{}, CurrentState: []string{}, TrustedPaths: []string{}, SharedPaths: []repository.SharedPath{}}
				for _, dir := range []string{"app", "other", "app/vendor"} {
					config.Parts = append(config.Parts, repository.Part{Name: strings.ReplaceAll(dir, "/", "-"), Directory: dir, Language: c.language, Image: "fixture@sha256:" + strings.Repeat("a", 64), Inputs: []string{}, Lockfiles: []string{}, Commands: commands})
					write(t, root, map[string]string{dir + "/README.md": "part\n"})
				}
				data, err := json.Marshal(config)
				if err != nil {
					t.Fatal(err)
				}
				write(t, root, map[string]string{repository.Path: string(data), c.source: c.body})
				if c.config != "" && tc.mode != "defaults" {
					write(t, root, map[string]string{c.config: c.configText})
				}
				if c.framework == "go" {
					write(t, root, map[string]string{"app/page/page.go": "package page\n", "app/old/old.go": "package old\n", "app/other/other.go": "package other\n"})
				}
				existing := strings.Replace(c.added[0], "regression", "existing", 1)
				existingText := scopeRegressionText654(c)
				if c.framework == "go" {
					existingText = strings.Replace(existingText, "TestRegression654", "TestExisting654", 1)
				}
				write(t, root, map[string]string{existing: existingText})
				if tc.mode == "existing" {
					write(t, root, map[string]string{tc.file: scopeRegressionText654(c)})
				}
				commit(t, root, "captured repository")
				gitRun(t, root, "branch", "-f", "main")
				snapshot, err := repository.Load(ctx, root, "HEAD")
				if err != nil {
					t.Fatal(err)
				}
				plan := plumbing.Plan{Name: "regression scope", Summary: "Implement orders within captured test scope.", Files: []string{c.source}, Tests: []plumbing.Test{{Name: "contract", Part: "app", Framework: c.framework, File: c.accept, Selector: c.selector, Says: "The contract holds."}}}
				if c.framework == "go" {
					plan.Files = append(plan.Files, "app/old/old.go", "app/new/new.go")
				}
				draft := t.TempDir()
				// Marshal only agent-supplied fields; authority and sources are captured by the reader.
				data, err = json.Marshal(map[string]any{"name": plan.Name, "summary": plan.Summary, "files": plan.Files, "tests": plan.Tests})
				if err != nil {
					t.Fatal(err)
				}
				write(t, draft, map[string]string{"plan.json": string(data), "tests/" + c.accept: c.acceptance})
				bound, err := plumbing.ReadRepositoryDraft(ctx, draft, snapshot)
				if err != nil {
					t.Fatal(err)
				}
				lock := plumbing.Lock{Plan: *bound, Ratified: plumbing.Ratification{Issue: 7, Proposal: bound.Hash()}}

				// Move the target independently, leaving the original captured commit
				// as the diff's merge base and the source of novelty and policy.
				if strings.HasPrefix(tc.mode, "target") || tc.mode == "existing" {
					gitRun(t, root, "checkout", "-q", "main")
					switch tc.mode {
					case "target deletes":
						err = os.Remove(filepath.Join(root, existing))
					case "existing":
						err = os.Remove(filepath.Join(root, tc.file))
					case "target adds":
						write(t, root, map[string]string{tc.file: scopeRegressionText654(c)})
					case "target config":
						write(t, root, map[string]string{c.config: "target policy is unrelated to this branch's base\n"})
					}
					if err != nil {
						t.Fatal(err)
					}
					commit(t, root, "target moves on")
					gitRun(t, root, "checkout", "-q", "invariant/issue-7-new")
				}
				if err := lock.Write(root); err != nil {
					t.Fatal(err)
				}
				write(t, root, map[string]string{c.source: c.body + "\n"})
				if c.framework == "go" {
					if err := os.Remove(filepath.Join(root, "app/old/old.go")); err != nil {
						t.Fatal(err)
					}
					write(t, root, map[string]string{"app/new/new.go": "package newpkg\n"})
				}
				badFile := tc.file
				switch tc.mode {
				case "accept remove":
					err = os.Remove(filepath.Join(root, tc.file))
				case "accept link":
					if err = os.Remove(filepath.Join(root, tc.file)); err == nil {
						err = os.Symlink(filepath.Base(c.source), filepath.Join(root, tc.file))
					}
				case "symlink":
					if err = os.MkdirAll(filepath.Dir(filepath.Join(root, tc.file)), 0o755); err == nil {
						err = os.Symlink(filepath.Base(c.source), filepath.Join(root, tc.file))
					}
				case "lock change":
					lock.Plan.Summary = "unratified replacement"
					data, err = json.Marshal(lock)
					if err == nil {
						write(t, root, map[string]string{tc.file: string(data)})
					}
				case "contract change":
					write(t, root, map[string]string{tc.file: `{"version":999}`})
				case "config edit":
					write(t, root, map[string]string{tc.file: "candidate configuration\n"})
				case "accept change":
					write(t, root, map[string]string{tc.file: c.acceptance + "\n" + "changed source\n"})
				default:
					body := scopeRegressionText654(c)
					if tc.mode == "existing" {
						body += "\n"
					}
					if strings.HasPrefix(tc.file, "app/new/") {
						body = strings.Replace(body, "package page", "package newpkg", 1)
					}
					write(t, root, map[string]string{tc.file: body})
					if tc.mode == "widen" {
						wide := "[pytest]\ntestpaths = tests src\nnorecursedirs =\n"
						if c.framework == "vitest" {
							wide = "export default {test: {include: ['**/*.test.ts'], exclude: []}};\n"
						}
						if c.framework == "jest" {
							wide = `{"testMatch":["<rootDir>/**/*.spec.ts"],"testPathIgnorePatterns":[]}`
						}
						write(t, root, map[string]string{c.config: wide})
					}
					if tc.mode == "repair remove" {
						commit(t, root, "first build with new test")
						err = os.Remove(filepath.Join(root, tc.file))
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				commit(t, root, "candidate")
				result, err := Check(ctx, root, "main", "HEAD", 7)
				if err != nil {
					t.Fatal(err)
				}
				if result.OK() != tc.allowed {
					t.Errorf("scope allowed=%v, want %v: %v", result.OK(), tc.allowed, result.Problems)
				}
				if !tc.allowed && tc.mode != "lock change" {
					if !strings.Contains(strings.Join(result.Problems, "\n"), badFile) {
						t.Errorf("scope failed to name refused path %s: %v", badFile, result.Problems)
					}
				}
				if !result.Plumbing || result.Project != plumbing.LockPath(7) {
					t.Error("native plan identity lost")
				}
			})
		}
	}
}
