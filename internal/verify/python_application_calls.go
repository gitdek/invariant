package verify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/project"
)

// PythonApplicationCall is a completed call inside an explored application
// entry, in completion order. Arguments are copied on entry, by parameter
// name, and the result on return; neither holds application objects.
type PythonApplicationCall struct {
	Target       string         `json:"target"`
	SourceSHA256 string         `json:"source_sha256"`
	Arguments    map[string]any `json:"arguments"`
	Result       any            `json:"result"`
}

func (c *PythonApplicationCall) UnmarshalJSON(b []byte) error {
	if _, err := pythonApplicationJSONFields(b, true, "target", "source_sha256", "arguments", "result"); err != nil {
		return fmt.Errorf("observed call: %w", err)
	}
	type call PythonApplicationCall
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode((*call)(c)); err != nil {
		return err
	}
	if c.Arguments == nil || !pythonApplicationValue(c.Arguments, true) || !pythonApplicationValue(c.Result, true) {
		return fmt.Errorf("observed call arguments and result must be portable JSON values")
	}
	for name := range c.Arguments {
		if !pythonApplicationEnv.MatchString(name) {
			return fmt.Errorf("observed call arguments must be bound by parameter name")
		}
	}
	return nil
}

func pythonApplicationObservedTarget(inv *project.ComponentInventory, target string) bool {
	return slices.ContainsFunc(inv.Checks, func(c project.ResolvedComponentCheck) bool {
		return len(c.Conformance) > 0 && slices.Contains(c.Observe, target)
	})
}

func pythonApplicationObservedDeclaration(p *project.Project, root string, c project.Component) error {
	if c.ID == "" || c.Language != "python" || strings.Contains(c.Symbol, ".") {
		return fmt.Errorf("observe requires a Python module function: %s", c.ID)
	}
	if _, err := pythonApplicationTargetSources(p, root, c); err != nil {
		return fmt.Errorf("observed function %s: %w", c.ID, err)
	}
	return nil
}

func (s *PythonApplicationSession) observedDeclarations(target string) []project.Component {
	var out []project.Component
	if s.inventory != nil {
		for _, check := range s.inventory.Checks {
			if check.Target == target {
				for _, id := range check.Observe {
					out = append(out, s.declarations[id])
				}
			}
		}
	}
	return out
}

func pythonApplicationValidateCalls(row PythonApplicationObservation, declarations []project.Component, explored bool) error {
	if row.Calls == nil {
		return nil
	}
	if !explored || row.Kind != "transition" || row.Operation == "release" || row.Entry == "" || row.Replay == nil || row.Called == nil {
		return fmt.Errorf("observed calls require an explored application entry attempt")
	}
	for _, call := range row.Calls {
		if !slices.ContainsFunc(declarations, func(c project.Component) bool {
			return c.ID == call.Target && c.SourceSHA256 == call.SourceSHA256
		}) {
			return fmt.Errorf("observed call names an undeclared or stale function: %s", call.Target)
		}
		if call.Arguments == nil || !pythonApplicationValue(call.Arguments, true) || !pythonApplicationValue(call.Result, true) {
			return fmt.Errorf("observed call arguments and result must be portable JSON values")
		}
	}
	return nil
}

// pythonApplicationObservedProof derives qualification from the matching
// execution and both conformance records, never from a saved proof status.
func pythonApplicationObservedProof(e *ComponentEvidence, a *PythonApplicationReport, target string, proof *ComponentProofResult) bool {
	if a == nil || proof == nil {
		return false
	}
	index, err := indexComponentInventory(e.Inventory)
	if err != nil {
		return false
	}
	declaration, ok := index.components[target]
	if !ok || !slices.Contains(proof.Inputs, declaration) ||
		!slices.Contains(proof.FileInputs, project.ComponentFile{Path: declaration.Source, SHA256: declaration.SourceSHA256}) {
		return false
	}
	for _, row := range e.Conformance {
		check := index.checks[row.Target]
		if row.Origin != "gate" || row.Status != "tested" || !slices.Contains(check.Observe, target) {
			continue
		}
		var observed []project.Component
		for _, id := range check.Observe {
			observed = append(observed, index.declarations[id])
		}
		for _, c := range a.Conformance {
			if c.Key.Target != row.Target || c.Key.Check != row.Check || c.Key.Kind != "conformance" ||
				row.Result.Invocation == nil || c.Key.ID != row.Result.Invocation.ID ||
				!reflect.DeepEqual(c.Invocation, *row.Result.Invocation) || c.Mode != "bounded-exhaustive" ||
				!c.Result.Exhaustive || !c.passed() || c.OperationalError {
				continue
			}
			qualified, err := index.conformance([]ComponentConformanceResult{row.Result}, c.Bounds)
			if err != nil || len(qualified) != 1 || !qualified[0].TransitionsChecked || !qualified[0].ExhaustiveStates || !qualified[0].EveryStep {
				continue
			}
			for _, x := range a.Executions {
				if x.Key != c.Key || !reflect.DeepEqual(x.Declared, c.Invocation) ||
					!x.Success || !x.Complete || x.ExitCode != 0 || x.ObservationMode != "explored" || x.Exploration == nil || len(x.Actual.Overlays) != 0 ||
					!reflect.DeepEqual(x.Provenance.Files, row.Result.FileInputs) ||
					componentFileReferences(index.files, x.Provenance.Files) != nil ||
					!slices.Contains(x.Provenance.Files, project.ComponentFile{Path: declaration.Source, SHA256: declaration.SourceSHA256}) {
					continue
				}
				if c.Result.Runs != x.Exploration.Runs || c.Result.States != x.Exploration.States ||
					c.Result.Tried.Attempts != x.Exploration.Attempts || c.Result.Tried.States != x.Exploration.States {
					continue
				}
				if pythonApplicationCallCoverage(x, observed, index.declarations, target) {
					return true
				}
			}
		}
	}
	return false
}

func pythonApplicationCallCoverage(x PythonApplicationExecutionResult, observed []project.Component, declarations map[string]project.Component, target string) bool {
	// Reconstruct the full observation protocol, including continuity,
	// replayed calls and refusal declarations, for decoded receipts too.
	steps := slices.Clone(x.Exploration.Steps)
	for i := range steps {
		steps[i].Entry = ""
	}
	raw, err := json.Marshal(struct {
		Type  string                             `json:"type"`
		Steps []PythonApplicationExplorationStep `json:"steps"`
	}{"exploration", steps})
	explorer := &pythonApplicationExplorer{}
	if err != nil || explorer.declare(raw) != nil {
		return false
	}
	files := map[string]string{}
	for _, f := range x.Provenance.Files {
		files[f.Path] = f.SHA256
	}
	observations := newPythonApplicationObservations(x.Key, x.Actual.Sampling, files, observed, explorer)
	worker, driver := declarations[x.Key.Target], declarations[x.Key.Check]
	accepted, refused := false, false
	for _, row := range x.Observations {
		// Observation JSON has always omitted an empty argument list. Its
		// absence on a decoded attempt means the zero-argument step; the
		// declared step still checks that no required arguments are missing.
		if row.Kind == "transition" || row.Kind == "omitted" {
			row.Args = append([]any{}, row.Args...)
		}
		if observations.observe(row, worker, driver) != nil {
			return false
		}
		if row.Replay == nil || *row.Replay {
			continue
		}
		for _, call := range row.Calls {
			if call.Target == target {
				accepted = accepted || row.Outcome == "accepted"
				refused = refused || row.Outcome == "refused"
			}
		}
	}
	stats, err := observations.finish()
	return err == nil && accepted && refused && pythonApplicationKey(stats) == pythonApplicationKey(x.Exploration)
}
