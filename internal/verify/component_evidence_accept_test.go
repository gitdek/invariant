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

// Issue #274 adds these public APIs (the wire helpers keep the base compiling
// so missing functionality fails as an assertion, not a compiler error):
//
//   Report.AttributeComponentAssumptions(*project.ComponentInventory,
//       []verify.ComponentAssumptionRecord) error
//   ComponentAssumptionRecord { Target string; Input project.Component;
//       Assumption project.ResolvedComponentAssumption }
//   ComponentAssumptionEvidence { Target, Status string; Verified bool;
//       Record ComponentAssumptionRecord }
//   ComponentEvidence.Assumptions []ComponentAssumptionEvidence
//
// Assumption rows are sorted by (target, record.assumption.id); Status is
// "assumed", Verified is false (present in JSON), and Record is the supplied
// target snapshot plus exact selected assumption, not a tool result. No record
// is invented for a declaration without one. Empty/cleared assumptions may be
// omitted or an empty JSON collection. Their JSON keys are target, status,
// verified, record; record contains target, input, assumption.
//
// ComponentProofResult keeps all existing fields and adds optional Invocation
// *project.ResolvedComponentInvocation, FileInputs []project.ComponentFile,
// and Tool *ComponentProofTool { Name, Version string; Pins map[string]string },
// using invocation, file_inputs and tool JSON keys (omitempty). The actual
// invocation matches this target's selected (kind=proof, check=target, id),
// command, ordered args, working_dir, env, sampling and input snapshot set.
// FileInputs are additional actual file provenance, never a replacement for
// declaration Inputs. When present they include the selected target source;
// all supplied files match the known production/provenance/module snapshots,
// including known extra inputs beyond the invocation's declared input set.
// Invocation and FileInputs can each be supplied independently. Tool identity
// and pins are opaque supplied data, copied and fingerprinted, never executed.
// Old callers need none of these fields; attribution never synthesizes them.
//
// Both operations replace/clear only their own kind, on a detached candidate.
// They validate the complete inventory and retained evidence before committing
// once, including retained incomplete attempts' target/contract snapshots and
// selections. Invalid batches preserve full report JSON and both stored and
// recomputed fingerprints. A changed inventory cannot silently promote or
// demote retained coverage. Canonically equivalent ordering is accepted.
// Targets must be production declarations; selected tests/drivers and files
// use separate provenance. Matching cross-collection overlaps are valid.
// No new mandatory provenance object or filesystem lookup is imposed on old
// valid proof-only callers, and all earlier acceptance contracts remain.

type ce274AssumptionRecord struct {
	Target     string                              `json:"target"`
	Input      project.Component                   `json:"input"`
	Assumption project.ResolvedComponentAssumption `json:"assumption"`
}

type ce274AssumptionEvidence struct {
	Target   string                `json:"target"`
	Status   string                `json:"status"`
	Verified *bool                 `json:"verified"`
	Record   ce274AssumptionRecord `json:"record"`
}

type ce274Tool struct {
	Name    string            `json:"name"`
	Version string            `json:"version"`
	Pins    map[string]string `json:"pins,omitempty"`
}

type ce274Proof struct {
	verify.ComponentProofResult
	Invocation *project.ResolvedComponentInvocation `json:"invocation,omitempty"`
	FileInputs []project.ComponentFile              `json:"file_inputs,omitempty"`
	Tool       *ce274Tool                           `json:"tool,omitempty"`
}

type ce274FixtureData struct {
	Root                                 string
	Inv                                  *project.ComponentInventory
	Target, Other, Assumed, Helper, Same string
}

func ce274Fixture(t *testing.T) ce274FixtureData {
	t.Helper()
	root := provenance257Fixture(t)
	cpWrite(t, root, "core/core.go", "package core\n"+
		"//@ ensures result >= 0\nfunc Step() (result int) { return 1 }\n"+
		"//@ ensures result >= 0\nfunc Other() (result int) { return 2 }\n"+
		"func Neighbor() {}\nfunc Assumed() {}\n")
	cpWrite(t, root, "core/contracts.go", "package core\n//@ ensures result >= 0\nfunc Contract() (result int) { return 0 }\n")
	cpWrite(t, root, "neighbors/extra.go", "package neighbors\nfunc Unchecked() {}\nfunc Step() {}\n")
	provenance257EditManifest(t, root, func(m map[string]any) {
		step := provenance257ConfigCheck(m, "Step")
		step["proof"] = map[string]any{"contracts": []any{provenance257Ref("core/core.go", "Step"), provenance257Ref("core/contracts.go", "Contract")}}
		calls := step["invocations"].([]any)
		repeat := cpClone(t, calls[0].(map[string]any))
		repeat["id"] = "repeat"
		step["invocations"] = append(calls, repeat)
		delete(provenance257ConfigCheck(m, "Assumed"), "tests")
		other := cpClone(t, step)
		other["target"] = provenance257Ref("core/core.go", "Other")
		other["proof"] = map[string]any{"contracts": []any{provenance257Ref("core/core.go", "Other")}}
		delete(other, "tests")
		delete(other, "conformance")
		otherCall := cpClone(t, calls[0].(map[string]any))
		otherCall["check"] = provenance257Ref("core/core.go", "Other")
		other["invocations"] = []any{otherCall}
		cfg := m["components"].(map[string]any)
		cfg["checks"] = append(cfg["checks"].([]any), other)
	})
	return ce274FixtureData{Root: root, Inv: cpResolve(t, root),
		Target: provenance257ID("core/core.go", "Step"), Other: provenance257ID("core/core.go", "Other"),
		Assumed: provenance257ID("core/core.go", "Assumed"), Helper: provenance257ID("core/contracts.go", "Contract"),
		Same: provenance257ID("neighbors/extra.go", "Step")}
}

func ce274Check(t *testing.T, inv *project.ComponentInventory, target string) *project.ResolvedComponentCheck {
	t.Helper()
	for i := range inv.Checks {
		if inv.Checks[i].Target == target {
			return &inv.Checks[i]
		}
	}
	t.Fatalf("fixture lacks check %s", target)
	return nil
}

func ce274Component(t *testing.T, inv *project.ComponentInventory, id string) project.Component {
	t.Helper()
	all := slices.Clone(inv.Components)
	if inv.Provenance != nil {
		all = append(all, inv.Provenance.Declarations...)
	}
	for _, c := range all {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("fixture lacks component %s", id)
	return project.Component{}
}

func ce274ProofFor(t *testing.T, f ce274FixtureData, target string, metadata bool) ce274Proof {
	t.Helper()
	p := ce274Proof{ComponentProofResult: cpResult(t, f.Inv, target)}
	if !metadata {
		return p
	}
	for _, v := range ce274Check(t, f.Inv, target).Invocations {
		if v.Kind == "proof" && v.ID == "run" {
			v = cpClone(t, v)
			p.Invocation = &v
		}
	}
	if p.Invocation == nil {
		t.Fatal("fixture lacks selected proof invocation")
	}
	// Actual source, dependency, model and non-Go data inputs include known
	// extras selected for a different check. They are still supplied facts.
	byPath := map[string]project.ComponentFile{}
	for _, fs := range [][]project.ComponentFile{f.Inv.Files, f.Inv.Provenance.Files, f.Inv.Provenance.Modules} {
		for _, file := range fs {
			byPath[file.Path] = file
		}
	}
	for _, file := range byPath {
		p.FileInputs = append(p.FileInputs, file)
	}
	slices.SortFunc(p.FileInputs, func(a, b project.ComponentFile) int { return strings.Compare(a.Path, b.Path) })
	p.Tool = &ce274Tool{Name: "Gobra", Version: "1.2.3", Pins: map[string]string{"image": "gobra@sha256:" + strings.Repeat("a", 64), "recipe": "sha256:" + strings.Repeat("b", 64)}}
	return p
}

func ce274Records(t *testing.T, f ce274FixtureData) []ce274AssumptionRecord {
	t.Helper()
	var records []ce274AssumptionRecord
	for _, target := range []string{f.Target, f.Assumed} {
		for _, a := range ce274Check(t, f.Inv, target).Assumptions {
			if a.ID == "clock" {
				records = append(records, ce274AssumptionRecord{Target: target, Input: ce274Component(t, f.Inv, target), Assumption: a})
			}
		}
	}
	if len(records) != 2 {
		t.Fatal("fixture lacks selected assumptions")
	}
	return records
}

func ce274Proofs(t *testing.T, r *verify.Report, inv *project.ComponentInventory, proofs []ce274Proof) error {
	t.Helper()
	var supplied []verify.ComponentProofResult
	if err := json.Unmarshal(cpJSON(t, proofs), &supplied); err != nil {
		t.Fatal(err)
	}
	return r.AttributeComponentProofs(inv, supplied)
}

func ce274Assumptions(t *testing.T, r *verify.Report, inv *project.ComponentInventory, records []ce274AssumptionRecord) error {
	t.Helper()
	m := reflect.ValueOf(r).MethodByName("AttributeComponentAssumptions")
	if !m.IsValid() {
		return fmt.Errorf("missing Report.AttributeComponentAssumptions")
	}
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	if m.Type().NumIn() != 2 || m.Type().In(0) != reflect.TypeOf(inv) || m.Type().In(1).Kind() != reflect.Slice ||
		m.Type().NumOut() != 1 || m.Type().Out(0) != errorType {
		t.Fatal("AttributeComponentAssumptions must take an inventory and a slice of supplied records and return error")
	}
	arg := reflect.New(m.Type().In(1))
	if err := json.Unmarshal(cpJSON(t, records), arg.Interface()); err != nil {
		t.Fatal(err)
	}
	out := m.Call([]reflect.Value{reflect.ValueOf(inv), arg.Elem()})[0]
	if out.IsNil() {
		return nil
	}
	return out.Interface().(error)
}

func ce274AssumptionRows(t *testing.T, r verify.Report) []ce274AssumptionEvidence {
	t.Helper()
	var doc struct {
		Components struct {
			Assumptions []ce274AssumptionEvidence `json:"assumptions"`
		} `json:"components"`
	}
	if err := json.Unmarshal(cpJSON(t, r), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Components.Assumptions
}

func ce274WantRecords(t *testing.T, r verify.Report, records []ce274AssumptionRecord) {
	t.Helper()
	rows := ce274AssumptionRows(t, r)
	if len(rows) != len(records) {
		t.Fatalf("recorded assumptions = %d, want %d supplied records", len(rows), len(records))
	}
	want := cpClone(t, records)
	slices.SortFunc(want, func(a, b ce274AssumptionRecord) int {
		if n := strings.Compare(a.Target, b.Target); n != 0 {
			return n
		}
		return strings.Compare(a.Assumption.ID, b.Assumption.ID)
	})
	for i, row := range rows {
		if row.Target != want[i].Target || row.Status != "assumed" || row.Verified == nil || *row.Verified || row.Record != want[i] {
			t.Fatalf("assumption row lost supplied facts or assumed/unverified label: %#v; want %#v", row, want[i])
		}
	}
}

func ce274Statuses(t *testing.T, r verify.Report, proved ...string) {
	t.Helper()
	cpStatuses(t, r, proved...)
	for _, row := range r.Components.Proofs {
		if !slices.ContainsFunc(r.Components.Inventory.Components, func(c project.Component) bool { return c.ID == row.Target }) {
			t.Fatal("selected test/driver became a production proof row")
		}
	}
}

func ce274Legacy(t *testing.T, r verify.Report) string {
	t.Helper()
	r.Components, r.Assurance, r.Fingerprint = nil, "", ""
	return string(cpJSON(t, r))
}

func ce274Seed() verify.Report {
	r := cpReport()
	r.Ratified = &project.Ratification{By: "owner", Issue: 274, Proposal: "sha256:ratified"}
	r.Proposal, r.Decision = r.Ratified.Proposal, "D-0163"
	r.Pins = []verify.Pin{{Name: "Safe", Kind: "invariant", Says: "safe", Want: "pinned", Got: "pinned", Match: true}}
	return r
}

func ce274Attach(t *testing.T, f ce274FixtureData, metadata bool) verify.Report {
	t.Helper()
	r := ce274Seed()
	if err := ce274Proofs(t, &r, f.Inv, []ce274Proof{ce274ProofFor(t, f, f.Target, metadata)}); err != nil {
		t.Fatal(err)
	}
	if err := ce274Assumptions(t, &r, f.Inv, ce274Records(t, f)); err != nil {
		t.Fatal(err)
	}
	ce274WantRecords(t, r, ce274Records(t, f))
	ce274Statuses(t, r, f.Target)
	return r
}

func ce274Unchanged(t *testing.T, r *verify.Report, operation func() error) {
	t.Helper()
	before, stored, recomputed := string(cpJSON(t, r)), r.Fingerprint, verify.Fingerprint(*r)
	if err := operation(); err == nil {
		t.Fatal("invalid attribution succeeded")
	}
	if string(cpJSON(t, r)) != before || r.Fingerprint != stored || verify.Fingerprint(*r) != recomputed {
		t.Fatal("invalid attribution changed report bytes or stored/recomputed fingerprint")
	}
}

func TestIndependentComponentEvidenceCoexistsWithoutTransferringProof(t *testing.T) {
	f := ce274Fixture(t)
	r := ce274Seed()
	p := ce274ProofFor(t, f, f.Target, false)
	// Supersets remain valid: a helper and same-named neighbor may be named
	// verified, another contract checked, and known declarations supplied.
	p.Verified = append(p.Verified, f.Helper, f.Same)
	p.Contracts = append(p.Contracts, f.Other)
	p.Inputs = slices.Clone(f.Inv.Components)
	for _, declaration := range f.Inv.Provenance.Declarations {
		if !slices.ContainsFunc(p.Inputs, func(c project.Component) bool { return c.ID == declaration.ID }) {
			p.Inputs = append(p.Inputs, declaration)
		}
	}
	if err := ce274Proofs(t, &r, f.Inv, []ce274Proof{p}); err != nil {
		t.Fatal(err)
	}
	ce274Statuses(t, r, f.Target)
	ce274WantRecords(t, r, nil)
	legacy := ce274Legacy(t, r)
	if err := ce274Assumptions(t, &r, f.Inv, ce274Records(t, f)); err != nil {
		t.Fatal(err)
	}
	ce274Statuses(t, r, f.Target)
	ce274WantRecords(t, r, ce274Records(t, f))
	if ce274Legacy(t, r) != legacy {
		t.Fatal("assumptions changed legacy checks or ratified data")
	}
	// A stale aggregate assurance label after decoding cannot turn these
	// independent records into an all-components or every-size claim.
	decoded := cpClone(t, r)
	decoded.Assurance = "proved"
	if decoded.CodeAssurance() != "component evidence" || decoded.Claim() != "component evidence" || decoded.EverySize() || verify.Fingerprint(decoded) != r.Fingerprint {
		t.Fatal("decoded explicit report trusted a stale aggregate label")
	}
	for _, tc := range []struct {
		name string
		edit func(*ce274Proof)
	}{
		{"failed", func(p *ce274Proof) { p.Passed = false }},
		{"errors", func(p *ce274Proof) { p.Errors = []string{"failed"} }},
		{"no verifier", func(p *ce274Proof) { p.Verifier = "" }},
		{"no verified target", func(p *ce274Proof) { p.Verified = []string{f.Helper} }},
		{"same named target", func(p *ce274Proof) { p.Verified = []string{f.Same} }},
		{"missing selected contract", func(p *ce274Proof) { p.Contracts = []string{f.Target} }},
		{"missing target snapshot", func(p *ce274Proof) {
			p.Inputs = slices.DeleteFunc(p.Inputs, func(c project.Component) bool { return c.ID == f.Target })
		}},
		{"missing extra verified snapshot", func(p *ce274Proof) {
			p.Inputs = slices.DeleteFunc(p.Inputs, func(c project.Component) bool { return c.ID == f.Same })
		}},
		{"missing extra contract snapshot", func(p *ce274Proof) {
			p.Inputs = slices.DeleteFunc(p.Inputs, func(c project.Component) bool { return c.ID == f.Other })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := cpClone(t, p)
			tc.edit(&x)
			if err := ce274Proofs(t, &r, f.Inv, []ce274Proof{x}); err != nil {
				t.Fatal(err)
			}
			ce274Statuses(t, r)
			ce274WantRecords(t, r, ce274Records(t, f))
			var retained *verify.ComponentProofResult
			for _, row := range r.Components.Proofs {
				if row.Target == f.Target {
					retained = row.Result
				}
			}
			if retained == nil || retained.Passed != x.Passed || !slices.Equal(retained.Errors, x.Errors) {
				t.Fatal("incomplete/failed attempt vanished")
			}
		})
	}
}

func TestIndependentComponentEvidenceReplacesAndClearsOnlyItsKind(t *testing.T) {
	f := ce274Fixture(t)
	for _, passed := range []bool{false, true} {
		for _, assumptionsFirst := range []bool{false, true} {
			for _, clearAssumptionsFirst := range []bool{false, true} {
				t.Run(fmt.Sprintf("pass=%v/assumptionsFirst=%v/clearAssumptionsFirst=%v", passed, assumptionsFirst, clearAssumptionsFirst), func(t *testing.T) {
					r := ce274Seed()
					r.Passed, r.Build.Passed = passed, passed
					legacy := ce274Legacy(t, r)
					records := ce274Records(t, f)
					proof := []ce274Proof{ce274ProofFor(t, f, f.Target, false)}
					proofOp := func() {
						if err := ce274Proofs(t, &r, f.Inv, proof); err != nil {
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
						ce274Statuses(t, r)
						proofOp()
					} else {
						proofOp()
						assumptionOp()
					}
					ce274Statuses(t, r, f.Target)
					ce274WantRecords(t, r, records)
					// Replace one kind's set without merging old records or
					// dropping the other kind's records.
					proof = []ce274Proof{ce274ProofFor(t, f, f.Other, true)}
					proofOp()
					ce274Statuses(t, r, f.Other)
					ce274WantRecords(t, r, records)
					records = records[1:]
					assumptionOp()
					ce274Statuses(t, r, f.Other)
					ce274WantRecords(t, r, records)
					if clearAssumptionsFirst {
						records = nil
						assumptionOp()
						ce274Statuses(t, r, f.Other)
						ce274WantRecords(t, r, nil)
						proof = nil
						proofOp()
					} else {
						proof = nil
						proofOp()
						ce274Statuses(t, r)
						ce274WantRecords(t, r, records)
						records = nil
						assumptionOp()
					}
					ce274Statuses(t, r)
					ce274WantRecords(t, r, nil)
					if ce274Legacy(t, r) != legacy {
						t.Fatal("replacement/clear changed gate, legacy or ratified data")
					}
					for _, row := range r.Components.Proofs {
						if row.Result != nil {
							t.Fatal("clear retained an old proof result")
						}
					}
				})
			}
		}
	}
}

func TestComponentProofActualProvenanceIsOptionalAndRetained(t *testing.T) {
	f := ce274Fixture(t)
	for _, mode := range []string{"old", "invocation", "files", "all"} {
		t.Run(mode, func(t *testing.T) {
			p := ce274ProofFor(t, f, f.Target, mode != "old")
			if mode == "invocation" {
				p.FileInputs, p.Tool = nil, nil
			}
			if mode == "files" {
				p.Invocation, p.Tool = nil, nil
			}
			r := ce274Seed()
			if err := ce274Proofs(t, &r, f.Inv, []ce274Proof{p}); err != nil {
				t.Fatal(err)
			}
			ce274Statuses(t, r, f.Target)
			var got ce274Proof
			for _, row := range r.Components.Proofs {
				if row.Target == f.Target {
					if err := json.Unmarshal(cpJSON(t, row.Result), &got); err != nil {
						t.Fatal(err)
					}
				}
			}
			if !reflect.DeepEqual(got.Invocation, p.Invocation) || !reflect.DeepEqual(got.FileInputs, p.FileInputs) || !reflect.DeepEqual(got.Tool, p.Tool) {
				t.Fatalf("supplied optional proof provenance was lost or synthesized: %s", cpJSON(t, got))
			}
			if !reflect.DeepEqual(got.Inputs, p.Inputs) {
				t.Fatal("file provenance replaced compatible declaration Inputs")
			}
			if mode == "old" {
				for _, name := range []string{"invocation", "file_inputs", "tool"} {
					var obj map[string]any
					_ = json.Unmarshal(cpJSON(t, got), &obj)
					if _, ok := obj[name]; ok {
						t.Fatalf("old proof result acquired %s", name)
					}
				}
			}
		})
	}
}
