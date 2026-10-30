package verify

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/toolchain"
)

// References pass host validation and pip parses them in a read-only mount.
// Opening deps/../constraints.txt requires the empty deps/ directory, but only
// canonical requirements and constraints reach the Docker installation context.
// Installation must succeed without any original traversed directories there.
//
// INVARIANT_PYTHON_DEPS_REAL=1 go test ./internal/verify -run '^TestPythonDependencyRecipeResolvesTraversedDirectories$' -count=1 -timeout 20m
func TestPythonDependencyRecipeResolvesTraversedDirectories(t *testing.T) {
	for _, option := range []string{"-r", "-c"} {
		t.Run(option, func(t *testing.T) {
			root := pyDepsFixture(t)
			// The exact reported regression occurs in deps/refs/entry.txt,
			// whose sibling deps/ directory is empty and has no input file.
			rootReference := "root-hop/child/../../deps/application.txt"
			rootBody := "-r " + rootReference + "\n"
			applicationBody := "-r refs/entry.txt\n"
			nestedBody := "packaging>=24,<25\n-c ../constraints.txt\n"
			rootTarget := "deps/application.txt"
			if option == "-c" {
				rootReference = "root-hop/child/../../deps/constraints.txt"
				rootBody = "-r deps/application.txt\n-c " + rootReference + "\n"
				applicationBody = "packaging>=24,<25\n-c refs/entry.txt\n"
				nestedBody = "-c ../constraints.txt\n"
				rootTarget = "deps/constraints.txt"
			}
			pyDepsWrite(t, root, "requirements.txt", fmt.Sprintf("# traversed-directory recipe %s %d\n%s", option, time.Now().UnixNano(), rootBody))
			pyDepsWrite(t, root, "deps/application.txt", applicationBody)
			pyDepsWrite(t, root, "deps/refs/entry.txt", option+" deps/../constraints.txt\n")
			pyDepsWrite(t, root, "deps/refs/constraints.txt", nestedBody)
			traversed := []string{"root-hop", "root-hop/child", "deps/refs/deps"}
			for _, dir := range traversed {
				if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, dir := range []string{"root-hop/child", "deps/refs/deps"} {
				entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
				if err != nil || len(entries) != 0 {
					t.Fatalf("fixture traversal directory %s must be empty: %v %v", dir, entries, err)
				}
			}
			inputs := []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt", "deps/refs/entry.txt", "deps/refs/constraints.txt"}
			before := pyDepsFiles(t, root)
			beforeDirs := pyDepsTraversalDirectories(t, root)
			// These opens intentionally preserve .. so the fixture itself
			// proves that every reference is valid before staging.
			pyDepsAssertTraversalOpen(t, root, "requirements.txt", rootReference, rootTarget)
			pyDepsAssertTraversalOpen(t, root, "deps/refs/entry.txt", "deps/../constraints.txt", "deps/refs/constraints.txt")
			t.Run("generated recipe contract", func(t *testing.T) {
				pyDepsFakeDocker(t)
				env := pyDepsBuild(t, root)
				pyDepsAssertRecipeEnvironment(t, root, env, inputs)
				pyDepsAssertCanonicalStaging(t, root, env, inputs)
				staged := filepath.Join(os.Getenv("INVARIANT_PY_DEPS_FAKE"), "context")
				if got := pyDepsTraversalDirectories(t, staged); !reflect.DeepEqual(got, []string{".", "canonical"}) {
					t.Fatalf("original traversal directories reached the installation context: %#v", got)
				}
				if again := pyDepsBuild(t, root); !reflect.DeepEqual(again, env) || pyDepsBuildCount(t) != 1 {
					t.Fatalf("host-resolved traversal inputs did not reuse their recorded environment: first=%#v again=%#v", env, again)
				}
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
			if !reflect.DeepEqual(before, pyDepsFiles(t, root)) || !reflect.DeepEqual(beforeDirs, pyDepsTraversalDirectories(t, root)) {
				t.Fatal("building or executing a traversal recipe rewrote application inputs or directories")
			}
		})
	}
}

// Canonical containment alone cannot establish that host reads are safe: lexical
// path components discarded by .. must also stay inside the project and must
// not be symlinks. Every fixture below can actually be opened as written and
// resolves to the existing pinned constraint, so a missing target cannot make
// an otherwise unsafe resolver pass this test.
func TestPythonDependenciesRejectUnsafeTraversal(t *testing.T) {
	pyDepsFakeDocker(t)
	for _, kind := range []string{"canceled symlink", "outside and back"} {
		for _, option := range []string{"-r", "-c"} {
			for _, containing := range []string{"requirements.txt", "deps/application.txt"} {
				t.Run(kind+"/"+option+"/"+containing, func(t *testing.T) {
					root := pyDepsFixture(t)
					base := filepath.Dir(filepath.Join(root, filepath.FromSlash(containing)))
					var reference string
					if kind == "canceled symlink" {
						if err := os.Mkdir(filepath.Join(base, "ordinary"), 0o755); err != nil {
							t.Fatal(err)
						}
						if err := os.Symlink("ordinary", filepath.Join(base, "linked")); err != nil {
							t.Fatal(err)
						}
						reference = "linked/../deps/constraints.txt"
						if containing != "requirements.txt" {
							reference = "linked/../constraints.txt"
						}
					} else {
						reference = "../" + filepath.Base(root) + "/deps/constraints.txt"
						if containing != "requirements.txt" {
							reference = "../" + reference
						}
					}
					body := option + " " + reference + "\n"
					pyDepsWrite(t, root, containing, body)
					pyDepsAssertTraversalOpen(t, root, containing, reference, "deps/constraints.txt")
					if recipe, err := toolchain.PythonDepsRecipe(root); err == nil {
						t.Errorf("accepted unsafe intermediate directory in %s: reference=%q recipe=%q", containing, reference, recipe)
					}
					if env, err := toolchain.PythonDepsImage(context.Background(), root); err == nil || env != nil {
						t.Errorf("unsafe traversal reached image selection: reference=%q env=%#v error=%v", reference, env, err)
					}
					if got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(containing))); err != nil || string(got) != body {
						t.Errorf("rejecting unsafe traversal rewrote its input: error=%v got=%q want=%q", err, got, body)
					}
				})
			}
		}
	}
	if calls := pyDepsCalls(t); len(calls) != 0 {
		t.Fatalf("unsafe traversals must fail before Docker runs: %#v", calls)
	}
}

func pyDepsAssertTraversalOpen(t *testing.T, root, containing, reference, target string) {
	t.Helper()
	// filepath.Join would clean away the very directories being tested.
	literal := filepath.Dir(filepath.Join(root, filepath.FromSlash(containing))) + string(os.PathSeparator) + filepath.FromSlash(reference)
	got, err := os.ReadFile(literal)
	if err != nil {
		t.Fatalf("cannot open reference %q from %s as written: %v", reference, containing, err)
	}
	want, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(target)))
	if err != nil || string(got) != string(want) {
		t.Fatalf("reference %q from %s did not read exact bytes of %s: error=%v got=%q want=%q", reference, containing, target, err, got, want)
	}
}

func pyDepsTraversalDirectories(t *testing.T, root string) []string {
	t.Helper()
	var dirs []string
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			dirs = append(dirs, filepath.ToSlash(rel))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return dirs
}
