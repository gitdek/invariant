package verify_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

func TestComponentEvidenceRejectsInvalidRecordsAtomically(t *testing.T) {
	f := ce274Fixture(t)
	baseline := ce274Attach(t, f, true)
	// Every malformed proof follows a valid, independent target result. The
	// entire batch must fail, not install its valid prefix or remove assumptions.
	proofCases := []struct {
		name string
		edit func(*ce274Proof)
	}{
		{"unknown target", func(p *ce274Proof) { p.Target = "go:missing#Other@missing.go" }},
		{"bare target", func(p *ce274Proof) { p.Target = "Other" }},
		{"unselected target", func(p *ce274Proof) { p.Target = f.Assumed }},
		{"selected test is not production target", func(p *ce274Proof) { p.Target = ce274ExternalDeclaration(t, f.Inv).ID }},
		{"unknown verified declaration", func(p *ce274Proof) { p.Verified = append(p.Verified, "missing") }},
		{"bare verified declaration", func(p *ce274Proof) { p.Verified = []string{"Other"} }},
		{"duplicate verified declaration", func(p *ce274Proof) { p.Verified = append(p.Verified, p.Verified[0]) }},
		{"unknown checked contract", func(p *ce274Proof) { p.Contracts = append(p.Contracts, "missing") }},
		{"bare checked contract", func(p *ce274Proof) { p.Contracts = []string{"Other"} }},
		{"duplicate checked contract", func(p *ce274Proof) { p.Contracts = append(p.Contracts, p.Contracts[0]) }},
		{"duplicate declaration input", func(p *ce274Proof) { p.Inputs = append(p.Inputs, p.Inputs[0]) }},
		{"unknown declaration input", func(p *ce274Proof) { p.Inputs[0].ID = "missing" }},
		{"wrong declaration identity", func(p *ce274Proof) { p.Inputs[0].Symbol = "Step" }},
		{"stale declaration source", func(p *ce274Proof) { p.Inputs[0].SourceSHA256 = ce274ChangedHash() }},
		{"stale declaration contract", func(p *ce274Proof) { p.Inputs[0].ContractSHA256 = ce274ChangedHash() }},
		{"unknown invocation", func(p *ce274Proof) { p.Invocation.ID = "never-selected" }},
		{"wrong invocation kind", func(p *ce274Proof) { p.Invocation.Kind = "test" }},
		{"wrong canonical invocation check", func(p *ce274Proof) { p.Invocation.Check = f.Target }},
		{"bare invocation check", func(p *ce274Proof) { p.Invocation.Check = "Other" }},
		{"invocation command", func(p *ce274Proof) { p.Invocation.Command += " --different" }},
		{"ordered invocation arguments", func(p *ce274Proof) { slices.Reverse(p.Invocation.Args) }},
		{"invocation working directory", func(p *ce274Proof) { p.Invocation.WorkingDir = "core" }},
		{"invocation environment value", func(p *ce274Proof) { p.Invocation.Env["MODE"] = "different" }},
		{"invocation missing environment", func(p *ce274Proof) { delete(p.Invocation.Env, "MODE") }},
		{"invocation extra environment", func(p *ce274Proof) { p.Invocation.Env["EXTRA"] = "different" }},
		{"invocation sampling", func(p *ce274Proof) { p.Invocation.Sampling["seed"] = "999" }},
		{"invocation missing sampling", func(p *ce274Proof) { p.Invocation.Sampling = nil }},
		{"invocation stale file", func(p *ce274Proof) { p.Invocation.Inputs[0].SHA256 = ce274ChangedHash() }},
		{"invocation unknown file", func(p *ce274Proof) { p.Invocation.Inputs[0].Path = "undeclared.bin" }},
		{"invocation duplicate file", func(p *ce274Proof) { p.Invocation.Inputs = append(p.Invocation.Inputs, p.Invocation.Inputs[0]) }},
		{"invocation missing declared file", func(p *ce274Proof) { p.Invocation.Inputs = p.Invocation.Inputs[1:] }},
		{"invocation extra declared file", func(p *ce274Proof) {
			p.Invocation.Inputs = append(p.Invocation.Inputs, ce274File(t, f.Inv, "data/fixture.bin"))
		}},
		{"unknown actual file", func(p *ce274Proof) { p.FileInputs[0].Path = "undeclared.bin" }},
		{"duplicate actual file", func(p *ce274Proof) { p.FileInputs = append(p.FileInputs, p.FileInputs[0]) }},
		{"actual files omit selected source", func(p *ce274Proof) {
			source := ce274Component(t, f.Inv, f.Other).Source
			p.FileInputs = slices.DeleteFunc(p.FileInputs, func(file project.ComponentFile) bool { return file.Path == source })
		}},
	}
	// Check non-Go data, dependency files, model files, selected checks and
	// production sources separately: none may escape actual-input validation.
	for _, name := range []string{"core/core.go", "core/contracts.go", "tests/step_test.go", "drivers/drive.go", "data/fixture.bin", "go.mod", "go.sum", ".invariant/specs/Counter.tla", ".invariant/specs/Include.tla", ".invariant/ratified.lock"} {
		proofCases = append(proofCases, struct {
			name string
			edit func(*ce274Proof)
		}{"stale actual file " + name, func(p *ce274Proof) {
			for i := range p.FileInputs {
				if p.FileInputs[i].Path == name {
					p.FileInputs[i].SHA256 = ce274ChangedHash()
					return
				}
			}
			t.Fatalf("fixture lacks actual input %s", name)
		}})
	}
	for _, tc := range proofCases {
		t.Run("proof/"+tc.name, func(t *testing.T) {
			r := cpClone(t, baseline)
			proofs := []ce274Proof{ce274ProofFor(t, f, f.Target, true), ce274ProofFor(t, f, f.Other, true)}
			tc.edit(&proofs[1])
			ce274Unchanged(t, &r, func() error { return ce274Proofs(t, &r, f.Inv, proofs) })
		})
	}
	t.Run("proof/late duplicate target", func(t *testing.T) {
		r := cpClone(t, baseline)
		first, second := ce274ProofFor(t, f, f.Target, true), ce274ProofFor(t, f, f.Other, true)
		ce274Unchanged(t, &r, func() error { return ce274Proofs(t, &r, f.Inv, []ce274Proof{first, second, first}) })
	})

	// A record naming its own target and a declared assumption whose contents
	// changed is stale (D-0253); an unknown or duplicate one is only invalid.
	assumptionCases := []struct {
		name  string
		stale bool
		edit  func(*ce274AssumptionRecord)
	}{
		{"unknown target", false, func(a *ce274AssumptionRecord) { a.Target = "missing" }},
		{"bare target", false, func(a *ce274AssumptionRecord) { a.Target = "Assumed" }},
		{"unconfigured production target", false, func(a *ce274AssumptionRecord) { a.Target = f.Same }},
		{"unselected assumption target", false, func(a *ce274AssumptionRecord) { a.Target = f.Helper }},
		{"selected test is not production target", false, func(a *ce274AssumptionRecord) { a.Target = ce274ExternalDeclaration(t, f.Inv).ID }},
		{"unknown assumption", false, func(a *ce274AssumptionRecord) { a.Assumption.ID = "unknown" }},
		{"empty assumption identity", false, func(a *ce274AssumptionRecord) { a.Assumption.ID = "" }},
		{"same local ID belongs to different target", false, func(a *ce274AssumptionRecord) { a.Input = ce274Component(t, f.Inv, f.Target) }},
		{"missing target snapshot", false, func(a *ce274AssumptionRecord) { a.Input = project.Component{} }},
		{"wrong target snapshot identity", true, func(a *ce274AssumptionRecord) { a.Input.Symbol = "Other" }},
		{"stale target source", true, func(a *ce274AssumptionRecord) { a.Input.SourceSHA256 = ce274ChangedHash() }},
		{"stale target contract", true, func(a *ce274AssumptionRecord) { a.Input.ContractSHA256 = ce274ChangedHash() }},
		{"changed assumption statement", true, func(a *ce274AssumptionRecord) { a.Assumption.Statement += " Changed." }},
		{"changed assumption reason", true, func(a *ce274AssumptionRecord) { a.Assumption.Reason += " Changed." }},
		{"different known assumption source", true, func(a *ce274AssumptionRecord) { a.Assumption.Source = ce274File(t, f.Inv, "policy.txt") }},
		{"unknown assumption source", true, func(a *ce274AssumptionRecord) { a.Assumption.Source.Path = "missing.md" }},
		{"stale assumption source", true, func(a *ce274AssumptionRecord) { a.Assumption.Source.SHA256 = ce274ChangedHash() }},
	}
	for _, tc := range assumptionCases {
		t.Run("assumption/"+tc.name, func(t *testing.T) {
			r := cpClone(t, baseline)
			records := ce274Records(t, f)
			tc.edit(&records[1])
			var err error
			ce274Unchanged(t, &r, func() error { err = ce274Assumptions(t, &r, f.Inv, records); return err })
			if errors.Is(err, verify.ErrStaleComponentEvidence) != tc.stale {
				t.Fatalf("stale = %t for %v, want %t", errors.Is(err, verify.ErrStaleComponentEvidence), err, tc.stale)
			}
		})
	}
	t.Run("assumption/late duplicate target and local ID", func(t *testing.T) {
		r := cpClone(t, baseline)
		records := ce274Records(t, f)
		records = append(records, records[0])
		var err error
		ce274Unchanged(t, &r, func() error { err = ce274Assumptions(t, &r, f.Inv, records); return err })
		if errors.Is(err, verify.ErrStaleComponentEvidence) {
			t.Fatalf("a duplicate record is invalid, not stale: %v", err)
		}
	})
	for _, operation := range []string{"proof", "assumption"} {
		for _, invalid := range []string{"nil inventory", "legacy inventory", "model-only report"} {
			t.Run(operation+"/"+invalid, func(t *testing.T) {
				r := cpClone(t, baseline)
				inv := cpClone(t, f.Inv)
				switch invalid {
				case "nil inventory":
					inv = nil
				case "legacy inventory":
					inv.Mode = "legacy"
				case "model-only report":
					r.ModelOnly = true
				}
				ce274Unchanged(t, &r, func() error {
					if operation == "proof" {
						return ce274Proofs(t, &r, inv, nil)
					}
					return ce274Assumptions(t, &r, inv, nil)
				})
			})
		}
	}
}

func TestComponentEvidenceRejectsInvalidReplacementInventories(t *testing.T) {
	f := ce274Fixture(t)
	baseline := ce274Attach(t, f, true)
	cases := []struct {
		name string
		edit func(*project.ComponentInventory)
	}{
		{"duplicate scope", func(i *project.ComponentInventory) { i.Scope = append(i.Scope, i.Scope[0]) }},
		{"duplicate production component", func(i *project.ComponentInventory) { i.Components = append(i.Components, i.Components[0]) }},
		{"noncanonical component ID", func(i *project.ComponentInventory) { i.Components[0].ID = i.Components[0].Symbol }},
		{"inconsistent canonical component identity", func(i *project.ComponentInventory) { i.Components[0].Symbol += "Changed" }},
		{"duplicate production file", func(i *project.ComponentInventory) { i.Files = append(i.Files, i.Files[0]) }},
		{"component source file missing", func(i *project.ComponentInventory) {
			source := ce274Component(t, i, f.Helper).Source
			i.Files = slices.DeleteFunc(i.Files, func(file project.ComponentFile) bool { return file.Path == source })
		}},
		{"component and file snapshot disagree", func(i *project.ComponentInventory) { i.Components[0].SourceSHA256 = ce274ChangedHash() }},
		{"duplicate configured target", func(i *project.ComponentInventory) { i.Checks = append(i.Checks, i.Checks[0]) }},
		{"unknown configured target", func(i *project.ComponentInventory) { i.Checks[0].Target = "missing" }},
		{"bare configured target", func(i *project.ComponentInventory) { i.Checks[0].Target = "Step" }},
		{"selected test used as production target", func(i *project.ComponentInventory) { i.Checks[0].Target = ce274ExternalDeclaration(t, i).ID }},
		{"empty selected contracts", func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Proof.Contracts = nil }},
		{"unknown selected contract", func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Proof.Contracts = []string{"missing"} }},
		{"duplicate selected contract", func(i *project.ComponentInventory) {
			p := ce274Check(t, i, f.Target).Proof
			p.Contracts = append(p.Contracts, p.Contracts[0])
		}},
		{"selected declaration lacks a contract", func(i *project.ComponentInventory) {
			ce274ChangeContract(i, f.Helper, "")
		}},
		{"unknown selected test", func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Tests = []string{"missing"} }},
		{"duplicate selected test", func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Tests = append(c.Tests, c.Tests[0])
		}},
		{"unknown selected driver", func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Conformance = []string{"missing"} }},
		{"duplicate selected driver", func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Conformance = append(c.Conformance, c.Conformance[0])
		}},
		{"duplicate provenance declaration", func(i *project.ComponentInventory) {
			i.Provenance.Declarations = append(i.Provenance.Declarations, i.Provenance.Declarations[0])
		}},
		{"selected declaration missing from provenance", func(i *project.ComponentInventory) {
			id := ce274ExternalDeclaration(t, i).ID
			i.Provenance.Declarations = slices.DeleteFunc(i.Provenance.Declarations, func(c project.Component) bool { return c.ID == id })
		}},
		{"provenance declaration source disagrees", func(i *project.ComponentInventory) { i.Provenance.Declarations[0].SourceSHA256 = ce274ChangedHash() }},
		{"production and selected driver disagree", func(i *project.ComponentInventory) {
			for n := range i.Provenance.Declarations {
				if i.Provenance.Declarations[n].Source == "core/scoped_driver.go" {
					i.Provenance.Declarations[n].ContractSHA256 = ce274ChangedHash()
					return
				}
			}
			t.Fatal("fixture lacks overlapping driver")
		}},
		{"duplicate provenance file", func(i *project.ComponentInventory) {
			i.Provenance.Files = append(i.Provenance.Files, i.Provenance.Files[0])
		}},
		{"duplicate module file", func(i *project.ComponentInventory) {
			i.Provenance.Modules = append(i.Provenance.Modules, i.Provenance.Modules[0])
		}},
		{"module and declared input disagree", func(i *project.ComponentInventory) {
			for n := range i.Provenance.Modules {
				if i.Provenance.Modules[n].Path == "go.mod" {
					i.Provenance.Modules[n].SHA256 = ce274ChangedHash()
				}
			}
		}},
		{"duplicate target-local assumption", func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Assumptions = append(c.Assumptions, c.Assumptions[0])
		}},
		{"blank declared assumption identity", func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Assumptions[0].ID = "" }},
		{"blank declared assumption statement", func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Assumptions[0].Statement = "" }},
		{"blank declared assumption reason", func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Assumptions[0].Reason = "" }},
		{"unknown declared assumption source", func(i *project.ComponentInventory) {
			ce274Check(t, i, f.Target).Assumptions[0].Source.Path = "missing.md"
		}},
		{"stale declared assumption source", func(i *project.ComponentInventory) {
			ce274Check(t, i, f.Target).Assumptions[0].Source.SHA256 = ce274ChangedHash()
		}},
		{"unknown invocation kind", func(i *project.ComponentInventory) { ce274SelectedInvocation(t, i, f.Target).Kind = "unknown" }},
		{"unknown invocation check", func(i *project.ComponentInventory) { ce274SelectedInvocation(t, i, f.Target).Check = "missing" }},
		{"bare invocation check", func(i *project.ComponentInventory) { ce274SelectedInvocation(t, i, f.Target).Check = "Step" }},
		{"unselected invocation check", func(i *project.ComponentInventory) { ce274SelectedInvocation(t, i, f.Target).Check = f.Other }},
		{"duplicate local invocation key", func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Invocations = append(c.Invocations, *ce274SelectedInvocation(t, i, f.Target))
		}},
		{"blank invocation identity", func(i *project.ComponentInventory) { ce274SelectedInvocation(t, i, f.Target).ID = "" }},
		{"blank invocation command", func(i *project.ComponentInventory) { ce274SelectedInvocation(t, i, f.Target).Command = "" }},
		{"unknown invocation input", func(i *project.ComponentInventory) {
			ce274SelectedInvocation(t, i, f.Target).Inputs[0].Path = "missing.bin"
		}},
		{"stale invocation input", func(i *project.ComponentInventory) {
			ce274SelectedInvocation(t, i, f.Target).Inputs[0].SHA256 = ce274ChangedHash()
		}},
		{"duplicate invocation input", func(i *project.ComponentInventory) {
			v := ce274SelectedInvocation(t, i, f.Target)
			v.Inputs = append(v.Inputs, v.Inputs[0])
		}},
	}
	for _, tc := range cases {
		for _, operation := range []string{"proof", "assumption"} {
			for _, clear := range []bool{false, true} {
				mode := "replace"
				if clear {
					mode = "clear"
				}
				t.Run(tc.name+"/"+operation+"/"+mode, func(t *testing.T) {
					r := cpClone(t, baseline)
					inv := cpClone(t, f.Inv)
					tc.edit(inv)
					ce274Unchanged(t, &r, func() error {
						if operation == "proof" {
							var proofs []ce274Proof
							if !clear {
								proofs = []ce274Proof{ce274ProofFor(t, f, f.Target, true)}
							}
							return ce274Proofs(t, &r, inv, proofs)
						}
						var records []ce274AssumptionRecord
						if !clear {
							records = ce274Records(t, f)
						}
						return ce274Assumptions(t, &r, inv, records)
					})
				})
			}
		}
	}
}

func TestComponentEvidenceRevalidatesRetainedKindsOnReplaceAndClear(t *testing.T) {
	f := ce274Fixture(t)
	proofChanges := []struct {
		name       string
		incomplete bool
		edit       func(*project.ComponentInventory)
	}{
		{"target source", false, func(i *project.ComponentInventory) { ce274ChangeFile(i, "core/core.go", ce274ChangedHash()) }},
		{"target contract", false, func(i *project.ComponentInventory) { ce274ChangeContract(i, f.Target, ce274ChangedHash()) }},
		{"selected helper source", false, func(i *project.ComponentInventory) { ce274ChangeFile(i, "core/contracts.go", ce274ChangedHash()) }},
		{"selected helper contract", false, func(i *project.ComponentInventory) { ce274ChangeContract(i, f.Helper, ce274ChangedHash()) }},
		{"extra declaration input", false, func(i *project.ComponentInventory) { ce274ChangeFile(i, "neighbors/extra.go", ce274ChangedHash()) }},
		{"extra actual model input", false, func(i *project.ComponentInventory) {
			ce274ChangeFile(i, ".invariant/specs/Counter.tla", ce274ChangedHash())
		}},
		{"actual dependency input", false, func(i *project.ComponentInventory) { ce274ChangeFile(i, "go.sum", ce274ChangedHash()) }},
		{"invocation command", false, func(i *project.ComponentInventory) { ce274SelectedInvocation(t, i, f.Target).Command += " changed" }},
		{"invocation argument order", false, func(i *project.ComponentInventory) { slices.Reverse(ce274SelectedInvocation(t, i, f.Target).Args) }},
		{"invocation working directory", false, func(i *project.ComponentInventory) { ce274SelectedInvocation(t, i, f.Target).WorkingDir = "core" }},
		{"invocation environment", false, func(i *project.ComponentInventory) { ce274SelectedInvocation(t, i, f.Target).Env["MODE"] = "changed" }},
		{"invocation sampling", false, func(i *project.ComponentInventory) { ce274SelectedInvocation(t, i, f.Target).Sampling["seed"] = "991" }},
		{"invocation input selection", false, func(i *project.ComponentInventory) { ce274SelectedInvocation(t, i, f.Target).Inputs = nil }},
		{"invocation removed", false, func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Invocations = slices.DeleteFunc(c.Invocations, func(v project.ResolvedComponentInvocation) bool { return v.Kind == "proof" && v.ID == "run" })
		}},
		{"proof selection removed", false, func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Proof = nil
			c.Invocations = slices.DeleteFunc(c.Invocations, func(v project.ResolvedComponentInvocation) bool { return v.Kind == "proof" })
		}},
		{"selection would demote retained coverage", false, func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Proof.Contracts = append(c.Proof.Contracts, f.Other)
		}},
		{"target removed", false, func(i *project.ComponentInventory) {
			i.Components = slices.DeleteFunc(i.Components, func(c project.Component) bool { return c.ID == f.Target })
			ce274DropCheck(t, i, f.Target, f.Other)
		}},
		{"incomplete attempt target source", true, func(i *project.ComponentInventory) { ce274ChangeFile(i, "core/core.go", ce274ChangedHash()) }},
		{"incomplete attempt target contract", true, func(i *project.ComponentInventory) { ce274ChangeContract(i, f.Target, ce274ChangedHash()) }},
		{"incomplete attempt selected contract", true, func(i *project.ComponentInventory) { ce274ChangeContract(i, f.Helper, ce274ChangedHash()) }},
		{"incomplete attempt selected source", true, func(i *project.ComponentInventory) { ce274ChangeFile(i, "core/contracts.go", ce274ChangedHash()) }},
	}
	for _, tc := range proofChanges {
		for _, clear := range []bool{false, true} {
			mode := "replace"
			if clear {
				mode = "clear"
			}
			t.Run("retained proof/"+tc.name+"/assumption "+mode, func(t *testing.T) {
				r := ce274Attach(t, f, true)
				proof := ce274ProofFor(t, f, f.Target, !tc.incomplete)
				proof.Inputs = append(proof.Inputs, ce274Component(t, f.Inv, f.Same))
				if tc.incomplete {
					proof.Inputs, proof.Verified, proof.Contracts = nil, nil, nil
				}
				if err := ce274Proofs(t, &r, f.Inv, []ce274Proof{proof}); err != nil {
					t.Fatal(err)
				}
				inv := cpClone(t, f.Inv)
				tc.edit(inv)
				var records []ce274AssumptionRecord
				if !clear {
					// This fresh record belongs to another configured target.
					// It intentionally replaces all previous assumptions, so the
					// only stale evidence in the candidate is the retained proof.
					records = []ce274AssumptionRecord{ce274RecordFor(t, inv, f.Other)}
				}
				probe := ce274Seed()
				if err := ce274Assumptions(t, &probe, inv, records); err != nil {
					t.Fatalf("replacement inventory and incoming assumptions must be independently valid: %v", err)
				}
				ce274Unchanged(t, &r, func() error { return ce274Assumptions(t, &r, inv, records) })
			})
		}
	}

	assumptionChanges := []struct {
		name string
		edit func(*project.ComponentInventory)
	}{
		{"target source", func(i *project.ComponentInventory) { ce274ChangeFile(i, "core/core.go", ce274ChangedHash()) }},
		{"target contract", func(i *project.ComponentInventory) { ce274ChangeContract(i, f.Target, ce274ChangedHash()) }},
		{"assumption statement", func(i *project.ComponentInventory) {
			ce274Check(t, i, f.Target).Assumptions[0].Statement += " Changed."
		}},
		{"assumption reason", func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Assumptions[0].Reason += " Changed." }},
		{"assumption source path", func(i *project.ComponentInventory) {
			ce274Check(t, i, f.Target).Assumptions[0].Source = ce274File(t, i, "policy.txt")
		}},
		{"assumption source bytes", func(i *project.ComponentInventory) { ce274ChangeFile(i, "assumptions.md", ce274ChangedHash()) }},
		{"assumption identity", func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Assumptions[0].ID = "different-clock" }},
		{"assumption removed", func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Assumptions = slices.DeleteFunc(c.Assumptions, func(a project.ResolvedComponentAssumption) bool { return a.ID == "clock" })
		}},
		{"assumption target unselected", func(i *project.ComponentInventory) {
			ce274DropCheck(t, i, f.Target, f.Other)
		}},
		{"assumption target removed", func(i *project.ComponentInventory) {
			i.Components = slices.DeleteFunc(i.Components, func(c project.Component) bool { return c.ID == f.Target })
			ce274DropCheck(t, i, f.Target, f.Other)
		}},
	}
	for _, tc := range assumptionChanges {
		for _, clear := range []bool{false, true} {
			mode := "replace"
			if clear {
				mode = "clear"
			}
			t.Run("retained assumption/"+tc.name+"/proof "+mode, func(t *testing.T) {
				r := ce274Attach(t, f, true)
				inv := cpClone(t, f.Inv)
				tc.edit(inv)
				var proofs []ce274Proof
				if !clear {
					next := f
					next.Inv = inv
					proofs = []ce274Proof{ce274ProofFor(t, next, f.Other, true)}
				}
				probe := ce274Seed()
				if err := ce274Proofs(t, &probe, inv, proofs); err != nil {
					t.Fatalf("replacement inventory and incoming proofs must be independently valid: %v", err)
				}
				// A retained record whose target or assumption changed or went
				// away is stale (D-0253).
				var err error
				ce274Unchanged(t, &r, func() error { err = ce274Proofs(t, &r, inv, proofs); return err })
				if !errors.Is(err, verify.ErrStaleComponentEvidence) {
					t.Fatalf("a retained assumption whose anchors changed isn't stale evidence: %v", err)
				}
			})
		}
	}
	// A previously incomplete proof cannot become proved merely because the
	// replacement inventory stops selecting a missing contract.
	for _, clear := range []bool{false, true} {
		t.Run("retained proof/selection would promote coverage/clear="+map[bool]string{false: "false", true: "true"}[clear], func(t *testing.T) {
			r := ce274Attach(t, f, false)
			proof := ce274ProofFor(t, f, f.Target, false)
			proof.Contracts = []string{f.Target}
			if err := ce274Proofs(t, &r, f.Inv, []ce274Proof{proof}); err != nil {
				t.Fatal(err)
			}
			ce274Statuses(t, r)
			inv := cpClone(t, f.Inv)
			ce274Check(t, inv, f.Target).Proof.Contracts = []string{f.Target}
			var records []ce274AssumptionRecord
			if !clear {
				records = []ce274AssumptionRecord{ce274RecordFor(t, inv, f.Other)}
			}
			ce274Unchanged(t, &r, func() error { return ce274Assumptions(t, &r, inv, records) })
		})
	}
	// Order changes are not stale snapshots. Matching module/file and scoped
	// driver overlaps and repeated local IDs remain legitimate in both paths.
	for _, first := range []string{"proof", "assumption"} {
		t.Run("equivalent inventory/clear "+first+" first", func(t *testing.T) {
			r := ce274Attach(t, f, true)
			inv := cpClone(t, f.Inv)
			ce274ReverseInventory(inv)
			if first == "proof" {
				if err := ce274Proofs(t, &r, inv, nil); err != nil {
					t.Fatal(err)
				}
				if len(ce274AssumptionRows(t, r)) != 2 {
					t.Fatal("proof clear lost retained assumptions")
				}
				if err := ce274Assumptions(t, &r, inv, nil); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := ce274Assumptions(t, &r, inv, nil); err != nil {
					t.Fatal(err)
				}
				ce274Statuses(t, r, f.Target)
				if err := ce274Proofs(t, &r, inv, nil); err != nil {
					t.Fatal(err)
				}
			}
			ce274Statuses(t, r)
			if len(ce274AssumptionRows(t, r)) != 0 || r.Fingerprint != verify.Fingerprint(r) {
				t.Fatal("successful clears failed to finalize independent evidence")
			}
		})
	}
}

// Every other kind's attribution replays the retained assumptions, and one
// whose target or assumption changed is stale evidence whichever kind replays
// it, with the record's message, leaving the report as it was (D-0253).
func TestEveryKindReplaysAStaleAssumptionAsStaleEvidence(t *testing.T) {
	f := ce274Fixture(t)
	kinds := []struct {
		name      string
		attribute func(*verify.Report, *project.ComponentInventory) error
	}{
		{"proofs", func(r *verify.Report, inv *project.ComponentInventory) error {
			return r.AttributeComponentProofs(inv, nil)
		}},
		{"conformance", func(r *verify.Report, inv *project.ComponentInventory) error {
			return r.AttributeComponentConformance(inv, nil)
		}},
		{"runtime", func(r *verify.Report, inv *project.ComponentInventory) error {
			return r.AttributeComponentRuntime(inv, nil)
		}},
		{"contract tests", func(r *verify.Report, inv *project.ComponentInventory) error {
			return r.AttributeComponentContractTests(inv, nil)
		}},
	}
	changes := []struct {
		name string
		edit func(*project.ComponentInventory)
	}{
		{"target source", func(i *project.ComponentInventory) { ce274ChangeFile(i, "core/core.go", ce274ChangedHash()) }},
		{"assumption statement", func(i *project.ComponentInventory) {
			ce274Check(t, i, f.Target).Assumptions[0].Statement += " Changed."
		}},
		{"assumption removed", func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Assumptions = slices.DeleteFunc(c.Assumptions, func(a project.ResolvedComponentAssumption) bool { return a.ID == "clock" })
		}},
	}
	for _, kind := range kinds {
		for _, change := range changes {
			t.Run(kind.name+"/"+change.name, func(t *testing.T) {
				r := ce274Seed()
				if err := ce274Assumptions(t, &r, f.Inv, ce274Records(t, f)); err != nil {
					t.Fatal(err)
				}
				if same := cpClone(t, r); kind.attribute(&same, f.Inv) != nil {
					t.Fatal("the same clear fails against the unchanged inventory")
				}
				inv := cpClone(t, f.Inv)
				change.edit(inv)
				var err error
				ce274Unchanged(t, &r, func() error { err = kind.attribute(&r, inv); return err })
				if !errors.Is(err, verify.ErrStaleComponentEvidence) || !strings.Contains(err.Error(), "component assumptions: invalid, stale or duplicate record") {
					t.Fatalf("replaying a stale assumption: %v", err)
				}
			})
		}
	}
}

func ce274ChangedHash() string { return "sha256:" + strings.Repeat("f", 64) }

func ce274ExternalDeclaration(t *testing.T, inv *project.ComponentInventory) project.Component {
	t.Helper()
	for _, c := range inv.Provenance.Declarations {
		if strings.HasSuffix(c.Source, "_test.go") {
			return c
		}
	}
	t.Fatal("fixture lacks selected external test")
	return project.Component{}
}

func ce274File(t *testing.T, inv *project.ComponentInventory, name string) project.ComponentFile {
	t.Helper()
	for _, files := range [][]project.ComponentFile{inv.Files, inv.Provenance.Files, inv.Provenance.Modules} {
		for _, file := range files {
			if file.Path == name {
				return file
			}
		}
	}
	t.Fatalf("fixture lacks file %s", name)
	return project.ComponentFile{}
}

func ce274SelectedInvocation(t *testing.T, inv *project.ComponentInventory, target string) *project.ResolvedComponentInvocation {
	t.Helper()
	check := ce274Check(t, inv, target)
	for i := range check.Invocations {
		if v := &check.Invocations[i]; v.Kind == "proof" && v.ID == "run" {
			return v
		}
	}
	t.Fatal("fixture lacks proof invocation run")
	return nil
}

func ce274RecordFor(t *testing.T, inv *project.ComponentInventory, target string) ce274AssumptionRecord {
	t.Helper()
	for _, assumption := range ce274Check(t, inv, target).Assumptions {
		if assumption.ID == "clock" {
			return ce274AssumptionRecord{Target: target, Input: ce274Component(t, inv, target), Assumption: assumption}
		}
	}
	t.Fatal("fixture lacks clock assumption")
	return ce274AssumptionRecord{}
}

// Update every occurrence to create a consistent replacement inventory. The
// old evidence, not an internally contradictory new inventory, must reject it.
func ce274ChangeFile(inv *project.ComponentInventory, name, hash string) {
	for _, files := range [][]project.ComponentFile{inv.Files, inv.Provenance.Files, inv.Provenance.Modules} {
		for i := range files {
			if files[i].Path == name {
				files[i].SHA256 = hash
			}
		}
	}
	for _, components := range [][]project.Component{inv.Components, inv.Provenance.Declarations} {
		for i := range components {
			if components[i].Source == name {
				components[i].SourceSHA256 = hash
			}
		}
	}
	for i := range inv.Checks {
		check := &inv.Checks[i]
		for j := range check.Assumptions {
			if check.Assumptions[j].Source.Path == name {
				check.Assumptions[j].Source.SHA256 = hash
			}
		}
		for j := range check.Invocations {
			for k := range check.Invocations[j].Inputs {
				if check.Invocations[j].Inputs[k].Path == name {
					check.Invocations[j].Inputs[k].SHA256 = hash
				}
			}
		}
	}
}

func ce274ChangeContract(inv *project.ComponentInventory, id, hash string) {
	for _, components := range [][]project.Component{inv.Components, inv.Provenance.Declarations} {
		for i := range components {
			if components[i].ID == id {
				components[i].ContractSHA256 = hash
			}
		}
	}
}

// Keep the selected test/driver provenance selected when removing one target's
// check, so the replacement remains a valid independently resolvable inventory.
func ce274DropCheck(t *testing.T, inv *project.ComponentInventory, target, other string) {
	t.Helper()
	removed, remaining := ce274Check(t, inv, target), ce274Check(t, inv, other)
	remaining.Tests = append(remaining.Tests, removed.Tests...)
	remaining.Conformance = append(remaining.Conformance, removed.Conformance...)
	for _, invocation := range removed.Invocations {
		if invocation.Kind != "proof" {
			remaining.Invocations = append(remaining.Invocations, invocation)
		}
	}
	inv.Checks = slices.DeleteFunc(inv.Checks, func(c project.ResolvedComponentCheck) bool { return c.Target == target })
}

func ce274ReverseInventory(inv *project.ComponentInventory) {
	slices.Reverse(inv.Scope)
	slices.Reverse(inv.Files)
	slices.Reverse(inv.Components)
	slices.Reverse(inv.Checks)
	slices.Reverse(inv.Provenance.Declarations)
	slices.Reverse(inv.Provenance.Files)
	slices.Reverse(inv.Provenance.Modules)
	for i := range inv.Checks {
		c := &inv.Checks[i]
		if c.Proof != nil {
			slices.Reverse(c.Proof.Contracts)
		}
		slices.Reverse(c.Tests)
		slices.Reverse(c.Conformance)
		slices.Reverse(c.Assumptions)
		slices.Reverse(c.Invocations)
		for j := range c.Invocations {
			slices.Reverse(c.Invocations[j].Inputs)
		}
	}
}
