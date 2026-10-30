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

func cci279Decode(t *testing.T, value any) verify.Report {
	t.Helper()
	var r verify.Report
	if err := json.Unmarshal(cpJSON(t, value), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// Mutate each wire field independently, so a checksum of the inventory alone
// cannot stand in for the supplied runner, invocation, observations or attempts.
func TestComponentConformanceFingerprintBindsEverySuppliedField(t *testing.T) {
	f := cc279Fixture(t)
	result := cc279Good(t, f, f.Target, "", "")
	result.Mode, result.Attempts = "bounded-exhaustive", cc279AttemptGood()
	r := cc279Attach(t, f, result)
	rows := cc279Rows(t, r)
	if len(rows) != 1 {
		t.Fatalf("got %d supplied conformance rows", len(rows))
	}
	cc279Want(t, rows[0], result, "tested", true, true, true)
	baseline := verify.Fingerprint(r)
	if baseline != r.Fingerprint {
		t.Fatal("conformance attribution did not finalize its fingerprint")
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
			// These are arbitrary supplied map keys, not struct field names.
			if name, ok := path[len(path)-1].(string); ok && slices.Contains([]string{"env", "sampling", "pins", "values", "from", "to"}, name) {
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
			for i, item := range v {
				visit(item, append(slices.Clone(path), i))
			}
		case string:
			add(path, v+"-changed", "value")
		case float64:
			add(path, v+1, "value")
		case bool:
			add(path, !v, "value")
		default:
			t.Fatalf("unexpected conformance fixture value %T at %v", value, path)
		}
	}
	// Walk the expected wire object rather than the stored result, which could
	// otherwise hide a field silently dropped by JSON decoding or attribution.
	want := cc279Evidence{Target: result.Target, Check: result.Check, Status: "tested", TransitionsChecked: true, ExhaustiveStates: true, EveryStep: true, Result: result}
	visit(ci274Object(t, want), []any{"components", "conformance", 0})
	add([]any{"components", "conformance"}, []any{}, "presence")
	for _, m := range mutations {
		t.Run(fmt.Sprintf("%v/%s", m.path, m.kind), func(t *testing.T) {
			obj := ci274Object(t, r)
			// Require the complete public wire field to exist before changing it.
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
				t.Fatal("fingerprint ignored a supplied conformance or attempt field")
			}
			if string(cpJSON(t, decoded)) != before {
				t.Fatal("Fingerprint mutated the decoded report")
			}
		})
	}
}

func cci279ReverseSets(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if !slices.Contains([]string{"args", "errors", "observations", "diagnostics"}, key) {
				cci279ReverseSets(child)
			}
		}
	case []any:
		slices.Reverse(v)
		for _, child := range v {
			cci279ReverseSets(child)
		}
	}
}

func TestComponentConformanceCanonicalSetsSequencesAndRelocation(t *testing.T) {
	f := cc279Fixture(t)
	scoped := provenance257ID("core/scoped_driver.go", "DriveScoped")
	results := []cc279Result{cc279Good(t, f, f.Target, "", "run"), cc279Good(t, f, f.Target, "", "repeat"),
		cc279Good(t, f, f.Target, scoped, "run"), cc279Good(t, f, f.Same, "", "run")}
	results[0].Attempts = cc279AttemptGood()
	results[0].Checked = append(results[0].Checked, scoped)
	results[0].Inputs = append(results[0].Inputs, ce274Component(t, f.Inv, scoped))
	r := cc279Attach(t, f, results...)
	canonical := string(cpJSON(t, r))
	obj := ci274Object(t, r)
	cci279ReverseSets(obj["components"])
	decoded := cci279Decode(t, obj)
	before := string(cpJSON(t, decoded))
	if verify.Fingerprint(decoded) != r.Fingerprint || string(cpJSON(t, decoded)) != before {
		t.Fatal("decoded set/completion order changed the fingerprint or mutated the report")
	}
	var permuted struct {
		Inventory   project.ComponentInventory `json:"inventory"`
		Conformance []cc279Evidence            `json:"conformance"`
	}
	if err := json.Unmarshal(cpJSON(t, obj["components"]), &permuted); err != nil {
		t.Fatal(err)
	}
	var reversed []cc279Result
	for _, row := range permuted.Conformance {
		reversed = append(reversed, row.Result)
	}
	f2 := f
	f2.Inv = &permuted.Inventory
	x := cc279Attach(t, f2, reversed...)
	if string(cpJSON(t, x)) != canonical {
		t.Fatal("reference sets or result completion order changed canonical storage")
	}
	if err := cc279Call(t, &r, f2.Inv, reversed); err != nil {
		t.Fatal(err)
	}
	if string(cpJSON(t, r)) != canonical {
		t.Fatal("equivalent reordered replacement changed conformance evidence")
	}
	relocated := cc279Fixture(t)
	if relocated.Root == f.Root || !reflect.DeepEqual(relocated.Inv, f.Inv) {
		t.Fatal("relocation fixture must have identical relative snapshots")
	}
	x = cc279Attach(t, relocated, results...)
	if string(cpJSON(t, x)) != canonical {
		t.Fatal("checkout relocation changed supplied conformance")
	}
	// Locate the row with attempts after canonical sorting by target/check/id.
	index := -1
	for i, row := range cc279Rows(t, r) {
		if row.Result.Attempts != nil {
			index = i
		}
	}
	if index < 0 {
		t.Fatal("canonicalization lost attempt evidence")
	}
	prefix := []any{"components", "conformance", index, "result"}
	for _, path := range [][]any{{"invocation", "args"}, {"observations"}, {"observations", 0, "args"}, {"diagnostics"},
		{"attempts", "observations"}, {"attempts", "observations", 0, "args"}, {"attempts", "diagnostics"}} {
		t.Run(fmt.Sprint(path), func(t *testing.T) {
			obj := ci274Object(t, r)
			sequence := ci274At(t, obj, append(slices.Clone(prefix), path...)...).([]any)
			if len(sequence) < 2 {
				t.Fatal("sequence fixture needs distinct ordered entries")
			}
			slices.Reverse(sequence)
			if verify.Fingerprint(cci279Decode(t, obj)) == r.Fingerprint {
				t.Fatal("meaningful invocation/observation/diagnostic order was treated as a set")
			}
		})
	}
	volatile := cpClone(t, r)
	volatile.Bugs = []verify.Bug{{Name: "Bug", Trace: "/first/trace"}}
	volatile.Properties = []verify.Property{{Name: "Live", Trace: "/first/property"}}
	initial := verify.Fingerprint(volatile)
	volatile.GeneratedAt, volatile.Agent, volatile.Build.Output, volatile.Toolchain.Go = "tomorrow", "another agent", "timings", "go1.27.99"
	volatile.Bugs[0].Trace, volatile.Properties[0].Trace = "/relocated/trace", "/relocated/property"
	volatile.Assurance = "proved"
	if verify.Fingerprint(volatile) != initial || volatile.CodeAssurance() != "component evidence" || volatile.Claim() != "component evidence" || volatile.EverySize() {
		t.Fatal("volatile data or a stale aggregate label changed supplied conformance assurance")
	}
}

func TestComponentConformanceOwnsActualTypedArgumentsAndInventory(t *testing.T) {
	f := cc279Fixture(t)
	for _, outcome := range []string{"passed", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			result := cc279Good(t, f, f.Target, "", "")
			result.Outcome, result.Attempts = outcome, cc279AttemptGood()
			inv := cpClone(t, *f.Inv)
			r := ce274Seed()
			method, actual := cc279CallArg(t, &r, &inv, []cc279Result{result})
			out := method.Call([]reflect.Value{reflect.ValueOf(&inv), actual})[0]
			if !out.IsNil() {
				t.Fatal(out.Interface())
			}
			before, stored, recomputed := string(cpJSON(t, r)), r.Fingerprint, verify.Fingerprint(r)
			// This is the concrete []verify.ComponentConformanceResult argument
			// passed above, not the independent wire helper used to create it.
			argBefore := string(cpJSON(t, actual.Interface()))
			provenance257Mutate(actual)
			if string(cpJSON(t, actual.Interface())) == argBefore {
				t.Fatal("typed argument was not mutated")
			}
			if string(cpJSON(t, r)) != before || r.Fingerprint != stored || verify.Fingerprint(r) != recomputed {
				t.Fatal("report aliases caller result slices, observations, maps, snapshots, runner pins or diagnostics")
			}
			provenance257Mutate(reflect.ValueOf(&inv))
			if string(cpJSON(t, r)) != before || r.Fingerprint != stored || verify.Fingerprint(r) != recomputed {
				t.Fatal("report aliases caller inventory declarations, inputs or invocation metadata")
			}
		})
	}
}

func TestComponentConformanceAttributionNeverExecutesAndModelOnlyRejectsIt(t *testing.T) {
	f := cc279Fixture(t)
	bin := t.TempDir()
	log := filepath.Join(bin, "called")
	t.Setenv("COMPONENT_279_TOOL_LOG", log)
	for _, name := range []string{"go", "docker", "java", "tlc", "provenance-command", "component-conformance-sentinel"} {
		cpWrite(t, bin, name, "#!/bin/sh\necho called >> \"$COMPONENT_279_TOOL_LOG\"\nexit 73\n")
		if err := os.Chmod(filepath.Join(bin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	provenance257EditManifest(t, f.Root, func(m map[string]any) {
		for _, item := range provenance257ConfigCheck(m, "Step")["invocations"].([]any) {
			call := item.(map[string]any)
			if call["kind"] != "conformance" {
				continue
			}
			call["command"] = "component-conformance-sentinel; component-conformance-sentinel"
			call["args"] = []string{"$(component-conformance-sentinel)", "`component-conformance-sentinel`", "; component-conformance-sentinel"}
			call["env"] = map[string]string{"EXPANSION": "$(component-conformance-sentinel)"}
			call["sampling"] = map[string]string{"opaque": "`component-conformance-sentinel`"}
		}
	})
	f.Inv = cpResolve(t, f.Root)
	result := cc279Good(t, f, f.Target, "", "")
	result.Runner.Name, result.Runner.Version = "component-conformance-sentinel", "$(component-conformance-sentinel)"
	result.Runner.Pins["command"] = "`component-conformance-sentinel`"
	result.Attempts = cc279AttemptGood()
	r := cc279Attach(t, f, result)
	if err := cc279Call(t, &r, f.Inv, nil); err != nil {
		t.Fatal(err)
	}
	if err := verify.Check(f.Root); !errors.Is(err, verify.ErrComponents) {
		t.Fatalf("explicit preflight must reject before execution: %v", err)
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
		t.Fatalf("model-only checking did not reach stub model tools: %v", err)
	}
	ce274Unchanged(t, model, func() error { return cc279Call(t, model, f.Inv, []cc279Result{result}) })
	ce274Unchanged(t, model, func() error { return cc279Call(t, model, f.Inv, nil) })
	after, err := os.ReadFile(log)
	if err != nil || string(after) != string(called) {
		t.Fatal("rejected model-only conformance attribution executed another tool")
	}
}

func TestComponentConformanceMarkdownPreservesProofView(t *testing.T) {
	f := cc279Fixture(t)
	r := ce274Attach(t, f, true)
	before := receipt.Markdown(&r)
	result := cc279Good(t, f, f.Same, "", "")
	result.Mode, result.Attempts = "bounded-exhaustive", cc279AttemptGood()
	result.Diagnostics = []string{"SUPPLIED-CONFORMANCE-ONLY"}
	result.Attempts.Diagnostics = []string{"SUPPLIED-ATTEMPT-ONLY"}
	if err := cc279Call(t, &r, f.Inv, []cc279Result{result}); err != nil {
		t.Fatal(err)
	}
	ce274Statuses(t, r, f.Target)
	// The proof table remains independent of conformance; a separate view
	// carries the supplied conformance and attempt detail alongside JSON.
	after := receipt.Markdown(&r)
	proofTable := func(markdown string) string {
		_, rest, ok := strings.Cut(markdown, "| Component | Proof | Source | Contract | Evidence |\n")
		if !ok {
			t.Fatal("receipt omitted its component proof table")
		}
		table, _, ok := strings.Cut(rest, "\n\nScope:")
		if !ok {
			t.Fatal("receipt omitted its production scope")
		}
		return table
	}
	if proofTable(after) != proofTable(before) {
		t.Fatal("supplied conformance changed Markdown's proof table")
	}
	for _, want := range []string{"Assurance: **component evidence**", "recorded checks, supplied data and declared assumptions", "SUPPLIED-CONFORMANCE-ONLY", "SUPPLIED-ATTEMPT-ONLY", "### Component conformance"} {
		if !strings.Contains(after, want) {
			t.Fatalf("explicit receipt lacks %q", want)
		}
	}
	for _, unwanted := range []string{"Every step tried |", "Code · conformance |"} {
		if strings.Contains(after, unwanted) {
			t.Fatalf("component Markdown contains aggregate evidence %q", unwanted)
		}
	}
	legacy := ce274Seed()
	legacy.Fingerprint = verify.Fingerprint(legacy)
	legacyJSON, legacyMarkdown := string(cpJSON(t, legacy)), receipt.Markdown(&legacy)
	// Explicit attribution to one report must not alter legacy serialization,
	// fingerprints or the pre-existing successful conformance receipt wording.
	decoded := cpClone(t, legacy)
	if string(cpJSON(t, decoded)) != legacyJSON || verify.Fingerprint(decoded) != legacy.Fingerprint || receipt.Markdown(&decoded) != legacyMarkdown {
		t.Fatal("legacy report round-trip changed JSON, fingerprint or rendering")
	}
	if !strings.Contains(legacyMarkdown, "| Code · conformance | ✅ tested against the model: every reachable state, no step outside it |") ||
		!strings.Contains(legacyMarkdown, "| Every step tried | ➖ not checked | the driver records its runs, not its attempts (D-0082) |") {
		t.Fatal("successful legacy conformance receipt wording changed")
	}
}
