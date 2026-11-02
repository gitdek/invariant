package receipt

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gitdek/invariant/internal/verify"
)

func componentOrigin(origin string) string {
	if origin == "" {
		origin = "supplied"
	}
	return componentLiteral(origin)
}

// pythonApplication renders the application's executions and checks, and
// larger, the overall check one size larger, when the receipt shows it
// nowhere else, as when TLC couldn't finish there (#342).
func pythonApplication(b *strings.Builder, a *verify.PythonApplicationReport, larger *verify.Larger) {
	explored := false
	for _, c := range a.Conformance {
		explored = explored || c.Mode == "bounded-exhaustive"
	}
	if explored && pythonApplicationDeclaresRefusal(a) {
		// #392: explain strict outcomes only when the evidence declares them.
		b.WriteString("\n### Python application · explored execution\n\nThe gate's harness explored the application at the ratified bounds: every state it reached was read back from the application, and every attempt was a bound application call. TLC checks the starting states against Init and steps without a refusal declaration against Next. For a step with a declared refusal, TLC requires the named action to be disabled for a refused attempt, and checks every normal return against that exact named action's before/after relation, including an unchanged state. For these declared steps, normal read-only returns are accepted outcomes; declared_refusals counts only matched exceptions, with their declared attributes retained below. TLC also checks that every step was tried, but where only the environment's bounds rule one out. **Tested in every state** needs those to pass and the states reached to equal the model's; a failed attempt check blocks it and **every step tried**, and keeps the checked transitions. A run one size larger, when the driver names its bounds, is recorded apart from the ratified bounds' results. None of this is a proof or an every-size claim. Runtime tests establish only their own observed outcomes. Gate and supplied component origins describe provenance; neither label grants a gate verdict.\n")
	} else if explored {
		// #342: preserve receipts for explorations without declarations.
		b.WriteString("\n### Python application · explored execution\n\nThe gate's harness explored the application at the ratified bounds: every state it reached was read back from the application, and every attempt was a bound application call. TLC checks the starting states against Init, every step against Next, and the attempts for every step tried, but where only the environment's bounds rule one out. **Tested in every state** needs those to pass and the states reached to equal the model's; a failed attempt check blocks it and **every step tried**, and keeps the checked transitions. A run one size larger, when the driver names its bounds, is recorded apart from the ratified bounds' results. None of this is a proof or an every-size claim. Runtime tests establish only their own observed outcomes. Gate and supplied component origins describe provenance; neither label grants a gate verdict.\n")
	} else {
		b.WriteString("\n### Python application · sampled execution\n\nThese gate executions test observed initial states against Init and observed transitions against Next at the ratified bounds. They establish no proof, exhaustive implementation coverage, every-step or every-size claim. Runtime tests establish only their own observed outcomes. Gate and supplied component origins describe provenance; neither label grants a gate verdict.\n")
	}
	if a.ModelError != "" {
		fmt.Fprintf(b, "\nModel tool error: %s\n", componentLiteral(a.ModelError))
	}
	for _, x := range a.Executions {
		fmt.Fprintf(b, "\nInvocation %s · %s; complete: %t; successful: %t; exit: %d.\n\n", componentLiteral(x.Key.ID), componentLiteral(x.Key.Kind), x.Complete, x.Success, x.ExitCode)
		// Render the complete record using the same literal rules as component
		// evidence, including keys, diagnostics and arbitrary observation values.
		raw, _ := json.MarshalIndent(x, "", "  ")
		fmt.Fprintf(b, "%s\n", componentLiteral(string(raw)))
	}
	for _, c := range a.Conformance {
		raw, _ := json.MarshalIndent(c, "", "  ")
		label := "TLC sampled check"
		if c.Mode == "bounded-exhaustive" {
			label = "TLC exploration check"
		}
		fmt.Fprintf(b, "\n%s: %s\n", label, componentLiteral(string(raw)))
	}
	for _, l := range a.Larger {
		raw, _ := json.MarshalIndent(l, "", "  ")
		fmt.Fprintf(b, "\nOne size larger: %s\n", componentLiteral(string(raw)))
	}
	if larger != nil {
		raw, _ := json.MarshalIndent(larger, "", "  ")
		fmt.Fprintf(b, "\nOne size larger, overall: %s\n", componentLiteral(string(raw)))
	}
	pythonApplicationProofs(b, a)
}

func pythonApplicationDeclaresRefusal(a *verify.PythonApplicationReport) bool {
	declares := func(x *verify.PythonApplicationExploration) bool {
		if x == nil {
			return false
		}
		if x.DeclaredRefusals != nil {
			return true
		}
		for _, step := range x.Steps {
			if step.Refusal != nil {
				return true
			}
		}
		return false
	}
	for _, x := range a.Executions {
		if declares(x.Exploration) {
			return true
		}
	}
	for _, x := range a.Larger {
		if x.Execution != nil && declares(x.Execution.Exploration) {
			return true
		}
	}
	return false
}

// pythonApplicationFailures names an explored check's failed claims, which
// its JSON alone would leave to the reader (#342).
func (render renderer) pythonApplicationFailures(a *verify.PythonApplicationReport) []string {
	var out []string
	for _, c := range a.Conformance {
		if c.Mode != "bounded-exhaustive" || !c.Result.Passed {
			continue
		}
		if int64(c.Result.States) != c.Result.ModelStates {
			out = append(out, fmt.Sprintf("Every state, %s: %s", render.literal(c.Key.ID), render.text(c.Result.Message)))
		}
		if t := c.Result.Tried; t != nil && !t.Passed {
			out = append(out, fmt.Sprintf("Every step tried, %s: %s. %s", render.literal(c.Key.ID), render.text(t.Message), render.triedEvidence(*t)))
		}
	}
	return out
}
