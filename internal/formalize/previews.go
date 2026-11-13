package formalize

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// Preview keeps the complete isolated input and the check's derived output.
// Fingerprint binds this record to its containing question and option words.
type Preview struct {
	Fork        string            `json:"fork"`
	Option      string            `json:"option"`
	Choices     map[string]string `json:"choices,omitempty"`
	Model       *Proposal         `json:"model"`
	Receipt     *verify.Report    `json:"receipt,omitempty"`
	Problem     string            `json:"problem,omitempty"`
	Fingerprint string            `json:"fingerprint,omitempty"`
}

// CheckContext is supplied by the caller before drafting. Revisions lists
// only changes that pass the factory's existing writer/comment/option checks.
// The agent can select one of these through revised, but cannot authorize one.
type CheckContext struct {
	Baseline         *statechart.ComparisonSnapshot `json:"baseline,omitempty"`
	BaselineViewPath string                         `json:"baseline_view_path,omitempty"`
	ParentPlan       int                            `json:"parent_plan,omitempty"`
	Planning         string                         `json:"planning,omitempty"`
	Choices          map[string]string              `json:"choices,omitempty"`
	Revisions        []Revision                     `json:"revisions,omitempty"`
	// Amended is the statements of the lock an amendment replaces, from
	// the project as the caller captured it. A draft may keep one of its
	// unclassified bugs as it is (#857). It travels with the rest of the
	// context, so the check the drafting agent runs holds a draft to what
	// the caller's own check will.
	Amended []project.Statement `json:"amended,omitempty"`
	// Language is the language the draft's project is written in: the
	// request's, TypeScript for code checked as it is, or the language of
	// the project an amendment changes. The model doesn't depend on it. A
	// current bug's replay does: the gate requires one only where a driver
	// checks the code, so the check has to know (#910).
	Language string `json:"language,omitempty"`
}

var checkContexts sync.Map

func (r Request) CheckContext() CheckContext {
	c := CheckContext{ParentPlan: r.ParentPlan, Choices: map[string]string{}, Revisions: slices.Clone(r.RevisionChoices)}
	if r.Plumbing != "" {
		c.Planning = "plumbing"
	}
	if r.PRD != "" {
		c.Planning = "prd"
	}
	for _, a := range r.Answers {
		c.Choices[a.Fork] = a.Option
	}
	c.Baseline = r.comparisonBaseline()
	c.Language = r.Language
	if r.Existing != nil {
		c.Language = "typescript"
	}
	if current := r.CapturedCurrent(); current != nil {
		c.Language = current.Manifest.Language
		c.Amended = slices.Clone(current.Lock.Statements)
		if current.View != "" {
			c.BaselineViewPath = filepath.ToSlash(filepath.Join(current.Dir, ".invariant/view.json"))
		}
	}
	if c.Baseline != nil {
		// Only sources are authority. Derived evidence stays session-local.
		c.Baseline.Chart, c.Baseline.Limitation = nil, nil
	}
	return c
}

func stageCheckContext(ws string, c CheckContext) (string, func(), error) {
	session := NewCheckSession(c)
	c = session.authority
	b, err := json.Marshal(c)
	if err != nil {
		return "", nil, err
	}
	if err := writeFile(filepath.Join(ws, "check-context.json"), string(b)+"\n"); err != nil {
		return "", nil, err
	}
	// Direct Check calls retain a detached copy. Modeled MCP checks fetch
	// these captured bytes from the caller, never from this guidance file.
	// The independent final check retains its original caller-memory context.
	checkContexts.Store(ws, session)
	if err := stageBaseline(ws, c.Baseline); err != nil {
		checkContexts.Delete(ws)
		return "", nil, err
	}
	return string(b), func() { checkContexts.Delete(ws) }, nil
}

func stagePreviousPreviews(ws string, p *Proposal) error {
	if !p.HasPreviews() {
		return nil
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(ws, "previous", "proposal.json"), string(b)+"\n")
}

func (c CheckContext) effective(revised []Revision) (map[string]string, error) {
	choices := maps.Clone(c.Choices)
	for _, r := range revised {
		id := ""
		for fork := range choices {
			if strings.EqualFold(fork, r.Fork) {
				id = fork
			}
		}
		allowed := slices.ContainsFunc(c.Revisions, func(a Revision) bool {
			return strings.EqualFold(a.Fork, r.Fork) && strings.EqualFold(a.Option, r.Option) && strings.EqualFold(a.By, r.By)
		})
		if id == "" || !allowed {
			return nil, fmt.Errorf("revision of %s to %s by %s has no caller authorization", r.Fork, r.Option, r.By)
		}
		for _, a := range c.Revisions {
			if strings.EqualFold(a.Fork, r.Fork) && strings.EqualFold(a.Option, r.Option) && strings.EqualFold(a.By, r.By) {
				choices[id] = a.Option
				break
			}
		}
	}
	return choices, nil
}

// Step-fit is deliberately narrow. A label alone never exempts a behavior
// choice, nor does a parent number supplied by the agent.
func (f Fork) ValidateStepFit(parent int) error {
	if f.Kind != "step_fit" || parent <= 0 || len(f.Options) != 2 {
		return fmt.Errorf("fork %s: step_fit requires an authenticated parent and the two build-or-replan options", f.ID)
	}
	if !regexp.MustCompile(fmt.Sprintf(`#%d\b`, parent)).MatchString(f.Question) {
		return fmt.Errorf("fork %s: step_fit must identify parent #%d", f.ID, parent)
	}
	build, replan := false, false
	for _, o := range f.Options {
		if o.Preview != nil {
			return fmt.Errorf("fork %s: step_fit stays chart-free", f.ID)
		}
		if o.Says == "Build the complete step as one change." {
			build = true
			continue
		}
		replan = regexp.MustCompile(fmt.Sprintf(`^A writer stops parent #%d with /invariant stop and requests a replacement plan on a new (top-level )?issue with /invariant plan([.,].*)?$`, parent)).MatchString(o.Says)
	}
	if !build || !replan {
		return fmt.Errorf("fork %s: step_fit only asks whether to build the complete step as one change or stop parent #%d and request a replacement plan on a new issue", f.ID, parent)
	}
	return nil
}

func (p *Proposal) HasPreviews() bool {
	if p == nil {
		return false
	}
	for _, f := range p.Forks {
		for _, o := range f.Options {
			if o.Preview != nil {
				return true
			}
		}
	}
	return false
}

var previewID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

func previewInput(f Fork, o Option, choices map[string]string) error {
	v := o.Preview
	if !previewID.MatchString(f.ID) || !previewID.MatchString(o.ID) {
		return fmt.Errorf("preview IDs must be single alphanumeric names, with optional underscores or hyphens")
	}
	if v == nil || v.Model == nil {
		return fmt.Errorf("missing preview model; check every modeled option")
	}
	if v.Fork != f.ID || v.Option != o.ID {
		return fmt.Errorf("preview identity does not match %s/%s", f.ID, o.ID)
	}
	if !maps.Equal(v.Choices, choices) {
		return fmt.Errorf("preview choices omit, contradict or invent previously authorized answers")
	}
	m := v.Model
	if len(m.Forks) != 0 || m.Plan != nil || m.IssuePlan != nil || m.Target != nil ||
		m.Unsupported != "" || len(m.Revised) != 0 || m.Dir != "" || m.Agent != "" || len(m.Existing) != 0 {
		return fmt.Errorf("preview model must contain only the candidate draft and its inline sources, without forks, plans or authority")
	}
	if err := m.validateNames(); err != nil {
		return err
	}
	if m.View == "" {
		return fmt.Errorf("preview model needs a pure view")
	}
	return validateIncludes(m.Includes, m.Module)
}

func (p *Proposal) validatePlanningForks(parent int) error {
	if p.HasPreviews() {
		return fmt.Errorf("plumbing and PRD forks stay chart-free")
	}
	for _, f := range p.Forks {
		if f.Kind == "step_fit" {
			if err := f.ValidateStepFit(parent); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Proposal) checkPreviews(ctx context.Context, tc toolchain.Toolchain, c CheckContext) error {
	if c.Planning == "plumbing" || c.Planning == "prd" {
		return p.validatePlanningForks(c.ParentPlan)
	}
	choices, choiceErr := c.effective(p.Revised)
	var failures []error
	for _, f := range p.Forks {
		if f.Kind == "step_fit" {
			if err := f.ValidateStepFit(c.ParentPlan); err != nil {
				failures = append(failures, err)
			}
			continue
		}
		for _, o := range f.Options {
			v := o.Preview
			if v != nil {
				v.Receipt, v.Problem, v.Fingerprint = nil, "", ""
				if v.Model != nil {
					v.Model.Reader, v.Model.Hash = nil, ""
				}
			}
			err := choiceErr
			if err == nil && f.Kind != "" {
				err = fmt.Errorf("unknown modeled fork kind %q", f.Kind)
			}
			if err == nil {
				err = previewInput(f, o, choices)
			}
			if err == nil {
				err = v.check(ctx, tc, c.Amended, c.Language)
			}
			if err != nil {
				err = fmt.Errorf("fork %s option %s: %w", f.ID, o.ID, err)
				failures = append(failures, err)
				if v != nil {
					v.Problem = err.Error()
				}
			}
			if v != nil {
				v.Fingerprint, _ = previewFingerprint(f, o)
			}
		}
	}
	return errors.Join(failures...)
}

func (v *Preview) check(ctx context.Context, tc toolchain.Toolchain, amended []project.Statement, language string) error {
	m := v.Model
	if err := m.PinAmending(amended); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "invariant-option-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := m.stagedIn(language).Write(dir, "", nil); err != nil {
		return err
	}
	r, err := verify.RunModel(ctx, dir, tc)
	v.Receipt = r
	if err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("model check returned no receipt")
	}
	if !r.Passed {
		return fmt.Errorf("%s", verify.Feedback(r))
	}
	// As for a draft, one check says everything it finds: a refused path
	// and a view the reader can't take are reported together.
	refused := replayPrepared(r)
	if r.Reader == nil {
		return errors.Join(refused, fmt.Errorf("model check returned no reader evidence"))
	}
	if d := r.Reader.Unavailable; d != nil {
		return errors.Join(refused, fmt.Errorf("%s: %s", d.Code, d.Message))
	}
	if refused != nil {
		return refused
	}
	scope := statechart.Preview{Fork: v.Fork, Option: v.Option, Choices: maps.Clone(v.Choices)}
	r.Reader.Preview = scope
	if r.Reader.Chart != nil {
		r.Reader.Chart.Preview = scope
	}
	lock := project.Lock{Bounds: m.Bounds, Statements: m.Statements}
	r.Reader.Fingerprint = receiptReaderFingerprint(r.Reader, lock)
	m.Reader = &Reader{Version: 1, Chart: r.Reader.Chart, Limitation: r.Reader.Limitation}
	m.Reader.Fingerprint, err = m.readerFingerprint()
	return err
}

func previewHash(x any) (string, error) {
	b, err := json.Marshal(x)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(b)), nil
}

func receiptReaderFingerprint(r *verify.Reader, lock project.Lock) string {
	copyReader := *r
	copyReader.Fingerprint = ""
	h, _ := previewHash(struct {
		Reader verify.Reader `json:"reader"`
		Lock   project.Lock  `json:"lock"`
	}{copyReader, lock})
	return h
}

// previewFingerprint is a preview's identity: the fork and option it
// answers, and everything in it but the fingerprint itself. Its receipt
// goes in as saved previews have carried it, the evidence and the coverage
// view (verify.Report.SavedJSON), and not as the receipt is published
// today. A view the receipt gained since, such as each bug's replay status
// (#857), is derived from that evidence, so a preview saved before the view
// existed still reads as itself.
func previewFingerprint(f Fork, o Option) (string, error) {
	v := o.Preview
	var receipt json.RawMessage
	if v.Receipt != nil {
		b, err := v.Receipt.SavedJSON()
		if err != nil {
			return "", err
		}
		receipt = b
	}
	return previewHash(struct {
		Fork     string          `json:"fork"`
		Kind     string          `json:"kind,omitempty"`
		Question string          `json:"question"`
		Option   string          `json:"option"`
		Says     string          `json:"says"`
		Preview  previewIdentity `json:"preview"`
	}{f.ID, f.Kind, f.Question, o.ID, o.Says, previewIdentity{v.Fork, v.Option, v.Choices, v.Model, receipt, v.Problem}})
}

// PreviewFingerprint is the fingerprint ValidatePreview holds an option's
// preview to, for a caller that builds or repairs one, as a fixture does.
func (f Fork) PreviewFingerprint(o Option) (string, error) {
	if o.Preview == nil {
		return "", fmt.Errorf("fork %s option %s: missing preview evidence", f.ID, o.ID)
	}
	return previewFingerprint(f, o)
}

// previewIdentity is Preview's fields in Preview's order, less the
// fingerprint, with the receipt already encoded.
type previewIdentity struct {
	Fork    string            `json:"fork"`
	Option  string            `json:"option"`
	Choices map[string]string `json:"choices,omitempty"`
	Model   *Proposal         `json:"model"`
	Receipt json.RawMessage   `json:"receipt,omitempty"`
	Problem string            `json:"problem,omitempty"`
}

// ValidatePreview validates just this option, so a damaged dashboard sibling
// cannot erase healthy alternatives. Publication uses ValidatePreviews instead.
func (f Fork) ValidatePreview(o Option) error {
	v := o.Preview
	if v == nil {
		return fmt.Errorf("fork %s option %s: missing preview evidence", f.ID, o.ID)
	}
	if err := previewInput(f, o, v.Choices); err != nil {
		return err
	}
	if f.Kind != "" {
		return fmt.Errorf("preview belongs to a modeled behavior fork")
	}
	if v.Problem != "" {
		return fmt.Errorf("%s", v.Problem)
	}
	if err := v.Model.ValidateReader(); err != nil {
		return err
	}
	r, m := v.Receipt, v.Model
	if r == nil || !r.Passed || !r.ModelOnly || r.Ratified != nil || r.Journal != nil || r.Decision != "" ||
		r.Code != nil || r.Conformance != nil || r.Agreement != nil || r.Application != nil || r.Components != nil ||
		r.Reader == nil || r.Reader.Unavailable != nil {
		return fmt.Errorf("preview needs a successful unratified model-only receipt")
	}
	scope := statechart.Preview{Fork: f.ID, Option: o.ID, Choices: v.Choices}
	if r.Reader.Preview.Fork != scope.Fork || r.Reader.Preview.Option != scope.Option || !maps.Equal(r.Reader.Preview.Choices, scope.Choices) ||
		!reflect.DeepEqual(r.Reader.Chart, m.Reader.Chart) || !reflect.DeepEqual(r.Reader.Limitation, m.Reader.Limitation) {
		return fmt.Errorf("preview receipt and model reader identities or evidence disagree")
	}
	if c := m.Reader.Chart; c != nil {
		if c.Preview.Fork != scope.Fork || c.Preview.Option != scope.Option || !maps.Equal(c.Preview.Choices, scope.Choices) {
			return fmt.Errorf("preview chart identity disagrees with its option")
		}
		if _, err := c.SVG(); err != nil {
			return err
		}
	}
	if r.Reader.Fingerprint != receiptReaderFingerprint(r.Reader, project.Lock{Bounds: m.Bounds, Statements: m.Statements}) {
		return fmt.Errorf("preview receipt reader fingerprint is stale")
	}
	inputs := verify.ReaderInputs{Module: m.Module, Model: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(m.ModuleText))),
		View: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(m.View))), Includes: map[string]string{}}
	for name, text := range m.Includes {
		inputs.Includes[name] = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(text)))
	}
	if !reflect.DeepEqual(inputs, r.Reader.Inputs) || !maps.Equal(r.Bounds, m.Bounds) || r.Project != m.Name ||
		(r.Proposal != "" && r.Proposal != m.Hash) || m.Hash != project.ProposalHash(m.Bounds, m.Statements) ||
		r.Fingerprint != verify.Fingerprint(*r) || len(r.Pins) != len(m.Statements) || !r.Design.Passed {
		return fmt.Errorf("preview receipt does not match the candidate source and model check")
	}
	model, err := tla.Read(m.ModuleText, m.Includes)
	if err != nil {
		return fmt.Errorf("preview receipt candidate model: %w", err)
	}
	for i, st := range m.Statements {
		pin := r.Pins[i]
		got, err := model.PinHash(st.Name, project.Model)
		if err != nil || got != st.SHA256 || !pin.Match || pin.Got != got || pin.Want != got ||
			pin.Name != st.Name || pin.Kind != st.Kind || pin.Says != st.Says {
			return fmt.Errorf("preview receipt candidate pins disagree with its model")
		}
	}
	if c := m.Reader.Chart; c != nil {
		if !reflect.DeepEqual(c.Report.Rules, m.Statements) || !maps.Equal(c.Report.Source.Config.Constants, m.Bounds) ||
			c.Report.Counts["tlc_states"] != r.Design.DistinctStates || c.Report.Counts["tlc_generated_states"] != r.Design.StatesGenerated {
			return fmt.Errorf("preview chart rules, bounds or counts disagree with its receipt")
		}
	}
	want, err := previewFingerprint(f, o)
	if err != nil {
		return err
	}
	if v.Fingerprint != want {
		return fmt.Errorf("preview evidence is stale: option words, inputs, choices, diagnostics or computed evidence changed; check again")
	}
	return nil
}

// ValidatePreviews is the indivisible publication/export boundary. Chart-free
// callers validate step-fit provenance separately; legacy Read stays usable.
func (p *Proposal) ValidatePreviews() error {
	if err := validateForks(p.Forks); err != nil {
		return err
	}
	var failures []error
	for _, f := range p.Forks {
		if f.Kind == "step_fit" {
			for _, o := range f.Options {
				if o.Preview != nil {
					failures = append(failures, fmt.Errorf("fork %s option %s: step_fit stays chart-free", f.ID, o.ID))
				}
			}
			continue
		}
		for _, o := range f.Options {
			if err := f.ValidatePreview(o); err != nil {
				failures = append(failures, fmt.Errorf("fork %s option %s: %w", f.ID, o.ID, err))
			}
		}
	}
	return errors.Join(failures...)
}

// ValidatePreviewContext compares a fresh or recovered result with the caller's
// planning mode and effective choices. Partial-answer posts keep the original
// snapshots instead.
func (p *Proposal) ValidatePreviewContext(c CheckContext) error {
	if c.Planning == "plumbing" || c.Planning == "prd" {
		return p.validatePlanningForks(c.ParentPlan)
	}
	choices, err := c.effective(p.Revised)
	if err != nil {
		return err
	}
	for _, f := range p.Forks {
		if f.Kind == "step_fit" {
			if err := f.ValidateStepFit(c.ParentPlan); err != nil {
				return err
			}
			continue
		}
		for _, o := range f.Options {
			if err := previewInput(f, o, choices); err != nil {
				return fmt.Errorf("fork %s option %s: %w", f.ID, o.ID, err)
			}
		}
	}
	return p.ValidatePreviews()
}

func (p *Proposal) PreviewFeedback() string {
	var lines []string
	for _, f := range p.Forks {
		for _, o := range f.Options {
			if v := o.Preview; v != nil && v.Model != nil {
				lines = append(lines, f.ID+"/"+o.ID+": "+v.Model.ReaderFeedback())
			}
		}
	}
	return strings.Join(lines, "\n")
}

// WritePreviews replaces the complete exported set only after validation and
// rendering succeed. Reusing out removes options absent from this request.
func (p *Proposal) WritePreviews(out string) error {
	if err := p.ValidatePreviews(); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(out, ".previews-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for _, f := range p.Forks {
		for _, o := range f.Options {
			if o.Preview == nil {
				continue
			}
			dir := filepath.Join(stage, f.ID, o.ID)
			if err := receipt.Write(dir, o.Preview.Receipt); err != nil {
				return err
			}
			b, err := json.MarshalIndent(o.Preview, "", "  ")
			if err != nil {
				return err
			}
			if err := writeFile(filepath.Join(dir, "preview.json"), string(b)+"\n"); err != nil {
				return err
			}
		}
	}
	dest := filepath.Join(out, "previews")
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	return os.Rename(stage, dest)
}
