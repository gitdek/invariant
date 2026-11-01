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
	if explored {
		// #342: the exploration's own words, and what each label needs.
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
