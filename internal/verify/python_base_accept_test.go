package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

// The parser must not rely on the test harness, a previous build, or Docker
// run's implicit pulling to make its base available. This wrapper adds a
// missing/present base state to the ordinary CLI double. Only a successful
// production pull of the pinned linux/amd64 digest changes that state.
func pyDepsBaseDocker(t *testing.T, present bool) {
	t.Helper()
	pyDepsFakeDocker(t)
	state := os.Getenv("INVARIANT_PY_DEPS_FAKE")
	if present {
		if err := os.WriteFile(filepath.Join(state, "base-present"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\nINVARIANT_PY_DEPS_BASE_HELPER=1 exec \"$INVARIANT_PY_DEPS_TEST_BINARY\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(state, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func init() {
	if os.Getenv("INVARIANT_PY_DEPS_BASE_HELPER") == "1" {
		os.Exit(pyDepsBaseDockerCommand(os.Args[1:]))
	}
}

func pyDepsBaseDockerCommand(args []string) int {
	state := os.Getenv("INVARIANT_PY_DEPS_FAKE")
	marker := filepath.Join(state, "base-present")
	_, statErr := os.Stat(marker)
	present := statErr == nil
	hasBase := false
	for _, arg := range args {
		hasBase = hasBase || arg == toolchain.PythonImage
	}
	log := func(kind string) {
		f, err := os.OpenFile(filepath.Join(state, "calls.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			panic(err)
		}
		defer f.Close()
		if err := json.NewEncoder(f).Encode(pyDepsCall{Kind: kind, Image: toolchain.PythonImage, Args: args}); err != nil {
			panic(err)
		}
	}
	fail := func(message string) int {
		fmt.Fprintln(os.Stderr, message)
		return 71
	}
	if len(args) == 0 {
		return fail("missing Docker command")
	}
	inspect := hasBase && ((args[0] == "image" && len(args) > 1 && args[1] == "inspect") || args[0] == "inspect")
	pull := args[0] == "pull" || (args[0] == "image" && len(args) > 1 && args[1] == "pull")
	if inspect {
		log("base-lifecycle-inspect")
		if !present {
			// A multi-platform digest may exist locally only for arm64.
			// A platform-qualified amd64 probe correctly reports it absent;
			// an unqualified probe succeeds and reports the available arch.
			if os.Getenv("INVARIANT_PY_DEPS_BASE_WRONG_PLATFORM") == "1" && pyDepsBaseOption(args, "--platform") != "linux/amd64" {
				metadata := map[string]any{
					"Id": pyDepsHash([]byte("arm64-only-base")), "Os": "linux", "Architecture": "arm64",
					"Variant": "v8", "RepoDigests": []string{toolchain.PythonImage},
				}
				format := pyDepsBaseOption(args, "--format")
				if format == "" {
					format = pyDepsBaseOption(args, "-f")
				}
				if format == "" {
					if err := json.NewEncoder(os.Stdout).Encode([]any{metadata}); err != nil {
						return fail(err.Error())
					}
					return 0
				}
				formatTemplate, err := template.New("inspect").Funcs(template.FuncMap{
					"json": func(value any) (string, error) {
						b, err := json.Marshal(value)
						return string(b), err
					},
				}).Parse(format)
				if err != nil {
					return fail(err.Error())
				}
				if err := formatTemplate.Execute(os.Stdout, metadata); err != nil {
					return fail(err.Error())
				}
				fmt.Fprintln(os.Stdout)
				return 0
			}
			fmt.Fprintln(os.Stderr, "Error response from daemon: No such image: "+toolchain.PythonImage)
			return 1
		}
	}
	if pull {
		log("base-lifecycle-pull")
		if !hasBase || !strings.Contains(toolchain.PythonImage, "@sha256:") || pyDepsBaseOption(args, "--platform") != "linux/amd64" {
			return fail("base acquisition must pull the pinned linux/amd64 Python digest")
		}
		if diagnostic := os.Getenv("INVARIANT_PY_DEPS_BASE_PULL_FAILURE"); diagnostic != "" {
			return fail(diagnostic)
		}
		if status := pyDepsDocker(args); status != 0 {
			return status
		}
		if err := os.WriteFile(marker, nil, 0o644); err != nil {
			return fail(err.Error())
		}
		return 0
	}
	if args[0] == "run" && hasBase && !present {
		log("base-lifecycle-run-missing")
		return fail("parser cannot start: pinned Python base is absent")
	}
	return pyDepsDocker(args)
}

func pyDepsBaseOption(args []string, option string) string {
	for i, arg := range args {
		if arg == option && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, option+"=") {
			return strings.TrimPrefix(arg, option+"=")
		}
	}
	return ""
}

func pyDepsAssertBaseLifecycle(t *testing.T, root string, pulls int) {
	t.Helper()
	observedPulls := 0
	parsed := false
	for _, call := range pyDepsCalls(t) {
		switch call.Kind {
		case "base-lifecycle-pull":
			observedPulls++
			if parsed && observedPulls <= pulls {
				t.Errorf("base acquisition happened after the parser had already started: %#v", call)
			}
			if pyDepsBaseOption(call.Args, "--platform") != "linux/amd64" {
				t.Errorf("base acquisition omitted the parser's platform: %#v", call)
			}
		case "base-lifecycle-run-missing":
			t.Errorf("production tried the offline parser before acquiring its base: %#v", call)
		case "parser":
			parsed = true
			if observedPulls != pulls {
				t.Errorf("parser ran before required base acquisition: pulled %d, want %d", observedPulls, pulls)
			}
			if pyDepsBaseOption(call.Args, "--pull") != "never" {
				t.Errorf("parser can implicitly pull its image instead of using the ensured base: %#v", call)
			}
		case "build":
			if !parsed {
				t.Error("dependency installation preceded the offline authoritative parse")
			}
		}
	}
	if observedPulls != pulls {
		t.Errorf("base pulls = %d, want %d", observedPulls, pulls)
	}
	pyDepsAssertParserCalls(t, root)
}

func TestPythonDependenciesAcquireMissingPinnedBase(t *testing.T) {
	for _, localBase := range []string{"absent", "arm64 only"} {
		for _, api := range []string{"recipe", "image"} {
			t.Run(localBase+"/"+api, func(t *testing.T) {
				t.Setenv("INVARIANT_PYTHON_DEPS_REAL", "")
				if localBase == "arm64 only" {
					t.Setenv("INVARIANT_PY_DEPS_BASE_WRONG_PLATFORM", "1")
				}
				pyDepsBaseDocker(t, false)
				root := pyDepsFixture(t)
				before := pyDepsFiles(t, root)
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				if api == "recipe" {
					recipe, err := toolchain.PythonDepsRecipe(root)
					if err != nil || !pyDepsDigest.MatchString(recipe) {
						t.Fatalf("recipe on a daemon without the base failed: %q %v", recipe, err)
					}
					again, err := toolchain.PythonDepsRecipe(root)
					if err != nil || again != recipe || pyDepsBuildCount(t) != 0 {
						t.Fatalf("recipe reuse changed identity or built an image: %q %v", again, err)
					}
				} else {
					// Call Image first: a successful Recipe call would warm the
					// base and conceal a missing ensure step in this public API.
					first, err := toolchain.PythonDepsImage(ctx, root)
					if err != nil || first == nil {
						t.Fatalf("image build on a daemon without the base failed: %#v %v", first, err)
					}
					second, err := toolchain.PythonDepsImage(ctx, root)
					if err != nil || second == nil || first.Image != second.Image || first.Recipe != second.Recipe || pyDepsBuildCount(t) != 1 {
						t.Fatalf("warm dependency image was not reused: first=%#v second=%#v error=%v", first, second, err)
					}
					pyDepsAssertEnvironment(t, root, first, "24.2")
					pyDepsRunChecks(t, root)
				}
				pyDepsAssertBaseLifecycle(t, root, 1)
				if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
					t.Fatal("base acquisition rewrote the application's dependency inputs")
				}
			})
		}
	}
	if os.Getenv("INVARIANT_PYTHON_DEPS_REAL") == "1" {
		t.Run("real production acquisition and parser", pyDepsAcquirePinnedBaseWithRealDocker)
	}
}

func TestPythonDependenciesReusePresentPinnedBase(t *testing.T) {
	t.Setenv("INVARIANT_PYTHON_DEPS_REAL", "")
	pyDepsBaseDocker(t, true)
	root := pyDepsFixture(t)
	pyDepsBuild(t, root)
	pyDepsRunChecks(t, root)
	pyDepsAssertBaseLifecycle(t, root, 0)
}

func TestPythonDependenciesBaseAcquisitionFailureDoesNotFallBack(t *testing.T) {
	for _, cached := range []bool{false, true} {
		for _, api := range []string{"recipe", "image", "check"} {
			t.Run(fmt.Sprintf("%s/cached=%t", api, cached), func(t *testing.T) {
				t.Setenv("INVARIANT_PYTHON_DEPS_REAL", "")
				pyDepsBaseDocker(t, cached)
				root := pyDepsFixture(t)
				if cached {
					pyDepsBuild(t, root)
					if err := os.Remove(filepath.Join(os.Getenv("INVARIANT_PY_DEPS_FAKE"), "base-present")); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(os.Getenv("INVARIANT_PY_DEPS_FAKE"), "calls.jsonl"), nil, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				const diagnostic = "acceptance registry unavailable while acquiring pinned Python base"
				t.Setenv("INVARIANT_PY_DEPS_BASE_PULL_FAILURE", diagnostic)
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				var failure error
				switch api {
				case "recipe":
					recipe, err := toolchain.PythonDepsRecipe(root)
					failure = err
					if recipe != "" {
						t.Errorf("failed acquisition returned recipe %q", recipe)
					}
				case "image":
					env, err := toolchain.PythonDepsImage(ctx, root)
					failure = err
					if env != nil {
						t.Errorf("failed acquisition selected cached environment: %#v", env)
					}
				case "check":
					p, err := project.Load(root)
					if err != nil {
						t.Fatal(err)
					}
					build, evidence, err := (Python{Image: toolchain.PythonImage}).Check(ctx, p)
					failure = err
					if build.Passed || len(evidence.Traces) != 0 {
						t.Errorf("failed acquisition produced successful checks: %#v %#v", build, evidence)
					}
				}
				if failure == nil || !strings.Contains(failure.Error(), diagnostic) {
					t.Errorf("acquisition failure lost the Docker diagnostic: %v", failure)
				}
				pulls := 0
				for _, call := range pyDepsCalls(t) {
					if call.Kind == "base-lifecycle-pull" {
						pulls++
					}
					if call.Kind == "parser" || call.Kind == "base-lifecycle-run-missing" || call.Kind == "build" || call.Kind == "evidence" || call.Kind == "application" {
						t.Errorf("failed base acquisition reached parsing, installation, evidence or fallback execution: %#v", call)
					}
				}
				if pulls == 0 {
					t.Error("production did not attempt to acquire the missing base")
				}
			})
		}
	}
}

func TestPythonDependenciesPreflightRejectsBeforeBaseAcquisition(t *testing.T) {
	for _, tc := range []struct{ name, file, body string }{
		{"root index override", "requirements.txt", "--index-url https://example.invalid/simple\npackaging==24.2\n"},
		{"referenced source builds", "deps/application.txt", "--no-binary :all:\npackaging==24.2\n"},
		{"invalid UTF-8", "deps/constraints.txt", "packaging==24.2\n#\xff\n"},
		{"escaping include", "requirements.txt", "-r ../outside.txt\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("INVARIANT_PYTHON_DEPS_REAL", "")
			pyDepsBaseDocker(t, false)
			root := pyDepsFixture(t)
			pyDepsWrite(t, root, tc.file, tc.body)
			before := pyDepsFiles(t, root)
			pyDepsAssertAuthorityRefused(t, root, false)
			if calls := pyDepsCalls(t); len(calls) != 0 {
				t.Errorf("strict preflight contacted Docker before rejecting invalid input: %#v", calls)
			}
			if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
				t.Fatal("strict preflight changed the application's files")
			}
		})
	}
	t.Run("no requirements", func(t *testing.T) {
		t.Setenv("INVARIANT_PYTHON_DEPS_REAL", "")
		pyDepsBaseDocker(t, false)
		root := t.TempDir()
		if recipe, err := toolchain.PythonDepsRecipe(root); err != nil || recipe != "" {
			t.Fatalf("no requirements should retain the standard-library recipe: %q %v", recipe, err)
		}
		if env, err := toolchain.PythonDepsImage(context.Background(), root); err != nil || env != nil {
			t.Fatalf("no requirements should retain the standard-library image path: %#v %v", env, err)
		}
		if calls := pyDepsCalls(t); len(calls) != 0 {
			t.Errorf("a project without requirements acquired a dependency-parser base: %#v", calls)
		}
	})
}

func pyDepsAcquirePinnedBaseWithRealDocker(t *testing.T) {
	// Simulate absence at the Docker CLI boundary without deleting an image
	// from the user's daemon. The production pull and the production offline
	// parser run against real Docker; the harness performs neither in advance.
	for _, api := range []string{"recipe", "image"} {
		t.Run(api, func(t *testing.T) {
			pyDepsBaseDocker(t, false)
			root := pyDepsFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			if api == "recipe" {
				if recipe, err := toolchain.PythonDepsRecipe(root); err != nil || !pyDepsDigest.MatchString(recipe) {
					t.Fatalf("production failed to acquire and parse with the real pinned base: %q %v", recipe, err)
				}
			} else {
				if env, err := toolchain.PythonDepsImage(ctx, root); err != nil || env == nil {
					t.Fatalf("production image path failed to acquire and parse with the real pinned base: %#v %v", env, err)
				}
			}
			pyDepsAssertBaseLifecycle(t, root, 1)
		})
	}
}
