package verify_test

import (
	"slices"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

func TestSuppliedRuntimeRejectsInvalidAttributionAtomically(t *testing.T) {
	f := cr287Fixture(t)
	cr287RequireAPI(t, f)
	good := cr287Good(t, f, "", "")
	cr287Context(t, f, &good)
	baseline := cr287ValidationSeed(t, f, good)
	declaration := func(id string, edit func(*project.Component)) func(*cr287Result) {
		return func(r *cr287Result) {
			for i := range r.Inputs {
				if r.Inputs[i].ID == id {
					edit(&r.Inputs[i])
					return
				}
			}
			t.Fatalf("fixture lacks actual declaration %s", id)
		}
	}
	mutations := []struct {
		name string
		edit func(*cr287Result)
	}{
		{"unknown target", func(r *cr287Result) { r.Target = "go:unknown#Step@missing.go" }},
		{"bare target", func(r *cr287Result) { r.Target = "Step" }},
		{"unconfigured target", func(r *cr287Result) { r.Target = f.Helper }},
		{"target without selected tests", func(r *cr287Result) { r.Target = f.Assumed }},
		{"external test as production target", func(r *cr287Result) { r.Target = r.Check }},
		{"unknown check", func(r *cr287Result) { r.Check = "go:unknown#TestStep@missing.go" }},
		{"bare check", func(r *cr287Result) { r.Check = "TestStep" }},
		{"unselected known check", func(r *cr287Result) { r.Check, r.Invocation.Check, r.Tests[0].Check = f.Helper, f.Helper, f.Helper }},
		{"missing invocation", func(r *cr287Result) { r.Invocation = nil }},
		{"blank invocation identity", func(r *cr287Result) { r.Invocation.ID = " " }},
		{"unknown invocation identity", func(r *cr287Result) { r.Invocation.ID = "not-selected" }},
		{"wrong invocation kind", func(r *cr287Result) { r.Invocation.Kind = "conformance" }},
		{"bare invocation check", func(r *cr287Result) { r.Invocation.Check = "TestStep" }},
		{"different selected invocation check", func(r *cr287Result) { r.Invocation.Check = provenance257ID("tests/step_test.go", "TestAgain") }},
		{"changed command", func(r *cr287Result) { r.Invocation.Command += " changed" }},
		{"changed argument order", func(r *cr287Result) { slices.Reverse(r.Invocation.Args) }},
		{"additional argument", func(r *cr287Result) { r.Invocation.Args = append(r.Invocation.Args, "--extra") }},
		{"changed working directory", func(r *cr287Result) { r.Invocation.WorkingDir = "tests" }},
		{"changed environment", func(r *cr287Result) { r.Invocation.Env["MODE"] = "other" }},
		{"omitted environment", func(r *cr287Result) { delete(r.Invocation.Env, "MODE") }},
		{"additional environment", func(r *cr287Result) { r.Invocation.Env["EXTRA"] = "value" }},
		{"changed sampling", func(r *cr287Result) { r.Invocation.Sampling["seed"] = "43" }},
		{"omitted sampling", func(r *cr287Result) { delete(r.Invocation.Sampling, "seed") }},
		{"additional sampling", func(r *cr287Result) { r.Invocation.Sampling["extra"] = "value" }},
		{"omitted declared input", func(r *cr287Result) { r.Invocation.Inputs = r.Invocation.Inputs[1:] }},
		{"additional declared input", func(r *cr287Result) {
			r.Invocation.Inputs = append(r.Invocation.Inputs, ce274File(t, f.Inv, "assumptions.md"))
		}},
		{"duplicate declared input", func(r *cr287Result) { r.Invocation.Inputs = append(r.Invocation.Inputs, r.Invocation.Inputs[0]) }},
		{"stale declared input", func(r *cr287Result) { r.Invocation.Inputs[0].SHA256 = ce274ChangedHash() }},
		{"unknown declared input", func(r *cr287Result) { r.Invocation.Inputs[0].Path = "missing.bin" }},
		{"duplicate actual declaration", func(r *cr287Result) { r.Inputs = append(r.Inputs, r.Inputs[0]) }},
		{"unknown actual declaration", func(r *cr287Result) { r.Inputs[0].ID = "go:unknown#Absent@missing.go" }},
		{"bare actual declaration", func(r *cr287Result) { r.Inputs[0].ID = r.Inputs[0].Symbol }},
		{"inconsistent declaration identity", func(r *cr287Result) { r.Inputs[0].Symbol += "Changed" }},
		{"stale target source", declaration(f.Target, func(c *project.Component) { c.SourceSHA256 = ce274ChangedHash() })},
		{"stale target contract", declaration(f.Target, func(c *project.Component) { c.ContractSHA256 = ce274ChangedHash() })},
		{"stale selected contract source", declaration(f.Helper, func(c *project.Component) { c.SourceSHA256 = ce274ChangedHash() })},
		{"stale selected contract", declaration(f.Helper, func(c *project.Component) { c.ContractSHA256 = ce274ChangedHash() })},
		{"stale selected test source", declaration(good.Check, func(c *project.Component) { c.SourceSHA256 = ce274ChangedHash() })},
		{"duplicate actual file", func(r *cr287Result) { r.FileInputs = append(r.FileInputs, r.FileInputs[0]) }},
		{"stale actual file", func(r *cr287Result) { r.FileInputs[0].SHA256 = ce274ChangedHash() }},
		{"unknown actual file", func(r *cr287Result) { r.FileInputs[0].Path = "missing.bin" }},
		{"unknown actual test", func(r *cr287Result) { r.Tests[0].Check = "go:unknown#TestOther@missing.go" }},
		{"bare actual test", func(r *cr287Result) { r.Tests[0].Check = "TestStep" }},
		{"known unselected actual test", func(r *cr287Result) { r.Tests[0].Check = f.Other }},
		{"conformance driver as actual test", func(r *cr287Result) { r.Tests[0].Check = provenance257ID("drivers/drive.go", "Drive") }},
		{"duplicate actual test", func(r *cr287Result) { r.Tests = append(r.Tests, cpClone(t, r.Tests[0])) }},
		{"blank assertion identity", func(r *cr287Result) { r.Tests[0].Assertions[0].ID = "" }},
		{"whitespace assertion identity", func(r *cr287Result) { r.Tests[0].Assertions[0].ID = " \t" }},
		{"duplicate assertion identity", func(r *cr287Result) {
			r.Tests[0].Assertions = append(r.Tests[0].Assertions, cpClone(t, r.Tests[0].Assertions[0]))
		}},
		{"unknown assertion source", func(r *cr287Result) { r.Tests[0].Assertions[0].Source.Path = "missing.go" }},
		{"stale assertion source", func(r *cr287Result) { r.Tests[0].Assertions[0].Source.SHA256 = ce274ChangedHash() }},
		{"blank assertion source snapshot", func(r *cr287Result) { r.Tests[0].Assertions[0].Source.SHA256 = "" }},
		{"unknown process outcome", func(r *cr287Result) { r.Outcome = "package passed" }},
		{"unknown test outcome", func(r *cr287Result) { r.Tests[0].Outcome = "proved" }},
		{"unknown assertion outcome", func(r *cr287Result) { r.Tests[0].Assertions[0].Outcome = "covered" }},
		{"stale model source", func(r *cr287Result) { r.Model.Inputs[0].SHA256 = ce274ChangedHash() }},
		{"duplicate model input", func(r *cr287Result) { r.Model.Inputs = append(r.Model.Inputs, r.Model.Inputs[0]) }},
		{"model name instead of source", func(r *cr287Result) { r.Model.Module = "Spec" }},
		{"undeclared model input", func(r *cr287Result) { r.Model.Inputs = append(r.Model.Inputs, ce274File(t, f.Inv, "assumptions.md")) }},
		{"stale bounds source", func(r *cr287Result) { r.Bounds.Sources[0].SHA256 = ce274ChangedHash() }},
		{"duplicate bounds source", func(r *cr287Result) { r.Bounds.Sources = append(r.Bounds.Sources, r.Bounds.Sources[0]) }},
		{"undeclared bounds source", func(r *cr287Result) {
			r.Bounds.Sources = []project.ComponentFile{ce274File(t, f.Inv, "assumptions.md")}
		}},
		{"different bounds", func(r *cr287Result) { r.Bounds.Values["Limit"] = "3" }},
		{"omitted report bound", func(r *cr287Result) { delete(r.Bounds.Values, "Limit") }},
		{"additional report bound", func(r *cr287Result) { r.Bounds.Values["Other"] = "3" }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			r := cpClone(t, baseline)
			bad := cpClone(t, good)
			mutation.edit(&bad)
			ce274Unchanged(t, &r, func() error { return cr287Call(t, &r, f.Inv, []cr287Result{bad}) })
			// A preceding valid row changes evidence, testing detached validation
			// rather than rejection before any state has been touched.
			first := cr287Good(t, f, "", "repeat")
			first.Outcome, first.Diagnostics = "failed", []string{"new valid failed attempt"}
			ce274Unchanged(t, &r, func() error { return cr287Call(t, &r, f.Inv, []cr287Result{first, bad}) })
		})
	}
	t.Run("duplicate result after distinct valid invocation", func(t *testing.T) {
		r := cpClone(t, baseline)
		first := cr287Good(t, f, "", "repeat")
		duplicate := cpClone(t, good)
		duplicate.Outcome = "failed"
		ce274Unchanged(t, &r, func() error { return cr287Call(t, &r, f.Inv, []cr287Result{first, good, duplicate}) })
	})
}

func TestSuppliedRuntimeRejectsInvalidReplacementInventories(t *testing.T) {
	f := cr287Fixture(t)
	cr287RequireAPI(t, f)
	good := cr287Good(t, f, "", "")
	baseline := cr287ValidationSeed(t, f, good)
	cases := []struct {
		name string
		edit func(*project.ComponentInventory)
	}{
		{"legacy inventory", func(i *project.ComponentInventory) { i.Mode = "legacy" }},
		{"duplicate scope", func(i *project.ComponentInventory) { i.Scope = append(i.Scope, i.Scope[0]) }},
		{"duplicate component", func(i *project.ComponentInventory) { i.Components = append(i.Components, i.Components[0]) }},
		{"bare component identity", func(i *project.ComponentInventory) { i.Components[0].ID = i.Components[0].Symbol }},
		{"inconsistent component identity", func(i *project.ComponentInventory) { i.Components[0].Symbol += "Changed" }},
		{"duplicate production file", func(i *project.ComponentInventory) { i.Files = append(i.Files, i.Files[0]) }},
		{"inconsistent production source", func(i *project.ComponentInventory) { i.Components[0].SourceSHA256 = ce274ChangedHash() }},
		{"duplicate configured target", func(i *project.ComponentInventory) { i.Checks = append(i.Checks, i.Checks[0]) }},
		{"unknown configured target", func(i *project.ComponentInventory) { i.Checks[0].Target = "missing" }},
		{"duplicate selected contract", func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Proof.Contracts = append(c.Proof.Contracts, c.Proof.Contracts[0])
		}},
		{"unknown selected test", func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Tests = []string{"missing"} }},
		{"bare selected test", func(i *project.ComponentInventory) { ce274Check(t, i, f.Target).Tests[0] = "TestStep" }},
		{"duplicate selected test", func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Tests = append(c.Tests, c.Tests[0])
		}},
		{"duplicate selected driver", func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Conformance = append(c.Conformance, c.Conformance[0])
		}},
		{"duplicate provenance declaration", func(i *project.ComponentInventory) {
			i.Provenance.Declarations = append(i.Provenance.Declarations, i.Provenance.Declarations[0])
		}},
		{"inconsistent test source", func(i *project.ComponentInventory) {
			for n := range i.Provenance.Declarations {
				if i.Provenance.Declarations[n].ID == good.Check {
					i.Provenance.Declarations[n].SourceSHA256 = ce274ChangedHash()
				}
			}
		}},
		{"duplicate provenance file", func(i *project.ComponentInventory) {
			i.Provenance.Files = append(i.Provenance.Files, i.Provenance.Files[0])
		}},
		{"duplicate module file", func(i *project.ComponentInventory) {
			i.Provenance.Modules = append(i.Provenance.Modules, i.Provenance.Modules[0])
		}},
		{"duplicate assumption", func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Assumptions = append(c.Assumptions, c.Assumptions[0])
		}},
		{"duplicate invocation", func(i *project.ComponentInventory) {
			c := ce274Check(t, i, f.Target)
			c.Invocations = append(c.Invocations, *cr287Selected(t, i, f.Target, "", ""))
		}},
		{"blank invocation identity", func(i *project.ComponentInventory) { cr287Selected(t, i, f.Target, "", "").ID = "" }},
		{"blank invocation command", func(i *project.ComponentInventory) { cr287Selected(t, i, f.Target, "", "").Command = "" }},
		{"unknown invocation kind", func(i *project.ComponentInventory) { cr287Selected(t, i, f.Target, "", "").Kind = "unknown" }},
		{"unselected invocation check", func(i *project.ComponentInventory) { cr287Selected(t, i, f.Target, "", "").Check = f.Other }},
		{"unknown invocation input", func(i *project.ComponentInventory) {
			cr287Selected(t, i, f.Target, "", "").Inputs[0].Path = "missing.bin"
		}},
		{"stale invocation input", func(i *project.ComponentInventory) {
			cr287Selected(t, i, f.Target, "", "").Inputs[0].SHA256 = ce274ChangedHash()
		}},
		{"duplicate invocation input", func(i *project.ComponentInventory) {
			v := cr287Selected(t, i, f.Target, "", "")
			v.Inputs = append(v.Inputs, v.Inputs[0])
		}},
	}
	for _, tc := range cases {
		for _, mode := range []string{"replace", "clear"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				r, inv := cpClone(t, baseline), cpClone(t, f.Inv)
				tc.edit(inv)
				var records []cr287Result
				if mode == "replace" {
					records = []cr287Result{good}
				}
				ce274Unchanged(t, &r, func() error { return cr287Call(t, &r, inv, records) })
			})
		}
	}
	for _, records := range [][]cr287Result{nil, {good}} {
		r := cpClone(t, baseline)
		ce274Unchanged(t, &r, func() error { return cr287Call(t, &r, nil, records) })
	}
}

func TestSuppliedRuntimeAcceptsKnownExtraInputsAndLocalAssertionIdentities(t *testing.T) {
	f := cr287Fixture(t)
	cr287RequireAPI(t, f)
	result := cr287Good(t, f, "", "")
	// Extra known declarations need not be selected tests. Every known actual
	// file remains supplied input, including the fixture's assumption sources.
	result.Inputs = append(result.Inputs, ce274Component(t, f.Inv, f.Other))
	result.Tests[0].Assertions[0].ID = "plain local assertion <with arbitrary text>"
	again := provenance257ID("tests/step_test.go", "TestAgain")
	result.Inputs = append(result.Inputs, ce274Component(t, f.Inv, again))
	second := cpClone(t, result.Tests[0])
	second.Check = again
	result.Tests = append(result.Tests, second)
	r := cr287Attach(t, f, result)
	rows := cr287Rows(t, r)
	if len(rows) != 1 || rows[0].Status != "tested" || len(rows[0].Tests) != 2 {
		t.Fatalf("valid supplied records rejected or dropped: %+v", rows)
	}
	cr287WantResult(t, rows[0], result)
	for _, test := range rows[0].Tests {
		if test.Status != "tested" || len(test.Assertions) != 1 || test.Assertions[0].Status != "passed" {
			t.Fatalf("local assertion identities must be scoped to their parent tests: %+v", test)
		}
	}
}

func cr287ValidationSeed(t *testing.T, f ce274FixtureData, result cr287Result) verify.Report {
	t.Helper()
	r := ce274Attach(t, f, true)
	if err := cc279Call(t, &r, f.Inv, []cc279Result{cc279Good(t, f, f.Target, "", "")}); err != nil {
		t.Fatal(err)
	}
	if err := cr287Call(t, &r, f.Inv, []cr287Result{result}); err != nil {
		t.Fatal(err)
	}
	return r
}
