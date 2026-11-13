package verify

// These tests start at the post-collection execution report boundary. They do
// not replace the collector, authenticate fabricated output or claim a real TLC
// run. The two exported test-only bridges let the external receipt tests call
// the existing internal paths without adding a production API or an import cycle.
// Existing module encoding, recording storage/transport, component attribution
// and receipt literal tests keep their contracts and need no edits.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/project"
)

func AttributePythonModuleReceipt837(r *Report, inv *project.ComponentInventory) error {
	return r.attributePythonApplication(inv)
}

func RecordingPythonModuleReceipt837(x PythonApplicationExecutionResult) ([]byte, error) {
	var out bytes.Buffer
	err := writePythonApplicationRecording(context.Background(), &out, x)
	return out.Bytes(), err
}

// PythonModuleReceiptFixture837 supplies complete provenance and independent
// transition/attempt results. Values enter only through raw observation state;
// no component From/To map is populated by the fixture.
func PythonModuleReceiptFixture837(explored bool, states []map[string]any) (*Report, *project.ComponentInventory) {
	file := func(path string) project.ComponentFile {
		return project.ComponentFile{Path: path, SHA256: "sha256:" + strings.Repeat("a", 64)}
	}
	source, driver := file("app/tasks.py"), file("drivers/drive.py")
	model, lock := file("verification/Tasks.tla"), file("verification/ratified.lock")
	decl := func(pkg, symbol string, f project.ComponentFile) project.Component {
		return project.Component{ID: "python:" + pkg + "#" + symbol + "@" + f.Path,
			Language: "python", Package: pkg, Symbol: symbol, Source: f.Path, SourceSHA256: f.SHA256}
	}
	target, neighbor, check := decl("app.tasks", "advance", source), decl("app.tasks", "neighbor", source), decl("drivers.drive", "drive", driver)
	files := []project.ComponentFile{source, driver, model, lock}
	invocation := project.ResolvedComponentInvocation{ID: "status-observation", Kind: "conformance", Check: check.ID,
		Command: "python", Args: []string{driver.Path}, WorkingDir: ".", Inputs: files}
	inv := &project.ComponentInventory{Mode: "explicit", Scope: []string{"app"}, Files: []project.ComponentFile{source},
		Components: []project.Component{target, neighbor},
		Provenance: &project.ComponentProvenance{Declarations: []project.Component{check}, Files: files},
		Checks:     []project.ResolvedComponentCheck{{Target: target.ID, Conformance: []string{check.ID}, Invocations: []project.ResolvedComponentInvocation{invocation}}}}
	key := PythonApplicationInvocationKey{Target: target.ID, Check: check.ID, Kind: "conformance", ID: invocation.ID}
	x := PythonApplicationExecutionResult{Key: key, Declared: invocation, Success: true, Complete: true, ObservationMode: "sampled",
		Provenance: PythonApplicationProvenance{Files: files, RunnerSHA256: "runner-837", ObserverSHA256: "observer-837"}}
	base := func(run, sequence int) PythonApplicationObservation {
		return PythonApplicationObservation{Form: "module", Run: run, Instance: run, Sequence: sequence, Key: key,
			Target: target.ID, Driver: check.ID, Symbol: target.Symbol, Entry: "python:app.tasks#submit@app/tasks.py",
			SourceSHA256: source.SHA256, DriverSHA256: driver.SHA256, EntrySHA256: source.SHA256,
			Readback: map[string]string{"status": "tasks[*].status"}}
	}
	initial := func(run int) {
		o := base(run, 0)
		o.Kind, o.State, o.Entry = "initial", states[0], target.ID
		x.Observations = append(x.Observations, o)
	}
	args := []any{
		map[string]any{"$seq": []any{json.Number("9007199254740993"), "arg"}},
		map[string]any{"$set": []any{"b", "a", "a"}},
		map[string]any{"$mv": "p1"},
		map[string]any{"$fn": []any{[]any{json.Number("1"), "one"}}},
		true, json.Number("9007199254740993"),
	}
	transition := func(run, index int, replay bool) {
		o := base(run, index+1)
		o.Kind, o.Before, o.After = "transition", states[index], states[min(index+1, len(states)-1)]
		if explored {
			o.Step, o.Args, o.Replay = "Advance", args, &replay
			called := true
			o.Called = &called
		}
		x.Observations = append(x.Observations, o)
	}
	steps, runs := len(states)-1, 1
	if explored {
		x.ObservationMode = "explored"
		steps, runs = len(states), len(states)
		// Each run replays to one state and tries Advance there; the last
		// attempt stutters. This covers every reached state exactly once.
		for i := range states {
			initial(i + 1)
			for j := 0; j <= i; j++ {
				transition(i+1, j, j < i)
			}
		}
		x.Exploration = &PythonApplicationExploration{Runs: runs, States: len(states), Depth: len(states), Attempts: steps,
			Steps: []PythonApplicationExplorationStep{{Name: "Advance", Args: [][]any{args}, Entry: "python:app.tasks#submit@app/tasks.py"}}}
	} else {
		initial(1)
		for i := 0; i < steps; i++ {
			transition(1, i, false)
		}
	}
	c := PythonApplicationConformance{Key: key, Invocation: invocation,
		Result: conformance.Result{Passed: true, Runs: runs, Steps: steps, States: len(states), Transitions: len(states) - 1, ModelStates: int64(len(states))},
		Model:  ComponentConformanceModel{Module: model.Path, Inputs: []project.ComponentFile{model}},
		Bounds: map[string]string{"Limit": "4"}, BoundsSources: []project.ComponentFile{lock},
		Tool: &ComponentProofTool{Name: "TLC", Version: "fixture-837"}}
	if explored {
		c.Mode = "bounded-exhaustive"
		c.Result.Exhaustive = true
		c.Result.Tried = &conformance.Tried{Passed: true, Attempts: steps, States: len(states)}
	}
	r := &Report{Passed: true, Bounds: map[string]string{"Limit": "4"},
		Application: &PythonApplicationReport{Executions: []PythonApplicationExecutionResult{x}, Conformance: []PythonApplicationConformance{c}}}
	return r, inv
}

func moduleReceipt837JSON(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPythonModuleComponentStateErrors837(t *testing.T) {
	for _, explored := range []bool{false, true} {
		for _, form := range []string{"module", ""} {
			bad := []struct {
				name  string
				value any
			}{
				{"null", nil}, {"fraction", json.Number("1.5")}, {"exponent", json.Number("1e2")},
				{"bad record field", map[string]any{"bad-key": true}},
			}
			if form == "module" {
				bad = append(bad, struct {
					name  string
					value any
				}{"nested null", []any{map[string]any{"item": nil}}})
				for _, tag := range []string{"$seq", "$set", "$fn", "$mv"} {
					var value any = []any{}
					if tag == "$mv" {
						value = "p1"
					}
					bad = append(bad, struct {
						name  string
						value any
					}{"application tag " + tag, map[string]any{tag: value}})
				}
			} else {
				bad = append(bad, struct {
					name  string
					value any
				}{"bare array stays ambiguous", []any{"queued"}})
			}
			for _, side := range []string{"before", "after"} {
				for _, invalid := range bad {
					t.Run(fmt.Sprintf("explored=%t/form=%s/%s/%s", explored, form, side, invalid.name), func(t *testing.T) {
						r, inv := PythonModuleReceiptFixture837(explored, []map[string]any{{"status": "idle"}, {"status": "queued"}})
						for i := range r.Application.Executions[0].Observations {
							r.Application.Executions[0].Observations[i].Form = form
						}
						if err := r.attributePythonApplication(inv); err != nil {
							t.Fatalf("valid baseline: %v", err)
						}
						components := moduleReceipt837JSON(t, r.Components)
						fingerprint, passed := r.Fingerprint, r.Passed
						o := &r.Application.Executions[0].Observations[1]
						o.Run, o.Sequence = 17, 23
						if side == "before" {
							o.Before = map[string]any{"status": invalid.value}
						} else {
							o.After = map[string]any{"status": invalid.value}
						}
						raw := moduleReceipt837JSON(t, r.Application)
						err := r.attributePythonApplication(inv)
						if err == nil {
							t.Errorf("invalid %s state silently became component evidence", side)
						} else {
							for _, want := range []string{o.Key.Target, o.Key.Check, o.Key.ID, "run 17", "step 23", side, "status"} {
								if !strings.Contains(err.Error(), want) {
									t.Errorf("diagnostic %q omits %q", err, want)
								}
							}
						}
						if got := moduleReceipt837JSON(t, r.Components); got != components {
							t.Error("failed conversion committed partial component evidence")
						}
						if r.Fingerprint != fingerprint || r.Passed != passed {
							t.Error("failed conversion changed fingerprint or gate outcome")
						}
						if moduleReceipt837JSON(t, r.Application) != raw {
							t.Error("conversion changed raw execution evidence")
						}
					})
				}
			}
		}
	}
}
