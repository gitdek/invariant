// Package formalize is the factory's formalization step. A coding agent
// reads an issue and drafts the formal statements people would ratify, with
// a draft model to check them against. When the issue leaves a real decision
// open, the agent asks instead of guessing: it lists the fork, and people
// answer it before anything is drafted (D-0002).
package formalize

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// Fork is a question the factory won't answer for people: the issue allows
// materially different behaviors, and each option says what the system
// would do.
type Fork struct {
	ID       string   `json:"id"` // F1, F2, ...
	Question string   `json:"question"`
	Options  []Option `json:"options"`
}

type Option struct {
	ID   string `json:"id"` // A, B, ...
	Says string `json:"says"`
}

// Option finds one of the fork's options by its id.
func (f Fork) Option(id string) (Option, bool) {
	for _, o := range f.Options {
		if strings.EqualFold(o.ID, id) {
			return o, true
		}
	}
	return Option{}, false
}

// Draft is what the formalizing agent writes to proposal.json: forks to ask,
// or a project to ratify. Its statements carry no pins; the factory pins
// them.
type Draft struct {
	Name        string              `json:"name"`    // the project, in a few plain words
	Slug        string              `json:"slug"`    // its directory name, in kebab case
	Module      string              `json:"module"`  // the TLA+ module's name
	Package     string              `json:"package"` // the code's package name
	Bounds      map[string]string   `json:"bounds"`
	Statements  []project.Statement `json:"statements"`
	Forks       []Fork              `json:"forks,omitempty"`
	Unsupported string              `json:"unsupported,omitempty"` // why the factory can't take the issue
	// Language is the code's language: go, typescript or python. The factory
	// sets it from the issue's labels (D-0040); the agent doesn't choose it.
	Language string `json:"language,omitempty"`
}

// Proposal is a draft the factory has pinned: its statements carry the hash
// of their text, and Hash identifies the whole proposal.
type Proposal struct {
	Draft
	ModuleText string `json:"module_text"` // the whole module: the statements and the draft model
	Hash       string `json:"hash,omitempty"`
}

// Ratifiable says whether the proposal is one a person can ratify: nothing
// is left to ask.
func (p *Proposal) Ratifiable() bool {
	return len(p.Forks) == 0 && p.Unsupported == "" && p.Hash != ""
}

var (
	slugRE    = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	moduleRE  = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	packageRE = regexp.MustCompile(`^[a-z][a-z0-9]*$`)
)

// Read loads the draft an agent left in its workspace, validates it, and
// pins its statements.
func Read(ws string) (*Proposal, error) {
	b, err := os.ReadFile(filepath.Join(ws, "proposal.json"))
	if err != nil {
		return nil, fmt.Errorf("the draft has no proposal.json: %w", err)
	}
	var d Draft
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("proposal.json: %w", err)
	}
	p := &Proposal{Draft: d}
	switch {
	case d.Unsupported != "":
		return p, nil
	case len(d.Forks) > 0:
		return p, validateForks(d.Forks)
	}
	if err := d.validateNames(); err != nil {
		return nil, err
	}
	text, err := os.ReadFile(filepath.Join(ws, d.Module+".tla"))
	if err != nil {
		return nil, fmt.Errorf("proposal.json names module %s, but %s.tla can't be read: %w", d.Module, d.Module, err)
	}
	p.ModuleText = string(text)
	return p, p.Pin()
}

func validateForks(forks []Fork) error {
	ids := map[string]bool{}
	for _, f := range forks {
		if f.ID == "" || f.Question == "" || ids[strings.ToUpper(f.ID)] {
			return fmt.Errorf("every fork needs a question and its own id: %+v", f)
		}
		ids[strings.ToUpper(f.ID)] = true
		if len(f.Options) < 2 {
			return fmt.Errorf("fork %s needs at least two options", f.ID)
		}
		opts := map[string]bool{}
		for _, o := range f.Options {
			if o.ID == "" || o.Says == "" || opts[strings.ToUpper(o.ID)] {
				return fmt.Errorf("fork %s: every option needs its own id and says what happens", f.ID)
			}
			opts[strings.ToUpper(o.ID)] = true
		}
	}
	return nil
}

func (d Draft) validateNames() error {
	switch {
	case strings.TrimSpace(d.Name) == "":
		return fmt.Errorf("proposal.json: name is empty")
	case !slugRE.MatchString(d.Slug):
		return fmt.Errorf("proposal.json: slug %q must be kebab case, like bounded-buffer", d.Slug)
	case !moduleRE.MatchString(d.Module):
		return fmt.Errorf("proposal.json: module %q must be a CamelCase TLA+ module name", d.Module)
	case !packageRE.MatchString(d.Package):
		return fmt.Errorf("proposal.json: package %q must be a short lowercase package name", d.Package)
	}
	return nil
}

// Pin validates the proposal's statements against its module, records each
// one's pin, and hashes the whole proposal.
func (p *Proposal) Pin() error {
	if name, err := tla.ModuleName(p.ModuleText); err != nil || name != p.Module {
		return fmt.Errorf("%s.tla must begin with ---- MODULE %s ----", p.Module, p.Module)
	}
	if err := project.Validate(p.Manifest(), project.Lock{Bounds: p.Bounds, Statements: p.Statements}); err != nil {
		return err
	}
	for i, s := range p.Statements {
		h, err := tla.PinHash(p.ModuleText, s.Name, project.Model)
		if err != nil {
			return fmt.Errorf("statement %s: %w", s.Name, err)
		}
		p.Statements[i].SHA256 = h
	}
	p.Hash = project.ProposalHash(p.Bounds, p.Statements)
	return nil
}

// Manifest is the project's manifest. TypeScript lives in src and Python in
// its package, each with a conformance driver that explores every state the
// code can reach (D-0038, D-0039).
func (p *Proposal) Manifest() project.Manifest {
	m := project.Manifest{Name: p.Name, Module: ".invariant/specs/" + p.Module + ".tla", Code: p.Package, Language: "go"}
	switch p.Language {
	case "typescript":
		m.Language, m.Code, m.Conformance, m.Exhaustive = "typescript", "src", "conformance.ts", true
	case "python":
		m.Language, m.Conformance, m.Exhaustive = "python", "conformance.py", true
	}
	return m
}

// Languages the factory writes, and how their code is checked.
var Languages = map[string]string{
	"go":         "in Go, proved with Gobra",
	"typescript": "in TypeScript, tested against the model in every state it can reach",
	"python":     "in Python, proved with Nagini",
}

// Write lays the proposal out as a project in dir: the manifest, the lock,
// the module and the request. ratified is nil for a draft.
func (p *Proposal) Write(dir, request string, ratified *project.Ratification) error {
	m := p.Manifest()
	lock := project.Lock{Ratified: ratified, Bounds: p.Bounds, Statements: p.Statements}
	for path, v := range map[string]any{".invariant/invariant.json": m, ".invariant/ratified.lock": lock} {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(dir, path), string(b)+"\n"); err != nil {
			return err
		}
	}
	if err := writeFile(filepath.Join(dir, m.Module), p.ModuleText); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, ".invariant/request.md"), request)
}

// Check stages the draft in ws as a project and runs the gate's model
// checks on it: TLC, the witnesses and the known bugs.
func Check(ctx context.Context, ws string, tc toolchain.Toolchain) (*Proposal, *verify.Report, error) {
	p, err := Read(ws)
	if err != nil {
		return nil, nil, err
	}
	if !p.Ratifiable() {
		return p, nil, nil
	}
	dir, err := os.MkdirTemp("", "invariant-draft-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	if err := p.Write(dir, "", nil); err != nil {
		return nil, nil, err
	}
	r, err := verify.RunModel(ctx, dir, tc)
	return p, r, err
}

// Pinned is the part of the module a person ratifies: the statements and
// every definition they depend on, in module order.
func (p *Proposal) Pinned() (string, error) {
	keep := map[string]bool{}
	for _, s := range p.Statements {
		names, err := tla.Closure(p.ModuleText, s.Name, project.Model)
		if err != nil {
			return "", err
		}
		for _, n := range names {
			keep[n] = true
		}
	}
	var out []string
	for _, name := range tla.Definitions(p.ModuleText) {
		if !keep[name] {
			continue
		}
		def, err := tla.Definition(p.ModuleText, name)
		if err != nil {
			return "", err
		}
		out = append(out, def)
	}
	return strings.Join(out, "\n\n") + "\n", nil
}

func writeFile(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o644)
}
