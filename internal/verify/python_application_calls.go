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
// Caller is the staged file whose code made the call: the nearest frame
// beneath it that runs application code. A call counts toward a proof only
// when that file is one of the application's own sources, so a call a
// driver, a helper beside it or a callback of theirs makes shows nothing
// about the application.
type PythonApplicationCall struct {
	Target       string         `json:"target"`
	SourceSHA256 string         `json:"source_sha256"`
	Caller       string         `json:"caller,omitempty"`
	Arguments    map[string]any `json:"arguments"`
	Result       any            `json:"result"`
}

func (c *PythonApplicationCall) UnmarshalJSON(b []byte) error {
	// The collector names every call's caller. A record with none, as one
	// written before callers were recorded has, counts toward no proof.
	if _, err := pythonApplicationJSONFields(b, true, "target", "source_sha256", "caller", "arguments", "result"); err != nil {
		if _, err := pythonApplicationJSONFields(b, true, "target", "source_sha256", "arguments", "result"); err != nil {
			return fmt.Errorf("observed call: %w", err)
		}
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

// pythonApplicationOwnCall says whether the application's own code made a
// recorded call: its caller is one of the manifest's existing sources, or
// lies under one, and isn't the driver.
func pythonApplicationOwnCall(call PythonApplicationCall, existing []string, driver string) bool {
	if call.Caller == "" || call.Caller == driver {
		return false
	}
	return slices.ContainsFunc(existing, func(name string) bool {
		return call.Caller == name || strings.HasPrefix(call.Caller, name+"/")
	})
}

func pythonApplicationValidateCalls(row PythonApplicationObservation, declarations []project.Component, files map[string]string, explored bool) error {
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
		// A caller is a file the run staged, or nothing when no application
		// code made the call.
		if _, staged := files[call.Caller]; call.Caller != "" && (!staged || !strings.HasSuffix(call.Caller, ".py")) {
			return fmt.Errorf("observed call names a caller the run didn't stage: %s", call.Caller)
		}
	}
	return nil
}

// pythonApplicationObservedProof derives qualification from the matching
// execution and both conformance records, never from a saved proof status.
// When the proof doesn't qualify, missing says what it lacks.
func pythonApplicationObservedProof(e *ComponentEvidence, a *PythonApplicationReport, target string, proof *ComponentProofResult) (qualified bool, missing string) {
	if a == nil || proof == nil {
		return false, "the session's application report with its proof"
	}
	index, err := indexComponentInventory(e.Inventory)
	if err != nil {
		return false, "a consistent component inventory"
	}
	declaration, ok := index.components[target]
	if !ok || !slices.Contains(proof.Inputs, declaration) ||
		!slices.Contains(proof.FileInputs, project.ComponentFile{Path: declaration.Source, SHA256: declaration.SourceSHA256}) {
		return false, "a proof made from the bytes of the declaration the exploration observed"
	}
	missing = "a complete exploration, in this session, with passing transition, state and attempted-step conformance against the proof's source bytes"
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
				accepted, refused, valid := pythonApplicationCallCoverage(x, observed, index.declarations, check.Existing, target)
				switch {
				case !valid:
				case accepted && refused:
					return true, ""
				case accepted:
					missing = "a completed call by the application's own code on a declared-refused attempt: such calls were seen only on accepted attempts"
				case refused:
					missing = "a completed call by the application's own code on an accepted attempt: such calls were seen only on declared-refused attempts"
				default:
					missing = "any completed call by the application's own code inside an explored entry: a call a driver, a helper outside the application's existing sources, or a callback of theirs makes doesn't count"
				}
			}
		}
	}
	return false, missing
}

// pythonApplicationCallCoverage says whether the application's own code
// completed a call of target on an accepted attempt, and on a declared-
// refused one, that weren't replays. valid is false when the execution's
// rows don't hold up as the observation protocol requires.
func pythonApplicationCallCoverage(x PythonApplicationExecutionResult, observed []project.Component, declarations map[string]project.Component, existing []string, target string) (accepted, refused, valid bool) {
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
		return false, false, false
	}
	files := map[string]string{}
	for _, f := range x.Provenance.Files {
		files[f.Path] = f.SHA256
	}
	observations := newPythonApplicationObservations(x.Key, x.Actual.Sampling, files, observed, explorer)
	worker, driver := declarations[x.Key.Target], declarations[x.Key.Check]
	for _, row := range x.Observations {
		// Observation JSON has always omitted an empty argument list. Its
		// absence on a decoded attempt means the zero-argument step; the
		// declared step still checks that no required arguments are missing.
		if row.Kind == "transition" || row.Kind == "omitted" {
			row.Args = append([]any{}, row.Args...)
		}
		if observations.observe(row, worker, driver) != nil {
			return false, false, false
		}
		if row.Replay == nil || *row.Replay {
			continue
		}
		for _, call := range row.Calls {
			// Only a call the application's own code made shows that the
			// application runs the function.
			if call.Target == target && pythonApplicationOwnCall(call, existing, driver.Source) {
				accepted = accepted || row.Outcome == "accepted"
				refused = refused || row.Outcome == "refused"
			}
		}
	}
	stats, err := observations.finish()
	return accepted, refused, err == nil && pythonApplicationKey(stats) == pythonApplicationKey(x.Exploration)
}
