package verify_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/verify"
)

// Preserve the existing module, recording and receipt contracts. In particular,
// no test changes the scalar-only legacy collector, the shared Encode API, the
// streaming recording path or the meaning of attempted-step argument tags.
// This Go runner captures the actual generated TLC modules and configurations;
// its verdicts are doubles, not evidence of a real verifier run.
type moduleReceipt837Runner struct {
	mu     sync.Mutex
	bodies map[string]string
}

func (r *moduleReceipt837Runner) Check(ctx context.Context, dir, module string, cfg tlc.Config) (tlc.Result, error) {
	if err := ctx.Err(); err != nil {
		return tlc.Result{}, err
	}
	wantSpec := map[string]string{"Invariant_ConformanceInit": "Invariant_ISpec", "Invariant_Conformance": "Invariant_CSpec", "Invariant_Tried": "Invariant_TSpec"}
	if wantSpec[module] == "" || cfg.Specification != wantSpec[module] || cfg.Constants["Limit"] != "4" {
		return tlc.Result{}, fmt.Errorf("unexpected conformance request %s: %+v", module, cfg)
	}
	if module == "Invariant_ConformanceInit" && !reflect.DeepEqual(cfg.Invariants, []string{"Init"}) {
		return tlc.Result{}, fmt.Errorf("starting states were not checked against Init: %+v", cfg)
	}
	if module == "Invariant_Tried" && !reflect.DeepEqual(cfg.Invariants, []string{"Invariant_EveryStepTried"}) {
		return tlc.Result{}, fmt.Errorf("attempt coverage request changed: %+v", cfg)
	}
	b, err := os.ReadFile(filepath.Join(dir, module+".tla"))
	if err != nil {
		return tlc.Result{}, err
	}
	r.mu.Lock()
	r.bodies[module] += string(b)
	r.mu.Unlock()
	return tlc.Result{Outcome: tlc.Passed}, nil
}

const moduleReceipt837Hostile = "</code><script>alert(1)</script> | **forged** [link](https://evil.invalid) &amp; `tick` \\\"\r\nnext"

func moduleReceipt837States(t *testing.T) ([]map[string]any, []map[string]string) {
	t.Helper()
	statuses := []string{`[]`, `["queued"]`, `["queued","queued","done"]`, `["done","queued","queued"]`}
	texts := []string{`<<>>`, `<<"queued">>`, `<<"queued", "queued", "done">>`, `<<"done", "queued", "queued">>`}
	var states []map[string]any
	var wants []map[string]string
	for i, status := range statuses {
		var state map[string]any
		d := json.NewDecoder(strings.NewReader(`{"status":` + status + `,"nested":{"rows":[{"id":9007199254740993,"items":[[true,false],[],{"label":"done"}]}]},"empty_list":[],"empty_text":"","yes":true,"no":false,"big":9007199254740993,"huge":-123456789012345678901234567890,"number_text":"9007199254740993"}`))
		d.UseNumber()
		if err := d.Decode(&state); err != nil {
			t.Fatal(err)
		}
		state["literal"] = []any{moduleReceipt837Hostile}
		quoted, err := json.Marshal(moduleReceipt837Hostile)
		if err != nil {
			t.Fatal(err)
		}
		states = append(states, state)
		wants = append(wants, map[string]string{
			"status":     texts[i],
			"nested":     `[rows |-> <<[id |-> 9007199254740993, items |-> <<<<TRUE, FALSE>>, <<>>, [label |-> "done"]>>]>>]`,
			"empty_list": `<<>>`, "empty_text": `""`, "yes": "TRUE", "no": "FALSE",
			"big": "9007199254740993", "huge": "-123456789012345678901234567890", "number_text": `"9007199254740993"`,
			"literal": "<<" + string(quoted) + ">>",
		})
	}
	return states, wants
}

func moduleReceipt837Bytes(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func moduleReceipt837Clone(t *testing.T, r *verify.Report) *verify.Report {
	t.Helper()
	var out verify.Report
	if err := json.Unmarshal(moduleReceipt837Bytes(t, r), &out); err != nil {
		t.Fatal(err)
	}
	return &out
}

func moduleReceipt837ComponentHash(r *verify.Report) string {
	copy := *r
	copy.Application = nil // A raw observation hash must not mask lost component values.
	return verify.Fingerprint(copy)
}

func moduleReceipt837Qualification(t *testing.T, r *verify.Report, tested, exhaustive, tried bool) {
	t.Helper()
	if r.Components == nil || len(r.Components.Conformance) != 1 {
		t.Fatal("missing attributed conformance")
	}
	row := r.Components.Conformance[0]
	status := "unchecked"
	if tested {
		status = "tested"
	}
	if row.Status != status || row.TransitionsChecked != tested || row.ExhaustiveStates != exhaustive || row.EveryStep != tried || row.Origin != "gate" {
		t.Errorf("conformance qualifications changed: %+v", row)
	}
	if !r.Passed {
		t.Error("attribution changed the recorded gate outcome")
	}
	if len(r.Components.Proofs) != 2 {
		t.Fatal("production inventory lost its unchecked neighbor")
	}
	for _, p := range r.Components.Proofs {
		if p.Status != "unchecked" || p.Result != nil {
			t.Error("observations manufactured a proof")
		}
	}
}

func moduleReceipt837Exercise(t *testing.T, explored bool) {
	t.Helper()
	states, wants := moduleReceipt837States(t)
	r, inv := verify.PythonModuleReceiptFixture837(explored, states)
	rawBefore := moduleReceipt837Bytes(t, r.Application.Executions)
	x := r.Application.Executions[0]
	recording, err := verify.RecordingPythonModuleReceipt837(x)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Traces   [][]map[string]any `json:"traces"`
		States   []map[string]any   `json:"states"`
		Attempts [][]any            `json:"attempts"`
	}
	d := json.NewDecoder(bytes.NewReader(recording))
	d.UseNumber()
	if err := d.Decode(&document); err != nil {
		t.Fatal(err)
	}
	recordedStates := document.States
	if !explored {
		if len(document.Traces) != 1 {
			t.Fatal("sampled run boundary lost")
		}
		recordedStates = document.Traces[0]
	}
	if len(recordedStates) != len(wants) {
		t.Fatalf("recording has %d states, want %d", len(recordedStates), len(wants))
	}
	for i, state := range recordedStates {
		if len(state) != len(wants[i]) {
			t.Fatalf("recording lost fields: %#v", state)
		}
		for field, value := range state {
			got, err := conformance.Encode(value)
			if err != nil || got != wants[i][field] {
				t.Fatalf("recording state %d %s: %q (%v), want %q", i, field, got, err, wants[i][field])
			}
		}
	}
	vars := make([]string, 0, len(states[0]))
	for field := range states[0] {
		vars = append(vars, field)
	}
	sort.Strings(vars)
	stage := t.TempDir()
	modelText := "---- MODULE Tasks ----\nEXTENDS Integers, Sequences, TLC\nCONSTANT Limit\nVARIABLES " + strings.Join(vars, ", ") + "\nvars == <<" + strings.Join(vars, ", ") + ">>\nInit == TRUE\nAdvance(a,b,c,d,e,f) == TRUE\nNext == TRUE\n====\n"
	if err := os.WriteFile(filepath.Join(stage, "Tasks.tla"), []byte(modelText), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &moduleReceipt837Runner{bodies: map[string]string{}}
	var model *conformance.Model
	if explored {
		model = &conformance.Model{Steps: []tla.Step{{Name: "Advance", Args: []string{"a", "b", "c", "d", "e", "f"}}}}
	}
	checked, err := conformance.CheckReader(context.Background(), runner, stage, "Tasks", r.Bounds, vars, int64(len(states)), bytes.NewReader(recording), model)
	if err != nil || !checked.Passed {
		t.Fatalf("recording did not reach TLC transport: %+v, %v", checked, err)
	}
	if explored {
		if checked.Tried == nil || !checked.Tried.Passed || checked.Tried.Attempts != len(states) {
			t.Fatalf("attempt coverage lost: %+v", checked)
		}
		checked.Exhaustive, checked.Runs = true, x.Exploration.Runs
	}
	r.Application.Conformance[0].Result = checked
	if err := verify.AttributePythonModuleReceipt837(r, inv); err != nil {
		t.Fatal(err)
	}
	moduleReceipt837Qualification(t, r, true, explored, explored)
	row := r.Components.Conformance[0]
	if row.Result.Runs != int64(checked.Runs) || row.Result.Steps != int64(checked.Steps) || row.Result.States != int64(checked.States) || row.Result.Transitions != int64(checked.Transitions) || row.Result.ModelStates != checked.ModelStates {
		t.Error("attribution changed conformance counts")
	}
	var transitions []verify.PythonApplicationObservation
	for _, o := range x.Observations {
		if o.Kind == "transition" && (o.Replay == nil || !*o.Replay) {
			transitions = append(transitions, o)
		}
	}
	if len(row.Result.Observations) != len(transitions) {
		t.Fatalf("component observations lost attempts or retained replays: %d, want %d", len(row.Result.Observations), len(transitions))
	}
	for i, got := range row.Result.Observations {
		source := transitions[i]
		if got.Run != int64(source.Run) || got.Step != int64(source.Sequence) {
			t.Errorf("observation identity lost: %+v", got)
		}
		if !reflect.DeepEqual(got.From, wants[i]) || !reflect.DeepEqual(got.To, wants[min(i+1, len(wants)-1)]) {
			t.Errorf("component observation %d lost module values:\nfrom=%v\nto=%v\nwant from=%v\nwant to=%v", i, got.From, got.To, wants[i], wants[min(i+1, len(wants)-1)])
		}
		if explored {
			wantArgs := []string{`<<9007199254740993, "arg">>`, `{"a", "b"}`, "p1", `(1 :> "one")`, "TRUE", "9007199254740993"}
			if got.Action != "Advance" || !reflect.DeepEqual(got.Args, wantArgs) {
				t.Errorf("attempt argument semantics changed: %+v", got)
			}
			if len(document.Attempts) != len(transitions) {
				t.Fatal("recording attempt count changed")
			}
			args, ok := document.Attempts[i][2].([]any)
			if !ok || !reflect.DeepEqual(args, source.Args) {
				t.Error("module conversion rewrote attempt arguments")
			}
			for _, arg := range wantArgs {
				if !strings.Contains(runner.bodies["Invariant_Tried"], arg) {
					t.Errorf("TLC attempts lost %s", arg)
				}
			}
		} else if got.Action != source.Entry || len(got.Args) != 0 {
			t.Error("sampled action changed")
		}
	}
	if runner.bodies["Invariant_ConformanceInit"] == "" || runner.bodies["Invariant_Conformance"] == "" {
		t.Fatal("TLC did not receive both initial states and transitions")
	}
	for i, state := range wants {
		for field, text := range state {
			if !strings.Contains(runner.bodies["Invariant_Conformance"], field+" |-> "+text) {
				t.Errorf("TLC input lost state %d field %s=%s", i, field, text)
			}
		}
	}
	if !bytes.Equal(rawBefore, moduleReceipt837Bytes(t, r.Application.Executions)) {
		t.Error("recording/attribution mutated raw observations")
	}
	r.Fingerprint = verify.Fingerprint(*r)
	beforeRender := moduleReceipt837Bytes(t, r)
	md := receipt.Markdown(r)
	if !bytes.Equal(beforeRender, moduleReceipt837Bytes(t, r)) {
		t.Error("Markdown mutated report")
	}
	_, section, found := strings.Cut(md, "### Component conformance\n")
	if !found {
		t.Fatal("no component conformance receipt")
	}
	section, _, _ = strings.Cut(section, "### Component runtime")
	codes := regexp.MustCompile(`<code>(.*?)</code>`).FindAllStringSubmatch(section, -1)
	visible := map[string]bool{}
	for _, code := range codes {
		visible[html.UnescapeString(code[1])] = true
	}
	for _, state := range wants {
		for _, text := range state {
			if !visible[text] {
				t.Errorf("Markdown component evidence lost literal %q", text)
			}
		}
	}
	for _, forbidden := range []string{"<script>", "</code><script>", "**forged**", "[link](https://evil.invalid)", "\nnext"} {
		if strings.Contains(section, forbidden) {
			t.Errorf("state escaped literal rendering: %q", forbidden)
		}
	}
	lines := strings.Split(strings.TrimSpace(section), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "| ") && strings.Count(line, "|") != 5 {
			t.Errorf("state created a Markdown table cell: %s", line)
		}
	}
	back := moduleReceipt837Clone(t, r)
	if verify.Fingerprint(*back) != r.Fingerprint || moduleReceipt837ComponentHash(back) != moduleReceipt837ComponentHash(r) {
		t.Error("JSON round trip changed evidence fingerprints")
	}
	if receipt.Markdown(back) != md || !reflect.DeepEqual(back.Components, r.Components) {
		t.Error("JSON round trip changed component JSON or Markdown")
	}
	if !bytes.Equal(rawBefore, moduleReceipt837Bytes(t, back.Application.Executions)) {
		t.Error("JSON round trip lost raw arrays or exact integers")
	}
	// Re-attribution after decoding must use the same module-value semantics.
	if err := verify.AttributePythonModuleReceipt837(back, &back.Components.Inventory); err != nil {
		t.Fatal(err)
	}
	if verify.Fingerprint(*back) != r.Fingerprint {
		t.Error("decoded raw evidence attributes differently")
	}
	moduleReceipt837Qualification(t, back, true, explored, explored)
	baseline := moduleReceipt837ComponentHash(r)
	changes := []struct {
		name   string
		change func([]map[string]any)
	}{
		{"element", func(s []map[string]any) { s[1]["status"] = []any{"running"} }},
		{"order", func(s []map[string]any) { s[2]["status"] = []any{"queued", "done", "queued"} }},
		{"multiplicity", func(s []map[string]any) { s[2]["status"] = []any{"queued", "done"} }},
		{"empty list versus string", func(s []map[string]any) { s[0]["status"] = "" }},
		{"exact integer", func(s []map[string]any) { s[2]["big"] = json.Number("9007199254740994") }},
		{"nested list", func(s []map[string]any) {
			s[2]["nested"].(map[string]any)["rows"].([]any)[0].(map[string]any)["items"] = []any{true, false}
		}},
	}
	for _, change := range changes {
		variant, _ := moduleReceipt837States(t)
		change.change(variant)
		other, otherInv := verify.PythonModuleReceiptFixture837(explored, variant)
		// Hold every non-observation conformance fact equal to baseline.
		other.Application.Conformance[0] = r.Application.Conformance[0]
		if err := verify.AttributePythonModuleReceipt837(other, otherInv); err != nil {
			t.Fatal(err)
		}
		if moduleReceipt837ComponentHash(other) == baseline {
			t.Errorf("component fingerprint ignores changed %s", change.name)
		}
	}
	// A failed attempt check must still preserve checked transitions and all
	// list evidence, and a failed conformance result must qualify nothing.
	if explored {
		failed := moduleReceipt837Clone(t, r)
		failed.Application.Conformance[0].Result.Tried.Passed = false
		if err := verify.AttributePythonModuleReceipt837(failed, inv); err != nil {
			t.Fatal(err)
		}
		moduleReceipt837Qualification(t, failed, true, false, false)
		if !reflect.DeepEqual(failed.Components.Conformance[0].Result.Observations, row.Result.Observations) {
			t.Error("failed attempts discarded state values")
		}
	}
	failed := moduleReceipt837Clone(t, r)
	failed.Application.Conformance[0].Result.Passed = false
	if err := verify.AttributePythonModuleReceipt837(failed, inv); err != nil {
		t.Fatal(err)
	}
	moduleReceipt837Qualification(t, failed, false, false, false)
	if !reflect.DeepEqual(failed.Components.Conformance[0].Result.Observations, row.Result.Observations) {
		t.Error("failed conformance discarded state values")
	}
	// Old scalar observations still use the shared encoder directly.
	legacy, legacyInv := verify.PythonModuleReceiptFixture837(explored, []map[string]any{
		{"status": "idle", "n": json.Number("9007199254740993"), "ok": false},
		{"status": "queued", "n": json.Number("9007199254740994"), "ok": true},
	})
	for i := range legacy.Application.Executions[0].Observations {
		legacy.Application.Executions[0].Observations[i].Form = ""
	}
	if err := verify.AttributePythonModuleReceipt837(legacy, legacyInv); err != nil {
		t.Fatal(err)
	}
	legacyRow := legacy.Components.Conformance[0].Result.Observations[0]
	if !reflect.DeepEqual(legacyRow.From, map[string]string{"status": `"idle"`, "n": "9007199254740993", "ok": "FALSE"}) || !reflect.DeepEqual(legacyRow.To, map[string]string{"status": `"queued"`, "n": "9007199254740994", "ok": "TRUE"}) {
		t.Error("non-module scalar encoding changed")
	}
}

func TestPythonModuleSampledComponentReceipts837(t *testing.T) {
	moduleReceipt837Exercise(t, false)
}

func TestPythonModuleExploredComponentReceipts837(t *testing.T) {
	moduleReceipt837Exercise(t, true)
}
