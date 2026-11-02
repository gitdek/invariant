package verify

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A root its repository ignores, with nothing tracked under it, such as
// synthesize's default output under this repository's ignored out/, holds
// nothing Git would commit. The shared copiers select it as a directory
// outside Git, so nothing in it is silently left out, and the repository's
// own root still selects by Git. The strict stager still refuses its
// ignored declarations by name.
func TestGitStageIgnoredRootSelectsDirectory(t *testing.T) {
	gs391IsolateGit(t)
	repo := t.TempDir()
	gs391Init(t, repo)
	writeFiles(t, repo, map[string]string{".gitignore": "/out/\n*.cache\n", "kept.go": "package kept\n"})
	gs391Commit(t, repo)
	root := filepath.Join(repo, "out", "synthesis", "result")
	want := map[string]string{
		"go.mod":            "module result\n",
		"main.go":           "package main\n",
		"local.cache":       "matches a pattern as well",
		"package.json":      "{}",
		"package-lock.json": "{}",
		"src/lib/keep.ts":   "export const n = 1;",
	}
	writeFiles(t, root, want)
	writeFiles(t, root, map[string]string{"bin/generated": "copyTree leaves build output out"})

	selected, err := stagingFiles(root, copyTreeExcluded, false)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Method != "directory" {
		t.Errorf("an ignored root was selected by %q, want directory", selected.Method)
	}
	copy := filepath.Join(t.TempDir(), "copy")
	if err := copyTree(root, copy); err != nil {
		t.Fatal(err)
	}
	if got := gs391Tree(t, copy); !reflect.DeepEqual(got, want) {
		t.Errorf("copyTree staged %v, want %v", gs391Names(got), gs391Names(want))
	}
	if err := stagingIgnored(root, []string{"local.cache", "src/lib"}, false); err != nil {
		t.Errorf("declarations in an ignored root were refused: %v", err)
	}
	if err := stagingIgnored(root, []string{"src/lib"}, true); err == nil || !strings.Contains(err.Error(), `"src/lib"`) {
		t.Errorf("the strict stager admitted a declaration its repository ignores: %v", err)
	}
	pkg := filepath.Join(t.TempDir(), "package")
	if err := StagePackage(root, []string{"src/lib"}, pkg); err != nil {
		t.Fatal(err)
	}
	a, err := HashExisting(root, []string{"src/lib"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashExisting(pkg, []string{"src/lib"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) || len(a) != 1 || a[0].Files != 1 {
		t.Errorf("hash describes files not staged: %v != %v", a, b)
	}

	top, err := stagingFiles(repo, copyTreeExcluded, false)
	if err != nil {
		t.Fatal(err)
	}
	if top.Method != "git" || !reflect.DeepEqual(top.Files, []string{".gitignore", "kept.go"}) {
		t.Errorf("the repository's root selected %q %v", top.Method, top.Files)
	}
}

// Tracked files keep an ignored root Git's to select, so the work tree
// stages what a fresh clone does, not a local payload beside them.
func TestGitStageIgnoredRootWithTrackedFiles(t *testing.T) {
	gs391IsolateGit(t)
	repo := t.TempDir()
	gs391Init(t, repo)
	writeFiles(t, repo, map[string]string{".gitignore": "lib/\n", "lib/app/main.go": "package main\n"})
	gs391Git(t, repo, "add", "-f", ".gitignore", "lib/app/main.go")
	gs391Git(t, repo, "commit", "--quiet", "-m", "Tracked under an ignored directory")
	writeFiles(t, repo, map[string]string{"lib/app/payload.bin": "local only"})
	copy := filepath.Join(t.TempDir(), "copy")
	if err := copyTree(filepath.Join(repo, "lib", "app"), copy); err != nil {
		t.Fatal(err)
	}
	if got, want := gs391Tree(t, copy), map[string]string{"main.go": "package main\n"}; !reflect.DeepEqual(got, want) {
		t.Errorf("copyTree staged %v, want %v", gs391Names(got), gs391Names(want))
	}
}

// A Python application whose root its repository ignores is refused by name
// before image selection, as #391 asks, rather than staged whole.
func TestGitStageRefusesIgnoredApplicationRoot(t *testing.T) {
	root, p := gs391Fixture(t)
	outer := filepath.Dir(root)
	gs391Init(t, outer)
	writeFiles(t, outer, map[string]string{".gitignore": "/" + filepath.Base(root) + "/\n"})
	got, err := StagePythonApplication(pa310Load(t, p), filepath.Join(t.TempDir(), "stage"))
	if got != nil || err == nil || !strings.Contains(err.Error(), "ignored by Git") {
		t.Errorf("an ignored application root was staged: %v", err)
	}
}

// A tracked file replaced by a repository is a nested repository whose files
// Git doesn't list, so it's refused by name rather than left out.
func TestGitStageRefusesRepositoryOverTrackedFile(t *testing.T) {
	gs391IsolateGit(t)
	root := t.TempDir()
	gs391Init(t, root)
	writeFiles(t, root, map[string]string{"main.go": "package main\n", "nested": "a file"})
	gs391Commit(t, root)
	if err := os.Remove(filepath.Join(root, "nested")); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, root, map[string]string{"nested/lib.go": "package lib\n"})
	gs391Init(t, filepath.Join(root, "nested"))
	for _, strict := range []bool{false, true} {
		_, err := stagingFiles(root, copyTreeExcluded, strict)
		if err == nil || !strings.Contains(err.Error(), `"nested"`) || !strings.Contains(err.Error(), "nested Git repositories") {
			t.Errorf("strict=%t: a repository over a tracked file wasn't refused by name: %v", strict, err)
		}
	}
}

// Copiers that skipped symlinks before Git selection still skip them under
// Git, so a fresh clone's verdict matches a plain directory's.
func TestGitStageCopierSkipsSelectedSymlinks(t *testing.T) {
	gs391IsolateGit(t)
	for _, inGit := range []bool{true, false} {
		t.Run(map[bool]string{true: "git", false: "directory"}[inGit], func(t *testing.T) {
			root := t.TempDir()
			if inGit {
				gs391Init(t, root)
			}
			want := map[string]string{"main.go": "package main\n", "docs/x.md": "docs"}
			writeFiles(t, root, want)
			if err := os.Symlink(filepath.Join("docs", "x.md"), filepath.Join(root, "README.md")); err != nil {
				t.Fatal(err)
			}
			if inGit {
				gs391Commit(t, root)
			}
			copy := filepath.Join(t.TempDir(), "copy")
			if err := copyTree(root, copy); err != nil {
				t.Fatalf("a selected symlink blocked the copy: %v", err)
			}
			if got := gs391Tree(t, copy); !reflect.DeepEqual(got, want) {
				t.Errorf("copyTree staged %v, want %v", gs391Names(got), gs391Names(want))
			}
		})
	}
}

// A tracked file replaced by a directory isn't a file to stage. Git lists
// the files under it, which git add -A would commit, strict or not.
func TestGitStageTrackedFileReplacedByDirectory(t *testing.T) {
	gs391IsolateGit(t)
	root := t.TempDir()
	gs391Init(t, root)
	writeFiles(t, root, map[string]string{"main.go": "package main\n", "data": "a file"})
	gs391Commit(t, root)
	if err := os.Remove(filepath.Join(root, "data")); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, root, map[string]string{"data/new.txt": "now a directory"})
	for _, strict := range []bool{false, true} {
		selected, err := stagingFiles(root, copyTreeExcluded, strict)
		if err != nil {
			t.Fatalf("strict=%t: %v", strict, err)
		}
		if want := []string{"data/new.txt", "main.go"}; !reflect.DeepEqual(selected.Files, want) {
			t.Errorf("strict=%t selected %v, want %v", strict, selected.Files, want)
		}
	}
}

// Git lists an untracked repository inside the root only as its directory,
// so staging refuses it by name rather than leaving its files out.
func TestGitStageRefusesNestedRepository(t *testing.T) {
	gs391IsolateGit(t)
	root := t.TempDir()
	gs391Init(t, root)
	writeFiles(t, root, map[string]string{"main.go": "package main\n"})
	gs391Commit(t, root)
	nested := filepath.Join(root, "vendor", "lib")
	writeFiles(t, nested, map[string]string{"lib.go": "package lib\n"})
	gs391Init(t, nested)
	_, err := stagingFiles(root, copyTreeExcluded, false)
	if err == nil || !strings.Contains(err.Error(), `"vendor/lib"`) || !strings.Contains(err.Error(), "nested Git repositories") {
		t.Errorf("a nested repository wasn't refused by name: %v", err)
	}
}

// A root reached through a symlink to a directory below a work tree's top,
// where no .git lies on its own path, is still Git's to select, so ignored
// files stay out and the method says git.
func TestGitStageSymlinkedRootSelectsByGit(t *testing.T) {
	gs391IsolateGit(t)
	repo := t.TempDir()
	gs391Init(t, repo)
	writeFiles(t, repo, map[string]string{".gitignore": "*.cache\n", "sub/inner/main.go": "package main\n", "sub/inner/local.cache": "ignored"})
	gs391Commit(t, repo)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Join(repo, "sub"), link); err != nil {
		t.Fatal(err)
	}
	selected, err := stagingFiles(filepath.Join(link, "inner"), copyTreeExcluded, false)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Method != "git" || !reflect.DeepEqual(selected.Files, []string{"main.go"}) {
		t.Errorf("a root through a symlink selected %q %v", selected.Method, selected.Files)
	}
}

// check-ignore reads its input as pathspecs, so a declaration named :x must
// still be checked as itself, not as x.
func TestGitStageRefusesIgnoredColonDeclaration(t *testing.T) {
	gs391IsolateGit(t)
	root := t.TempDir()
	gs391Init(t, root)
	writeFiles(t, root, map[string]string{"package.json": "{}", "package-lock.json": "{}", ".gitignore": ":x\n", ":x": "ignored", "x": "not ignored"})
	gs391Commit(t, root)
	err := StagePackage(root, []string{":x"}, filepath.Join(t.TempDir(), "colon"))
	if err == nil || !strings.Contains(err.Error(), `":x"`) || !strings.Contains(err.Error(), "ignored") {
		t.Errorf("the ignored declaration :x wasn't refused by name: %v", err)
	}
	if err := StagePackage(root, []string{"x"}, filepath.Join(t.TempDir(), "plain")); err != nil {
		t.Errorf("x was refused: %v", err)
	}
}
