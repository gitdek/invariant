package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// An ignored root falls back only when its tracked ordinary files are gone.
// Keep this observable through both copiers, their existing-code hash, and
// controls for roots that still belong to Git or require strict staging.
func TestDeletedTrackedStagingEvidence(t *testing.T) {
	gs391IsolateGit(t)
	rootWithDeleted := func(t *testing.T, ignored bool) (string, string) {
		t.Helper()
		repo := t.TempDir()
		gs391Init(t, repo)
		patterns := "*.cache\n"
		if ignored {
			patterns += "/package/\n"
		}
		writeFiles(t, repo, map[string]string{".gitignore": patterns, "package/src/deleted.ts": "old index bytes"})
		gs391Git(t, repo, "add", "-f", "package/src/deleted.ts")
		root := filepath.Join(repo, "package")
		if err := os.Remove(filepath.Join(root, "src", "deleted.ts")); err != nil {
			t.Fatal(err)
		}
		return repo, root
	}
	assertTree := func(t *testing.T, root string, want map[string]string) {
		t.Helper()
		if got := gs391Tree(t, root); !reflect.DeepEqual(got, want) {
			t.Errorf("staged bytes = %v, want %v", got, want)
		}
	}

	t.Run("ignored root copies current files", func(t *testing.T) {
		_, root := rootWithDeleted(t, true)
		want := map[string]string{
			"package.json": "{}", "package-lock.json": "{}", "tsconfig.json": "{}",
			"go.mod": "module example.invalid/fallback\n", "main.go": "package main\n",
			"src/keep.ts": "export const n = 1;", "src/local.cache": "included in directory mode",
			"unselected.txt": "outside the explicit existing boundary",
		}
		writeFiles(t, root, want)
		writeFiles(t, root, map[string]string{"bin/output": "excluded", "out/output": "excluded", "src/bin/output": "excluded", "src/out/output": "excluded"})
		copy := filepath.Join(t.TempDir(), "copy")
		if err := copyTree(root, copy); err != nil {
			t.Errorf("copyTree: %v", err)
		} else {
			assertTree(t, copy, want)
		}
		selected, err := stagingFiles(root, copyTreeExcluded, false)
		if err != nil || selected == nil || selected.Method != "directory" {
			t.Errorf("ignored root with only deleted tracked files: selection=%v, error=%v", selected, err)
		}
		pkg := filepath.Join(t.TempDir(), "package")
		if err := StagePackage(root, []string{"src"}, pkg); err != nil {
			t.Errorf("StagePackage refused fallback: %v", err)
		} else {
			assertTree(t, pkg, map[string]string{
				"package.json": "{}", "package-lock.json": "{}", "tsconfig.json": "{}",
				"src/keep.ts": want["src/keep.ts"], "src/local.cache": want["src/local.cache"],
			})
		}
		// Compute the receipt digest from the expected staged paths and bytes,
		// without using HashExisting itself to derive the expected value.
		h := sha256.New()
		for _, name := range []string{"src/keep.ts", "src/local.cache"} {
			fmt.Fprintf(h, "%s\n%d\n", name, len(want[name]))
			h.Write([]byte(want[name]))
		}
		hashes, err := HashExisting(root, []string{"src"})
		expected := []ExistingCode{{Path: "src", Files: 2, SHA256: "sha256:" + hex.EncodeToString(h.Sum(nil))}}
		if err != nil || !reflect.DeepEqual(hashes, expected) {
			t.Errorf("existing-code hash = %v, %v; want %v", hashes, err, expected)
		}
	})

	t.Run("surviving tracked file keeps Git selection", func(t *testing.T) {
		repo, root := rootWithDeleted(t, true)
		writeFiles(t, root, map[string]string{"live.go": "index bytes", "payload.cache": "ignored"})
		gs391Git(t, repo, "add", "-f", "package/live.go")
		writeFiles(t, root, map[string]string{"live.go": "working-tree bytes"})
		copy := filepath.Join(t.TempDir(), "copy")
		if err := copyTree(root, copy); err != nil {
			t.Fatal(err)
		}
		assertTree(t, copy, map[string]string{"live.go": "working-tree bytes"})
	})

	t.Run("nonignored root keeps Git selection", func(t *testing.T) {
		_, root := rootWithDeleted(t, false)
		writeFiles(t, root, map[string]string{"new.go": "package current\n", "payload.cache": "ignored"})
		copy := filepath.Join(t.TempDir(), "copy")
		if err := copyTree(root, copy); err != nil {
			t.Fatal(err)
		}
		assertTree(t, copy, map[string]string{"new.go": "package current\n"})
		selected, err := stagingFiles(root, copyTreeExcluded, false)
		if err != nil || selected == nil || selected.Method != "git" {
			t.Errorf("nonignored all-deleted root: selection=%v, error=%v", selected, err)
		}
	})

	t.Run("strict application still refuses ignored declarations", func(t *testing.T) {
		root, p := gs391Fixture(t)
		repo := filepath.Dir(root)
		gs391Init(t, repo)
		writeFiles(t, repo, map[string]string{".gitignore": "/application/\n", "application/deleted.py": "old bytes"})
		gs391Git(t, repo, "add", "-f", "application/deleted.py")
		if err := os.Remove(filepath.Join(root, "deleted.py")); err != nil {
			t.Fatal(err)
		}
		if err := stagingIgnored(root, []string{"acme/service/core.py"}, true); err == nil || !strings.Contains(err.Error(), "ignored by Git") {
			t.Errorf("strict declaration admitted: %v", err)
		}
		w, err := StagePythonApplication(pa310Load(t, p), filepath.Join(t.TempDir(), "stage"))
		if w != nil || err == nil || !strings.Contains(err.Error(), "ignored by Git") {
			t.Errorf("strict application root admitted: workspace=%v, error=%v", w, err)
		}
	})

	t.Run("missing gitlink still refused", func(t *testing.T) {
		repo, root := rootWithDeleted(t, true)
		gs391Git(t, repo, "add", ".gitignore")
		gs391Git(t, repo, "commit", "--quiet", "-m", "Ignored root fixture")
		head := gs391Git(t, repo, "rev-parse", "HEAD")
		gs391Git(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+head+",package/vendor")
		writeFiles(t, root, map[string]string{"package.json": "{}", "package-lock.json": "{}", "payload.cache": "must not trigger fallback"})
		for _, copier := range []string{"copyTree", "StagePackage"} {
			dst := filepath.Join(t.TempDir(), "copy")
			var err error
			if copier == "copyTree" {
				err = copyTree(root, dst)
			} else {
				err = StagePackage(root, []string{"vendor"}, dst)
			}
			if err == nil || !strings.Contains(err.Error(), "vendor") || !strings.Contains(err.Error(), "submodule") {
				t.Errorf("%s failed to refuse an uninitialized gitlink by name: %v", copier, err)
			}
		}
	})
}
