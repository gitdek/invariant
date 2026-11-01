package receipt

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/project"
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

// componentInPlace is a gate application report's component evidence with
// its proof, contract and conformance results qualified again by the
// receipt, so an edited status can't pass or hide a shortfall, or nil for a
// report that selects no in-place proof (#359).
func componentInPlace(r *verify.Report) *verify.ComponentEvidence {
	if r.Application == nil || r.Components == nil || !slices.ContainsFunc(r.Components.Inventory.Checks, func(c project.ResolvedComponentCheck) bool { return c.Proof != nil }) {
		return nil
	}
	e := *r.Components
	e.Proofs = slices.Clone(e.Proofs)
	for i, row := range e.Proofs {
		e.Proofs[i].Status = componentProofQualification(r, row)
		if row.Result != nil && !componentInPlaceRecorded(r, row.Result) {
			// Nagini may have verified it; what's missing is the attempt.
			result := *row.Result
			result.Errors = []string{"the receipt doesn't record the attempt its result names, so nothing here shows what Nagini read"}
			e.Proofs[i].Result = &result
		}
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
	return &e
}

// componentUnqualified says which proved components' in-place proofs fall
// short of an effective guarantee, which the receipt doesn't count as proved.
func componentUnqualified(r *verify.Report) map[string]bool {
	e := componentInPlace(r)
	if e == nil {
		return nil
	}
	out := map[string]bool{}
	for _, row := range e.Proofs {
		if row.Status == "proved" && len(verify.PythonApplicationTargetProblems(e, row.Target)) > 0 {
			out[row.Target] = true
		}
	}
	return out
}

// componentProofFailures lists each selected in-place proof that falls short
// of an effective guarantee, and why (#359).
func componentProofFailures(r *verify.Report) []string {
	e := componentInPlace(r)
	if e == nil {
		return nil
	}
	var out []string
	for _, problem := range verify.PythonApplicationProofProblems(e) {
		out = append(out, "In-place proof: "+componentText(problem))
	}
	return out
}

// componentInPlaceRecorded says whether a result that names an in-place
// verifier input, by the pin only the gate's in-place proof sets, has that
// attempt in the report's application proofs. One that doesn't proves
// nothing, since nothing in the receipt shows what Nagini read (#359).
func componentInPlaceRecorded(r *verify.Report, result *verify.ComponentProofResult) bool {
	if result.Tool == nil || result.Tool.Pins[verify.InPlaceInputPin] == "" {
		return true
	}
	if r.Application == nil {
		return false
	}
	return slices.ContainsFunc(r.Application.Proofs, func(p verify.PythonApplicationProof) bool {
		return p.Result.Target == result.Target && p.Input.Fingerprint == result.Tool.Pins[verify.InPlaceInputPin]
	})
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
		case p.Result.Passed && slices.Contains(p.Result.Verified, p.Result.Target) && len(p.Result.Unaccounted) > 0:
			verdict = "verified by Nagini only under an assumption nothing accounts for, so it proves nothing"
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
