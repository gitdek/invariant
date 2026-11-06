package plumbing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func TestNativeDirectoryOwnerBinding661(t *testing.T) {
	rootPart := n503Part("go", ".")
	rootPart.Name = "root"
	app, deep, peer := n503Part("go", "app"), n503Part("go", "app/tools"), n503Part("go", "peer")
	deep.Name = "deep"
	peer.Inputs = append(peer.Inputs, "app/tools/go.mod", "shared/input.txt")
	_, _, snapshot := fixture484(t, []repository.Part{deep, app, rootPart, peer}, func(c *repository.Config) {
		c.SharedPaths = []repository.SharedPath{{Path: "app/tools/new_accept_test.go", Parts: []string{"app", "root", "peer"}}}
	})
	const source = "package fixture\nimport \"testing\"\nfunc TestReady(t *testing.T) { t.Fatal(\"pending\") }\n"
	for file, want := range map[string]string{
		"app/tools/new_accept_test.go":     "deep",
		"app/toolsmith/new_accept_test.go": "app",
		"appish/new_accept_test.go":        "root",
		"peer/new_accept_test.go":          "peer",
	} {
		owner, ok, err := snapshot.DirectoryOwner(file)
		if err != nil || !ok || owner.Name != want {
			t.Fatalf("directory owner of %s = %+v, %v, %v", file, owner, ok, err)
		}
		p := n503Plan(t, "go", want, file, "TestReady", source)
		p.Files = []string{"app/main.go"}
		bound := n503Read(t, p, snapshot)
		if err := bound.ValidateRepository(t.Context(), snapshot); err != nil {
			t.Fatal(err)
		}
		owner.Inputs[0] = "mutated"
		owner.Commands["test_one"].Argv[0] = "mutated"
		if fresh := n503Read(t, p, snapshot); fresh.Hash() != bound.Hash() {
			t.Fatal("mutating a returned owner changed native binding")
		}
		for _, wrong := range []string{"deep", "app", "root", "peer"} {
			if wrong == want {
				continue
			}
			bad := n503Clone(t, p)
			bad.Tests[0].Part = wrong
			if _, err := ReadRepositoryDraft(t.Context(), n503Draft(t, bad), snapshot); err == nil {
				t.Fatalf("%s executed in %s instead of directory owner %s", file, wrong, want)
			}
		}
	}
	parts, err := snapshot.PartsFor([]string{"app/tools/go.mod"})
	if err != nil || len(parts) != 2 || parts[0].Name != "deep" || parts[1].Name != "peer" {
		t.Fatalf("dependency check ownership changed: %+v, %v", parts, err)
	}
	parts, err = snapshot.PartsFor([]string{"app/tools/new_accept_test.go"})
	if err != nil || len(parts) != 4 {
		t.Fatalf("shared check ownership changed: %+v, %v", parts, err)
	}
	// A check selection supplied solely by a shared rule supplies no directory
	// in which acceptance may execute. The same is true of outside inputs.
	_, _, noRoot := fixture484(t, []repository.Part{app, peer}, func(c *repository.Config) {
		c.Parts[1].Inputs = []string{"peer/go.mod", "shared/input.txt"}
		c.SharedPaths = []repository.SharedPath{{Path: "shared/", Parts: []string{"app", "peer"}}}
	})
	for _, file := range []string{"shared/new_accept_test.go", "shared/input.txt"} {
		if _, ok, err := noRoot.DirectoryOwner(file); ok || err != nil {
			t.Fatalf("additional check ownership invented a directory for %s: %v, %v", file, ok, err)
		}
		if parts, err := noRoot.PartsFor([]string{file}); err != nil || len(parts) != 2 {
			t.Fatalf("additional check selection lost %s: %+v, %v", file, parts, err)
		}
	}
	for _, name := range []string{"app", "peer"} {
		p := n503Plan(t, "go", name, "shared/new_accept_test.go", "TestReady", source)
		if _, err := ReadRepositoryDraft(t.Context(), n503Draft(t, p), noRoot); err == nil {
			t.Fatal("shared-only path supplied a native execution directory")
		}
	}
	// Retain the captured-base refusal even after committed bytes are cached.
	p := n503Plan(t, "go", "app", "api/existing_test.go", "TestReady", source)
	// This fixture's api directory is outside all parts, so use a captured
	// regular test source in a separate owning part.
	_, _, existing := fixture484(t, []repository.Part{n503Part("go", "api")}, nil)
	p.Tests[0].Part, p.Files = "api", []string{"api/main.go"}
	if _, err := existing.ReadFile(p.Tests[0].File); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRepositoryDraft(t.Context(), n503Draft(t, p), existing); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing cached acceptance was not refused: %v", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	p = n503Plan(t, "go", "api", "api/new_accept_test.go", "TestReady", source)
	if _, err := ReadRepositoryDraft(canceled, n503Draft(t, p), existing); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled binding supplied new acceptance: %v", err)
	}
}
