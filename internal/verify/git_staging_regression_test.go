package verify

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A root its repository ignores, such as synthesize's default output under
// this repository's ignored out/, holds nothing Git would commit. Every
// stager selects it as a directory outside Git, so nothing in it is silently
// left out, and the repository's own root still selects by Git.
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
	if err := stagingIgnored(root, []string{"local.cache", "src/lib"}); err != nil {
		t.Errorf("declarations in an ignored root were refused: %v", err)
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
