package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// #661 adds ErrFileMissing and DirectoryOwner(string) (Part, bool, error).
// DirectoryOwner validates the same file paths as PartsFor and IsTrusted;
// absence of an enclosing directory is a false result, not a path error.
func TestSnapshotMissingFileClassification661(t *testing.T) {
	root := repo461(t, contract461(part461("app", ".", "go")))
	if err := os.Symlink("src", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	base := commit461(t, root)
	git461(t, root, "update-index", "--add", "--cacheinfo", "160000,"+base+",vendor")
	git461(t, root, "commit", "--quiet", "--no-verify", "-m", "gitlink")
	s := load461(t, root, "HEAD")
	for _, file := range []string{"absent.go", "new/directory/absent.go", "src/is not a committed regular file.go"} {
		write461(t, root, file, "candidate-only bytes\n")
		for _, read := range []func(string) ([]byte, error){s.ReadFile, func(p string) ([]byte, error) {
			return s.ReadFileContext(t.Context(), p)
		}} {
			_, err := read(file)
			if !errors.Is(fmt.Errorf("caller: %w", err), ErrFileMissing) || errors.Is(err, ErrMissing) {
				t.Fatalf("absent captured file %q: %v; want only ErrFileMissing", file, err)
			}
		}
	}
	for _, file := range []string{"", ".", "../outside", "/absolute", "src/../x", "src//x", `src\x`, ".git/config", "src/.GIT/x", "src/*.go", "bad\nname", "src", "go.mod/child", "alias", "alias/new.go", "vendor", "vendor/new.go"} {
		_, err := s.ReadFile(file)
		if err == nil || errors.Is(err, ErrFileMissing) || errors.Is(err, ErrMissing) {
			t.Errorf("unsafe or nonregular %q classified as absence: %v", file, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, file := range []string{"src/old.go", "absent.go"} {
		_, err := s.ReadFileContext(ctx, file)
		if !errors.Is(err, context.Canceled) || errors.Is(err, ErrFileMissing) || errors.Is(err, ErrMissing) {
			t.Fatalf("canceled read %q: %v", file, err)
		}
	}
	// The tree has a regular entry, but its never-read object is unavailable.
	blob := git461(t, root, "rev-parse", s.Commit()+":src/old.go")
	if err := os.Remove(filepath.Join(root, ".git", "objects", blob[:2], blob[2:])); err != nil {
		t.Fatal(err)
	}
	_, err := s.ReadFile("src/old.go")
	if err == nil || errors.Is(err, ErrFileMissing) || errors.Is(err, ErrMissing) {
		t.Fatalf("unreadable committed object classified as absence: %v", err)
	}
	missing := repo461(t, nil)
	_, err = Load(t.Context(), missing, commit461(t, missing))
	if !errors.Is(err, ErrMissing) || errors.Is(err, ErrFileMissing) {
		t.Fatalf("missing contract lost its separate classification: %v", err)
	}
	_, err = Load(t.Context(), missing, "not-a-revision")
	if err == nil || errors.Is(err, ErrMissing) || errors.Is(err, ErrFileMissing) {
		t.Fatalf("invalid revision classified as absence: %v", err)
	}
	write461(t, missing, ".invariant", "not a directory\n")
	_, err = Load(t.Context(), missing, commit461(t, missing))
	if !errors.Is(err, ErrInvalid) || errors.Is(err, ErrMissing) || errors.Is(err, ErrFileMissing) {
		t.Fatalf("invalid contract parent classified as absence: %v", err)
	}
}

func TestSnapshotDirectoryOwner661(t *testing.T) {
	app, deep, fallback := part461("app", "app", "go"), part461("deep", "app/tools", "go"), part461("root", ".", "go")
	fallback["inputs"] = []string{"go.mod", "app/tools/go.mod"}
	c := contract461(app, deep, fallback)
	c["shared_paths"] = []map[string]any{{"path": "app/tools/new.go", "parts": []string{"root", "app"}}}
	root := repo461(t, c)
	if err := os.Symlink("app", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	base := commit461(t, root)
	git461(t, root, "update-index", "--add", "--cacheinfo", "160000,"+base+",vendor")
	git461(t, root, "commit", "--quiet", "--no-verify", "-m", "gitlink")
	s := load461(t, root, "HEAD")
	wantConfig := s.Config()
	for file, want := range map[string]string{
		"app/src/old.go": "app", "app/new/deep/file.go": "app",
		"app/tools/src/old.go": "deep", "app/tools/new.go": "deep",
		"app/tools/go.mod": "deep", "app/toolsmith/new.go": "app",
		"appish/new.go": "root", "other/new.go": "root",
		Path: "root", "AGENTS.md": "root",
	} {
		owner, ok, err := s.DirectoryOwner(file)
		if err != nil || !ok || owner.Name != want {
			t.Fatalf("DirectoryOwner(%q) = %+v, %v, %v; want %s", file, owner, ok, err, want)
		}
		// All slices, command maps and nested argv must belong to the caller.
		owner.Name = "mutated"
		owner.Lockfiles[0], owner.Inputs[0] = "mutated.lock", "mutated.input"
		owner.Commands["test"].Argv[0] = "mutated"
		delete(owner.Commands, "lint")
		again, ok, err := s.DirectoryOwner(file)
		if err != nil || !ok || again.Name != want || !reflect.DeepEqual(s.Config(), wantConfig) {
			t.Fatalf("owner mutation changed captured authority for %s", file)
		}
	}
	parts461(t, s, []string{"app/tools/new.go"}, "app", "deep", "root")
	parts461(t, s, []string{"app/tools/go.mod"}, "deep", "root")
	parts461(t, s, []string{"app/tools/src/old.go"}, "deep")
	parts461(t, s, []string{"app/toolsmith/new.go"}, "app")
	for _, file := range []string{Path, "AGENTS.md", "SPEC.md", "README.md"} {
		parts461(t, s, []string{file}, "app", "deep", "root")
	}
	if err := os.Remove(filepath.Join(root, "app/tools/src/old.go")); err != nil {
		t.Fatal(err)
	}
	for _, snap := range []*Snapshot{s, load461(t, root, commit461(t, root))} {
		owner, ok, err := snap.DirectoryOwner("app/tools/src/old.go")
		if err != nil || !ok || owner.Name != "deep" {
			t.Fatalf("deleted path lost its directory owner: %+v, %v, %v", owner, ok, err)
		}
		parts461(t, snap, []string{"app/tools/src/old.go"}, "deep")
	}
	for _, bad := range []string{"", ".", "../x", "app//x", "app", "app/go.mod/child", "alias", "alias/new.go", "vendor/new.go", ".git/config"} {
		if _, ok, err := s.DirectoryOwner(bad); err == nil || ok {
			t.Errorf("unsafe owner query %q = %v, %v", bad, ok, err)
		}
		for _, paths := range [][]string{{Path, bad}, {bad, Path}} {
			if parts, err := s.PartsFor(paths); err == nil || len(parts) != 0 {
				t.Errorf("global selection hid unsafe path %q: %+v, %v", bad, parts, err)
			}
		}
	}
	// Additional check owners are valid even without a directory owner.
	app["inputs"] = []string{"app/go.mod", "workspace.env"}
	only := contract461(app)
	only["shared_paths"] = []map[string]any{{"path": "shared/", "parts": []string{"app"}}}
	other := repo461(t, only)
	withoutRoot := load461(t, other, commit461(t, other))
	for _, file := range []string{"shared/new.go", "workspace.env", "AGENTS.md", Path, "appish/new.go", "unowned/new.go"} {
		if _, ok, err := withoutRoot.DirectoryOwner(file); err != nil || ok {
			t.Errorf("safe path without a directory owner %q = %v, %v", file, ok, err)
		}
	}
	parts461(t, withoutRoot, []string{"shared/new.go", "workspace.env"}, "app")
	parts461(t, withoutRoot, nil)
	for _, bad := range []string{"unowned/new.go", "appish/new.go"} {
		for _, paths := range [][]string{{Path, bad}, {bad, Path}} {
			if parts, err := withoutRoot.PartsFor(paths); err == nil || len(parts) != 0 {
				t.Errorf("global selection hid unowned path %q: %+v, %v", bad, parts, err)
			}
		}
	}
}
