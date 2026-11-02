package receipt

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/verify"
)

func TestPythonApplicationLegacyExploredReceiptUnchanged(t *testing.T) {
	const original = "\n### Python application · explored execution\n\nThe gate's harness explored the application at the ratified bounds: every state it reached was read back from the application, and every attempt was a bound application call. TLC checks the starting states against Init, every step against Next, and the attempts for every step tried, but where only the environment's bounds rule one out. **Tested in every state** needs those to pass and the states reached to equal the model's; a failed attempt check blocks it and **every step tried**, and keeps the checked transitions. A run one size larger, when the driver names its bounds, is recorded apart from the ratified bounds' results. None of this is a proof or an every-size claim. Runtime tests establish only their own observed outcomes. Gate and supplied component origins describe provenance; neither label grants a gate verdict.\n"
	for _, exploration := range []*verify.PythonApplicationExploration{nil, {
		Steps: []verify.PythonApplicationExplorationStep{{Name: "Read", Args: [][]any{{}}}},
	}} {
		a := &verify.PythonApplicationReport{
			Conformance: []verify.PythonApplicationConformance{{Mode: "bounded-exhaustive"}},
			Executions:  []verify.PythonApplicationExecutionResult{{Exploration: exploration}},
			Larger: []verify.PythonApplicationLarger{{}, {
				Execution: &verify.PythonApplicationExecutionResult{Exploration: exploration},
			}},
		}
		var b strings.Builder
		pythonApplication(&b, a, nil)
		md := b.String()
		if !strings.HasPrefix(md, original) {
			t.Errorf("legacy explored explanation changed: %s", md)
		}
		if strings.Contains(md, "declared_refusals") || strings.Contains(md, "named action") {
			t.Errorf("legacy exploration gained declared-refusal wording: %s", md)
		}
	}
}

func TestPythonApplicationDeclaredReceiptWithZeroRefusals(t *testing.T) {
	zero := 0
	declared := &verify.PythonApplicationExploration{
		Steps: []verify.PythonApplicationExplorationStep{{
			Name:    "Read",
			Args:    [][]any{{}},
			Refusal: &verify.PythonApplicationRefusal{Type: "app.Refused", Attributes: map[string]any{}},
		}},
		DeclaredRefusals: &zero,
	}
	for _, tc := range []struct {
		name        string
		exploration *verify.PythonApplicationExploration
		larger      bool
	}{
		{"declaration with zero count", declared, false},
		{"declaration alone", &verify.PythonApplicationExploration{Steps: declared.Steps}, false},
		{"zero count alone", &verify.PythonApplicationExploration{DeclaredRefusals: &zero}, false},
		{"larger declaration with zero count", declared, true},
		{"larger declaration alone", &verify.PythonApplicationExploration{Steps: declared.Steps}, true},
		{"larger zero count alone", &verify.PythonApplicationExploration{DeclaredRefusals: &zero}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &verify.PythonApplicationReport{
				Conformance: []verify.PythonApplicationConformance{{Mode: "bounded-exhaustive"}},
				Executions:  []verify.PythonApplicationExecutionResult{{}},
			}
			x := verify.PythonApplicationExecutionResult{Exploration: tc.exploration}
			if tc.larger {
				a.Larger = []verify.PythonApplicationLarger{{}, {Execution: &x}}
			} else {
				a.Executions = append(a.Executions, x)
			}
			var b strings.Builder
			pythonApplication(&b, a, nil)
			md := b.String()
			for _, want := range []string{"named action to be disabled", "every normal return", "declared_refusals counts only matched exceptions"} {
				if !strings.Contains(md, want) {
					t.Errorf("declared explanation lacks %q: %s", want, md)
				}
			}
			if strings.Contains(md, "every step against Next") {
				t.Errorf("declared exploration kept legacy explanation: %s", md)
			}
		})
	}
}
