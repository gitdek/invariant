// Package formalize is the factory's formalization step. A coding agent
// reads an issue and drafts the formal statements people would ratify, with
// a draft model to check them against. When the issue leaves a real decision
// open, the agent asks instead of guessing: it lists the fork, and people
// answer it before a selected proposal is drafted (D-0002). Modeled options
// carry checked, unratified previews of their candidate behavior.
package formalize

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/regular"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// Fork is a question the factory won't answer for people: the issue allows
// materially different behaviors, and each option says what the system
// would do.
type Fork struct {
	ID       string   `json:"id"` // F1, F2, ...
	Kind     string   `json:"kind,omitempty"`
	Question string   `json:"question"`
	Options  []Option `json:"options"`
}

type Option struct {
	ID      string   `json:"id"` // A, B, ...
	Says    string   `json:"says"`
	Preview *Preview `json:"preview,omitempty"`
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

// Revision is a decided fork that a person's later comment changed.
type Revision struct {
	Fork   string `json:"fork"`
	Option string `json:"option"`
	By     string `json:"by"` // the login of the person whose comment asked for it
}

// Draft is what the formalizing agent writes to proposal.json: forks to ask,
// or a project to ratify. Its statements carry no pins; the factory pins
// them.
type Draft struct {
	Name       string              `json:"name"`    // the project, in a few plain words
	Slug       string              `json:"slug"`    // its directory name, in kebab case
	Module     string              `json:"module"`  // the TLA+ module's name
	Package    string              `json:"package"` // the code's package name
	Bounds     map[string]string   `json:"bounds"`
	Statements []project.Statement `json:"statements"`
	Forks      []Fork              `json:"forks,omitempty"`
	// Revised is each decided fork that a person's later comment changed,
	// with the option the draft follows instead. The factory records it as
	// decided only if it checks out (D-0094).
	Revised     []Revision `json:"revised,omitempty"`
	Unsupported string     `json:"unsupported,omitempty"` // why the factory can't take the issue
	// Language is the code's language: go, typescript or python. The factory
	// sets it from the issue's labels (D-0040); the agent doesn't choose it.
	Language string `json:"language,omitempty"`
	// Existing is the code the project checks without holding it, for an
	// issue that names code with Code: lines (D-0054). The factory sets it,
	// like the language.
	Existing []string `json:"existing,omitempty"`
}

// Proposal is a draft the factory has pinned: its statements carry the hash
// of their text, and Hash identifies the whole proposal.
type Proposal struct {
	Draft
	ModuleText string            `json:"module_text"` // the whole module: the statements and the draft model
	Hash       string            `json:"hash,omitempty"`
	View       string            `json:"view,omitempty"`
	Includes   map[string]string `json:"includes,omitempty"`
	Reader     *Reader           `json:"reader,omitempty"`
	// Baseline carries the last selected model through an intervening question.
	Baseline *statechart.ComparisonSnapshot `json:"comparison_baseline,omitempty"`
	// Target is the existing project an amendment changes (D-0045). Dir is
	// where a new project goes when its issue names one. Both are empty for
	// a new project placed the usual way.
	Target *Target `json:"target,omitempty"`
	Dir    string  `json:"dir,omitempty"`
	// Agent is the coding agent the issue picked with its Agent: line when
	// it was drafted, if it picked one (#173). The factory sets it, as it
	// sets Dir, and the build runs the agent it settles. A new project's
	// manifest records it, and an amendment keeps its project's manifest as
	// it is (D-0046).
	Agent string `json:"agent,omitempty"`
	// Plan is a plumbing change's proposal, in place of statements: what
	// changes, the files it may touch, and its acceptance tests (D-0105).
	Plan *plumbing.Plan `json:"plan,omitempty"`
	// IssuePlan is a PRD's proposal, in place of statements: the issues
	// that carry it out, in order (#112).
	IssuePlan *IssuePlan `json:"issue_plan,omitempty"`
}

// Target is the project an amendment changes, as it stood when the
// amendment was drafted.
type Target struct {
	Baseline *Current         `json:"baseline,omitempty"`
	Dir      string           `json:"dir"`
	Manifest project.Manifest `json:"manifest"`
	Amends   string           `json:"amends"`   // the ProposalHash of the lock it replaces
	Previous string           `json:"previous"` // where that lock was ratified: "#3", or "D-0027"
}

// Carrier is what an answered question passes on to the draft that follows
// it: the selected baseline and the captured target, with no draft to
// revise.
func (p *Proposal) Carrier() *Proposal {
	if p == nil || p.Baseline == nil && p.Target == nil {
		return nil
	}
	return &Proposal{Baseline: p.Baseline, Target: p.Target}
}

// drafted says whether the proposal holds a draft to revise, rather than
// only what an answered question carries on.
func (p *Proposal) drafted() bool {
	return p.ModuleText != "" || len(p.Forks) > 0 || p.Plan != nil || p.IssuePlan != nil || p.Unsupported != ""
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
	b, err := regular.ReadFile(ws, "proposal.json")
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
	text, err := regular.ReadFile(ws, d.Module+".tla")
	if err != nil {
		return nil, fmt.Errorf("proposal.json names module %s, but %s.tla can't be read: %w", d.Module, d.Module, err)
	}
	p.ModuleText = string(text)
	view, err := regular.ReadFile(ws, "view.json")
	if err != nil {
		return nil, fmt.Errorf("the modeled draft needs view.json: %w", err)
	}
	p.View = string(view)
	p.Includes, err = readIncludes(ws, d.Module)
	if err != nil {
		return nil, err
	}
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

// insideProject says whether a path a proposal carries stays inside its
// project once it's joined to the project's directory: relative, clean,
// and made of plain names.
func insideProject(name string) bool {
	if name == "" || name == "." || path.IsAbs(name) || path.Clean(name) != name || strings.Contains(name, `\`) || strings.ContainsFunc(name, unicode.IsControl) {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." || strings.HasPrefix(part, "-") {
			return false
		}
	}
	return true
}

// ValidateDestinations holds what a proposal says about where its files go
// to staying inside its project. A draft's names are checked as it's made,
// but a proposal is also read back from a comment or a saved record, and
// what's read there decides where ratification writes and, for an
// amendment, which model files it removes beside the module (#790). So
// Write checks it too, whoever calls it.
func (p *Proposal) ValidateDestinations() error {
	if p.Plan != nil || p.IssuePlan != nil {
		return nil
	}
	if !moduleRE.MatchString(p.Module) {
		return fmt.Errorf("module %q must be a CamelCase TLA+ module name", p.Module)
	}
	if p.Target != nil {
		if m := p.Target.Manifest.Module; !insideProject(m) || path.Ext(m) != ".tla" {
			return fmt.Errorf("the amended project's module %q must be a .tla file inside the project", m)
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
		if err != nil && !strings.Contains(err.Error(), "no MODULE header") {
			return fmt.Errorf("%s.tla: %w", p.Module, err)
		}
		return fmt.Errorf("%s.tla must begin with ---- MODULE %s ----", p.Module, p.Module)
	}
	if err := project.Validate(p.Manifest(), project.Lock{Bounds: p.Bounds, Statements: p.Statements}); err != nil {
		return err
	}
	// The model is read the way TLC reads it, with the modules beside it,
	// and one the gate couldn't pin is refused here, where its drafter can
	// fix it (#812).
	model, err := tla.Read(p.ModuleText, p.Includes)
	if err != nil {
		return fmt.Errorf("%s.tla: %w", p.Module, err)
	}
	for i, s := range p.Statements {
		h, err := model.PinHash(s.Name, project.Model)
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
// code can reach (D-0038, D-0039). An amendment's is its project's as it is,
// and a new project's names the agent its issue picked, if it picked one.
func (p *Proposal) Manifest() project.Manifest {
	if p.Target != nil {
		return p.Target.Manifest
	}
	m := project.Manifest{Name: p.Name, Module: ".invariant/specs/" + p.Module + ".tla", Code: p.Package, Language: "go", Agent: p.Agent}
	if len(p.Existing) > 0 {
		// The project holds a model and a driver, and checks the named code
		// where it is (D-0054).
		m.Language, m.Code, m.Conformance, m.Existing = "typescript", ".", "conformance.ts", p.Existing
		return m
	}
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
	if len(p.Forks) != 0 {
		return fmt.Errorf("alternatives are not a selected project")
	}
	if p.Reader != nil {
		if err := p.ValidateReader(); err != nil {
			return err
		}
	}
	if err := validateIncludes(p.Includes, p.Module); err != nil {
		return err
	}
	if err := p.ValidateDestinations(); err != nil {
		return err
	}
	m := p.Manifest()
	if ratified != nil && ratified.Proposal == "" {
		copyRatification := *ratified
		copyRatification.Proposal = project.ProposalHash(p.Bounds, p.Statements)
		ratified = &copyRatification
	}
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
	modelDir := filepath.Join(dir, filepath.Dir(m.Module))
	if p.Reader != nil {
		if err := removeOldIncludes(modelDir, p.Module, p.Includes); err != nil {
			return err
		}
	}
	if err := writeIncludes(modelDir, p.Includes); err != nil {
		return err
	}
	if err := writeReader(filepath.Join(dir, ".invariant"), p.View, p.Reader, true); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, ".invariant/request.md"), request)
}

// Check stages the draft in ws as a project and runs the gate's model
// checks on it: TLC, the witnesses and the known bugs.
func Check(ctx context.Context, ws string, tc toolchain.Toolchain) (*Proposal, *verify.Report, error) {
	if saved, ok := checkContexts.Load(ws); ok {
		return saved.(*CheckSession).Check(ctx, ws, tc)
	}
	return CheckWithContext(ctx, ws, tc, CheckContext{})
}

// CheckWithContext freshly validates caller-owned authority, never fields in
// proposal.json or a drafting session's memo.
func CheckWithContext(ctx context.Context, ws string, tc toolchain.Toolchain, authority CheckContext) (*Proposal, *verify.Report, error) {
	return checkWithContext(ctx, ws, tc, authority, nil)
}

func checkWithContext(ctx context.Context, ws string, tc toolchain.Toolchain, authority CheckContext, memo *baselineMemo) (*Proposal, *verify.Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := CheckStepDraft(ws, authority.ParentPlan); err != nil {
		return nil, nil, err
	}
	p, err := Read(ws)
	if err != nil {
		return nil, nil, err
	}
	if len(p.Forks) > 0 {
		p.Baseline = detachedSnapshot(authority.Baseline)
		return p, nil, p.checkPreviews(ctx, tc, authority)
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
	if err != nil || r == nil || !r.Passed {
		return p, r, err
	}
	err = p.computeReader(ctx, filepath.Join(dir, filepath.Dir(p.Manifest().Module)), tc, r.Design.DistinctStates)
	if err == nil {
		err = p.computeComparison(ctx, ws, tc, authority, memo)
	}
	if err == nil {
		err = ctx.Err()
	}
	return p, r, err
}

// Pinned is the part of the module a person ratifies: the statements and
// every definition they depend on, in module order.
func (p *Proposal) Pinned() (string, error) {
	model, err := tla.Read(p.ModuleText, p.Includes)
	if err != nil {
		return "", err
	}
	keep := map[string]bool{}
	for _, s := range p.Statements {
		names, err := model.Closure(s.Name, project.Model)
		if err != nil {
			return "", err
		}
		for _, n := range names {
			keep[n] = true
		}
	}
	var out []string
	for _, name := range model.Definitions() {
		if !keep[name] {
			continue
		}
		def, err := model.Definition(name)
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
