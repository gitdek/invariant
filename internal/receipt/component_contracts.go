package receipt

import (
	"fmt"
	"strings"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

// componentContracts renders trusted contracts (#294): each declaration with
// its operation's identity, support and use, the supplied backing-test
// results, and the trusted-boundary diagnostics.
func componentContracts(b *strings.Builder, r *verify.Report) {
	e := r.Components
	standing, diagnostics := componentContractStanding(r)
	declarations := map[string]project.Component{}
	if p := e.Inventory.Provenance; p != nil {
		for _, c := range p.Declarations {
			declarations[c.ID] = c
		}
	}
	b.WriteString("\n### Trusted contracts\n\nA trusted contract is an assumption about a real operation beyond a proof boundary: the standard library, a map, I/O, a third-party package or the application's own code. Its backing tests can support it, and nothing here proves the operation itself. A declaration alone shows no call, no test run and no proof that used it.\n\n")
	b.WriteString("| Contract | Operation | Support | Use | Evidence |\n| :-- | :-- | :-- | :-- | :-- |\n")
	for _, c := range e.Inventory.TrustedContracts {
		row, ok := standing[c.ID]
		support, use := "trusted · untested", "declaration only: no proof reported using it"
		if ok && row.Support == "tested" {
			support = "trusted · tested"
		}
		if ok && row.Use == "assumed" {
			use = "assumed by " + componentStrings(row.Relying)
		}
		contract := componentLiteral(c.Contract)
		if d, ok := declarations[c.Contract]; ok {
			contract = componentDeclaration(d)
		}
		parts := []string{"contract: " + contract, "identity: " + componentBinding(row.Binding), "backing tests: " + componentStrings(c.Tests)}
		for _, v := range c.Invocations {
			parts = append(parts, "declared "+componentInvocation(&v))
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s |\n", componentLiteral(c.ID), componentOperation(c.Operation, declarations), support, use, strings.Join(parts, "; "))
	}

	b.WriteString("\n### Trusted contract tests\n")
	if len(e.ContractTests) == 0 {
		b.WriteString("\nno supplied results\n")
	}
	b.WriteString("\nSupplied results describe runs the component API didn't make. A backing test supports its contract only when it was exercised and passed in a process that passed, with the contract's snapshots and its operation's identity present.\n\n")
	b.WriteString("| Contract | Check | Status | Evidence |\n| :-- | :-- | :-- | :-- |\n")
	for _, row := range e.ContractTests {
		qualified := componentContractTestQualification(r, row)
		result := row.Result
		parts := []string{"Origin: " + componentOrigin(row.Origin), "Process outcome: " + componentOutcome(result.Outcome), componentInvocation(result.Invocation),
			componentTool(result.Runner), "identity: " + componentBinding(result.Binding), componentInputs(result.Inputs, result.FileInputs),
			"diagnostics: " + componentStrings(result.Diagnostics)}
		if len(result.Tests) == 0 {
			parts = append(parts, "no supplied test results")
		}
		parts = append(parts, componentTests(result.Tests, qualified.Tests)...)
		componentCheckRow(b, row.Contract, row.Check, qualified.Status, parts)
	}

	b.WriteString("\n### Trusted boundary diagnostics\n\n")
	if len(diagnostics) == 0 {
		b.WriteString("none\n")
		return
	}
	b.WriteString("| Kind | Contract | Target | Detail |\n| :-- | :-- | :-- | :-- |\n")
	for _, d := range diagnostics {
		fmt.Fprintf(b, "| %s | %s | %s | %s |\n", componentLiteral(d.Kind), componentOptional(d.Contract), componentOptional(d.Target), componentText(d.Detail))
	}
}

// componentContractStanding derives each contract's standing from this report
// alone, replaying its supplied proof and backing-test results on a detached
// report as attribution does, so decoded JSON can't carry a stale or forged
// one. A result that doesn't revalidate supports and uses nothing.
func componentContractStanding(r *verify.Report) (map[string]verify.ComponentContractEvidence, []verify.ComponentContractDiagnostic) {
	inv := &r.Components.Inventory
	detached := func() verify.Report { return verify.Report{Bounds: r.Bounds, Toolchain: r.Toolchain} }
	var proofs []verify.ComponentProofResult
	for _, row := range r.Components.Proofs {
		if row.Result == nil {
			continue
		}
		d := detached()
		if d.AttributeComponentProofs(inv, []verify.ComponentProofResult{*row.Result}) == nil {
			proofs = append(proofs, *row.Result)
		}
	}
	var tests []verify.ComponentContractTestResult
	for _, row := range r.Components.ContractTests {
		result := componentKnownContractTest(row.Result)
		d := detached()
		if row.Contract == result.Contract && row.Check == result.Check && d.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{result}) == nil {
			tests = append(tests, result)
		}
	}
	d := detached()
	// Results valid alone can still clash together, as duplicates do.
	if d.AttributeComponentProofs(inv, proofs) != nil || d.AttributeComponentContractTests(inv, tests) != nil {
		d = detached()
		_ = d.AttributeComponentProofs(inv, nil)
	}
	standing := map[string]verify.ComponentContractEvidence{}
	if d.Components == nil {
		return standing, nil
	}
	for _, c := range d.Components.Contracts {
		standing[c.Contract] = c
	}
	return standing, d.Components.ContractDiagnostics
}

func componentContractTestQualification(r *verify.Report, row verify.ComponentContractTestEvidence) verify.ComponentContractTestEvidence {
	result := componentKnownContractTest(row.Result)
	qualified := verify.Report{Bounds: r.Bounds, Toolchain: r.Toolchain}
	if row.Contract == result.Contract && row.Check == result.Check {
		if err := qualified.AttributeComponentContractTests(&r.Components.Inventory, []verify.ComponentContractTestResult{result}); err == nil {
			return qualified.Components.ContractTests[0]
		}
	}
	return verify.ComponentContractTestEvidence{Status: "unchecked"}
}

// Unknown outcomes count as absent for qualification only.
func componentKnownContractTest(result verify.ComponentContractTestResult) verify.ComponentContractTestResult {
	result.Outcome = componentKnownOutcome(result.Outcome)
	tests := make([]verify.ComponentRuntimeTestResult, len(result.Tests))
	for i, test := range result.Tests {
		test.Outcome = componentKnownOutcome(test.Outcome)
		assertions := make([]verify.ComponentRuntimeAssertionResult, len(test.Assertions))
		for j, a := range test.Assertions {
			a.Outcome = componentKnownOutcome(a.Outcome)
			assertions[j] = a
		}
		test.Assertions = assertions
		tests[i] = test
	}
	result.Tests = tests
	return result
}

func componentOperation(o project.ResolvedComponentOperation, declarations map[string]project.Component) string {
	switch o.Kind {
	case project.OperationStandardLibrary:
		return "standard library: " + componentLiteral(o.Module+"."+o.Symbol)
	case project.OperationDependency:
		return "dependency " + componentLiteral(o.Distribution) + ": " + componentLiteral(o.Module+"."+o.Symbol)
	case project.OperationApplication:
		if d, ok := declarations[o.Declaration]; ok {
			return "application: " + componentDeclaration(d)
		}
		return "application: " + componentLiteral(o.Declaration)
	}
	return componentLiteral(o.Kind)
}

func componentBinding(b *verify.ComponentContractBinding) string {
	if b == nil {
		return "not bound"
	}
	parts := []string{"runtime " + componentLiteral(shortImage(b.Runtime))}
	if b.Declaration != nil {
		parts = append(parts, "declaration "+componentDeclaration(*b.Declaration))
	}
	if w := b.Wheel; w != nil {
		parts = append(parts, "wheel "+componentLiteral(w.Name)+" "+componentLiteral(w.Version)+" · "+componentLiteral(w.Filename)+" · "+componentLiteral(short(w.SHA256)))
	}
	return strings.Join(parts, ", ")
}

// componentBoundary renders a proof attempt's account of its trusted boundary,
// only where its adapter gave one, so other receipts keep their text.
func componentBoundary(result *verify.ComponentProofResult) []string {
	var parts []string
	if len(result.Assumed) > 0 {
		ids := make([]string, len(result.Assumed))
		for i, use := range result.Assumed {
			ids[i] = use.Contract
		}
		parts = append(parts, "assumed trusted contracts: "+componentStrings(ids))
	}
	for _, u := range result.Unaccounted {
		text := componentLiteral(u.Kind) + " " + componentLiteral(u.Operation)
		if u.Detail != "" {
			text += ": " + componentText(u.Detail)
		}
		parts = append(parts, "unaccounted boundary: "+text)
	}
	return parts
}

func componentOptional(s string) string {
	if s == "" {
		return "—"
	}
	return componentLiteral(s)
}
