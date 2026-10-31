package receipt

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/verify"
)

// componentConditional says what a proved component's proof rests on: the
// trusted contracts its attempt assumed, each with its tested standing, which
// tests support and nothing proves (#359).
func componentConditional(result *verify.ComponentProofResult, standing map[string]verify.ComponentContractEvidence) string {
	parts := make([]string, len(result.Assumed))
	for i, use := range result.Assumed {
		support := "untested"
		if standing[use.Contract].Support == "tested" {
			support = "tested"
		}
		parts[i] = componentLiteral(use.Contract) + " (trusted · " + support + ")"
	}
	return "proved only on the condition that these trusted contracts hold: " + strings.Join(parts, ", ")
}

// componentProofFailures lists each selected in-place proof that falls short
// of an effective guarantee, and why (#359). It qualifies the receipt's own
// proof, contract and conformance results again first, so an edited status
// can't hide a failure.
func componentProofFailures(r *verify.Report) []string {
	if r.Application == nil || r.Components == nil || len(r.Application.Proofs) == 0 {
		return nil
	}
	e := *r.Components
	e.Proofs = slices.Clone(e.Proofs)
	for i := range e.Proofs {
		e.Proofs[i].Status = componentProofQualification(r, e.Proofs[i])
	}
	standing, _ := componentContractStanding(r)
	e.Contracts = nil
	for _, c := range e.Inventory.TrustedContracts {
		if row, ok := standing[c.ID]; ok {
			e.Contracts = append(e.Contracts, row)
		}
	}
	e.Conformance = slices.Clone(e.Conformance)
	for i := range e.Conformance {
		e.Conformance[i].Status = componentConformanceQualification(r, e.Conformance[i]).Status
	}
	var out []string
	for _, problem := range verify.PythonApplicationProofProblems(&e) {
		out = append(out, "In-place proof: "+componentText(problem))
	}
	return out
}

// pythonApplicationProofs renders each in-place proof attempt (#359): what
// it covers and rests on, how its verifier input maps to the application's
// original bytes, how it binds every name the function reads, and the record
// itself.
func pythonApplicationProofs(b *strings.Builder, a *verify.PythonApplicationReport) {
	if len(a.Proofs) == 0 {
		return
	}
	b.WriteString("\n### Python application · in-place proof\n\nNagini checked each selected function's own body against its own contract, from the function's original bytes in its module. Every other name the function reads is bound to Nagini's own contract functions, a builtin Nagini models, a literal constant copied as it is, or a trusted contract's abstraction, which Nagini assumes. A proof holds only on the condition that those trusted contracts hold: their backing tests support them, and nothing proves them. It covers no dependency, no unselected function, its neighbors in the same module included, and not the whole application. Nagini ran from its pinned recipe in its own image, with none of the application's requirements installed.\n")
	for _, p := range a.Proofs {
		verdict := "not proved"
		switch {
		case !p.Ran:
			verdict = "not run, since no trusted contract binds something the function reaches"
		case p.Result.Passed && slices.Contains(p.Result.Verified, p.Result.Target):
			verdict = "verified by Nagini"
		}
		fmt.Fprintf(b, "\nIn-place proof of %s: %s. Nagini %s, recipe %s, linux/amd64; verifier input %s.\n",
			componentLiteral(p.Result.Target), verdict, componentLiteral(verify.NaginiVersion), componentLiteral(short(p.Recipe)), componentLiteral(short(p.Input.Fingerprint)))
		b.WriteString("\n| Verifier input | Lines | From the application |\n| :-- | :-- | :-- |\n")
		for _, s := range p.Input.Spans {
			from := "written by the gate"
			if s.Source != "" {
				from = fmt.Sprintf("%s lines %d–%d · %s", componentLiteral(s.Source), s.SourceLine, s.SourceEndLine, componentLiteral(short(s.SourceSHA256)))
			}
			fmt.Fprintf(b, "| %s · %s | %d–%d | %s |\n", componentLiteral(s.File), componentLiteral(s.Kind), s.Line, s.EndLine, from)
		}
		var bound []string
		for _, x := range p.Input.Boundaries {
			bound = append(bound, componentLiteral(x.Name)+" → "+pythonApplicationProofBinding(x))
		}
		if len(bound) > 0 {
			fmt.Fprintf(b, "\nNames the function reads: %s.\n", strings.Join(bound, "; "))
		}
		if len(p.Input.Excluded) > 0 {
			fmt.Fprintf(b, "\nLeft out of the verifier input: %s.\n", componentStrings(p.Input.Excluded))
		}
		raw, _ := json.MarshalIndent(p, "", "  ")
		fmt.Fprintf(b, "\nAttempt record: %s\n", componentLiteral(string(raw)))
	}
}

// pythonApplicationProofBinding says how a verifier input binds one name.
func pythonApplicationProofBinding(x verify.PythonApplicationProofBoundary) string {
	switch x.Kind {
	case "nagini":
		return "Nagini's own contract function"
	case "native":
		return "a builtin Nagini models"
	case "typing":
		return "a typing name"
	case "constant":
		return "a literal constant, copied"
	case "contract":
		return "trusted contract " + componentLiteral(x.Contract)
	case "missing":
		return "no trusted contract binds it"
	}
	return componentLiteral(x.Kind)
}
