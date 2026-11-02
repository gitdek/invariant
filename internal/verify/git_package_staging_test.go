package verify

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGitStagePackageWorkingBytesAndHash(t *testing.T) {
	gs391IsolateGit(t)
	root := t.TempDir()
	gs391Init(t, root)
	writeFiles(t, root, map[string]string{
		"package.json":                     "{}",
		"package-lock.json":                "{}",
		".gitignore":                       "*.cache\nignored-dir/\n/package*.json\n",
		"src/retained.cache":               "indexed bytes",
		"src/deleted.ts":                   "deleted after indexing",
		"src/nested/keep.ts":               "selected source",
		"src/ignored.cache":                "ignored payload",
		"src/out/generated.ts":             "excluded output",
		"src/nested/bin/generated.ts":      "excluded output",
		"src/node_modules/local/module.js": "inside the explicit boundary",
		"ignored-dir/source.ts":            "ignored explicit directory",
		"elsewhere/source.ts":              "outside the explicit boundary",
	})
	gs391Git(t, root, "add", "-f", "src/retained.cache", "src/deleted.ts", "src/out/generated.ts", "ignored-dir/source.ts")
	writeFiles(t, root, map[string]string{"src/retained.cache": "current working bytes"})
	if err := os.Remove(filepath.Join(root, "src/deleted.ts")); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "package")
	if err := StagePackage(root, []string{"src"}, dst); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"package.json":                     "{}",
		"package-lock.json":                "{}",
		"src/retained.cache":               "current working bytes",
		"src/nested/keep.ts":               "selected source",
		"src/node_modules/local/module.js": "inside the explicit boundary",
	}
	if got := gs391Tree(t, dst); !reflect.DeepEqual(got, want) {
		t.Fatalf("staged bytes = %v, want %v", got, want)
	}
	a, err := HashExisting(root, []string{"src"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashExisting(dst, []string{"src"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) || len(a) != 1 || a[0].Files != 3 {
		t.Fatalf("original and staged hashes differ: %v != %v", a, b)
	}
	for _, name := range []string{"src/ignored.cache", "ignored-dir"} {
		if err := StagePackage(root, []string{name}, filepath.Join(t.TempDir(), "refused")); err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(strings.ToLower(err.Error()), "ignor") {
			t.Errorf("ignored declaration %s: %v", name, err)
		}
		if _, err := HashExisting(root, []string{name}); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("hashed ignored declaration %s: %v", name, err)
		}
	}
	for _, name := range []string{"src/retained.cache", "ignored-dir/source.ts"} {
		if err := StagePackage(root, []string{name}, filepath.Join(t.TempDir(), "tracked")); err != nil {
			t.Fatalf("tracked explicit file matching an ignore rule was refused: %v", err)
		}
	}
}

func TestGitStagePackageExplicitSymlinks(t *testing.T) {
	gs391IsolateGit(t)
	for _, inGit := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory", true: "git"}[inGit], func(t *testing.T) {
			root := t.TempDir()
			if inGit {
				gs391Init(t, root)
			}
			writeFiles(t, root, map[string]string{
				"package.json":      "{}",
				"package-lock.json": "{}",
				"src/source.ts":     "source",
			})
			for _, link := range []struct{ name, target string }{{"file.ts", "src/source.ts"}, {"directory", "src"}} {
				if err := os.Symlink(filepath.Join(root, filepath.FromSlash(link.target)), filepath.Join(root, link.name)); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"file.ts", "directory", "directory/source.ts"} {
				dst := filepath.Join(t.TempDir(), "stage")
				err := StagePackage(root, []string{name}, dst)
				a, hashErr := HashExisting(root, []string{name})
				if inGit {
					if err == nil || !strings.Contains(err.Error(), name) {
						t.Errorf("staged symlink declaration %s: %v", name, err)
					}
					if hashErr == nil || !strings.Contains(hashErr.Error(), name) {
						t.Errorf("hashed symlink declaration %s: %v", name, hashErr)
					}
					continue
				}
				if err != nil || hashErr != nil {
					t.Fatalf("non-Git explicit symlink handling changed: stage=%v hash=%v", err, hashErr)
				}
				if name == "directory" {
					if len(a) != 1 || a[0].Files != 0 {
						t.Errorf("directory symlink was followed when hashing: %v", a)
					}
					if _, err := os.Lstat(filepath.Join(dst, name)); !os.IsNotExist(err) {
						t.Errorf("directory symlink was followed when copying: %v", err)
					}
					continue
				}
				body, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(name)))
				if err != nil || string(body) != "source" {
					t.Errorf("non-Git explicit file symlink was not copied: %q %v", body, err)
				}
				b, err := HashExisting(dst, []string{name})
				if err != nil || !reflect.DeepEqual(a, b) {
					t.Errorf("explicit file hashes differ from staged bytes: %v != %v (%v)", a, b, err)
				}
			}
			if err := os.Remove(filepath.Join(root, "package.json")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(root, "package-lock.json"), filepath.Join(root, "package.json")); err != nil {
				t.Fatal(err)
			}
			dst := filepath.Join(t.TempDir(), "metadata")
			err := StagePackage(root, []string{"src/source.ts"}, dst)
			if inGit {
				if err == nil || !strings.Contains(err.Error(), "package.json") {
					t.Errorf("Git metadata symlink was admitted: %v", err)
				}
			} else {
				if err != nil {
					t.Fatalf("non-Git metadata symlink was refused: %v", err)
				}
				if body, err := os.ReadFile(filepath.Join(dst, "package.json")); err != nil || string(body) != "{}" {
					t.Errorf("non-Git metadata symlink was not copied: %q %v", body, err)
				}
			}
		})
	}
}

func TestGitStagePackageSubmoduleBoundaries(t *testing.T) {
	gs391IsolateGit(t)
	root := t.TempDir()
	gs391Init(t, root)
	writeFiles(t, root, map[string]string{
		"package.json":      "{}",
		"package-lock.json": "{}",
		"src/source.ts":     "source",
	})
	gs391Commit(t, root)
	head := gs391Git(t, root, "rev-parse", "HEAD")
	gs391Git(t, root, "update-index", "--add", "--cacheinfo", "160000,"+head+",vendor")
	for _, initialized := range []bool{false, true} {
		t.Run(map[bool]string{false: "uninitialized", true: "initialized"}[initialized], func(t *testing.T) {
			if initialized {
				gs391Git(t, root, "clone", "--quiet", root, filepath.Join(root, "vendor"))
			}
			if err := StagePackage(root, []string{"vendor"}, filepath.Join(t.TempDir(), "refused")); err == nil || !strings.Contains(err.Error(), "vendor") || !strings.Contains(strings.ToLower(err.Error()), "submodule") {
				t.Errorf("explicit submodule was not refused by name: %v", err)
			}
			if _, err := HashExisting(root, []string{"vendor"}); err == nil || !strings.Contains(err.Error(), "vendor") || !strings.Contains(strings.ToLower(err.Error()), "submodule") {
				t.Errorf("explicit submodule was hashed: %v", err)
			}
			dst := filepath.Join(t.TempDir(), "selected")
			if err := StagePackage(root, []string{"src"}, dst); err != nil {
				t.Fatalf("unselected sibling submodule blocked staging: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(dst, "vendor")); !os.IsNotExist(err) {
				t.Errorf("staging enlarged its explicit boundary: %v", err)
			}
			if _, err := HashExisting(root, []string{"src"}); err != nil {
				t.Errorf("unselected sibling submodule blocked hashing: %v", err)
			}
		})
	}
}

func TestGitStageSharedCopierRefusesCorruptIndex(t *testing.T) {
	gs391IsolateGit(t)
	root := t.TempDir()
	gs391Init(t, root)
	writeFiles(t, root, map[string]string{"source.ts": "tracked source"})
	gs391Git(t, root, "add", "source.ts")
	writeFiles(t, root, map[string]string{".git/index": "corrupt index", "local.txt": "must not reach a fallback copy"})
	dst := filepath.Join(t.TempDir(), "stage")
	if err := copyTree(root, dst); err == nil {
		t.Fatal("corrupt Git index silently fell back to directory copying")
	}
	if _, err := os.Lstat(filepath.Join(dst, "local.txt")); !os.IsNotExist(err) {
		t.Errorf("Git failure staged a fallback file: %v", err)
	}
}

func TestGitStageSharedCopierRefusesSymlinkParent(t *testing.T) {
	gs391IsolateGit(t)
	root := t.TempDir()
	gs391Init(t, root)
	writeFiles(t, root, map[string]string{"ordinary/source.ts": "tracked source"})
	gs391Git(t, root, "add", "ordinary/source.ts")
	outside := t.TempDir()
	writeFiles(t, outside, map[string]string{"source.ts": "outside payload"})
	if err := os.RemoveAll(filepath.Join(root, "ordinary")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "ordinary")); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "stage")
	if err := copyTree(root, dst); err == nil || !strings.Contains(err.Error(), "ordinary") {
		t.Fatalf("tracked parent replaced by a symlink was admitted: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "ordinary/source.ts")); !os.IsNotExist(err) {
		t.Errorf("shared copier followed a symlink parent: %v", err)
	}
}
