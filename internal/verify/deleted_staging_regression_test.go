package verify

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func ds400IgnoredRoot(t *testing.T, tracked string) (repo, root string) {
	t.Helper()
	repo = t.TempDir()
	gs391Init(t, repo)
	root = filepath.Join(repo, "package")
	writeFiles(t, repo, map[string]string{".gitignore": "/package/\n", "package/" + tracked: "indexed bytes"})
	gs391Git(t, repo, "add", "-f", "package/"+tracked)
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(tracked))); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, root, map[string]string{"local.cache": "directory-only bytes"})
	return repo, root
}

// A replacement is not a deleted indexed path: Git must still select it,
// apply the copier's exclusions, and refuse unsafe repository boundaries.
func TestGitStageDeletedTrackedPreservesReplacements(t *testing.T) {
	gs391IsolateGit(t)
	for _, kind := range []string{"directory", "symlink", "repository", "symlink parent", "excluded symlink parent"} {
		t.Run(kind, func(t *testing.T) {
			tracked := "old"
			if strings.Contains(kind, "parent") {
				tracked = "old/source.go"
			}
			if strings.HasPrefix(kind, "excluded") {
				tracked = "bin/source.go"
			}
			_, root := ds400IgnoredRoot(t, tracked)
			var wantError string
			switch kind {
			case "directory", "repository":
				writeFiles(t, root, map[string]string{"old/new.go": "ignored replacement contents"})
				if kind == "repository" {
					gs391Init(t, filepath.Join(root, "old"))
					wantError = "nested Git repositories"
				}
			case "symlink", "symlink parent", "excluded symlink parent":
				name := strings.Split(tracked, "/")[0]
				if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), filepath.Join(root, name)); err != nil {
					t.Fatal(err)
				}
				if kind == "symlink parent" {
					wantError = `parent "old" is not a directory`
				}
			}
			selected, err := stagingFiles(root, copyTreeExcluded, false)
			if wantError != "" {
				if err == nil || !strings.Contains(err.Error(), wantError) {
					t.Fatalf("replacement was not refused: selection=%v, error=%v", selected, err)
				}
				return
			}
			if err != nil || selected == nil || selected.Method != "git" || len(selected.Files) != 0 {
				t.Errorf("replacement widened Git selection: selection=%v, error=%v", selected, err)
			}
		})
	}
}

// The new fallback cannot cross a nested repository, but retains each
// copier's exclusions and the established directory behavior outside Git.
func TestGitStageDeletedTrackedFallbackRepositoryBoundaries(t *testing.T) {
	gs391IsolateGit(t)
	for _, kind := range []string{"nested", "excluded", "outside Git"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			if kind != "outside Git" {
				_, root = ds400IgnoredRoot(t, "deleted.go")
			}
			name := "src/vendor/lib"
			if kind == "excluded" {
				name = "src/bin/lib"
			}
			writeFiles(t, root, map[string]string{name + "/new.go": "nested source"})
			gs391Init(t, filepath.Join(root, filepath.FromSlash(name)))
			selected, err := stagingFiles(root, copyTreeExcluded, false)
			if kind == "nested" {
				if err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "nested Git repositories") {
					t.Fatalf("fallback crossed a nested repository: selection=%v, error=%v", selected, err)
				}
				return
			}
			want := []string{"local.cache"}
			if kind == "outside Git" {
				want = []string{name + "/new.go"}
			}
			if err != nil || selected == nil || selected.Method != "directory" || !reflect.DeepEqual(selected.Files, want) {
				t.Errorf("directory boundaries changed: selection=%v, error=%v; want %v", selected, err, want)
			}
		})
	}
}

func TestGitStageDeletedTrackedFallbackRefusesGitFailure(t *testing.T) {
	gs391IsolateGit(t)
	repo, root := ds400IgnoredRoot(t, "deleted.go")
	writeFiles(t, repo, map[string]string{".git/index": "corrupt index"})
	if selected, err := stagingFiles(root, copyTreeExcluded, false); err == nil {
		t.Fatalf("Git failure widened an ignored root to directory selection: %v", selected)
	}
}
