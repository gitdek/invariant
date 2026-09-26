// Package project reads an Invariant project: a manifest saying where things
// are, and a lock file recording what a person ratified.
package project

import (
	"crypto/sha256"
	"encoding/hex"
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
	// Conformance is the driver that runs the code and records its states in
	// the spec's vocabulary, relative to the project. TypeScript and Python
	// projects need one.
	Conformance string `json:"conformance,omitempty"`
	// Exhaustive says the driver explores every state the code can reach,
	// rather than sampling runs, so the gate requires it to visit every
	// state the model reaches. The factory's TypeScript and Python are
	// written this way (D-0038, D-0039).
	Exhaustive bool `json:"exhaustive,omitempty"`
}

// Lock records what a person ratified: the statements, pinned to their text,
// and the bounds TLC checks them within. The factory may not edit it;
// changing it is a decision.
type Lock struct {
	// Decision names the entry in decisions/log.md that ratified a
	// hand-built project. A factory project has Ratified instead.
	Decision   string            `json:"decision,omitempty"`
	Ratified   *Ratification     `json:"ratified,omitempty"`
	Bounds     map[string]string `json:"bounds"`
	Statements []Statement       `json:"statements"`
}

// Ratification records who ratified a factory project's statements, and
// where: a person with write access replied to the factory's proposal on its
// issue with /invariant ratify and the proposal's hash (D-0034).
type Ratification struct {
	By       string `json:"by"`       // the GitHub login that ratified
	At       string `json:"at"`       // when, in RFC 3339
	Issue    int    `json:"issue"`    // the issue the proposal answers
	Comment  string `json:"comment"`  // the ratifying comment's URL
	Proposal string `json:"proposal"` // the ProposalHash that was ratified
	// Amends is the ProposalHash of the lock this ratification replaced,
	// when it amended an existing project (D-0045), and Previous says
	// where that lock was ratified: "#3", or "D-0027" for a hand-built
	// project. Each lock names the one before it.
	Amends   string `json:"amends,omitempty"`
	Previous string `json:"previous,omitempty"`
}

// ProposalHash identifies what a person ratifies: the bounds, and every
// statement with its plain-language meaning and its pin. A ratifying comment
// names it, and anyone can recompute it from the lock.
func ProposalHash(bounds map[string]string, statements []Statement) string {
	b, _ := json.Marshal(struct {
		Bounds     map[string]string `json:"bounds"`
		Statements []Statement       `json:"statements"`
	}{bounds, statements})
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Validate checks a lock and manifest together, as Load does.
func Validate(m Manifest, l Lock) error {
	return (&Project{Manifest: m, Lock: l}).validate()
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
	names := map[string]bool{}
	for _, s := range l.Statements {
		if names[s.Name] {
			return fmt.Errorf("%s: statement %s appears twice", lockFile, s.Name)
		}
		names[s.Name] = true
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
