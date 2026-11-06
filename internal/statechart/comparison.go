package statechart

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
)

// ComparisonSnapshot contains sources and one checked result, never an older
// comparison. Keeping this flat prevents revision histories growing recursively.
type ComparisonSnapshot struct {
	Kind       string            `json:"kind"`
	Name       string            `json:"name"`
	Module     string            `json:"module"`
	Model      string            `json:"model"`
	Includes   map[string]string `json:"includes,omitempty"`
	View       string            `json:"view"`
	Lock       project.Lock      `json:"lock"`
	Chart      *Result           `json:"chart,omitempty"`
	Limitation *Limitation       `json:"limitation,omitempty"`
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

type ComparisonDelta struct {
	AddedStates   []State       `json:"added_states"`
	RemovedStates []State       `json:"removed_states"`
	ChangedStates []StateChange `json:"changed_states"`
	AddedArrows   []Arrow       `json:"added_arrows"`
	RemovedArrows []Arrow       `json:"removed_arrows"`
	ChangedArrows []ArrowChange `json:"changed_arrows"`
}

type ComparisonDiagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Comparison struct {
	Version           int                   `json:"version"`
	Fingerprint       string                `json:"fingerprint"`
	Baseline          ComparisonSnapshot    `json:"baseline"`
	Candidate         ComparisonSnapshot    `json:"candidate"`
	BaselineIdentity  ComparisonIdentity    `json:"baseline_identity"`
	CandidateIdentity ComparisonIdentity    `json:"candidate_identity"`
	Visible           ComparisonDelta       `json:"visible"`
	BeforeHiding      ComparisonDelta       `json:"before_hiding"`
	ViewChanged       bool                  `json:"view_changed"`
	BoundsChanged     bool                  `json:"bounds_changed"`
	ModelChanged      bool                  `json:"model_changed"`
	Scope             string                `json:"scope,omitempty"`
	Unavailable       *ComparisonDiagnostic `json:"unavailable,omitempty"`
}

func comparisonHash(b []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(b)) }

func comparisonJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// Compare compares canonical chart identities and labeled endpoint triples.
// Equal topology says nothing about behavioral equivalence or unbounded runs.
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
		BaselineIdentity: a.Identity(), CandidateIdentity: b.Identity()}
	c.ViewChanged = a.View != b.View
	c.BoundsChanged = !maps.Equal(a.Lock.Bounds, b.Lock.Bounds)
	c.ModelChanged = a.Module != b.Module || a.Model != b.Model || !maps.Equal(a.Includes, b.Includes)
	if c.BoundsChanged {
		c.Scope = "Checking bounds differ: additions and removals describe the displayed scope at each side's own bounds. They do not establish a behavior change at equal scope."
	}
	if a.Limitation != nil || b.Limitation != nil {
		c.Unavailable = &ComparisonDiagnostic{Code: "too_large", Message: "Comparison unavailable: at least one complete graph exceeds 500 reachable states. No empty graph or delta is substituted; bounds are unchanged."}
	} else {
		c.Visible = compareCharts(a.Chart.States, b.Chart.States, a.Chart.Arrows, b.Chart.Arrows)
		c.BeforeHiding = compareCharts(a.Chart.States, b.Chart.States, beforeHiding(a.Chart), beforeHiding(b.Chart))
	}
	c.Fingerprint = comparisonHash(comparisonJSON(c))
	return c, nil
}

func (s ComparisonSnapshot) Identity() ComparisonIdentity {
	i := ComparisonIdentity{ModelSHA256: comparisonHash([]byte(s.Model)), ViewSHA256: comparisonHash([]byte(s.View)),
		LockSHA256: comparisonHash(comparisonJSON(s.Lock)), ProposalHash: project.ProposalHash(s.Lock.Bounds, s.Lock.Statements), Includes: map[string]string{}}
	for name, text := range s.Includes {
		i.Includes[name] = comparisonHash([]byte(text))
	}
	return i
}

func canonicalSnapshot(s ComparisonSnapshot) (ComparisonSnapshot, error) {
	if (s.Kind != "proposal" && s.Kind != "ratification") || strings.TrimSpace(s.Name) == "" {
		return s, fmt.Errorf("requires a named proposal or ratification")
	}
	if (s.Chart == nil) == (s.Limitation == nil) {
		return s, fmt.Errorf("requires exactly one checked chart or explicit limitation")
	}
	if name, err := tla.ModuleName(s.Model); err != nil || name != s.Module {
		return s, fmt.Errorf("model module disagrees with snapshot")
	}
	if _, err := parseView(context.Background(), []byte(s.View)); err != nil {
		return s, err
	}
	for name := range s.Includes {
		if !strings.HasSuffix(name, ".tla") || !identifier.MatchString(strings.TrimSuffix(name, ".tla")) || name == s.Module+".tla" {
			return s, fmt.Errorf("invalid sibling include %q", name)
		}
	}
	for _, st := range s.Lock.Statements {
		h, err := tla.PinHash(s.Model, st.Name, project.Model)
		if err != nil || h != st.SHA256 {
			return s, fmt.Errorf("source pin differs for %s", st.Name)
		}
	}
	data, err := json.Marshal(s)
	if err != nil {
		return s, err
	}
	var out ComparisonSnapshot
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(&out); err != nil {
		return s, err
	}
	var source Source
	if c := out.Chart; c != nil {
		if !c.Preview.IsZero() {
			return s, fmt.Errorf("an alternative preview is not a selected proposal")
		}
		if err := validateSVG(c); err != nil {
			return s, err
		}
		if err := validateSVGText(reflect.ValueOf(*c)); err != nil {
			return s, err
		}
		if !bytes.Equal(comparisonJSON(c.Report.Rules), comparisonJSON(out.Lock.Statements)) {
			return s, fmt.Errorf("chart rules differ from captured lock")
		}
		source = c.Report.Source
		slices.SortFunc(c.States, func(a, b State) int { return strings.Compare(a.ID, b.ID) })
		slices.Sort(c.Initial)
		for i := range c.States {
			st := &c.States[i]
			for _, list := range [][]string{st.Members, st.InitialMembers, st.ViolatedRules, st.UnavailableActions} {
				slices.Sort(list)
			}
		}
		for _, arrows := range [][]Arrow{c.Arrows, c.Report.Hidden} {
			sortArrows(arrows)
			for i := range arrows {
				sortSteps(arrows[i].Members)
				slices.Sort(arrows[i].HidingRules)
			}
		}
	} else {
		l := out.Limitation
		if l.Code != "too_large" || l.Limit != MaxStates || l.Actual <= l.Limit {
			return s, fmt.Errorf("invalid graph size limitation")
		}
		source = l.Source
	}
	if source.Module != out.Module || !maps.Equal(source.Config.Constants, out.Lock.Bounds) || source.TLC == "" || source.Tool.Image == "" || !hash.MatchString(source.Tool.JarSHA256) {
		return s, fmt.Errorf("chart source, bounds or tool provenance differs from snapshot")
	}
	if _, err := validateLock(tlc.GraphResult{Config: source.Config}, out.Lock); err != nil {
		return s, err
	}
	return out, nil
}

type comparisonArrowKey struct{ from, label, to string }

func arrowKey(a Arrow) comparisonArrowKey { return comparisonArrowKey{a.From, a.Label, a.To} }

func beforeHiding(c *Result) []Arrow {
	arrows := append(slices.Clone(c.Arrows), c.Report.Hidden...)
	for i := range arrows {
		arrows[i].HidingRules = nil
	}
	sortArrows(arrows)
	return arrows
}

func compareCharts(oldStates, newStates []State, oldArrows, newArrows []Arrow) ComparisonDelta {
	d := ComparisonDelta{}
	oldS, newS := map[string]State{}, map[string]State{}
	for _, s := range oldStates {
		oldS[s.ID] = s
	}
	for _, s := range newStates {
		newS[s.ID] = s
		if old, ok := oldS[s.ID]; !ok {
			d.AddedStates = append(d.AddedStates, s)
		} else if !reflect.DeepEqual(old, s) {
			d.ChangedStates = append(d.ChangedStates, StateChange{old, s})
		}
	}
	for _, s := range oldStates {
		if _, ok := newS[s.ID]; !ok {
			d.RemovedStates = append(d.RemovedStates, s)
		}
	}
	oldA, newA := map[comparisonArrowKey]Arrow{}, map[comparisonArrowKey]Arrow{}
	for _, a := range oldArrows {
		oldA[arrowKey(a)] = a
	}
	for _, a := range newArrows {
		newA[arrowKey(a)] = a
		if old, ok := oldA[arrowKey(a)]; !ok {
			d.AddedArrows = append(d.AddedArrows, a)
		} else if !reflect.DeepEqual(old, a) {
			d.ChangedArrows = append(d.ChangedArrows, ArrowChange{old, a})
		}
	}
	for _, a := range oldArrows {
		if _, ok := newA[arrowKey(a)]; !ok {
			d.RemovedArrows = append(d.RemovedArrows, a)
		}
	}
	return d
}

// Validate recomputes identities and deltas from the retained source snapshots.
// It detects damaged saved evidence; execution authority belongs to the caller.
func (c *Comparison) Validate() error {
	if c == nil {
		return fmt.Errorf("comparison is missing; check against the captured baseline")
	}
	want, err := Compare(c.Baseline, c.Candidate)
	if err != nil {
		return err
	}
	if !bytes.Equal(comparisonJSON(c), comparisonJSON(want)) {
		return fmt.Errorf("comparison is stale or damaged: source identities, delta or fingerprint changed; check the captured baseline again")
	}
	return nil
}

// Text is the literal comparison report for callers that provide their own
// escaping, such as HTML templates. It contains no Markdown or SVG syntax.
func (c *Comparison) Text() string {
	if err := c.Validate(); err != nil {
		return "Comparison unavailable: " + err.Error()
	}
	return strings.Join(append(c.description(), c.deltaDescription()...), "\n")
}

func (c *Comparison) description() []string {
	lines := []string{"Proposal comparison", "Bounded, view-qualified explanation. Equal charts do not establish behavioral equivalence. This adds no proof claim.",
		"Legend: added states and arrows are green; removed states and arrows are red and dashed. Changed memberships and occurrence multisets are retained below."}
	if c.ViewChanged {
		lines = append(lines, "View change: exact view bytes changed; labels or hiding rules may differ. Displayed differences may result from the view.")
	}
	if c.ModelChanged {
		lines = append(lines, "Model-source change: exact model or include bytes changed; chart equality cannot establish behavioral equivalence.")
	}
	if c.Scope != "" {
		lines = append(lines, c.Scope)
	}
	if c.Unavailable != nil {
		lines = append(lines, c.Unavailable.Message)
	}
	for i, s := range []ComparisonSnapshot{c.Baseline, c.Candidate} {
		name, id := "Baseline", c.BaselineIdentity
		if i == 1 {
			name, id = "Candidate", c.CandidateIdentity
		}
		lines = append(lines, name+": "+s.Name, "Kind: "+s.Kind+"; module: "+s.Module,
			"Model: "+id.ModelSHA256, "View: "+id.ViewSHA256, "Lock: "+id.LockSHA256, "Proposal: "+id.ProposalHash)
		for _, n := range slices.Sorted(maps.Keys(id.Includes)) {
			lines = append(lines, "Include "+n+": "+id.Includes[n])
		}
		lines = append(lines, name+" checked bounds:")
		if len(s.Lock.Bounds) == 0 {
			lines = append(lines, "No constants supplied.")
		}
		for _, n := range slices.Sorted(maps.Keys(s.Lock.Bounds)) {
			lines = append(lines, n+" = "+s.Lock.Bounds[n])
		}
		if l := s.Limitation; l != nil {
			lines = append(lines, fmt.Sprintf("The chart is not drawn (too_large): TLC reached %d states at these bounds, more than the %d states charts are drawn for. Mappings and hiding were not evaluated.", l.Actual, l.Limit))
		}
		var source Source
		if s.Chart != nil {
			source = s.Chart.Report.Source
		} else if s.Limitation != nil {
			source = s.Limitation.Source
		}
		lines = append(lines, "TLC: "+source.TLC, "Java image: "+source.Tool.Image, "TLC jar SHA-256: "+source.Tool.JarSHA256)
	}
	return lines
}

func (c *Comparison) deltaDescription() []string {
	if c.Unavailable != nil {
		return nil
	}
	var lines []string
	for i, d := range []ComparisonDelta{c.Visible, c.BeforeHiding} {
		name := "Visible changes"
		if i == 1 {
			name = "Before hiding changes (visibility and hiding rule IDs ignored)"
		}
		lines = append(lines, name, fmt.Sprintf("States: %d added, %d removed, %d changed. Arrows: %d added, %d removed, %d changed.", len(d.AddedStates), len(d.RemovedStates), len(d.ChangedStates), len(d.AddedArrows), len(d.RemovedArrows), len(d.ChangedArrows)))
		state := func(how string, s State) {
			lines = append(lines, how+" state: "+s.Label, "Identity: "+s.ID, "Concrete members: "+svgList(s.Members),
				"Initial members: "+svgList(s.InitialMembers), "Violated invariants: "+svgList(s.ViolatedRules), "Unavailable actions: "+svgList(s.UnavailableActions))
		}
		arrow := func(how string, a Arrow) {
			lines = append(lines, how+" arrow: "+a.Label, "From: "+a.From, "To: "+a.To,
				fmt.Sprintf("Concrete occurrence count: %d", a.Count), "Hiding rules: "+svgList(a.HidingRules))
			for j, step := range a.Members {
				lines = append(lines, fmt.Sprintf("Occurrence %d: ", j+1)+step.From+" -- "+step.Action+" --> "+step.To)
			}
		}
		for _, s := range d.AddedStates {
			state("Added", s)
		}
		for _, s := range d.RemovedStates {
			state("Removed", s)
		}
		for _, s := range d.ChangedStates {
			state("Changed before", s.Before)
			state("Changed after", s.After)
		}
		for _, a := range d.AddedArrows {
			arrow("Added", a)
		}
		for _, a := range d.RemovedArrows {
			arrow("Removed", a)
		}
		for _, a := range d.ChangedArrows {
			arrow("Changed before", a.Before)
			arrow("Changed after", a.After)
		}
	}
	return lines
}
