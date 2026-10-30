package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/gitdek/invariant/internal/toolchain"
)

// These tests have two deliberately different layers. The Docker double only
// captures the context: its successful "build" is never evidence that pip or
// Python ran. The default layer checks the generated recipe's literal COPY,
// separate input tree and isolated Python commands. The real layer executes
// that recipe through PythonDepsImage and verifies its installed environment.
// The real command is a required acceptance check, not optional merge evidence:
//
// INVARIANT_PYTHON_DEPS_REAL=1 go test ./internal/verify -run '^TestPythonDependencyRecipe(IsolatesInputModules|CopiesLiteralFilenames)$' -count=1 -timeout 20m
//
// Requested real runs fail, rather than skip, when Docker/builds are unavailable.

func TestPythonDependencyRecipeIsolatesInputModules(t *testing.T) {
	for _, module := range []string{"venv", "pip"} {
		t.Run(module, func(t *testing.T) {
			root := pyDepsFixture(t)
			filename := module + ".py"
			// This is both valid Python syntax and a valid pip requirement with
			// a false marker. pip treats __import__ as inert marker text. If
			// Python executes the file as a module, evaluating python_version
			// raises NameError before the legitimate venv/pip module can run.
			payload := "print; python_version == \"__import__('sys').exit(86)\"\n"
			pyDepsWrite(t, root, filename, payload)
			pyDepsWrite(t, root, "requirements.txt", fmt.Sprintf("# shadow recipe %s %d\n-r deps/application.txt\n-r %s\n", module, time.Now().UnixNano(), filename))
			inputs := []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt", filename}
			before := pyDepsFiles(t, root)
			t.Run("generated recipe contract", func(t *testing.T) {
				pyDepsFakeDocker(t)
				env := pyDepsBuild(t, root)
				pyDepsAssertRecipeEnvironment(t, root, env, inputs)
				pyDepsAssertLiteralStaging(t, root, inputs)
			})
			if os.Getenv("INVARIANT_PYTHON_DEPS_REAL") == "1" {
				t.Run("execute generated recipe", func(t *testing.T) {
					pyDepsRequireRealDocker(t)
					env := pyDepsBuild(t, root)
					pyDepsCleanupRecipeImage(t, env.Image)
					pyDepsAssertRecipeEnvironment(t, root, env, inputs)
					pyDepsRunChecks(t, root)
					// Negative control: the same harmless input MUST shadow the
					// module if -I and the separate working directory are absent.
					ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
					defer cancel()
					out, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "--platform", "linux/amd64", "--read-only", "-v", root+":/shadow:ro", "-w", "/shadow", "--entrypoint", "python", toolchain.PythonImage, "-m", module, "--version").CombinedOutput()
					if err == nil || !strings.Contains(string(out), "NameError") || !strings.Contains(string(out), "python_version") || !strings.Contains(string(out), "/shadow/"+filename) {
						t.Fatalf("shadow fixture did not exercise module loading: error=%v\n%s", err, out)
					}
				})
			}
			if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
				t.Fatal("dependency recipe or execution rewrote application files")
			}
		})
	}
}

func TestPythonDependencyRecipeCopiesLiteralFilenames(t *testing.T) {
	root := pyDepsFixture(t)
	pyDepsWrite(t, root, "requirements.txt", fmt.Sprintf("# literal recipe %d\n-r deps/application.txt\n-r [a].txt\n-c a.txt\n", time.Now().UnixNano()))
	pyDepsWrite(t, root, "[a].txt", "# literal bracket filename\npackaging==24.2\n")
	// Constraints for an unrequested package are inert. If Docker expands a
	// COPY source [a].txt to a.txt, pip instead sees both conflicting versions
	// as requirements. That recipe must fail instead of passing this test.
	pyDepsWrite(t, root, "a.txt", "packaging==24.2\nidna==3.9\nidna==3.10\n")
	inputs := []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt", "[a].txt", "a.txt"}
	before := pyDepsFiles(t, root)
	t.Run("generated recipe contract", func(t *testing.T) {
		pyDepsFakeDocker(t)
		env := pyDepsBuild(t, root)
		pyDepsAssertRecipeEnvironment(t, root, env, inputs)
		pyDepsAssertLiteralStaging(t, root, inputs)
	})
	if os.Getenv("INVARIANT_PYTHON_DEPS_REAL") == "1" {
		t.Run("execute generated recipe", func(t *testing.T) {
			pyDepsRequireRealDocker(t)
			env := pyDepsBuild(t, root)
			pyDepsCleanupRecipeImage(t, env.Image)
			pyDepsAssertRecipeEnvironment(t, root, env, inputs)
			pyDepsRunChecks(t, root)
		})
	}
	if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
		t.Fatal("literal dependency inputs were rewritten")
	}
}

func pyDepsAssertRecipeEnvironment(t *testing.T, root string, env *toolchain.PythonDependencies, inputs []string) {
	t.Helper()
	recipe, err := toolchain.PythonDepsRecipe(root)
	if err != nil || recipe != env.Recipe || env.Base != toolchain.PythonImage || env.Platform != "linux/amd64" {
		t.Fatalf("wrong recipe provenance: %#v; recipe error=%v", env, err)
	}
	want := map[string]string{}
	for _, filename := range inputs {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(filename)))
		if err != nil {
			t.Fatal(err)
		}
		want[filename] = pyDepsHash(body)
	}
	got := map[string]string{}
	var ordered []string
	for _, input := range env.Inputs {
		got[input.Path] = input.SHA256
		ordered = append(ordered, input.Path)
	}
	if !reflect.DeepEqual(got, want) || len(env.Inputs) != len(inputs) || !sort.StringsAreSorted(ordered) {
		t.Fatalf("receipt must hash every literal input exactly once, in path order: got=%#v want=%#v", env.Inputs, want)
	}
	if len(env.Artifacts) != 1 {
		t.Fatalf("false markers or inert constraints became installed dependencies: %#v", env.Artifacts)
	}
	a := env.Artifacts[0]
	if a.Name != "packaging" || a.Version != "24.2" || a.Filename != "packaging-24.2-py3-none-any.whl" || !pyDepsDigest.MatchString(a.SHA256) {
		t.Fatalf("recipe installed a different environment: %#v", a)
	}
}

func pyDepsAssertLiteralStaging(t *testing.T, root string, inputs []string) {
	t.Helper()
	staged := pyDepsFiles(t, filepath.Join(os.Getenv("INVARIANT_PY_DEPS_FAKE"), "context"))
	want := map[string]string{}
	for _, filename := range inputs {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(filename)))
		if err != nil {
			t.Fatal(err)
		}
		want[filename] = string(body)
	}
	got := map[string]string{}
	for filename, body := range staged {
		if strings.HasPrefix(filename, "inputs/") {
			got[strings.TrimPrefix(filename, "inputs/")] = body
		} else if filename != "Dockerfile" && filename != ".dockerignore" {
			t.Errorf("dependency build staged a file outside the dedicated input tree: %s", filename)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dedicated context tree changed literal filenames or bytes: got=%#v want=%#v", got, want)
	}
	pyDepsAssertIsolatedRecipe(t, staged["Dockerfile"])
}

// The generated recipe uses a small, auditable form: one stage, one literal
// directory COPY, absolute WORKDIR and direct Python commands (shell or exec
// form). Reject indirect shell scripts/eval rather than pretending a regex
// proves that an arbitrary shell program always invokes isolated Python.
func pyDepsAssertIsolatedRecipe(t *testing.T, recipe string) {
	t.Helper()
	python := regexp.MustCompile(`^python(?:[0-9]+(?:\.[0-9]+)*)?$`)
	pip := regexp.MustCompile(`^pip(?:[0-9]+(?:\.[0-9]+)*)?$`)
	recipe = strings.ReplaceAll(strings.ReplaceAll(recipe, "\\\r\n", " "), "\\\n", " ")
	workdir, inputdir := "", ""
	copies, stages := 0, 0
	venvSeen, pipSeen := false, false
	for _, line := range strings.Split(recipe, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		words := strings.Fields(line)
		instruction := strings.ToUpper(words[0])
		body := strings.TrimSpace(line[len(words[0]):])
		switch instruction {
		case "FROM":
			stages++
		case "ADD":
			t.Fatal("recipe must copy the dedicated input directory literally, without ADD")
		case "COPY":
			args := pyDepsRecipeArguments(t, body)
			if len(args) != 2 || (args[0] != "inputs/" && args[0] != "./inputs/") || !path.IsAbs(args[1]) || strings.ContainsAny(args[1], "$*?[]") {
				t.Fatalf("COPY must use one literal inputs/ directory and an absolute destination: %s", line)
			}
			inputdir = path.Clean(args[1])
			copies++
		case "WORKDIR":
			args := pyDepsRecipeArguments(t, body)
			if len(args) != 1 || !path.IsAbs(args[0]) || strings.ContainsAny(args[0], "$*?[]") {
				t.Fatalf("recipe needs an explicit absolute clean WORKDIR: %s", line)
			}
			workdir = path.Clean(args[0])
		case "RUN":
			if workdir == "" || inputdir == "" || workdir == inputdir || strings.HasPrefix(workdir, inputdir+"/") {
				t.Fatalf("RUN must execute outside the dependency-input directory: workdir=%q inputs=%q line=%s", workdir, inputdir, line)
			}
			args := pyDepsRecipeArguments(t, body)
			for i, arg := range args {
				base := path.Base(arg)
				switch base {
				case "cd", "eval", "exec", "sh", "bash", "ash", "source", ".":
					t.Fatalf("keep build commands direct and their clean cwd auditable: %s", line)
				}
				if pip.MatchString(base) {
					// A module argument is safe; a pip console entry point does
					// not establish the required isolated Python interpreter.
					if i == 0 || args[i-1] != "-m" {
						t.Fatalf("pip must run through isolated Python: %s", line)
					}
				}
				if !python.MatchString(base) {
					continue
				}
				isolated := false
				for j := i + 1; j < len(args); j++ {
					if args[j] == "-I" {
						isolated = true
					}
					if args[j] == "-m" && j+1 < len(args) {
						venvSeen = venvSeen || args[j+1] == "venv"
						pipSeen = pipSeen || args[j+1] == "pip"
						break
					}
					if args[j] == "-c" || !strings.HasPrefix(args[j], "-") {
						break
					}
				}
				if !isolated {
					t.Fatalf("every build-side Python invocation must use -I: %s", line)
				}
			}
		}
	}
	if stages != 1 || copies != 1 || !venvSeen || !pipSeen {
		t.Fatalf("expected one stage, one literal directory COPY, isolated venv and isolated pip: stages=%d copies=%d venv=%t pip=%t\n%s", stages, copies, venvSeen, pipSeen, recipe)
	}
}

// Tokenize the generated recipe's literal shell commands without executing
// anything. Quoted -c code remains one argument; command separators do not
// hide a second interpreter. Exec-form instructions use JSON directly.
func pyDepsRecipeArguments(t *testing.T, body string) []string {
	t.Helper()
	if strings.HasPrefix(body, "[") {
		var args []string
		if err := json.Unmarshal([]byte(body), &args); err != nil {
			t.Fatalf("invalid exec-form recipe instruction %q: %v", body, err)
		}
		return args
	}
	var args []string
	var token strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if token.Len() != 0 {
			args = append(args, token.String())
			token.Reset()
		}
	}
	for _, r := range body {
		switch {
		case escaped:
			token.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				token.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case unicode.IsSpace(r):
			flush()
		case strings.ContainsRune(";&|()", r):
			flush()
			args = append(args, string(r))
		case r == '`' || r == '$':
			t.Fatalf("generated recipe must use literal, directly auditable commands: %s", body)
		default:
			token.WriteRune(r)
		}
	}
	if quote != 0 || escaped {
		t.Fatalf("unterminated quote or escape in recipe: %s", body)
	}
	flush()
	return args
}

func pyDepsRequireRealDocker(t *testing.T) {
	t.Helper()
	if os.Getenv("INVARIANT_PY_DEPS_FAKE") != "" {
		t.Fatal("real recipe acceptance cannot use the Docker double")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "info").CombinedOutput(); err != nil {
		t.Fatalf("requested real recipe acceptance requires Docker: %v\n%s", err, out)
	}
}

func pyDepsCleanupRecipeImage(t *testing.T, image string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		exec.CommandContext(ctx, "docker", "image", "rm", image).Run()
	})
}
