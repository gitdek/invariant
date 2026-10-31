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

func pythonApplication(b *strings.Builder, a *verify.PythonApplicationReport) {
	b.WriteString("\n### Python application · sampled execution\n\nThese gate executions test observed initial states against Init and observed transitions against Next at the ratified bounds. They establish no proof, exhaustive implementation coverage, every-step or every-size claim. Runtime tests establish only their own observed outcomes. Gate and supplied component origins describe provenance; neither label grants a gate verdict.\n")
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
		fmt.Fprintf(b, "\nTLC sampled check: %s\n", componentLiteral(string(raw)))
	}
}
