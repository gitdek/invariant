package verify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

// #320: an application that runs as `uv run serve.py` declares its
// environment inline, and its root requirements.txt, here one that cannot
// install, belongs to something else.
const pa320Block = "# /// script\n# requires-python = \">=3.11\"\n# dependencies = [\"packaging==24.2\"]\n# ///\n"

func pa320Fixture(t *testing.T) (string, *project.Project) {
	t.Helper()
	root, p := pa315Fixture(t, true)
	pa310Write(t, root, "requirements.txt", "definitely-not-a-real-project-package==0\n")
	pa310Write(t, root, "serve.py", "#!/usr/bin/env python3\n"+pa320Block+"from tasks.lifecycle import advance\nprint(advance('pending'))\n")
	p.Manifest.Environment = "serve.py"
	pa310Save(t, p)
	return root, p
}

func TestPythonDependenciesFromInlineMetadata(t *testing.T) {
	root, p := pa320Fixture(t)
	if _, err := project.ResolvePythonApplication(p); err != nil {
		t.Fatalf("environment script refused: %v", err)
	}
	t.Run("manifest", func(t *testing.T) { pa320Manifest(t) })
	t.Run("evidence", pa320Evidence)
	if os.Getenv("INVARIANT_PYTHON_DEPS_REAL") != "1" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if b, err := toolchain.Docker(ctx, "info").CombinedOutput(); err != nil {
		t.Fatalf("real suite requested but Docker unavailable: %v\n%s", err, b)
	}
	t.Run("real session", func(t *testing.T) { pa320Real(t, root, p) })
	t.Run("real refusals", pa320RealRefusals)
}

func pa320Manifest(t *testing.T) {
	root, p := pa320Fixture(t)
	legacy := p.Manifest
	legacy.Existing = nil
	if err := project.Validate(legacy, p.Lock); err == nil || !strings.Contains(err.Error(), "environment") {
		t.Fatalf("environment without existing sources: %v", err)
	}
	other := p.Manifest
	other.Language = "typescript"
	if err := project.Validate(other, p.Lock); err == nil {
		t.Fatal("environment accepted outside Python")
	}
	pa310Write(t, root, "notes.txt", "not a script\n")
	if err := os.Symlink(filepath.Join(root, "serve.py"), filepath.Join(root, "alias.py")); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"missing.py", "notes.txt", "alias.py", "../serve.py", "/serve.py", "tasks", ".venv/serve.py", `tasks\lifecycle.py`} {
		q := *p
		q.Manifest.Environment = bad
		if _, err := project.ResolvePythonApplication(&q); err == nil {
			t.Errorf("environment %q accepted", bad)
		}
	}
}

// The session admits inline evidence only for the script the manifest names,
// bound to that script as staged, and never in place of root requirements.
func pa320Evidence(t *testing.T) {
	files := []project.ComponentFile{{Path: "requirements.txt", SHA256: pa315Image}, {Path: "serve.py", SHA256: "sha256:" + strings.Repeat("b", 64)}}
	inline := func() *toolchain.PythonDependencies {
		return &toolchain.PythonDependencies{Image: pa315Image, Base: toolchain.PythonImage, Recipe: pa315Image, Platform: "linux/amd64",
			Script:          &toolchain.PythonInput{Path: "serve.py", SHA256: "sha256:" + strings.Repeat("c", 64)},
			Inputs:          []toolchain.PythonInput{{Path: "serve.py", SHA256: files[1].SHA256}},
			CanonicalInputs: []toolchain.PythonInput{{Path: "constraints.txt", SHA256: pa315Image}, {Path: "requirements.txt", SHA256: pa315Image}},
			Artifacts:       []toolchain.PythonArtifact{{Name: "packaging", Version: "24.2", Filename: "packaging-24.2-py3-none-any.whl", SHA256: pa315Image}}}
	}
	if err := pythonApplicationDependencies(files, "serve.py", pa315Image, inline()); err != nil {
		t.Fatalf("inline evidence refused: %v", err)
	}
	for name, change := range map[string]func(*toolchain.PythonDependencies){
		"no script":     func(d *toolchain.PythonDependencies) { d.Script = nil },
		"other script":  func(d *toolchain.PythonDependencies) { d.Script.Path = "main.py" },
		"script digest": func(d *toolchain.PythonDependencies) { d.Script.SHA256 = "c" },
		"stale input":   func(d *toolchain.PythonDependencies) { d.Inputs[0].SHA256 = pa315Image },
		"root inputs": func(d *toolchain.PythonDependencies) {
			d.Inputs = []toolchain.PythonInput{{Path: "requirements.txt", SHA256: pa315Image}}
		},
		"extra input": func(d *toolchain.PythonDependencies) {
			d.Inputs = append(d.Inputs, toolchain.PythonInput{Path: "requirements.txt", SHA256: pa315Image})
		},
		"canonical inputs": func(d *toolchain.PythonDependencies) { d.CanonicalInputs = d.CanonicalInputs[:1] },
		"wheel":            func(d *toolchain.PythonDependencies) { d.Artifacts[0].Filename = "other-24.2-py3-none-any.whl" },
	} {
		d := inline()
		change(d)
		if err := pythonApplicationDependencies(files, "serve.py", pa315Image, d); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := pythonApplicationDependencies(files, "serve.py", toolchain.PythonImage, nil); err == nil {
		t.Error("the standard-library base stood in for an environment script")
	}
	// Without an environment script, root requirements stay authoritative.
	if err := pythonApplicationDependencies(files, "", pa315Image, inline()); err == nil {
		t.Error("inline evidence stood in for root requirements")
	}
}

func pa320Real(t *testing.T, root string, p *project.Project) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	before := pa315Tree(t, root)
	s := pa315Prepare(t, p, PythonApplicationSessionOptions{})
	v := pa315Unit(t, s)
	r, err := s.Run(ctx, v.Key)
	if err != nil || r == nil || !r.Success || len(r.Tests) != 1 || r.Tests[0].Outcome != "passed" {
		t.Fatalf("the copied test could not import the inline dependency: %+v %v", r, err)
	}
	d := r.Provenance.Dependencies
	if d == nil || d.Script == nil || d.Script.Path != "serve.py" || !pyDepsDigest.MatchString(d.Script.SHA256) || len(d.Inputs) != 1 || d.Inputs[0].Path != "serve.py" || d.Inputs[0].SHA256 != before["serve.py"] || len(d.CanonicalInputs) != 2 {
		t.Fatalf("wrong inline dependency evidence: %+v", d)
	}
	if len(d.Artifacts) != 1 || d.Artifacts[0].Name != "packaging" || d.Artifacts[0].Version != "24.2" || d.Artifacts[0].Filename != "packaging-24.2-py3-none-any.whl" {
		t.Fatalf("wrong installed wheel: %+v", d.Artifacts)
	}
	if after := pa315Tree(t, root); len(after) != len(before) {
		t.Fatal("the application's files changed")
	} else {
		for name, hash := range before {
			if after[name] != hash {
				t.Fatalf("%s changed", name)
			}
		}
	}
	// Editing code outside the block keeps the environment and its image;
	// editing the block makes a new one.
	recipe, err := toolchain.PythonScriptDepsRecipe(root, "serve.py")
	if err != nil || recipe != d.Recipe {
		t.Fatalf("recipe %q %v, want %q", recipe, err, d.Recipe)
	}
	pa310Write(t, root, "serve.py", "#!/usr/bin/env python3\n"+pa320Block+"from tasks.lifecycle import advance\nprint(advance('running'))\n")
	edited := pa315Prepare(t, p, PythonApplicationSessionOptions{})
	if got := edited.Provenance().Dependencies; got == nil || got.Recipe != d.Recipe || got.Inputs[0].SHA256 == d.Inputs[0].SHA256 {
		t.Fatalf("a code edit changed the environment or kept the stale script hash: %+v", got)
	}
	pa310Write(t, root, "serve.py", strings.Replace(pa320Block, "# ///\n", "# [tool.ruff]\n# line-length = 100\n# ///\n", 1))
	if changed, err := toolchain.PythonScriptDepsRecipe(root, "serve.py"); err != nil || changed == d.Recipe {
		t.Fatalf("a block edit kept the recipe: %q %v", changed, err)
	}
}

func pa320RealRefusals(t *testing.T) {
	root := t.TempDir()
	for name, c := range map[string]struct{ script, says string }{
		"no block":      {"import json\n", "exactly one is required"},
		"uv settings":   {"# /// script\n# dependencies = [\"packaging==24.2\"]\n# [tool.uv]\n# exclude-newer = \"2024-01-01T00:00:00Z\"\n# ///\n", "[tool.uv]"},
		"python":        {"# /// script\n# requires-python = \">=3.14\"\n# dependencies = [\"packaging==24.2\"]\n# ///\n", "excludes the pinned Python"},
		"direct source": {"# /// script\n# dependencies = [\"packaging @ https://files.pythonhosted.org/packages/packaging-24.2-py3-none-any.whl\"]\n# ///\n", "inline dependencies of serve.py"},
		"pip option":    {"# /// script\n# dependencies = [\"packaging==24.2 --hash=sha256:00\"]\n# ///\n", "serve.py"},
	} {
		pa310Write(t, root, "serve.py", c.script)
		if _, err := toolchain.PythonScriptDepsRecipe(root, "serve.py"); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want an error saying %q", name, err, c.says)
		}
	}
}
