// Package statechart computes reader charts from complete bounded TLC evidence.
// A view can name states and arrows and request hiding, but supplies no facts.
package statechart

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tlc"
)

const MaxStates = 500
const ArrowTarget = 20

// Input is trusted evidence, supplied independently of the view. Invariants
// contains one boolean per locked invariant and concrete state, from TLC.
type Input struct {
	Graph      tlc.GraphResult
	Lock       project.Lock
	Invariants map[string]map[string]bool
	Temporal   []tlc.TraceFile
}

type Result struct {
	Preview Preview  `json:"preview,omitzero"`
	Version int      `json:"version"`
	States  []State  `json:"states"`
	Initial []string `json:"initial"`
	Arrows  []Arrow  `json:"arrows"`
	Report  Report   `json:"report"`
}

// Preview identifies an unratified alternative, separately from prior choices.
// The value field leaves ordinary charts and their metadata unchanged.
type Preview struct {
	Fork    string            `json:"fork"`
	Option  string            `json:"option"`
	Choices map[string]string `json:"choices,omitempty"`
}

func (p Preview) IsZero() bool { return p.Fork == "" && p.Option == "" && len(p.Choices) == 0 }

func (p Preview) Description() string {
	if p.IsZero() {
		return ""
	}
	return "Unratified alternative " + p.Fork + "/" + p.Option + "; model-only evidence with candidate rules."
}

type State struct {
	ID                 string   `json:"id"`
	Label              string   `json:"label"`
	Members            []string `json:"members"`
	InitialMembers     []string `json:"initial_members"`
	ViolatedRules      []string `json:"violated_rules"`
	UnavailableActions []string `json:"unavailable_actions"`
}

type Arrow struct {
	From        string          `json:"from"`
	To          string          `json:"to"`
	Label       string          `json:"label"`
	Members     []tlc.GraphStep `json:"members"`
	Count       int             `json:"count"`
	HidingRules []string        `json:"hiding_rules"`
}

// Refusal never accompanies a partial result. States are concrete identities;
// Steps retains occurrences, and Rules names the view rules needing repair.
type Refusal struct {
	Code          string
	Message       string
	States        []string
	Steps         []tlc.GraphStep
	Rules         []string
	Actual, Limit int
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("statechart %s: %s; states=%v steps=%v view rules=%v actual=%d limit=%d",
		r.Code, r.Message, r.States, r.Steps, r.Rules, r.Actual, r.Limit)
}

func refuse(code, message string) *Refusal {
	return &Refusal{Code: code, Message: message}
}

// Compute validates the whole view and evidence, then partitions before hiding.
// It does no I/O and returns detached, canonically ordered chart data.
func Compute(ctx context.Context, in Input, data []byte) (result *Result, err error) {
	defer func() {
		if cause := context.Cause(ctx); cause != nil {
			result, err = nil, cause
		}
	}()
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	v, err := parseView(ctx, data)
	if err != nil {
		return nil, err
	}
	states, err := validateGraph(ctx, in.Graph)
	if err != nil {
		return nil, err
	}
	invariants, err := validateLock(in.Graph, in.Lock)
	if err != nil {
		return nil, err
	}
	if err := validateEvidence(in, states, invariants); err != nil {
		return nil, err
	}
	initial := map[string]bool{}
	for _, id := range in.Graph.Graph.Initial {
		initial[id] = true
	}
	owners := map[string]string{}
	groups := map[string]*State{}
	for _, id := range slices.Sorted(maps.Keys(states)) {
		s := states[id]
		rule, err := match(ctx, v.state, environment{vars: s.Values})
		if err != nil {
			err.(*Refusal).States = []string{id}
			return nil, err
		}
		if rule == nil {
			return nil, &Refusal{Code: "unmapped_state", Message: "no state rule matched", States: []string{id}}
		}
		violated := []string{}
		for _, name := range invariants {
			if !in.Invariants[id][name] {
				violated = append(violated, name)
			}
		}
		// Encoding the pair is injective, independent of member order and graph
		// size. A display label is not itself a chart identity.
		key, _ := json.Marshal([]any{rule.label, violated})
		chartID := "state-" + base64.RawURLEncoding.EncodeToString(key)
		g := groups[chartID]
		if g == nil {
			g = &State{ID: chartID, Label: rule.label, Members: []string{}, InitialMembers: []string{},
				ViolatedRules: violated, UnavailableActions: []string{}}
			groups[chartID] = g
		}
		g.Members = append(g.Members, id)
		if initial[id] {
			g.InitialMembers = append(g.InitialMembers, id)
		}
		owners[id] = chartID
	}

	type arrowKey struct{ from, label, to string }
	type bucket struct {
		arrow  Arrow
		hidden int
	}
	buckets := map[arrowKey]*bucket{}
	actions := map[string]bool{}
	enabled := map[string]map[string]bool{}
	steps := slices.Clone(in.Graph.Graph.Steps)
	sortSteps(steps)
	for _, step := range steps {
		from, to := owners[step.From], owners[step.To]
		actions[step.Action] = true
		if enabled[from] == nil {
			enabled[from] = map[string]bool{}
		}
		enabled[from][step.Action] = true
		rule, err := match(ctx, v.step, environment{from: states[step.From].Values, to: states[step.To].Values, action: step.Action})
		if err != nil {
			r := err.(*Refusal)
			r.States, r.Steps = unique([]string{step.From, step.To}), []tlc.GraphStep{step}
			return nil, err
		}
		if rule == nil {
			return nil, &Refusal{Code: "unmapped_step", Message: "no step rule matched", Steps: []tlc.GraphStep{step}, States: unique([]string{step.From, step.To})}
		}
		key := arrowKey{from, rule.label, to}
		b := buckets[key]
		if b == nil {
			b = &bucket{arrow: Arrow{From: from, To: to, Label: rule.label, Members: []tlc.GraphStep{}, HidingRules: []string{}}}
			buckets[key] = b
		}
		b.arrow.Members = append(b.arrow.Members, step)
		b.arrow.Count++
		if rule.hide {
			b.hidden++
			b.arrow.HidingRules = append(b.arrow.HidingRules, rule.id)
		}
	}
	result = &Result{Version: 1, States: []State{}, Initial: []string{}, Arrows: []Arrow{}}
	for _, id := range slices.Sorted(maps.Keys(groups)) {
		s := groups[id]
		for _, action := range slices.Sorted(maps.Keys(actions)) {
			if !enabled[id][action] {
				s.UnavailableActions = append(s.UnavailableActions, action)
			}
		}
		result.States = append(result.States, *s)
		if len(s.InitialMembers) > 0 {
			result.Initial = append(result.Initial, id)
		}
	}
	hidden := []Arrow{}
	partial := refuse("partial_arrow", "an arrow must have uniform visibility across all concrete occurrences")
	for _, b := range buckets {
		b.arrow.HidingRules = unique(b.arrow.HidingRules)
		if b.hidden > 0 && b.hidden != b.arrow.Count {
			partial.Steps = append(partial.Steps, b.arrow.Members...)
			partial.Rules = append(partial.Rules, b.arrow.HidingRules...)
			for _, s := range b.arrow.Members {
				partial.States = append(partial.States, s.From, s.To)
			}
		}
		if b.hidden > 0 {
			hidden = append(hidden, b.arrow)
		} else {
			result.Arrows = append(result.Arrows, b.arrow)
		}
	}
	if len(partial.Steps) > 0 {
		return nil, canonicalRefusal(partial)
	}
	sortArrows(result.Arrows)
	sortArrows(hidden)
	if err := validateHiding(result, hidden); err != nil {
		return nil, err
	}
	result.Report, err = makeReport(in, result, hidden)
	if err != nil {
		return nil, err
	}
	return result, nil
}

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var hash = regexp.MustCompile(`^(sha256:)?[0-9a-f]{64}$`)

func validateGraph(ctx context.Context, g tlc.GraphResult) (map[string]tlc.GraphState, error) {
	if len(g.Graph.States) > MaxStates {
		return nil, &Refusal{Code: "too_large", Message: "reachable graph exceeds supported size", Actual: len(g.Graph.States), Limit: MaxStates}
	}
	bad := func(message string) (map[string]tlc.GraphState, error) { return nil, refuse("invalid_graph", message) }
	if len(g.Graph.States) == 0 || g.StateCount != len(g.Graph.States) || g.StepCount != len(g.Graph.Steps) || g.Run.DistinctStates != int64(g.StateCount) {
		return bad("graph state or step counts disagree")
	}
	if g.Run.Outcome != tlc.Passed || g.Run.ExitCode != 0 || g.Run.Depth < 1 || g.Run.Version == "" ||
		g.Run.Invariant != "" || g.Run.Property != "" || len(g.Run.Trace) != 0 || g.Run.Loop != 0 || g.Run.Stutters {
		return bad("graph requires a completed successful TLC run")
	}
	if len(g.Graph.Initial) == 0 || g.Run.StatesGenerated < int64(g.StepCount)+int64(len(g.Graph.Initial)) {
		return bad("missing initials or inconsistent generated count")
	}
	if !identifier.MatchString(g.Module) || strings.TrimSpace(g.Tool.Image) == "" || !hash.MatchString(g.Tool.JarSHA256) {
		return bad("missing or malformed graph provenance")
	}
	states := map[string]tlc.GraphState{}
	var variables []string
	for _, s := range g.Graph.States {
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		if strings.TrimSpace(s.ID) == "" || strings.TrimSpace(s.Label) == "" || len(s.Values) == 0 {
			return bad("state requires identity, label and variable values")
		}
		if _, exists := states[s.ID]; exists {
			return nil, &Refusal{Code: "invalid_graph", Message: "duplicate concrete identity", States: []string{s.ID}}
		}
		names := slices.Sorted(maps.Keys(s.Values))
		if variables == nil {
			variables = names
		} else if !slices.Equal(variables, names) {
			return nil, &Refusal{Code: "invalid_graph", Message: "inconsistent state variable set", States: []string{s.ID}}
		}
		for _, name := range names {
			if !identifier.MatchString(name) || strings.TrimSpace(s.Values[name]) == "" {
				return bad("invalid variable name or empty TLA+ value")
			}
		}
		states[s.ID] = s
	}
	initial := map[string]bool{}
	for _, id := range g.Graph.Initial {
		if _, ok := states[id]; !ok || initial[id] {
			return nil, &Refusal{Code: "invalid_graph", Message: "unknown or duplicate initial identity", States: []string{id}}
		}
		initial[id] = true
	}
	next := map[string][]string{}
	for _, s := range g.Graph.Steps {
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		_, from := states[s.From]
		_, to := states[s.To]
		if !from || !to || strings.TrimSpace(s.Action) == "" {
			return nil, &Refusal{Code: "invalid_graph", Message: "unknown endpoint or empty action", Steps: []tlc.GraphStep{s}}
		}
		next[s.From] = append(next[s.From], s.To)
	}
	reached := reachable(g.Graph.Initial, next)
	unreachable := []string{}
	for id := range states {
		if !reached[id] {
			unreachable = append(unreachable, id)
		}
	}
	if len(unreachable) > 0 {
		return nil, canonicalRefusal(&Refusal{Code: "invalid_graph", Message: "unreachable concrete states", States: unreachable})
	}
	return states, nil
}

func validateLock(g tlc.GraphResult, lock project.Lock) ([]string, error) {
	bad := func(message string) ([]string, error) { return nil, refuse("invalid_evidence", message) }
	if !maps.Equal(g.Config.Constants, lock.Bounds) {
		return bad("graph constants differ from locked bounds")
	}
	kinds := map[string]string{}
	var invariants []string
	specs := 0
	for _, s := range lock.Statements {
		if !identifier.MatchString(s.Name) || kinds[s.Name] != "" || strings.TrimSpace(s.Says) == "" || !hash.MatchString(s.SHA256) {
			return bad("invalid or duplicate locked statement " + s.Name)
		}
		switch s.Kind {
		case project.Spec:
			specs++
			if s.Name != g.Config.Specification {
				return bad("graph specification differs from lock")
			}
		case project.Invariant:
			invariants = append(invariants, s.Name)
		case project.Property, project.Witness, project.Bug, project.Fairness:
		default:
			return bad("unknown locked statement kind " + s.Kind)
		}
		kinds[s.Name] = s.Kind
	}
	if specs != 1 {
		return bad("lock requires exactly one specification")
	}
	for _, checks := range []struct {
		names []string
		kind  string
	}{{g.Config.Invariants, project.Invariant}, {g.Config.Properties, project.Property}} {
		seen := map[string]bool{}
		for _, name := range checks.names {
			if kinds[name] != checks.kind || seen[name] {
				return bad("unknown, duplicate or mistyped configured check " + name)
			}
			seen[name] = true
		}
	}
	slices.Sort(invariants)
	return invariants, nil
}

func validateEvidence(in Input, states map[string]tlc.GraphState, invariants []string) error {
	if len(in.Invariants) != len(states) {
		return refuse("invalid_evidence", "invariant matrix must cover exactly the graph states")
	}
	for _, id := range slices.Sorted(maps.Keys(states)) {
		row, ok := in.Invariants[id]
		if !ok || len(row) != len(invariants) {
			return &Refusal{Code: "invalid_evidence", Message: "incomplete or foreign invariant evidence", States: []string{id}}
		}
		for _, name := range invariants {
			holds, ok := row[name]
			if !ok || (!holds && slices.Contains(in.Graph.Config.Invariants, name)) {
				return &Refusal{Code: "invalid_evidence", Message: "missing invariant or contradiction with successful TLC check: " + name, States: []string{id}}
			}
		}
	}
	properties := map[string]bool{}
	for _, s := range in.Lock.Statements {
		if s.Kind == project.Property {
			properties[s.Name] = true
		}
	}
	for _, trace := range in.Temporal {
		if !properties[trace.Check] || (trace.Violated != "" && trace.Violated != trace.Check) ||
			trace.Module != in.Graph.Module || trace.TLC != in.Graph.Run.Version || len(trace.States) == 0 ||
			trace.Loop < 0 || trace.Loop > len(trace.States) || (trace.Loop == 0 && !trace.Stutters) || (trace.Loop > 0 && trace.Stutters) ||
			slices.Contains(in.Graph.Config.Properties, trace.Check) {
			return refuse("invalid_evidence", "invalid or contradictory temporal counterexample for "+trace.Check)
		}
		for i, s := range trace.States {
			if s.Index != i+1 || s.Action == "" || len(s.TLA) == 0 {
				return refuse("invalid_evidence", "incomplete temporal trace for "+trace.Check)
			}
		}
	}
	return nil
}

func validateHiding(result *Result, hidden []Arrow) error {
	next := map[string][]string{}
	for _, a := range result.Arrows {
		next[a.From] = append(next[a.From], a.To)
	}
	reached := reachable(result.Initial, next)
	r := refuse("unreachable_chart", "hiding disconnects chart states from every initial")
	for _, s := range result.States {
		if !reached[s.ID] {
			r.States = append(r.States, s.Members...)
		}
	}
	if len(r.States) > 0 {
		for _, a := range hidden {
			if !reached[a.To] {
				r.Steps = append(r.Steps, a.Members...)
				r.Rules = append(r.Rules, a.HidingRules...)
			}
		}
		return canonicalRefusal(r)
	}
	r = refuse("empty_outgoing", "hiding removes a chart state's last outgoing arrow")
	empty := map[string]bool{}
	for _, a := range hidden {
		if len(next[a.From]) == 0 {
			empty[a.From] = true
			r.Steps = append(r.Steps, a.Members...)
			r.Rules = append(r.Rules, a.HidingRules...)
		}
	}
	for _, s := range result.States {
		if empty[s.ID] {
			r.States = append(r.States, s.Members...)
		}
	}
	if len(r.States) > 0 {
		return canonicalRefusal(r)
	}
	return nil
}

func reachable(initial []string, next map[string][]string) map[string]bool {
	seen := map[string]bool{}
	queue := slices.Clone(initial)
	for _, id := range initial {
		seen[id] = true
	}
	for i := 0; i < len(queue); i++ {
		for _, id := range next[queue[i]] {
			if !seen[id] {
				seen[id] = true
				queue = append(queue, id)
			}
		}
	}
	return seen
}

func unique(s []string) []string {
	slices.Sort(s)
	return slices.Compact(s)
}

func sortSteps(s []tlc.GraphStep) {
	slices.SortFunc(s, func(a, b tlc.GraphStep) int {
		return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.Action, b.Action), cmp.Compare(a.To, b.To))
	})
}

func sortArrows(a []Arrow) {
	slices.SortFunc(a, func(a, b Arrow) int {
		return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.Label, b.Label), cmp.Compare(a.To, b.To))
	})
}

func canonicalRefusal(r *Refusal) *Refusal {
	r.States, r.Rules = unique(r.States), unique(r.Rules)
	sortSteps(r.Steps)
	return r
}
