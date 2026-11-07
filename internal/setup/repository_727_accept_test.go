package setup_test

// DraftRepository(dir) ([]byte, []string, error) is the read-only drafting API.
// Its bytes are the proposed repository.json, not an enabled configuration
// until all requirements are resolved. Incomplete JSON has an extra top-level
// "unresolved" string array equal to the returned diagnostics; complete JSON
// omits it. The existing strict loader must keep refusing this extra field.
// Diagnostics identify the affected part/path and field and explain completion.
// Missing commands remain empty declarations, never not_applicable reasons.
//
// Discovery is conservative: plain command strings in package scripts may be
// tokenized (including quoted arguments), but shell control, expansions and
// opaque wrappers are not guessed. Conventional Go checks and locked pytest /
// Ruff declarations are evidence; an unknown build/format command needs a
// person. No helper files or dependency manifests are generated or executed.
// Existing setup/setup_test.go, setup/check_accept_test.go and the CLI's
// init_accept_test.go keep their pinning and modeled-setup assertions unchanged.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/setup"
	"github.com/gitdek/invariant/internal/toolchain"
)

func write727(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func json727(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func draft727(t *testing.T, root string) ([]byte, repository.Config, []string) {
	t.Helper()
	b, issues, err := setup.DraftRepository(root)
	if err != nil {
		t.Fatalf("drafting: %v", err)
	}
	var c repository.Config
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatalf("draft is not JSON: %v\n%s", err, b)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	if c.Version != 1 {
		t.Fatalf("draft version = %d", c.Version)
	}
	if len(issues) == 0 {
		if _, found := fields["unresolved"]; found {
			t.Fatal("complete draft still has an unresolved field")
		}
	} else {
		var saved []string
		if err := json.Unmarshal(fields["unresolved"], &saved); err != nil || !reflect.DeepEqual(saved, issues) {
			t.Fatalf("draft must retain every unresolved requirement: saved=%q issues=%q err=%v", saved, issues, err)
		}
	}
	for _, p := range c.Parts {
		if len(p.Commands) != 6 {
			t.Errorf("part %s has %d command fields", p.Directory, len(p.Commands))
		}
		for _, field := range []string{"install", "format", "lint", "build", "test", "test_one"} {
			if _, ok := p.Commands[field]; !ok {
				t.Errorf("part %s omits %s", p.Directory, field)
			}
		}
	}
	return b, c, issues
}

func part727(t *testing.T, c repository.Config, directory, language string) repository.Part {
	t.Helper()
	var found []repository.Part
	for _, p := range c.Parts {
		if p.Directory == directory && p.Language == language {
			found = append(found, p)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want one %s part at %s, got %+v", language, directory, c.Parts)
	}
	return found[0]
}

func has727(t *testing.T, got []string, want ...string) {
	t.Helper()
	for _, s := range want {
		if !slices.Contains(got, s) {
			t.Errorf("%q missing from %q", s, got)
		}
	}
}

func issue727(t *testing.T, issues []string, words ...string) {
	t.Helper()
	for _, issue := range issues {
		ok := true
		for _, word := range words {
			ok = ok && strings.Contains(strings.ToLower(issue), strings.ToLower(word))
		}
		if ok {
			return
		}
	}
	t.Errorf("no diagnostic identifies %q: %q", words, issues)
}

func argv727(t *testing.T, p repository.Part, field string, want ...string) {
	t.Helper()
	c := p.Commands[field]
	if c.NotApplicable != "" || !slices.Equal(c.Argv, want) {
		t.Errorf("%s %s = %+v; want argv %q", p.Directory, field, c, want)
	}
}

func missing727(t *testing.T, p repository.Part, field string) {
	t.Helper()
	c := p.Commands[field]
	if len(c.Argv) != 0 || c.NotApplicable != "" {
		t.Errorf("unresolved %s %s is disguised as a command or inapplicable check: %+v", p.Directory, field, c)
	}
}

func selector727(t *testing.T, p repository.Part, framework string) {
	t.Helper()
	var adapter plumbing.RepositoryAcceptanceAdapter
	var file, selector string
	switch framework {
	case "go":
		adapter, file, selector = plumbing.GoRepositoryAcceptanceAdapter{}, "ready_accept_test.go", "TestReady"
	case "pytest":
		adapter, file, selector = plumbing.PytestRepositoryAcceptanceAdapter{}, "tests/test_ready.py", "tests/test_ready.py::test_ready"
	case "vitest":
		adapter, file, selector = plumbing.VitestRepositoryAcceptanceAdapter{}, "tests/ready.test.ts", "ready works"
	case "jest":
		adapter, file, selector = plumbing.JestRepositoryAcceptanceAdapter{}, "tests/ready.test.ts", "ready works"
	default:
		t.Fatal(framework)
	}
	_, err := adapter.Select(context.Background(), p, plumbing.Test{Name: "ready", Says: "It works.", Part: p.Name, Framework: framework, File: path.Join(p.Directory, file), Selector: selector})
	if err != nil {
		t.Errorf("drafted %s selector refused by existing adapter: %v; command=%+v", framework, err, p.Commands["test_one"])
	}
}

func load727(t *testing.T, root string, data []byte) (*repository.Snapshot, error) {
	t.Helper()
	write727(t, root, repository.Path, string(data))
	for _, args := range [][]string{{"init", "-q"}, {"add", "--all"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-q", "--no-verify", "-m", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=maintenance.auto", "GIT_CONFIG_VALUE_0=false", "GIT_CONFIG_KEY_1=gc.auto", "GIT_CONFIG_VALUE_1=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %q: %v\n%s", args, err, out)
		}
	}
	return repository.Load(context.Background(), root, "HEAD")
}

func refused727(t *testing.T, root string, data []byte) {
	t.Helper()
	if _, err := load727(t, root, data); !errors.Is(err, repository.ErrInvalid) {
		t.Fatalf("incomplete committed draft must be invalid, got %v", err)
	}
}

const go727 = "module example.invalid/app\n\ngo 1.27.1\n"

func TestRepositoryDraft727Go(t *testing.T) {
	for _, withDependencies := range []bool{false, true} {
		t.Run(fmt.Sprint(withDependencies), func(t *testing.T) {
			root := t.TempDir()
			mod := go727
			if withDependencies {
				mod += "\nrequire example.invalid/dep v1.2.3\n"
				hash := "h1:" + strings.Repeat("A", 43) + "="
				write727(t, root, "go.sum", "example.invalid/dep v1.2.3 "+hash+"\nexample.invalid/dep v1.2.3/go.mod "+hash+"\n")
			}
			write727(t, root, "go.mod", mod)
			write727(t, root, "app.go", "package app\nconst Value = 1\n")
			b, c, issues := draft727(t, root)
			p := part727(t, c, ".", "go")
			if len(c.Parts) != 1 || p.Image != toolchain.GoImage {
				t.Errorf("Go discovery or maintained pin: %+v", c.Parts)
			}
			has727(t, p.Inputs, "go.mod")
			if withDependencies {
				has727(t, p.Lockfiles, "go.sum")
			} else if len(p.Lockfiles) != 0 {
				t.Errorf("invented lock: %q", p.Lockfiles)
			}
			argv727(t, p, "install", "go", "mod", "download")
			argv727(t, p, "lint", "go", "vet", "./...")
			argv727(t, p, "build", "go", "build", "./...")
			selector727(t, p, "go")
			if cmd := p.Commands["test"].Argv; len(cmd) < 3 || !slices.Equal(cmd[:3], []string{"go", "test", "./..."}) {
				t.Errorf("Go suite = %q", cmd)
			}
			missing727(t, p, "format")
			issue727(t, issues, "format")
			if len(issues) != 1 {
				t.Errorf("ordinary Go fixture should need only a failing formatting check, got %q", issues)
			}
			refused727(t, root, b)
		})
	}
}

func python727(t *testing.T, root, lock string) {
	t.Helper()
	write727(t, root, "pyproject.toml", "[project]\nname = \"worker\"\nversion = \"1.0.0\"\nrequires-python = \">=3.13,<3.14\"\n[tool.pytest.ini_options]\ntestpaths = [\"tests\"]\n[tool.ruff]\nline-length = 88\n")
	write727(t, root, lock, "# Generated hash-locked requirements\npytest==8.3.5 \\\n    --hash=sha256:"+strings.Repeat("a", 64)+"\nruff==0.9.10 --hash=sha256:"+strings.Repeat("b", 64)+"\n")
	write727(t, root, "tests/test_worker.py", "def test_worker():\n    assert True\n")
}

func TestRepositoryDraft727Python(t *testing.T) {
	for _, lock := range []string{"requirements.txt", "requirements.lock"} {
		t.Run(lock, func(t *testing.T) {
			root := t.TempDir()
			python727(t, root, lock)
			if lock == "requirements.lock" {
				write727(t, root, "requirements.txt", "pytest>=8\nruff>=0.9\n")
			}
			b, c, issues := draft727(t, root)
			p := part727(t, c, ".", "python")
			if p.Image != toolchain.PythonImage {
				t.Errorf("Python pin = %q", p.Image)
			}
			has727(t, p.Inputs, "pyproject.toml")
			has727(t, p.Lockfiles, lock)
			if lock == "requirements.lock" {
				has727(t, p.Inputs, "requirements.txt")
				if slices.Contains(p.Lockfiles, "requirements.txt") {
					t.Error("floating source requirements became a second lock")
				}
			}
			install := p.Commands["install"].Argv
			if len(install) < 6 || !slices.Equal(install[:4], []string{"python3", "-m", "pip", "install"}) {
				t.Fatalf("pip installation = %q", install)
			}
			found := false
			for i := 0; i+1 < len(install); i++ {
				if (install[i] == "-r" || install[i] == "--requirement") && install[i+1] == lock {
					found = true
				}
			}
			if !found {
				t.Errorf("install lacks part-relative lock %s: %q", lock, install)
			}
			selector727(t, p, "pytest")
			if got := p.Commands["test"].Argv; !slices.Contains(got, "pytest") || !slices.Contains(got, "tests") {
				t.Errorf("pytest suite lost declared testpaths: %q", got)
			}
			argv727(t, p, "format", "ruff", "format", "--check", ".")
			argv727(t, p, "lint", "ruff", "check", ".")
			missing727(t, p, "build")
			issue727(t, issues, "build")
			if len(issues) != 1 {
				t.Errorf("only the unknown build should need completion: %q", issues)
			}
			refused727(t, root, b)
		})
	}
}

func node727(t *testing.T, root, framework string, version int) {
	t.Helper()
	deps := map[string]string{"prettier": "3.5.0", "eslint": "9.20.0", "typescript": "5.7.3", framework: "3.0.0"}
	test := framework
	if framework == "vitest" {
		test += " run"
	} else {
		test += " --runInBand"
	}
	test += " --config " + framework + ".config.json --testTimeout 2500"
	pkg := map[string]any{"name": "fixture", "version": "1.0.0", "private": true, "engines": map[string]string{"node": ">=24 <25"}, "devDependencies": deps,
		"scripts": map[string]string{"format:check": `prettier --check "src/**/*.ts"`, "lint": "eslint .", "build": "tsc --noEmit", "test": test}}
	packages := map[string]any{"": map[string]any{"name": "fixture", "version": "1.0.0", "devDependencies": deps}}
	for name, v := range deps {
		packages["node_modules/"+name] = map[string]any{"version": v, "resolved": "https://registry.npmjs.org/" + name + "/-/" + name + "-" + v + ".tgz", "integrity": "sha512-" + strings.Repeat("A", 86) + "=="}
	}
	write727(t, root, "package.json", json727(t, pkg))
	write727(t, root, "package-lock.json", json727(t, map[string]any{"name": "fixture", "version": "1.0.0", "lockfileVersion": version, "packages": packages}))
	write727(t, root, framework+".config.json", "{}\n")
	write727(t, root, "tsconfig.json", "{}\n")
	write727(t, root, "src/app.ts", "export const value = 1;\n")
}

func TestRepositoryDraft727Node(t *testing.T) {
	for _, framework := range []string{"vitest", "jest"} {
		for _, version := range []int{2, 3} {
			t.Run(fmt.Sprintf("%s-v%d", framework, version), func(t *testing.T) {
				root := t.TempDir()
				node727(t, root, framework, version)
				b, c, issues := draft727(t, root)
				if len(issues) != 0 {
					t.Fatalf("fully specified npm project is unresolved: %q", issues)
				}
				p := part727(t, c, ".", "typescript")
				if p.Image != toolchain.NodeDepsBase && p.Image != toolchain.NodeImage {
					t.Errorf("not a maintained Node pin: %q", p.Image)
				}
				has727(t, p.Inputs, "package.json")
				has727(t, p.Lockfiles, "package-lock.json")
				install := p.Commands["install"].Argv
				if len(install) < 2 || !slices.Equal(install[:2], []string{"npm", "ci"}) {
					t.Errorf("npm install = %q", install)
				}
				argv727(t, p, "format", "prettier", "--check", "src/**/*.ts")
				argv727(t, p, "lint", "eslint", ".")
				argv727(t, p, "build", "tsc", "--noEmit")
				selector727(t, p, framework)
				for _, field := range []string{"test", "test_one"} {
					has727(t, p.Commands[field].Argv, "--config", framework+".config.json", "--testTimeout", "2500")
				}
				s, err := load727(t, root, b)
				if err != nil {
					t.Fatalf("complete committed draft rejected: %v\n%s", err, b)
				}
				owners, err := s.PartsFor([]string{"src/app.ts"})
				if err != nil || len(owners) != 1 || owners[0].Name != p.Name {
					t.Fatalf("root ownership: %+v, %v", owners, err)
				}
			})
		}
	}
}

func TestRepositoryDraft727Unresolved(t *testing.T) {
	// Every case is incomplete for the stated reason even where another field,
	// such as Go formatting, independently needs completion.
	cases := []struct {
		name, field, evidence string
		files                 map[string]string
		remove                string
	}{
		{"Go missing sum", "install", "go.sum", map[string]string{"go.mod": go727 + "require example.invalid/dep v1.2.3\n"}, ""},
		{"Go incomplete sum", "install", "go.sum", map[string]string{"go.mod": go727 + "require example.invalid/dep v1.2.3\n", "go.sum": "example.invalid/dep v1.2.3 h1:" + strings.Repeat("A", 43) + "=\n"}, ""},
		{"Go replacement", "install", "replace", map[string]string{"go.mod": go727 + "replace example.invalid/dep => ../dep\n"}, ""},
		{"Go workspace", "install", "go.work", map[string]string{"go.mod": go727, "go.work": "go 1.27.1\nuse .\n"}, ""},
		{"Go newer toolchain", "image", "toolchain", map[string]string{"go.mod": "module fixture\n\ngo 1.99.0\n"}, ""},
		{"Python floating", "install", "requirements.txt", map[string]string{"requirements.txt": "pytest>=8\n"}, ""},
		{"Python unhashed", "install", "requirements.txt", map[string]string{"requirements.txt": "pytest==8.3.5\n"}, ""},
		{"Python recursive", "install", "requirements.txt", map[string]string{"requirements.txt": "-r other.txt\n", "other.txt": "pytest==8.3.5 --hash=sha256:" + strings.Repeat("a", 64) + "\n"}, ""},
		{"Python missing lock", "install", "lock", map[string]string{"pyproject.toml": "[project]\nname=\"app\"\nrequires-python=\">=3.13\"\n"}, ""},
		{"Python uv", "install", "uv.lock", map[string]string{"pyproject.toml": "[project]\nname=\"app\"\n", "uv.lock": "version = 1\n"}, ""},
		{"Python poetry", "install", "poetry.lock", map[string]string{"pyproject.toml": "[tool.poetry]\nname=\"app\"\n", "poetry.lock": "[[package]]\nname=\"pytest\"\nversion=\"8.3.5\"\n"}, ""},
		{"Python pipenv", "install", "Pipfile.lock", map[string]string{"requirements.txt": "pytest\n", "Pipfile.lock": "{}"}, ""},
		{"npm missing lock", "install", "lock", nil, "package-lock.json"},
		{"npm old lock", "install", "package-lock.json", map[string]string{"package-lock.json": `{"lockfileVersion":1}`}, ""},
		{"npm bad integrity", "install", "package-lock.json", map[string]string{"package-lock.json": `{"name":"fixture","version":"1.0.0","lockfileVersion":3,"packages":{"":{"name":"fixture","version":"1.0.0"}}}`}, ""},
		{"npm shrinkwrap", "install", "npm-shrinkwrap.json", map[string]string{"npm-shrinkwrap.json": "{}"}, ""},
		{"npm pnpm", "install", "pnpm-lock.yaml", map[string]string{"pnpm-lock.yaml": "lockfileVersion: '9.0'\n"}, "package-lock.json"},
		{"npm yarn", "install", "yarn.lock", map[string]string{"yarn.lock": "# yarn lockfile v1\n"}, "package-lock.json"},
		{"npm bun", "install", "bun.lock", map[string]string{"bun.lock": "{}"}, "package-lock.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if strings.HasPrefix(tc.name, "npm ") {
				node727(t, root, "vitest", 3)
			}
			for name, body := range tc.files {
				write727(t, root, name, body)
			}
			if tc.remove != "" {
				if err := os.Remove(filepath.Join(root, tc.remove)); err != nil {
					t.Fatal(err)
				}
			}
			b, c, issues := draft727(t, root)
			if len(c.Parts) != 1 {
				t.Fatalf("project disappeared: %+v", c.Parts)
			}
			p := c.Parts[0]
			issue727(t, issues, tc.evidence)
			if _, exists := tc.files[tc.evidence]; exists && strings.Contains(tc.evidence, "lock") {
				has727(t, append(append([]string{}, p.Inputs...), p.Lockfiles...), tc.evidence)
			}
			if tc.field == "image" {
				if p.Image != "" {
					t.Errorf("invented compatibility for %q", p.Image)
				}
			} else {
				missing727(t, p, tc.field)
			}
			if strings.Contains(tc.name, "floating") || strings.Contains(tc.name, "unhashed") {
				if slices.Contains(p.Lockfiles, "requirements.txt") {
					t.Errorf("floating/unhashed requirements classified as lock: %+v", p)
				}
				has727(t, p.Inputs, "requirements.txt")
			}
			refused727(t, root, b)
		})
	}
	for _, script := range []string{"custom-check", "vitest run && echo success", "vitest run $(touch executed727)", "npm run unit", "vitest --watch", "jest --listTests", "npx vitest run"} {
		t.Run("ambiguous test "+script, func(t *testing.T) {
			root := t.TempDir()
			node727(t, root, "vitest", 3)
			mutatePackage727(t, root, func(pkg map[string]any) { pkg["scripts"].(map[string]any)["test"] = script })
			b, c, issues := draft727(t, root)
			issue727(t, issues, "test")
			missing727(t, c.Parts[0], "test_one")
			refused727(t, root, b)
		})
	}
	for _, script := range []string{"prettier --write .", "prettier --list-different . || true", "gofmt -l .", "gofmt -d ."} {
		t.Run("unsafe format "+script, func(t *testing.T) {
			root := t.TempDir()
			node727(t, root, "vitest", 3)
			mutatePackage727(t, root, func(pkg map[string]any) { pkg["scripts"].(map[string]any)["format:check"] = script })
			b, c, issues := draft727(t, root)
			issue727(t, issues, "format")
			missing727(t, c.Parts[0], "format")
			refused727(t, root, b)
		})
	}
	for _, name := range []string{"unknown Node version", "unknown Python version", "no project", "co-located languages", "unowned files"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			switch name {
			case "unknown Node version":
				node727(t, root, "vitest", 3)
				mutatePackage727(t, root, func(pkg map[string]any) { pkg["engines"] = map[string]any{"node": ">=99"} })
			case "unknown Python version":
				python727(t, root, "requirements.txt")
				write727(t, root, "pyproject.toml", "[project]\nname=\"app\"\nrequires-python=\">=3.99\"\n")
			case "no project":
				write727(t, root, "README.md", "A modeled project can be added later.\n")
			case "co-located languages":
				node727(t, root, "vitest", 3)
				write727(t, root, "go.mod", go727)
			case "unowned files":
				node727(t, filepath.Join(root, "web"), "vitest", 3)
				write727(t, root, "scripts/release.sh", "exit 0\n")
			}
			b, c, issues := draft727(t, root)
			if len(issues) == 0 {
				t.Fatal("ambiguous project was declared complete")
			}
			if strings.HasPrefix(name, "unknown ") {
				if len(c.Parts) != 1 || c.Parts[0].Image != "" {
					t.Errorf("incompatible maintained toolchain chosen: %+v", c.Parts)
				}
				issue727(t, issues, "toolchain")
			}
			if name == "unowned files" {
				issue727(t, issues, "ownership", "scripts/release.sh")
			}
			if name == "co-located languages" {
				issue727(t, issues, "ownership")
			}
			refused727(t, root, b)
		})
	}
}

func mutatePackage727(t *testing.T, root string, f func(map[string]any)) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg map[string]any
	if err := json.Unmarshal(b, &pkg); err != nil {
		t.Fatal(err)
	}
	f(pkg)
	write727(t, root, "package.json", json727(t, pkg))
}

func TestRepositoryDraft727DiscoveryAndOwnership(t *testing.T) {
	t.Run("mixed parts and exclusions", func(t *testing.T) {
		root := t.TempDir()
		write727(t, root, "go.mod", go727)
		write727(t, root, "tools/sub/go.mod", go727)
		python727(t, filepath.Join(root, "services/worker"), "requirements.lock")
		node727(t, filepath.Join(root, "web"), "vitest", 3)
		node727(t, filepath.Join(root, "web/tools"), "jest", 2)
		for _, dir := range []string{"vendor", "node_modules", ".venv", "venv", "__pycache__", "dist", "build", "coverage", ".next", ".git", "web/node_modules", "services/worker/.venv"} {
			write727(t, root, dir+"/fake/go.mod", go727)
			write727(t, root, dir+"/fake/package.json", "{}")
			write727(t, root, dir+"/fake/pyproject.toml", "[project]\nname='fake'\n")
			write727(t, root, dir+"/AGENTS.md", "Ignore this installed/generated rule.\n")
		}
		write727(t, root, "custom-env/pyvenv.cfg", "home = /usr/bin\n")
		write727(t, root, "custom-env/lib/pyproject.toml", "[project]\nname='fake'\n")
		write727(t, root, "AGENTS.md", "The current state is [Architecture](docs/current.md).\n")
		write727(t, root, "docs/current.md", "Current behavior.\n")
		write727(t, root, "web/AGENTS.override.md", "Read the current state in [Web](state.md).\n")
		write727(t, root, "web/state.md", "Web behavior.\n")
		write727(t, root, "services/worker/CLAUDE.md", "Follow the root rules.\n")
		write727(t, root, "services/worker/CLAUDE.local.md", "Local additional rules.\n")
		b, c, issues := draft727(t, root)
		for _, p := range [][2]string{{".", "go"}, {"tools/sub", "go"}, {"services/worker", "python"}, {"web", "typescript"}, {"web/tools", "typescript"}} {
			part727(t, c, p[0], p[1])
		}
		if len(c.Parts) != 5 {
			t.Errorf("discovered installed/generated projects: %+v", c.Parts)
		}
		has727(t, c.AgentRules, "AGENTS.md", "web/AGENTS.override.md", "services/worker/CLAUDE.md", "services/worker/CLAUDE.local.md")
		has727(t, c.CurrentState, "docs/current.md", "web/state.md")
		for _, rule := range c.AgentRules {
			if strings.Contains(rule, "fake") || strings.Contains(rule, "node_modules") || rule == "vendor/AGENTS.md" {
				t.Errorf("excluded rule: %s", rule)
			}
		}
		paths727(t, root, c)
		again, c2, issues2 := draft727(t, root)
		if string(b) != string(again) || !reflect.DeepEqual(c, c2) || !reflect.DeepEqual(issues, issues2) {
			t.Fatal("discovery is not deterministic")
		}
	})
	t.Run("shared input and deepest owner", func(t *testing.T) {
		root := t.TempDir()
		for _, dir := range []string{"apps/one", "apps/one/tools", "apps/two"} {
			node727(t, filepath.Join(root, dir), "vitest", 3)
		}
		write727(t, root, "tsconfig.base.json", "{}\n")
		write727(t, root, "apps/one/tsconfig.json", `{"extends":"../../tsconfig.base.json"}`)
		write727(t, root, "apps/two/tsconfig.json", `{"extends":"../../tsconfig.base.json"}`)
		write727(t, root, "AGENTS.md", "The current state is [Design](docs/state.md).\n")
		write727(t, root, "docs/state.md", "Current state.\n")
		b, c, issues := draft727(t, root)
		if len(issues) != 0 {
			t.Fatalf("documented three-part project unresolved: %q", issues)
		}
		paths727(t, root, c)
		s, err := load727(t, root, b)
		if err != nil {
			t.Fatalf("complete shared contract: %v\n%s", err, b)
		}
		for _, tc := range []struct {
			file string
			dirs []string
		}{
			{"apps/one/src/new.ts", []string{"apps/one"}},
			{"apps/one/tools/src/new.ts", []string{"apps/one/tools"}},
			{"tsconfig.base.json", []string{"apps/one", "apps/two"}},
			{"docs/state.md", []string{"apps/one", "apps/one/tools", "apps/two"}},
			{"AGENTS.md", []string{"apps/one", "apps/one/tools", "apps/two"}},
		} {
			parts, err := s.PartsFor([]string{tc.file})
			if err != nil {
				t.Fatal(err)
			}
			var dirs []string
			for _, p := range parts {
				dirs = append(dirs, p.Directory)
			}
			sort.Strings(dirs)
			if !slices.Equal(dirs, tc.dirs) {
				t.Errorf("owners of %s = %q, want %q", tc.file, dirs, tc.dirs)
			}
		}
	})
}

func paths727(t *testing.T, root string, c repository.Config) {
	t.Helper()
	check := func(name string, directory bool) {
		if name == "." && directory {
			return
		}
		clean := strings.TrimSuffix(name, "/")
		if clean == "" || path.IsAbs(clean) || path.Clean(clean) != clean || strings.HasPrefix(clean, "../") || strings.ContainsAny(clean, "\\\x00\r\n") || strings.Contains("/"+clean+"/", "/.git/") {
			t.Errorf("unsafe repository path %q", name)
			return
		}
		for dir := clean; dir != "."; dir = path.Dir(dir) {
			info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(dir)))
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				t.Errorf("nonregular discovery path %s: %v", dir, err)
				return
			}
		}
	}
	for _, list := range [][]string{c.AgentRules, c.CurrentState, c.TrustedPaths} {
		for _, p := range list {
			check(p, false)
		}
	}
	for _, p := range c.Parts {
		check(p.Directory, true)
		for _, name := range append(append([]string{}, p.Inputs...), p.Lockfiles...) {
			check(name, false)
		}
		if p.Image != "" && !regexp.MustCompile(`@sha256:[0-9a-f]{64}$`).MatchString(p.Image) {
			t.Errorf("floating image %q", p.Image)
		}
	}
	for _, s := range c.SharedPaths {
		check(s.Path, false)
	}
}

func TestRepositoryDraft727NeverExecutes(t *testing.T) {
	root, outside, bin := t.TempDir(), t.TempDir(), t.TempDir()
	node727(t, root, "vitest", 3)
	write727(t, outside, "go.mod", go727)
	if err := os.Symlink(outside, filepath.Join(root, "linked-project")); err != nil {
		t.Fatal(err)
	}
	write727(t, root, "linked-manifest/keep.txt", "keep\n")
	if err := os.Symlink(filepath.Join(outside, "go.mod"), filepath.Join(root, "linked-manifest/go.mod")); err != nil {
		t.Fatal(err)
	}
	write727(t, root, "setup.py", "raise RuntimeError('do not execute repository code')\n")
	write727(t, root, "sitecustomize.py", "raise RuntimeError('do not import repository code')\n")
	mutatePackage727(t, root, func(pkg map[string]any) { pkg["scripts"].(map[string]any)["postinstall"] = "touch executed727" })
	seen := filepath.Join(outside, "executed")
	t.Setenv("DRAFT727_EXECUTIONS", seen)
	for _, name := range []string{"git", "go", "gofmt", "npm", "npx", "node", "python", "python3", "pip", "uv", "poetry", "pnpm", "yarn", "docker", "make", "sh"} {
		write727(t, bin, name, "#!/bin/sh\nprintf '%s\\n' \"$0\" >> \"$DRAFT727_EXECUTIONS\"\nexit 97\n")
		if err := os.Chmod(filepath.Join(bin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	before := tree727(t, root)
	t.Setenv("PATH", bin)
	_, c, _ := draft727(t, root)
	part727(t, c, ".", "typescript")
	for _, p := range c.Parts {
		if strings.HasPrefix(p.Directory, "linked-") {
			t.Errorf("followed linked project: %+v", p)
		}
	}
	paths727(t, root, c)
	if !reflect.DeepEqual(before, tree727(t, root)) {
		t.Fatal("read-only discovery changed the checkout")
	}
	if _, err := os.Stat(seen); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("discovery executed a tool: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "executed727")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("discovery executed a lifecycle hook: %v", err)
	}
}

func tree727(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(name string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		if e.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(name)
			result[rel] = "link:" + target
			return err
		}
		if e.IsDir() {
			result[rel] = "directory"
			return nil
		}
		b, err := os.ReadFile(name)
		result[rel] = "file:" + string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
