package verify

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gitdek/invariant/internal/toolchain"
)

func TestGitStageProvenanceBindsSelection(t *testing.T) {
	root, p := gs391Fixture(t)
	prepare := func(mode string) *PythonApplicationSession {
		t.Helper()
		s, err := PreparePythonApplicationSession(context.Background(), pa310Load(t, p), filepath.Join(t.TempDir(), "stage"), PythonApplicationSessionOptions{
			SelectImage: func(_ context.Context, w *PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
				gs391Mode(t, w, mode)
				return toolchain.PythonImage, nil, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		})
		gs391Mode(t, s.Provenance(), mode)
		return s
	}

	directory := prepare("directory")
	// Adding repository metadata changes the selection method without changing
	// any application file: .git itself must never enter either snapshot.
	gs391Init(t, root)
	git := prepare("git")
	a, b := directory.Provenance(), git.Provenance()
	if !reflect.DeepEqual(a.Files, b.Files) {
		t.Fatalf("selection methods staged different application bytes:\ndirectory: %v\ngit: %v", a.Files, b.Files)
	}
	if a.Fingerprint == "" || b.Fingerprint == "" || a.Fingerprint == b.Fingerprint {
		t.Fatalf("selection method is not bound to provenance: directory=%q git=%q", a.Fingerprint, b.Fingerprint)
	}
	b.FileSelection, b.Fingerprint = a.FileSelection, a.Fingerprint
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("provenance differs beyond selection method and fingerprint:\ndirectory: %+v\ngit: %+v", a, b)
	}
	gs391Mode(t, directory.Provenance(), "directory")
	gs391Mode(t, git.Provenance(), "git")
}
