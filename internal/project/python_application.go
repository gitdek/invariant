package project

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// PythonApplication locates existing Python sources without importing them.
// ProjectPath is the verification directory relative to Root, using slashes.
type PythonApplication struct {
	Root        string
	ProjectPath string
}

// ResolvePythonApplication requires exactly one strict ancestor at which every
// existing selection names Python source. Dependency files do not choose roots.
func ResolvePythonApplication(p *Project) (*PythonApplication, error) {
	if p.Manifest.Language != "python" || len(p.Manifest.Existing) == 0 {
		return nil, fmt.Errorf("Python application requires language python and existing sources")
	}
	for _, name := range p.Manifest.Existing {
		if err := pythonApplicationPath(name); err != nil {
			return nil, err
		}
		if name == "." {
			return nil, fmt.Errorf("Python existing selection must not be '.'")
		}
	}
	dir, err := filepath.Abs(p.Dir)
	if err != nil {
		return nil, err
	}
	var app *PythonApplication
	for root := filepath.Dir(dir); root != dir; root = filepath.Dir(root) {
		matches := true
		for _, name := range p.Manifest.Existing {
			if err := pythonApplicationSelection(root, name); err != nil {
				matches = false
				break
			}
		}
		if matches {
			if app != nil {
				return nil, fmt.Errorf("Python application has ambiguous roots: %s and %s", app.Root, root)
			}
			rel, err := filepath.Rel(root, dir)
			if err != nil {
				return nil, err
			}
			app = &PythonApplication{Root: root, ProjectPath: filepath.ToSlash(rel)}
		}
		if filepath.Dir(root) == root {
			break
		}
	}
	if app == nil {
		return nil, fmt.Errorf("Python application: no enclosing root contains every existing selection")
	}
	if err := pythonApplicationPath(app.ProjectPath); err != nil {
		return nil, err
	}
	if _, err := componentPath(app.Root, app.ProjectPath, true); err != nil {
		return nil, fmt.Errorf("Python verification directory: %w", err)
	}
	if env := p.Manifest.Environment; env != "" {
		if err := pythonApplicationPath(env); err != nil {
			return nil, err
		}
		if !strings.HasSuffix(env, ".py") {
			return nil, fmt.Errorf("Python environment %q must name a .py script", env)
		}
		if _, err := componentPath(app.Root, env, false); err != nil {
			return nil, fmt.Errorf("Python environment script %q: %w", env, err)
		}
	}
	for _, field := range []struct {
		name      string
		directory bool
	}{{p.Manifest.Module, false}, {p.Manifest.Conformance, false}, {p.Manifest.Code, true}} {
		if err := pythonApplicationPath(field.name); err != nil {
			return nil, err
		}
		if _, err := componentPath(dir, field.name, field.directory); err != nil {
			return nil, fmt.Errorf("Python project path %q: %w", field.name, err)
		}
	}
	// The ordinary resolver validates existence, kinds and declarations. Check
	// exclusions here too, so a declared input cannot disappear during staging.
	if cfg := p.Manifest.Components; cfg != nil {
		names := append([]string(nil), cfg.Scope...)
		for _, check := range cfg.Checks {
			names = append(names, check.Target.Source)
			refs := append([]ComponentRef(nil), check.Tests...)
			refs = append(refs, check.Conformance...)
			if check.Proof != nil {
				refs = append(refs, check.Proof.Contracts...)
			}
			for _, ref := range refs {
				names = append(names, ref.Source)
			}
			for _, a := range check.Assumptions {
				names = append(names, a.Source)
			}
			for _, v := range check.Invocations {
				names = append(names, v.Check.Source, v.WorkingDir)
				names = append(names, v.Inputs...)
			}
		}
		for _, c := range cfg.TrustedContracts {
			names = append(names, c.Contract.Source)
			if c.Operation.Kind == OperationApplication {
				names = append(names, c.Operation.Source)
			}
			for _, ref := range c.Tests {
				names = append(names, ref.Source)
			}
			for _, v := range c.Invocations {
				names = append(names, v.Check.Source, v.WorkingDir)
				names = append(names, v.Inputs...)
			}
		}
		for _, name := range names {
			if err := pythonApplicationPath(name); err != nil {
				return nil, err
			}
		}
	}
	return app, nil
}

func pythonApplicationPath(name string) error {
	if name == "" || path.Clean(name) != name || path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, `\`) {
		return fmt.Errorf("Python application path %q must be a clean contained slash path", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".git" || part == ".venv" || part == "__pycache__" {
			return fmt.Errorf("Python application path %q selects an excluded directory", name)
		}
	}
	return nil
}

func pythonApplicationSelection(root, name string) error {
	// Check the parents before inspecting the leaf; Lstat alone would follow
	// links in the parent path.
	if _, err := componentPath(root, path.Dir(name), true); err != nil {
		return err
	}
	full := filepath.Join(root, filepath.FromSlash(name))
	info, err := os.Lstat(full)
	if err != nil {
		return err
	}
	if info.Mode().IsRegular() && strings.HasSuffix(name, ".py") {
		return nil
	}
	if !info.IsDir() {
		return fmt.Errorf("%q must name a regular Python source or directory", name)
	}
	found := false
	err = filepath.WalkDir(full, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".venv" || entry.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("Python selection %q contains a nonregular file: %s", name, file)
		}
		if strings.HasSuffix(entry.Name(), ".py") {
			found = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("Python selection %q contains no Python sources", name)
	}
	return nil
}
