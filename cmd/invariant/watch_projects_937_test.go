package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

// A watcher's -projects must be the directory its repository's committed
// contract reserves for modeled projects, the repository's own checks skip
// only that one (#937). The contract on the base decides, never the working
// tree, and a repository with no contract runs as it did.
func TestWatchProjectsMustMatchTheContract937(t *testing.T) {
	ctx := t.Context()
	f := gateFixtureFor736(t, "go")
	agree := func(projects string) {
		t.Helper()
		if err := checkWatchProjects(ctx, f.root, "main", projects); err != nil {
			t.Fatalf("-projects %q agrees with the contract, and was refused: %v", projects, err)
		}
	}
	refuse := func(projects string, says ...string) {
		t.Helper()
		err := checkWatchProjects(ctx, f.root, "main", projects)
		if err == nil {
			t.Fatalf("a watcher with -projects %q started against a contract that reserves another directory", projects)
		}
		for _, want := range says {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("-projects %q: the refusal doesn't say %q: %v", projects, want, err)
			}
		}
	}

	// The fixture's contract names no directory, so it reserves invariant,
	// and the watcher's own default, examples, disagrees with it.
	agree("invariant")
	refuse("examples", `"examples"`, `"invariant"`, "names no projects directory", "-projects invariant", `"projects_directory": "examples"`, repository.Path, "main")

	f.config.ProjectsDirectory = "web/model checks"
	gateWrite736(t, f.root, repository.Path, gateJSON736(t, f.config))
	f.base = gateCommit736(t, f.root)
	for _, same := range []string{"web/model checks", "web/model checks/", "./web/model checks"} {
		agree(same)
	}
	refuse("invariant", `"invariant"`, `declares "web/model checks" as its projects directory`, "-projects 'web/model checks'", `"projects_directory": "invariant"`)
	// A value that can't be declared gets only the fix that works.
	err := checkWatchProjects(ctx, f.root, "main", "../elsewhere")
	if err == nil || strings.Contains(err.Error(), "commit") || !strings.Contains(err.Error(), "-projects 'web/model checks'") {
		t.Fatalf("an undeclarable -projects must be told only to pass the contract's: %v", err)
	}

	// The working tree supplies nothing.
	f.config.ProjectsDirectory = "examples"
	gateWrite736(t, f.root, repository.Path, gateJSON736(t, f.config))
	refuse("examples", `"web/model checks"`)
	agree("web/model checks")

	// A contract that can't be read is refused, as setup refuses it.
	gateWrite736(t, f.root, repository.Path, `{"version":999}`)
	f.base = gateCommit736(t, f.root)
	if err := checkWatchProjects(ctx, f.root, "main", "invariant"); err == nil {
		t.Fatal("a malformed committed contract was taken for none")
	}

	// No contract: any directory, as before.
	if err := os.Remove(filepath.Join(f.root, repository.Path)); err != nil {
		t.Fatal(err)
	}
	f.base = gateCommit736(t, f.root)
	for _, projects := range []string{"examples", "invariant", "core"} {
		agree(projects)
	}
}
