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
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

func ci274Object(t *testing.T, value any) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(cpJSON(t, value), &obj); err != nil {
		t.Fatal(err)
	}
	return obj
}

func ci274At(t *testing.T, value any, path ...any) any {
	t.Helper()
	for _, part := range path {
		switch key := part.(type) {
		case string:
			obj, ok := value.(map[string]any)
			if !ok || obj[key] == nil {
				t.Fatalf("missing object field %q in fingerprint fixture", key)
			}
			value = obj[key]
		case int:
			list, ok := value.([]any)
			if !ok || key < 0 || key >= len(list) {
				t.Fatalf("missing array position %d in fingerprint fixture", key)
			}
			value = list[key]
		default:
			t.Fatalf("unsupported fingerprint path part %#v", part)
		}
	}
	return value
}

func ci274ProofIndex(t *testing.T, r verify.Report, target string) int {
	t.Helper()
	for i, row := range r.Components.Proofs {
		if row.Target == target && row.Result != nil {
			return i
		}
	}
	t.Fatalf("no supplied proof result for %s", target)
	return -1
}

// Decoding is a public entry point too: changing only recorded evidence must
// change its fingerprint, even when the declaration inventory is unchanged.
func TestComponentEvidenceFingerprintBindsEveryNewSemanticInput(t *testing.T) {
	f := ce274Fixture(t)
	r := ce274Attach(t, f, true)
	baseline := verify.Fingerprint(r)
	if baseline != r.Fingerprint {
		t.Fatal("attribution did not finalize its fingerprint")
	}
	proof := []any{"components", "proofs", ci274ProofIndex(t, r, f.Target), "result"}
	type mutation struct {
		name  string
		path  []any
		value any
	}
	var mutations []mutation
	add := func(name string, prefix []any, value any, fields ...any) {
		path := append(slices.Clone(prefix), fields...)
		mutations = append(mutations, mutation{name, path, value})
	}
	add("assumption presence", nil, []any{}, "components", "assumptions")
	for i := range ce274AssumptionRows(t, r) {
		assumption := []any{"components", "assumptions", i}
		name := fmt.Sprintf("assumption %d ", i)
		add(name+"row target", assumption, f.Other, "target")
		add(name+"status", assumption, "changed", "status")
		add(name+"verified flag", assumption, true, "verified")
		add(name+"record target", assumption, f.Other, "record", "target")
		for _, field := range []string{"id", "language", "package", "symbol", "source", "source_sha256", "contract_sha256"} {
			add(name+"target snapshot "+field, assumption, "changed-"+field, "record", "input", field)
		}
		for _, field := range []string{"id", "statement", "reason"} {
			add(name+"declaration "+field, assumption, "changed-"+field, "record", "assumption", field)
		}
		for _, field := range []string{"path", "sha256"} {
			add(name+"source "+field, assumption, "changed-"+field, "record", "assumption", "source", field)
		}
	}
	add("actual invocation presence", proof, nil, "invocation")
	for _, field := range []string{"id", "kind", "check", "command", "working_dir"} {
		add("actual invocation "+field, proof, "changed-"+field, "invocation", field)
	}
	add("actual argument value", proof, []any{"changed", "a-second"}, "invocation", "args")
	add("actual argument order", proof, []any{"a-second", "z-first"}, "invocation", "args")
	add("actual environment value", proof, "changed", "invocation", "env", "MODE")
	add("actual environment key", proof, map[string]string{"DIFFERENT": "strict", "OTHER": "kept"}, "invocation", "env")
	add("actual sampling value", proof, "18", "invocation", "sampling", "seed")
	add("actual sampling key", proof, map[string]string{"other-seed": "17", "runs": "3"}, "invocation", "sampling")
	add("actual invocation input presence", proof, []any{}, "invocation", "inputs")
	invocationInputs := ci274At(t, ci274Object(t, r), append(slices.Clone(proof), "invocation", "inputs")...).([]any)
	for i, item := range invocationInputs {
		file := item.(map[string]any)
		for _, field := range []string{"path", "sha256"} {
			add("actual invocation input "+file["path"].(string)+" "+field, proof, "changed-"+field, "invocation", "inputs", i, field)
		}
	}
	add("actual file inputs presence", proof, []any{}, "file_inputs")
	files := ci274At(t, ci274Object(t, r), append(slices.Clone(proof), "file_inputs")...).([]any)
	for i, item := range files {
		file := item.(map[string]any)
		for _, field := range []string{"path", "sha256"} {
			add("actual file "+file["path"].(string)+" "+field, proof, "changed-"+field, "file_inputs", i, field)
		}
	}
	add("actual tool presence", proof, nil, "tool")
	add("actual tool name", proof, "Other verifier", "tool", "name")
	add("actual tool version", proof, "9.8.7", "tool", "version")
	add("actual tool pin presence", proof, map[string]string{}, "tool", "pins")
	pins := ci274At(t, ci274Object(t, r), append(slices.Clone(proof), "tool", "pins")...).(map[string]any)
	add("actual tool pin key", proof, map[string]any{"different": pins["image"], "recipe": pins["recipe"]}, "tool", "pins")
	for _, pin := range []string{"image", "recipe"} {
		add("actual tool pin "+pin, proof, "sha256:changed", "tool", "pins", pin)
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			obj := ci274Object(t, r)
			parent := ci274At(t, obj, tc.path[:len(tc.path)-1]...).(map[string]any)
			parent[tc.path[len(tc.path)-1].(string)] = tc.value
			var decoded verify.Report
			if err := json.Unmarshal(cpJSON(t, obj), &decoded); err != nil {
				t.Fatal(err)
			}
			before := string(cpJSON(t, decoded))
			if verify.Fingerprint(decoded) == baseline {
				t.Fatal("fingerprint ignored changed supplied evidence while the inventory stayed fixed")
			}
			if string(cpJSON(t, decoded)) != before {
				t.Fatal("fingerprinting mutated decoded evidence")
			}
		})
	}
	// Also exercise accepted changes through attribution, rather than relying
	// only on synthetic decoded reports whose fields are deliberately stale.
	for _, name := range []string{"fewer assumptions", "additional declared assumption", "alternate selected invocation", "additional file set", "tool version", "tool pin", "old proof-only metadata"} {
		t.Run("valid attribution/"+name, func(t *testing.T) {
			x := cpClone(t, r)
			p := ce274ProofFor(t, f, f.Target, true)
			records := ce274Records(t, f)
			switch name {
			case "fewer assumptions":
				records = records[1:]
			case "additional declared assumption":
				for _, a := range ce274Check(t, f.Inv, f.Target).Assumptions {
					if a.ID == "valid" {
						records = append(records, ce274AssumptionRecord{Target: f.Target, Input: ce274Component(t, f.Inv, f.Target), Assumption: a})
					}
				}
			case "alternate selected invocation":
				p.Invocation.ID = "repeat"
			case "additional file set":
				p.FileInputs = slices.DeleteFunc(p.FileInputs, func(file project.ComponentFile) bool { return file.Path == "neighbors/extra.go" })
			case "tool version":
				p.Tool.Version = "2.0.0"
			case "tool pin":
				p.Tool.Pins["image"] = "gobra@sha256:" + strings.Repeat("f", 64)
			case "old proof-only metadata":
				p.Invocation, p.FileInputs, p.Tool = nil, nil, nil
			}
			if err := ce274Proofs(t, &x, f.Inv, []ce274Proof{p}); err != nil {
				t.Fatal(err)
			}
			if err := ce274Assumptions(t, &x, f.Inv, records); err != nil {
				t.Fatal(err)
			}
			if x.Fingerprint == baseline || x.Fingerprint != verify.Fingerprint(x) {
				t.Fatal("valid changed evidence did not produce a new finalized fingerprint")
			}
			ce274Statuses(t, x, f.Target)
			ce274WantRecords(t, x, records)
		})
	}
}

// Everything under components is a set except ordered arguments and tool
// diagnostics. Reverse sets even in decoded JSON to test Fingerprint itself.
func ci274ReverseSets(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if key != "args" && key != "errors" {
				ci274ReverseSets(child)
			}
		}
	case []any:
		slices.Reverse(v)
		for _, child := range v {
			ci274ReverseSets(child)
		}
	}
}

func ci274AttachBoth(t *testing.T, f ce274FixtureData, proofs []ce274Proof, records []ce274AssumptionRecord, assumptionsFirst bool) verify.Report {
	t.Helper()
	r := ce274Seed()
	proofOp := func() {
		if err := ce274Proofs(t, &r, f.Inv, proofs); err != nil {
			t.Fatal(err)
		}
	}
	assumptionOp := func() {
		if err := ce274Assumptions(t, &r, f.Inv, records); err != nil {
			t.Fatal(err)
		}
	}
	if assumptionsFirst {
		assumptionOp()
		proofOp()
	} else {
		proofOp()
		assumptionOp()
	}
	if r.Fingerprint != verify.Fingerprint(r) {
		t.Fatal("attribution did not finalize its canonical fingerprint")
	}
	return r
}

func TestComponentEvidenceCanonicalOrderAndRelocation(t *testing.T) {
	f := ce274Fixture(t)
	proofs := []ce274Proof{ce274ProofFor(t, f, f.Target, true), ce274ProofFor(t, f, f.Other, true)}
	proofs[0].Verified = append(proofs[0].Verified, f.Helper)
	records := ce274Records(t, f)
	r := ci274AttachBoth(t, f, proofs, records, false)
	canonical := string(cpJSON(t, r))
	obj := ci274Object(t, r)
	ci274ReverseSets(obj["components"])
	var decoded verify.Report
	if err := json.Unmarshal(cpJSON(t, obj), &decoded); err != nil {
		t.Fatal(err)
	}
	before := string(cpJSON(t, decoded))
	if verify.Fingerprint(decoded) != r.Fingerprint || string(cpJSON(t, decoded)) != before {
		t.Fatal("decoded set ordering changed the fingerprint or Fingerprint mutated its caller")
	}
	var reversed struct {
		Inventory project.ComponentInventory `json:"inventory"`
		Proofs    []struct {
			Result *ce274Proof `json:"result"`
		} `json:"proofs"`
		Assumptions []ce274AssumptionEvidence `json:"assumptions"`
	}
	if err := json.Unmarshal(cpJSON(t, obj["components"]), &reversed); err != nil {
		t.Fatal(err)
	}
	var reversedProofs []ce274Proof
	for _, row := range reversed.Proofs {
		if row.Result != nil {
			reversedProofs = append(reversedProofs, *row.Result)
		}
	}
	var reversedRecords []ce274AssumptionRecord
	for _, row := range reversed.Assumptions {
		reversedRecords = append(reversedRecords, row.Record)
	}
	permuted := f
	permuted.Inv = &reversed.Inventory
	for _, assumptionsFirst := range []bool{false, true} {
		x := ci274AttachBoth(t, permuted, reversedProofs, reversedRecords, assumptionsFirst)
		if string(cpJSON(t, x)) != canonical {
			t.Fatal("record/completion ordering changed canonical report bytes")
		}
	}
	// Replacement must accept equivalent inventories while revalidating the
	// retained kind, not merely tolerate ordering on initially empty reports.
	if err := ce274Assumptions(t, &r, permuted.Inv, reversedRecords); err != nil {
		t.Fatal(err)
	}
	if err := ce274Proofs(t, &r, permuted.Inv, reversedProofs); err != nil {
		t.Fatal(err)
	}
	if string(cpJSON(t, r)) != canonical {
		t.Fatal("equivalent replacement inventory changed retained evidence")
	}
	relocated := ce274Fixture(t)
	if relocated.Root == f.Root || !reflect.DeepEqual(relocated.Inv, f.Inv) {
		t.Fatal("relocation fixture does not have identical relative snapshots")
	}
	x := ci274AttachBoth(t, relocated, proofs, ce274Records(t, relocated), true)
	if string(cpJSON(t, x)) != canonical {
		t.Fatal("checkout relocation changed supplied evidence or fingerprint")
	}
	// Matching reordered arguments remain valid actual metadata, but order
	// carries meaning and must not be normalized as a set.
	changed := f
	inv := cpClone(t, *f.Inv)
	changed.Inv = &inv
	for i := range inv.Checks {
		for j := range inv.Checks[i].Invocations {
			slices.Reverse(inv.Checks[i].Invocations[j].Args)
		}
	}
	changedProofs := []ce274Proof{ce274ProofFor(t, changed, f.Target, true), ce274ProofFor(t, changed, f.Other, true)}
	changedProofs[0].Verified = append(changedProofs[0].Verified, f.Helper)
	x = ci274AttachBoth(t, changed, changedProofs, records, false)
	if x.Fingerprint == r.Fingerprint {
		t.Fatal("ordered invocation arguments were treated as a set")
	}
	volatile := cpClone(t, r)
	volatile.Bugs = []verify.Bug{{Name: "Bug", Trace: "/first/trace"}}
	volatile.Properties = []verify.Property{{Name: "Live", Trace: "/first/property"}}
	initial := verify.Fingerprint(volatile)
	volatile.GeneratedAt, volatile.Agent, volatile.Build.Output, volatile.Toolchain.Go = "tomorrow", "another agent", "timings", "go1.27.99"
	volatile.Bugs[0].Trace, volatile.Properties[0].Trace = "/relocated/trace", "/relocated/property"
	volatile.Assurance = "proved"
	if verify.Fingerprint(volatile) != initial || volatile.CodeAssurance() != "component evidence" || volatile.EverySize() {
		t.Fatal("volatile metadata or stale assurance entered the evidence fingerprint")
	}
}

func TestComponentEvidenceOwnsEveryNestedInput(t *testing.T) {
	for _, assumptionsFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("assumptionsFirst=%v", assumptionsFirst), func(t *testing.T) {
			f := ce274Fixture(t)
			r := ce274Seed()
			proofInventory, assumptionInventory := cpClone(t, *f.Inv), cpClone(t, *f.Inv)
			p := ce274ProofFor(t, f, f.Target, true)
			p.Passed, p.Errors = false, []string{"retained diagnostic"}
			var proofs []verify.ComponentProofResult
			if err := json.Unmarshal(cpJSON(t, []ce274Proof{p}), &proofs); err != nil {
				t.Fatal(err)
			}
			method := reflect.ValueOf(&r).MethodByName("AttributeComponentAssumptions")
			if !method.IsValid() {
				t.Fatal("missing Report.AttributeComponentAssumptions")
			}
			if method.Type().NumIn() != 2 || method.Type().In(1).Kind() != reflect.Slice || method.Type().NumOut() != 1 {
				t.Fatal("invalid AttributeComponentAssumptions signature")
			}
			records := reflect.New(method.Type().In(1))
			if err := json.Unmarshal(cpJSON(t, ce274Records(t, f)), records.Interface()); err != nil {
				t.Fatal(err)
			}
			proofOp := func() {
				if err := r.AttributeComponentProofs(&proofInventory, proofs); err != nil {
					t.Fatal(err)
				}
			}
			assumptionOp := func() {
				out := method.Call([]reflect.Value{reflect.ValueOf(&assumptionInventory), records.Elem()})[0]
				if !out.IsNil() {
					t.Fatal(out.Interface())
				}
			}
			mutateAndCheck := func(inv *project.ComponentInventory, input reflect.Value) {
				before, stored, recomputed := string(cpJSON(t, r)), r.Fingerprint, verify.Fingerprint(r)
				provenance257Mutate(reflect.ValueOf(inv))
				provenance257Mutate(input)
				if string(cpJSON(t, r)) != before || r.Fingerprint != stored || verify.Fingerprint(r) != recomputed {
					t.Fatal("report retained caller-owned declarations, records, inputs, invocation maps/arguments/files or tool pins")
				}
			}
			if assumptionsFirst {
				assumptionOp()
				mutateAndCheck(&assumptionInventory, records.Elem())
				proofOp()
				mutateAndCheck(&proofInventory, reflect.ValueOf(proofs))
			} else {
				proofOp()
				mutateAndCheck(&proofInventory, reflect.ValueOf(proofs))
				assumptionOp()
				mutateAndCheck(&assumptionInventory, records.Elem())
			}
			ce274WantRecords(t, r, ce274Records(t, f))
			ce274Statuses(t, r)
		})
	}
}

func TestComponentEvidenceAttributionNeverExecutes(t *testing.T) {
	f := ce274Fixture(t)
	bin := t.TempDir()
	log := filepath.Join(bin, "called")
	t.Setenv("COMPONENT_274_TOOL_LOG", log)
	for _, name := range []string{"go", "docker", "provenance-command", "component-evidence-sentinel"} {
		cpWrite(t, bin, name, "#!/bin/sh\necho called >> \"$COMPONENT_274_TOOL_LOG\"\nexit 73\n")
		if err := os.Chmod(filepath.Join(bin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	provenance257EditManifest(t, f.Root, func(m map[string]any) {
		for _, item := range provenance257ConfigCheck(m, "Step")["invocations"].([]any) {
			call := item.(map[string]any)
			call["command"] = "component-evidence-sentinel; component-evidence-sentinel"
			call["args"] = []string{"$(component-evidence-sentinel)", "`component-evidence-sentinel`", "; component-evidence-sentinel"}
			call["env"] = map[string]string{"EXPANSION": "$(component-evidence-sentinel)"}
			call["sampling"] = map[string]string{"opaque": "`component-evidence-sentinel`"}
		}
	})
	f.Inv = cpResolve(t, f.Root)
	p := ce274ProofFor(t, f, f.Target, true)
	p.Tool.Name, p.Tool.Version = "component-evidence-sentinel", "$(component-evidence-sentinel)"
	p.Tool.Pins["command"] = "`component-evidence-sentinel`"
	records := ce274Records(t, f)
	for _, assumptionsFirst := range []bool{false, true} {
		ci274AttachBoth(t, f, []ce274Proof{p}, records, assumptionsFirst)
	}
	if err := verify.Check(f.Root); !errors.Is(err, verify.ErrComponents) {
		t.Fatalf("explicit preflight must reject before execution: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if r, err := verify.Run(ctx, f.Root, "", toolchain.Toolchain{}); r != nil || !errors.Is(err, verify.ErrComponents) {
		t.Fatalf("explicit verification started an execution path: %#v, %v", r, err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("attribution, inventory resolution or rejected explicit verification executed a command")
	}
	model, err := verify.RunModel(ctx, f.Root, toolchain.Toolchain{})
	if err != nil || model == nil || !model.ModelOnly {
		t.Fatalf("model-only checking became unavailable: %#v, %v", model, err)
	}
	called, err := os.ReadFile(log)
	if err != nil || len(called) == 0 {
		t.Fatalf("model-only checking did not reach the stub model tools: %v", err)
	}
	ce274Unchanged(t, model, func() error { return ce274Proofs(t, model, f.Inv, []ce274Proof{p}) })
	ce274Unchanged(t, model, func() error { return ce274Assumptions(t, model, f.Inv, records) })
	after, err := os.ReadFile(log)
	if err != nil || string(after) != string(called) {
		t.Fatal("rejected model-only attribution executed another tool")
	}
}
