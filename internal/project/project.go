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
	Name          string `json:"name"`
	Module        string `json:"module"`        // the TLA+ module, relative to the project
	Specification string `json:"specification"` // the operator TLC checks, such as "Spec"
	Code          string `json:"code"`          // the Go package, relative to the project
}

// Lock records what a person ratified: the statements and their pinned text,
// the bounds TLC checks them within, and the known bugs they must catch. The
// factory may not edit it; changing it is a decision.
type Lock struct {
	Decision   string            `json:"decision"`
	Bounds     map[string]string `json:"bounds"`
	Statements []Statement       `json:"statements"`
	Mutants    []Mutant          `json:"mutants"`
}

// Statement kinds.
const (
	Invariant = "invariant" // must hold in every reachable state
	Witness   = "witness"   // must hold in at least one reachable state
)

// Statement is one ratified formal statement.
type Statement struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Says   string `json:"says"`   // the plain-language meaning a person ratified
	SHA256 string `json:"sha256"` // the pin: tla.Hash of the definition's text
}

// Mutant is a known bug, written as a one-line change to the model, that the
// invariants must catch.
type Mutant struct {
	Name    string `json:"name"`
	Says    string `json:"says"`
	Replace string `json:"replace"`
	With    string `json:"with"`
	Expect  string `json:"expect"` // the invariant that must be violated
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
	if m.Module == "" || m.Specification == "" || m.Code == "" {
		return fmt.Errorf("%s: module, specification and code are required", manifestFile)
	}
	if len(l.Bounds) == 0 {
		return fmt.Errorf("%s: no bounds; TLC needs finite bounds to check", lockFile)
	}
	invariants := map[string]bool{}
	for _, s := range l.Statements {
		switch s.Kind {
		case Invariant:
			invariants[s.Name] = true
		case Witness:
		default:
			return fmt.Errorf("%s: statement %s has kind %q; want %q or %q", lockFile, s.Name, s.Kind, Invariant, Witness)
		}
	}
	if len(invariants) == 0 {
		return fmt.Errorf("%s: no invariants", lockFile)
	}
	for _, m := range l.Mutants {
		if !invariants[m.Expect] {
			return fmt.Errorf("%s: mutant %s expects %s, which isn't a ratified invariant", lockFile, m.Name, m.Expect)
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

// CodeDir is the Go package's directory.
func (p *Project) CodeDir() string { return filepath.Join(p.Dir, p.Manifest.Code) }

// Invariants names the ratified invariants.
func (p *Project) Invariants() []string {
	var names []string
	for _, s := range p.Lock.Statements {
		if s.Kind == Invariant {
			names = append(names, s.Name)
		}
	}
	return names
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
		def, err := tla.Definition(string(src), s.Name)
		if err != nil {
			return nil, err
		}
		if h := tla.Hash(def); h != s.SHA256 {
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
