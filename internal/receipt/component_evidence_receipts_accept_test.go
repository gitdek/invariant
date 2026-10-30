package receipt_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

// The production table remains the coverage inventory. Separate conformance,
// runtime and assumption tables associate each supplied record with its exact
// target. Their Status columns contain controlled qualifications; raw outcomes,
// selections and diagnostics belong in Evidence. No result is invented from a
// selection. These headings are part of the human-readable receipt contract.
const evidence296Production = "| Component | Proof | Source | Contract | Evidence |"
const evidence296Checks = "| Target | Check | Status | Evidence |"
const evidence296AssumptionTable = "| Target | Assumption | Status | Evidence |"

type evidence296Data struct {
	Inv         project.ComponentInventory
	Proofs      []verify.ComponentProofResult
	Conformance []verify.ComponentConformanceResult
	Runtime     []verify.ComponentRuntimeResult
	Assumptions []verify.ComponentAssumptionRecord
	Seed        verify.Report
}

func evidence296Clone[T any](t *testing.T, value T) T {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func evidence296Fixture(t *testing.T) evidence296Data {
	t.Helper()
	f := evidence296Data{Seed: receiptAssuranceFixtures()[0]}
	f.Seed.Project = "supplied component evidence"
	f.Seed.Passed = false // Successful observations must survive an unrelated failure.
	file := func(path string) project.ComponentFile {
		return project.ComponentFile{Path: path, SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(path)))}
	}
	component := func(symbol, path string, contracted bool) project.Component {
		c := project.Component{Language: "go", Package: "example.test/receipt296/" + strings.Split(path, "/")[0], Symbol: symbol,
			Source: path, SourceSHA256: file(path).SHA256}
		c.ID = c.Language + ":" + c.Package + "#" + c.Symbol + "@" + path
		if contracted {
			c.ContractSHA256 = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("contract:"+symbol)))
		}
		return c
	}
	all := component("All", "core/all.go", true)
	assumed := component("Assumed", "core/assumed.go", false)
	contract := component("Contract", "core/contract.go", true)
	neighbor := component("Neighbor", "core/neighbor.go", false)
	none := component("NoResult", "core/noresult.go", false)
	unchecked := component("Unchecked", "core/unchecked.go", false)
	driver := component("Drive", "drivers/drive.go", false)
	test := component("TestOne", "tests/one_test.go", false)
	selected := component("TestSelectedOnly", "tests/selected_test.go", false)
	f.Inv = project.ComponentInventory{Mode: "explicit", Scope: []string{"core"}, Components: []project.Component{all, assumed, contract, neighbor, none, unchecked}}
	for _, c := range f.Inv.Components {
		f.Inv.Files = append(f.Inv.Files, file(c.Source))
	}
	f.Inv.Files = append(f.Inv.Files, file("core/data.go")) // No function, still fingerprinted.
	inputs := []project.ComponentFile{file(driver.Source), file(test.Source), file(selected.Source), file("assumptions.txt"), file("fixtures/input.bin"), file("config/check.json"), file("go.sum"), file("model/Counter.tla"), file("model/Include.tla"), file(".invariant/ratified.lock")}
	f.Inv.Provenance = &project.ComponentProvenance{Declarations: []project.Component{driver, test, selected}, Files: inputs, Modules: []project.ComponentFile{file("go.mod"), file("drivers/go.mod")}}
	invocation := func(kind, check, id string) project.ResolvedComponentInvocation {
		return project.ResolvedComponentInvocation{Kind: kind, Check: check, ID: id, Command: "not-an-executable-" + kind,
			Args: []string{"first-argument", "second-argument"}, WorkingDir: ".", Env: map[string]string{"EXPLICIT_ENV": "value"}, Sampling: map[string]string{"seed": "17"}, Inputs: slices.Clone(inputs)}
	}
	a := project.ResolvedComponentAssumption{ID: "clock-premise", Statement: "The supplied clock is monotonic.", Reason: "Caller policy, not an executed check.", Source: file("assumptions.txt")}
	pv, cv, rv := invocation("proof", all.ID, "proof-invocation"), invocation("conformance", driver.ID, "conformance-invocation"), invocation("test", test.ID, "runtime-invocation")
	f.Inv.Checks = []project.ResolvedComponentCheck{
		{Target: all.ID, Proof: &project.ResolvedComponentProof{Contracts: []string{contract.ID}}, Conformance: []string{driver.ID}, Tests: []string{test.ID, selected.ID}, Assumptions: []project.ResolvedComponentAssumption{a}, Invocations: []project.ResolvedComponentInvocation{pv, cv, rv}},
		{Target: assumed.ID, Assumptions: []project.ResolvedComponentAssumption{a}},
		{Target: neighbor.ID, Conformance: []string{driver.ID}, Invocations: []project.ResolvedComponentInvocation{cv}},
		{Target: none.ID, Proof: &project.ResolvedComponentProof{Contracts: []string{contract.ID}}, Tests: []string{test.ID}, Conformance: []string{driver.ID}, Assumptions: []project.ResolvedComponentAssumption{a}, Invocations: []project.ResolvedComponentInvocation{cv, rv}},
	}
	actualFiles := append(slices.Clone(f.Inv.Files), inputs...)
	actualFiles = append(actualFiles, f.Inv.Provenance.Modules...)
	tool := func(name string) *verify.ComponentProofTool {
		return &verify.ComponentProofTool{Name: name, Version: "v2.9", Pins: map[string]string{"image": "registry.test/runner@sha256:" + strings.Repeat("a", 64), "recipe": "sha256:" + strings.Repeat("b", 64)}}
	}
	f.Proofs = []verify.ComponentProofResult{{Target: all.ID, Verifier: "supplied-verifier", Passed: true, Inputs: []project.Component{all, contract}, Verified: []string{all.ID, contract.ID}, Contracts: []string{contract.ID}, Invocation: &pv, FileInputs: slices.Clone(actualFiles), Tool: tool("proof-tool")}}
	model := &verify.ComponentConformanceModel{Module: "model/Counter.tla", Inputs: []project.ComponentFile{file("model/Counter.tla"), file("model/Include.tla")}}
	bounds := &verify.ComponentConformanceBounds{Values: map[string]string{"Limit": "2"}, Sources: []project.ComponentFile{file(".invariant/ratified.lock")}}
	obs := verify.ComponentConformanceObservation{Run: 1, Step: 1, Action: "Increment", Args: []string{"arg-one", "arg-two"}, From: map[string]string{"n": "0"}, To: map[string]string{"n": "1"}}
	f.Conformance = []verify.ComponentConformanceResult{{Target: all.ID, Check: driver.ID, Invocation: &cv, Runner: tool("conformance-runner"), Inputs: []project.Component{all, contract, driver}, FileInputs: slices.Clone(actualFiles), Model: model, Bounds: bounds, Mode: "bounded-exhaustive", Outcome: "passed", Checked: []string{driver.ID}, Runs: 3, Steps: 11, States: 5, Transitions: 7, ModelStates: 5, Observations: []verify.ComponentConformanceObservation{obs}, Attempts: &verify.ComponentAttemptResult{Outcome: "passed", Attempts: 13, States: 5, Observations: []verify.ComponentConformanceObservation{obs}, Diagnostics: []string{"attempt-detail"}}, Diagnostics: []string{"conformance-detail"}}}
	cn := evidence296Clone(t, f.Conformance[0])
	cn.Target = neighbor.ID
	cn.Inputs = []project.Component{neighbor, driver}
	cn.Mode = "sampled"
	cn.Attempts = nil
	f.Conformance = append(f.Conformance, cn)
	assertionSource := file(test.Source)
	f.Runtime = []verify.ComponentRuntimeResult{{Target: all.ID, Check: test.ID, Invocation: &rv, Runner: tool("runtime-runner"), Inputs: []project.Component{all, contract, test}, FileInputs: slices.Clone(actualFiles), Model: model, Bounds: bounds, Outcome: "passed", Tests: []verify.ComponentRuntimeTestResult{{Check: test.ID, Exercised: true, Outcome: "passed", Diagnostics: []string{"test-detail"}, Assertions: []verify.ComponentRuntimeAssertionResult{{ID: "assert-count", Source: &assertionSource, Exercised: true, Outcome: "passed", Diagnostics: []string{"assertion-detail"}}}}}, Diagnostics: []string{"runtime-detail"}}}
	f.Assumptions = []verify.ComponentAssumptionRecord{{Target: all.ID, Input: all, Assumption: a}, {Target: assumed.ID, Input: assumed, Assumption: a}}
	return f
}

func (f evidence296Data) report(t *testing.T) verify.Report {
	t.Helper()
	r := evidence296Clone(t, f.Seed)
	for _, call := range []func() error{
		func() error { return r.AttributeComponentProofs(&f.Inv, f.Proofs) },
		func() error { return r.AttributeComponentConformance(&f.Inv, f.Conformance) },
		func() error { return r.AttributeComponentRuntime(&f.Inv, f.Runtime) },
		func() error { return r.AttributeComponentAssumptions(&f.Inv, f.Assumptions) },
	} {
		if err := call(); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func evidence296Render(t *testing.T, r *verify.Report) string {
	t.Helper()
	before, _ := json.Marshal(r)
	fingerprint, passed := verify.Fingerprint(*r), r.Passed
	md := receipt.Markdown(r)
	after, _ := json.Marshal(r)
	if string(before) != string(after) || fingerprint != verify.Fingerprint(*r) || r.Passed != passed {
		t.Fatal("rendering changed report JSON, fingerprint or gate outcome")
	}
	decoded := evidence296Clone(t, *r)
	if receipt.Markdown(&decoded) != md || verify.Fingerprint(decoded) != fingerprint {
		t.Fatal("JSON round trip changed receipt or fingerprint")
	}
	return md
}

func evidence296Section(t *testing.T, md, name string) string {
	t.Helper()
	start := strings.Index(md, "### Component "+name+"\n")
	if start < 0 {
		t.Fatalf("missing separate component %s view", name)
	}
	rest := md[start+len("### Component "+name+"\n"):]
	if end := strings.Index(rest, "\n### "); end >= 0 {
		rest = rest[:end]
	}
	// Ordinary receipt footer starts after component sections.
	if end := strings.Index(rest, "\nDesign checked within"); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

func evidence296Visible(s string) string {
	return html.UnescapeString(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(s, ""))
}

func evidence296Contains(t *testing.T, s string, values ...string) {
	t.Helper()
	s = evidence296Visible(s)
	for _, value := range values {
		if !strings.Contains(s, value) {
			t.Errorf("receipt omits visible %q", value)
		}
	}
}

func evidence296Record(t *testing.T, section, header, target, check string) []string {
	t.Helper()
	rows := explicit247Table(t, section, header)
	for _, row := range rows {
		if len(row) != 4 {
			t.Fatalf("evidence row has %d cells, want 4: %#v", len(row), row)
		}
		if evidence296Visible(row[0]) == target && evidence296Visible(row[1]) == check {
			return row
		}
	}
	t.Fatalf("missing evidence association %q / %q", target, check)
	return nil
}

func evidence296Coverage(t *testing.T, md string, r verify.Report) {
	t.Helper()
	rows := explicit247Table(t, md, evidence296Production)
	if len(rows) != len(r.Components.Inventory.Components) {
		t.Fatalf("coverage has %d rows for %d production components", len(rows), len(r.Components.Inventory.Components))
	}
	want := map[string]string{}
	for _, p := range r.Components.Proofs {
		want[p.Target] = p.Status
	}
	for _, row := range rows {
		if len(row) != 5 {
			t.Fatalf("coverage cells changed: %#v", row)
		}
		id := evidence296Visible(row[0])
		if want[id] == "" || strings.TrimSpace(row[1]) != want[id] {
			t.Errorf("proof association %q = %q, want %q", id, row[1], want[id])
		}
		delete(want, id)
	}
	if len(want) > 0 {
		t.Errorf("production components missing: %v", want)
	}
}

func TestComponentReceiptsExposeEveryIndependentEvidenceKind(t *testing.T) {
	f := evidence296Fixture(t)
	r := f.report(t)
	md := evidence296Render(t, &r)
	evidence296Coverage(t, md, r)
	if r.Claim() != "component evidence" || r.CodeAssurance() != "component evidence" || r.EverySize() || r.Passed {
		t.Fatal("component evidence inflated aggregate assurance or gate outcome")
	}
	for _, bad := range []string{"The code is **proved", "at every size", "from tool output only"} {
		if strings.Contains(md, bad) {
			t.Errorf("explicit receipt overclaims with %q", bad)
		}
	}
	evidence296Contains(t, md, "API runs no tools", "supplied results", "declared assumptions")
	conformance := evidence296Section(t, md, "conformance")
	runtime := evidence296Section(t, md, "runtime")
	assumptions := evidence296Section(t, md, "assumptions")
	for _, c := range r.Components.Conformance {
		row := evidence296Record(t, conformance, evidence296Checks, c.Target, c.Check)
		if row[2] != "tested" {
			t.Errorf("conformance status = %q", row[2])
		}
		evidence296Contains(t, row[3], c.Result.Invocation.ID, c.Result.Runner.Name, c.Result.Runner.Version, c.Result.Mode, c.Result.Outcome, c.Result.Model.Module, "Limit", "2", "3 runs", "11 steps", "5 states", "7 transitions", "5 model states", "conformance-detail")
	}
	for _, rr := range r.Components.Runtime {
		row := evidence296Record(t, runtime, evidence296Checks, rr.Target, rr.Check)
		if row[2] != "tested" {
			t.Errorf("runtime qualification = %q", row[2])
		}
		evidence296Contains(t, row[3], "runtime-invocation", "runtime-runner", "assert-count", "exercised", "passed", "test-detail", "assertion-detail", "runtime-detail")
	}
	if len(explicit247Table(t, assumptions, evidence296AssumptionTable)) != 2 {
		t.Fatal("missing or fabricated supplied assumption records")
	}
	for _, a := range r.Components.Assumptions {
		row := evidence296Record(t, assumptions, evidence296AssumptionTable, a.Target, a.Record.Assumption.ID)
		if row[2] != "assumed / unverified" {
			t.Errorf("assumption is not explicitly assumed and unverified: %q", row[2])
		}
		evidence296Contains(t, row[3], a.Record.Assumption.Statement, a.Record.Assumption.Reason, a.Record.Assumption.Source.Path, a.Record.Assumption.Source.SHA256[:19], a.Record.Input.Source, a.Record.Input.SourceSHA256[:19])
	}
	footer := explicit247Line(t, md, "<sub>")
	evidence296Contains(t, footer, "supplied", "declared assumptions", "assumed", "unverified")
	if strings.Contains(strings.ToLower(footer), "tool output only") {
		t.Fatal("footer calls assumptions tool output")
	}
	// Actual proof provenance is also visible, independently of other kinds.
	evidence296Contains(t, md, "proof-invocation", "proof-tool", "v2.9", "not-an-executable-proof", "first-argument", "second-argument", "EXPLICIT_ENV", "value", "seed", "17")
	for _, c := range f.Inv.Components {
		evidence296Contains(t, md, c.ID, c.Source, c.SourceSHA256[:19])
		if c.ContractSHA256 != "" {
			evidence296Contains(t, md, c.ContractSHA256[:19])
		}
	}
	// Input-only declarations and files have their own view, never production rows.
	provenance := evidence296Section(t, md, "input provenance")
	for _, c := range f.Inv.Provenance.Declarations {
		evidence296Contains(t, provenance, c.ID, c.Source, c.SourceSHA256[:19])
	}
	for _, files := range [][]project.ComponentFile{f.Inv.Provenance.Files, f.Inv.Provenance.Modules} {
		for _, file := range files {
			evidence296Contains(t, provenance, file.Path, file.SHA256[:19])
		}
	}
	for _, pin := range []string{"registry.test/runner@sha256:" + strings.Repeat("a", 12), "sha256:" + strings.Repeat("b", 12)} {
		evidence296Contains(t, conformance, pin)
		evidence296Contains(t, runtime, pin)
	}
	// Inspect serialized fields, not just the Go view, including false booleans.
	wire := evidence296Clone(t, r)
	if !reflect.DeepEqual(wire.Components, r.Components) {
		t.Fatal("component JSON lost evidence or provenance")
	}
	for _, a := range wire.Components.Assumptions {
		if a.Status != "assumed" || a.Verified {
			t.Fatal("JSON verifies an assumption")
		}
	}
	var raw struct {
		Components struct {
			Assumptions []map[string]json.RawMessage `json:"assumptions"`
			Conformance []map[string]json.RawMessage `json:"conformance"`
		} `json:"components"`
	}
	encoded, _ := json.Marshal(r)
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	for _, a := range raw.Components.Assumptions {
		if string(a["verified"]) != "false" {
			t.Error("assumption JSON must explicitly contain verified:false")
		}
	}
	for _, c := range raw.Components.Conformance {
		for _, key := range []string{"transitions_checked", "exhaustive_states", "every_step"} {
			if _, ok := c[key]; !ok {
				t.Errorf("conformance JSON omitted qualification %s", key)
			}
		}
	}
	for _, p := range wire.Components.Proofs {
		if p.Target != f.Proofs[0].Target && p.Status != "unchecked" {
			t.Fatal("proof leaked to a neighboring target or contract")
		}
	}
}

func TestComponentReceiptConformanceClaimsRequireTheRightObservations(t *testing.T) {
	for _, tc := range []struct {
		name, mode, outcome, attempt string
		attempts, states             int64
		absent                       bool
		tested, exhaustive, every    bool
	}{
		{"exhaustive with attempts", "bounded-exhaustive", "passed", "passed", 13, 5, false, true, true, true},
		{"exhaustive without attempts", "bounded-exhaustive", "passed", "", 0, 0, true, true, true, false},
		{"failed attempts preserve observations", "bounded-exhaustive", "passed", "failed", 13, 5, false, true, false, false},
		{"empty successful attempts", "bounded-exhaustive", "passed", "passed", 0, 0, false, true, false, false},
		{"timed out attempts", "bounded-exhaustive", "passed", "timed_out", 13, 5, false, true, false, false},
		{"skipped attempts", "bounded-exhaustive", "passed", "skipped", 13, 5, false, true, false, false},
		{"empty attempts", "bounded-exhaustive", "passed", "empty", 0, 0, false, true, false, false},
		{"missing attempt outcome", "bounded-exhaustive", "passed", "", 13, 5, false, true, false, false},
		{"sampled", "sampled", "passed", "passed", 13, 5, false, true, false, true},
		{"failed conformance", "bounded-exhaustive", "failed", "passed", 13, 5, false, false, false, false},
		{"timed out conformance", "bounded-exhaustive", "timed_out", "passed", 13, 5, false, false, false, false},
		{"skipped conformance", "bounded-exhaustive", "skipped", "passed", 13, 5, false, false, false, false},
		{"empty conformance", "bounded-exhaustive", "empty", "passed", 13, 5, false, false, false, false},
		{"missing conformance outcome", "bounded-exhaustive", "", "passed", 13, 5, false, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := evidence296Fixture(t)
			c := &f.Conformance[0]
			f.Conformance = f.Conformance[:1]
			c.Mode, c.Outcome = tc.mode, tc.outcome
			if tc.absent {
				c.Attempts = nil
			} else {
				c.Attempts.Outcome, c.Attempts.Attempts, c.Attempts.States = tc.attempt, tc.attempts, tc.states
			}
			r := f.report(t)
			md := evidence296Render(t, &r)
			section := evidence296Section(t, md, "conformance")
			e := r.Components.Conformance[0]
			want := "unchecked"
			if tc.tested {
				want = "tested"
			}
			if e.Status != want || e.ExhaustiveStates != tc.exhaustive || e.EveryStep != tc.every {
				t.Fatalf("unexpected API qualifications: %+v", e)
			}
			row := evidence296Record(t, section, evidence296Checks, e.Target, e.Check)
			if row[2] != want {
				t.Errorf("receipt qualification %q, want %q", row[2], want)
			}
			state, step := "not established", "not established"
			if tc.absent {
				step = "not checked"
			}
			if tc.exhaustive {
				state = "tested in every state"
			}
			if tc.every {
				step = "every step tried"
			}
			evidence296Contains(t, row[3], "State coverage: "+state, "Attempt coverage: "+step, "conformance-detail")
			if !tc.exhaustive && strings.Contains(evidence296Visible(row[3]), "tested in every state") {
				t.Error("unqualified exhaustive-state claim")
			}
			if !tc.every && strings.Contains(evidence296Visible(row[3]), "every step tried") {
				t.Error("unqualified every-step claim")
			}
			if c.Outcome != "" {
				evidence296Contains(t, row[3], c.Outcome)
			} else {
				evidence296Contains(t, row[3], "outcome not supplied")
			}
			if c.Attempts != nil {
				evidence296Contains(t, row[3], "attempt-detail")
				if tc.attempt != "" {
					evidence296Contains(t, row[3], tc.attempt)
				}
			}
		})
	}
	for _, counts := range []struct {
		name                       string
		states, modelStates, steps int64
	}{{"no observations", 0, 0, 0}, {"unequal state counts", 4, 5, 11}} {
		t.Run(counts.name, func(t *testing.T) {
			f := evidence296Fixture(t)
			f.Conformance = f.Conformance[:1]
			c := &f.Conformance[0]
			c.States, c.ModelStates, c.Steps = counts.states, counts.modelStates, counts.steps
			r := f.report(t)
			md := evidence296Render(t, &r)
			row := evidence296Record(t, evidence296Section(t, md, "conformance"), evidence296Checks, c.Target, c.Check)
			evidence296Contains(t, row[3], "State coverage: not established")
			if strings.Contains(evidence296Visible(row[3]), "tested in every state") {
				t.Fatal("state counts forged exhaustive coverage")
			}
		})
	}
}

func TestComponentReceiptRuntimeDoesNotInferExerciseFromProcessSuccess(t *testing.T) {
	for _, tc := range []struct {
		name, process, test, assertion     string
		exercised, asserted, source, tests bool
		status, testStatus, assertStatus   string
	}{
		{"actual success", "passed", "passed", "passed", true, true, true, true, "tested", "tested", "passed"},
		{"passing process without tests", "passed", "passed", "passed", true, true, true, false, "unchecked", "", ""},
		{"unexercised test", "passed", "passed", "passed", false, true, true, true, "unchecked", "unchecked", "unchecked"},
		{"unexercised assertion", "passed", "passed", "passed", true, false, true, true, "tested", "tested", "unchecked"},
		{"missing assertion source", "passed", "passed", "passed", true, true, false, true, "tested", "tested", "unchecked"},
		{"failed assertion", "passed", "passed", "failed", true, true, true, true, "tested", "tested", "unchecked"},
		{"failed test preserves assertion", "passed", "failed", "passed", true, true, true, true, "unchecked", "unchecked", "passed"},
		{"failed process preserves observations", "failed", "passed", "passed", true, true, true, true, "unchecked", "tested", "passed"},
		{"timed out", "timed_out", "timed_out", "timed_out", true, true, true, true, "unchecked", "unchecked", "unchecked"},
		{"skipped", "skipped", "skipped", "skipped", false, false, true, true, "unchecked", "unchecked", "unchecked"},
		{"empty", "empty", "empty", "empty", false, false, true, true, "unchecked", "unchecked", "unchecked"},
		{"missing outcomes", "", "", "", true, true, true, true, "unchecked", "unchecked", "unchecked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := evidence296Fixture(t)
			rr := &f.Runtime[0]
			rr.Outcome = tc.process
			tr := &rr.Tests[0]
			tr.Outcome, tr.Exercised = tc.test, tc.exercised
			ar := &tr.Assertions[0]
			ar.Outcome, ar.Exercised = tc.assertion, tc.asserted
			if !tc.source {
				ar.Source = nil
			}
			if !tc.tests {
				rr.Tests = nil
			}
			r := f.report(t)
			md := evidence296Render(t, &r)
			section := evidence296Section(t, md, "runtime")
			row := evidence296Record(t, section, evidence296Checks, rr.Target, rr.Check)
			if row[2] != tc.status || r.Components.Runtime[0].Status != tc.status {
				t.Errorf("process outcome supplied runtime success: %q", row[2])
			}
			evidence296Contains(t, row[3], "Process outcome:", "runtime-detail")
			if tc.process == "" {
				evidence296Contains(t, row[3], "outcome not supplied")
			} else {
				evidence296Contains(t, row[3], tc.process)
			}
			if tc.tests {
				evidence296Contains(t, row[3], "Test status: "+tc.testStatus, "Assertion status: "+tc.assertStatus, "assert-count", "test-detail", "assertion-detail")
				e := r.Components.Runtime[0].Tests[0]
				if e.Status != tc.testStatus || e.Assertions[0].Status != tc.assertStatus {
					t.Fatal("JSON lost independent test/assertion qualifications")
				}
			} else {
				evidence296Contains(t, row[3], "no supplied test results")
				if strings.Contains(row[3], "assert-count") {
					t.Error("invented assertion")
				}
			}
			evidence296Contains(t, section, "does not establish target code coverage")
			evidence296Coverage(t, md, r)
		})
	}
}

func TestComponentReceiptSelectionsCompatibilityAndNoFabricatedResults(t *testing.T) {
	f := evidence296Fixture(t)
	f.Proofs = nil
	f.Conformance = nil
	f.Runtime = nil
	f.Assumptions = nil
	r := f.report(t)
	md := evidence296Render(t, &r)
	evidence296Coverage(t, md, r)
	// Every configured kind is identified as a selection, and none becomes an
	// actual result. All assumptions, including configuration-only ones, are inert.
	evidence296Contains(t, md, "selected tests", "selected conformance", "declared assumptions", "no supplied results", "clock-premise", "The supplied clock is monotonic.", "Caller policy, not an executed check.", "API runs no tools")
	for _, kind := range []string{"conformance", "runtime", "assumptions"} {
		section := evidence296Section(t, md, kind)
		header := evidence296Checks
		if kind == "assumptions" {
			header = evidence296AssumptionTable
		}
		if rows := explicit247Table(t, section, header); len(rows) != 0 {
			t.Errorf("%s fabricated supplied records from selections: %#v", kind, rows)
		}
	}
	if len(r.Components.Conformance)+len(r.Components.Runtime)+len(r.Components.Assumptions) != 0 {
		t.Fatal("empty API calls synthesized results")
	}
	encoded, _ := json.Marshal(r.Components)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"conformance", "runtime", "assumptions"} {
		if _, exists := fields[key]; exists {
			t.Errorf("empty optional JSON field %s was not omitted", key)
		}
	}
	// The old proof-only shape has no provenance or new fields. New receipt prose
	// must truthfully explain supplied results even for that round trip.
	old := explicit247Report(t, "core/step.go", true)
	old.Assurance = "proved at every size"
	old = evidence296Clone(t, old)
	oldMD := evidence296Render(t, &old)
	evidence296Coverage(t, oldMD, old)
	if old.Claim() != "component evidence" || old.EverySize() {
		t.Fatal("stale aggregate JSON label inflated explicit assurance")
	}
	evidence296Contains(t, oldMD, "API runs no tools")
	if strings.Contains(oldMD, "at every size") {
		t.Fatal("old aggregate label leaked into component receipt")
	}
	// Keep successful legacy Go/Python/TypeScript bytes, fingerprints and
	// unverified-function lists. No optional component fields appear in legacy JSON.
	goldens := []struct{ fp, md string }{
		{"sha256:12362a3a9c164fea85ae6ea6ff4bf623d16ed3136adfbbdb211ea8161007453a", "fa2c20aa584f9ba051a6e8f89b97843086750f307ac6e09d0c1b646273381e15"},
		{"sha256:8d0e0ac4ecf82433ffb6f0971b45a14bafc96fa10407766f3bcae0b76dc50fda", "dd24cf0587a3a16d7f372d38f562402c2f3a6738b4587ee064184dd192d91728"},
		{"sha256:295f04994adf44460b659ba6ded42792ceab506707fae3516ef90e7fab2d475e", "040b3e9231b957ca2bd8e2ff20585fde0798b6b392d6ab9e750b51c5908e447d"},
	}
	for i, legacy := range receiptAssuranceFixtures() {
		legacy.Fingerprint = verify.Fingerprint(legacy)
		original := evidence296Clone(t, legacy)
		got := evidence296Render(t, &legacy)
		if legacy.Fingerprint != goldens[i].fp || fmt.Sprintf("%x", sha256.Sum256([]byte(got))) != goldens[i].md || !reflect.DeepEqual(legacy.Code, original.Code) {
			t.Errorf("legacy %s changed", legacy.Project)
		}
		b, _ := json.Marshal(legacy)
		if strings.Contains(string(b), `"components"`) {
			t.Error("legacy JSON gained component evidence")
		}
	}
}
