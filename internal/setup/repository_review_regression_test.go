package setup_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestRepositoryDraftInheritedCheckOwnership(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".", "web", "web/tools", "server"} {
		node727(t, filepath.Join(root, dir), "vitest", 3)
	}
	for name, body := range map[string]string{
		".prettierrc.json":      `{"semi":false}`,
		"eslint.config.mjs":     `export default [];`,
		".editorconfig":         "root = true\n",
		"web/.prettierrc.json":  `{"singleQuote":true}`,
		"web/.eslintrc.json":    `{}`,
		"web/.prettierignore":   "generated\n",
		"server/.eslintrc.json": `{}`,
		"server/.eslintignore":  "generated\n",
	} {
		write727(t, root, name, body)
	}
	mutatePackage727(t, root, func(pkg map[string]any) { pkg["prettier"] = map[string]any{"semi": false} })
	mutatePackage727(t, filepath.Join(root, "web"), func(pkg map[string]any) { pkg["eslintConfig"] = map[string]any{} })
	b, c, issues := draft727(t, root)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	paths727(t, root, c)
	s, err := load727(t, root, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		file string
		dirs []string
	}{
		{".prettierrc.json", []string{".", "server", "web", "web/tools"}},
		{"eslint.config.mjs", []string{".", "server", "web", "web/tools"}},
		{".editorconfig", []string{".", "server", "web", "web/tools"}},
		{"package.json", []string{".", "server", "web", "web/tools"}},
		{"web/.prettierrc.json", []string{"web", "web/tools"}},
		{"web/.eslintrc.json", []string{"web", "web/tools"}},
		{"web/.prettierignore", []string{"web", "web/tools"}},
		{"web/package.json", []string{"web", "web/tools"}},
		{"server/.eslintrc.json", []string{"server"}},
		{"server/.eslintignore", []string{"server"}},
		{"tsconfig.json", []string{"."}},
	} {
		for _, p := range c.Parts {
			if slices.Contains(p.Inputs, tc.file) != slices.Contains(tc.dirs, p.Directory) {
				t.Errorf("%s inputs do not capture the owners of %s: %q", p.Directory, tc.file, p.Inputs)
			}
		}
		parts, err := s.PartsFor([]string{tc.file})
		if err != nil {
			t.Fatal(err)
		}
		var dirs []string
		for _, p := range parts {
			dirs = append(dirs, p.Directory)
		}
		slices.Sort(dirs)
		if !slices.Equal(dirs, tc.dirs) {
			t.Errorf("checks selected by %s = %q, want %q", tc.file, dirs, tc.dirs)
		}
	}
	again, _, issues := draft727(t, root)
	if string(again) != string(b) || len(issues) != 0 {
		t.Fatalf("inherited policy draft changed after commit: %q", issues)
	}
}

func TestRepositoryDraftRefusesUnsupportedNativeSettings(t *testing.T) {
	for _, tc := range []struct {
		framework, config, setting string
	}{
		{"jest", `{"testEnvironment":"jsdom"}`, "testEnvironment"},
		{"jest", `{"runner":"custom-runner"}`, "runner"},
		{"jest", `{"setupFilesAfterEnv":["./setup.js"]}`, "setupFilesAfterEnv"},
		{"jest", `{"transform":{"^.+\\.ts$":"babel-jest"}}`, "transform"},
		{"jest", `{"transform":{"^.+\\.ts$":["ts-jest",{"tsconfig":"tsconfig.json"}]}}`, "transform"},
		{"jest", `{"clearMocks":"true"}`, "clearMocks"},
		{"jest", `{"resetMocks":null}`, "resetMocks"},
		{"jest", `{"testTimeout":-1}`, "testTimeout"},
		{"jest", `{"testMatch":"**/*.test.ts"}`, "testMatch"},
		{"jest", `{"moduleFileExtensions":[1]}`, "moduleFileExtensions"},
		{"jest", `{"testRegex":"["}`, "testRegex"},
		{"vitest", `{"test":{"environment":"jsdom"}}`, "environment"},
		{"vitest", `{"plugins":[]}`, "plugins"},
		{"vitest", `{"test":{"setupFiles":["./setup.ts"]}}`, "setupFiles"},
		{"vitest", `{"test":{"pool":"threads"}}`, "pool"},
		{"vitest", `{"test":{"browser":{"enabled":true}}}`, "browser"},
		{"vitest", `{"test":{"globals":"true"}}`, "globals"},
		{"vitest", `{"test":{"include":null}}`, "include"},
		{"vitest", `{"test":{"testTimeout":1.5}}`, "testTimeout"},
		{"vitest", `{"test":{"hookTimeout":null}}`, "hookTimeout"},
		{"vitest", `{"esbuild":{"plugins":[]}}`, "esbuild"},
		{"vitest", `{"test":[]}`, "vitest.test"},
	} {
		t.Run(tc.framework+"/"+tc.config, func(t *testing.T) {
			root := t.TempDir()
			node727(t, root, tc.framework, 3)
			name := tc.framework + ".config.json"
			write727(t, root, name, tc.config)
			b, c, issues := draft727(t, root)
			p := c.Parts[0]
			has727(t, p.Inputs, name)
			missing727(t, p, "test")
			missing727(t, p, "test_one")
			issue727(t, issues, "test", name, tc.setting)
			refused727(t, root, b)
		})
	}
}

func TestRepositoryDraftNativeSettingsSources(t *testing.T) {
	for _, tc := range []struct {
		framework, config string
	}{
		{"jest", `{"testEnvironment":"node","testMatch":["**/*.test.ts"],"moduleFileExtensions":["ts","js"],"clearMocks":true,"resetMocks":false,"restoreMocks":true,"testTimeout":2500}`},
		{"vitest", `{"test":{"environment":"node","globals":true,"include":["**/*.test.ts"],"exclude":["**/generated/**"],"testTimeout":2500,"hookTimeout":3000}}`},
	} {
		for _, source := range []string{"explicit", "default", "package"} {
			if tc.framework != "jest" && source == "package" {
				continue
			}
			t.Run(tc.framework+"/"+source, func(t *testing.T) {
				root := t.TempDir()
				node727(t, root, tc.framework, 3)
				name := tc.framework + ".config.json"
				write727(t, root, name, tc.config)
				if source != "explicit" {
					mutatePackage727(t, root, func(pkg map[string]any) {
						command := "jest --runInBand"
						if tc.framework == "vitest" {
							command = "vitest run"
						}
						pkg["scripts"].(map[string]any)["test"] = command
					})
				}
				if source == "package" {
					if err := os.Remove(filepath.Join(root, name)); err != nil {
						t.Fatal(err)
					}
					mutatePackage727(t, root, func(pkg map[string]any) {
						pkg["jest"] = map[string]any{"testEnvironment": "node", "clearMocks": true}
					})
				}
				b, c, issues := draft727(t, root)
				if len(issues) != 0 {
					t.Fatal(issues)
				}
				selector727(t, c.Parts[0], tc.framework)
				if _, err := load727(t, root, b); err != nil {
					t.Fatal(err)
				}
				if source == "package" {
					mutatePackage727(t, root, func(pkg map[string]any) {
						pkg["jest"] = map[string]any{"testEnvironment": "jsdom"}
					})
					b, c, issues = draft727(t, root)
					missing727(t, c.Parts[0], "test")
					missing727(t, c.Parts[0], "test_one")
					issue727(t, issues, "package.json", "jest", "testEnvironment")
					refused727(t, root, b)
				}
			})
		}
	}
}

func TestRepositoryDraftPytestTestpathsBoundaries(t *testing.T) {
	for _, tc := range []struct {
		value string
		argv  []string
	}{
		{`"tests integration"`, nil},
		{`"tests"`, nil},
		{`'"tests integration"'`, nil},
		{`"tests\tintegration"`, nil},
		{`['tests', 'integration']`, []string{"tests", "integration"}},
		{`['tests integration']`, []string{"tests integration"}},
	} {
		t.Run(tc.value, func(t *testing.T) {
			root := t.TempDir()
			python727(t, root, "requirements.lock")
			write727(t, root, "pyproject.toml", "[tool.pytest.ini_options]\ntestpaths = "+tc.value+"\n")
			b, c, issues := draft727(t, root)
			has727(t, c.Parts[0].Inputs, "pyproject.toml")
			if tc.argv == nil {
				missing727(t, c.Parts[0], "test")
				missing727(t, c.Parts[0], "test_one")
				issue727(t, issues, "pyproject.toml", "testpaths", "literal array")
				refused727(t, root, b)
			} else {
				if len(issues) != 1 {
					t.Fatal(issues)
				}
				argv727(t, c.Parts[0], "test", append([]string{"python3", "-m", "pytest"}, tc.argv...)...)
				argv727(t, c.Parts[0], "test_one", "python3", "-m", "pytest", "{test}")
			}
		})
	}
}
