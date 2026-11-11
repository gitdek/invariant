package factory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/verify"
)

// DraftReference names exactly one completed draft's result.json. It has no
// caller-selected path or URL, and its digest covers the complete saved bytes.
type DraftReference struct {
	Version      int    `json:"version"`
	Repository   string `json:"repository"`
	Issue        int    `json:"issue"`
	Step         string `json:"step"`
	Commit       string `json:"commit"`
	SHA256       string `json:"sha256"`
	ProposalHash string `json:"proposal_hash"`
}

var draftCommitRE = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
var draftDigestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var draftFieldRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func draftDigest(b []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(b)) }

func draftEqual(a, b any) bool {
	x, ex := json.Marshal(a)
	y, ey := json.Marshal(b)
	return ex == nil && ey == nil && bytes.Equal(x, y)
}

// Reference-bearing objects cannot silently choose one of two conflicting
// fields. Raw values keep validation separate from marker discovery.
func draftObject(b []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("expected a JSON object")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok || !draftFieldRE.MatchString(name) || fields[name] != nil {
			return nil, fmt.Errorf("duplicate or noncanonical JSON field %q", name)
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, err
		}
		fields[name] = value
	}
	if _, err := d.Token(); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected data after JSON object")
	}
	return fields, nil
}

// MarshalJSON retains the transport even after resolution. The fork IDs are
// observations about which questions remain; their evidence stays in the run.
func (m Marker) MarshalJSON() ([]byte, error) {
	type marker Marker
	if m.ProposalRef == nil {
		return json.Marshal(marker(m))
	}
	type forkID struct {
		ID string `json:"id"`
	}
	var forks []forkID
	for _, f := range m.Forks {
		forks = append(forks, forkID{f.ID})
	}
	m.Proposal, m.Forks = nil, nil
	return json.Marshal(struct {
		marker
		Forks []forkID `json:"forks,omitempty"`
	}{marker(m), forks})
}

// ResolveDraft is shared by factory and dashboard readers. The caller must
// first authenticate the enclosing comment under its factory-author rule and
// supply callbacks confined to repository. No checker or agent runs here.
// Failure returns neither a usable marker nor a partial result, so callers
// can retry without falling back to an earlier proposal or answering commands.
func (m Marker) ResolveDraft(ctx context.Context, repository string, issue int,
	run func(context.Context, int, string) (RunState, string, error),
	show func(context.Context, string, string) ([]byte, error)) (Marker, *formalize.Result, error) {
	if m.ProposalRef == nil {
		return m, nil, nil
	}
	fail := func(err error) (Marker, *formalize.Result, error) {
		return Marker{}, nil, fmt.Errorf("draft reference for %s#%d: %w; retry after restoring the saved record", repository, issue, err)
	}
	if m.referenceProblem != "" {
		return fail(fmt.Errorf("malformed marker: %s", m.referenceProblem))
	}
	switch m.Kind {
	case KindForks, KindProposal, KindStuck, KindUnsupported, KindRatified, KindPR, KindFailed, KindMerged, KindClosed, KindNote:
	default:
		return fail(fmt.Errorf("unknown marker state %q", m.Kind))
	}
	var ref DraftReference
	fields, err := draftObject(m.ProposalRef)
	if err != nil {
		return fail(err)
	}
	for _, field := range []string{"version", "repository", "issue", "step", "commit", "sha256", "proposal_hash"} {
		if fields[field] == nil || bytes.Equal(fields[field], []byte("null")) {
			return fail(fmt.Errorf("missing reference field %s", field))
		}
	}
	d := json.NewDecoder(bytes.NewReader(m.ProposalRef))
	d.DisallowUnknownFields()
	if err := d.Decode(&ref); err != nil {
		return fail(err)
	}
	if ref.Version != 1 || repository == "" || ref.Repository != repository || issue <= 0 || ref.Issue != issue ||
		!draftCommitRE.MatchString(ref.Commit) || !draftDigestRE.MatchString(ref.SHA256) ||
		(ref.ProposalHash != "" && !draftDigestRE.MatchString(ref.ProposalHash)) {
		return fail(fmt.Errorf("unsupported or mismatched repository, issue, version, commit or digest"))
	}
	if ref.Step != "draft-issue" {
		id := strings.TrimPrefix(ref.Step, "draft-")
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil || n <= 0 || ref.Step != "draft-"+strconv.FormatInt(n, 10) {
			return fail(fmt.Errorf("invalid draft step %q", ref.Step))
		}
	}
	if run == nil || show == nil {
		return fail(fmt.Errorf("saved-run reader is unavailable"))
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	state, commit, err := run(ctx, issue, ref.Step)
	if err != nil {
		return fail(err)
	}
	if state != RunDone || commit != ref.Commit {
		return fail(fmt.Errorf("%s is not completed at the referenced commit", ref.Step))
	}
	b, err := show(ctx, ref.Commit, "result.json")
	if err != nil {
		return fail(err)
	}
	if draftDigest(b) != ref.SHA256 {
		return fail(fmt.Errorf("result.json digest disagrees"))
	}
	if _, err := draftObject(b); err != nil {
		return fail(err)
	}
	var result formalize.Result
	if err := json.Unmarshal(b, &result); err != nil {
		return fail(err)
	}
	if err := validateDraftResult(&result); err != nil {
		return fail(err)
	}
	p := result.Proposal
	if ref.ProposalHash != p.Hash || (m.Hash != "" && m.Hash != p.Hash) {
		return fail(fmt.Errorf("proposal identity disagrees"))
	}
	selected := p
	if m.ProposalCarrier {
		if (m.Kind != KindStuck && m.Kind != KindUnsupported && m.Kind != KindNote) ||
			len(p.Forks) == 0 || len(m.Forks) != 0 || m.Hash != "" || p.Carrier() == nil {
			return fail(fmt.Errorf("marker cannot carry an answered question's baseline"))
		}
		selected = p.Carrier()
	}
	if m.Proposal != nil && !draftEqual(m.Proposal, selected) {
		return fail(fmt.Errorf("inline proposal conflicts with the saved result"))
	}
	var forks []formalize.Fork
	seen := map[string]bool{}
	for _, remaining := range m.Forks {
		i := slices.IndexFunc(p.Forks, func(f formalize.Fork) bool { return f.ID == remaining.ID })
		if i < 0 || seen[remaining.ID] {
			return fail(fmt.Errorf("remaining fork %q is absent or repeated", remaining.ID))
		}
		if !draftEqual(remaining, formalize.Fork{ID: remaining.ID}) && !draftEqual(remaining, p.Forks[i]) {
			return fail(fmt.Errorf("inline fork %s conflicts with the saved question", remaining.ID))
		}
		seen[remaining.ID] = true
		forks = append(forks, p.Forks[i])
	}
	if m.Kind == KindForks && len(forks) == 0 || m.Kind == KindProposal && !p.Ratifiable() {
		return fail(fmt.Errorf("marker state disagrees with the saved proposal"))
	}
	// Detach the marker's observations too. A caller can mutate either returned
	// value without changing the decoded transport it resolves next time.
	observations, err := json.Marshal(m)
	if err != nil {
		return fail(err)
	}
	var hydrated Marker
	if err := json.Unmarshal(observations, &hydrated); err != nil {
		return fail(err)
	}
	hydrated.Proposal, hydrated.Forks = selected, forks
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	return hydrated, &result, nil
}

// savedSlug is the kebab case formalize requires of every draft's slug, which
// names the project's directory and its branch.
var savedSlug = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func validateDraftResult(r *formalize.Result) error {
	p := r.Proposal
	if p == nil || r.CheckContext == nil || r.Problem != "" || p.Unsupported != "" {
		return fmt.Errorf("saved result needs a complete successful proposal and caller check context")
	}
	if p.Plan != nil || p.IssuePlan != nil || r.CheckContext.Planning != "" {
		return fmt.Errorf("saved draft references require modeled evidence")
	}
	// Ratification writes where the proposal says, and a payload whose digest
	// matches can still say anything there. Hold a saved directory, and a
	// selected proposal's slug below, to the rules they meet when an issue
	// names one or a draft is made.
	if p.Dir != "" {
		if problem := badDir(p.Dir); problem != "" {
			return fmt.Errorf("saved project directory %q: %s", p.Dir, problem)
		}
	}
	if target := p.Target; target != nil {
		if problem := badDir(target.Dir); problem != "" {
			return fmt.Errorf("saved amendment directory %q: %s", target.Dir, problem)
		}
		c := target.Baseline
		if c == nil || target.Dir != c.Dir || !draftEqual(target.Manifest, c.Manifest) ||
			target.Amends != project.ProposalHash(c.Lock.Bounds, c.Lock.Statements) || target.Previous != c.Previous() {
			return fmt.Errorf("saved amendment target disagrees with its original capture")
		}
		context := formalize.Request{Current: c}.CheckContext()
		if baseline := r.CheckContext.Baseline; baseline != nil && baseline.View == "" {
			context.Baseline.View = "" // a missing legacy view can be supplied by the check
		}
		if !draftEqual(context.Baseline, r.CheckContext.Baseline) {
			return fmt.Errorf("saved amendment target disagrees with its check context")
		}
	}
	if len(p.Forks) > 0 {
		if p.Hash != "" {
			return fmt.Errorf("an unanswered question has no selected proposal identity")
		}
		if err := p.ValidatePreviewContext(*r.CheckContext); err != nil {
			return err
		}
		if !draftEqual(p.Baseline, r.CheckContext.Baseline) {
			return fmt.Errorf("question comparison baseline disagrees with its saved context")
		}
		for _, f := range p.Forks {
			for _, o := range f.Options {
				if v := o.Preview; v != nil {
					scope := statechart.Preview{Fork: f.ID, Option: o.ID, Choices: v.Choices}
					if err := validateDraftReceipt(v.Model, v.Receipt, scope); err != nil {
						return fmt.Errorf("fork %s option %s: %w", f.ID, o.ID, err)
					}
				}
			}
		}
		return nil
	}
	if !savedSlug.MatchString(p.Slug) {
		return fmt.Errorf("saved proposal slug %q must be kebab case", p.Slug)
	}
	if err := p.ValidateReader(); err != nil {
		return err
	}
	if err := p.ValidateComparisonContext(*r.CheckContext); err != nil {
		return err
	}
	if r.Changes != nil && !draftEqual(r.Changes.Reader, p.Reader.Comparison) {
		return fmt.Errorf("saved changes disagree with the proposal comparison")
	}
	return validateDraftReceipt(p, r.Report, statechart.Preview{})
}

// The model receipt and proposal reader are independently computed. Bind both
// to the same sources, pins, bounds, tools and counts without requiring TLC's
// per-exploration concrete fingerprints to be identical. Alternatives retain
// their checked preview scope; selected proposals require an empty scope.
func validateDraftReceipt(p *formalize.Proposal, r *verify.Report, scope statechart.Preview) error {
	if r == nil || !r.Passed || !r.ModelOnly || r.Ratified != nil || r.Journal != nil || r.Decision != "" ||
		r.Code != nil || r.Conformance != nil || r.Agreement != nil || r.Application != nil || r.Components != nil ||
		r.Reader == nil || r.Reader.Unavailable != nil || r.Reader.Version != 1 || !draftEqual(r.Reader.Preview, scope) {
		return fmt.Errorf("saved proposal needs a successful unratified model-only receipt")
	}
	if p.Hash != project.ProposalHash(p.Bounds, p.Statements) || r.Project != p.Name ||
		(r.Proposal != "" && r.Proposal != p.Hash) || !maps.Equal(r.Bounds, p.Bounds) ||
		r.Fingerprint != verify.Fingerprint(*r) || len(r.Pins) != len(p.Statements) {
		return fmt.Errorf("saved receipt disagrees with the proposal")
	}
	inputs := verify.ReaderInputs{Module: p.Module, Model: draftDigest([]byte(p.ModuleText)),
		View: draftDigest([]byte(p.View)), Includes: map[string]string{}}
	for name, text := range p.Includes {
		inputs.Includes[name] = draftDigest([]byte(text))
	}
	if !draftEqual(inputs, r.Reader.Inputs) {
		return fmt.Errorf("saved receipt sources disagree with the proposal")
	}
	lock := project.Lock{Bounds: p.Bounds, Statements: p.Statements}
	rr := *r.Reader
	rr.Fingerprint = ""
	b, err := json.Marshal(struct {
		Reader verify.Reader `json:"reader"`
		Lock   project.Lock  `json:"lock"`
	}{rr, lock})
	if err != nil || r.Reader.Fingerprint != draftDigest(b) {
		return fmt.Errorf("saved receipt reader fingerprint disagrees")
	}
	cfg := tlc.Config{Constants: p.Bounds}
	for i, st := range p.Statements {
		pin := r.Pins[i]
		got, err := tla.PinHash(p.ModuleText, st.Name, project.Model)
		if err != nil || got != st.SHA256 || !pin.Match || pin.Got != got || pin.Want != got ||
			pin.Name != st.Name || pin.Kind != st.Kind || pin.Says != st.Says {
			return fmt.Errorf("saved receipt pins disagree with the model")
		}
		switch st.Kind {
		case project.Spec:
			cfg.Specification = st.Name
		case project.Invariant:
			cfg.Invariants = append(cfg.Invariants, st.Name)
		}
	}
	if err := validateDraftOutcomes(p, r); err != nil {
		return err
	}
	// The report names the pinned release; reader provenance hashes the jar
	// actually used, which can differ in checking fixtures. Both independent
	// readers must agree on that actual hash as well as the image and version.
	var actual statechart.Source
	if rr.Chart != nil {
		actual = rr.Chart.Report.Source
	} else if rr.Limitation != nil {
		actual = rr.Limitation.Source
	}
	if !draftDigestRE.MatchString("sha256:" + actual.Tool.JarSHA256) {
		return fmt.Errorf("saved reader is missing its actual TLC jar identity")
	}
	source := statechart.Source{Module: p.Module, Config: cfg,
		Tool: tlc.GraphTool{Image: r.Toolchain.JavaImage, JarSHA256: actual.Tool.JarSHA256}, TLC: r.Toolchain.TLC}
	for _, reader := range []formalize.Reader{*p.Reader, {Version: rr.Version, Chart: rr.Chart, Limitation: rr.Limitation}} {
		if (reader.Chart == nil) == (reader.Limitation == nil) {
			return fmt.Errorf("saved reader needs exactly one checked chart or limitation")
		}
		if c := reader.Chart; c != nil {
			if !draftEqual(c.Preview, scope) || !draftEqual(c.Report.Rules, p.Statements) || !draftEqual(c.Report.Source, source) ||
				c.Report.Counts["tlc_states"] != r.Design.DistinctStates || c.Report.Counts["tlc_generated_states"] != r.Design.StatesGenerated {
				return fmt.Errorf("saved chart rules, counts or tools disagree with the model check")
			}
			if _, err := c.SVG(); err != nil {
				return err
			}
		} else if l := reader.Limitation; l.Code != "too_large" || l.Limit != statechart.MaxStates || l.Actual <= l.Limit ||
			int64(l.Actual) != r.Design.DistinctStates || !draftEqual(l.Source, source) {
			return fmt.Errorf("saved reader limitation disagrees with the model check")
		}
	}
	return nil
}

// A passing report flag cannot replace any of the model checks. Require one
// successful result for every statement, in the order the gate records them,
// for selected proposals and for each alternative's model-only receipt.
func validateDraftOutcomes(p *formalize.Proposal, r *verify.Report) error {
	if p == nil || r == nil {
		return fmt.Errorf("saved receipt is missing model-check evidence")
	}
	model := project.Project{Lock: project.Lock{Statements: p.Statements}}
	if len(model.Of(project.Spec)) != 1 || !r.Design.Passed || r.Design.Outcome != string(tlc.Passed) || r.Design.Violated != "" ||
		r.Design.Spec != model.SpecName() || !slices.Equal(r.Design.Invariants, model.Invariants()) {
		return fmt.Errorf("saved receipt has no matching completed model check")
	}
	witnesses, bugs := model.Of(project.Witness), model.Of(project.Bug)
	properties, fairness := model.Of(project.Property), model.Of(project.Fairness)
	if len(r.Witnesses) != len(witnesses) || len(r.Bugs) != len(bugs) ||
		len(r.Properties) != len(properties) || len(r.Fairness) != len(fairness) {
		return fmt.Errorf("saved receipt is missing or has extra model-check results")
	}
	for i, s := range witnesses {
		w := r.Witnesses[i]
		if w.Name != s.Name || w.Says != s.Says || !w.Reached {
			return fmt.Errorf("saved receipt has no matching reached witness %s", s.Name)
		}
	}
	for i, s := range bugs {
		b := r.Bugs[i]
		outcome := string(tlc.Violated)
		if slices.ContainsFunc(properties, func(p project.Statement) bool { return p.Name == s.Expect }) {
			outcome = string(tlc.PropertyViolated)
		}
		if b.Name != s.Name || b.Says != s.Says || b.Expect != s.Expect || !b.Caught || b.Violated != s.Expect || b.Outcome != outcome {
			return fmt.Errorf("saved receipt has no matching caught bug %s", s.Name)
		}
	}
	for i, s := range properties {
		p := r.Properties[i]
		if p.Name != s.Name || p.Says != s.Says || !p.Holds || p.Outcome != string(tlc.Passed) {
			return fmt.Errorf("saved receipt has no matching successful property %s", s.Name)
		}
	}
	for i, s := range fairness {
		f := r.Fairness[i]
		if f.Name != s.Name || f.Says != s.Says || !f.InNext || f.Action == "" {
			return fmt.Errorf("saved receipt has no matching checked fairness %s", s.Name)
		}
	}
	return nil
}

func (f *Factory) draftPost(ctx context.Context, issue int, c github.Comment) (Post, bool, error) {
	if f.Self != "" && c.User.Login != f.Self {
		return Post{}, false, nil
	}
	m, ok := DecodeMarker(c.Body)
	if !ok {
		return Post{}, false, nil
	}
	if m.ProposalRef == nil {
		return Post{Comment: c, Marker: m}, true, nil
	}
	if f.Repo == nil {
		return Post{}, false, fmt.Errorf("draft reference reader is unavailable")
	}
	m, _, err := m.ResolveDraft(ctx, f.Repository, issue, f.Repo.Run, f.Repo.Show)
	if err != nil {
		return Post{}, false, fmt.Errorf("comment %d: %w", c.ID, err)
	}
	return Post{Comment: c, Marker: m}, true, nil
}

// retainedDraft keeps a previous reference only when this post carries that
// complete proposal or its answered-question carrier, never just the same hash.
func retainedDraft(t Thread, m Marker) Marker {
	if state, ok := t.State(); ok && state.Marker.ProposalRef != nil && m.Proposal != nil {
		switch {
		case draftEqual(state.Marker.Proposal, m.Proposal):
			m.ProposalRef, m.ProposalCarrier = state.Marker.ProposalRef, state.Marker.ProposalCarrier
		case draftEqual(state.Marker.Proposal.Carrier(), m.Proposal):
			m.ProposalRef, m.ProposalCarrier = state.Marker.ProposalRef, true
		}
	}
	return m
}

// savedDraftResult extends legacy result.json without changing the complete
// formalization result. Publication belongs to the caller, captured before the
// run, and is saved with effective proposal metadata before Finish exposes it.
type savedDraftResult struct {
	formalize.Result
	Publication *draftPublication `json:"publication,omitempty"`
}

type draftPublication struct {
	Agent         string              `json:"agent"`
	Dir           string              `json:"dir,omitempty"`
	ProposalAgent string              `json:"proposal_agent,omitempty"`
	Base          string              `json:"base,omitempty"`
	Answers       []formalize.Answer  `json:"answers,omitempty"`
	Previous      *formalize.Proposal `json:"previous,omitempty"`
	Authority     *formalize.Proposal `json:"authority,omitempty"`
}

func applyDraftPublication(r *savedDraftResult) {
	if p := r.Proposal; p != nil {
		if r.Publication.Dir != "" {
			p.Dir = r.Publication.Dir
		}
		p.Agent = r.Publication.ProposalAgent
	}
}

func (f *Factory) loadDraft(ctx context.Context, commit string) (*savedDraftResult, error) {
	b, err := f.Repo.Show(ctx, commit, "result.json")
	if err != nil {
		return nil, err
	}
	var r savedDraftResult
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("read completed draft result: %w", err)
	}
	return &r, nil
}
