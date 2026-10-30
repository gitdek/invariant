package verify_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

// Contract for #287: Report.AttributeComponentRuntime(*project.ComponentInventory,
// []ComponentRuntimeResult) error replaces only supplied runtime evidence.
// ComponentEvidence.Runtime is []ComponentRuntimeEvidence, JSON "runtime,omitempty".
// The cr287 wire structs below specify the public structs and JSON fields:
// cr287Result = ComponentRuntimeResult; cr287Test = ComponentRuntimeTestResult;
// cr287Assertion = ComponentRuntimeAssertionResult; cr287Evidence = ComponentRuntimeEvidence;
// cr287TestEvidence = ComponentRuntimeTestEvidence;
// cr287AssertionEvidence = ComponentRuntimeAssertionEvidence.
// Reflection allows the absent API to fail assertions on the base branch.
//
// A result is unique by (Target, Check, Invocation.ID). Check and each Tests.Check
// must be canonical selected tests of Target; Invocation is required, kind "test",
// and matches the selected check, ID, literal command, ordered args, working_dir,
// env, sampling and input snapshot set exactly. Tests are actual supplied test
// outcomes, never inferred from selections. Test IDs are unique within a result;
// assertion IDs are nonblank, unique within their test and scoped to that test
// and invocation, not discovered component IDs. Assertion Source, when supplied,
// must match a known input file; an absent source leaves it unchecked. No AST
// parsing, assertion discovery, execution, filesystem lookup or gate connection.
//
// Inputs and FileInputs preserve every supplied declaration and file snapshot,
// including known extras beyond declared invocation inputs. Wrong/duplicate
// snapshots reject; omitted required actual snapshots leave records unchecked.
// Common qualification needs runner name/version, target and selected-contract
// declarations and source files, and all invocation inputs. A test additionally
// needs its own declaration/source, Exercised=true and Outcome="passed".
// An assertion additionally needs its parent test actually exercised, its own
// Exercised=true, Outcome="passed", and Source present in actual FileInputs.
// Individual assertion success survives a failing test/process; neither a
// failing nor unexercised assertion can inherit a passing enclosing outcome.
// Row Status="tested" needs Outcome="passed" and a qualifying actual test for
// Check. Test statuses are "tested"/"unchecked"; assertion statuses are
// "passed"/"unchecked". No status establishes target coverage or another kind.
// A passing test needs no assertion records; omitted tests/assertions stay absent.
// Outcomes at every level are "passed", "failed", "timed_out", "skipped",
// "empty" or ""; diagnostics are retained ordered data, not alternate outcomes.
//
// Optional Model and Bounds reuse ComponentConformanceModel/Bounds. Supplied
// model/include and bounds sources must match declared invocation inputs, and
// supplied bounds values match Report.Bounds. Incomplete supplied context blocks
// success; ordinary runtime tests need neither optional context. All actual
// referenced inputs, and incomplete records' original target/contract/test and
// invocation anchors, are revalidated by every update/clear of another kind.
// Rows sort by (Target,Check,Invocation.ID), tests by Check, assertions by ID.
// Inputs, file inputs, model/bounds sources and all reference sets canonicalize;
// arguments, observations and diagnostics retain meaningful order. Fingerprint
// applies the same canonicalization to decoded reports without mutating them.

type cr287Assertion struct {
	ID          string                 `json:"id"`
	Source      *project.ComponentFile `json:"source,omitempty"`
	Exercised   bool                   `json:"exercised"`
	Outcome     string                 `json:"outcome"`
	Diagnostics []string               `json:"diagnostics,omitempty"`
}

type cr287Test struct {
	Check       string           `json:"check"`
	Exercised   bool             `json:"exercised"`
	Outcome     string           `json:"outcome"`
	Assertions  []cr287Assertion `json:"assertions,omitempty"`
	Diagnostics []string         `json:"diagnostics,omitempty"`
}

type cr287Result struct {
	Target      string                               `json:"target"`
	Check       string                               `json:"check"`
	Invocation  *project.ResolvedComponentInvocation `json:"invocation,omitempty"`
	Runner      *verify.ComponentProofTool           `json:"runner,omitempty"`
	Inputs      []project.Component                  `json:"inputs"`
	FileInputs  []project.ComponentFile              `json:"file_inputs"`
	Model       *verify.ComponentConformanceModel    `json:"model,omitempty"`
	Bounds      *verify.ComponentConformanceBounds   `json:"bounds,omitempty"`
	Outcome     string                               `json:"outcome"`
	Tests       []cr287Test                          `json:"tests,omitempty"`
	Diagnostics []string                             `json:"diagnostics,omitempty"`
}

type cr287AssertionEvidence struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type cr287TestEvidence struct {
	Check      string                   `json:"check"`
	Status     string                   `json:"status"`
	Assertions []cr287AssertionEvidence `json:"assertions,omitempty"`
}

type cr287Evidence struct {
	Target string              `json:"target"`
	Check  string              `json:"check"`
	Status string              `json:"status"`
	Tests  []cr287TestEvidence `json:"tests,omitempty"`
	Result cr287Result         `json:"result"`
}

func cr287Fixture(t *testing.T) ce274FixtureData {
	t.Helper()
	f := cc279Fixture(t)
	provenance257EditManifest(t, f.Root, func(m map[string]any) {
		step := provenance257ConfigCheck(m, "Step")
		var call map[string]any
		for _, item := range step["invocations"].([]any) {
			v := item.(map[string]any)
			if v["kind"] == "test" {
				call = v
			}
		}
		call["inputs"] = []string{"tests/step_test.go", "core/core.go", "core/contracts.go", "data/fixture.bin", "config/check.json", "go.mod", "go.sum", ".invariant/specs/Counter.tla", ".invariant/specs/Include.tla", ".invariant/ratified.lock"}
		repeat := cpClone(t, call)
		repeat["id"] = "repeat"
		other := cpClone(t, call)
		other["check"] = provenance257Ref("tests/step_test.go", "TestAgain")
		step["invocations"] = append(step["invocations"].([]any), repeat, other)
	})
	f.Inv = cpResolve(t, f.Root)
	return f
}

func cr287Selected(t *testing.T, inv *project.ComponentInventory, target, check, id string) *project.ResolvedComponentInvocation {
	t.Helper()
	if check == "" {
		check = provenance257ID("tests/step_test.go", "TestStep")
	}
	if id == "" {
		id = "run"
	}
	c := ce274Check(t, inv, target)
	for i := range c.Invocations {
		v := &c.Invocations[i]
		if v.Kind == "test" && v.Check == check && v.ID == id {
			return v
		}
	}
	t.Fatalf("fixture lacks test invocation %s/%s/%s", target, check, id)
	return nil
}

func cr287Good(t *testing.T, f ce274FixtureData, check, id string) cr287Result {
	t.Helper()
	v := cpClone(t, cr287Selected(t, f.Inv, f.Target, check, id))
	base := cc279Good(t, f, f.Target, "", "")
	inputs := []project.Component{ce274Component(t, f.Inv, f.Target)}
	for _, selected := range append(slices.Clone(ce274Check(t, f.Inv, f.Target).Proof.Contracts), v.Check) {
		if !slices.ContainsFunc(inputs, func(c project.Component) bool { return c.ID == selected }) {
			inputs = append(inputs, ce274Component(t, f.Inv, selected))
		}
	}
	source := ce274File(t, f.Inv, "tests/step_test.go")
	result := cr287Result{Target: f.Target, Check: v.Check, Invocation: v,
		Runner: &verify.ComponentProofTool{Name: "supplied-test-runner", Version: "1.2.3", Pins: map[string]string{"image": "runner@sha256:abc", "recipe": "sha256:def"}},
		Inputs: inputs, FileInputs: base.FileInputs, Outcome: "passed",
		Tests: []cr287Test{{Check: v.Check, Exercised: true, Outcome: "passed", Diagnostics: []string{"test observation", "test detail"},
			Assertions: []cr287Assertion{{ID: "assert-a", Source: &source, Exercised: true, Outcome: "passed", Diagnostics: []string{"assertion observation", "assertion detail"}}}}},
		Diagnostics: []string{"runtime observation", "runtime detail"}}
	slices.SortFunc(result.Inputs, func(a, b project.Component) int { return strings.Compare(a.ID, b.ID) })
	return result
}

func cr287Context(t *testing.T, f ce274FixtureData, result *cr287Result) {
	t.Helper()
	result.Model = &verify.ComponentConformanceModel{Module: ".invariant/specs/Counter.tla", Inputs: []project.ComponentFile{ce274File(t, f.Inv, ".invariant/specs/Counter.tla"), ce274File(t, f.Inv, ".invariant/specs/Include.tla")}}
	result.Bounds = &verify.ComponentConformanceBounds{Values: map[string]string{"Limit": "2"}, Sources: []project.ComponentFile{ce274File(t, f.Inv, ".invariant/ratified.lock")}}
}

func cr287CallArg(t *testing.T, r *verify.Report, inv *project.ComponentInventory, results []cr287Result) (reflect.Value, reflect.Value) {
	t.Helper()
	m := reflect.ValueOf(r).MethodByName("AttributeComponentRuntime")
	if !m.IsValid() {
		t.Fatal("missing Report.AttributeComponentRuntime")
	}
	if m.Type().NumIn() != 2 || m.Type().In(0) != reflect.TypeOf(inv) || m.Type().In(1).Kind() != reflect.Slice || m.Type().NumOut() != 1 || m.Type().Out(0) != reflect.TypeOf((*error)(nil)).Elem() {
		t.Fatal("AttributeComponentRuntime must accept inventory and result slice and return error")
	}
	if m.Type().In(1).Elem().Name() != "ComponentRuntimeResult" {
		t.Fatal("runtime API must expose ComponentRuntimeResult")
	}
	arg := reflect.New(m.Type().In(1))
	if err := json.Unmarshal(cpJSON(t, results), arg.Interface()); err != nil {
		t.Fatal(err)
	}
	return m, arg.Elem()
}

func cr287Call(t *testing.T, r *verify.Report, inv *project.ComponentInventory, results []cr287Result) error {
	t.Helper()
	m, arg := cr287CallArg(t, r, inv, results)
	out := m.Call([]reflect.Value{reflect.ValueOf(inv), arg})[0]
	if out.IsNil() {
		return nil
	}
	return out.Interface().(error)
}

func cr287Attach(t *testing.T, f ce274FixtureData, results ...cr287Result) verify.Report {
	t.Helper()
	r := ce274Seed()
	if err := cr287Call(t, &r, f.Inv, results); err != nil {
		t.Fatal(err)
	}
	return r
}

func cr287Rows(t *testing.T, r verify.Report) []cr287Evidence {
	t.Helper()
	var doc struct {
		Components struct {
			Runtime []cr287Evidence `json:"runtime"`
		} `json:"components"`
	}
	if err := json.Unmarshal(cpJSON(t, r), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Components.Runtime
}

func cr287RequireAPI(t *testing.T, f ce274FixtureData) {
	t.Helper()
	rows := cr287Rows(t, cr287Attach(t, f, cr287Good(t, f, "", "")))
	if len(rows) != 1 || rows[0].Status != "tested" || len(rows[0].Tests) != 1 || rows[0].Tests[0].Status != "tested" || len(rows[0].Tests[0].Assertions) != 1 || rows[0].Tests[0].Assertions[0].Status != "passed" {
		t.Fatalf("runtime API did not qualify actual supplied test/assertion outcomes: %+v", rows)
	}
}

func cr287CanonicalResult(result cr287Result) cr287Result {
	slices.SortFunc(result.Inputs, func(a, b project.Component) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(result.FileInputs, func(a, b project.ComponentFile) int { return strings.Compare(a.Path, b.Path) })
	if result.Invocation != nil {
		slices.SortFunc(result.Invocation.Inputs, func(a, b project.ComponentFile) int { return strings.Compare(a.Path, b.Path) })
	}
	if result.Model != nil {
		slices.SortFunc(result.Model.Inputs, func(a, b project.ComponentFile) int { return strings.Compare(a.Path, b.Path) })
	}
	if result.Bounds != nil {
		slices.SortFunc(result.Bounds.Sources, func(a, b project.ComponentFile) int { return strings.Compare(a.Path, b.Path) })
	}
	slices.SortFunc(result.Tests, func(a, b cr287Test) int { return strings.Compare(a.Check, b.Check) })
	for i := range result.Tests {
		slices.SortFunc(result.Tests[i].Assertions, func(a, b cr287Assertion) int { return strings.Compare(a.ID, b.ID) })
	}
	return result
}

func cr287WantResult(t *testing.T, row cr287Evidence, result cr287Result) {
	t.Helper()
	want := cr287CanonicalResult(cpClone(t, result))
	if row.Target != want.Target || row.Check != want.Check || string(cpJSON(t, row.Result)) != string(cpJSON(t, want)) {
		t.Fatalf("runtime record lost supplied values: got %s; want %s", cpJSON(t, row), cpJSON(t, want))
	}
}

func TestRuntimeOutcomesDoNotInventTestsOrAssertions(t *testing.T) {
	f := cr287Fixture(t)
	cr287RequireAPI(t, f)
	good := cr287Good(t, f, "", "")
	for _, outcome := range []string{"passed", "failed", "timed_out", "skipped", "empty", ""} {
		for _, level := range []string{"process", "test", "assertion"} {
			t.Run(level+"/"+outcome, func(t *testing.T) {
				result := cpClone(t, good)
				switch level {
				case "process":
					result.Outcome = outcome
				case "test":
					result.Tests[0].Outcome = outcome
				case "assertion":
					result.Tests[0].Assertions[0].Outcome = outcome
				}
				r := cr287Attach(t, f, result)
				rows := cr287Rows(t, r)
				if len(rows) != 1 || len(rows[0].Tests) != 1 || len(rows[0].Tests[0].Assertions) != 1 {
					t.Fatal("actual records were dropped or declarations created more records")
				}
				row := rows[0]
				cr287WantResult(t, row, result)
				rowStatus, testStatus, assertionStatus := "tested", "tested", "passed"
				if outcome != "passed" {
					switch level {
					case "process":
						rowStatus = "unchecked"
					case "test":
						rowStatus, testStatus = "unchecked", "unchecked"
					case "assertion":
						assertionStatus = "unchecked"
					}
				}
				if row.Status != rowStatus || row.Tests[0].Status != testStatus || row.Tests[0].Assertions[0].Status != assertionStatus {
					t.Fatalf("enclosing outcome overwrote actual per-item evidence: %+v", row)
				}
				ce274Statuses(t, r)
			})
		}
	}
	for _, level := range []string{"test", "assertion"} {
		t.Run("unexercised "+level, func(t *testing.T) {
			result := cpClone(t, good)
			if level == "test" {
				result.Tests[0].Exercised = false
			} else {
				result.Tests[0].Assertions[0].Exercised = false
			}
			row := cr287Rows(t, cr287Attach(t, f, result))[0]
			cr287WantResult(t, row, result)
			if row.Tests[0].Assertions[0].Status != "unchecked" {
				t.Fatal("unexercised outcome granted assertion success")
			}
			if level == "test" && (row.Status != "unchecked" || row.Tests[0].Status != "unchecked") {
				t.Fatal("configured test name granted success without actual exercise")
			}
		})
	}
	t.Run("package pass without test evidence", func(t *testing.T) {
		result := cpClone(t, good)
		result.Tests = nil
		r := cr287Attach(t, f, result)
		row := cr287Rows(t, r)[0]
		cr287WantResult(t, row, result)
		if row.Status != "unchecked" || len(row.Tests) != 0 {
			t.Fatal("process/package pass or selected names manufactured test/assertion evidence")
		}
		ce274Statuses(t, r)
	})
	t.Run("passing test without assertion evidence", func(t *testing.T) {
		result := cpClone(t, good)
		result.Tests[0].Assertions = nil
		row := cr287Rows(t, cr287Attach(t, f, result))[0]
		cr287WantResult(t, row, result)
		if row.Status != "tested" || row.Tests[0].Status != "tested" || len(row.Tests[0].Assertions) != 0 {
			t.Fatal("passing actual test must stand alone without inventing assertions")
		}
	})
	t.Run("different selected test reported", func(t *testing.T) {
		result := cpClone(t, good)
		again := cr287Good(t, f, provenance257ID("tests/step_test.go", "TestAgain"), "")
		result.Tests = cpClone(t, again.Tests)
		result.Inputs = append(result.Inputs, ce274Component(t, f.Inv, again.Check))
		row := cr287Rows(t, cr287Attach(t, f, result))[0]
		cr287WantResult(t, row, result)
		if row.Status != "unchecked" || len(row.Tests) != 1 || row.Tests[0].Check != again.Check || row.Tests[0].Status != "tested" {
			t.Fatal("a reported different selected test filled in the unreported primary check")
		}
	})
	t.Run("legacy pass without supplied runtime", func(t *testing.T) {
		r := cr287Attach(t, f)
		if len(cr287Rows(t, r)) != 0 {
			t.Fatal("legacy success or selections created runtime evidence")
		}
		ce274Statuses(t, r)
	})
}

func TestRuntimeSuccessRequiresRunnerAndActualInputSnapshots(t *testing.T) {
	f := cr287Fixture(t)
	cr287RequireAPI(t, f)
	good := cr287Good(t, f, "", "")
	changes := []struct {
		name          string
		edit          func(*cr287Result)
		assertionOnly bool
	}{
		{"missing runner", func(r *cr287Result) { r.Runner = nil }, false},
		{"missing runner name", func(r *cr287Result) { r.Runner.Name = " \t" }, false},
		{"missing runner version", func(r *cr287Result) { r.Runner.Version = "" }, false},
		{"missing all declarations", func(r *cr287Result) { r.Inputs = nil }, false},
		{"missing all actual files", func(r *cr287Result) { r.FileInputs = nil }, false},
		{"missing assertion source", func(r *cr287Result) { r.Tests[0].Assertions[0].Source = nil }, true},
		{"missing supplied model identity", func(r *cr287Result) { cr287Context(t, f, r); r.Model.Module = "" }, false},
		{"missing supplied model source", func(r *cr287Result) { cr287Context(t, f, r); r.Model.Inputs = nil }, false},
		{"missing supplied bounds source", func(r *cr287Result) { cr287Context(t, f, r); r.Bounds.Sources = nil }, false},
	}
	for _, id := range []string{f.Target, f.Helper, good.Check} {
		changes = append(changes, struct {
			name          string
			edit          func(*cr287Result)
			assertionOnly bool
		}{
			"missing declaration " + id, func(r *cr287Result) {
				r.Inputs = slices.DeleteFunc(r.Inputs, func(c project.Component) bool { return c.ID == id })
			}, false})
	}
	for _, name := range []string{"core/core.go", "core/contracts.go", "tests/step_test.go", "data/fixture.bin", "config/check.json", "go.mod", "go.sum", ".invariant/specs/Counter.tla", ".invariant/specs/Include.tla", ".invariant/ratified.lock"} {
		changes = append(changes, struct {
			name          string
			edit          func(*cr287Result)
			assertionOnly bool
		}{
			"missing file " + name, func(r *cr287Result) {
				r.FileInputs = slices.DeleteFunc(r.FileInputs, func(f project.ComponentFile) bool { return f.Path == name })
			}, false})
	}
	for _, tc := range changes {
		t.Run(tc.name, func(t *testing.T) {
			result := cpClone(t, good)
			tc.edit(&result)
			rows := cr287Rows(t, cr287Attach(t, f, result))
			if len(rows) != 1 || len(rows[0].Tests) != 1 || len(rows[0].Tests[0].Assertions) != 1 {
				t.Fatal("incomplete record was dropped")
			}
			cr287WantResult(t, rows[0], result)
			if rows[0].Tests[0].Assertions[0].Status != "unchecked" {
				t.Fatal("assertion succeeded without required actual provenance")
			}
			status := "unchecked"
			if tc.assertionOnly {
				status = "tested"
			}
			if rows[0].Status != status || rows[0].Tests[0].Status != status {
				t.Fatal("incomplete runtime inputs were upgraded or unrelated test evidence was lost")
			}
		})
	}
	// A known additional assertion source is allowed, but it must be among the
	// files actually supplied, even when it was not an invocation declaration.
	result := cpClone(t, good)
	extra := ce274File(t, f.Inv, "data/second.bin")
	result.Tests[0].Assertions[0].Source = &extra
	row := cr287Rows(t, cr287Attach(t, f, result))[0]
	if row.Tests[0].Assertions[0].Status != "passed" {
		t.Fatal("known supplied assertion source was not retained")
	}
	result.FileInputs = slices.DeleteFunc(result.FileInputs, func(f project.ComponentFile) bool { return f.Path == extra.Path })
	row = cr287Rows(t, cr287Attach(t, f, result))[0]
	if row.Tests[0].Assertions[0].Status != "unchecked" || row.Tests[0].Status != "tested" {
		t.Fatal("assertion-specific missing source changed unrelated test evidence")
	}
}

func TestRuntimeKeepsDistinctSelectedChecksInvocationsAndActualAssertions(t *testing.T) {
	f := cr287Fixture(t)
	cr287RequireAPI(t, f)
	a := cr287Good(t, f, "", "")
	a.Outcome, a.Tests[0].Outcome = "failed", "failed"
	bad := cpClone(t, a.Tests[0].Assertions[0])
	bad.ID, bad.Outcome = "assert-b", "failed"
	missing := cpClone(t, bad)
	missing.ID, missing.Outcome, missing.Exercised = "assert-c", "passed", false
	a.Tests[0].Assertions = append(a.Tests[0].Assertions, bad, missing)
	b := cr287Good(t, f, "", "repeat")
	b.Tests[0].Assertions = nil
	c := cr287Good(t, f, provenance257ID("tests/step_test.go", "TestAgain"), "")
	// One invocation may actually report more than its primary selected check;
	// those tests remain selected canonical declarations with their own outcomes.
	c.Tests = append(c.Tests, cpClone(t, b.Tests[0]))
	c.Inputs = append(c.Inputs, ce274Component(t, f.Inv, b.Check))
	r := cr287Attach(t, f, a, b, c)
	rows := cr287Rows(t, r)
	if len(rows) != 3 {
		t.Fatalf("distinct check/invocation records collapsed: %+v", rows)
	}
	for _, want := range []cr287Result{a, b, c} {
		pos := slices.IndexFunc(rows, func(row cr287Evidence) bool {
			return row.Check == want.Check && row.Result.Invocation.ID == want.Invocation.ID
		})
		if pos < 0 {
			t.Fatal("selected test or invocation identity vanished")
		}
		row := rows[pos]
		cr287WantResult(t, row, want)
		if want.Invocation.ID == a.Invocation.ID && want.Check == a.Check {
			if row.Status != "unchecked" || row.Tests[0].Status != "unchecked" {
				t.Fatal("failed actual test became successful")
			}
			if len(row.Tests[0].Assertions) != 3 || row.Tests[0].Assertions[0].Status != "passed" || row.Tests[0].Assertions[1].Status != "unchecked" || row.Tests[0].Assertions[2].Status != "unchecked" {
				t.Fatal("assertions inherited enclosing outcomes or lost individual success")
			}
		} else if row.Status != "tested" {
			t.Fatal("one failed invocation erased a distinct passing invocation")
		}
	}
	// Report only TestStep, leaving the configured TestAgain unreported.
	r = cr287Attach(t, f, b)
	rows = cr287Rows(t, r)
	if len(rows) != 1 || len(rows[0].Tests) != 1 || rows[0].Tests[0].Check != b.Check || len(rows[0].Tests[0].Assertions) != 0 {
		t.Fatal("configuration filled unreported tests or assertions")
	}
	ce274Statuses(t, r)
}

func TestAllFourComponentKindsCoexistAndRoundTripWithoutTransferringProof(t *testing.T) {
	f := cr287Fixture(t)
	cr287RequireAPI(t, f)
	for _, passed := range []bool{false, true} {
		t.Run(fmt.Sprint(passed), func(t *testing.T) {
			r := ce274Seed()
			r.Passed = passed
			legacy := ce274Legacy(t, r)
			proof := ce274ProofFor(t, f, f.Target, false)
			proof.Verified = append(proof.Verified, f.Helper)
			if err := ce274Proofs(t, &r, f.Inv, []ce274Proof{proof}); err != nil {
				t.Fatal(err)
			}
			if err := ce274Assumptions(t, &r, f.Inv, ce274Records(t, f)); err != nil {
				t.Fatal(err)
			}
			conformance := []cc279Result{cc279Good(t, f, f.Target, "", ""), cc279Good(t, f, f.Same, "", "")}
			if err := cc279Call(t, &r, f.Inv, conformance); err != nil {
				t.Fatal(err)
			}
			runtime := cr287Good(t, f, "", "")
			cr287Context(t, f, &runtime)
			if err := cr287Call(t, &r, f.Inv, []cr287Result{runtime}); err != nil {
				t.Fatal(err)
			}
			ce274Statuses(t, r, f.Target)
			ce274WantRecords(t, r, ce274Records(t, f))
			if len(cc279Rows(t, r)) != 2 || len(cr287Rows(t, r)) != 1 {
				t.Fatal("all four evidence kinds did not coexist")
			}
			cr287WantResult(t, cr287Rows(t, r)[0], runtime)
			if ce274Legacy(t, r) != legacy || r.Passed != passed {
				t.Fatal("component evidence changed gate, ratified data or legacy checks")
			}
			if string(cpJSON(t, r.Components.Inventory)) != string(cpJSON(t, f.Inv)) {
				t.Fatal("evidence discarded unchecked neighbors, functionless files or separate provenance")
			}
			if !slices.ContainsFunc(r.Components.Inventory.Files, func(f project.ComponentFile) bool { return f.Path == "core/data.go" }) {
				t.Fatal("functionless production file disappeared")
			}
			decoded := cpClone(t, r)
			if string(cpJSON(t, decoded)) != string(cpJSON(t, r)) || verify.Fingerprint(decoded) != r.Fingerprint {
				t.Fatal("all-four-kind JSON round trip lost evidence or fingerprint")
			}
			decoded.Assurance = "proved"
			if decoded.CodeAssurance() != "component evidence" || decoded.Claim() != "component evidence" || decoded.EverySize() || verify.Fingerprint(decoded) != r.Fingerprint {
				t.Fatal("stale label manufactured aggregate proof or every-size claim")
			}
		})
	}
}

func TestRuntimeUpdatesPreserveConformanceObservationAndAttemptQualifications(t *testing.T) {
	f := cr287Fixture(t)
	cr287RequireAPI(t, f)
	for _, mode := range []string{"sampled", "bounded-exhaustive"} {
		for _, outcome := range []string{"absent", "passed", "failed", "timed_out", "skipped", "empty", "", "passed-but-empty"} {
			t.Run(mode+"/"+outcome, func(t *testing.T) {
				conformance := cc279Good(t, f, f.Target, "", "")
				conformance.Mode = mode
				if outcome != "absent" {
					conformance.Attempts = cc279AttemptGood()
					conformance.Attempts.Outcome = outcome
					if outcome == "passed-but-empty" {
						conformance.Attempts.Outcome, conformance.Attempts.Attempts = "passed", 0
					}
				}
				r := cc279Attach(t, f, conformance)
				before := cct279KindJSON(t, r, "conformance")
				result := cr287Good(t, f, "", "")
				for _, results := range [][]cr287Result{{result}, nil} {
					if err := cr287Call(t, &r, f.Inv, results); err != nil {
						t.Fatal(err)
					}
					if cct279KindJSON(t, r, "conformance") != before {
						t.Fatal("runtime replacement or clear changed observed conformance")
					}
					row := cc279Rows(t, r)[0]
					cc279Want(t, row, conformance, "tested", true, mode == "bounded-exhaustive" && (outcome == "absent" || outcome == "passed"), outcome == "passed")
				}
			})
		}
	}
}
