package verify

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

func TestPythonApplicationAdditionalPathFailures(t *testing.T) {
	for _, name := range []string{"empty selection", "non-Python selection", "excluded source", "excluded working directory", "linked source parent", "linked model", "missing model", "escaping code"} {
		t.Run(name, func(t *testing.T) {
			root, p := pa310Fixture(t, false)
			switch name {
			case "empty selection":
				if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
					t.Fatal(err)
				}
				p.Manifest.Existing = []string{"empty"}
			case "non-Python selection":
				p.Manifest.Existing = []string{"acme/service/increment.txt"}
			case "excluded source":
				pa310Write(t, root, ".venv/core.py", "def f(): pass\n")
				p.Manifest.Existing = []string{".venv/core.py"}
			case "excluded working directory":
				pa310Write(t, root, ".git/keep", "ignored")
				p.Manifest.Components.Checks[0].Invocations[0].WorkingDir = ".git"
			case "linked source parent":
				from := filepath.Join(root, "acme/service")
				if err := os.Rename(from, from+"-real"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("service-real", from); err != nil {
					t.Fatal(err)
				}
			case "linked model":
				from := p.ModulePath()
				if err := os.Rename(from, from+".real"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(from+".real", from); err != nil {
					t.Fatal(err)
				}
			case "missing model":
				p.Manifest.Module = "absent.tla"
			case "escaping code":
				p.Manifest.Code = ".."
			}
			pa310Save(t, p)
			if _, err := project.Load(p.Dir); err == nil {
				t.Fatal("invalid application loaded")
			}
			if w, err := StagePythonApplication(p, filepath.Join(t.TempDir(), "copy")); err == nil || w != nil {
				t.Fatalf("invalid application staged: %v", err)
			}
		})
	}
}

func TestPythonApplicationStageWithoutComponents(t *testing.T) {
	root, p := pa310Fixture(t, false)
	// A directory selection can find sources below its immediate children;
	// that does not infer a recursive production inventory.
	p.Manifest.Existing = []string{"acme"}
	p.Manifest.Components = nil
	pa310Save(t, p)
	dst := t.TempDir()
	w, err := StagePythonApplication(pa310Load(t, p), dst)
	if err != nil {
		t.Fatal(err)
	}
	if w.Inventory.Mode != "legacy" || len(w.Inventory.Components) != 0 || w.Inventory.Provenance != nil {
		t.Fatalf("staging inferred production coverage: %+v", w.Inventory)
	}
	if len(w.Files) != len(pa310Tree(t, root)) {
		t.Fatal("legacy declaration omitted application files")
	}
	if w.Project.Dir != filepath.Join(dst, filepath.FromSlash(pa310Project)) {
		t.Fatal("wrong staged project path")
	}
}
