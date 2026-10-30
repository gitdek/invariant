package project

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"
)

// ComponentAssumption records a target's declared premise, not evidence.
type ComponentAssumption struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
	Reason    string `json:"reason"`
	Source    string `json:"source"`
}

// ComponentInvocation describes an intended invocation. Its text is never
// interpreted or executed by component resolution.
type ComponentInvocation struct {
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	Check      ComponentRef      `json:"check"`
	Command    string            `json:"command"`
	Args       []string          `json:"args,omitempty"`
	WorkingDir string            `json:"working_dir"`
	Env        map[string]string `json:"env,omitempty"`
	Sampling   map[string]string `json:"sampling,omitempty"`
	Inputs     []string          `json:"inputs,omitempty"`
}

type ResolvedComponentAssumption struct {
	ID        string        `json:"id"`
	Statement string        `json:"statement"`
	Reason    string        `json:"reason"`
	Source    ComponentFile `json:"source"`
}

type ResolvedComponentInvocation struct {
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	Check      string            `json:"check"`
	Command    string            `json:"command"`
	Args       []string          `json:"args,omitempty"`
	WorkingDir string            `json:"working_dir"`
	Env        map[string]string `json:"env,omitempty"`
	Sampling   map[string]string `json:"sampling,omitempty"`
	Inputs     []ComponentFile   `json:"inputs,omitempty"`
}

// ComponentProvenance retains selected declarations and input snapshots apart
// from the production inventory. Modules are the nearest go.mod files used to
// resolve identities; files are selected sources and explicitly declared inputs.
type ComponentProvenance struct {
	Declarations []Component     `json:"declarations,omitempty"`
	Files        []ComponentFile `json:"files,omitempty"`
	Modules      []ComponentFile `json:"modules,omitempty"`
}

type componentInput struct {
	file  ComponentFile
	bytes []byte
}

func (r *componentResolver) input(name string) (componentInput, error) {
	if input, ok := r.inputs[name]; ok {
		return input, nil
	}
	full, err := componentPath(r.root, name, false)
	if err != nil {
		return componentInput{}, err
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return componentInput{}, err
	}
	input := componentInput{file: ComponentFile{Path: name, SHA256: componentHash(b)}, bytes: b}
	r.inputs[name] = input
	return input, nil
}

func (r *componentResolver) declaredInput(name string) (ComponentFile, error) {
	input, err := r.input(name)
	if err != nil {
		return ComponentFile{}, err
	}
	r.provenanceFiles[name] = input.file
	return input.file, nil
}

func (r *componentResolver) assumptions(declarations []ComponentAssumption) ([]ResolvedComponentAssumption, error) {
	var resolved []ResolvedComponentAssumption
	seen := map[string]bool{}
	for _, a := range declarations {
		if strings.TrimSpace(a.ID) == "" || strings.TrimSpace(a.Statement) == "" || strings.TrimSpace(a.Reason) == "" {
			return nil, fmt.Errorf("assumption requires nonblank id, statement and reason")
		}
		if seen[a.ID] {
			return nil, fmt.Errorf("duplicate assumption %q", a.ID)
		}
		seen[a.ID] = true
		source, err := r.declaredInput(a.Source)
		if err != nil {
			return nil, fmt.Errorf("assumption %q source %q: %w", a.ID, a.Source, err)
		}
		resolved = append(resolved, ResolvedComponentAssumption{ID: a.ID, Statement: a.Statement, Reason: a.Reason, Source: source})
	}
	sort.Slice(resolved, func(i, j int) bool { return resolved[i].ID < resolved[j].ID })
	return resolved, nil
}

func (r *componentResolver) invocations(declarations []ComponentInvocation, check ResolvedComponentCheck) ([]ResolvedComponentInvocation, error) {
	var resolved []ResolvedComponentInvocation
	seen := map[[3]string]bool{}
	for _, v := range declarations {
		if strings.TrimSpace(v.ID) == "" || strings.TrimSpace(v.Command) == "" {
			return nil, fmt.Errorf("invocation requires nonblank id and command")
		}
		var selections []string
		kind := v.Kind
		switch kind {
		case "proof":
			kind = "target"
			if check.Proof != nil {
				selections = []string{check.Target}
			}
		case "test":
			selections = check.Tests
		case "conformance":
			selections = check.Conformance
		default:
			return nil, fmt.Errorf("invocation %q: unknown kind %q", v.ID, v.Kind)
		}
		c, err := r.resolve(v.Check, kind)
		if err != nil {
			return nil, fmt.Errorf("invocation %q: %w", v.ID, err)
		}
		if !slices.Contains(selections, c.ID) {
			return nil, fmt.Errorf("invocation %q: %s is not a selected %s check", v.ID, c.ID, v.Kind)
		}
		key := [3]string{v.Kind, c.ID, v.ID}
		if seen[key] {
			return nil, fmt.Errorf("duplicate %s invocation %q for %s", v.Kind, v.ID, c.ID)
		}
		seen[key] = true
		if _, err := componentPath(r.root, v.WorkingDir, true); err != nil {
			return nil, fmt.Errorf("invocation %q working directory: %w", v.ID, err)
		}
		for _, field := range []struct {
			name   string
			values map[string]string
		}{{"env", v.Env}, {"sampling", v.Sampling}} {
			for name := range field.values {
				if strings.TrimSpace(name) == "" {
					return nil, fmt.Errorf("invocation %q: %s keys must be nonblank", v.ID, field.name)
				}
			}
		}
		out := ResolvedComponentInvocation{
			ID: v.ID, Kind: v.Kind, Check: c.ID, Command: v.Command,
			Args: slices.Clone(v.Args), WorkingDir: v.WorkingDir,
			Env: maps.Clone(v.Env), Sampling: maps.Clone(v.Sampling),
		}
		inputs := map[string]bool{}
		for _, name := range v.Inputs {
			if inputs[name] {
				return nil, fmt.Errorf("invocation %q: duplicate input %q", v.ID, name)
			}
			inputs[name] = true
			input, err := r.declaredInput(name)
			if err != nil {
				return nil, fmt.Errorf("invocation %q input %q: %w", v.ID, name, err)
			}
			out.Inputs = append(out.Inputs, input)
		}
		sort.Slice(out.Inputs, func(i, j int) bool { return out.Inputs[i].Path < out.Inputs[j].Path })
		resolved = append(resolved, out)
	}
	sort.Slice(resolved, func(i, j int) bool {
		a, b := resolved[i], resolved[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Check != b.Check {
			return a.Check < b.Check
		}
		return a.ID < b.ID
	})
	return resolved, nil
}

func (r *componentResolver) provenance() *ComponentProvenance {
	p := &ComponentProvenance{}
	for _, c := range r.declarations {
		p.Declarations = append(p.Declarations, c)
	}
	for _, f := range r.provenanceFiles {
		p.Files = append(p.Files, f)
	}
	for _, f := range r.modules {
		p.Modules = append(p.Modules, f)
	}
	sort.Slice(p.Declarations, func(i, j int) bool { return p.Declarations[i].ID < p.Declarations[j].ID })
	sort.Slice(p.Files, func(i, j int) bool { return p.Files[i].Path < p.Files[j].Path })
	sort.Slice(p.Modules, func(i, j int) bool { return p.Modules[i].Path < p.Modules[j].Path })
	return p
}
