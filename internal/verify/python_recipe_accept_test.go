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
	"unicode/utf8"

	"github.com/gitdek/invariant/internal/toolchain"
)

// These tests have two deliberately different layers. The Docker double only
// captures the context: its successful "build" is never evidence that pip or
// Python ran. The default layer checks the generated recipe's literal COPY,
// canonical input tree and isolated Python commands. The real layer executes
// that recipe through PythonDepsImage and verifies its installed environment.
// The real command is a required acceptance check, not optional merge evidence:
//
// INVARIANT_PYTHON_DEPS_REAL=1 go test ./internal/verify -run '^TestPythonDependencyRecipe(IsolatesInputModules|CopiesLiteralFilenames|ReadsOnlyCanonicalFiles)$' -count=1 -timeout 20m
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
				pyDepsAssertCanonicalStaging(t, root, env, inputs)
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
	// Constraints for an unrequested package are inert. Literal host reads
	// must preserve the roles of [a].txt and a.txt before canonicalization;
	// treating the latter as requirements makes its versions conflict.
	pyDepsWrite(t, root, "a.txt", "packaging==24.2\nidna==3.9\nidna==3.10\n")
	inputs := []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt", "[a].txt", "a.txt"}
	before := pyDepsFiles(t, root)
	t.Run("generated recipe contract", func(t *testing.T) {
		pyDepsFakeDocker(t)
		env := pyDepsBuild(t, root)
		pyDepsAssertRecipeEnvironment(t, root, env, inputs)
		pyDepsAssertCanonicalStaging(t, root, env, inputs)
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

// Capture the production recipe, then execute it with real Docker from a
// context containing only its generated files. No application include can be
// opened there. Real pip must honor the surviving hashes, markers and separate
// constraints; negative controls modify only canonical bytes and must fail.
func TestPythonDependencyRecipeReadsOnlyCanonicalFiles(t *testing.T) {
	writeFixture := func(t *testing.T, root, hash string) {
		t.Helper()
		pyDepsWrite(t, root, "requirements.txt", fmt.Sprintf("\xef\xbb\xbf# canonical-only recipe %d\r\n-r deps/application.txt\r\n-c deps/constraints.txt\r\n", time.Now().UnixNano()))
		pyDepsWrite(t, root, "deps/application.txt", "# accepted requirement and a continued hash\npackaging==24.2 ; python_version >= '3.0' \\\n    --hash="+hash+"\n"+
			"invariant-canonical-marker-never==0 ; python_version < '0' \\\n    --hash=sha256:"+strings.Repeat("a", 64)+"\n")
		// These conflicting constraints must stay inert: they are not
		// requirements, and no installed distribution needs idna.
		pyDepsWrite(t, root, "deps/constraints.txt", "# constraint-only input\npackaging==24.2\nidna==3.9\nidna==3.10\n")
	}
	inputs := []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt"}
	capture := func(t *testing.T, root string) (map[string]string, *toolchain.PythonDependencies) {
		t.Helper()
		pyDepsFakeDocker(t)
		env := pyDepsBuild(t, root)
		pyDepsAssertRecipeEnvironment(t, root, env, inputs)
		pyDepsAssertCanonicalStaging(t, root, env, inputs)
		return pyDepsFiles(t, filepath.Join(os.Getenv("INVARIANT_PY_DEPS_FAKE"), "context")), env
	}
	t.Run("generated recipe contract", func(t *testing.T) {
		root := pyDepsFixture(t)
		writeFixture(t, root, "sha256:"+strings.Repeat("a", 64))
		before := pyDepsFiles(t, root)
		capture(t, root)
		if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
			t.Fatal("canonicalization rewrote the application's dependency files")
		}
	})
	if os.Getenv("INVARIANT_PYTHON_DEPS_REAL") != "1" {
		return
	}
	t.Run("execute generated canonical-only recipe", func(t *testing.T) {
		pyDepsRequireRealDocker(t)
		root := pyDepsFixture(t)
		pyDepsWrite(t, root, "requirements.txt", fmt.Sprintf("# real hash oracle %d\n-r deps/application.txt\n", time.Now().UnixNano()))
		oracle := pyDepsBuild(t, root)
		pyDepsCleanupRecipeImage(t, oracle.Image)
		pyDepsAssertRecipeEnvironment(t, root, oracle, inputs)
		// The independent hash comes from a real installation report, never
		// from the Docker double or a guessed package version/digest table.
		hash := oracle.Artifacts[0].SHA256
		writeFixture(t, root, hash)
		before := pyDepsFiles(t, root)
		var staged map[string]string
		var captured *toolchain.PythonDependencies
		t.Run("capture exact production recipe", func(t *testing.T) {
			staged, captured = capture(t, root)
		})
		if staged == nil || captured == nil {
			t.Fatal("no canonical recipe was captured")
		}
		// Also execute production's normal build path, not just a test-owned
		// Docker invocation, and retain its receipt and offline checks.
		env := pyDepsBuild(t, root)
		pyDepsCleanupRecipeImage(t, env.Image)
		pyDepsAssertRecipeEnvironment(t, root, env, inputs)
		if !reflect.DeepEqual(env.CanonicalInputs, captured.CanonicalInputs) || !reflect.DeepEqual(env.Artifacts, oracle.Artifacts) {
			t.Fatalf("production build changed canonical bytes or installed artifacts: captured=%#v built=%#v oracle=%#v", captured, env, oracle)
		}
		pyDepsRunChecks(t, root)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		build := func(t *testing.T, change func(string), failure string) string {
			t.Helper()
			contextDir := t.TempDir()
			for filename, body := range staged {
				pyDepsWrite(t, contextDir, filename, body)
			}
			if change != nil {
				change(contextDir)
			}
			iidfile := filepath.Join(t.TempDir(), "image-id")
			out, err := exec.CommandContext(ctx, "docker", "build", "--no-cache", "--platform", "linux/amd64", "--iidfile", iidfile, contextDir).CombinedOutput()
			if failure != "" {
				if err == nil || !strings.Contains(strings.ToLower(string(out)), failure) {
					t.Fatalf("changing only canonical %s did not fail installation as expected: error=%v\n%s", failure, err, out)
				}
				return ""
			}
			if err != nil {
				t.Fatalf("unmodified generated recipe could not install using only canonical files: %v\n%s", err, out)
			}
			id, err := os.ReadFile(iidfile)
			image := strings.TrimSpace(string(id))
			if err != nil || !pyDepsDigest.MatchString(image) {
				t.Fatalf("replayed recipe did not produce an immutable image: id=%q error=%v", image, err)
			}
			pyDepsCleanupRecipeImage(t, image)
			return image
		}
		t.Run("canonical files alone install", func(t *testing.T) {
			image := build(t, nil, "")
			reportPath := pyDepsRecipeReportPath(t, staged["Dockerfile"])
			out, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "--platform", "linux/amd64", "--read-only", image, "/bin/cat", reportPath).CombinedOutput()
			if err != nil {
				t.Fatalf("read actual replayed installation report: %v\n%s", err, out)
			}
			var report struct {
				Install []struct {
					Metadata     struct{ Name, Version string }
					DownloadInfo struct {
						URL         string
						ArchiveInfo struct{ Hashes map[string]string } `json:"archive_info"`
					} `json:"download_info"`
				}
			}
			if err := json.Unmarshal(out, &report); err != nil || len(report.Install) != 1 {
				t.Fatalf("false markers or constraint-only entries became installed requirements: error=%v report=%s", err, out)
			}
			installed := report.Install[0]
			if installed.Metadata.Name != "packaging" || installed.Metadata.Version != "24.2" || "sha256:"+installed.DownloadInfo.ArchiveInfo.Hashes["sha256"] != hash || !strings.HasSuffix(installed.DownloadInfo.URL, "/packaging-24.2-py3-none-any.whl") {
				t.Fatalf("canonical recipe installed a different artifact: %s", out)
			}
			out, err = exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "--platform", "linux/amd64", "--read-only", "-w", "/tmp", image, "python", "-I", "-c", "import packaging, importlib.util; assert packaging.__version__ == '24.2'; assert importlib.util.find_spec('idna') is None").CombinedOutput()
			if err != nil {
				t.Fatalf("canonical-only image failed offline imports: %v\n%s", err, out)
			}
		})
		t.Run("canonical hashes are enforced", func(t *testing.T) {
			build(t, func(dir string) {
				pyDepsWrite(t, dir, "canonical/requirements.txt", strings.ReplaceAll(staged["canonical/requirements.txt"], hash, "sha256:"+strings.Repeat("0", 64)))
			}, "hashes")
		})
		t.Run("canonical constraints are enforced", func(t *testing.T) {
			build(t, func(dir string) {
				pyDepsWrite(t, dir, "canonical/constraints.txt", staged["canonical/constraints.txt"]+"packaging==24.1\n")
			}, "conflict")
		})
		if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
			t.Fatal("real canonical recipe build or execution rewrote original inputs")
		}
	})
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

// Application paths are evidence and host-side validation inputs only. The
// network-enabled installer gets exactly these two generated UTF-8/LF files.
// Return their bytes for semantic assertions without coupling tests to the
// production parser or reproducing its normalization algorithm.
func pyDepsAssertCanonicalStaging(t *testing.T, root string, env *toolchain.PythonDependencies, inputs []string) map[string]string {
	t.Helper()
	staged := pyDepsFiles(t, filepath.Join(os.Getenv("INVARIANT_PY_DEPS_FAKE"), "context"))
	want := map[string]string{}
	for _, filename := range inputs {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(filename)))
		if err != nil {
			t.Fatal(err)
		}
		want[filename] = pyDepsHash(body)
	}
	gotInputs := map[string]string{}
	for _, input := range env.Inputs {
		gotInputs[input.Path] = input.SHA256
	}
	if !reflect.DeepEqual(gotInputs, want) || len(env.Inputs) != len(want) {
		t.Fatalf("canonicalization lost original input evidence: got=%#v want=%#v", env.Inputs, want)
	}
	canonical := map[string]string{}
	for filename, body := range staged {
		switch filename {
		case "canonical/requirements.txt", "canonical/constraints.txt":
			name := strings.TrimPrefix(filename, "canonical/")
			canonical[name] = body
			if !utf8.ValidString(body) || strings.ContainsAny(body, "\\#\r\v\f\u001c\u001d\u001e\u0085\u2028\u2029\ufeff") || (body != "" && !strings.HasSuffix(body, "\n")) {
				t.Errorf("canonical file %s contains invalid UTF-8, comments, continuations, a BOM or non-LF endings: %q", filename, body)
			}
			for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
				if line == "" && body == "" {
					continue
				}
				if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*`).MatchString(line) || strings.TrimSpace(line) != line {
					t.Errorf("canonical file %s must contain one accepted requirement per line, without includes or option lines: %q", filename, line)
				}
				for _, token := range strings.Fields(line) {
					if strings.HasPrefix(token, "-") && !regexp.MustCompile(`^--hash=sha256:[0-9a-f]{64}$`).MatchString(token) {
						t.Errorf("canonical file %s contains an option other than an accepted SHA-256 hash: %q", filename, line)
					}
				}
			}
		case "Dockerfile", ".dockerignore":
		default:
			t.Errorf("build context exposes an original input or unrelated file to pip: %s", filename)
		}
	}
	if len(canonical) != 2 {
		t.Fatalf("build context must contain exactly canonical/requirements.txt and canonical/constraints.txt: %#v", staged)
	}
	if len(env.CanonicalInputs) != 2 {
		t.Fatalf("receipt must identify both canonical files: %#v", env.CanonicalInputs)
	}
	for i, name := range []string{"constraints.txt", "requirements.txt"} {
		input := env.CanonicalInputs[i]
		if input.Path != name || input.SHA256 != pyDepsHash([]byte(canonical[name])) {
			t.Errorf("canonical evidence must hash staged bytes in path order: got=%#v want path=%s hash=%s", input, name, pyDepsHash([]byte(canonical[name])))
		}
	}
	pyDepsAssertIsolatedRecipe(t, staged["Dockerfile"])
	return canonical
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
	venvSeen, pipSeen, requirementsSeen, constraintsSeen := false, false, false, false
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
			args := pyDepsRecipeArguments(t, body)
			if len(args) > 0 && args[0] == "--platform=linux/amd64" {
				args = args[1:]
			} else if len(args) > 1 && args[0] == "--platform" && args[1] == "linux/amd64" {
				args = args[2:]
			}
			if (len(args) != 1 && !(len(args) == 3 && strings.EqualFold(args[1], "AS"))) || args[0] != toolchain.PythonImage {
				t.Fatalf("recipe must build from the existing digest-pinned Python base: %s", line)
			}
		case "ADD":
			t.Fatal("recipe must copy the dedicated canonical directory literally, without ADD")
		case "COPY":
			args := pyDepsRecipeArguments(t, body)
			if len(args) != 2 || (args[0] != "canonical/" && args[0] != "./canonical/") || !path.IsAbs(args[1]) || strings.ContainsAny(args[1], "$*?[]") {
				t.Fatalf("COPY must use one literal canonical/ directory and an absolute destination: %s", line)
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
			pipCommand := false
			for i, arg := range args {
				if strings.Contains(";&|()", arg) {
					pipCommand = false
				}
				if pip.MatchString(path.Base(arg)) && i > 0 && args[i-1] == "-m" {
					pipCommand = true
				}
				// Every requirements/constraints argument must name a generated
				// canonical file. No application paths survive into pip commands.
				option, value := "", ""
				if pipCommand {
					for _, flag := range []string{"--requirement", "--constraint", "-r", "-c"} {
						if arg == flag && i+1 < len(args) {
							option, value = flag, args[i+1]
						} else if strings.HasPrefix(arg, flag+"=") {
							option, value = flag, strings.TrimPrefix(arg, flag+"=")
						} else if len(flag) == 2 && strings.HasPrefix(arg, flag) && len(arg) > 2 {
							option, value = flag, arg[2:]
						}
					}
				}
				if option != "" {
					name := "requirements.txt"
					if option == "-c" || option == "--constraint" {
						name = "constraints.txt"
					}
					if value != path.Join(inputdir, name) {
						t.Fatalf("installer must read only generated canonical files: %s", line)
					}
					requirementsSeen = requirementsSeen || name == "requirements.txt"
					constraintsSeen = constraintsSeen || name == "constraints.txt"
				}
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
	if stages != 1 || copies != 1 || !venvSeen || !pipSeen || !requirementsSeen || !constraintsSeen {
		t.Fatalf("expected one stage, literal canonical COPY, isolated venv/pip and both canonical files: stages=%d copies=%d venv=%t pip=%t requirements=%t constraints=%t\n%s", stages, copies, venvSeen, pipSeen, requirementsSeen, constraintsSeen, recipe)
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
