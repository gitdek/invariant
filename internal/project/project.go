// Package project reads an Invariant project: a manifest saying where things
// are, and a lock file recording what a person ratified.
package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gitdek/invariant/internal/tla"
)

// Manifest says where a project's parts live. The factory may edit it.
type Manifest struct {
	Name     string `json:"name"`
	Module   string `json:"module"`   // the TLA+ module, relative to the project
	Code     string `json:"code"`     // the implementation's package, relative to the project
	Language string `json:"language"` // "go", "typescript" or "python"
	// Conformance is the driver that runs the code at random and records its
	// states in the spec's vocabulary, relative to the project. TypeScript
	// and Python projects need one.
	Conformance string `json:"conformance,omitempty"`
}

// Lock records what a person ratified: the statements, pinned to their text,
// and the bounds TLC checks them within. The factory may not edit it;
// changing it is a decision.
type Lock struct {
	Decision   string            `json:"decision"`
	Bounds     map[string]string `json:"bounds"`
	Statements []Statement       `json:"statements"`
}

// Statement kinds.
const (
	Spec      = "spec"      // the behaviors TLC explores: Init /\ [][Next]_vars
	Invariant = "invariant" // must hold in every reachable state
	Witness   = "witness"   // must hold in at least one reachable state
	Bug       = "bug"       // an action the invariants must catch when added to Next
)

// Model names the definitions that belong to the factory. Pins stop at
// them, so the factory can write the model without touching a pin.
var Model = map[string]bool{"Init": true, "Next": true}

// Statement is one ratified formal statement.
type Statement struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Says   string `json:"says"`             // the plain-language meaning a person ratified
	Expect string `json:"expect,omitempty"` // for a bug: the invariant it must violate
	SHA256 string `json:"sha256"`           // the pin: tla.PinHash of the statement
}

// Project is a loaded project.
type Project struct {
	Dir      string
	Manifest Manifest
	Lock     Lock
}

const (
	manifestFile = ".invariant/invariant.json"
	lockFile     = ".invariant/ratified.lock"
	requestFile  = ".invariant/request.md"
)

// Load reads and validates the project in dir.
func Load(dir string) (*Project, error) {
	p := &Project{Dir: dir}
	if err := readJSON(filepath.Join(dir, manifestFile), &p.Manifest); err != nil {
		return nil, err
	}
	if err := readJSON(filepath.Join(dir, lockFile), &p.Lock); err != nil {
		return nil, err
	}
	return p, p.validate()
}

func (p *Project) validate() error {
	m, l := p.Manifest, p.Lock
	if m.Module == "" || m.Code == "" {
		return fmt.Errorf("%s: module and code are required", manifestFile)
	}
	if len(l.Bounds) == 0 {
		return fmt.Errorf("%s: no bounds; TLC needs finite bounds to check", lockFile)
	}
	kinds := map[string]int{}
	invariants := map[string]bool{}
	for _, s := range l.Statements {
		switch s.Kind {
		case Spec, Witness, Bug:
		case Invariant:
			invariants[s.Name] = true
		default:
			return fmt.Errorf("%s: statement %s has kind %q", lockFile, s.Name, s.Kind)
		}
		if Model[s.Name] {
			return fmt.Errorf("%s: %s belongs to the factory's model and can't be a statement", lockFile, s.Name)
		}
		kinds[s.Kind]++
	}
	if kinds[Spec] != 1 {
		return fmt.Errorf("%s: want exactly one statement of kind %q, found %d", lockFile, Spec, kinds[Spec])
	}
	if len(invariants) == 0 {
		return fmt.Errorf("%s: no invariants", lockFile)
	}
	for _, s := range l.Statements {
		if s.Kind == Bug && !invariants[s.Expect] {
			return fmt.Errorf("%s: bug %s expects %q, which isn't a ratified invariant", lockFile, s.Name, s.Expect)
		}
	}
	return nil
}

// ModulePath is the TLA+ module's path.
func (p *Project) ModulePath() string { return filepath.Join(p.Dir, p.Manifest.Module) }

// ModuleName is the module's name, taken from its file name.
func (p *Project) ModuleName() string {
	return strings.TrimSuffix(filepath.Base(p.Manifest.Module), ".tla")
}

// CodeDir is the implementation's package directory.
func (p *Project) CodeDir() string { return filepath.Join(p.Dir, p.Manifest.Code) }

// Request is the change the project's implementation should make, as an
// issue would state it.
func (p *Project) Request() (string, error) {
	b, err := os.ReadFile(filepath.Join(p.Dir, requestFile))
	return string(b), err
}

// Of returns the statements of one kind, in lock order.
func (p *Project) Of(kind string) []Statement {
	var out []Statement
	for _, s := range p.Lock.Statements {
		if s.Kind == kind {
			out = append(out, s)
		}
	}
	return out
}

// SpecName names the ratified spec.
func (p *Project) SpecName() string { return p.Of(Spec)[0].Name }

// Invariants names the ratified invariants.
func (p *Project) Invariants() []string {
	var names []string
	for _, s := range p.Of(Invariant) {
		names = append(names, s.Name)
	}
	return names
}

// Pinned names every definition a pin covers: each statement and everything
// it depends on, up to the factory's model.
func (p *Project) Pinned(src string) (map[string]bool, error) {
	pinned := map[string]bool{}
	for _, s := range p.Lock.Statements {
		names, err := tla.Closure(src, s.Name, Model)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			pinned[n] = true
		}
	}
	return pinned, nil
}

// Pin records the current text of every ratified statement in the lock file.
// Only a person runs this, when they ratify; it returns what changed.
func (p *Project) Pin() ([]string, error) {
	src, err := os.ReadFile(p.ModulePath())
	if err != nil {
		return nil, err
	}
	var changed []string
	for i, s := range p.Lock.Statements {
		h, err := tla.PinHash(string(src), s.Name, Model)
		if err != nil {
			return nil, err
		}
		if h != s.SHA256 {
			p.Lock.Statements[i].SHA256 = h
			changed = append(changed, s.Name)
		}
	}
	b, err := json.MarshalIndent(p.Lock, "", "  ")
	if err != nil {
		return nil, err
	}
	return changed, os.WriteFile(filepath.Join(p.Dir, lockFile), append(b, '\n'), 0o644)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
