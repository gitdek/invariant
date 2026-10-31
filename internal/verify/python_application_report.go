package verify

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tlc"
)

// PythonApplicationReport records actual executions and their independent TLC
// checks. A successful process is not itself conformance evidence.
type PythonApplicationReport struct {
	ModelError  string                             `json:"model_error,omitempty"`
	Executions  []PythonApplicationExecutionResult `json:"executions"`
	Conformance []PythonApplicationConformance     `json:"conformance"`
}

type PythonApplicationConformance struct {
	Key              PythonApplicationInvocationKey      `json:"key"`
	Invocation       project.ResolvedComponentInvocation `json:"invocation"`
	Result           conformance.Result                  `json:"result"`
	Model            ComponentConformanceModel           `json:"model"`
	Bounds           map[string]string                   `json:"bounds"`
	BoundsSources    []project.ComponentFile             `json:"bounds_sources"`
	Tool             *ComponentProofTool                 `json:"tool,omitempty"`
	Checks           []PythonApplicationModelCheck       `json:"checks,omitempty"`
	OperationalError bool                                `json:"operational_error,omitempty"`
}

type PythonApplicationModelCheck struct {
	Module      string     `json:"module"`
	Batch       string     `json:"batch"`
	InputSHA256 string     `json:"input_sha256"`
	Config      tlc.Config `json:"config"`
	Result      tlc.Result `json:"result"`
	Error       string     `json:"error,omitempty"`
}

func (a *PythonApplicationReport) tested() bool {
	for _, c := range a.Conformance {
		if c.Result.Passed {
			return true
		}
	}
	return false
}

// Preserve exact integers even when reports are read back through JSON.
func (o *PythonApplicationObservation) UnmarshalJSON(b []byte) error {
	type observation PythonApplicationObservation
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	return d.Decode((*observation)(o))
}

func portablePythonApplication(a *PythonApplicationReport) *PythonApplicationReport {
	b, _ := json.Marshal(a)
	var out PythonApplicationReport
	_ = json.Unmarshal(b, &out)
	for i := range out.Executions {
		x := &out.Executions[i]
		x.Actual.Image = ""
		x.Fingerprint, x.Provenance.Fingerprint = "", ""
	}
	return &out
}

func pythonApplicationExecutionFingerprint(x PythonApplicationExecutionResult) string {
	a := portablePythonApplication(&PythonApplicationReport{Executions: []PythonApplicationExecutionResult{x}})
	// Process output is diagnostic, not the authenticated evidence stream.
	a.Executions[0].Diagnostics = nil
	return pythonApplicationFingerprint(a.Executions[0])
}

func (r *Report) attributePythonApplication(inv *project.ComponentInventory) error {
	var runtime []ComponentRuntimeResult
	var conform []ComponentConformanceResult
	declarations := map[string]project.Component{}
	for _, c := range append(slices.Clone(inv.Components), inv.Provenance.Declarations...) {
		declarations[c.ID] = c
	}
	for _, x := range r.Application.Executions {
		inputs := []project.Component{declarations[x.Key.Target], declarations[x.Key.Check]}
		outcome := "failed"
		if x.Success {
			outcome = "passed"
		}
		v := x.Declared
		if x.Key.Kind == "test" {
			row := ComponentRuntimeResult{Target: x.Key.Target, Check: x.Key.Check, Invocation: &v, Inputs: inputs,
				FileInputs: x.Provenance.Files, Outcome: outcome, Diagnostics: x.Diagnostics,
				Runner: &ComponentProofTool{Name: "Python unittest", Version: r.Toolchain.PythonImage, Pins: map[string]string{"runner_sha256": x.Provenance.RunnerSHA256, "observer_sha256": x.Provenance.ObserverSHA256}}}
			for _, t := range x.Tests {
				if t.Check != x.Key.Check {
					continue
				}
				result := "failed"
				if t.Outcome == "passed" {
					result = "passed"
				} else if t.Outcome == "skipped" || t.Outcome == "not_run" {
					result = "skipped"
				}
				row.Tests = append(row.Tests, ComponentRuntimeTestResult{Check: t.Check, Exercised: t.Exercised, Outcome: result, Diagnostics: t.Diagnostics})
			}
			runtime = append(runtime, row)
			continue
		}
		for _, c := range r.Application.Conformance {
			if c.Key != x.Key {
				continue
			}
			outcome = "failed"
			if c.Result.Passed {
				outcome = "passed"
			}
			row := ComponentConformanceResult{Target: x.Key.Target, Check: x.Key.Check, Invocation: &v, Inputs: inputs,
				FileInputs: x.Provenance.Files, Runner: c.Tool, Mode: "sampled", Outcome: outcome,
				Runs: int64(c.Result.Runs), Steps: int64(c.Result.Steps), States: int64(c.Result.States), Transitions: int64(c.Result.Transitions), ModelStates: c.Result.ModelStates,
				Diagnostics: slices.Clone(x.Diagnostics)}
			if c.Result.Message != "" {
				row.Diagnostics = append(row.Diagnostics, c.Result.Message)
			}
			if c.Result.Passed {
				row.Checked = []string{x.Key.Check}
			}
			model := ComponentConformanceModel{}
			bounds := ComponentConformanceBounds{Values: maps.Clone(c.Bounds)}
			for _, f := range v.Inputs {
				if slices.Contains(c.Model.Inputs, f) {
					model.Inputs = append(model.Inputs, f)
				}
				if f.Path == c.Model.Module {
					model.Module = f.Path
				}
				if slices.Contains(c.BoundsSources, f) {
					bounds.Sources = append(bounds.Sources, f)
				}
			}
			if model.Module != "" {
				row.Model = &model
			}
			row.Bounds = &bounds
			for _, o := range x.Observations {
				if o.Kind != "transition" {
					continue
				}
				from, to := map[string]string{}, map[string]string{}
				for k, v := range o.Before {
					from[k], _ = conformance.Encode(v)
				}
				for k, v := range o.After {
					to[k], _ = conformance.Encode(v)
				}
				row.Observations = append(row.Observations, ComponentConformanceObservation{Run: int64(o.Run), Step: int64(o.Sequence), Action: o.Entry, From: from, To: to})
			}
			conform = append(conform, row)
		}
	}
	if err := r.AttributeComponentConformance(inv, conform); err != nil {
		return err
	}
	if err := r.AttributeComponentRuntime(inv, runtime); err != nil {
		return err
	}
	var assumptions []ComponentAssumptionRecord
	for _, check := range inv.Checks {
		for _, a := range check.Assumptions {
			assumptions = append(assumptions, ComponentAssumptionRecord{Target: check.Target, Input: declarations[check.Target], Assumption: a})
		}
	}
	if err := r.AttributeComponentAssumptions(inv, assumptions); err != nil {
		return err
	}
	for i := range r.Components.Conformance {
		r.Components.Conformance[i].Origin = "gate"
	}
	for i := range r.Components.Runtime {
		r.Components.Runtime[i].Origin = "gate"
	}
	return nil
}
