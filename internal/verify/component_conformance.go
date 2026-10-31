package verify

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/gitdek/invariant/internal/project"
)

// ComponentConformanceResult records a supplied run for an exact selected
// target, check and invocation. Actual outcomes and snapshots are retained even
// when incomplete. Attribution runs no tools and discovers no omitted inputs.
type ComponentConformanceResult struct {
	Target       string                               `json:"target"`
	Check        string                               `json:"check"`
	Invocation   *project.ResolvedComponentInvocation `json:"invocation,omitempty"`
	Runner       *ComponentProofTool                  `json:"runner,omitempty"`
	Inputs       []project.Component                  `json:"inputs"`
	FileInputs   []project.ComponentFile              `json:"file_inputs"`
	Model        *ComponentConformanceModel           `json:"model,omitempty"`
	Bounds       *ComponentConformanceBounds          `json:"bounds,omitempty"`
	Mode         string                               `json:"mode"`
	Outcome      string                               `json:"outcome"`
	Checked      []string                             `json:"checked"`
	Runs         int64                                `json:"runs"`
	Steps        int64                                `json:"steps"`
	States       int64                                `json:"states"`
	Transitions  int64                                `json:"transitions"`
	ModelStates  int64                                `json:"model_states"`
	Observations []ComponentConformanceObservation    `json:"observations,omitempty"`
	Attempts     *ComponentAttemptResult              `json:"attempts,omitempty"`
	Diagnostics  []string                             `json:"diagnostics,omitempty"`
}

// ComponentConformanceModel identifies the model source and supplied includes.
// Module is a source path, not the behavior name in Report.Design.Spec.
type ComponentConformanceModel struct {
	Module string                  `json:"module"`
	Inputs []project.ComponentFile `json:"inputs"`
}

type ComponentConformanceBounds struct {
	Values  map[string]string       `json:"values"`
	Sources []project.ComponentFile `json:"sources"`
}

type ComponentConformanceObservation struct {
	Run    int64             `json:"run"`
	Step   int64             `json:"step"`
	Action string            `json:"action"`
	Args   []string          `json:"args,omitempty"`
	From   map[string]string `json:"from"`
	To     map[string]string `json:"to"`
}

// ComponentAttemptResult describes steps tried in observed states separately
// from checked transitions and exhaustive state coverage.
type ComponentAttemptResult struct {
	Outcome      string                            `json:"outcome"`
	Attempts     int64                             `json:"attempts"`
	States       int64                             `json:"states"`
	Observations []ComponentConformanceObservation `json:"observations,omitempty"`
	Diagnostics  []string                          `json:"diagnostics,omitempty"`
}

type ComponentConformanceEvidence struct {
	Origin             string                     `json:"origin,omitempty"`
	Target             string                     `json:"target"`
	Check              string                     `json:"check"`
	Status             string                     `json:"status"`
	TransitionsChecked bool                       `json:"transitions_checked"`
	ExhaustiveStates   bool                       `json:"exhaustive_states"`
	EveryStep          bool                       `json:"every_step"`
	Result             ComponentConformanceResult `json:"result"`
}

// AttributeComponentConformance replaces or clears only supplied conformance,
// revalidating retained proofs, assumptions and runtime against inv and the
// report's bounds before committing.
// Neither successful runs nor attempted steps establish proof or gate success.
func (r *Report) AttributeComponentConformance(inv *project.ComponentInventory, results []ComponentConformanceResult) error {
	evidence, index, err := r.componentReplacement(inv)
	if err != nil {
		return err
	}
	if evidence.Conformance, err = index.conformance(results, r.Bounds); err != nil {
		return err
	}
	if evidence.Proofs, err = index.retainedProofs(evidence.Inventory, r.Components); err != nil {
		return err
	}
	if evidence.Assumptions, err = index.retainedAssumptions(r.Components); err != nil {
		return err
	}
	if evidence.Runtime, err = index.retainedRuntime(r.Components, r.Bounds); err != nil {
		return err
	}
	if evidence.ContractTests, err = index.retainedContractTests(r.Components); err != nil {
		return err
	}
	r.commitComponentEvidence(index, evidence)
	return nil
}

func (index *componentIndex) conformance(results []ComponentConformanceResult, bounds map[string]string) ([]ComponentConformanceEvidence, error) {
	var rows []ComponentConformanceEvidence
	seen := map[[3]string]bool{}
	for _, supplied := range results {
		result := copyComponentConformanceResult(supplied)
		check, ok := index.checks[result.Target]
		if !ok || !slices.Contains(check.Conformance, result.Check) {
			return nil, fmt.Errorf("component conformance: target %q has no selected check %q", result.Target, result.Check)
		}
		v := result.Invocation
		if v == nil || v.Kind != "conformance" || v.Check != result.Check || !slices.ContainsFunc(check.Invocations, func(selected project.ResolvedComponentInvocation) bool {
			return sameComponentInvocation(*v, selected)
		}) {
			return nil, fmt.Errorf("component conformance: invocation does not match a selected check for %q", result.Target)
		}
		key := [3]string{result.Target, result.Check, v.ID}
		if seen[key] {
			return nil, fmt.Errorf("component conformance: duplicate result for %q, %q, %q", key[0], key[1], key[2])
		}
		seen[key] = true
		if err := componentProofReferences(index.declarations, result.Checked, "checked conformance"); err != nil {
			return nil, err
		}
		for _, id := range result.Checked {
			if !slices.Contains(check.Conformance, id) {
				return nil, fmt.Errorf("component conformance: unselected checked declaration %q for %q", id, result.Target)
			}
		}
		inputs := map[string]bool{}
		for _, input := range result.Inputs {
			c, ok := index.declarations[input.ID]
			if !ok || input != c || inputs[input.ID] {
				return nil, fmt.Errorf("component conformance: invalid, stale or duplicate input %q", input.ID)
			}
			inputs[input.ID] = true
		}
		if err := componentFileReferences(index.files, result.FileInputs); err != nil {
			return nil, err
		}
		declared, err := componentFileSet(v.Inputs)
		if err != nil {
			return nil, err
		}
		if result.Model != nil {
			if result.Model.Module != "" {
				if _, ok := declared[result.Model.Module]; !ok {
					return nil, fmt.Errorf("component conformance: model module %q is not a declared invocation input", result.Model.Module)
				}
			}
			if err := componentFileReferences(declared, result.Model.Inputs); err != nil {
				return nil, err
			}
		}
		if result.Bounds != nil {
			if !maps.Equal(result.Bounds.Values, bounds) {
				return nil, fmt.Errorf("component conformance: supplied bounds do not match report bounds for %q", result.Target)
			}
			if err := componentFileReferences(declared, result.Bounds.Sources); err != nil {
				return nil, err
			}
		}
		if result.Mode != "" && result.Mode != "sampled" && result.Mode != "bounded-exhaustive" {
			return nil, fmt.Errorf("component conformance: unknown mode %q", result.Mode)
		}
		if !componentConformanceOutcome(result.Outcome) || result.Runs < 0 || result.Steps < 0 || result.States < 0 || result.Transitions < 0 || result.ModelStates < 0 {
			return nil, fmt.Errorf("component conformance: invalid outcome or counts for %q", result.Target)
		}
		if a := result.Attempts; a != nil && (!componentConformanceOutcome(a.Outcome) || a.Attempts < 0 || a.States < 0) {
			return nil, fmt.Errorf("component conformance: invalid attempt outcome or counts for %q", result.Target)
		}
		row := ComponentConformanceEvidence{Origin: "supplied", Target: result.Target, Check: result.Check, Status: "unchecked", Result: result}
		if result.coversConformance(check) {
			row.Status = "tested"
			row.TransitionsChecked = result.Transitions > 0
			a := result.Attempts
			row.EveryStep = a != nil && a.Outcome == "passed" && a.Attempts > 0 && a.States > 0
			row.ExhaustiveStates = result.Mode == "bounded-exhaustive" && result.States == result.ModelStates && (a == nil || row.EveryStep)
		}
		rows = append(rows, row)
	}
	return copyComponentConformance(rows), nil
}

func componentConformanceOutcome(outcome string) bool {
	switch outcome {
	case "", "passed", "failed", "timed_out", "skipped", "empty":
		return true
	default:
		return false
	}
}

func (r *ComponentConformanceResult) coversConformance(check project.ResolvedComponentCheck) bool {
	if r.Outcome != "passed" || r.Mode == "" || r.Runs <= 0 || r.Steps <= 0 || r.States <= 0 ||
		r.Runner == nil || strings.TrimSpace(r.Runner.Name) == "" || strings.TrimSpace(r.Runner.Version) == "" ||
		!slices.Contains(r.Checked, r.Check) || r.Model == nil || r.Model.Module == "" || r.Bounds == nil || len(r.Bounds.Sources) == 0 {
		return false
	}
	if !slices.ContainsFunc(r.Model.Inputs, func(file project.ComponentFile) bool { return file.Path == r.Model.Module }) {
		return false
	}
	inputs := map[string]project.Component{}
	for _, input := range r.Inputs {
		inputs[input.ID] = input
	}
	files := map[string]bool{}
	for _, file := range r.FileInputs {
		files[file.Path] = true
	}
	ids := []string{r.Target, r.Check}
	ids = append(ids, r.Checked...)
	if check.Proof != nil {
		ids = append(ids, check.Proof.Contracts...)
	}
	for _, id := range ids {
		input, ok := inputs[id]
		if !ok || !files[input.Source] {
			return false
		}
	}
	for _, group := range [][]project.ComponentFile{r.Invocation.Inputs, r.Model.Inputs, r.Bounds.Sources} {
		for _, file := range group {
			if !files[file.Path] {
				return false
			}
		}
	}
	return true
}

func (index *componentIndex) retainConformance(old project.ComponentInventory, row ComponentConformanceEvidence) error {
	check, ok := index.checks[row.Target]
	prior := slices.IndexFunc(old.Checks, func(c project.ResolvedComponentCheck) bool { return c.Target == row.Target })
	if row.Target != row.Result.Target || row.Check != row.Result.Check || !ok || prior < 0 ||
		(check.Proof == nil) != (old.Checks[prior].Proof == nil) {
		return fmt.Errorf("component conformance: retained selection changed for %q", row.Target)
	}
	ids := []string{row.Target, row.Check}
	ids = append(ids, row.Result.Checked...)
	if check.Proof != nil {
		if !slices.Equal(check.Proof.Contracts, old.Checks[prior].Proof.Contracts) {
			return fmt.Errorf("component conformance: retained contracts changed for %q", row.Target)
		}
		ids = append(ids, check.Proof.Contracts...)
	}
	// Missing actual snapshots never discard the original selection's anchors.
	declarations := slices.Clone(old.Components)
	if old.Provenance != nil {
		declarations = append(declarations, old.Provenance.Declarations...)
	}
	for _, id := range ids {
		pos := slices.IndexFunc(declarations, func(c project.Component) bool { return c.ID == id })
		c, ok := index.declarations[id]
		if pos < 0 || !ok || declarations[pos] != c {
			return fmt.Errorf("component conformance: retained declaration %q changed", id)
		}
	}
	return nil
}

func copyComponentConformanceResult(result ComponentConformanceResult) ComponentConformanceResult {
	result.Inputs = slices.Clone(result.Inputs)
	sort.Slice(result.Inputs, func(i, j int) bool { return result.Inputs[i].ID < result.Inputs[j].ID })
	result.FileInputs = copyComponentFiles(result.FileInputs)
	result.Checked = slices.Clone(result.Checked)
	sort.Strings(result.Checked)
	if result.Invocation != nil {
		invocation := copyComponentInvocation(*result.Invocation)
		result.Invocation = &invocation
	}
	if result.Runner != nil {
		runner := *result.Runner
		runner.Pins = maps.Clone(runner.Pins)
		result.Runner = &runner
	}
	if result.Model != nil {
		model := *result.Model
		model.Inputs = copyComponentFiles(model.Inputs)
		result.Model = &model
	}
	if result.Bounds != nil {
		bounds := *result.Bounds
		bounds.Values = maps.Clone(bounds.Values)
		bounds.Sources = copyComponentFiles(bounds.Sources)
		result.Bounds = &bounds
	}
	result.Observations = copyComponentConformanceObservations(result.Observations)
	if result.Attempts != nil {
		attempts := *result.Attempts
		attempts.Observations = copyComponentConformanceObservations(attempts.Observations)
		attempts.Diagnostics = slices.Clone(attempts.Diagnostics)
		result.Attempts = &attempts
	}
	result.Diagnostics = slices.Clone(result.Diagnostics)
	return result
}

func copyComponentConformanceObservations(observations []ComponentConformanceObservation) []ComponentConformanceObservation {
	observations = slices.Clone(observations)
	for i := range observations {
		observations[i].Args = slices.Clone(observations[i].Args)
		observations[i].From = maps.Clone(observations[i].From)
		observations[i].To = maps.Clone(observations[i].To)
	}
	return observations
}

func copyComponentConformance(rows []ComponentConformanceEvidence) []ComponentConformanceEvidence {
	rows = slices.Clone(rows)
	for i := range rows {
		rows[i].Result = copyComponentConformanceResult(rows[i].Result)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		if a.Check != b.Check {
			return a.Check < b.Check
		}
		var aid, bid string
		if a.Result.Invocation != nil {
			aid = a.Result.Invocation.ID
		}
		if b.Result.Invocation != nil {
			bid = b.Result.Invocation.ID
		}
		return aid < bid
	})
	return rows
}
