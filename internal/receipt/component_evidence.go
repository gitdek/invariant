package receipt

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

func componentViews(b *strings.Builder, r *verify.Report) {
	e := r.Components
	b.WriteString("\nThe component API runs no tools. These views describe supplied results and declared assumptions, independently of the recorded gate checks. Selections are configuration, not evidence that a check ran. Assumptions are always assumed / unverified.\n")
	b.WriteString("\n### Component selections\n\n| Target | Configuration |\n| :-- | :-- |\n")
	for _, check := range e.Inventory.Checks {
		var parts []string
		if check.Proof != nil {
			parts = append(parts, "selected proof contracts: "+componentStrings(check.Proof.Contracts))
		}
		parts = append(parts, "selected conformance: "+componentStrings(check.Conformance), "selected tests: "+componentStrings(check.Tests))
		for _, a := range check.Assumptions {
			parts = append(parts, "declared assumptions (assumed / unverified): "+componentAssumption(a))
		}
		for _, v := range check.Invocations {
			parts = append(parts, "declared "+componentInvocation(&v))
		}
		fmt.Fprintf(b, "| %s | %s |\n", componentLiteral(check.Target), strings.Join(parts, "; "))
	}

	componentCheckHeader(b, "conformance", len(e.Conformance))
	b.WriteString("\nSampled observations describe the supplied runs. Bounded-exhaustive state coverage and attempt coverage are qualified separately, within the supplied model and bounds.\n\n")
	componentCheckTable(b)
	for _, row := range e.Conformance {
		qualified := componentConformanceQualification(r, row)
		result := row.Result
		state, attempt, transitions := "not established", "not established", "not established"
		if qualified.ExhaustiveStates {
			state = "tested in every state"
		}
		if result.Attempts == nil {
			attempt = "not checked"
		} else if qualified.EveryStep {
			attempt = "every step tried"
		}
		if qualified.TransitionsChecked {
			transitions = "checked"
		}
		parts := []string{
			"Origin: " + componentOrigin(row.Origin),
			"Mode: " + componentLiteral(result.Mode), "Outcome: " + componentOutcome(result.Outcome),
			fmt.Sprintf("%d runs, %d steps, %d states, %d transitions, %d model states", result.Runs, result.Steps, result.States, result.Transitions, result.ModelStates),
			"Transitions: " + transitions, "State coverage: " + state, "Attempt coverage: " + attempt,
			"checked IDs: " + componentStrings(result.Checked),
			componentInvocation(result.Invocation), componentTool(result.Runner), componentInputs(result.Inputs, result.FileInputs),
			componentContext(result.Model, result.Bounds), componentObservations(result.Observations),
			"diagnostics: " + componentStrings(result.Diagnostics),
		}
		if a := result.Attempts; a != nil {
			parts = append(parts, "Attempt outcome: "+componentOutcome(a.Outcome),
				fmt.Sprintf("%d attempts in %d states", a.Attempts, a.States), componentObservations(a.Observations),
				"attempt diagnostics: "+componentStrings(a.Diagnostics))
		}
		componentCheckRow(b, row.Target, row.Check, qualified.Status, parts)
	}

	componentCheckHeader(b, "runtime", len(e.Runtime))
	b.WriteString("\nA supplied runtime result does not establish target code coverage. Process, test and assertion outcomes remain independent; passing a process invents no tests or assertions.\n\n")
	componentCheckTable(b)
	for _, row := range e.Runtime {
		qualified := componentRuntimeQualification(r, row)
		result := row.Result
		parts := []string{"Origin: " + componentOrigin(row.Origin), "Process outcome: " + componentOutcome(result.Outcome), componentInvocation(result.Invocation),
			componentTool(result.Runner), componentInputs(result.Inputs, result.FileInputs), componentContext(result.Model, result.Bounds),
			"diagnostics: " + componentStrings(result.Diagnostics)}
		if len(result.Tests) == 0 {
			parts = append(parts, "no supplied test results")
		}
		for _, test := range result.Tests {
			status := "unchecked"
			var assertions []verify.ComponentRuntimeAssertionEvidence
			for _, q := range qualified.Tests {
				if q.Check == test.Check {
					status, assertions = q.Status, q.Assertions
				}
			}
			parts = append(parts, "Test: "+componentLiteral(test.Check), "Test status: "+status,
				fmt.Sprintf("test exercised: %t", test.Exercised), "Test outcome: "+componentOutcome(test.Outcome),
				"test diagnostics: "+componentStrings(test.Diagnostics))
			for _, a := range test.Assertions {
				status := "unchecked"
				for _, q := range assertions {
					if q.ID == a.ID {
						status = q.Status
					}
				}
				parts = append(parts, "Assertion: "+componentLiteral(a.ID), "Assertion status: "+status,
					fmt.Sprintf("assertion exercised: %t", a.Exercised), "Assertion outcome: "+componentOutcome(a.Outcome),
					"assertion diagnostics: "+componentStrings(a.Diagnostics))
				if a.Source != nil {
					parts = append(parts, "assertion source: "+componentFile(*a.Source))
				} else {
					parts = append(parts, "assertion source not supplied")
				}
			}
		}
		componentCheckRow(b, row.Target, row.Check, qualified.Status, parts)
	}

	componentCheckHeader(b, "assumptions", len(e.Assumptions))
	b.WriteString("\n| Target | Assumption | Status | Evidence |\n| :-- | :-- | :-- | :-- |\n")
	for _, row := range e.Assumptions {
		fmt.Fprintf(b, "| %s | %s | assumed / unverified | %s; target input: %s |\n", componentLiteral(row.Target),
			componentLiteral(row.Record.Assumption.ID), componentAssumption(row.Record.Assumption), componentDeclaration(row.Record.Input))
	}

	b.WriteString("\n### Component input provenance\n\nSelected input declarations and files do not add production coverage.\n\n| Input declaration | Source and contract |\n| :-- | :-- |\n")
	if p := e.Inventory.Provenance; p != nil {
		for _, c := range p.Declarations {
			fmt.Fprintf(b, "| %s | %s |\n", componentLiteral(c.ID), componentDeclaration(c))
		}
		b.WriteString("\n| Input file | Identity |\n| :-- | :-- |\n")
		for _, f := range p.Files {
			fmt.Fprintf(b, "| %s | %s |\n", componentLiteral(f.Path), componentLiteral(short(f.SHA256)))
		}
		b.WriteString("\n| Module file | Identity |\n| :-- | :-- |\n")
		for _, f := range p.Modules {
			fmt.Fprintf(b, "| %s | %s |\n", componentLiteral(f.Path), componentLiteral(short(f.SHA256)))
		}
	}
}

func componentCheckHeader(b *strings.Builder, kind string, records int) {
	fmt.Fprintf(b, "\n### Component %s\n", kind)
	if records == 0 {
		b.WriteString("\nno supplied results\n")
	}
}

func componentCheckTable(b *strings.Builder) {
	b.WriteString("| Target | Check | Status | Evidence |\n| :-- | :-- | :-- | :-- |\n")
}

func componentCheckRow(b *strings.Builder, target, check, status string, parts []string) {
	fmt.Fprintf(b, "| %s | %s | %s | %s |\n", componentLiteral(target), componentLiteral(check), status, strings.Join(parts, "; "))
}

func componentStrings(values []string) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = componentLiteral(value)
	}
	return strings.Join(parts, ", ")
}

func componentMap(values map[string]string, pins bool) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := values[key]
		if pins {
			value = short(shortImage(value))
		}
		parts = append(parts, componentLiteral(key)+"="+componentLiteral(value))
	}
	return strings.Join(parts, ", ")
}

func componentFile(f project.ComponentFile) string {
	return componentLiteral(f.Path) + " · " + componentLiteral(short(f.SHA256))
}

func componentFiles(files []project.ComponentFile) string {
	parts := make([]string, len(files))
	for i, f := range files {
		parts[i] = componentFile(f)
	}
	return strings.Join(parts, ", ")
}

func componentDeclaration(c project.Component) string {
	text := componentLiteral(c.ID) + " · source: " + componentFile(project.ComponentFile{Path: c.Source, SHA256: c.SourceSHA256})
	if c.ContractSHA256 != "" {
		text += " · contract: " + componentLiteral(short(c.ContractSHA256))
	}
	return text
}

func componentInputs(inputs []project.Component, files []project.ComponentFile) string {
	parts := make([]string, len(inputs))
	for i, c := range inputs {
		parts[i] = componentDeclaration(c)
	}
	return "supplied declarations: " + strings.Join(parts, ", ") + "; supplied files: " + componentFiles(files)
}

func componentInvocation(v *project.ResolvedComponentInvocation) string {
	if v == nil {
		return "invocation not supplied"
	}
	return "invocation: " + componentLiteral(v.ID) + "; kind: " + componentLiteral(v.Kind) + "; check: " + componentLiteral(v.Check) +
		"; command: " + componentLiteral(v.Command) + "; arguments: " + componentStrings(v.Args) + "; working directory: " + componentLiteral(v.WorkingDir) +
		"; environment: " + componentMap(v.Env, false) + "; sampling: " + componentMap(v.Sampling, false) + "; invocation inputs: " + componentFiles(v.Inputs)
}

func componentTool(tool *verify.ComponentProofTool) string {
	if tool == nil {
		return "tool not supplied"
	}
	return "tool: " + componentLiteral(tool.Name) + "; version: " + componentLiteral(tool.Version) + "; pins: " + componentMap(tool.Pins, true)
}

func componentContext(model *verify.ComponentConformanceModel, bounds *verify.ComponentConformanceBounds) string {
	var parts []string
	if model != nil {
		parts = append(parts, "model: "+componentLiteral(model.Module), "model inputs: "+componentFiles(model.Inputs))
	}
	if bounds != nil {
		parts = append(parts, "bounds: "+componentMap(bounds.Values, false), "bounds sources: "+componentFiles(bounds.Sources))
	}
	return strings.Join(parts, "; ")
}

func componentAssumption(a project.ResolvedComponentAssumption) string {
	return componentLiteral(a.ID) + "; statement: " + componentText(a.Statement) + "; reason: " + componentText(a.Reason) + "; source: " + componentFile(a.Source)
}

func componentObservations(observations []verify.ComponentConformanceObservation) string {
	var parts []string
	for _, o := range observations {
		parts = append(parts, fmt.Sprintf("observation run %d step %d: %s; arguments: %s; from: %s; to: %s", o.Run, o.Step,
			componentLiteral(o.Action), componentStrings(o.Args), componentMap(o.From, false), componentMap(o.To, false)))
	}
	return strings.Join(parts, "; ")
}

func componentOutcome(outcome string) string {
	if outcome == "" {
		return "outcome not supplied"
	}
	return componentLiteral(outcome)
}

// Reuse attribution's provenance and qualification rules on detached reports.
// Decoded JSON may carry stale statuses or unknown outcomes. Unknown outcomes
// count as absent for qualification only; rendering retains the original text.
func componentKnownOutcome(outcome string) string {
	switch outcome {
	case "passed", "failed", "timed_out", "skipped", "empty":
		return outcome
	default:
		return ""
	}
}

func componentConformanceQualification(r *verify.Report, row verify.ComponentConformanceEvidence) verify.ComponentConformanceEvidence {
	result := row.Result
	result.Outcome = componentKnownOutcome(result.Outcome)
	if result.Mode != "sampled" && result.Mode != "bounded-exhaustive" {
		result.Mode = ""
	}
	if result.Attempts != nil {
		a := *result.Attempts
		a.Outcome = componentKnownOutcome(a.Outcome)
		result.Attempts = &a
	}
	qualified := verify.Report{Bounds: r.Bounds, ModelOnly: r.ModelOnly}
	if row.Target == result.Target && row.Check == result.Check {
		if err := qualified.AttributeComponentConformance(&r.Components.Inventory, []verify.ComponentConformanceResult{result}); err == nil {
			return qualified.Components.Conformance[0]
		}
	}
	return verify.ComponentConformanceEvidence{Status: "unchecked"}
}

func componentRuntimeQualification(r *verify.Report, row verify.ComponentRuntimeEvidence) verify.ComponentRuntimeEvidence {
	result := row.Result
	result.Outcome = componentKnownOutcome(result.Outcome)
	result.Tests = slices.Clone(result.Tests)
	for i := range result.Tests {
		test := &result.Tests[i]
		test.Outcome = componentKnownOutcome(test.Outcome)
		test.Assertions = slices.Clone(test.Assertions)
		for j := range test.Assertions {
			test.Assertions[j].Outcome = componentKnownOutcome(test.Assertions[j].Outcome)
		}
	}
	qualified := verify.Report{Bounds: r.Bounds, ModelOnly: r.ModelOnly}
	if row.Target == result.Target && row.Check == result.Check {
		if err := qualified.AttributeComponentRuntime(&r.Components.Inventory, []verify.ComponentRuntimeResult{result}); err == nil {
			return qualified.Components.Runtime[0]
		}
	}
	return verify.ComponentRuntimeEvidence{Status: "unchecked"}
}
