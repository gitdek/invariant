package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

// PythonApplicationWorkspace is a separate copy of an application's inputs.
// Callers own Root and remove it when finished. No application command runs.
type PythonApplicationWorkspace struct {
	Root      string
	Project   *project.Project
	Inventory *project.ComponentInventory
	Files     []project.ComponentFile
}

// StagePythonApplication preserves application bytes and layout, including the
// verification project and unselected siblings, without enlarging production
// scope. A failed stage returns no workspace; dst may hold a partial copy.
func StagePythonApplication(p *project.Project, dst string) (*PythonApplicationWorkspace, error) {
	if err := project.Validate(p.Manifest, p.Lock); err != nil {
		return nil, err
	}
	app, err := project.ResolvePythonApplication(p)
	if err != nil {
		return nil, err
	}
	if _, err := project.ResolveComponents(p); err != nil {
		return nil, err
	}
	dst, err = filepath.Abs(dst)
	if err != nil {
		return nil, err
	}
	if err := pythonApplicationDestination(app.Root, dst); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return nil, err
	}
	err = filepath.WalkDir(app.Root, func(from string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(app.Root, from)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".venv" || entry.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			return os.MkdirAll(to, 0o755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("Python application %q is not a regular file", filepath.ToSlash(rel))
		}
		b, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		_, writeErr := f.Write(b)
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	})
	if err != nil {
		return nil, err
	}
	staged, err := project.Load(filepath.Join(dst, filepath.FromSlash(app.ProjectPath)))
	if err != nil {
		return nil, err
	}
	context, err := project.ResolvePythonApplication(staged)
	if err != nil {
		return nil, err
	}
	if context.Root != dst {
		return nil, fmt.Errorf("staged Python application resolved outside its workspace")
	}
	inv, err := project.ResolveComponents(staged)
	if err != nil {
		return nil, err
	}
	w := &PythonApplicationWorkspace{Root: dst, Project: staged, Inventory: inv}
	// Hash the copied bytes, not the original tree. Provenance includes every
	// staged file while the inventory's production files retain explicit scope.
	err = filepath.WalkDir(dst, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("staged Python application contains a nonregular file: %s", name)
		}
		b, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dst, name)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(b)
		w.Files = append(w.Files, project.ComponentFile{Path: filepath.ToSlash(rel), SHA256: "sha256:" + hex.EncodeToString(hash[:])})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(w.Files, func(i, j int) bool { return w.Files[i].Path < w.Files[j].Path })
	if inv.Mode == "explicit" {
		inv.Provenance.Files = append([]project.ComponentFile(nil), w.Files...)
	}
	return w, nil
}

func pythonApplicationDestination(root, dst string) error {
	// Inspect all existing destination components before creating anything,
	// up to the platform's temporary directory: it and its ancestors belong to
	// the platform, and some are links, such as macOS's /var to /private/var.
	tmp, err := filepath.Abs(os.TempDir())
	if err != nil {
		return err
	}
	existing := ""
	for dir := dst; ; dir = filepath.Dir(dir) {
		if dir == tmp || strings.HasPrefix(tmp, dir+string(filepath.Separator)) {
			if existing == "" {
				existing = dir
			}
			break
		}
		info, err := os.Lstat(dir)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && !info.IsDir() {
			return fmt.Errorf("Python workspace path must contain only directories: %s", dir)
		}
		if err == nil && existing == "" {
			existing = dir
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	// Compare real paths as well, so a platform alias cannot conceal overlap.
	realExisting, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return err
	}
	rest, err := filepath.Rel(existing, dst)
	if err != nil {
		return err
	}
	realDst := filepath.Join(realExisting, rest)
	// Canonicalize the application too, so a host alias cannot conceal overlap.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	for _, pair := range [][2]string{{root, dst}, {dst, root}, {realRoot, dst}, {dst, realRoot}, {realRoot, realDst}, {realDst, realRoot}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err != nil {
			return err
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("Python workspace must be disjoint from the application")
		}
	}
	entries, err := os.ReadDir(dst)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("Python workspace destination must be missing or empty")
	}
	return nil
}

// SelectImage admits dependencies from the staged root using the existing
// installer and its evidence: from the environment script's inline metadata
// when the manifest names one (#320), else from root requirements. It never
// invokes application code.
func (w *PythonApplicationWorkspace) SelectImage(ctx context.Context) (string, *toolchain.PythonDependencies, error) {
	var deps *toolchain.PythonDependencies
	var err error
	if env := w.Project.Manifest.Environment; env != "" {
		deps, err = toolchain.PythonScriptDepsImage(ctx, w.Root, env)
	} else {
		deps, err = toolchain.PythonDepsImage(ctx, w.Root)
	}
	if err != nil {
		return "", nil, err
	}
	if deps == nil {
		return toolchain.PythonImage, nil, nil
	}
	return deps.Image, deps, nil
}
