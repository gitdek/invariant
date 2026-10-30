package verify_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

func crt287Orders() [][]string {
	var result [][]string
	var visit func([]string, []string)
	visit = func(prefix, rest []string) {
		if len(rest) == 0 {
			result = append(result, slices.Clone(prefix))
			return
		}
		for i, kind := range rest {
			visit(append(slices.Clone(prefix), kind), slices.Delete(slices.Clone(rest), i, i+1))
		}
	}
	visit(nil, []string{"proofs", "assumptions", "conformance", "runtime"})
	return result
}

func TestComponentRuntimeFourKindReplacementAndClear(t *testing.T) {
	f := cr287Fixture(t)
	cr287RequireAPI(t, f)
	orders := crt287Orders()
	if len(orders) != 24 {
		t.Fatal("fixture must exercise every four-kind attribution order")
	}
	for _, passed := range []bool{false, true} {
		canonical := map[string]string{}
		for n, order := range orders {
			t.Run(fmt.Sprintf("gate=%v/order=%d", passed, n), func(t *testing.T) {
				r := ce274Seed()
				r.Passed = passed
				legacy := ce274Legacy(t, r)
				inv := cpClone(t, f.Inv)
				ce274ReverseInventory(inv)
				proofs := []ce274Proof{ce274ProofFor(t, f, f.Target, true)}
				records := ce274Records(t, f)
				conformance := []cc279Result{cc279Good(t, f, f.Target, "", ""), cc279Good(t, f, f.Same, "", "")}
				runtime := []cr287Result{cr287Good(t, f, "", "repeat"), cr287Good(t, f, "", "")}
				present := map[string]bool{}
				proofTarget := f.Target
				apply := func(kind string, clear bool) {
					t.Helper()
					before := map[string]string{}
					if r.Components != nil {
						for _, other := range order {
							if other != kind {
								before[other] = cct279KindJSON(t, r, other)
							}
						}
					}
					var err error
					switch kind {
					case "proofs":
						x := proofs
						if clear {
							x = nil
						}
						err = ce274Proofs(t, &r, inv, x)
						if !clear {
							proofTarget = proofs[0].Target
						}
					case "assumptions":
						x := records
						if clear {
							x = nil
						}
						err = ce274Assumptions(t, &r, inv, x)
					case "conformance":
						x := conformance
						if clear {
							x = nil
						}
						err = cc279Call(t, &r, inv, x)
					case "runtime":
						x := runtime
						if clear {
							x = nil
						}
						err = cr287Call(t, &r, inv, x)
					}
					if err != nil {
						t.Fatal(err)
					}
					present[kind] = !clear
					for other, bytes := range before {
						if cct279KindJSON(t, r, other) != bytes {
							t.Fatalf("%s update/clear changed retained %s", kind, other)
						}
					}
					if ce274Legacy(t, r) != legacy || r.Passed != passed {
						t.Fatal("attribution changed legacy checks, gate outcome, pins or ratified data")
					}
					if r.Fingerprint != verify.Fingerprint(r) || r.Assurance != "component evidence" || r.CodeAssurance() != "component evidence" || r.Claim() != "component evidence" || r.EverySize() {
						t.Fatal("attribution lost its finalized fingerprint or component-only assurance")
					}
					if present["proofs"] {
						ce274Statuses(t, r, proofTarget)
					} else {
						ce274Statuses(t, r)
					}
					if present["assumptions"] {
						// The supplied collection changes only at this kind's update.
						if kind == "assumptions" {
							ce274WantRecords(t, r, records)
						}
					} else if len(ce274AssumptionRows(t, r)) != 0 {
						t.Fatal("clear kept assumptions")
					}
					if !present["conformance"] && len(cc279Rows(t, r)) != 0 {
						t.Fatal("clear kept conformance")
					}
					if !present["runtime"] && len(cr287Rows(t, r)) != 0 {
						t.Fatal("clear kept runtime evidence")
					}
					if kind == "conformance" && !clear {
						rows := cc279Rows(t, r)
						if len(rows) != len(conformance) {
							t.Fatal("conformance update lost or invented records")
						}
						for _, result := range conformance {
							pos := slices.IndexFunc(rows, func(row cc279Evidence) bool {
								return row.Target == result.Target && row.Check == result.Check && row.Result.Invocation.ID == result.Invocation.ID
							})
							if pos < 0 {
								t.Fatal("conformance result identity disappeared")
							}
							cc279Want(t, rows[pos], result, "tested", true, result.Mode == "bounded-exhaustive", result.Attempts != nil)
						}
					}
					if kind == "runtime" && !clear {
						rows := cr287Rows(t, r)
						if len(rows) != len(runtime) {
							t.Fatal("runtime update lost or invented records")
						}
						for _, result := range runtime {
							pos := slices.IndexFunc(rows, func(row cr287Evidence) bool {
								return row.Target == result.Target && row.Check == result.Check && row.Result.Invocation.ID == result.Invocation.ID
							})
							if pos < 0 {
								t.Fatal("runtime result identity disappeared")
							}
							cr287WantResult(t, rows[pos], result)
							if rows[pos].Status != "tested" || len(rows[pos].Tests) != 1 || rows[pos].Tests[0].Status != "tested" || len(rows[pos].Tests[0].Assertions) != 1 || rows[pos].Tests[0].Assertions[0].Status != "passed" {
								t.Fatal("supplied passing test/assertion qualifications disappeared")
							}
						}
					}
				}
				checkpoint := func(phase string) {
					t.Helper()
					bytes := string(cpJSON(t, r))
					if want, ok := canonical[phase]; ok && bytes != want {
						t.Fatalf("%s depends on attribution order", phase)
					}
					canonical[phase] = bytes
				}
				for _, kind := range order {
					apply(kind, false)
				}
				checkpoint("initial")
				proofs = []ce274Proof{ce274ProofFor(t, f, f.Other, true)}
				records = []ce274AssumptionRecord{ce274RecordFor(t, f.Inv, f.Other)}
				c := cc279Good(t, f, f.Target, "", "repeat")
				c.Mode, c.Attempts = "bounded-exhaustive", cc279AttemptGood()
				conformance = []cc279Result{c}
				runtime = []cr287Result{cr287Good(t, f, provenance257ID("tests/step_test.go", "TestAgain"), "run")}
				for i := len(order) - 1; i >= 0; i-- {
					apply(order[i], false)
				}
				checkpoint("replacement")
				for _, kind := range order {
					apply(kind, true)
				}
				checkpoint("cleared")
			})
		}
	}
}

func TestComponentRuntimeRetainedByEveryOtherUpdateAndClear(t *testing.T) {
	f := cr287Fixture(t)
	cr287RequireAPI(t, f)
	selected := provenance257ID("tests/step_test.go", "TestStep")
	changes := []struct {
		name      string
		extraOnly bool
		edit      func(*project.ComponentInventory)
	}{
		{"target contract", false, func(i *project.ComponentInventory) { ce274ChangeContract(i, f.Target, ce274ChangedHash()) }},
		{"helper contract", false, func(i *project.ComponentInventory) { ce274ChangeContract(i, f.Helper, ce274ChangedHash()) }},
		{"selected test contract", false, func(i *project.ComponentInventory) { ce274ChangeContract(i, selected, ce274ChangedHash()) }},
		{"contract selection removed", false, func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Proof.Contracts = []string{f.Target} }},
		{"contract selection added", false, func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Proof.Contracts = append(c.Proof.Contracts, f.Other)
		}},
		{"invocation identity", false, func(i *project.ComponentInventory) { cr287Selected(t, i, f.Target, "", "").ID = "changed" }},
		{"invocation command", false, func(i *project.ComponentInventory) { cr287Selected(t, i, f.Target, "", "").Command += " changed" }},
		{"invocation argument order", false, func(i *project.ComponentInventory) { slices.Reverse(cr287Selected(t, i, f.Target, "", "").Args) }},
		{"invocation working directory", false, func(i *project.ComponentInventory) { cr287Selected(t, i, f.Target, "", "").WorkingDir = "core" }},
		{"invocation environment", false, func(i *project.ComponentInventory) { cr287Selected(t, i, f.Target, "", "").Env["MODE"] = "changed" }},
		{"invocation sampling", false, func(i *project.ComponentInventory) { cr287Selected(t, i, f.Target, "", "").Sampling["seed"] = "991" }},
		{"invocation input selection", false, func(i *project.ComponentInventory) {
			v := cr287Selected(t, i, f.Target, "", "")
			v.Inputs = v.Inputs[1:]
		}},
		{"invocation removed", false, func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Invocations = slices.DeleteFunc(c.Invocations, func(v project.ResolvedComponentInvocation) bool {
				return v.Kind == "test" && v.Check == selected && v.ID == "run"
			})
		}},
		{"test unselected", false, func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Tests = slices.DeleteFunc(c.Tests, func(id string) bool { return id == selected })
			c.Invocations = slices.DeleteFunc(c.Invocations, func(v project.ResolvedComponentInvocation) bool { return v.Kind == "test" && v.Check == selected })
			// Preserve the selected provenance in a different valid target.
			ce274Check(t, i, f.Other).Tests = []string{selected}
		}},
		{"target unselected", false, func(i *project.ComponentInventory) { ce274DropCheck(t, i, f.Target, f.Other) }},
		{"report bounds", false, func(*project.ComponentInventory) {}},
	}
	for _, path := range []string{"core/core.go", "core/contracts.go", "tests/step_test.go", "data/fixture.bin", "config/check.json", "go.mod", "go.sum", ".invariant/specs/Counter.tla", ".invariant/specs/Include.tla", ".invariant/ratified.lock", "data/second.bin", "drivers/go.mod", "drivers/drive.go"} {
		extra := slices.Contains([]string{"data/second.bin", "drivers/go.mod", "drivers/drive.go"}, path)
		changes = append(changes, struct {
			name      string
			extraOnly bool
			edit      func(*project.ComponentInventory)
		}{"input " + path, extra, func(i *project.ComponentInventory) { ce274ChangeFile(i, path, ce274ChangedHash()) }})
	}
	for _, incomplete := range []bool{false, true} {
		for _, tc := range changes {
			if incomplete && tc.extraOnly {
				continue
			}
			for _, kind := range []string{"proof", "assumption", "conformance"} {
				for _, clear := range []bool{false, true} {
					t.Run(fmt.Sprintf("incomplete=%v/%s/%s/clear=%v", incomplete, tc.name, kind, clear), func(t *testing.T) {
						result := cr287Good(t, f, "", "")
						cr287Context(t, f, &result)
						if incomplete {
							result.Inputs, result.FileInputs, result.Tests = nil, nil, nil
							result.Runner, result.Outcome = nil, ""
						}
						r := cr287Attach(t, f, result)
						if incomplete && cr287Rows(t, r)[0].Status != "unchecked" {
							t.Fatal("incomplete runtime became successful")
						}
						inv := cpClone(t, f.Inv)
						tc.edit(inv)
						if tc.name == "report bounds" {
							r.Bounds["Limit"] = "3"
						}
						next := f
						next.Inv = inv
						var proofs []ce274Proof
						var records []ce274AssumptionRecord
						var conformance []cc279Result
						if !clear {
							proofs = []ce274Proof{ce274ProofFor(t, next, f.Other, false)}
							records = []ce274AssumptionRecord{ce274RecordFor(t, inv, f.Other)}
							c := cc279Good(t, next, f.Same, "", "")
							c.Bounds.Values = cpClone(t, r.Bounds)
							conformance = []cc279Result{c}
						}
						apply := func(report *verify.Report) error {
							switch kind {
							case "proof":
								return ce274Proofs(t, report, inv, proofs)
							case "assumption":
								return ce274Assumptions(t, report, inv, records)
							default:
								return cc279Call(t, report, inv, conformance)
							}
						}
						probe := ce274Seed()
						probe.Bounds = cpClone(t, r.Bounds)
						if err := apply(&probe); err != nil {
							t.Fatalf("replacement inventory and incoming evidence must be independently valid: %v", err)
						}
						ce274Unchanged(t, &r, func() error { return apply(&r) })
					})
				}
			}
		}
	}
}

func TestComponentRuntimeUpdatesRevalidateEveryRetainedKind(t *testing.T) {
	f := cr287Fixture(t)
	cr287RequireAPI(t, f)
	changes := []struct {
		name, kind string
		incomplete bool
		edit       func(*project.ComponentInventory)
	}{
		{"proof target source", "proof", false, func(i *project.ComponentInventory) { ce274ChangeFile(i, "core/core.go", ce274ChangedHash()) }},
		{"proof target contract", "proof", false, func(i *project.ComponentInventory) { ce274ChangeContract(i, f.Target, ce274ChangedHash()) }},
		{"proof helper source", "proof", false, func(i *project.ComponentInventory) { ce274ChangeFile(i, "core/contracts.go", ce274ChangedHash()) }},
		{"proof helper contract", "proof", false, func(i *project.ComponentInventory) { ce274ChangeContract(i, f.Helper, ce274ChangedHash()) }},
		{"proof extra declaration", "proof", false, func(i *project.ComponentInventory) { ce274ChangeFile(i, "neighbors/extra.go", ce274ChangedHash()) }},
		{"proof invocation", "proof", false, func(i *project.ComponentInventory) { ce274SelectedInvocation(t, i, f.Target).Command += " changed" }},
		{"proof contract selection", "proof", false, func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Proof.Contracts = []string{f.Target} }},
		{"incomplete proof target", "proof", true, func(i *project.ComponentInventory) { ce274ChangeFile(i, "core/core.go", ce274ChangedHash()) }},
		{"incomplete proof contract", "proof", true, func(i *project.ComponentInventory) { ce274ChangeContract(i, f.Helper, ce274ChangedHash()) }},
		{"incomplete proof selection", "proof", true, func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Proof.Contracts = []string{f.Target} }},
		{"conformance target contract", "conformance", false, func(i *project.ComponentInventory) { ce274ChangeContract(i, f.Target, ce274ChangedHash()) }},
		{"conformance helper contract", "conformance", false, func(i *project.ComponentInventory) { ce274ChangeContract(i, f.Helper, ce274ChangedHash()) }},
		{"conformance invocation", "conformance", false, func(i *project.ComponentInventory) { cct279Selected(t, i, f.Target).Command += " changed" }},
		{"conformance contract selection", "conformance", false, func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Proof.Contracts = []string{f.Target} }},
		{"conformance report bounds", "conformance", false, func(*project.ComponentInventory) {}},
		{"incomplete conformance driver", "conformance", true, func(i *project.ComponentInventory) { ce274ChangeFile(i, "drivers/drive.go", ce274ChangedHash()) }},
		{"incomplete conformance target", "conformance", true, func(i *project.ComponentInventory) { ce274ChangeFile(i, "core/core.go", ce274ChangedHash()) }},
		{"incomplete conformance contract", "conformance", true, func(i *project.ComponentInventory) { ce274ChangeContract(i, f.Helper, ce274ChangedHash()) }},
		{"incomplete conformance invocation", "conformance", true, func(i *project.ComponentInventory) { cct279Selected(t, i, f.Target).Args = []string{"changed"} }},
		{"assumption target", "assumption", false, func(i *project.ComponentInventory) { ce274ChangeFile(i, "core/core.go", ce274ChangedHash()) }},
		{"assumption target contract", "assumption", false, func(i *project.ComponentInventory) { ce274ChangeContract(i, f.Target, ce274ChangedHash()) }},
		{"assumption source bytes", "assumption", false, func(i *project.ComponentInventory) { ce274ChangeFile(i, "assumptions.md", ce274ChangedHash()) }},
		{"assumption source path", "assumption", false, func(i *project.ComponentInventory) {
			ce274Check(t, i, f.Target).Assumptions[0].Source = ce274File(t, i, "policy.txt")
		}},
		{"assumption text", "assumption", false, func(i *project.ComponentInventory) {
			ce274Check(t, i, f.Target).Assumptions[0].Statement += " Changed."
		}},
		{"assumption reason", "assumption", false, func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Assumptions[0].Reason += " Changed." }},
		{"assumption identity", "assumption", false, func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Assumptions[0].ID = "changed" }},
		{"assumption removed", "assumption", false, func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Assumptions = nil }},
	}
	for _, kind := range []string{"proof", "conformance"} {
		for _, path := range []string{"core/core.go", "core/contracts.go", "tests/step_test.go", "drivers/drive.go", "data/fixture.bin", "data/second.bin", "config/check.json", "go.mod", "go.sum", "drivers/go.mod", ".invariant/specs/Counter.tla", ".invariant/specs/Include.tla", ".invariant/ratified.lock"} {
			changes = append(changes, struct {
				name, kind string
				incomplete bool
				edit       func(*project.ComponentInventory)
			}{kind + " input " + path, kind, false, func(i *project.ComponentInventory) { ce274ChangeFile(i, path, ce274ChangedHash()) }})
		}
	}
	for _, tc := range changes {
		for _, clear := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/clear=%v", tc.name, clear), func(t *testing.T) {
				r := cr287Attach(t, f, cr287Good(t, f, "", ""))
				switch tc.kind {
				case "proof":
					proof := ce274ProofFor(t, f, f.Target, !tc.incomplete)
					proof.Inputs = append(proof.Inputs, ce274Component(t, f.Inv, f.Same))
					if tc.incomplete {
						proof.Inputs, proof.Verified, proof.Contracts = nil, nil, nil
					}
					if err := ce274Proofs(t, &r, f.Inv, []ce274Proof{proof}); err != nil {
						t.Fatal(err)
					}
				case "conformance":
					c := cc279Good(t, f, f.Target, "", "")
					if tc.incomplete {
						c.Inputs, c.FileInputs, c.Checked, c.Runner, c.Outcome = nil, nil, nil, nil, ""
					}
					if err := cc279Call(t, &r, f.Inv, []cc279Result{c}); err != nil {
						t.Fatal(err)
					}
				case "assumption":
					if err := ce274Assumptions(t, &r, f.Inv, []ce274AssumptionRecord{ce274RecordFor(t, f.Inv, f.Target)}); err != nil {
						t.Fatal(err)
					}
				}
				inv := cpClone(t, f.Inv)
				tc.edit(inv)
				if tc.name == "conformance report bounds" {
					r.Bounds["Limit"] = "3"
				}
				if tc.name == "conformance input drivers/drive.go" && (string(cpJSON(t, inv.Files)) != string(cpJSON(t, f.Inv.Files)) || string(cpJSON(t, inv.Components)) != string(cpJSON(t, f.Inv.Components))) {
					t.Fatal("outside-scope driver edit changed production inventory")
				}
				next := f
				next.Inv = inv
				var results []cr287Result
				if !clear {
					results = []cr287Result{cr287Good(t, next, "", "repeat")}
				}
				probe := ce274Seed()
				probe.Bounds = cpClone(t, r.Bounds)
				if err := cr287Call(t, &probe, inv, results); err != nil {
					t.Fatalf("replacement inventory and incoming runtime must be independently valid: %v", err)
				}
				ce274Unchanged(t, &r, func() error { return cr287Call(t, &r, inv, results) })
			})
		}
	}
}

func TestComponentRuntimeAndProofUpdatesRejectOutsideScopeConformanceEdit(t *testing.T) {
	f := cr287Fixture(t)
	cr287RequireAPI(t, f)
	for _, incomplete := range []bool{false, true} {
		for _, kind := range []string{"proof", "runtime"} {
			for _, clear := range []bool{false, true} {
				t.Run(fmt.Sprintf("incomplete=%v/%s/clear=%v", incomplete, kind, clear), func(t *testing.T) {
					c := cc279Good(t, f, f.Target, "", "")
					if incomplete {
						c.Inputs, c.FileInputs, c.Checked, c.Runner, c.Outcome = nil, nil, nil, nil, ""
					}
					r := cc279Attach(t, f, c)
					inv := cpClone(t, f.Inv)
					ce274ChangeFile(inv, "drivers/drive.go", ce274ChangedHash())
					if string(cpJSON(t, inv.Files)) != string(cpJSON(t, f.Inv.Files)) || string(cpJSON(t, inv.Components)) != string(cpJSON(t, f.Inv.Components)) {
						t.Fatal("driver-only edit changed production inventory")
					}
					next := f
					next.Inv = inv
					var proofs []ce274Proof
					var runtime []cr287Result
					if !clear {
						proofs = []ce274Proof{ce274ProofFor(t, next, f.Other, false)}
						runtime = []cr287Result{cr287Good(t, next, "", "")}
					}
					apply := func(report *verify.Report) error {
						if kind == "proof" {
							return ce274Proofs(t, report, inv, proofs)
						}
						return cr287Call(t, report, inv, runtime)
					}
					probe := ce274Seed()
					if err := apply(&probe); err != nil {
						t.Fatalf("driver-only replacement and incoming evidence must be valid: %v", err)
					}
					ce274Unchanged(t, &r, func() error { return apply(&r) })
				})
			}
		}
	}
}

func TestComponentRuntimeKeepsLegacyProofExtraDeclarations(t *testing.T) {
	f := cr287Fixture(t)
	cr287RequireAPI(t, f)
	for _, proofFirst := range []bool{false, true} {
		for _, incomplete := range []bool{false, true} {
			t.Run(fmt.Sprintf("proofFirst=%v/incomplete=%v", proofFirst, incomplete), func(t *testing.T) {
				r := ce274Seed()
				proof := ce274ProofFor(t, f, f.Target, false)
				driver := ce274Component(t, f.Inv, provenance257ID("drivers/drive.go", "Drive"))
				proof.Verified = append(proof.Verified, f.Same, driver.ID)
				proof.Contracts = append(proof.Contracts, f.Other)
				proof.Inputs = append(proof.Inputs, ce274Component(t, f.Inv, f.Same), driver, ce274Component(t, f.Inv, f.Other), ce274Component(t, f.Inv, f.Assumed))
				if incomplete {
					proof.Contracts = []string{f.Target}
				}
				attachProof := func() {
					t.Helper()
					if err := ce274Proofs(t, &r, f.Inv, []ce274Proof{proof}); err != nil {
						t.Fatal(err)
					}
				}
				attachRuntime := func() {
					t.Helper()
					if err := cr287Call(t, &r, f.Inv, []cr287Result{cr287Good(t, f, "", "")}); err != nil {
						t.Fatal(err)
					}
				}
				if proofFirst {
					attachProof()
					attachRuntime()
				} else {
					attachRuntime()
					attachProof()
				}
				if err := ce274Assumptions(t, &r, f.Inv, ce274Records(t, f)); err != nil {
					t.Fatal(err)
				}
				if err := cc279Call(t, &r, f.Inv, []cc279Result{cc279Good(t, f, f.Target, "", "")}); err != nil {
					t.Fatal(err)
				}
				if incomplete {
					ce274Statuses(t, r)
				} else {
					ce274Statuses(t, r, f.Target)
				}
				for _, row := range r.Components.Proofs {
					if row.Result != nil && (row.Result.Invocation != nil || len(row.Result.FileInputs) != 0 || row.Result.Tool != nil) {
						t.Fatal("old proof caller acquired invented execution provenance")
					}
				}
				before := cct279KindJSON(t, r, "proofs")
				inv := cpClone(t, f.Inv)
				ce274ReverseInventory(inv)
				if err := cr287Call(t, &r, inv, nil); err != nil {
					t.Fatal(err)
				}
				if cct279KindJSON(t, r, "proofs") != before || len(cr287Rows(t, r)) != 0 {
					t.Fatal("runtime clear changed compatible proof evidence")
				}
				ce274WantRecords(t, r, ce274Records(t, f))
				if len(cc279Rows(t, r)) != 1 {
					t.Fatal("runtime clear lost conformance")
				}
			})
		}
	}
}
