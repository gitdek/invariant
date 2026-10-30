package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/toolchain"
)

// The Docker double checks the production recipe and launcher contract; it
// cannot prove that wheel installation or Python startup is safe. The real
// layer executes that generated recipe, then builds separate hostile images
// outside requirements admission with real pip and the recipe's exact target.
// Each wheel has a working takeover control. The immutable fixture image goes
// directly to production evidence reading, launcher and ordinary/larger checks;
// local-wheel reports never pass through or relax public-PyPI validation.
// The gate must retain genuine receipt evidence and passing/failing results.
//
// INVARIANT_PYTHON_DEPS_REAL=1 go test ./internal/verify -run '^TestPythonDependenc' -count=1 -timeout 30m
func TestPythonDependenciesTreatInstalledDistributionsAsLibraries(t *testing.T) {
	for _, vector := range []string{"python", "pth", "sitecustomize", "usercustomize", "gate_modules"} {
		t.Run(vector, func(t *testing.T) {
			root := pyDepsFixture(t)
			pyDepsWrite(t, root, "requirements.txt", fmt.Sprintf("# library regression %s %d\n-r deps/application.txt\n", vector, time.Now().UnixNano()))
			var recipe string
			t.Run("recipe and launcher contract", func(t *testing.T) {
				pyDepsFakeDocker(t)
				tc := toolchain.Toolchain{PythonImage: toolchain.PythonImage, JavaImage: "accept-java", TLCJar: filepath.Join(t.TempDir(), "tlc.jar")}
				r, err := Run(context.Background(), root, "", tc)
				pyDepsRequireGateChecks(t, r, err)
				staged := pyDepsFiles(t, filepath.Join(os.Getenv("INVARIANT_PY_DEPS_FAKE"), "context"))
				recipe = staged["Dockerfile"]
				target := pyDepsRecipeLibraryTarget(t, recipe)
				pyDepsAssertIsolatedRecipe(t, recipe)
				for _, line := range strings.Split(recipe, "\n") {
					words := strings.Fields(line)
					if len(words) == 0 || !strings.EqualFold(words[0], "ENV") {
						continue
					}
					if strings.Contains(line, "PYTHONPATH") || strings.Contains(line, "PYTHONHOME") ||
						(regexp.MustCompile(`(?:^|\s)PATH(?:=|\s)`).MatchString(line) && strings.Contains(line, target)) {
						t.Fatalf("dependency target must not enter interpreter startup or command lookup through image ENV: %s", line)
					}
				}
				var ordinary, larger, evidence bool
				for _, call := range pyDepsCalls(t) {
					if call.Kind != "application" && call.Kind != "evidence" {
						continue
					}
					command := append([]string(nil), call.Command...)
					if call.Entrypoint != "" {
						command = append([]string{call.Entrypoint}, command...)
					}
					if len(command) == 0 || !pyDepsTrustedExecutable(command[0]) {
						t.Fatalf("gate launcher/evidence reader can be installed by a wheel: %#v", call)
					}
					for _, key := range []string{"PATH", "PYTHONPATH", "PYTHONHOME"} {
						for _, dir := range strings.Split(call.Environment[key], ":") {
							if dir == target || strings.HasPrefix(dir, target+"/") {
								t.Fatalf("dependency library enters startup/command lookup through %s: %#v", key, call)
							}
						}
					}
					if call.Kind == "evidence" {
						evidence = true
						continue
					}
					ordinary, larger = ordinary || !call.Count, larger || call.Count
					if path.Base(command[0]) != "sh" || len(command) != 3 || command[1] != "-c" {
						t.Fatalf("unexpected check launcher: %#v", command)
					}
					pyDepsRequireTrustedHelpers(t, command[2])
					pyDepsRequireIsolatedApplicationPython(t, command[2])
				}
				if !ordinary || !larger || !evidence {
					t.Fatalf("missing library isolation boundary: ordinary=%t larger=%t evidence=%t", ordinary, larger, evidence)
				}
			})
			if os.Getenv("INVARIANT_PYTHON_DEPS_REAL") == "1" {
				t.Run("real installed wheel cannot take over", func(t *testing.T) {
					pyDepsRealTrustedCommands(t, root, recipe, vector)
				})
			}
		})
	}
}

func pyDepsRequireIsolatedApplicationPython(t *testing.T, script string) {
	t.Helper()
	python := regexp.MustCompile(`^python(?:[0-9]+(?:\.[0-9]+)*)?$`)
	seen := false
	for _, command := range pyDepsShellCommands(t, script) {
		if strings.TrimSpace(command) == "exec 2>&1" {
			continue
		}
		args := pyDepsRecipeArguments(t, command)
		if len(args) == 0 {
			continue
		}
		i := 0
		if args[i] == "exec" {
			i++
		}
		if i < len(args) && path.Base(args[i]) == "timeout" {
			i++
			for i < len(args) && strings.HasPrefix(args[i], "-") {
				i++
			}
			i++ // duration, then the child executable
		}
		if i >= len(args) || !python.MatchString(path.Base(args[i])) {
			continue
		}
		seen = true
		if path.Dir(args[i]) != "/usr/local/bin" || !pyDepsTrustedExecutable(args[i]) {
			t.Fatalf("application Python is not the pinned base interpreter: %q in %s", args[i], script)
		}
		isolated, noSite, noUserSite, safePath := false, false, false, false
		for _, flag := range args[i+1:] {
			if flag == "-c" || flag == "-m" || !strings.HasPrefix(flag, "-") {
				break
			}
			if strings.HasPrefix(flag, "--") {
				continue
			}
			isolated = isolated || strings.Contains(flag, "I")
			noSite = noSite || strings.Contains(flag, "S")
			noUserSite = noUserSite || strings.Contains(flag, "s")
			safePath = safePath || strings.Contains(flag, "P")
		}
		// -I ignores PYTHONHASHSEED. A trusted bootstrap may instead disable
		// site startup, user sites and implicit source paths explicitly, under
		// the gate's controlled environment. Runtime hostile-wheel tests and
		// the hash-seed test decide whether either bootstrap is actually safe
		// and deterministic; spelling -I alone is not that evidence.
		if !isolated && !(noSite && noUserSite && safePath) {
			t.Fatalf("application Python can run startup hooks or load startup paths: %s", script)
		}
	}
	if !seen || strings.Contains(script, "addsitedir") {
		t.Fatalf("checks must start trusted isolated Python and add libraries without processing site hooks: %s", script)
	}
}

// Split only outside quotes, preserving multi-line Python -c programs as a
// single argument rather than treating their statements as shell commands.
func pyDepsShellCommands(t *testing.T, script string) []string {
	t.Helper()
	var commands []string
	var current strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if command := strings.TrimSpace(current.String()); command != "" {
			commands = append(commands, command)
		}
		current.Reset()
	}
	for i, r := range script {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			current.WriteRune(r)
			escaped = true
		case quote != 0:
			current.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			current.WriteRune(r)
			quote = r
		case r == '\n' || r == ';' || r == '|' || (r == '&' && ((i > 0 && script[i-1] == '&') || (i+1 < len(script) && script[i+1] == '&'))):
			flush()
		default:
			current.WriteRune(r)
		}
	}
	if quote != 0 || escaped {
		t.Fatalf("unterminated check launcher quote or escape: %s", script)
	}
	flush()
	return commands
}

func pyDepsShadowNegativeControl(t *testing.T, ctx context.Context, docker func(...string) []byte, image, target, reportPath, vector string) {
	t.Helper()
	base := []string{"run", "--rm", "--network", "none", "--platform", "linux/amd64", "-w", "/tmp"}
	if vector == "commands" {
		unsafePath := "PATH=" + path.Join(target, "bin") + ":/usr/local/bin:/usr/bin:/bin"
		var forged struct {
			Install []any `json:"install"`
		}
		out := docker(append(append([]string(nil), base...), "-e", unsafePath, image, "cat", reportPath)...)
		if err := json.Unmarshal(out, &forged); err != nil || forged.Install == nil || len(forged.Install) != 0 {
			t.Fatalf("installed cat did not forge empty evidence when enabled on PATH: %s %v", out, err)
		}
		for _, name := range []string{"sh", "timeout"} {
			args := append(append([]string(nil), base...), "-e", unsafePath, image, name)
			out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
			if err == nil || !strings.Contains(string(out), "INVARIANT_SHADOW_"+name) {
				t.Fatalf("installed %s did not intercept unsafe PATH: error=%v\n%s", name, err, out)
			}
		}
		return
	}
	var args []string
	switch vector {
	case "python":
		args = append(base, image, path.Join(target, "bin", "python"), "-c", "raise AssertionError('wrapped interpreter ran application')")
	case "pth":
		args = append(base, image, "/usr/local/bin/python", "-I", "-c", "import site, sys; site.addsitedir(sys.argv[1]); raise AssertionError('inactive pth fixture')", target)
	case "sitecustomize", "usercustomize":
		args = append(base, "-e", "PYTHONPATH="+target, image, "/usr/local/bin/python", "-c", "raise AssertionError('inactive startup module fixture')")
	case "gate_modules":
		args = append(base, image, "/usr/local/bin/python", "-I", "-c", "import sys; sys.path.insert(0, sys.argv[1]); import unittest; raise AssertionError('inactive runner replacement fixture')", target)
	default:
		t.Fatalf("unknown hostile wheel vector %q", vector)
	}
	out := docker(args...)
	if !strings.Contains(string(out), "INVARIANT_SHADOW_"+vector) || !strings.Contains(string(out), "@@invariant test=0") {
		t.Fatalf("hostile %s did not forge success with the unsafe startup control: %s", vector, out)
	}
}

func pyDepsAssertImportOnlyImage(t *testing.T, docker func(...string) []byte, image, target string) {
	t.Helper()
	const probe = `import builtins, importlib.util, os, site, sys
target = sys.argv[1]
assert os.path.realpath(target) not in [os.path.realpath(p) for p in sys.path if p]
assert os.path.realpath(target) not in [os.path.realpath(p) for p in site.getsitepackages() + [site.getusersitepackages()]]
assert importlib.util.find_spec("invariant_gate_shadow") is None
assert not hasattr(builtins, "INVARIANT_IMPORTED_LIBRARY")
# This raw path update imports no dependency and processes no .pth files.
sys.path.append(target)
assert not hasattr(builtins, "INVARIANT_IMPORTED_LIBRARY")
import invariant_gate_shadow
assert builtins.INVARIANT_IMPORTED_LIBRARY == "import-only"
from packaging.version import Version
assert Version("1.9") < Version("1.10")
print("INVARIANT_IMPORT_ONLY_OK")
`
	out := docker("run", "--rm", "--network", "none", "--platform", "linux/amd64", "--read-only", "-w", "/tmp", image, "/usr/local/bin/python", "-I", "-c", probe, target)
	if string(out) != "INVARIANT_IMPORT_ONLY_OK" {
		t.Fatalf("installed wheel ran at startup or failed explicit import: %s", out)
	}
}
