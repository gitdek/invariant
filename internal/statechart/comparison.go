package statechart

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
)

// ComparisonSnapshot contains one checked source, never its comparison history.
type ComparisonSnapshot struct {
	Kind       string            `json:"kind"`
	Name       string            `json:"name"`
	Module     string            `json:"module"`
	Model      string            `json:"model"`
	Includes   map[string]string `json:"includes"`
	View       string            `json:"view"`
	Lock       project.Lock      `json:"lock"`
	Chart      *Result           `json:"chart,omitempty"`
	Limitation *Limitation       `json:"limitation,omitempty"`
}

// Saved sources preserve temporal trace numbers even when their enclosing
// proposal or receipt is decoded with the ordinary JSON decoder.
func (s *ComparisonSnapshot) UnmarshalJSON(data []byte) error {
	type snapshot ComparisonSnapshot
	var out snapshot
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(&out); err != nil {
		return err
	}
	*s = ComparisonSnapshot(out)
	return nil
}

type ComparisonIdentity struct {
	ModelSHA256  string            `json:"model_sha256"`
	ViewSHA256   string            `json:"view_sha256"`
	Includes     map[string]string `json:"includes"`
	LockSHA256   string            `json:"lock_sha256"`
	ProposalHash string            `json:"proposal_hash"`
}

type StateChange struct {
	Before State `json:"before"`
	After  State `json:"after"`
}
type ArrowChange struct {
	Before Arrow `json:"before"`
	After  Arrow `json:"after"`
}

type Delta struct {
	AddedStates   []State       `json:"added_states"`
	RemovedStates []State       `json:"removed_states"`
	ChangedStates []StateChange `json:"changed_states"`
	AddedArrows   []Arrow       `json:"added_arrows"`
	RemovedArrows []Arrow       `json:"removed_arrows"`
	ChangedArrows []ArrowChange `json:"changed_arrows"`
}

type ComparisonUnavailable struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Comparison struct {
	Version           int                    `json:"version"`
	Fingerprint       string                 `json:"fingerprint"`
	Baseline          ComparisonSnapshot     `json:"baseline"`
	Candidate         ComparisonSnapshot     `json:"candidate"`
	BaselineIdentity  ComparisonIdentity     `json:"baseline_identity"`
	CandidateIdentity ComparisonIdentity     `json:"candidate_identity"`
	Visible           Delta                  `json:"visible"`
	BeforeHiding      Delta                  `json:"before_hiding"`
	ViewChanged       bool                   `json:"view_changed"`
	ModelChanged      bool                   `json:"model_changed"`
	BoundsChanged     bool                   `json:"bounds_changed"`
	Scope             string                 `json:"scope,omitempty"`
	Unavailable       *ComparisonUnavailable `json:"unavailable,omitempty"`
}

func comparisonHash(b []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(b)) }
func comparisonJSON(x any) []byte    { b, _ := json.Marshal(x); return b }
func comparisonEqual(a, b any) bool  { return bytes.Equal(comparisonJSON(a), comparisonJSON(b)) }

// Compare detaches its inputs and compares identities and concrete occurrence
// multisets. Equal charts make no assertion about behavioral equivalence.
func Compare(baseline, candidate ComparisonSnapshot) (*Comparison, error) {
	a, err := canonicalSnapshot(baseline)
	if err != nil {
		return nil, fmt.Errorf("comparison baseline: %w", err)
	}
	b, err := canonicalSnapshot(candidate)
	if err != nil {
		return nil, fmt.Errorf("comparison candidate: %w", err)
	}
	c := &Comparison{Version: 1, Baseline: a, Candidate: b,
		BaselineIdentity: a.Identity(), CandidateIdentity: b.Identity(),
		ViewChanged: a.View != b.View, ModelChanged: a.Model != b.Model || !maps.Equal(a.Includes, b.Includes),
		BoundsChanged: !maps.Equal(a.Lock.Bounds, b.Lock.Bounds)}
	if c.BoundsChanged {
		c.Scope = "Bounds differ: changes describe the displayed scope of each complete bounded exploration, not a common checking scope."
	}
	if a.Limitation != nil || b.Limitation != nil {
		c.Unavailable = &ComparisonUnavailable{Code: "too_large", Message: "Comparison unavailable: at least one complete exploration exceeds 500 states. No empty or partial graph is substituted; mappings and hiding for that source were not evaluated."}
	} else {
		c.Visible = compareCharts(a.Chart, b.Chart, false)
		c.BeforeHiding = compareCharts(a.Chart, b.Chart, true)
	}
	c.Fingerprint = comparisonHash(comparisonJSON(c))
	return c, nil
}

func (s ComparisonSnapshot) Identity() ComparisonIdentity {
	i := ComparisonIdentity{ModelSHA256: comparisonHash([]byte(s.Model)), ViewSHA256: comparisonHash([]byte(s.View)),
		Includes: map[string]string{}, LockSHA256: comparisonHash(comparisonJSON(s.Lock)), ProposalHash: project.ProposalHash(s.Lock.Bounds, s.Lock.Statements)}
	for name, text := range s.Includes {
		i.Includes[name] = comparisonHash([]byte(text))
	}
	return i
}

// Validate checks retained sources, identities, all deltas and their binding.
// It does not turn explanatory provenance into a new verifier result.
func (c *Comparison) Validate() error {
	_, err := c.Validated()
	return err
}

// Validated returns detached canonical evidence after checking every retained
// field. It is a snapshot, not a cache: later validation checks mutations anew.
func (c *Comparison) Validated() (*Comparison, error) {
	if c == nil {
		return nil, fmt.Errorf("comparison is missing; check against the captured baseline again")
	}
	want, err := Compare(c.Baseline, c.Candidate)
	if err != nil {
		return nil, err
	}
	if !comparisonEqual(c, want) {
		return nil, fmt.Errorf("comparison evidence is stale or damaged; check the captured baseline and candidate again")
	}
	return want, nil
}

func canonicalSnapshot(s ComparisonSnapshot) (ComparisonSnapshot, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return s, err
	}
	var out ComparisonSnapshot
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&out); err != nil {
		return s, err
	}
	s = out
	// Saved proposals omit empty includes, so no includes and an empty set
	// are one source.
	if s.Includes == nil {
		s.Includes = map[string]string{}
	}
	if (s.Kind != "proposal" && s.Kind != "ratification") || s.Name == "" || s.Model == "" || s.View == "" || (s.Chart == nil) == (s.Limitation == nil) {
		return s, fmt.Errorf("requires named source, model, view and exactly one checked chart or limitation")
	}
	if strings.IndexFunc(s.Name, func(r rune) bool { return !unicode.IsGraphic(r) }) >= 0 {
		return s, fmt.Errorf("source name is not printable")
	}
	if name, err := tla.ModuleName(s.Model); err != nil || name != s.Module {
		return s, fmt.Errorf("model module differs from captured source")
	}
	if _, err := parseView(context.Background(), []byte(s.View)); err != nil {
		return s, err
	}
	for name := range s.Includes {
		if !tla.ValidIncludeName(name, s.Module) {
			return s, fmt.Errorf("invalid sibling include %q", name)
		}
	}
	model, err := tla.Read(s.Model, s.Includes)
	if err != nil {
		return s, fmt.Errorf("captured model can't be read as TLC reads it: %w", err)
	}
	for _, st := range s.Lock.Statements {
		pin, err := model.PinHash(st.Name, project.Model)
		if err != nil || pin != st.SHA256 {
			return s, fmt.Errorf("captured rule %s does not match model pin", st.Name)
		}
	}
	var source Source
	if c := s.Chart; c != nil {
		if !c.Preview.IsZero() {
			return s, fmt.Errorf("an alternative is not a selected comparison source")
		}
		if err := validateSVG(c); err != nil {
			return s, err
		}
		if !comparisonEqual(c.Report.Rules, s.Lock.Statements) {
			return s, fmt.Errorf("chart rules differ from captured lock")
		}
		slices.SortFunc(c.States, func(a, b State) int { return strings.Compare(a.ID, b.ID) })
		slices.Sort(c.Initial)
		for i := range c.States {
			x := &c.States[i]
			slices.Sort(x.Members)
			slices.Sort(x.InitialMembers)
			slices.Sort(x.ViolatedRules)
			slices.Sort(x.UnavailableActions)
		}
		for _, arrows := range [][]Arrow{c.Arrows, c.Report.Hidden} {
			sortArrows(arrows)
			for i := range arrows {
				sortSteps(arrows[i].Members)
				slices.Sort(arrows[i].HidingRules)
			}
		}
		source = c.Report.Source
	} else {
		l := s.Limitation
		if l.Code != "too_large" || l.Limit != MaxStates || l.Actual <= MaxStates {
			return s, fmt.Errorf("invalid size limitation")
		}
		source = l.Source
	}
	if source.Module != s.Module || !maps.Equal(source.Config.Constants, s.Lock.Bounds) {
		return s, fmt.Errorf("chart source module or bounds differ from captured lock")
	}
	return s, nil
}

type arrowIdentity struct{ From, Label, To string }

func arrowID(a Arrow) arrowIdentity { return arrowIdentity{a.From, a.Label, a.To} }

// Concrete members are TLC fingerprints, and TLC picks its fingerprint
// polynomial afresh on every run, so two explorations of one model name the
// same concrete states differently. A state changes when how many concrete
// states it holds, or their status, changes; an arrow changes when the
// multiset of its occurrences' actions does.
func stateSignature(s State) any {
	return []any{s.Label, len(s.Members), len(s.InitialMembers), s.ViolatedRules, s.UnavailableActions}
}

func arrowSignature(a Arrow) any {
	return []any{a.Label, a.Count, arrowActions(a), a.HidingRules}
}

func arrowActions(a Arrow) []string {
	actions := make([]string, 0, len(a.Members))
	for _, step := range a.Members {
		actions = append(actions, step.Action)
	}
	slices.Sort(actions)
	return actions
}

func compareCharts(a, b *Result, beforeHiding bool) Delta {
	d := Delta{}
	oldStates, newStates := map[string]State{}, map[string]State{}
	for _, s := range a.States {
		oldStates[s.ID] = s
	}
	for _, s := range b.States {
		newStates[s.ID] = s
		if old, ok := oldStates[s.ID]; !ok {
			d.AddedStates = append(d.AddedStates, s)
		} else if !comparisonEqual(stateSignature(old), stateSignature(s)) {
			d.ChangedStates = append(d.ChangedStates, StateChange{old, s})
		}
	}
	for _, s := range a.States {
		if _, ok := newStates[s.ID]; !ok {
			d.RemovedStates = append(d.RemovedStates, s)
		}
	}
	arrows := func(c *Result) []Arrow {
		out := slices.Clone(c.Arrows)
		if beforeHiding {
			out = append(out, c.Report.Hidden...)
			for i := range out {
				out[i].HidingRules = nil
			}
		}
		sortArrows(out)
		return out
	}
	oldArrows, newArrows := map[arrowIdentity]Arrow{}, map[arrowIdentity]Arrow{}
	for _, x := range arrows(a) {
		oldArrows[arrowID(x)] = x
	}
	for _, x := range arrows(b) {
		id := arrowID(x)
		newArrows[id] = x
		if old, ok := oldArrows[id]; !ok {
			d.AddedArrows = append(d.AddedArrows, x)
		} else if !comparisonEqual(arrowSignature(old), arrowSignature(x)) {
			d.ChangedArrows = append(d.ChangedArrows, ArrowChange{old, x})
		}
	}
	for _, x := range arrows(a) {
		if _, ok := newArrows[arrowID(x)]; !ok {
			d.RemovedArrows = append(d.RemovedArrows, x)
		}
	}
	return d
}

// Qualifications supplies the same literal caveats to every publisher.
func (c *Comparison) Qualifications() []string {
	out := []string{"Equal charts do not establish behavioral equivalence. This bounded, view-qualified comparison adds no proof claim."}
	if c.ViewChanged {
		out = append(out, "View change: labels or hiding definitions differ; chart differences may reflect the view.")
	}
	if c.ModelChanged {
		out = append(out, "Model source change: exact model or sibling include bytes differ.")
	}
	if c.Scope != "" {
		out = append(out, c.Scope)
	}
	if c.Unavailable != nil {
		out = append(out, c.Unavailable.Message)
	}
	return out
}

func (c *Comparison) reportLines() []string {
	out := []string{"Proposal comparison", "Legend: added (green, solid); removed (red, dashed); changed retains before and after evidence."}
	out = append(out, c.Qualifications()...)
	for i, s := range []ComparisonSnapshot{c.Baseline, c.Candidate} {
		id, side := c.BaselineIdentity, "Baseline"
		if i == 1 {
			id, side = c.CandidateIdentity, "Candidate"
		}
		out = append(out, side+": "+s.Name, "Model: "+id.ModelSHA256, "View: "+id.ViewSHA256, "Lock: "+id.LockSHA256, "Proposal: "+id.ProposalHash)
		for _, name := range slices.Sorted(maps.Keys(id.Includes)) {
			out = append(out, "Include "+name+": "+id.Includes[name])
		}
		out = append(out, side+" checked bounds:")
		for _, name := range slices.Sorted(maps.Keys(s.Lock.Bounds)) {
			out = append(out, name+" = "+s.Lock.Bounds[name])
		}
		if s.Limitation != nil {
			out = append(out, fmt.Sprintf("The chart is not drawn (too_large): TLC reached %d states, more than the %d states charts are drawn for. View mappings and hiding were not evaluated.", s.Limitation.Actual, s.Limitation.Limit))
		}
	}
	if c.Unavailable != nil {
		return out
	}
	for i, d := range []Delta{c.Visible, c.BeforeHiding} {
		name := "Visible"
		if i == 1 {
			name = "Before hiding (visibility and hiding rule IDs ignored)"
		}
		out = append(out, fmt.Sprintf("%s: states added %d, removed %d, changed %d; arrows added %d, removed %d, changed %d.", name, len(d.AddedStates), len(d.RemovedStates), len(d.ChangedStates), len(d.AddedArrows), len(d.RemovedArrows), len(d.ChangedArrows)))
		if i == 0 {
			out = append(out, c.deltaLines(d, nil)...)
		} else {
			// States are never hidden, and the visible lines above already
			// name every arrow change that hiding doesn't affect.
			shown := map[string]bool{}
			for _, line := range c.deltaLines(Delta{AddedArrows: c.Visible.AddedArrows, RemovedArrows: c.Visible.RemovedArrows, ChangedArrows: c.Visible.ChangedArrows}, nil) {
				shown[line] = true
			}
			out = append(out, c.deltaLines(Delta{AddedArrows: d.AddedArrows, RemovedArrows: d.RemovedArrows, ChangedArrows: d.ChangedArrows}, shown)...)
		}
	}
	return out
}

// deltaLines names each change once, by reader label, with the counts and
// actions that make it a change. The structured delta keeps every concrete
// occurrence; a line never repeats one, so a report grows with the chart,
// not with the TLC graph beneath it.
func (c *Comparison) deltaLines(d Delta, skip map[string]bool) []string {
	var out []string
	add := func(line string) {
		if !skip[line] {
			out = append(out, line)
		}
	}
	for _, s := range d.AddedStates {
		add("Added state " + strconv.Quote(s.Label) + ": " + stateCounts(s) + ".")
	}
	for _, s := range d.RemovedStates {
		add("Removed state " + strconv.Quote(s.Label) + ": " + stateCounts(s) + ".")
	}
	for _, x := range d.ChangedStates {
		add("Changed state " + strconv.Quote(x.After.Label) + ": " + stateCounts(x.Before) + " before, " + stateCounts(x.After) + " after.")
	}
	for _, a := range d.AddedArrows {
		add("Added arrow " + c.arrowName(a) + ": " + arrowCounts(a) + ".")
	}
	for _, a := range d.RemovedArrows {
		add("Removed arrow " + c.arrowName(a) + ": " + arrowCounts(a) + ".")
	}
	for _, x := range d.ChangedArrows {
		add("Changed arrow " + c.arrowName(x.After) + ": " + arrowCounts(x.Before) + " before, " + arrowCounts(x.After) + " after.")
	}
	return out
}

func stateCounts(s State) string {
	out := fmt.Sprintf("%d concrete states, %d initial", len(s.Members), len(s.InitialMembers))
	if len(s.ViolatedRules) > 0 {
		out += ", violates " + strings.Join(s.ViolatedRules, ", ")
	}
	if len(s.UnavailableActions) > 0 {
		out += ", unavailable " + strings.Join(s.UnavailableActions, ", ")
	}
	return out
}

func arrowCounts(a Arrow) string {
	counts := map[string]int{}
	for _, action := range arrowActions(a) {
		counts[action]++
	}
	var actions []string
	for _, action := range slices.Sorted(maps.Keys(counts)) {
		actions = append(actions, fmt.Sprintf("%s x%d", action, counts[action]))
	}
	out := fmt.Sprintf("%d steps (%s)", len(a.Members), strings.Join(actions, ", "))
	if len(a.HidingRules) > 0 {
		out += ", hidden by " + strings.Join(a.HidingRules, ", ")
	}
	return out
}

func (c *Comparison) arrowName(a Arrow) string {
	label := func(id string) string {
		for _, chart := range []*Result{c.Candidate.Chart, c.Baseline.Chart} {
			if chart == nil {
				continue
			}
			for _, s := range chart.States {
				if s.ID == id {
					return strconv.Quote(s.Label)
				}
			}
		}
		return id
	}
	return label(a.From) + " to " + label(a.To) + " on " + strconv.Quote(a.Label)
}

func (c *Comparison) Markdown() string {
	var b strings.Builder
	for _, line := range c.reportLines() {
		literal := TableLiteral(line)
		// These fixed prose prefixes make issue hashes literal within the
		// paragraph. Keep issue references readable in saved Markdown; source
		// newlines and other markup remain escaped.
		if strings.HasPrefix(line, "Baseline: ") || strings.HasPrefix(line, "Candidate: ") {
			literal = strings.ReplaceAll(literal, "&#35;", "#")
		}
		b.WriteString(literal + "\n\n")
	}
	if c.Candidate.Chart != nil {
		b.WriteString("Complete candidate chart:\n\n" + c.Candidate.Chart.Markdown())
	}
	if c.Unavailable == nil {
		b.WriteString(c.changeMermaid())
	}
	return b.String()
}
