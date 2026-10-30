package verify_test

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

// Issue #279 adds Report.AttributeComponentConformance(*project.ComponentInventory,
// []verify.ComponentConformanceResult) error, and ComponentEvidence.Conformance
// []verify.ComponentConformanceEvidence, serialized as optional "conformance".
// The cc279 wire types below define the exported structs' fields and JSON keys:
// cc279Result = ComponentConformanceResult, cc279Model = ComponentConformanceModel,
// cc279Bounds = ComponentConformanceBounds, cc279Observation = ComponentConformanceObservation,
// cc279Attempt = ComponentAttemptResult, cc279Evidence = ComponentConformanceEvidence.
// Runner reuses *ComponentProofTool (opaque name, version and pins).
// Reflection keeps these acceptance tests compilable on the base branch; the
// missing API must fail an assertion rather than a compilation step.
//
// Each result names a selected (target, conformance check, invocation ID), unique
// within the batch; a nonnil complete invocation must match its declaration.
// A missing invocation or unselected reference is an attribution error. Inputs
// are actual declaration snapshots, FileInputs actual file snapshots. Success
// needs snapshots of the target, every selected proof contract, and driver, their
// source files, and every invocation/model/bounds input. All supplied references
// match known snapshots; model and bounds sources must be declared invocation
// inputs. Model.Module names the model source path in Model.Inputs, which also
// names all explicitly supplied includes. Bounds.Values equal Report.Bounds and
// Bounds.Sources identify the supplied ratified-bounds sources. These comparisons
// do not read files, parse models/locks or discover dependencies. Design.Spec is
// a behavior name, never model-source identity. Missing facts may remain visible
// as unchecked; supplied stale, unknown, duplicate or unselected facts are errors.
//
// Checked is the set of selected canonical conformance checks actually exercised
// and checked, and must include Check for successful evidence. Mode is "sampled"
// or "bounded-exhaustive"; Outcome is "passed", "failed", "timed_out", "skipped",
// "empty" or absent; missing mode leaves evidence unchecked. Nonnegative counts,
// declarations and package success alone
// establish nothing. Status "tested" requires passed, nonempty conformance
// (Runs, Steps and States positive), an identified runner (Name and Version), and
// complete matching provenance. Otherwise it is "unchecked". Diagnostics are
// supplied messages, not an alternate outcome; they and observations retain order.
// TransitionsChecked additionally requires positive Transitions. ExhaustiveStates
// additionally requires bounded-exhaustive mode and positive equal States and
// ModelStates, with absent or successful nonempty Attempts. EveryStep requires
// successful nonempty Attempts (passed, positive Attempts and States) as well as
// successful conformance; it describes tried steps in observed states, not proof
// or exhaustive states. Present unsuccessful/empty/missing-outcome attempts block
// exhaustive states, never erase successful transitions. Missing attempts are
// not checked and grant no EveryStep, but alone do not block exhaustive states.
//
// Result preserves supplied mode/outcomes and nested values even when qualified.
// Rows sort by (Target, Check, Result.Invocation.ID). All reference collections
// are sets; args, observations and diagnostics are ordered sequences. All values
// are copied, and Fingerprint canonicalizes sets after decoding too. Replacing
// any kind validates all retained kinds against the new inventory and report
// bounds before committing once. Missing snapshots in incomplete retained results
// do not remove the old inventory's anchors for selected target/contracts/driver,
// invocation and its inputs. All existing proof compatibility rules remain.

type cc279Model struct {
	Module string                  `json:"module"`
	Inputs []project.ComponentFile `json:"inputs"`
}

type cc279Bounds struct {
	Values  map[string]string       `json:"values"`
	Sources []project.ComponentFile `json:"sources"`
}

type cc279Observation struct {
	Run    int64             `json:"run"`
	Step   int64             `json:"step"`
	Action string            `json:"action"`
	Args   []string          `json:"args,omitempty"`
	From   map[string]string `json:"from"`
	To     map[string]string `json:"to"`
}

type cc279Attempt struct {
	Outcome      string             `json:"outcome"`
	Attempts     int64              `json:"attempts"`
	States       int64              `json:"states"`
	Observations []cc279Observation `json:"observations,omitempty"`
	Diagnostics  []string           `json:"diagnostics,omitempty"`
}

type cc279Result struct {
	Target       string                               `json:"target"`
	Check        string                               `json:"check"`
	Invocation   *project.ResolvedComponentInvocation `json:"invocation,omitempty"`
	Runner       *verify.ComponentProofTool           `json:"runner,omitempty"`
	Inputs       []project.Component                  `json:"inputs"`
	FileInputs   []project.ComponentFile              `json:"file_inputs"`
	Model        *cc279Model                          `json:"model,omitempty"`
	Bounds       *cc279Bounds                         `json:"bounds,omitempty"`
	Mode         string                               `json:"mode"`
	Outcome      string                               `json:"outcome"`
	Checked      []string                             `json:"checked"`
	Runs         int64                                `json:"runs"`
	Steps        int64                                `json:"steps"`
	States       int64                                `json:"states"`
	Transitions  int64                                `json:"transitions"`
	ModelStates  int64                                `json:"model_states"`
	Observations []cc279Observation                   `json:"observations,omitempty"`
	Attempts     *cc279Attempt                        `json:"attempts,omitempty"`
	Diagnostics  []string                             `json:"diagnostics,omitempty"`
}

type cc279Evidence struct {
	Target             string      `json:"target"`
	Check              string      `json:"check"`
	Status             string      `json:"status"`
	TransitionsChecked bool        `json:"transitions_checked"`
	ExhaustiveStates   bool        `json:"exhaustive_states"`
	EveryStep          bool        `json:"every_step"`
	Result             cc279Result `json:"result"`
}

func cc279Fixture(t *testing.T) ce274FixtureData {
	t.Helper()
	f := ce274Fixture(t)
	provenance257EditManifest(t, f.Root, func(m map[string]any) {
		step := provenance257ConfigCheck(m, "Step")
		var call map[string]any
		for _, v := range step["invocations"].([]any) {
			if v.(map[string]any)["kind"] == "conformance" {
				call = v.(map[string]any)
			}
		}
		call["inputs"] = []string{"drivers/drive.go", "core/core.go", "core/contracts.go", "core/scoped_driver.go",
			"data/fixture.bin", "data/second.bin", "config/check.json", "go.mod", "go.sum", "drivers/go.mod",
			".invariant/specs/Counter.tla", ".invariant/specs/Include.tla", ".invariant/ratified.lock"}
		repeat := cpClone(t, call)
		repeat["id"] = "repeat"
		other := cpClone(t, call)
		other["check"] = provenance257Ref("core/scoped_driver.go", "DriveScoped")
		step["invocations"] = append(step["invocations"].([]any), repeat, other)
		neighbor := map[string]any{"target": provenance257Ref("neighbors/extra.go", "Step"),
			"conformance": step["conformance"], "invocations": []any{cpClone(t, call)}}
		cfg := m["components"].(map[string]any)
		cfg["checks"] = append(cfg["checks"].([]any), neighbor)
	})
	f.Inv = cpResolve(t, f.Root)
	return f
}

func cc279Invocation(t *testing.T, inv *project.ComponentInventory, target, check, id string) *project.ResolvedComponentInvocation {
	t.Helper()
	for _, v := range ce274Check(t, inv, target).Invocations {
		if v.Kind == "conformance" && v.Check == check && v.ID == id {
			v = cpClone(t, v)
			return &v
		}
	}
	t.Fatalf("fixture lacks selected conformance invocation %s/%s/%s", target, check, id)
	return nil
}

func cc279Good(t *testing.T, f ce274FixtureData, target, check, id string) cc279Result {
	t.Helper()
	if check == "" {
		check = provenance257ID("drivers/drive.go", "Drive")
	}
	if id == "" {
		id = "run"
	}
	inputs := []project.Component{ce274Component(t, f.Inv, target), ce274Component(t, f.Inv, check)}
	cfg := ce274Check(t, f.Inv, target)
	if cfg.Proof != nil {
		for _, c := range cfg.Proof.Contracts {
			if !slices.ContainsFunc(inputs, func(x project.Component) bool { return x.ID == c }) {
				inputs = append(inputs, ce274Component(t, f.Inv, c))
			}
		}
	}
	files := map[string]project.ComponentFile{}
	for _, group := range [][]project.ComponentFile{f.Inv.Files, f.Inv.Provenance.Files, f.Inv.Provenance.Modules} {
		for _, file := range group {
			files[file.Path] = file
		}
	}
	actual := make([]project.ComponentFile, 0, len(files))
	for _, file := range files {
		actual = append(actual, file)
	}
	slices.SortFunc(actual, func(a, b project.ComponentFile) int { return strings.Compare(a.Path, b.Path) })
	slices.SortFunc(inputs, func(a, b project.Component) int { return strings.Compare(a.ID, b.ID) })
	obs := []cc279Observation{{Run: 1, Step: 1, Action: "Advance", Args: []string{"b", "a"}, From: map[string]string{"n": "0", "phase": "initial"}, To: map[string]string{"n": "1", "phase": "ready"}},
		{Run: 1, Step: 2, Action: "Refuse", Args: []string{"a", "b"}, From: map[string]string{"n": "1"}, To: map[string]string{"n": "1"}}}
	return cc279Result{Target: target, Check: check, Invocation: cc279Invocation(t, f.Inv, target, check, id),
		Runner: &verify.ComponentProofTool{Name: "supplied-conformance", Version: "1.2.3", Pins: map[string]string{"image": "runner@sha256:abc", "model_checker": "sha256:def"}},
		Inputs: inputs, FileInputs: actual,
		Model:  &cc279Model{Module: ".invariant/specs/Counter.tla", Inputs: []project.ComponentFile{files[".invariant/specs/Counter.tla"], files[".invariant/specs/Include.tla"]}},
		Bounds: &cc279Bounds{Values: map[string]string{"Limit": "2"}, Sources: []project.ComponentFile{files[".invariant/ratified.lock"]}},
		Mode:   "sampled", Outcome: "passed", Checked: []string{check}, Runs: 1, Steps: 2, States: 2, Transitions: 1, ModelStates: 2,
		Observations: obs, Diagnostics: []string{"supplied observation note", "second note"}}
}

func cc279AttemptGood() *cc279Attempt {
	return &cc279Attempt{Outcome: "passed", Attempts: 4, States: 2,
		Observations: []cc279Observation{{Run: 1, Step: 1, Action: "Advance", Args: []string{"b", "a"}, From: map[string]string{"n": "0"}, To: map[string]string{"n": "1"}},
			{Run: 1, Step: 2, Action: "Refuse", Args: []string{"a", "b"}, From: map[string]string{"n": "1"}, To: map[string]string{"n": "1"}}},
		Diagnostics: []string{"attempt note", "another attempt note"}}
}

func cc279CallArg(t *testing.T, r *verify.Report, inv *project.ComponentInventory, results []cc279Result) (reflect.Value, reflect.Value) {
	t.Helper()
	m := reflect.ValueOf(r).MethodByName("AttributeComponentConformance")
	if !m.IsValid() {
		t.Fatal("missing Report.AttributeComponentConformance")
	}
	if m.Type().NumIn() != 2 || m.Type().In(0) != reflect.TypeOf(inv) || m.Type().In(1).Kind() != reflect.Slice ||
		m.Type().NumOut() != 1 || m.Type().Out(0) != reflect.TypeOf((*error)(nil)).Elem() {
		t.Fatal("AttributeComponentConformance must take inventory and supplied result slice and return error")
	}
	arg := reflect.New(m.Type().In(1))
	if err := json.Unmarshal(cpJSON(t, results), arg.Interface()); err != nil {
		t.Fatal(err)
	}
	return m, arg.Elem()
}

func cc279Call(t *testing.T, r *verify.Report, inv *project.ComponentInventory, results []cc279Result) error {
	t.Helper()
	m, arg := cc279CallArg(t, r, inv, results)
	out := m.Call([]reflect.Value{reflect.ValueOf(inv), arg})[0]
	if out.IsNil() {
		return nil
	}
	return out.Interface().(error)
}

func cc279Rows(t *testing.T, r verify.Report) []cc279Evidence {
	t.Helper()
	var doc struct {
		Components struct {
			Conformance []cc279Evidence `json:"conformance"`
		} `json:"components"`
	}
	if err := json.Unmarshal(cpJSON(t, r), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Components.Conformance
}

func cc279Attach(t *testing.T, f ce274FixtureData, results ...cc279Result) verify.Report {
	t.Helper()
	r := ce274Seed()
	r.Design.Spec = "Spec" // A behavior name, deliberately not Model.Module's source path.
	if err := cc279Call(t, &r, f.Inv, results); err != nil {
		t.Fatal(err)
	}
	return r
}

func cc279Want(t *testing.T, row cc279Evidence, result cc279Result, status string, transitions, states, steps bool) {
	t.Helper()
	if row.Target != result.Target || row.Check != result.Check || row.Status != status || row.TransitionsChecked != transitions || row.ExhaustiveStates != states || row.EveryStep != steps {
		t.Fatalf("wrong conformance qualification: %+v", row)
	}
	if string(cpJSON(t, row.Result)) != string(cpJSON(t, result)) {
		t.Fatalf("supplied result changed: got %s, want %s", cpJSON(t, row.Result), cpJSON(t, result))
	}
}

func TestSuppliedConformanceSeparatesStatesTransitionsAndAttempts(t *testing.T) {
	f := cc279Fixture(t)
	for _, mode := range []string{"sampled", "bounded-exhaustive"} {
		for _, outcome := range []string{"absent", "failed", "timed_out", "skipped", "empty", "", "passed"} {
			for _, counts := range [][2]int64{{2, 2}, {2, 3}, {0, 0}, {2, 0}} {
				t.Run(fmt.Sprintf("%s/attempt=%s/states=%d/model=%d", mode, outcome, counts[0], counts[1]), func(t *testing.T) {
					result := cc279Good(t, f, f.Target, "", "")
					result.Mode, result.States, result.ModelStates = mode, counts[0], counts[1]
					if outcome != "absent" {
						result.Attempts = cc279AttemptGood()
						result.Attempts.Outcome = outcome
						if outcome == "empty" {
							result.Attempts.Attempts, result.Attempts.States = 0, 0
						}
					}
					r := cc279Attach(t, f, result)
					rows := cc279Rows(t, r)
					if len(rows) != 1 {
						t.Fatalf("want one supplied record, got %d", len(rows))
					}
					tested := counts[0] > 0
					status := "unchecked"
					if tested {
						status = "tested"
					}
					states := tested && mode == "bounded-exhaustive" && counts[0] == counts[1] && (outcome == "absent" || outcome == "passed")
					cc279Want(t, rows[0], result, status, tested, states, tested && outcome == "passed")
					ce274Statuses(t, r)
					if r.Fingerprint != verify.Fingerprint(r) {
						t.Fatal("fingerprint not finalized")
					}
				})
			}
		}
	}
	for _, field := range []string{"attempts", "states"} {
		t.Run("passed but empty attempt "+field, func(t *testing.T) {
			result := cc279Good(t, f, f.Target, "", "")
			result.Mode, result.Attempts = "bounded-exhaustive", cc279AttemptGood()
			if field == "attempts" {
				result.Attempts.Attempts = 0
			} else {
				result.Attempts.States = 0
			}
			cc279Want(t, cc279Rows(t, cc279Attach(t, f, result))[0], result, "tested", true, false, false)
		})
	}
	// A run containing only checked refusals is nonempty; it supplies no
	// positive checked-transition count, but can still cover its one state.
	result := cc279Good(t, f, f.Target, "", "")
	result.Mode, result.Transitions, result.States, result.ModelStates = "bounded-exhaustive", 0, 1, 1
	cc279Want(t, cc279Rows(t, cc279Attach(t, f, result))[0], result, "tested", false, true, false)
}

func TestSuppliedConformanceRequiresActualSuccessfulCompleteEvidence(t *testing.T) {
	f := cc279Fixture(t)
	good := cc279Good(t, f, f.Target, "", "")
	good.Mode, good.Attempts = "bounded-exhaustive", cc279AttemptGood()
	mutations := []struct {
		name string
		edit func(*cc279Result)
	}{
		{"missing runner", func(r *cc279Result) { r.Runner = nil }},
		{"missing runner name", func(r *cc279Result) { r.Runner.Name = "" }},
		{"blank runner name", func(r *cc279Result) { r.Runner.Name = " \t" }},
		{"missing runner version", func(r *cc279Result) { r.Runner.Version = "" }},
		{"no exercised checks", func(r *cc279Result) { r.Checked = nil }},
		{"only other selected check exercised", func(r *cc279Result) { r.Checked = []string{provenance257ID("core/scoped_driver.go", "DriveScoped")} }},
		{"no declaration snapshots", func(r *cc279Result) { r.Inputs = nil }},
		{"no actual files", func(r *cc279Result) { r.FileInputs = nil }},
		{"missing model", func(r *cc279Result) { r.Model = nil }},
		{"missing model inputs", func(r *cc279Result) { r.Model.Inputs = nil }},
		{"missing model module", func(r *cc279Result) { r.Model.Module = "" }},
		{"missing model's own snapshot", func(r *cc279Result) {
			r.Model.Inputs = slices.DeleteFunc(r.Model.Inputs, func(f project.ComponentFile) bool { return f.Path == r.Model.Module })
		}},
		{"missing bounds", func(r *cc279Result) { r.Bounds = nil }},
		{"missing bounds sources", func(r *cc279Result) { r.Bounds.Sources = nil }},
		{"missing mode", func(r *cc279Result) { r.Mode = "" }},
		{"no runs", func(r *cc279Result) { r.Runs = 0 }},
		{"no steps", func(r *cc279Result) { r.Steps = 0 }},
		{"no states", func(r *cc279Result) { r.States = 0 }},
	}
	for _, outcome := range []string{"failed", "timed_out", "skipped", "empty", ""} {
		mutations = append(mutations, struct {
			name string
			edit func(*cc279Result)
		}{"outcome=" + outcome, func(r *cc279Result) { r.Outcome = outcome }})
	}
	for _, c := range good.Inputs {
		mutations = append(mutations, struct {
			name string
			edit func(*cc279Result)
		}{"missing declaration " + c.ID, func(r *cc279Result) {
			r.Inputs = slices.DeleteFunc(r.Inputs, func(x project.Component) bool { return x.ID == c.ID })
		}})
	}
	for _, name := range []string{"core/core.go", "core/contracts.go", "drivers/drive.go", "data/fixture.bin", "data/second.bin", "config/check.json", "go.mod", "go.sum", "drivers/go.mod", ".invariant/specs/Counter.tla", ".invariant/specs/Include.tla", ".invariant/ratified.lock"} {
		mutations = append(mutations, struct {
			name string
			edit func(*cc279Result)
		}{"missing actual " + name, func(r *cc279Result) {
			r.FileInputs = slices.DeleteFunc(r.FileInputs, func(x project.ComponentFile) bool { return x.Path == name })
		}})
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			result := cpClone(t, good)
			mutation.edit(&result)
			r := cc279Attach(t, f, result)
			rows := cc279Rows(t, r)
			if len(rows) != 1 {
				t.Fatalf("incomplete/unsuccessful supplied attempt disappeared: %d rows", len(rows))
			}
			cc279Want(t, rows[0], result, "unchecked", false, false, false)
			ce274Statuses(t, r)
		})
	}
	// No result is invented from configuration, positive legacy model counts,
	// successful legacy conformance or a package/build result.
	r := cc279Attach(t, f)
	if len(cc279Rows(t, r)) != 0 {
		t.Fatal("invented conformance from declarations or legacy checks")
	}
	ce274Statuses(t, r)
}

func TestSuppliedConformanceUsesExactSelectedChecksAndInvocations(t *testing.T) {
	f := cc279Fixture(t)
	results := []cc279Result{
		cc279Good(t, f, f.Target, "", "run"),
		cc279Good(t, f, f.Target, "", "repeat"),
		cc279Good(t, f, f.Target, provenance257ID("core/scoped_driver.go", "DriveScoped"), "run"),
		cc279Good(t, f, f.Same, "", "run"),
	}
	results[1].Outcome = "failed"
	results[1].Diagnostics = []string{"a supplied mismatch", "not a package failure"}
	results[2].Mode, results[2].Attempts = "bounded-exhaustive", cc279AttemptGood()
	// Attribution uses supplied snapshots even after their checkout disappears.
	// It cannot validate by looking up files or discovering omitted inputs.
	if err := os.RemoveAll(f.Root); err != nil {
		t.Fatal(err)
	}
	r := cc279Attach(t, f, results...)
	rows := cc279Rows(t, r)
	if len(rows) != len(results) {
		t.Fatalf("collapsed checks/invocations: %d rows", len(rows))
	}
	want := cpClone(t, results)
	slices.SortFunc(want, func(a, b cc279Result) int {
		if c := strings.Compare(a.Target, b.Target); c != 0 {
			return c
		}
		if c := strings.Compare(a.Check, b.Check); c != 0 {
			return c
		}
		return strings.Compare(a.Invocation.ID, b.Invocation.ID)
	})
	for i, result := range want {
		status := "tested"
		if result.Outcome != "passed" {
			status = "unchecked"
		}
		cc279Want(t, rows[i], result, status, result.Outcome == "passed", result.Mode == "bounded-exhaustive", result.Attempts != nil)
	}
	if !reflect.DeepEqual(r.Components.Inventory, *f.Inv) {
		t.Fatal("conformance changed or omitted inventory")
	}
	ce274Statuses(t, r)
	for _, c := range r.Components.Inventory.Components {
		if c.Source == "drivers/drive.go" {
			t.Fatal("outside-scope driver became production coverage")
		}
	}
	var decoded verify.Report
	if err := json.Unmarshal(cpJSON(t, r), &decoded); err != nil {
		t.Fatal(err)
	}
	decoded.Assurance = "proved at every size"
	if decoded.CodeAssurance() != "component evidence" || decoded.Claim() != "component evidence" || decoded.EverySize() || verify.Fingerprint(decoded) != r.Fingerprint {
		t.Fatal("decoded conformance became aggregate proof or changed fingerprint")
	}
}

func TestSuppliedConformanceRejectsInvalidAttributionAtomically(t *testing.T) {
	f := cc279Fixture(t)
	good := cc279Good(t, f, f.Target, "", "")
	mutations := []struct {
		name string
		edit func(*cc279Result)
	}{
		{"unknown target", func(r *cc279Result) { r.Target = "go:unknown#Step@absent.go" }},
		{"bare target", func(r *cc279Result) { r.Target = "Step" }},
		{"unconfigured target", func(r *cc279Result) { r.Target = f.Helper }},
		{"driver as target", func(r *cc279Result) { r.Target = r.Check }},
		{"unknown check", func(r *cc279Result) { r.Check = "go:unknown#Drive@absent.go" }},
		{"bare check", func(r *cc279Result) { r.Check = "Drive" }},
		{"unselected known check", func(r *cc279Result) { r.Check = f.Helper }},
		{"missing invocation", func(r *cc279Result) { r.Invocation = nil }},
		{"unknown invocation", func(r *cc279Result) { r.Invocation.ID = "not-selected" }},
		{"wrong invocation kind", func(r *cc279Result) { r.Invocation.Kind = "test" }},
		{"wrong invocation check", func(r *cc279Result) { r.Invocation.Check = provenance257ID("core/scoped_driver.go", "DriveScoped") }},
		{"changed command", func(r *cc279Result) { r.Invocation.Command += " changed" }},
		{"changed argument order", func(r *cc279Result) { slices.Reverse(r.Invocation.Args) }},
		{"changed working directory", func(r *cc279Result) { r.Invocation.WorkingDir = "drivers" }},
		{"changed environment", func(r *cc279Result) { r.Invocation.Env["MODE"] = "other" }},
		{"changed sampling", func(r *cc279Result) { r.Invocation.Sampling["seed"] = "43" }},
		{"omitted declared input", func(r *cc279Result) { r.Invocation.Inputs = r.Invocation.Inputs[1:] }},
		{"duplicate declared input", func(r *cc279Result) { r.Invocation.Inputs = append(r.Invocation.Inputs, r.Invocation.Inputs[0]) }},
		{"stale declared input", func(r *cc279Result) { r.Invocation.Inputs[0].SHA256 = "sha256:stale" }},
		{"duplicate declaration", func(r *cc279Result) { r.Inputs = append(r.Inputs, r.Inputs[0]) }},
		{"stale declaration", func(r *cc279Result) { r.Inputs[0].SourceSHA256 = "sha256:stale" }},
		{"stale contract", func(r *cc279Result) { r.Inputs[0].ContractSHA256 = "sha256:stale" }},
		{"unknown declaration", func(r *cc279Result) { r.Inputs[0].ID = "unknown" }},
		{"duplicate file", func(r *cc279Result) { r.FileInputs = append(r.FileInputs, r.FileInputs[0]) }},
		{"stale file", func(r *cc279Result) { r.FileInputs[0].SHA256 = "sha256:stale" }},
		{"unknown file", func(r *cc279Result) { r.FileInputs[0].Path = "undeclared.txt" }},
		{"unknown checked", func(r *cc279Result) { r.Checked = []string{"Drive"} }},
		{"unselected checked", func(r *cc279Result) { r.Checked = []string{f.Helper} }},
		{"duplicate checked", func(r *cc279Result) { r.Checked = append(r.Checked, r.Check) }},
		{"stale model snapshot", func(r *cc279Result) { r.Model.Inputs[0].SHA256 = "sha256:stale" }},
		{"duplicate model input", func(r *cc279Result) { r.Model.Inputs = append(r.Model.Inputs, r.Model.Inputs[0]) }},
		{"model name is not its source", func(r *cc279Result) { r.Model.Module = "Spec" }},
		{"undeclared model input", func(r *cc279Result) { r.Model.Inputs = append(r.Model.Inputs, ce274File(t, f.Inv, "assumptions.md")) }},
		{"stale bounds snapshot", func(r *cc279Result) { r.Bounds.Sources[0].SHA256 = "sha256:stale" }},
		{"duplicate bounds source", func(r *cc279Result) { r.Bounds.Sources = append(r.Bounds.Sources, r.Bounds.Sources[0]) }},
		{"undeclared bounds source", func(r *cc279Result) {
			r.Bounds.Sources = []project.ComponentFile{ce274File(t, f.Inv, "assumptions.md")}
		}},
		{"different bounds", func(r *cc279Result) { r.Bounds.Values["Limit"] = "3" }},
		{"omitted report bound", func(r *cc279Result) { delete(r.Bounds.Values, "Limit") }},
		{"additional report bound", func(r *cc279Result) { r.Bounds.Values["Other"] = "3" }},
		{"unknown mode", func(r *cc279Result) { r.Mode = "proved" }},
		{"unknown outcome", func(r *cc279Result) { r.Outcome = "package passed" }},
		{"negative count", func(r *cc279Result) { r.Transitions = -1 }},
		{"unknown attempt outcome", func(r *cc279Result) { r.Attempts = cc279AttemptGood(); r.Attempts.Outcome = "assumed" }},
		{"negative attempt count", func(r *cc279Result) { r.Attempts = cc279AttemptGood(); r.Attempts.Attempts = -1 }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			r := ce274Attach(t, f, true)
			if err := cc279Call(t, &r, f.Inv, []cc279Result{good}); err != nil {
				t.Fatal(err)
			}
			bad := cpClone(t, good)
			mutation.edit(&bad)
			ce274Unchanged(t, &r, func() error { return cc279Call(t, &r, f.Inv, []cc279Result{bad}) })
			// The valid first record changes evidence; a late failure still must
			// leave the complete original report and both fingerprints untouched.
			first := cc279Good(t, f, f.Target, "", "repeat")
			first.Outcome = "failed"
			ce274Unchanged(t, &r, func() error { return cc279Call(t, &r, f.Inv, []cc279Result{first, bad}) })
		})
	}
	r := cc279Attach(t, f, good)
	ce274Unchanged(t, &r, func() error { return cc279Call(t, &r, f.Inv, []cc279Result{good, good}) })
	for _, inv := range []*project.ComponentInventory{nil, {Mode: "legacy"}} {
		ce274Unchanged(t, &r, func() error { return cc279Call(t, &r, inv, nil) })
	}
	// Existing inventory invariants are checked on the new entry point too,
	// including clears, before accepting even an otherwise valid first record.
	for _, edit := range []func(*project.ComponentInventory){
		func(i *project.ComponentInventory) { i.Scope = append(i.Scope, i.Scope[0]) },
		func(i *project.ComponentInventory) { i.Components = append(i.Components, i.Components[0]) },
		func(i *project.ComponentInventory) {
			i.Provenance.Files = append(i.Provenance.Files, i.Provenance.Files[0])
		},
		func(i *project.ComponentInventory) { i.Checks = append(i.Checks, i.Checks[0]) },
		func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Conformance = append(c.Conformance, c.Conformance[0])
		},
		func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Invocations = append(c.Invocations, c.Invocations[0])
		},
	} {
		inv := cpClone(t, f.Inv)
		edit(inv)
		ce274Unchanged(t, &r, func() error { return cc279Call(t, &r, inv, nil) })
	}
}
