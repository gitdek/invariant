package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// Public contract for #461:
//
// Load(ctx, checkout, baseRef) (*Snapshot, error) resolves an explicit nonempty
// commit/ref once and reads .invariant/repository.json from that Git tree, never
// from the index or working tree. ErrMissing identifies ONLY an absent contract
// in a valid base tree; ErrInvalid identifies invalid configuration. Neither an
// invalid revision nor a read failure is ErrMissing. No command is executed.
//
// Snapshot.Commit() string returns the resolved commit ID. Config() Config
// returns an independent value with the JSON shape below. ReadFile(path)
// returns independent bytes from the captured tree. PartsFor([]string)
// ([]Part, error) returns independent parts, sorted uniquely by Part.Name.
// IsTrusted(path) (bool, error) checks the captured trust policy. None of these
// methods rereads a ref, the candidate's configuration or candidate file bytes.
//
// Version 1 uses repository-relative slash paths throughout. Each part has a
// unique name and directory ("." permits the root), language, image, explicit
// lockfiles and inputs arrays (empty is allowed), and all six commands below.
// Commands run in the part's directory, and contain either a nonempty argv
// array or a nonblank not_applicable reason, never both. test_one's argv has
// exactly one {test} placeholder in one argument for the language's named-test
// selector; it is data for a later runner, never shell interpolation here.
// An inapplicable test_one is explicit just like any other command. Reject
// unknown/duplicate JSON keys, trailing JSON, unsupported versions/languages,
// invalid image digests, absent required fields and contradictory commands.
//
// Part directories and required rules, current-state documents, lockfiles and
// inputs exist in the commit. The latter are regular blobs. A trusted/shared
// selector is an exact file or a directory prefix ending in '/'; a prefix
// may reserve future files. Selectors cannot contain globs. All paths are clean,
// nonempty and inside the repo, with no backslashes, control characters, Git
// metadata, symlink ancestors, symlink leaves or gitlinks. Only part.directory
// permits '.'. Unrelated links in a repository do not invalidate its contract.
//
// For each touched file, take the deepest enclosing part, union any explicit
// shared owners and every part naming that file as a lockfile or input.
// Configuration, agent_rules and current_state files select every part.
// Shared rules list owners explicitly; duplicate/overlapping selectors are
// rejected, as are duplicate names/directories and nonexistent owner names.
// New files need not exist, and deleted files use this same base ownership.
// A nonempty selection containing ANY unowned/unsafe path fails as a whole.
// An empty selection is empty. Trust includes the config, agent_rules,
// lockfiles, inputs and trusted_paths, but does not implicitly trust all code
// or current-state documents. Config and result mutations cannot weaken it.
//
// This is a loader, not external plumbing enablement. Existing Invariant
// plumbing remains the explicit built-in runner/policy path, requiring no new
// file; this loader has no automatic Invariant or modeled-project fallback.

const contractPath461 = ".invariant/repository.json"

func command461(args ...string) map[string]any {
	return map[string]any{"argv": args}
}

func part461(name, dir, language string) map[string]any {
	root := ""
	if dir != "." {
		root = dir + "/"
	}
	p := map[string]any{
		"name": name, "directory": dir, "language": language,
		"image": "registry.example/toolchain@sha256:" + strings.Repeat("a", 64),
	}
	var commands map[string]any
	switch language {
	case "go":
		p["lockfiles"], p["inputs"] = []string{root + "go.sum"}, []string{root + "go.mod"}
		commands = map[string]any{
			"install":  command461("go", "mod", "download"),
			"format":   command461("gofmt", "-l", "."),
			"lint":     command461("go", "vet", "./..."),
			"build":    command461("go", "build", "./..."),
			"test":     command461("go", "test", "./..."),
			"test_one": command461("go", "test", "./...", "-run", "^{test}$", "-count=1"),
		}
	case "python":
		p["lockfiles"], p["inputs"] = []string{root + "uv.lock"}, []string{root + "pyproject.toml"}
		commands = map[string]any{
			"install":  command461("uv", "sync", "--frozen"),
			"format":   command461("ruff", "format", "--check", "."),
			"lint":     command461("ruff", "check", "."),
			"build":    map[string]any{"not_applicable": "This application has no distribution artifact."},
			"test":     command461("uv", "run", "pytest"),
			"test_one": command461("uv", "run", "pytest", "{test}"),
		}
	case "typescript":
		p["lockfiles"], p["inputs"] = []string{root + "package-lock.json"}, []string{root + "package.json"}
		commands = map[string]any{
			"install":  command461("npm", "ci"),
			"format":   command461("npm", "run", "format:check"),
			"lint":     command461("npm", "run", "lint"),
			"build":    command461("npm", "run", "build"),
			"test":     command461("npm", "test"),
			"test_one": command461("npm", "test", "--", "{test}"),
		}
	}
	p["commands"] = commands
	return p
}

func contract461(parts ...map[string]any) map[string]any {
	return map[string]any{
		"version": 1, "parts": parts,
		"agent_rules":   []string{"AGENTS.md"},
		"current_state": []string{"SPEC.md", "README.md"},
		"trusted_paths": []string{"tools/"},
		"shared_paths":  []map[string]any{},
	}
}

func write461(t *testing.T, root, name, text string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func json461(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func git461(t *testing.T, root string, args ...string) string {
	t.Helper()
	all := append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgSign=false",
		"-c", "user.name=Repository contract fixture", "-c", "user.email=fixture@example.invalid"}, args...)
	c := exec.Command("git", all...)
	c.Dir = root
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commit461(t *testing.T, root string) string {
	t.Helper()
	git461(t, root, "add", "--all")
	git461(t, root, "commit", "--quiet", "--allow-empty", "--no-verify", "-m", "fixture")
	return git461(t, root, "rev-parse", "HEAD")
}

// These tiny repositories require Git only; their declared commands are never
// run, and no language tools, image registry or network access are needed.
func repo461(t *testing.T, config map[string]any) string {
	t.Helper()
	root := t.TempDir()
	git461(t, root, "init", "--quiet", "-b", "main")
	for name, body := range map[string]string{
		"AGENTS.md": "Read SPEC.md before changing code.\n",
		"SPEC.md":   "The current application.\n", "README.md": "The front page.\n",
		"tools/check.sh": "exit 0\n", "shared/schema.json": "{}\n",
		"workspace.env": "MODE=test\n", "pnpm-lock.yaml": "lockfileVersion: '9.0'\n",
	} {
		write461(t, root, name, body)
	}
	if config != nil {
		for _, p := range config["parts"].([]map[string]any) {
			prefix := ""
			if p["directory"] != "." {
				prefix = p["directory"].(string) + "/"
			}
			for name, body := range map[string]string{
				"go.mod": "module fixture\n\ngo 1.27.1\n", "go.sum": "",
				"pyproject.toml": "[project]\nname = 'fixture'\nversion = '0.0.1'\n",
				"uv.lock":        "version = 1\n", "package.json": "{\"name\":\"fixture\",\"private\":true}\n",
				"package-lock.json": "{\"lockfileVersion\":3}\n", "src/old.go": "package fixture\n",
				"src/old.ts": "export const old = true;\n", "src/old.py": "old = True\n",
			} {
				write461(t, root, prefix+name, body)
			}
		}
		write461(t, root, contractPath461, json461(t, config))
	}
	return root
}

func load461(t *testing.T, root, base string) *Snapshot {
	t.Helper()
	s, err := Load(context.Background(), root, base)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func parts461(t *testing.T, s *Snapshot, paths []string, want ...string) {
	t.Helper()
	parts, err := s.PartsFor(paths)
	if err != nil {
		t.Fatalf("owners of %q: %v", paths, err)
	}
	got := make([]string, 0, len(parts))
	for _, p := range parts {
		got = append(got, p.Name)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("owners of %q = %q, want %q", paths, got, want)
	}
}

func trusted461(t *testing.T, s *Snapshot, path string, want bool) {
	t.Helper()
	got, err := s.IsTrusted(path)
	if err != nil || got != want {
		t.Errorf("trust of %q = %v, %v; want %v", path, got, err, want)
	}
}

func read461(t *testing.T, s *Snapshot, name, want string) {
	t.Helper()
	b, err := s.ReadFile(name)
	if err != nil || string(b) != want {
		t.Fatalf("base bytes of %s = %q, %v; want %q", name, b, err, want)
	}
}

// Path lists and part declarations are sets; argv order is significant.
func canonical461(v any, key string) any {
	switch x := v.(type) {
	case map[string]any:
		for k, value := range x {
			x[k] = canonical461(value, k)
		}
	case []any:
		for i := range x {
			x[i] = canonical461(x[i], "")
		}
		if key != "argv" {
			sort.Slice(x, func(i, j int) bool {
				a, _ := json.Marshal(x[i])
				b, _ := json.Marshal(x[j])
				return string(a) < string(b)
			})
		}
	}
	return v
}

func sameConfig461(t *testing.T, got, want any) {
	t.Helper()
	var a, b any
	if err := json.Unmarshal([]byte(json461(t, got)), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(json461(t, want)), &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(canonical461(a, ""), canonical461(b, "")) {
		t.Fatalf("captured configuration differs:\n got %s\nwant %s", json461(t, got), json461(t, want))
	}
}

func mixed461() map[string]any {
	api, web := part461("api", "api", "go"), part461("web", "web", "typescript")
	api["inputs"] = []string{"api/go.mod", "workspace.env"}
	web["inputs"], web["lockfiles"] = []string{"web/package.json", "workspace.env"}, []string{"pnpm-lock.yaml"}
	c := contract461(api, web)
	c["shared_paths"] = []map[string]any{{"path": "shared/", "parts": []string{"api", "web"}}}
	return c
}

func TestRepositoryContractLanguages(t *testing.T) {
	for _, language := range []string{"go", "python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			c := contract461(part461("app", ".", language))
			root := repo461(t, c)
			base := commit461(t, root)
			s := load461(t, root, "refs/heads/main")
			if s.Commit() != base {
				t.Fatalf("captured commit = %q, want %q", s.Commit(), base)
			}
			sameConfig461(t, s.Config(), c)
			parts461(t, s, []string{"src/new.file"}, "app")
			read461(t, s, "AGENTS.md", "Read SPEC.md before changing code.\n")
		})
	}
	t.Run("mixed", func(t *testing.T) {
		c := mixed461()
		root := repo461(t, c)
		s := load461(t, root, commit461(t, root))
		sameConfig461(t, s.Config(), c)
		parts461(t, s, []string{"api/src/new.go", "web/src/new.ts"}, "api", "web")
	})
	t.Run("explicit inapplicability and no dependency inputs", func(t *testing.T) {
		p := part461("app", ".", "go")
		p["lockfiles"], p["inputs"] = []string{}, []string{}
		commands := p["commands"].(map[string]any)
		commands["install"] = map[string]any{"not_applicable": "Only the standard library is used."}
		commands["lint"] = map[string]any{"not_applicable": "No separate linter is configured."}
		c := contract461(p)
		root := repo461(t, c)
		sameConfig461(t, load461(t, root, commit461(t, root)).Config(), c)
	})
}

func TestRepositoryContractMissingIsDistinct(t *testing.T) {
	root := repo461(t, nil)
	base := commit461(t, root)
	_, err := Load(context.Background(), root, base)
	if !errors.Is(err, ErrMissing) || errors.Is(err, ErrInvalid) {
		t.Fatalf("missing committed contract = %v; want only ErrMissing", err)
	}
	write461(t, root, contractPath461, json461(t, contract461(part461("app", ".", "go"))))
	git461(t, root, "add", "--all") // Even the index cannot supply authority.
	if _, err := Load(context.Background(), root, base); !errors.Is(err, ErrMissing) {
		t.Fatalf("candidate-only configuration replaced missing base: %v", err)
	}
	for _, ref := range []string{"", "no-such-base", "--help"} {
		if _, err := Load(context.Background(), root, ref); err == nil || errors.Is(err, ErrMissing) {
			t.Errorf("invalid base %q = %v; must not look like an absent config", ref, err)
		}
	}
	write461(t, root, contractPath461, "{broken")
	_, err = Load(context.Background(), root, commit461(t, root))
	if !errors.Is(err, ErrInvalid) || errors.Is(err, ErrMissing) || !strings.Contains(err.Error(), contractPath461) {
		t.Fatalf("malformed configuration = %v; want ErrInvalid naming its path", err)
	}
}

func TestRepositoryContractRejectsInvalidDeclarations(t *testing.T) {
	cases := []struct {
		name, diagnostic string
		change           func(map[string]any, map[string]any)
	}{
		{"version", "version", func(c, p map[string]any) { c["version"] = 2 }},
		{"missing version", "version", func(c, p map[string]any) { delete(c, "version") }},
		{"no parts", "parts", func(c, p map[string]any) { c["parts"] = []map[string]any{} }},
		{"missing name", "name", func(c, p map[string]any) { delete(p, "name") }},
		{"blank name", "name", func(c, p map[string]any) { p["name"] = "  " }},
		{"missing directory", "directory", func(c, p map[string]any) { delete(p, "directory") }},
		{"language", "language", func(c, p map[string]any) { p["language"] = "ruby" }},
		{"mutable tag", "image", func(c, p map[string]any) { p["image"] = "node:22" }},
		{"empty image", "image", func(c, p map[string]any) { p["image"] = "" }},
		{"short digest", "image", func(c, p map[string]any) { p["image"] = "node@sha256:abcdef" }},
		{"nonhex digest", "image", func(c, p map[string]any) { p["image"] = "node@sha256:" + strings.Repeat("z", 64) }},
		{"missing inputs", "inputs", func(c, p map[string]any) { delete(p, "inputs") }},
		{"null inputs", "inputs", func(c, p map[string]any) { p["inputs"] = nil }},
		{"missing lockfiles", "lockfiles", func(c, p map[string]any) { delete(p, "lockfiles") }},
		{"duplicate lockfile", "go.sum", func(c, p map[string]any) { p["lockfiles"] = []string{"go.sum", "go.sum"} }},
		{"unknown field", "surprise", func(c, p map[string]any) { c["surprise"] = true }},
		{"unknown part field", "surprise", func(c, p map[string]any) { p["surprise"] = true }},
		{"missing commands", "commands", func(c, p map[string]any) { delete(p, "commands") }},
		{"unknown check", "surprise", func(c, p map[string]any) { p["commands"].(map[string]any)["surprise"] = command461("true") }},
		{"two owners of directory", "directory", func(c, p map[string]any) { c["parts"] = []map[string]any{p, part461("other", ".", "go")} }},
		{"duplicate part name", "name", func(c, p map[string]any) { c["parts"] = []map[string]any{p, part461("app", "other", "go")} }},
		{"unknown shared owner", "ghost", func(c, p map[string]any) {
			c["shared_paths"] = []map[string]any{{"path": "shared/", "parts": []string{"ghost"}}}
		}},
		{"shared without owners", "shared", func(c, p map[string]any) {
			c["shared_paths"] = []map[string]any{{"path": "shared/", "parts": []string{}}}
		}},
		{"duplicate shared path", "shared", func(c, p map[string]any) {
			c["shared_paths"] = []map[string]any{{"path": "shared/", "parts": []string{"app"}}, {"path": "shared/", "parts": []string{"app"}}}
		}},
		{"overlapping shared paths", "shared", func(c, p map[string]any) {
			c["shared_paths"] = []map[string]any{{"path": "shared/", "parts": []string{"app"}}, {"path": "shared/schema.json", "parts": []string{"app"}}}
		}},
	}
	for _, name := range []string{"install", "format", "lint", "build", "test", "test_one"} {
		cases = append(cases, struct {
			name, diagnostic string
			change           func(map[string]any, map[string]any)
		}{"missing " + name, name, func(c, p map[string]any) { delete(p["commands"].(map[string]any), name) }})
	}
	for name, invalid := range map[string]any{
		"empty argv":            map[string]any{"argv": []string{}},
		"blank executable":      command461(" ", "arg"),
		"nul argument":          command461("echo", "bad\x00arg"),
		"implicit skip":         map[string]any{},
		"blank reason":          map[string]any{"not_applicable": "  "},
		"both forms":            map[string]any{"argv": []string{"true"}, "not_applicable": "Skipped"},
		"shell string":          "go test ./...",
		"unknown command field": map[string]any{"argv": []string{"true"}, "shell": true},
	} {
		cases = append(cases, struct {
			name, diagnostic string
			change           func(map[string]any, map[string]any)
		}{name, "test", func(c, p map[string]any) { p["commands"].(map[string]any)["test"] = invalid }})
	}
	for _, args := range [][]string{{"go", "test", "./..."}, {"pytest", "{test}", "{test}"}} {
		cases = append(cases, struct {
			name, diagnostic string
			change           func(map[string]any, map[string]any)
		}{fmt.Sprint("test selector ", args), "test_one", func(c, p map[string]any) { p["commands"].(map[string]any)["test_one"] = command461(args...) }})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := part461("app", ".", "go")
			c := contract461(p)
			root := repo461(t, c)
			// Keep unrelated validation from hiding an ownership error.
			write461(t, root, "other/go.mod", "module other\n")
			write461(t, root, "other/go.sum", "")
			tc.change(c, p)
			write461(t, root, contractPath461, json461(t, c))
			_, err := Load(context.Background(), root, commit461(t, root))
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), contractPath461) || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.diagnostic)) {
				t.Fatalf("invalid %s = %v; want ErrInvalid naming the contract and %q", tc.name, err, tc.diagnostic)
			}
		})
	}
	validJSON := json461(t, contract461(part461("app", ".", "go")))
	for _, tc := range []struct{ name, text string }{
		{"trailing JSON", validJSON + ` {}`},
		{"duplicate top key", strings.Replace(validJSON, `"version":1`, `"version":1,"version":1`, 1)},
		{"duplicate nested key", strings.Replace(validJSON, `"language":"go"`, `"language":"python","language":"go"`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := repo461(t, contract461(part461("app", ".", "go")))
			write461(t, root, contractPath461, tc.text)
			if _, err := Load(context.Background(), root, commit461(t, root)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("%s accepted: %v", tc.name, err)
			}
		})
	}
}

func TestRepositoryContractRejectsUnsafePaths(t *testing.T) {
	badPaths := []string{"", "../outside", "/absolute", "a/../b", "./go.mod", "a//b", `a\b`, ".git/config", "a/.git/config", "bad\nname", "*.go"}
	for _, field := range []string{"directory", "inputs", "lockfiles", "agent_rules", "current_state", "trusted_paths", "shared_paths"} {
		for _, bad := range badPaths {
			t.Run(field+"/"+fmt.Sprintf("%q", bad), func(t *testing.T) {
				p := part461("app", ".", "go")
				c := contract461(p)
				root := repo461(t, c)
				switch field {
				case "directory":
					p[field] = bad
				case "inputs", "lockfiles":
					p[field] = []string{bad}
				case "shared_paths":
					c[field] = []map[string]any{{"path": bad, "parts": []string{"app"}}}
				default:
					c[field] = []string{bad}
				}
				write461(t, root, contractPath461, json461(t, c))
				if _, err := Load(context.Background(), root, commit461(t, root)); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), contractPath461) {
					t.Fatalf("unsafe %s %q = %v; want ErrInvalid naming configuration", field, bad, err)
				}
			})
		}
	}
	for _, field := range []string{"inputs", "lockfiles", "agent_rules", "current_state"} {
		t.Run("missing committed "+field, func(t *testing.T) {
			p := part461("app", ".", "go")
			c := contract461(p)
			root := repo461(t, c)
			if field == "inputs" || field == "lockfiles" {
				p[field] = []string{"candidate-only.txt"}
			} else {
				c[field] = []string{"candidate-only.txt"}
			}
			write461(t, root, contractPath461, json461(t, c))
			base := commit461(t, root)
			write461(t, root, "candidate-only.txt", "uncommitted\n")
			if _, err := Load(context.Background(), root, base); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "candidate-only.txt") {
				t.Fatalf("missing committed %s = %v", field, err)
			}
		})
	}
	for _, target := range []string{contractPath461, "go.mod", "go.sum", "AGENTS.md", "SPEC.md", "tools", "shared"} {
		t.Run("linked "+target, func(t *testing.T) {
			p := part461("app", ".", "go")
			c := contract461(p)
			c["shared_paths"] = []map[string]any{{"path": "shared/", "parts": []string{"app"}}}
			root := repo461(t, c)
			if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(target))); err != nil {
				t.Fatal(err)
			}
			// Neither an in-repository link nor an escaping link is authority.
			if err := os.Symlink("../outside", filepath.Join(root, filepath.FromSlash(target))); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(context.Background(), root, commit461(t, root)); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), target) {
				t.Fatalf("linked %s = %v", target, err)
			}
		})
	}
	t.Run("linked configuration parent", func(t *testing.T) {
		root := repo461(t, contract461(part461("app", ".", "go")))
		if err := os.Rename(filepath.Join(root, ".invariant"), filepath.Join(root, "config")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("config", filepath.Join(root, ".invariant")); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(context.Background(), root, commit461(t, root)); !errors.Is(err, ErrInvalid) || errors.Is(err, ErrMissing) {
			t.Fatalf("linked configuration parent = %v; must not be an absent config", err)
		}
	})
	t.Run("part directory is a symlink", func(t *testing.T) {
		p := part461("app", ".", "go")
		c := contract461(p)
		root := repo461(t, c)
		p["directory"] = "alias"
		write461(t, root, contractPath461, json461(t, c))
		if err := os.Symlink("src", filepath.Join(root, "alias")); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(context.Background(), root, commit461(t, root)); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "alias") {
			t.Fatalf("linked part directory = %v", err)
		}
	})
	t.Run("part directory must be a tree", func(t *testing.T) {
		for _, directory := range []string{"not-committed", "go.mod"} {
			p := part461("app", ".", "go")
			c := contract461(p)
			root := repo461(t, c)
			p["directory"] = directory
			write461(t, root, contractPath461, json461(t, c))
			if _, err := Load(context.Background(), root, commit461(t, root)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("part directory %q = %v", directory, err)
			}
		}
	})
	t.Run("gitlink part", func(t *testing.T) {
		p := part461("app", ".", "go")
		c := contract461(p)
		root := repo461(t, c)
		object := commit461(t, root)
		p["directory"] = "vendor"
		write461(t, root, contractPath461, json461(t, c))
		git461(t, root, "add", "--all")
		git461(t, root, "update-index", "--add", "--cacheinfo", "160000,"+object+",vendor")
		git461(t, root, "commit", "--quiet", "--no-verify", "-m", "gitlink")
		if _, err := Load(context.Background(), root, "HEAD"); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "vendor") {
			t.Fatalf("gitlink directory = %v", err)
		}
	})
	t.Run("unsafe reads and touches", func(t *testing.T) {
		root := repo461(t, contract461(part461("app", ".", "go")))
		if err := os.Symlink("src", filepath.Join(root, "alias")); err != nil {
			t.Fatal(err)
		}
		s := load461(t, root, commit461(t, root))
		for _, bad := range append(badPaths, ".", "alias", "alias/new.go") {
			if _, err := s.PartsFor([]string{"src/new.go", bad}); err == nil {
				t.Errorf("unsafe touched path %q was accepted", bad)
			}
			if _, err := s.ReadFile(bad); err == nil {
				t.Errorf("unsafe read %q was accepted", bad)
			}
			if _, err := s.IsTrusted(bad); err == nil {
				t.Errorf("unsafe trust query %q was accepted", bad)
			}
		}
	})
}

func TestRepositoryContractKeepsBaseAuthority(t *testing.T) {
	c := mixed461()
	api := c["parts"].([]map[string]any)[0]
	// Even a declared command with a visible side effect is only data to Load.
	api["commands"].(map[string]any)["install"] = command461("sh", "-c", "printf ran > loader-ran-command")
	root := repo461(t, c)
	base := commit461(t, root)
	s := load461(t, root, "refs/heads/main")
	var wantConfig any
	if err := json.Unmarshal([]byte(json461(t, c)), &wantConfig); err != nil {
		t.Fatal(err)
	}
	wantRules := "Read SPEC.md before changing code.\n"

	changed := s.Config()
	changed.AgentRules[0] = "rogue.md"
	changed.CurrentState[0] = "rogue.md"
	changed.TrustedPaths[0] = "rogue/"
	changed.Parts[0].Inputs[0] = "rogue.json"
	changed.Parts[0].Lockfiles[0] = "rogue.lock"
	changed.Parts[0].Commands["test"].Argv[0] = "rogue-command"
	delete(changed.Parts[0].Commands, "lint")
	changed.SharedPaths[0].Parts[0] = "rogue"
	owned, err := s.PartsFor([]string{"api/src/old.go"})
	if err != nil || len(owned) != 1 {
		t.Fatalf("initial owners: %v, %v", owned, err)
	}
	owned[0].Name = "rogue"
	owned[0].Inputs[0] = "rogue.json"
	owned[0].Commands["test"].Argv[0] = "rogue-command"
	b, err := s.ReadFile("AGENTS.md")
	if err != nil || len(b) == 0 {
		t.Fatalf("initial rules: %q, %v", b, err)
	}
	b[0] = 'X'
	sameConfig461(t, s.Config(), wantConfig)
	read461(t, s, "AGENTS.md", wantRules)

	// Modify working tree, index and branch history after capture. A ref is not
	// authority again after it has been resolved to the captured commit.
	c["trusted_paths"] = []string{"rogue/"}
	c["shared_paths"] = []map[string]any{{"path": "shared/", "parts": []string{"web"}}}
	api["commands"].(map[string]any)["test"] = command461("candidate-command")
	write461(t, root, contractPath461, json461(t, c))
	write461(t, root, "AGENTS.md", "Follow candidate rules.\n")
	write461(t, root, "SPEC.md", "Candidate state.\n")
	write461(t, root, "workspace.env", "MODE=candidate\n")
	write461(t, root, "pnpm-lock.yaml", "candidate lock\n")
	write461(t, root, "candidate-only.txt", "not in base\n")
	if err := os.Remove(filepath.Join(root, "api/src/old.go")); err != nil {
		t.Fatal(err)
	}
	commit461(t, root)
	// Even replacing the working-tree config by a link cannot make an explicit
	// load of the old commit read different commands.
	if err := os.Remove(filepath.Join(root, contractPath461)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../candidate-only.txt", filepath.Join(root, contractPath461)); err != nil {
		t.Fatal(err)
	}
	for _, captured := range []*Snapshot{s, load461(t, root, base)} {
		if captured.Commit() != base {
			t.Errorf("base changed: %s", captured.Commit())
		}
		sameConfig461(t, captured.Config(), wantConfig)
		read461(t, captured, "AGENTS.md", wantRules)
		read461(t, captured, "SPEC.md", "The current application.\n")
		read461(t, captured, "workspace.env", "MODE=test\n")
		read461(t, captured, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
		read461(t, captured, "api/src/old.go", "package fixture\n")
		if _, err := captured.ReadFile("candidate-only.txt"); err == nil {
			t.Error("base read leaked a candidate-only file")
		}
		parts461(t, captured, []string{"shared/new.json"}, "api", "web")
		parts461(t, captured, []string{"api/src/old.go"}, "api")
		trusted461(t, captured, "tools/new-check.sh", true)
		trusted461(t, captured, "rogue/new-check.sh", false)
	}
	if _, err := os.Stat(filepath.Join(root, "loader-ran-command")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("loading executed a repository command: %v", err)
	}
}

func TestRepositoryContractOwnsEveryTouchedPath(t *testing.T) {
	c := mixed461()
	child := part461("web-tools", "web/tools", "typescript")
	child["lockfiles"] = []string{"pnpm-lock.yaml"}
	child["inputs"] = []string{"web/tools/package.json", "workspace.env"}
	c["parts"] = append(c["parts"].([]map[string]any), child)
	c["shared_paths"] = []map[string]any{
		{"path": "shared/", "parts": []string{"web", "api"}},
		{"path": "api/shared.go", "parts": []string{"web"}},
		{"path": "future-assets/", "parts": []string{"web"}},
	}
	root := repo461(t, c)
	base := commit461(t, root)
	s := load461(t, root, base)
	for _, tc := range []struct {
		paths, want []string
	}{
		{nil, nil},
		{[]string{"api/src/old.go"}, []string{"api"}},
		{[]string{"api/new/deep/file.go"}, []string{"api"}},
		{[]string{"web/src/new.ts"}, []string{"web"}},
		{[]string{"web/tools/src/new.ts"}, []string{"web-tools"}},
		{[]string{"web/toolsmith/new.ts"}, []string{"web"}},
		{[]string{"shared/schema.json", "shared/new.json"}, []string{"api", "web"}},
		{[]string{"api/shared.go"}, []string{"api", "web"}},
		{[]string{"future-assets/new/icon.svg"}, []string{"web"}},
		{[]string{"pnpm-lock.yaml"}, []string{"web", "web-tools"}},
		{[]string{"workspace.env"}, []string{"api", "web", "web-tools"}},
		{[]string{"web/tools/package.json"}, []string{"web-tools"}},
		{[]string{contractPath461}, []string{"api", "web", "web-tools"}},
		{[]string{"AGENTS.md"}, []string{"api", "web", "web-tools"}},
		{[]string{"SPEC.md", "README.md"}, []string{"api", "web", "web-tools"}},
		{[]string{"web/src/new.ts", "api/src/new.go", "web/src/new.ts", "pnpm-lock.yaml"}, []string{"api", "web", "web-tools"}},
	} {
		parts461(t, s, tc.paths, tc.want...)
	}
	for _, paths := range [][]string{{"misc/unowned.txt"}, {"api/src/new.go", "misc/unowned.txt"}, {"apiculture/new.go"}, {"sharedness/new.json"}} {
		if _, err := s.PartsFor(paths); err == nil {
			t.Errorf("unowned path was silently skipped in %v", paths)
		}
	}
	for _, name := range []string{"api/src/old.go", "web/tools/package.json", "pnpm-lock.yaml"} {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			t.Fatal(err)
		}
	}
	parts461(t, s, []string{"api/src/old.go", "web/tools/package.json", "pnpm-lock.yaml"}, "api", "web", "web-tools")
	for _, name := range []string{contractPath461, "AGENTS.md", "api/go.mod", "api/go.sum", "pnpm-lock.yaml", "workspace.env", "tools/new.sh"} {
		trusted461(t, s, name, true)
	}
	for _, name := range []string{"SPEC.md", "README.md", "web/src/new.ts", "toolsmith/new.sh"} {
		trusted461(t, s, name, false)
	}
	// Reordering declarations does not affect which checks are selected.
	c["parts"] = []map[string]any{child, c["parts"].([]map[string]any)[1], c["parts"].([]map[string]any)[0]}
	other := repo461(t, c)
	parts461(t, load461(t, other, commit461(t, other)), []string{"pnpm-lock.yaml", "api/src/old.go"}, "api", "web", "web-tools")
	// A root part is a fallback, not a second implicit owner for every child.
	rootConfig := contract461(part461("root", ".", "go"), part461("web", "web", "typescript"))
	rootRepo := repo461(t, rootConfig)
	r := load461(t, rootRepo, commit461(t, rootRepo))
	parts461(t, r, []string{"new/deep/file.go"}, "root")
	parts461(t, r, []string{"web/new.ts"}, "web")
	parts461(t, r, []string{"webish/new.go"}, "root")
}

func TestRepositoryContractLeavesModeledProjectsSeparate(t *testing.T) {
	root := repo461(t, nil)
	write461(t, root, ".invariant/invariant.json", `{"name":"legacy","module":"spec.tla","code":"core","language":"go"}`)
	write461(t, root, ".invariant/ratified.lock", `{"bounds":{"N":"2"},"statements":[{"name":"Spec","kind":"spec","says":"The behaviors"},{"name":"Safe","kind":"invariant","says":"Safe states"}]}`)
	write461(t, root, "spec.tla", "---- MODULE Fixture ----\n====\n")
	write461(t, root, "core/core.go", "package core\n")
	base := commit461(t, root)
	before, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), root, base); !errors.Is(err, ErrMissing) {
		t.Fatalf("modeled manifest became repository configuration: %v", err)
	}
	c := contract461(part461("app", ".", "go"))
	write461(t, root, "go.mod", "module fixture\n\ngo 1.27.1\n")
	write461(t, root, "go.sum", "")
	write461(t, root, contractPath461, json461(t, c))
	sameConfig461(t, load461(t, root, commit461(t, root)).Config(), c)
	after, err := project.Load(root)
	if err != nil || !reflect.DeepEqual(before.Manifest, after.Manifest) || !reflect.DeepEqual(before.Lock, after.Lock) {
		t.Fatalf("repository config changed legacy loading: before=%+v after=%+v err=%v", before, after, err)
	}
}
