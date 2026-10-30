package verify_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

func cri287Rich(t *testing.T, f ce274FixtureData) cr287Result {
	t.Helper()
	r := cr287Good(t, f, "", "")
	cr287Context(t, f, &r)
	other := cr287Good(t, f, provenance257ID("tests/step_test.go", "TestAgain"), "")
	r.Inputs = append(r.Inputs, ce274Component(t, f.Inv, other.Check))
	r.Tests = append(r.Tests, other.Tests[0])
	a := cpClone(t, r.Tests[0].Assertions[0])
	a.ID, a.Outcome = "assert-b", "failed"
	r.Tests[0].Assertions = append(r.Tests[0].Assertions, a)
	return r
}

func cri287All(t *testing.T, f ce274FixtureData, results ...cr287Result) verify.Report {
	t.Helper()
	r := ce274Attach(t, f, true)
	c := cc279Good(t, f, f.Target, "", "")
	c.Mode, c.Attempts = "bounded-exhaustive", cc279AttemptGood()
	if err := cc279Call(t, &r, f.Inv, []cc279Result{c}); err != nil {
		t.Fatal(err)
	}
	if err := cr287Call(t, &r, f.Inv, results); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestComponentRuntimeFingerprintBindsEverySuppliedField(t *testing.T) {
	f := cr287Fixture(t)
	cr287RequireAPI(t, f)
	result := cr287Good(t, f, "", "")
	cr287Context(t, f, &result)
	r := cr287Attach(t, f, result)
	rows := cr287Rows(t, r)
	if len(rows) != 1 {
		t.Fatalf("got %d runtime records", len(rows))
	}
	cr287WantResult(t, rows[0], result)
	baseline := verify.Fingerprint(r)
	if baseline != r.Fingerprint {
		t.Fatal("runtime attribution did not finalize its fingerprint")
	}
	type mutation struct {
		path  []any
		value any
		kind  string
	}
	var mutations []mutation
	add := func(path []any, value any, kind string) {
		mutations = append(mutations, mutation{slices.Clone(path), value, kind})
	}
	var visit func(any, []any)
	visit = func(value any, path []any) {
		switch v := value.(type) {
		case map[string]any:
			add(path, nil, "presence")
			keys := make([]string, 0, len(v))
			for key := range v {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			for _, key := range keys {
				visit(v[key], append(slices.Clone(path), key))
			}
			if name, ok := path[len(path)-1].(string); ok && slices.Contains([]string{"env", "sampling", "pins", "values"}, name) {
				for _, key := range keys {
					changed := cpClone(t, v)
					changed["different-"+key] = changed[key]
					delete(changed, key)
					add(path, changed, "key-"+key)
				}
			}
		case []any:
			if len(v) > 0 {
				add(path, []any{}, "presence")
			}
			for i, child := range v {
				visit(child, append(slices.Clone(path), i))
			}
		case string:
			add(path, v+"-changed", "value")
		case bool:
			add(path, !v, "value")
		case float64:
			add(path, v+1, "value")
		default:
			t.Fatalf("unexpected runtime fixture value %T at %v", value, path)
		}
	}
	// Walking the expected wire contract exposes fields silently lost during
	// decoding or attribution, even when the inventory remains unchanged.
	want := cr287Evidence{Target: result.Target, Check: result.Check, Status: "tested", Result: cr287CanonicalResult(cpClone(t, result)),
		Tests: []cr287TestEvidence{{Check: result.Check, Status: "tested", Assertions: []cr287AssertionEvidence{{ID: "assert-a", Status: "passed"}}}}}
	visit(ci274Object(t, want), []any{"components", "runtime", 0})
	add([]any{"components", "runtime"}, []any{}, "presence")
	for _, m := range mutations {
		t.Run(fmt.Sprintf("%v/%s", m.path, m.kind), func(t *testing.T) {
			obj := ci274Object(t, r)
			ci274At(t, obj, m.path...)
			parent := ci274At(t, obj, m.path[:len(m.path)-1]...)
			switch key := m.path[len(m.path)-1].(type) {
			case string:
				parent.(map[string]any)[key] = m.value
			case int:
				parent.([]any)[key] = m.value
			}
			decoded := cci279Decode(t, obj)
			before := string(cpJSON(t, decoded))
			if verify.Fingerprint(decoded) == baseline {
				t.Fatal("fingerprint ignored supplied runtime identity, outcome, source, diagnostics or execution metadata")
			}
			if string(cpJSON(t, decoded)) != before {
				t.Fatal("Fingerprint mutated decoded runtime evidence")
			}
		})
	}
	for name, edit := range map[string]func(*cr287Result){
		"runner version":         func(x *cr287Result) { x.Runner.Version = "2.0.0" },
		"runner pin":             func(x *cr287Result) { x.Runner.Pins["image"] = "runner@sha256:changed" },
		"missing runner":         func(x *cr287Result) { x.Runner = nil },
		"process outcome":        func(x *cr287Result) { x.Outcome = "failed" },
		"test outcome":           func(x *cr287Result) { x.Tests[0].Outcome = "timed_out" },
		"test exercised":         func(x *cr287Result) { x.Tests[0].Exercised = false },
		"assertion outcome":      func(x *cr287Result) { x.Tests[0].Assertions[0].Outcome = "skipped" },
		"assertion exercised":    func(x *cr287Result) { x.Tests[0].Assertions[0].Exercised = false },
		"assertion identity":     func(x *cr287Result) { x.Tests[0].Assertions[0].ID = "assert-other" },
		"assertion source":       func(x *cr287Result) { x.Tests[0].Assertions[0].Source = nil },
		"assertion diagnostics":  func(x *cr287Result) { x.Tests[0].Assertions[0].Diagnostics[0] = "new assertion diagnostic" },
		"test diagnostics":       func(x *cr287Result) { x.Tests[0].Diagnostics[0] = "new test diagnostic" },
		"runtime diagnostics":    func(x *cr287Result) { x.Diagnostics[0] = "new process diagnostic" },
		"unreported test":        func(x *cr287Result) { x.Tests = nil },
		"unreported assertions":  func(x *cr287Result) { x.Tests[0].Assertions = nil },
		"selected invocation":    func(x *cr287Result) { x.Invocation.ID = "repeat" },
		"additional declaration": func(x *cr287Result) { x.Inputs = append(x.Inputs, ce274Component(t, f.Inv, f.Other)) },
		"additional file": func(x *cr287Result) {
			x.FileInputs = slices.DeleteFunc(x.FileInputs, func(file project.ComponentFile) bool { return file.Path == "data/second.bin" })
		},
		"optional model":  func(x *cr287Result) { x.Model = nil },
		"optional bounds": func(x *cr287Result) { x.Bounds = nil },
	} {
		t.Run("accepted update/"+name, func(t *testing.T) {
			x, changed := cpClone(t, r), cpClone(t, result)
			edit(&changed)
			if err := cr287Call(t, &x, f.Inv, []cr287Result{changed}); err != nil {
				t.Fatal(err)
			}
			if x.Fingerprint == baseline || x.Fingerprint != verify.Fingerprint(x) {
				t.Fatal("accepted runtime change did not produce a new finalized fingerprint")
			}
			cr287WantResult(t, cr287Rows(t, x)[0], changed)
		})
	}
}

func TestComponentRuntimeCanonicalSetsSequencesAndRelocation(t *testing.T) {
	f := cr287Fixture(t)
	cr287RequireAPI(t, f)
	results := []cr287Result{cri287Rich(t, f), cr287Good(t, f, "", "repeat"), cr287Good(t, f, provenance257ID("tests/step_test.go", "TestAgain"), "run")}
	r := cri287All(t, f, results...)
	canonical := string(cpJSON(t, r))
	obj := ci274Object(t, r)
	cci279ReverseSets(obj["components"])
	decoded := cci279Decode(t, obj)
	before := string(cpJSON(t, decoded))
	if verify.Fingerprint(decoded) != r.Fingerprint || string(cpJSON(t, decoded)) != before {
		t.Fatal("decoded reference sets or result completion order changed the fingerprint or mutated evidence")
	}
	var permuted struct {
		Inventory project.ComponentInventory `json:"inventory"`
		Proofs    []struct {
			Result *ce274Proof `json:"result"`
		} `json:"proofs"`
		Assumptions []ce274AssumptionEvidence `json:"assumptions"`
		Conformance []cc279Evidence           `json:"conformance"`
		Runtime     []cr287Evidence           `json:"runtime"`
	}
	if err := json.Unmarshal(cpJSON(t, obj["components"]), &permuted); err != nil {
		t.Fatal(err)
	}
	var proofs []ce274Proof
	var assumptions []ce274AssumptionRecord
	var conformance []cc279Result
	var runtime []cr287Result
	for _, row := range permuted.Proofs {
		if row.Result != nil {
			proofs = append(proofs, *row.Result)
		}
	}
	for _, row := range permuted.Assumptions {
		assumptions = append(assumptions, row.Record)
	}
	for _, row := range permuted.Conformance {
		conformance = append(conformance, row.Result)
	}
	for _, row := range permuted.Runtime {
		runtime = append(runtime, row.Result)
	}
	x := ce274Seed()
	for _, op := range []func() error{
		func() error { return cr287Call(t, &x, &permuted.Inventory, runtime) },
		func() error { return cc279Call(t, &x, &permuted.Inventory, conformance) },
		func() error { return ce274Assumptions(t, &x, &permuted.Inventory, assumptions) },
		func() error { return ce274Proofs(t, &x, &permuted.Inventory, proofs) },
	} {
		if err := op(); err != nil {
			t.Fatal(err)
		}
	}
	if string(cpJSON(t, x)) != canonical {
		t.Fatal("set, completion or kind attribution order changed canonical storage")
	}
	if err := cr287Call(t, &r, &permuted.Inventory, runtime); err != nil {
		t.Fatal(err)
	}
	if string(cpJSON(t, r)) != canonical {
		t.Fatal("equivalent reordered inventory changed retained kinds")
	}
	relocated := cr287Fixture(t)
	if relocated.Root == f.Root || !reflect.DeepEqual(relocated.Inv, f.Inv) {
		t.Fatal("relocation fixture lacks identical relative snapshots")
	}
	x = cri287All(t, relocated, results...)
	if string(cpJSON(t, x)) != canonical {
		t.Fatal("checkout relocation changed runtime evidence")
	}
	index, testIndex := -1, -1
	for i, row := range cr287Rows(t, r) {
		for j, test := range row.Result.Tests {
			if len(test.Assertions) == 2 {
				index, testIndex = i, j
			}
		}
	}
	if index < 0 {
		t.Fatal("canonicalization lost nested runtime assertions")
	}
	prefix := []any{"components", "runtime", index, "result"}
	paths := [][]any{{"invocation", "args"}, {"diagnostics"}, {"tests", testIndex, "diagnostics"}, {"tests", testIndex, "assertions", 0, "diagnostics"}}
	var sequences [][]any
	for _, path := range paths {
		sequences = append(sequences, append(slices.Clone(prefix), path...))
	}
	for _, path := range [][]any{{"observations"}, {"observations", 0, "args"}, {"diagnostics"}, {"attempts", "observations"}, {"attempts", "observations", 0, "args"}, {"attempts", "diagnostics"}} {
		sequences = append(sequences, append([]any{"components", "conformance", 0, "result"}, path...))
	}
	for _, path := range sequences {
		t.Run(fmt.Sprint(path), func(t *testing.T) {
			obj := ci274Object(t, r)
			seq := ci274At(t, obj, path...).([]any)
			if len(seq) < 2 {
				t.Fatal("ordered-sequence fixture needs distinct entries")
			}
			slices.Reverse(seq)
			if verify.Fingerprint(cci279Decode(t, obj)) == r.Fingerprint {
				t.Fatal("meaningful argument, diagnostic or observation order was treated as a set")
			}
		})
	}
	changed := f
	changed.Inv = cpClone(t, f.Inv)
	for i := range changed.Inv.Checks {
		for j := range changed.Inv.Checks[i].Invocations {
			v := &changed.Inv.Checks[i].Invocations[j]
			if v.Kind == "test" {
				slices.Reverse(v.Args)
			}
		}
	}
	x = cri287All(t, changed, cri287Rich(t, changed), cr287Good(t, changed, "", "repeat"), cr287Good(t, changed, provenance257ID("tests/step_test.go", "TestAgain"), "run"))
	if x.Fingerprint == r.Fingerprint {
		t.Fatal("matching changed actual invocation argument order did not change fingerprint")
	}
	volatile := cpClone(t, r)
	volatile.Bugs = []verify.Bug{{Name: "Bug", Trace: "/first/trace"}}
	volatile.Properties = []verify.Property{{Name: "Live", Trace: "/first/property"}}
	initial := verify.Fingerprint(volatile)
	volatile.GeneratedAt, volatile.Agent, volatile.Build.Output, volatile.Toolchain.Go = "tomorrow", "another agent", "timings", "go1.27.99"
	volatile.Bugs[0].Trace, volatile.Properties[0].Trace = "/relocated/trace", "/relocated/property"
	volatile.Assurance = "proved"
	volatile = cpClone(t, volatile)
	if verify.Fingerprint(volatile) != initial || volatile.CodeAssurance() != "component evidence" || volatile.Claim() != "component evidence" || volatile.EverySize() {
		t.Fatal("volatile data or decoded stale assurance changed runtime evidence assurance")
	}
}

func TestComponentRuntimeOwnsTypedArgumentsInventoryAndRetainedKinds(t *testing.T) {
	f := cr287Fixture(t)
	cr287RequireAPI(t, f)
	for _, passed := range []bool{true, false} {
		t.Run(fmt.Sprintf("gatePassed=%v", passed), func(t *testing.T) {
			r := ce274Seed()
			r.Passed = passed
			proofInv, assumptionInv, conformanceInv, runtimeInv := cpClone(t, f.Inv), cpClone(t, f.Inv), cpClone(t, f.Inv), cpClone(t, f.Inv)
			proof := ce274ProofFor(t, f, f.Target, true)
			proof.Errors = []string{"retained proof diagnostic"}
			var proofs []verify.ComponentProofResult
			if err := json.Unmarshal(cpJSON(t, []ce274Proof{proof}), &proofs); err != nil {
				t.Fatal(err)
			}
			if err := r.AttributeComponentProofs(proofInv, proofs); err != nil {
				t.Fatal(err)
			}
			assumptionMethod := reflect.ValueOf(&r).MethodByName("AttributeComponentAssumptions")
			assumptions := reflect.New(assumptionMethod.Type().In(1))
			if err := json.Unmarshal(cpJSON(t, ce274Records(t, f)), assumptions.Interface()); err != nil {
				t.Fatal(err)
			}
			if out := assumptionMethod.Call([]reflect.Value{reflect.ValueOf(assumptionInv), assumptions.Elem()})[0]; !out.IsNil() {
				t.Fatal(out.Interface())
			}
			c := cc279Good(t, f, f.Target, "", "")
			c.Mode, c.Attempts = "bounded-exhaustive", cc279AttemptGood()
			cm, ca := cc279CallArg(t, &r, conformanceInv, []cc279Result{c})
			if out := cm.Call([]reflect.Value{reflect.ValueOf(conformanceInv), ca})[0]; !out.IsNil() {
				t.Fatal(out.Interface())
			}
			result := cri287Rich(t, f)
			if !passed {
				result.Outcome, result.Tests[0].Outcome = "failed", "failed"
			}
			rm, ra := cr287CallArg(t, &r, runtimeInv, []cr287Result{result})
			if out := rm.Call([]reflect.Value{reflect.ValueOf(runtimeInv), ra})[0]; !out.IsNil() {
				t.Fatal(out.Interface())
			}
			before, stored, recomputed := string(cpJSON(t, r)), r.Fingerprint, verify.Fingerprint(r)
			// These are the concrete typed values passed to all four public APIs,
			// not the independent JSON helpers used to construct those arguments.
			for _, actual := range []reflect.Value{reflect.ValueOf(proofs), assumptions.Elem(), ca, ra, reflect.ValueOf(proofInv), reflect.ValueOf(assumptionInv), reflect.ValueOf(conformanceInv), reflect.ValueOf(runtimeInv)} {
				original := string(cpJSON(t, actual.Interface()))
				provenance257Mutate(actual)
				if string(cpJSON(t, actual.Interface())) == original {
					t.Fatal("caller argument mutation did not exercise ownership")
				}
				if string(cpJSON(t, r)) != before || r.Fingerprint != stored || verify.Fingerprint(r) != recomputed {
					t.Fatal("report aliases caller-owned nested evidence, inventory, tests, assertion sources, diagnostics, maps or pins")
				}
			}
			for _, kind := range []string{"proof", "conformance", "runtime", "assumptions"} {
				for _, clear := range []bool{false, true} {
					x := cpClone(t, r)
					old := x.Components
					ps, cs, rs, as := []ce274Proof{proof}, []cc279Result{c}, []cr287Result{result}, ce274Records(t, f)
					if clear {
						ps, cs, rs, as = nil, nil, nil, nil
					}
					var err error
					switch kind {
					case "proof":
						err = ce274Proofs(t, &x, f.Inv, ps)
					case "conformance":
						err = cc279Call(t, &x, f.Inv, cs)
					case "runtime":
						err = cr287Call(t, &x, f.Inv, rs)
					case "assumptions":
						err = ce274Assumptions(t, &x, f.Inv, as)
					}
					if err != nil {
						t.Fatal(err)
					}
					want, fingerprint := string(cpJSON(t, x)), x.Fingerprint
					provenance257Mutate(reflect.ValueOf(old))
					if string(cpJSON(t, x)) != want || x.Fingerprint != fingerprint || verify.Fingerprint(x) != fingerprint {
						t.Fatalf("%s clear=%v aliases the previous inventory or retained evidence", kind, clear)
					}
				}
			}
		})
	}
}

func TestComponentRuntimeAttributionNeverExecutesAndModelOnlyRejectsIt(t *testing.T) {
	f := cr287Fixture(t)
	bin := t.TempDir()
	log := filepath.Join(bin, "called")
	t.Setenv("COMPONENT_287_TOOL_LOG", log)
	for _, name := range []string{"go", "docker", "java", "tlc", "provenance-command", "supplied-test-runner", "component-runtime-sentinel"} {
		cpWrite(t, bin, name, "#!/bin/sh\necho called >> \"$COMPONENT_287_TOOL_LOG\"\nexit 73\n")
		if err := os.Chmod(filepath.Join(bin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	provenance257EditManifest(t, f.Root, func(m map[string]any) {
		for _, item := range provenance257ConfigCheck(m, "Step")["invocations"].([]any) {
			call := item.(map[string]any)
			if call["kind"] != "test" {
				continue
			}
			call["command"] = "component-runtime-sentinel"
			if call["id"] == "repeat" {
				call["command"] = "component-runtime-sentinel; component-runtime-sentinel"
			}
			call["args"] = []string{"$(component-runtime-sentinel)", "`component-runtime-sentinel`", "; component-runtime-sentinel"}
			call["env"] = map[string]string{"EXPANSION": "$(component-runtime-sentinel)"}
			call["sampling"] = map[string]string{"opaque": "`component-runtime-sentinel`"}
		}
	})
	f.Inv = cpResolve(t, f.Root)
	cr287RequireAPI(t, f)
	results := []cr287Result{cr287Good(t, f, "", ""), cr287Good(t, f, "", "repeat")}
	for i := range results {
		results[i].Runner.Name, results[i].Runner.Version = "component-runtime-sentinel", "$(component-runtime-sentinel)"
		results[i].Runner.Pins["command"] = "`component-runtime-sentinel`"
	}
	r := cri287All(t, f, results...)
	if err := cr287Call(t, &r, f.Inv, nil); err != nil {
		t.Fatal(err)
	}
	if err := verify.Check(f.Root); !errors.Is(err, verify.ErrComponents) {
		t.Fatalf("explicit preflight must reject before tools: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if report, err := verify.Run(ctx, f.Root, "", toolchain.Toolchain{}); report != nil || !errors.Is(err, verify.ErrComponents) {
		t.Fatalf("explicit verification started an execution path: %#v, %v", report, err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("supplied attribution, clearing or rejected explicit verification executed a command")
	}
	model, err := verify.RunModel(ctx, f.Root, toolchain.Toolchain{})
	if err != nil || model == nil || !model.ModelOnly {
		t.Fatalf("model-only checking became unavailable: %#v, %v", model, err)
	}
	called, err := os.ReadFile(log)
	if err != nil || len(called) == 0 {
		t.Fatalf("model-only checking did not reach stub tools: %v", err)
	}
	ce274Unchanged(t, model, func() error { return cr287Call(t, model, f.Inv, results) })
	ce274Unchanged(t, model, func() error { return cr287Call(t, model, f.Inv, nil) })
	after, err := os.ReadFile(log)
	if err != nil || string(after) != string(called) {
		t.Fatal("rejected model-only runtime attribution executed another tool")
	}
}

func TestComponentRuntimeMarkdownPreservesProofViewAndLegacyCompatibility(t *testing.T) {
	f := cr287Fixture(t)
	cr287RequireAPI(t, f)
	r := cri287All(t, f)
	before := receipt.Markdown(&r)
	result := cri287Rich(t, f)
	result.Diagnostics = []string{"SUPPLIED-RUNTIME-ONLY | `**forged**` <script>"}
	result.Tests[0].Diagnostics = []string{"SUPPLIED-TEST-ONLY"}
	result.Tests[0].Assertions[0].Diagnostics = []string{"SUPPLIED-ASSERTION-ONLY"}
	if err := cr287Call(t, &r, f.Inv, []cr287Result{result}); err != nil {
		t.Fatal(err)
	}
	ce274Statuses(t, r, f.Target)
	proofTable := func(markdown string) string {
		_, rest, ok := strings.Cut(markdown, "| Component | Proof | Source | Contract | Evidence |\n")
		if !ok {
			t.Fatal("receipt omitted component proof table")
		}
		table, _, ok := strings.Cut(rest, "\n\nScope:")
		if !ok {
			t.Fatal("receipt omitted production scope")
		}
		return table
	}
	for _, report := range []verify.Report{r, cpClone(t, r)} {
		encoded := string(cpJSON(t, report))
		after := receipt.Markdown(&report)
		if proofTable(after) != proofTable(before) {
			t.Fatal("runtime tests/assertions changed Markdown proof coverage")
		}
		for _, want := range []string{"Assurance: **component evidence**", "recorded checks, supplied data and declared assumptions", "SUPPLIED-RUNTIME-ONLY", "SUPPLIED-TEST-ONLY", "SUPPLIED-ASSERTION-ONLY", "### Component runtime"} {
			if !strings.Contains(after, want) {
				t.Fatalf("component receipt lacks %q", want)
			}
		}
		for _, unwanted := range []string{"Every step tried |", "Code · conformance |", "`**forged**`", "<script>"} {
			if strings.Contains(after, unwanted) {
				t.Fatalf("component Markdown contains aggregate evidence or active markup %q", unwanted)
			}
		}
		if string(cpJSON(t, report)) != encoded || verify.Fingerprint(report) != report.Fingerprint {
			t.Fatal("Markdown mutated runtime report")
		}
	}
	legacy := ce274Seed()
	legacy.Fingerprint = verify.Fingerprint(legacy)
	legacyJSON, legacyMarkdown := string(cpJSON(t, legacy)), receipt.Markdown(&legacy)
	decoded := cpClone(t, legacy)
	if string(cpJSON(t, decoded)) != legacyJSON || verify.Fingerprint(decoded) != legacy.Fingerprint || receipt.Markdown(&decoded) != legacyMarkdown {
		t.Fatal("legacy report round trip changed JSON, fingerprint or Markdown")
	}
	if !strings.Contains(legacyMarkdown, " from tool output only. Fingerprint ") || strings.Contains(legacyMarkdown, "declared assumptions") ||
		!strings.Contains(legacyMarkdown, "| Code · conformance | ✅ tested against the model: every reachable state, no step outside it |") ||
		!strings.Contains(legacyMarkdown, "| Every step tried | ➖ not checked | the driver records its runs, not its attempts (D-0082) |") {
		t.Fatal("legacy footer or successful conformance rendering changed")
	}
}
