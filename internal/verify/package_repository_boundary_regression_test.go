package verify

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Naming a nested repository, or a path inside it, must not make it a new
// staging root that loses the containing package's repository boundary.
func TestPackageExplicitRepositoryBoundary(t *testing.T) {
	gs391IsolateGit(t)
	for _, mode := range []string{"ignored-deleted", "git", "outside Git"} {
		for _, metadata := range []string{"directory", "file"} {
			t.Run(mode+"/"+metadata, func(t *testing.T) {
				root := t.TempDir()
				if mode == "ignored-deleted" {
					_, root = ds400IgnoredRoot(t, "deleted.ts")
				} else if mode == "git" {
					gs391Init(t, root)
				}
				writeFiles(t, root, map[string]string{
					"package.json":         "{}",
					"package-lock.json":    "{}",
					"src/source.ts":        "selected sibling",
					"vendor/source.ts":     "nested source",
					"vendor/lib/source.ts": "nested descendant",
				})
				nested := filepath.Join(root, "vendor")
				if metadata == "directory" {
					gs391Init(t, nested)
				} else {
					gs391Git(t, nested, "init", "--quiet", "--separate-git-dir", filepath.Join(t.TempDir(), "metadata"))
				}
				for _, name := range []string{"vendor", "vendor/source.ts", "vendor/lib", "vendor/lib/source.ts"} {
					t.Run(name, func(t *testing.T) {
						dst := filepath.Join(t.TempDir(), "stage")
						stageErr := StagePackage(root, []string{name}, dst)
						hashes, hashErr := HashExisting(root, []string{name})
						if mode != "outside Git" {
							for operation, err := range map[string]error{"staging": stageErr, "hashing": hashErr} {
								if err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "nested Git repositories") {
									t.Errorf("%s crossed the package's repository boundary: %v", operation, err)
								}
							}
							if _, err := os.Lstat(dst); !os.IsNotExist(err) {
								t.Errorf("refused selection wrote a partial package: %v", err)
							}
							return
						}
						if stageErr != nil || hashErr != nil {
							t.Fatalf("selection outside Git changed: staging=%v hashing=%v", stageErr, hashErr)
						}
						files := 1
						if name == "vendor" {
							files = 2
						}
						staged, err := HashExisting(dst, []string{name})
						if err != nil || !reflect.DeepEqual(hashes, staged) || len(hashes) != 1 || hashes[0].Files != files {
							t.Errorf("selection outside Git differs from staged bytes: %v != %v (%v)", hashes, staged, err)
						}
					})
				}

				// An unselected repository must not block an ordinary selection.
				dst := filepath.Join(t.TempDir(), "sibling")
				if err := StagePackage(root, []string{"src"}, dst); err != nil {
					t.Fatal(err)
				}
				want := map[string]string{"package.json": "{}", "package-lock.json": "{}", "src/source.ts": "selected sibling"}
				if got := gs391Tree(t, dst); !reflect.DeepEqual(got, want) {
					t.Errorf("explicit package selection changed: %v, want %v", got, want)
				}
				hashes, err := HashExisting(root, []string{"src"})
				if err != nil || len(hashes) != 1 || hashes[0].Files != 1 {
					t.Errorf("unselected repository blocked hashing: %v, %v", hashes, err)
				}
			})
		}
	}
}
