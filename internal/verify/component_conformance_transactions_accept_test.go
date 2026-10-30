package verify_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

func cct279KindJSON(t *testing.T, r verify.Report, kind string) string {
	t.Helper()
	var wire struct {
		Components map[string]json.RawMessage `json:"components"`
	}
	if err := json.Unmarshal(cpJSON(t, r), &wire); err != nil {
		t.Fatal(err)
	}
	return string(wire.Components[kind])
}

func cct279RequireAPI(t *testing.T, f ce274FixtureData) {
	t.Helper()
	r := cc279Attach(t, f, cc279Good(t, f, f.Target, "", ""))
	rows := cc279Rows(t, r)
	if len(rows) != 1 || rows[0].Status != "tested" {
		t.Fatal("conformance attribution must first retain a successful supplied result")
	}
}

func TestComponentConformanceIndependentReplacementAndClear(t *testing.T) {
	f := cc279Fixture(t)
	cct279RequireAPI(t, f)
	orders := [][]string{
		{"proofs", "assumptions", "conformance"}, {"proofs", "conformance", "assumptions"},
		{"assumptions", "proofs", "conformance"}, {"assumptions", "conformance", "proofs"},
		{"conformance", "proofs", "assumptions"}, {"conformance", "assumptions", "proofs"},
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
				results := []cc279Result{cc279Good(t, f, f.Same, "", ""), cc279Good(t, f, f.Target, "", "")}
				present := map[string]bool{}
				proofTarget := f.Target
				var activeRecords []ce274AssumptionRecord
				var activeResults []cc279Result
				apply := func(kind string, clear bool) {
					t.Helper()
					before := map[string]string{}
					if r.Components != nil {
						for _, other := range []string{"proofs", "assumptions", "conformance"} {
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
						activeRecords = x
					case "conformance":
						x := results
						if clear {
							x = nil
						}
						err = cc279Call(t, &r, inv, x)
						activeResults = x
					}
					if err != nil {
						t.Fatal(err)
					}
					present[kind] = !clear
					for other, bytes := range before {
						if cct279KindJSON(t, r, other) != bytes {
							t.Fatalf("%s replacement/clear changed retained %s", kind, other)
						}
					}
					if ce274Legacy(t, r) != legacy || r.Passed != passed {
						t.Fatal("attribution changed legacy checks, gate outcome, pins or ratified data")
					}
					if r.Fingerprint != verify.Fingerprint(r) || r.Assurance != "component evidence" || r.CodeAssurance() != "component evidence" || r.Claim() != "component evidence" || r.EverySize() {
						t.Fatal("independent attribution lost its finalized fingerprint or component-only assurance")
					}
					if present["proofs"] {
						ce274Statuses(t, r, proofTarget)
					} else {
						ce274Statuses(t, r)
					}
					if present["assumptions"] {
						ce274WantRecords(t, r, activeRecords)
					} else if len(ce274AssumptionRows(t, r)) != 0 {
						t.Fatal("clear kept assumptions")
					}
					if present["conformance"] {
						rows := cc279Rows(t, r)
						if len(rows) != len(activeResults) {
							t.Fatal("conformance replacement retained old results or lost new results")
						}
						for _, result := range activeResults {
							pos := slices.IndexFunc(rows, func(row cc279Evidence) bool {
								return row.Target == result.Target && row.Check == result.Check && row.Result.Invocation != nil && row.Result.Invocation.ID == result.Invocation.ID
							})
							if pos < 0 {
								t.Fatal("conformance result identity disappeared")
							}
							cc279Want(t, rows[pos], result, "tested", true, result.Mode == "bounded-exhaustive", result.Attempts != nil)
						}
					} else if len(cc279Rows(t, r)) != 0 {
						t.Fatal("clear kept conformance")
					}
				}
				checkpoint := func(phase string) {
					t.Helper()
					bytes := string(cpJSON(t, r))
					if want, ok := canonical[phase]; ok && bytes != want {
						t.Fatalf("%s result depends on attribution order", phase)
					}
					canonical[phase] = bytes
				}
				for _, kind := range order {
					apply(kind, false)
				}
				checkpoint("initial")
				proofs = []ce274Proof{ce274ProofFor(t, f, f.Other, true)}
				records = []ce274AssumptionRecord{ce274RecordFor(t, f.Inv, f.Other)}
				result := cc279Good(t, f, f.Target, "", "repeat")
				result.Mode, result.Attempts = "bounded-exhaustive", cc279AttemptGood()
				results = []cc279Result{result}
				// Reverse the order for replacements, then clear in the original
				// order. Every kind has previously been retained by both others.
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

func TestComponentConformanceRetainsLegacyProofCompatibility(t *testing.T) {
	f := cc279Fixture(t)
	cct279RequireAPI(t, f)
	for _, proofFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("proofFirst=%v", proofFirst), func(t *testing.T) {
			r := ce274Seed()
			proof := ce274ProofFor(t, f, f.Target, false)
			// Additional known declarations may have been verified or checked;
			// they never inherit the target's proof. A matching snapshot alone
			// is also legitimate and does not need to be a verified declaration.
			driver := ce274Component(t, f.Inv, provenance257ID("drivers/drive.go", "Drive"))
			proof.Verified = append(proof.Verified, f.Same, driver.ID)
			proof.Contracts = append(proof.Contracts, f.Other)
			proof.Inputs = append(proof.Inputs, ce274Component(t, f.Inv, f.Same), driver, ce274Component(t, f.Inv, f.Other), ce274Component(t, f.Inv, f.Assumed))
			conformance := cc279Good(t, f, f.Target, "", "")
			attachProof := func() {
				t.Helper()
				if err := ce274Proofs(t, &r, f.Inv, []ce274Proof{proof}); err != nil {
					t.Fatal(err)
				}
			}
			attachConformance := func() {
				t.Helper()
				if err := cc279Call(t, &r, f.Inv, []cc279Result{conformance}); err != nil {
					t.Fatal(err)
				}
			}
			if proofFirst {
				attachProof()
				attachConformance()
			} else {
				attachConformance()
				attachProof()
			}
			if err := ce274Assumptions(t, &r, f.Inv, ce274Records(t, f)); err != nil {
				t.Fatal(err)
			}
			ce274Statuses(t, r, f.Target)
			for _, row := range r.Components.Proofs {
				if row.Result != nil && (row.Result.Invocation != nil || len(row.Result.FileInputs) != 0 || row.Result.Tool != nil) {
					t.Fatal("old proof caller acquired invented execution provenance")
				}
			}
			before := cct279KindJSON(t, r, "proofs")
			inv := cpClone(t, f.Inv)
			ce274ReverseInventory(inv)
			if err := cc279Call(t, &r, inv, nil); err != nil {
				t.Fatal(err)
			}
			if cct279KindJSON(t, r, "proofs") != before || len(cc279Rows(t, r)) != 0 {
				t.Fatal("conformance clear changed compatible proof evidence")
			}
			ce274WantRecords(t, r, ce274Records(t, f))
		})
	}
}

// Unlike cc279Invocation, this returns the mutable selection in an inventory.
func cct279Selected(t *testing.T, inv *project.ComponentInventory, target string) *project.ResolvedComponentInvocation {
	t.Helper()
	check := ce274Check(t, inv, target)
	for i := range check.Invocations {
		v := &check.Invocations[i]
		if v.Kind == "conformance" && v.Check == provenance257ID("drivers/drive.go", "Drive") && v.ID == "run" {
			return v
		}
	}
	t.Fatal("fixture lacks selected conformance invocation")
	return nil
}

func TestComponentConformanceRevalidatedOnProofAndAssumptionUpdates(t *testing.T) {
	f := cc279Fixture(t)
	cct279RequireAPI(t, f)
	driver := provenance257ID("drivers/drive.go", "Drive")
	changes := []struct {
		name string
		edit func(*project.ComponentInventory)
	}{
		{"target contract", func(i *project.ComponentInventory) { ce274ChangeContract(i, f.Target, ce274ChangedHash()) }},
		{"selected contract", func(i *project.ComponentInventory) { ce274ChangeContract(i, f.Helper, ce274ChangedHash()) }},
		{"contract selection removed", func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Proof.Contracts = []string{f.Target} }},
		{"contract selection added", func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Proof.Contracts = append(c.Proof.Contracts, f.Other)
		}},
		{"invocation identity", func(i *project.ComponentInventory) { cct279Selected(t, i, f.Target).ID = "changed" }},
		{"invocation command", func(i *project.ComponentInventory) { cct279Selected(t, i, f.Target).Command += " changed" }},
		{"invocation ordered arguments", func(i *project.ComponentInventory) { slices.Reverse(cct279Selected(t, i, f.Target).Args) }},
		{"invocation working directory", func(i *project.ComponentInventory) { cct279Selected(t, i, f.Target).WorkingDir = "core" }},
		{"invocation environment", func(i *project.ComponentInventory) { cct279Selected(t, i, f.Target).Env["MODE"] = "changed" }},
		{"invocation sampling", func(i *project.ComponentInventory) { cct279Selected(t, i, f.Target).Sampling["seed"] = "991" }},
		{"invocation input selection", func(i *project.ComponentInventory) { v := cct279Selected(t, i, f.Target); v.Inputs = v.Inputs[1:] }},
		{"invocation removed", func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Invocations = slices.DeleteFunc(c.Invocations, func(v project.ResolvedComponentInvocation) bool {
				return v.Kind == "conformance" && v.Check == driver && v.ID == "run"
			})
		}},
		{"driver unselected", func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Conformance = slices.DeleteFunc(c.Conformance, func(id string) bool { return id == driver })
			c.Invocations = slices.DeleteFunc(c.Invocations, func(v project.ResolvedComponentInvocation) bool { return v.Kind == "conformance" && v.Check == driver })
		}},
		{"target unselected", func(i *project.ComponentInventory) {
			i.Checks = slices.DeleteFunc(i.Checks, func(c project.ResolvedComponentCheck) bool { return c.Target == f.Target })
		}},
	}
	for _, path := range []string{"drivers/drive.go", "core/core.go", "core/contracts.go", "data/fixture.bin", "data/second.bin", "config/check.json", "go.mod", "go.sum", "drivers/go.mod", ".invariant/specs/Counter.tla", ".invariant/specs/Include.tla", ".invariant/ratified.lock"} {
		changes = append(changes, struct {
			name string
			edit func(*project.ComponentInventory)
		}{"input " + path, func(i *project.ComponentInventory) { ce274ChangeFile(i, path, ce274ChangedHash()) }})
	}
	// No inventory edit is needed to change the report's applicable bounds.
	changes = append(changes, struct {
		name string
		edit func(*project.ComponentInventory)
	}{"report bounds", func(*project.ComponentInventory) {}})
	for _, incomplete := range []bool{false, true} {
		for _, tc := range changes {
			for _, kind := range []string{"proof", "assumption"} {
				for _, clear := range []bool{false, true} {
					t.Run(fmt.Sprintf("incomplete=%v/%s/%s/clear=%v", incomplete, tc.name, kind, clear), func(t *testing.T) {
						result := cc279Good(t, f, f.Target, "", "")
						if incomplete {
							// Positive counts and a selected invocation do not turn
							// omitted actual outcomes/inputs into successful evidence.
							result.Inputs, result.FileInputs, result.Checked = nil, nil, nil
							result.Runner, result.Outcome = nil, ""
						}
						r := cc279Attach(t, f, result)
						if incomplete && cc279Rows(t, r)[0].Status != "unchecked" {
							t.Fatal("incomplete conformance became successful")
						}
						inv := cpClone(t, f.Inv)
						tc.edit(inv)
						if tc.name == "input drivers/drive.go" && (string(cpJSON(t, inv.Files)) != string(cpJSON(t, f.Inv.Files)) || string(cpJSON(t, inv.Components)) != string(cpJSON(t, f.Inv.Components))) {
							t.Fatal("outside-scope driver edit unexpectedly changed production inventory")
						}
						if tc.name == "report bounds" {
							r.Bounds["Limit"] = "3"
						}
						next := f
						next.Inv = inv
						var proofs []ce274Proof
						var assumptions []ce274AssumptionRecord
						if !clear {
							proofs = []ce274Proof{ce274ProofFor(t, next, f.Other, false)}
							assumptions = []ce274AssumptionRecord{ce274RecordFor(t, inv, f.Other)}
						}
						apply := func(report *verify.Report) error {
							if kind == "proof" {
								return ce274Proofs(t, report, inv, proofs)
							}
							return ce274Assumptions(t, report, inv, assumptions)
						}
						probe := ce274Seed()
						probe.Bounds = cpClone(t, r.Bounds)
						if err := apply(&probe); err != nil {
							t.Fatalf("replacement inventory and incoming records must be independently valid: %v", err)
						}
						ce274Unchanged(t, &r, func() error { return apply(&r) })
					})
				}
			}
		}
	}
}

func TestComponentConformanceUpdatesRevalidateRetainedProofsAndAssumptions(t *testing.T) {
	f := cc279Fixture(t)
	cct279RequireAPI(t, f)
	changes := []struct {
		name       string
		kind       string
		incomplete bool
		edit       func(*project.ComponentInventory)
	}{
		{"target source", "proof", false, func(i *project.ComponentInventory) { ce274ChangeFile(i, "core/core.go", ce274ChangedHash()) }},
		{"target contract", "proof", false, func(i *project.ComponentInventory) { ce274ChangeContract(i, f.Target, ce274ChangedHash()) }},
		{"helper contract", "proof", false, func(i *project.ComponentInventory) { ce274ChangeContract(i, f.Helper, ce274ChangedHash()) }},
		{"proof invocation", "proof", false, func(i *project.ComponentInventory) { ce274SelectedInvocation(t, i, f.Target).Command += " changed" }},
		{"proof actual dependency", "proof", false, func(i *project.ComponentInventory) { ce274ChangeFile(i, "go.sum", ce274ChangedHash()) }},
		{"proof extra declaration", "proof", false, func(i *project.ComponentInventory) { ce274ChangeFile(i, "neighbors/extra.go", ce274ChangedHash()) }},
		{"proof selection removed", "proof", false, func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Proof = nil
			c.Invocations = slices.DeleteFunc(c.Invocations, func(v project.ResolvedComponentInvocation) bool { return v.Kind == "proof" })
		}},
		{"incomplete proof target", "proof", true, func(i *project.ComponentInventory) { ce274ChangeFile(i, "core/core.go", ce274ChangedHash()) }},
		{"incomplete proof selected contract", "proof", true, func(i *project.ComponentInventory) { ce274ChangeContract(i, f.Helper, ce274ChangedHash()) }},
		{"incomplete proof selection", "proof", true, func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Proof.Contracts = []string{f.Target} }},
		{"assumption target", "assumption", false, func(i *project.ComponentInventory) { ce274ChangeFile(i, "core/core.go", ce274ChangedHash()) }},
		{"assumption target contract", "assumption", false, func(i *project.ComponentInventory) { ce274ChangeContract(i, f.Target, ce274ChangedHash()) }},
		{"assumption source", "assumption", false, func(i *project.ComponentInventory) { ce274ChangeFile(i, "assumptions.md", ce274ChangedHash()) }},
		{"assumption statement", "assumption", false, func(i *project.ComponentInventory) {
			ce274Check(t, i, f.Target).Assumptions[0].Statement += " Changed."
		}},
		{"assumption reason", "assumption", false, func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Assumptions[0].Reason += " Changed." }},
		{"assumption identity", "assumption", false, func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Assumptions[0].ID = "changed" }},
		{"assumption unselected", "assumption", false, func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Assumptions = nil }},
	}
	for _, tc := range changes {
		for _, clear := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/clear=%v", tc.name, clear), func(t *testing.T) {
				r := cc279Attach(t, f, cc279Good(t, f, f.Same, "", ""))
				if tc.kind == "proof" {
					proof := ce274ProofFor(t, f, f.Target, !tc.incomplete)
					proof.Inputs = append(proof.Inputs, ce274Component(t, f.Inv, f.Same))
					if tc.incomplete {
						proof.Inputs, proof.Verified, proof.Contracts = nil, nil, nil
					}
					if err := ce274Proofs(t, &r, f.Inv, []ce274Proof{proof}); err != nil {
						t.Fatal(err)
					}
				} else if err := ce274Assumptions(t, &r, f.Inv, []ce274AssumptionRecord{ce274RecordFor(t, f.Inv, f.Target)}); err != nil {
					t.Fatal(err)
				}
				inv := cpClone(t, f.Inv)
				tc.edit(inv)
				next := f
				next.Inv = inv
				var results []cc279Result
				if !clear {
					results = []cc279Result{cc279Good(t, next, f.Same, "", "")}
				}
				// The old conformance is replaced or cleared, and the supplied
				// replacement uses current snapshots. Only retained evidence is stale.
				probe := ce274Seed()
				if err := cc279Call(t, &probe, inv, results); err != nil {
					t.Fatalf("new inventory and conformance must be independently valid: %v", err)
				}
				ce274Unchanged(t, &r, func() error { return cc279Call(t, &r, inv, results) })
			})
		}
	}
}
