package repository

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestSnapshotIgnoresReplacementRefs(t *testing.T) {
	root := repo461(t, contract461(part461("app", ".", "go")))
	base := commit461(t, root)
	snapshot := load461(t, root, "refs/heads/main")
	oldRules := git461(t, root, "rev-parse", base+":AGENTS.md")
	write461(t, root, "AGENTS.md", "Candidate rules.\n")
	candidate := commit461(t, root)
	newRules := git461(t, root, "rev-parse", candidate+":AGENTS.md")
	git461(t, root, "replace", base, candidate)
	git461(t, root, "replace", oldRules, newRules)

	for _, captured := range []*Snapshot{snapshot, load461(t, root, base)} {
		if captured.Commit() != base {
			t.Fatalf("replacement changed captured commit: %s", captured.Commit())
		}
		read461(t, captured, "AGENTS.md", "Read SPEC.md before changing code.\n")
	}
}

func TestSnapshotIgnoresGitEnvironmentRedirection(t *testing.T) {
	root := repo461(t, contract461(part461("app", ".", "go")))
	base := commit461(t, root)
	other := repo461(t, nil)
	commit461(t, other)

	// Git honors these before its working directory unless the loader removes
	// them. The selected checkout must remain authoritative for both operations.
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_OBJECT_DIRECTORY", filepath.Join(other, ".git", "objects"))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.bare")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	snapshot := load461(t, root, "refs/heads/main")
	if snapshot.Commit() != base {
		t.Fatalf("environment changed checkout: got %s, want %s", snapshot.Commit(), base)
	}
	read461(t, snapshot, "AGENTS.md", "Read SPEC.md before changing code.\n")
}

func TestSnapshotMissingRequiresValidContractParent(t *testing.T) {
	root := repo461(t, nil)
	write461(t, root, ".invariant", "This is a file, not a configuration directory.\n")
	_, err := Load(context.Background(), root, commit461(t, root))
	if !errors.Is(err, ErrInvalid) || errors.Is(err, ErrMissing) {
		t.Fatalf("non-directory configuration parent = %v; want only ErrInvalid", err)
	}
}

func TestSnapshotCancelledLoadIsNotMissing(t *testing.T) {
	root := repo461(t, nil)
	base := commit461(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Load(ctx, root, base)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrMissing) {
		t.Fatalf("cancelled load = %v; want cancellation, not missing contract", err)
	}
}
