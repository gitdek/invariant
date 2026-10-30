package verify_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/gobra"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// This slice adds an attribution API, not an executor:
//
//   (*verify.Report).AttributeComponentProofs(*project.ComponentInventory,
//       []verify.ComponentProofResult) error
//
// ComponentProofResult has Target, Verifier string; Passed bool; Inputs
// []project.Component; Verified, Contracts, Errors []string, with their
// lower-case JSON names. Target, Verified and Contracts use canonical IDs,
// never legacy Code.Functions' bare names. Inputs are snapshots of the
// declarations actually supplied to the tool. Verified identifies the exact
// declarations covered by that result; Contracts identifies contracts checked
// by it. Future execution adapters must establish these facts from a run on
// those inputs; this API neither runs a prover nor infers them from intent.
//
// Accept only explicit inventories and non-model-only reports. Each supplied
// result must have a distinct, configured proof target. All referenced IDs
// must be in the inventory, with no duplicates within a reference list; every
// supplied input snapshot must exactly match its inventoried component.
// Reject invalid attribution atomically. For otherwise valid results, proof
// requires Passed, a nonempty Verifier, no Errors, the target in Verified,
// all selected contract IDs in Contracts, and snapshots for every Verified
// and Contracts ID. Missing evidence leaves the component unchecked. Failed
// attempts remain visible but prove nothing. No result transfers proof to
// another component, even one named in the same run's Verified list.
//
// Report JSON adds optional components:{inventory:<ComponentInventory>,
// proofs:[{target:<ID>,status:"proved"|"unchecked",result?:<result>}]},
// with one proof row for EVERY component, sorted by ID. Inventory and all
// reference sets are copied and canonically ordered. A missing result is
// omitted, never synthesized from configuration. There is no component score
// or overall assurance level replacing the distinct proof evidence.
//
// Attribution replaces the previous component evidence, sets Assurance to
// "component evidence", and recomputes Fingerprint, preserving gate Passed
// and all other recorded legacy checks. CodeAssurance and Claim return that
// same phrase for explicit reports, including after JSON decoding with a
// stale Assurance field; EverySize is false. This does not assign model
// testing, runtime checks or trusted assumptions to any component.

type componentProofJSON struct {
	Inventory project.ComponentInventory `json:"inventory"`
	Proofs    []struct {
		Target string                       `json:"target"`
		Status string                       `json:"status"`
		Result *verify.ComponentProofResult `json:"result"`
	} `json:"proofs"`
}

func cpJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func cpClone[T any](t *testing.T, v T) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(cpJSON(t, v), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func cpWrite(t *testing.T, root, name, body string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func cpID(source, symbol string) string {
	return "go:example.test/proofs/" + filepath.ToSlash(filepath.Dir(source)) + "#" + symbol + "@" + source
}

func cpFixture(t *testing.T) (string, *project.ComponentInventory) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"go.mod": "module example.test/proofs\n\ngo 1.27.1\n",
		"core/ops.go": "package core\ntype Alpha struct{}\ntype Beta struct{}\n" +
			"//@ ensures result >= 0\nfunc (a *Alpha) Step() (result int) { return 1 }\n" +
			"//@ ensures result >= 0\nfunc (b Beta) Step() (result int) { return 2 }\n" +
			"func Neighbor() {}\nfunc init() {}\n",
		"core/data.go":                 "package core\nconst Limit = 2\n",
		"contracts/contract.go":        "package contracts\n//@ ensures result >= 0\nfunc Contract() (result int) { return 0 }\n",
		"other/ops.go":                 "package other\ntype Alpha struct{}\nfunc (a *Alpha) Step() {}\n",
		".invariant/ratified.lock":     `{"bounds":{"Limit":"2"},"statements":[{"name":"Spec","kind":"spec"},{"name":"Safe","kind":"invariant"}]}`,
		".invariant/specs/Counter.tla": "---- MODULE Counter ----\nEXTENDS Naturals\nCONSTANT Limit\nVARIABLE n\nInit == n = 0\nNext == n' = n\nSpec == Init /\\ [][Next]_n\nSafe == n >= 0\n====\n",
	} {
		cpWrite(t, dir, name, body)
	}
	a := project.ComponentRef{Source: "core/ops.go", Symbol: "(*Alpha).Step"}
	b := project.ComponentRef{Source: "core/ops.go", Symbol: "Beta.Step"}
	c := project.ComponentRef{Source: "contracts/contract.go", Symbol: "Contract"}
	m := project.Manifest{Name: "component proofs", Language: "go", Code: "core", Module: ".invariant/specs/Counter.tla",
		Components: &project.ComponentsConfig{Scope: []string{"other", "core", "contracts"}, Checks: []project.ComponentCheck{
			{Target: a, Proof: &project.ComponentProof{Contracts: []project.ComponentRef{c, a}}},
			{Target: b, Proof: &project.ComponentProof{Contracts: []project.ComponentRef{b}}},
		}}}
	cpWrite(t, dir, ".invariant/invariant.json", string(cpJSON(t, m)))
	return dir, cpResolve(t, dir)
}

func cpResolve(t *testing.T, dir string) *project.ComponentInventory {
	t.Helper()
	p, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := project.ResolveComponents(p)
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func cpResult(t *testing.T, inv *project.ComponentInventory, target string) verify.ComponentProofResult {
	t.Helper()
	r := verify.ComponentProofResult{Target: target, Verifier: "Gobra", Passed: true, Verified: []string{target}}
	for _, check := range inv.Checks {
		if check.Target == target && check.Proof != nil {
			r.Contracts = append([]string(nil), check.Proof.Contracts...)
		}
	}
	for _, c := range inv.Components {
		if c.ID == target || slices.Contains(r.Contracts, c.ID) {
			r.Inputs = append(r.Inputs, c)
		}
	}
	return r
}

func cpReport() verify.Report {
	// These are intentionally stronger-looking legacy results. None may be
	// used as evidence of a component check this API was not given.
	return verify.Report{Project: "component proofs", Passed: true, Assurance: "proved",
		Bounds: map[string]string{"Limit": "2"}, Build: verify.Build{Passed: true},
		Code:        &verify.Code{Verifier: "Gobra", Passed: true, Functions: []string{"Step"}},
		Agreement:   &verify.Agreement{Passed: true},
		Conformance: &conformance.Result{Passed: true, Exhaustive: true, States: 1, ModelStates: 1},
		Larger:      &verify.Larger{Passed: true, Bounds: map[string]string{"Limit": "3"}},
		Toolchain:   verify.Toolchain{GobraImage: "gobra@sha256:" + strings.Repeat("a", 64)},
	}
}

func cpAttach(t *testing.T, inv *project.ComponentInventory, results []verify.ComponentProofResult) verify.Report {
	t.Helper()
	r := cpReport()
	if err := r.AttributeComponentProofs(inv, results); err != nil {
		t.Fatal(err)
	}
	return r
}

func cpEvidence(t *testing.T, r verify.Report) componentProofJSON {
	t.Helper()
	var doc struct {
		Components *componentProofJSON `json:"components"`
	}
	if err := json.Unmarshal(cpJSON(t, r), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Components == nil || doc.Components.Inventory.Mode != "explicit" {
		t.Fatal("report lost the explicit inventory and proof evidence")
	}
	return *doc.Components
}

func cpStatuses(t *testing.T, r verify.Report, proved ...string) {
	t.Helper()
	e := cpEvidence(t, r)
	if len(e.Proofs) != len(e.Inventory.Components) {
		t.Fatalf("proof rows = %d, scoped components = %d", len(e.Proofs), len(e.Inventory.Components))
	}
	for i, c := range e.Inventory.Components {
		want := "unchecked"
		if slices.Contains(proved, c.ID) {
			want = "proved"
		}
		if p := e.Proofs[i]; p.Target != c.ID || p.Status != want {
			t.Errorf("proof row %d = %s/%s, want %s/%s", i, p.Target, p.Status, c.ID, want)
		}
	}
	if r.Assurance != "component evidence" || r.Claim() != "component evidence" || r.CodeAssurance() != "component evidence" || r.EverySize() {
		t.Errorf("explicit report made a project-wide claim: assurance=%q claim=%q everySize=%v", r.Assurance, r.Claim(), r.EverySize())
	}
	if r.Fingerprint != verify.Fingerprint(r) || !strings.HasPrefix(r.Fingerprint, "sha256:") || len(r.Fingerprint) != 71 {
		t.Error("attribution did not finalize the evidence fingerprint")
	}
}

func TestComponentProofAttributionKeepsNeighborsUnchecked(t *testing.T) {
	_, inv := cpFixture(t)
	a := cpID("core/ops.go", "(*Alpha).Step")
	b := cpID("core/ops.go", "Beta.Step")
	c := cpID("contracts/contract.go", "Contract")
	result := cpResult(t, inv, a)
	result.Verified = append(result.Verified, c)
	before := string(cpJSON(t, inv))
	r := cpAttach(t, inv, []verify.ComponentProofResult{result})
	cpStatuses(t, r, a)
	if got := cpEvidence(t, r).Inventory; !reflect.DeepEqual(got, *inv) {
		t.Fatal("the receipt lost inventory files, identities or intended selections")
	}
	if string(cpJSON(t, inv)) != before {
		t.Fatal("attribution mutated the caller's inventory")
	}
	// A second result can establish a second target independently, but a
	// declaration named only as a checked contract stays unchecked.
	if err := r.AttributeComponentProofs(inv, []verify.ComponentProofResult{result, cpResult(t, inv, b)}); err != nil {
		t.Fatal(err)
	}
	cpStatuses(t, r, a, b)
	// Replacing the evidence must not leave a proof from the previous run.
	if err := r.AttributeComponentProofs(inv, nil); err != nil {
		t.Fatal(err)
	}
	cpStatuses(t, r)
	for _, row := range cpEvidence(t, r).Proofs {
		if row.Result != nil {
			t.Error("missing tool results were synthesized from intent or retained from an earlier run")
		}
	}
}

func TestComponentProofRequiresSuccessfulExactCoverage(t *testing.T) {
	_, inv := cpFixture(t)
	a := cpID("core/ops.go", "(*Alpha).Step")
	c := cpID("contracts/contract.go", "Contract")
	for _, tc := range []struct {
		name string
		edit func(*verify.ComponentProofResult)
	}{
		{"failed output", func(r *verify.ComponentProofResult) {
			p := gobra.Parse("Gobra found 1 errors", 1)
			r.Passed, r.Errors = p.Passed, p.Errors
		}},
		{"missing success output", func(r *verify.ComponentProofResult) {
			p := gobra.Parse("Gobra started", 0)
			r.Passed, r.Errors = p.Passed, p.Errors
		}},
		{"empty verified list", func(r *verify.ComponentProofResult) { r.Verified = nil }},
		{"only selected contract proved", func(r *verify.ComponentProofResult) { r.Verified = []string{c} }},
		{"other receiver proved", func(r *verify.ComponentProofResult) {
			r.Verified = []string{cpID("core/ops.go", "Beta.Step")}
			r.Inputs = append([]project.Component(nil), inv.Components...)
		}},
		{"same receiver in other package proved", func(r *verify.ComponentProofResult) {
			r.Verified = []string{cpID("other/ops.go", "(*Alpha).Step")}
			r.Inputs = append([]project.Component(nil), inv.Components...)
		}},
		{"selected contracts never checked", func(r *verify.ComponentProofResult) { r.Contracts = nil }},
		{"only target contract checked", func(r *verify.ComponentProofResult) { r.Contracts = []string{a} }},
		{"no source snapshots", func(r *verify.ComponentProofResult) { r.Inputs = nil }},
		{"missing contract snapshot", func(r *verify.ComponentProofResult) {
			r.Inputs = slices.DeleteFunc(r.Inputs, func(c project.Component) bool { return c.ID != a })
		}},
		{"unnamed verifier", func(r *verify.ComponentProofResult) { r.Verifier = "" }},
		{"contradictory errors", func(r *verify.ComponentProofResult) { r.Errors = []string{"proof did not finish"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := cpResult(t, inv, a)
			tc.edit(&result)
			r := cpAttach(t, inv, []verify.ComponentProofResult{result})
			cpStatuses(t, r)
			for _, row := range cpEvidence(t, r).Proofs {
				if row.Target == a && (row.Result == nil || row.Result.Passed != result.Passed || !reflect.DeepEqual(row.Result.Errors, result.Errors)) {
					t.Error("unsuccessful tool evidence disappeared from the report")
				}
			}
		})
	}
	cpStatuses(t, cpAttach(t, inv, nil))
	// A separate gate failure does not erase successful proof evidence.
	r := cpReport()
	r.Passed, r.Build.Passed = false, false
	if err := r.AttributeComponentProofs(inv, []verify.ComponentProofResult{cpResult(t, inv, a)}); err != nil {
		t.Fatal(err)
	}
	cpStatuses(t, r, a)
	if r.Passed || r.Build.Passed {
		t.Fatal("attribution changed gate failures into a pass")
	}
}

func TestComponentProofRejectsUnboundOrStaleResults(t *testing.T) {
	_, inv := cpFixture(t)
	a := cpID("core/ops.go", "(*Alpha).Step")
	for _, tc := range []struct {
		name string
		edit func(*project.ComponentInventory, *[]verify.ComponentProofResult)
	}{
		{"legacy inventory", func(i *project.ComponentInventory, _ *[]verify.ComponentProofResult) { i.Mode = "legacy" }},
		{"unknown target", func(_ *project.ComponentInventory, rs *[]verify.ComponentProofResult) {
			(*rs)[0].Target = "go:missing#Step@missing.go"
		}},
		{"unselected proof", func(_ *project.ComponentInventory, rs *[]verify.ComponentProofResult) {
			(*rs)[0].Target = cpID("core/ops.go", "Neighbor")
		}},
		{"duplicate target results", func(_ *project.ComponentInventory, rs *[]verify.ComponentProofResult) { *rs = append(*rs, (*rs)[0]) }},
		{"bare verified name", func(_ *project.ComponentInventory, rs *[]verify.ComponentProofResult) {
			(*rs)[0].Verified = []string{"Step"}
		}},
		{"unknown contract", func(_ *project.ComponentInventory, rs *[]verify.ComponentProofResult) {
			(*rs)[0].Contracts = []string{"missing"}
		}},
		{"duplicate verified ID", func(_ *project.ComponentInventory, rs *[]verify.ComponentProofResult) {
			(*rs)[0].Verified = []string{a, a}
		}},
		{"duplicate contract ID", func(_ *project.ComponentInventory, rs *[]verify.ComponentProofResult) {
			(*rs)[0].Contracts = []string{a, a}
		}},
		{"duplicate snapshot", func(_ *project.ComponentInventory, rs *[]verify.ComponentProofResult) {
			(*rs)[0].Inputs = append((*rs)[0].Inputs, (*rs)[0].Inputs[0])
		}},
		{"unknown snapshot", func(_ *project.ComponentInventory, rs *[]verify.ComponentProofResult) {
			(*rs)[0].Inputs[0].ID = "missing"
		}},
		{"stale source", func(_ *project.ComponentInventory, rs *[]verify.ComponentProofResult) {
			(*rs)[0].Inputs[0].SourceSHA256 = "sha256:" + strings.Repeat("f", 64)
		}},
		{"stale contract", func(_ *project.ComponentInventory, rs *[]verify.ComponentProofResult) {
			(*rs)[0].Inputs[0].ContractSHA256 = "sha256:" + strings.Repeat("f", 64)
		}},
		{"wrong declaration", func(_ *project.ComponentInventory, rs *[]verify.ComponentProofResult) {
			(*rs)[0].Inputs[0].Symbol = "Neighbor"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := cpAttach(t, inv, []verify.ComponentProofResult{cpResult(t, inv, a)})
			before := string(cpJSON(t, r))
			i := cpClone(t, *inv)
			results := []verify.ComponentProofResult{cpResult(t, inv, a)}
			tc.edit(&i, &results)
			if err := r.AttributeComponentProofs(&i, results); err == nil {
				t.Fatal("invalid attribution was accepted")
			}
			if string(cpJSON(t, r)) != before {
				t.Fatal("rejected attribution partially changed the report")
			}
		})
	}
	for _, modelOnly := range []bool{false, true} {
		r := cpReport()
		r.ModelOnly = modelOnly
		i := inv
		if !modelOnly {
			i = nil
		}
		before := string(cpJSON(t, r))
		if err := r.AttributeComponentProofs(i, nil); err == nil || string(cpJSON(t, r)) != before {
			t.Fatal("nil inventory or model-only attribution must be rejected atomically")
		}
	}
}

func TestComponentProofFingerprintsBindSourcesAndContracts(t *testing.T) {
	dir, inv := cpFixture(t)
	a := cpID("core/ops.go", "(*Alpha).Step")
	b := cpID("core/ops.go", "Beta.Step")
	results := []verify.ComponentProofResult{cpResult(t, inv, a), cpResult(t, inv, b)}
	r := cpAttach(t, inv, results)
	want := r.Fingerprint
	_, relocated := cpFixture(t)
	if got := cpAttach(t, relocated, results).Fingerprint; got != want {
		t.Fatal("moving the checkout changed identical evidence")
	}
	// Inventory order and execution completion order have no meaning.
	reordered := cpClone(t, *inv)
	slices.Reverse(reordered.Scope)
	slices.Reverse(reordered.Files)
	slices.Reverse(reordered.Components)
	slices.Reverse(reordered.Checks)
	for i := range reordered.Checks {
		slices.Reverse(reordered.Checks[i].Proof.Contracts)
	}
	rs := cpClone(t, results)
	slices.Reverse(rs)
	for i := range rs {
		slices.Reverse(rs[i].Inputs)
		slices.Reverse(rs[i].Verified)
		slices.Reverse(rs[i].Contracts)
	}
	beforeInventory, beforeResults := string(cpJSON(t, reordered)), string(cpJSON(t, rs))
	permuted := cpAttach(t, &reordered, rs)
	if permuted.Fingerprint != want || string(cpJSON(t, reordered)) != beforeInventory || string(cpJSON(t, rs)) != beforeResults {
		t.Fatal("canonical attribution changed its inputs or depended on their order")
	}
	// The stored evidence owns its slices, including nested result slices.
	saved := string(cpJSON(t, permuted))
	reordered.Components[0].SourceSHA256 = "changed"
	reordered.Checks[0].Proof.Contracts[0] = "changed"
	rs[0].Inputs[0].SourceSHA256 = "changed"
	rs[0].Verified[0] = "changed"
	if string(cpJSON(t, permuted)) != saved {
		t.Fatal("the report retained mutable caller-owned evidence")
	}
	volatile := cpClone(t, r)
	volatile.GeneratedAt, volatile.Agent, volatile.Build.Output, volatile.Toolchain.Go = "tomorrow", "another agent", "test timings", "another patch version"
	if verify.Fingerprint(volatile) != want {
		t.Fatal("volatile metadata changed component fingerprints")
	}
	// Changing actual source files re-resolves identities; proof inputs in
	// the new report are snapshots of those changed files, not old evidence.
	for _, tc := range []struct{ name, file, old, new string }{
		{"target body", "core/ops.go", "return 1", "return 3"},
		{"target contract", "core/ops.go", "ensures result >= 0", "ensures result >= 1"},
		{"unchecked neighbor", "other/ops.go", "Step() {}", "Step() { println(1) }"},
		{"selected contract body", "contracts/contract.go", "return 0", "return 1"},
		{"selected contract text", "contracts/contract.go", "ensures result >= 0", "ensures result >= 1"},
		{"file with no functions", "core/data.go", "Limit = 2", "Limit = 3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.file)
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			cpWrite(t, dir, tc.file, strings.Replace(string(original), tc.old, tc.new, 1))
			changed := cpResolve(t, dir)
			got := cpAttach(t, changed, []verify.ComponentProofResult{cpResult(t, changed, a), cpResult(t, changed, b)})
			cpWrite(t, dir, tc.file, string(original))
			if got.Fingerprint == want {
				t.Fatal("changed source or contract did not change fingerprint")
			}
		})
	}
	// Contract identity is independently bound even if a caller supplies a
	// different contract snapshot with the same source hash.
	changed := cpClone(t, *inv)
	for i := range changed.Components {
		if changed.Components[i].ID == a {
			changed.Components[i].ContractSHA256 = "sha256:" + strings.Repeat("c", 64)
		}
	}
	if cpAttach(t, &changed, []verify.ComponentProofResult{cpResult(t, &changed, a), cpResult(t, &changed, b)}).Fingerprint == want {
		t.Fatal("contract identity was omitted from the fingerprint")
	}
	for _, mutate := range []func(*verify.Report){
		func(r *verify.Report) { r.Toolchain.GobraImage = "different proof tool" },
	} {
		changed := cpClone(t, r)
		mutate(&changed)
		if verify.Fingerprint(changed) == want {
			t.Fatal("proof tool pin was omitted")
		}
	}
	failed := cpClone(t, results)
	failed[0].Passed, failed[0].Errors = false, []string{"verification failed"}
	if cpAttach(t, inv, failed).Fingerprint == want {
		t.Fatal("proof outcome was omitted")
	}
	selection := cpClone(t, *inv)
	for i := range selection.Checks {
		if selection.Checks[i].Target == a {
			selection.Checks[i].Proof.Contracts = []string{a}
		}
	}
	if cpAttach(t, &selection, results).Fingerprint == want {
		t.Fatal("contract selection was omitted")
	}
	// Even a scope with no functions must remain explicit and bind its file.
	empty := &project.ComponentInventory{Mode: "explicit", Scope: []string{"core"}, Files: []project.ComponentFile{{Path: "core/data.go", SHA256: "sha256:" + strings.Repeat("a", 64)}}}
	emptyReport := cpAttach(t, empty, nil)
	cpStatuses(t, emptyReport)
	empty.Files[0].SHA256 = "sha256:" + strings.Repeat("b", 64)
	if cpAttach(t, empty, nil).Fingerprint == emptyReport.Fingerprint {
		t.Fatal("empty component scope lost its source identity")
	}
}

func TestComponentProofAttributionKeepsExecutionDisabled(t *testing.T) {
	dir, inv := cpFixture(t)
	bin := t.TempDir()
	log := filepath.Join(bin, "called")
	t.Setenv("COMPONENT_PROOF_ACCEPT_LOG", log)
	for _, name := range []string{"go", "docker"} {
		cpWrite(t, bin, name, "#!/bin/sh\necho called >> \"$COMPONENT_PROOF_ACCEPT_LOG\"\nexit 73\n")
		if err := os.Chmod(filepath.Join(bin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	a := cpID("core/ops.go", "(*Alpha).Step")
	for _, passed := range []bool{false, true} {
		r := cpReport()
		r.Passed = passed
		legacyCode := string(cpJSON(t, r.Code))
		if err := r.AttributeComponentProofs(inv, []verify.ComponentProofResult{cpResult(t, inv, a)}); err != nil {
			t.Fatal(err)
		}
		if r.Passed != passed || string(cpJSON(t, r.Code)) != legacyCode {
			t.Fatal("attribution rewrote the gate outcome or legacy evidence")
		}
	}
	if err := verify.Check(dir); !errors.Is(err, verify.ErrComponents) {
		t.Fatalf("preflight no longer refuses explicit execution: %v", err)
	}
	if r, err := verify.Run(context.Background(), dir, "", toolchain.Toolchain{}); r != nil || !errors.Is(err, verify.ErrComponents) {
		t.Fatalf("explicit execution became available before adapters: report=%#v error=%v", r, err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("attribution or refused verification started an external tool")
	}
}
