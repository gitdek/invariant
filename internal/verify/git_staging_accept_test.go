package verify

// Issue #391 acceptance contract. Git selection is the union of
// git ls-files --cached --others --exclude-standard, parsed as NUL-delimited
// paths relative to the copied root, deduplicated and sorted. Tracked files
// remain selected even when an ignore pattern matches them; use working-tree
// bytes, not index blobs. Missing tracked files are omitted. Gitlinks (mode
// 160000), including uninitialized ones, are refused, not silently omitted.
// Git errors must not turn an in-repository copy into a whole-tree copy.
// Selection must work below the repository root and in linked worktrees.
// Never follow symlinks in selected paths or copy Git metadata. Preserve
// each caller's existing exclusions, destination safety and ownership rules.
//
// Workspace and PythonApplicationProvenance expose FileSelection string
// with JSON name file_selection and values "git" and "directory". Bind it
// into provenance/fingerprints; don't infer Git mode from the staged copy,
// which deliberately lacks .git. Existing session consumers already share
// a snapshot: preserve that through image selection, proofs and RunLarger.
// Markdown calls these "git ls-files" and "whole directory" respectively.
//
// Validate ignored declarations before image selection; this includes all
// component invocation inputs and selected source references, trusted
// contract inputs included, as well as manifest existing/environment and
// the verification project. Directly naming an ignored directory is an
// error; unselected ignored descendants of an admitted directory are omitted.
// A tracked file matching an ignore pattern is admitted, as Git admits it.
// For StagePackage retain the fixed package metadata and explicit existing
// boundaries; filter directory descendants and reject ignored explicit files.
// HashExisting must describe those selected bytes, not excluded host files.
// No change to verify.go's TLA-only stage is required.

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

// Use the existing #310 application fixture. Most staging tests need no
// component discovery; keep explicit declarations for the invocation-input
// test, where they are part of the behavior being exercised.
func gs391Fixture(t *testing.T, explicit ...bool) (string, *project.Project) {
	t.Helper()
	gs391IsolateGit(t)
	root, p := pa310Fixture(t, false)
	if len(explicit) == 0 || !explicit[0] {
		p.Manifest.Components = nil
	} else {
		pa310Write(t, root, "acme/service/core.py", "def advance(n):\n    return n + 1\n")
		for _, name := range []string{"acme/service/__init__.py", "acme/service/helper.py", "acme/service/contracts.py"} {
			if err := os.Remove(filepath.Join(root, name)); err != nil {
				t.Fatal(err)
			}
		}
		check := &p.Manifest.Components.Checks[0]
		check.Tests, check.Assumptions = nil, nil
		invocation := check.Invocations[1]
		invocation.Inputs = append(invocation.Inputs, "acme/service/increment.txt")
		check.Invocations = []project.ComponentInvocation{invocation}
	}
	pa310Save(t, p)
	return root, p
}

func gs391IsolateGit(t *testing.T) {
	t.Helper()
	for _, k := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS"} {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	t.Setenv("GIT_AUTHOR_NAME", "Staging acceptance")
	t.Setenv("GIT_AUTHOR_EMAIL", "staging@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "Staging acceptance")
	t.Setenv("GIT_COMMITTER_EMAIL", "staging@example.invalid")
}

func gs391Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr strings.Builder
	c.Stderr = &stderr
	b, err := c.Output()
	if err != nil {
		t.Fatalf("git %v: %v\n%s\n%s", args, err, b, stderr.String())
	}
	return strings.TrimSpace(string(b))
}

func gs391Init(t *testing.T, root string) {
	t.Helper()
	gs391Git(t, root, "init", "--quiet")
}

func gs391Commit(t *testing.T, root string) {
	t.Helper()
	gs391Git(t, root, "add", "--all")
	gs391Git(t, root, "commit", "--quiet", "-m", "Application fixture")
}

func gs391Tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func gs391Mode(t *testing.T, value any, want string) {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := json.Unmarshal(fields["file_selection"], &got); err != nil || got != want {
		t.Errorf("file_selection = %q (%v), want %q", got, err, want)
	}
}

func gs391Stage(t *testing.T, p *project.Project) *PythonApplicationWorkspace {
	t.Helper()
	w, err := StagePythonApplication(p, filepath.Join(t.TempDir(), "stage"))
	if err != nil || w == nil {
		t.Fatalf("stage: %v", err)
	}
	return w
}

func TestGitStageUncommittedApplication(t *testing.T) {
	root, p := gs391Fixture(t)
	gitRoot := filepath.Dir(root)
	gs391Init(t, gitRoot)
	pa310Write(t, gitRoot, "outside.txt", "not in the application")
	pa310Write(t, root, ".gitignore", "renders/\n*.cache\n")
	pa310Write(t, root, "removed.txt", "tracked but deleted")
	pa310Write(t, root, "retained.cache", "index bytes")
	gs391Git(t, gitRoot, "add", "application/removed.txt")
	gs391Git(t, gitRoot, "add", "-f", "application/retained.cache")
	if err := os.Remove(filepath.Join(root, "removed.txt")); err != nil {
		t.Fatal(err)
	}
	pa310Write(t, root, "retained.cache", "current working bytes")
	pa310Write(t, root, "resources/spaced\tand\nquoted\".txt", "literal path")
	want := gs391Tree(t, root)
	pa310Write(t, root, "renders/large.bin", strings.Repeat("x", 2<<20))
	pa310Write(t, root, "scratch.cache", "ignored")
	pa310Write(t, gitRoot, ".git/info/exclude", "application/local-only.txt\n")
	pa310Write(t, root, "local-only.txt", "ignored by repository exclude")
	for _, d := range []string{".git", ".venv", "__pycache__"} {
		pa310Write(t, root, "nested/"+d+"/file", "never staged")
	}
	w := gs391Stage(t, pa310Load(t, p))
	if got := gs391Tree(t, w.Root); !reflect.DeepEqual(got, want) {
		t.Errorf("staged file set differs: got %v, want %v", gs391Names(got), gs391Names(want))
	}
	gs391Mode(t, w, "git")
	if len(w.Files) != len(want) {
		t.Errorf("provenance contains %d files, want %d", len(w.Files), len(want))
	}
	for _, f := range w.Files {
		body, ok := want[f.Path]
		if !ok || f.SHA256 != pa310Hash([]byte(body)) {
			t.Errorf("unexpected provenance file: %+v", f)
		}
	}
}

func gs391Names(tree map[string]string) []string {
	names := make([]string, 0, len(tree))
	for name := range tree {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestGitStageMatchesFreshClone(t *testing.T) {
	root, p := gs391Fixture(t)
	gs391Init(t, root)
	pa310Write(t, root, ".gitignore", "renders/\nlocal.cache\n")
	gs391Commit(t, root)
	pa310Write(t, root, "renders/local.bin", "ignored local payload")
	pa310Write(t, root, "local.cache", "ignored cache")
	clone := filepath.Join(t.TempDir(), "clone")
	gs391Git(t, root, "clone", "--quiet", "--no-hardlinks", root, clone)
	q, err := project.Load(filepath.Join(clone, pa310Project))
	if err != nil {
		t.Fatal(err)
	}
	a, b := gs391Stage(t, pa310Load(t, p)), gs391Stage(t, q)
	if !reflect.DeepEqual(a.Files, b.Files) || !reflect.DeepEqual(gs391Tree(t, a.Root), gs391Tree(t, b.Root)) {
		t.Fatalf("work tree and fresh clone staged different paths or bytes:\nwork tree %v\nclone %v", a.Files, b.Files)
	}
}

func TestGitStageRefusesIgnoredDeclarations(t *testing.T) {
	for _, kind := range []string{"input", "existing", "environment", "project"} {
		t.Run(kind, func(t *testing.T) {
			root, p := gs391Fixture(t, kind == "input")
			gs391Init(t, root)
			name := "acme/service/increment.txt"
			switch kind {
			case "existing":
				name = "acme/service/core.py"
			case "environment":
				name = "environment.py"
				p.Manifest.Environment = name
				pa310Write(t, root, name, "# /// script\n# dependencies = ['packaging==24.2']\n# ///\n")
				pa310Save(t, p)
			case "project":
				name = pa310Project
			}
			pa310Write(t, root, ".gitignore", "/"+name+"\n")
			w, err := StagePythonApplication(p, filepath.Join(t.TempDir(), "stage"))
			if w != nil || err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(strings.ToLower(err.Error()), "ignor") {
				t.Fatalf("ignored %s must be refused by name: workspace=%v, err=%v", name, w != nil, err)
			}
			selected := false
			s, err := PreparePythonApplicationSession(context.Background(), p, filepath.Join(t.TempDir(), "session"), PythonApplicationSessionOptions{
				SelectImage: func(context.Context, *PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
					selected = true
					return toolchain.PythonImage, nil, nil
				},
			})
			if s != nil {
				s.Close()
			}
			if err == nil || selected {
				t.Errorf("ignored declaration reached image selection: selected=%t, err=%v", selected, err)
			}
		})
	}
}

func TestGitStageRefusesSubmodules(t *testing.T) {
	root, p := gs391Fixture(t)
	gs391Init(t, root)
	gs391Commit(t, root)
	head := gs391Git(t, root, "rev-parse", "HEAD")
	gs391Git(t, root, "update-index", "--add", "--cacheinfo", "160000,"+head+",vendor")
	pa310Write(t, root, ".gitmodules", "[submodule \"vendor\"]\n\tpath = vendor\n\turl = ./unused-local-source\n")
	for _, initialized := range []bool{false, true} {
		t.Run(map[bool]string{false: "uninitialized", true: "initialized"}[initialized], func(t *testing.T) {
			if initialized {
				// A local clone needs no network or submodule URL transport.
				gs391Git(t, root, "clone", "--quiet", root, filepath.Join(root, "vendor"))
			}
			w, err := StagePythonApplication(pa310Load(t, p), filepath.Join(t.TempDir(), "stage"))
			if w != nil || err == nil || !strings.Contains(err.Error(), "vendor") || !strings.Contains(strings.ToLower(err.Error()), "submodule") {
				t.Fatalf("submodule must be refused by name: workspace=%v err=%v", w != nil, err)
			}
		})
	}
}

func TestGitStageLinkedWorktreeAndRegularFiles(t *testing.T) {
	root, p := gs391Fixture(t)
	gs391Init(t, root)
	pa310Write(t, root, ".gitignore", "ignored.bin\n")
	pa310Write(t, root, "ordinary/data.txt", "tracked")
	gs391Commit(t, root)
	linked := filepath.Join(t.TempDir(), "linked")
	gs391Git(t, root, "worktree", "add", "--quiet", "--detach", linked, "HEAD")
	pa310Write(t, linked, "ignored.bin", "must not enter copy")
	q, err := project.Load(filepath.Join(linked, pa310Project))
	if err != nil {
		t.Fatal(err)
	}
	w := gs391Stage(t, q)
	for _, name := range []string{".git", "ignored.bin"} {
		if _, err := os.Lstat(filepath.Join(w.Root, name)); !os.IsNotExist(err) {
			t.Errorf("linked worktree staged %s: %v", name, err)
		}
	}
	gs391Mode(t, w, "git")
	for _, parent := range []bool{false, true} {
		name := "link.txt"
		if parent {
			name = "ordinary"
			if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
				t.Fatal(err)
			}
		}
		target := filepath.Join(root, "acme")
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
		got, err := StagePythonApplication(pa310Load(t, p), filepath.Join(t.TempDir(), "bad"))
		if got != nil || err == nil {
			t.Errorf("selected symlink %s was admitted: %v", name, err)
		}
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGitStageDirectoryFallback(t *testing.T) {
	root, p := gs391Fixture(t)
	pa310Write(t, root, ".gitignore", "local.bin\n")
	pa310Write(t, root, "local.bin", "directory mode keeps this")
	want := gs391Tree(t, root)
	for _, d := range []string{".git", ".venv", "__pycache__"} {
		pa310Write(t, root, "nested/"+d+"/cache", "excluded")
	}
	w := gs391Stage(t, pa310Load(t, p))
	if got := gs391Tree(t, w.Root); !reflect.DeepEqual(got, want) {
		t.Errorf("non-Git staging changed: got %v want %v", gs391Names(got), gs391Names(want))
	}
	gs391Mode(t, w, "directory")
	if err := os.Symlink(filepath.Join(root, "local.bin"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if got, err := StagePythonApplication(pa310Load(t, p), filepath.Join(t.TempDir(), "bad")); got != nil || err == nil {
		t.Errorf("non-Git symlink admitted: %v", err)
	}
}

func TestGitStageSharedCopiers(t *testing.T) {
	gs391IsolateGit(t)
	for _, inGit := range []bool{true, false} {
		t.Run(map[bool]string{true: "git", false: "directory"}[inGit], func(t *testing.T) {
			root := t.TempDir()
			if inGit {
				gs391Init(t, root)
			}
			for name, body := range map[string]string{"package.json": "{}", "package-lock.json": "{}", "tsconfig.json": "{}", ".gitignore": "*.cache\n", "src/lib/keep.ts": "export const n = 1;", "src/lib/local.cache": "ignored", "unselected.txt": "outside selection", "bin/generated": "excluded", "out/generated": "excluded"} {
				pa310Write(t, root, name, body)
			}
			copy := filepath.Join(t.TempDir(), "copy")
			if err := copyTree(root, copy); err != nil {
				t.Fatal(err)
			}
			pkg := filepath.Join(t.TempDir(), "package")
			if err := StagePackage(root, []string{"src/lib"}, pkg); err != nil {
				t.Fatal(err)
			}
			for _, dst := range []string{copy, pkg} {
				files := gs391Tree(t, dst)
				_, hasIgnored := files["src/lib/local.cache"]
				if hasIgnored == inGit {
					t.Errorf("Git=%t staged ignored file=%t at %s", inGit, hasIgnored, dst)
				}
				if files["src/lib/keep.ts"] != "export const n = 1;" {
					t.Errorf("lost selected source at %s", dst)
				}
				for _, name := range []string{"bin/generated", "out/generated", ".git/HEAD"} {
					if _, ok := files[name]; ok {
						t.Errorf("staged excluded %s", name)
					}
				}
			}
			if _, err := os.Stat(filepath.Join(pkg, "unselected.txt")); !os.IsNotExist(err) {
				t.Error("StagePackage enlarged its selection")
			}
			a, err := HashExisting(root, []string{"src/lib"})
			if err != nil {
				t.Fatal(err)
			}
			b, err := HashExisting(pkg, []string{"src/lib"})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(a, b) {
				t.Errorf("hash describes files not staged: %v != %v", a, b)
			}
			wantCount := 2
			if inGit {
				wantCount = 1
			}
			if len(a) != 1 || a[0].Files != wantCount {
				t.Errorf("HashExisting counts %v, want %d files", a, wantCount)
			}
			if inGit {
				err := StagePackage(root, []string{"src/lib/local.cache"}, filepath.Join(t.TempDir(), "explicit"))
				if err == nil || !strings.Contains(err.Error(), "src/lib/local.cache") || !strings.Contains(strings.ToLower(err.Error()), "ignor") {
					t.Errorf("ignored explicit selection not refused by name: %v", err)
				}
			}
		})
	}
}

func TestGitStageSessionConsumers(t *testing.T) {
	root, p := gs391Fixture(t)
	gs391Init(t, root)
	p.Manifest.Exhaustive = true
	pa310Write(t, root, pa310Project+"/driver.py", "Limit = 2\nfrom acme.service.core import advance\ndef drive():\n    return advance(1)\n")
	pa310Save(t, p)
	pa310Write(t, root, ".gitignore", "ignored.bin\n")
	pa310Write(t, root, "ignored.bin", "excluded")
	var stagedRoot string
	var selectedFiles []project.ComponentFile
	images, runs, larger := 0, 0, 0
	s, err := PreparePythonApplicationSession(context.Background(), p, filepath.Join(t.TempDir(), "stage"), PythonApplicationSessionOptions{
		SelectImage: func(_ context.Context, w *PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
			images++
			stagedRoot = w.Root
			selectedFiles = append([]project.ComponentFile(nil), w.Files...)
			gs391Mode(t, w, "git")
			if _, err := os.Stat(filepath.Join(w.Root, "ignored.bin")); !os.IsNotExist(err) {
				t.Error("image selection saw ignored file")
			}
			return toolchain.PythonImage, nil, nil
		},
		Execute: func(_ context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
			runs++
			if c.Root != stagedRoot {
				t.Error("execution changed snapshot root")
			}
			if _, err := os.Stat(filepath.Join(c.Root, "ignored.bin")); !os.IsNotExist(err) {
				t.Error("execution saw ignored file")
			}
			if _, err := os.Stat(filepath.Join(c.Root, "later.txt")); !os.IsNotExist(err) {
				t.Error("execution restaged later original edits")
			}
			if len(c.Overlays) != 0 {
				larger++
				body, err := os.ReadFile(c.Overlays[0].Host)
				if err != nil || !strings.Contains(string(body), "Limit = 3") {
					t.Errorf("larger driver not rewritten: %q %v", body, err)
				}
			}
			// No synthetic success evidence: this test observes input delivery.
			return PythonApplicationProcess{ExitCode: 1}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	gs391Mode(t, s.Provenance(), "git")
	pa310Write(t, root, "later.txt", "created after snapshot")
	pa310Write(t, root, "acme/shared.py", "VALUE = 999\n")
	for _, v := range s.Invocations() {
		x, _ := s.Run(context.Background(), v.Key)
		if x == nil {
			t.Fatal("execution did not return provenance")
		}
		gs391Mode(t, x.Provenance, "git")
		if !reflect.DeepEqual(x.Provenance.Files, selectedFiles) {
			t.Error("execution lost snapshot membership")
		}
		if v.Explored {
			x, _, _ := s.RunLarger(context.Background(), v.Key, map[string]int{"Limit": 3})
			if x == nil {
				t.Fatal("larger execution did not return provenance")
			}
			gs391Mode(t, x.Provenance, "git")
			if !reflect.DeepEqual(x.Provenance.Files, selectedFiles) {
				t.Error("larger run changed base snapshot membership")
			}
		}
	}
	if images != 1 || runs != len(s.Invocations())+1 || larger != 1 {
		t.Errorf("consumer counts: images=%d runs=%d larger=%d", images, runs, larger)
	}
}
