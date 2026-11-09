package dashboard

import (
	"regexp"
	"testing"

	"github.com/gitdek/invariant/internal/verify"
)

// A receipt with explicit components says what each one earned, and the page
// says what the code earned in the words it uses for every project: never
// "component evidence" or "unchecked" for code that ran against the model
// (#780).
func TestComponentEvidenceSaysWhatTheCodeEarned780(t *testing.T) {
	ran := func(exhaustive bool, states int64) verify.ComponentConformanceEvidence {
		return verify.ComponentConformanceEvidence{Target: "t", TransitionsChecked: true, ExhaustiveStates: exhaustive, EveryStep: exhaustive,
			Result: verify.ComponentConformanceResult{Outcome: "passed", States: states, ModelStates: 5}}
	}
	unchecked := verify.ComponentProof{Target: "t", Status: "unchecked"}
	for _, c := range []struct {
		name      string
		evidence  verify.ComponentEvidence
		assurance string
		verified  int
		visited   int64
	}{
		{"every state", verify.ComponentEvidence{Proofs: []verify.ComponentProof{unchecked}, Conformance: []verify.ComponentConformanceEvidence{ran(true, 5)}}, "tested in every state", 0, 5},
		{"sampled", verify.ComponentEvidence{Proofs: []verify.ComponentProof{unchecked}, Conformance: []verify.ComponentConformanceEvidence{ran(false, 3)}}, "tested", 0, 3},
		{"one sampled", verify.ComponentEvidence{Conformance: []verify.ComponentConformanceEvidence{ran(true, 5), ran(false, 3)}}, "tested", 0, 5},
		{"proved", verify.ComponentEvidence{Proofs: []verify.ComponentProof{{Target: "t", Status: "proved"}, unchecked}, Conformance: []verify.ComponentConformanceEvidence{ran(true, 5)}}, "proved", 1, 0},
		{"failed", verify.ComponentEvidence{Conformance: []verify.ComponentConformanceEvidence{{Target: "t", TransitionsChecked: true, Result: verify.ComponentConformanceResult{Outcome: "failed", States: 2}}}}, "unchecked", 0, 0},
		{"nothing", verify.ComponentEvidence{Proofs: []verify.ComponentProof{unchecked}}, "unchecked", 0, 0},
	} {
		c.evidence.Inventory.Mode = "explicit"
		p := projectOf(projectSource{}, &verify.Report{Project: "Statue generation", Passed: true, Components: &c.evidence})
		if p.Assurance != c.assurance || p.Verified != c.verified || p.Visited != c.visited {
			t.Errorf("%s: the page says %q, %d proved, %d states reached; want %q, %d, %d", c.name, p.Assurance, p.Verified, p.Visited, c.assurance, c.verified, c.visited)
		}
	}
}

// The page keeps the scrollbar's room whether or not it shows, so a short
// view can't bring the scrollbar back and forth without end (#781).
func TestThePageKeepsTheScrollbarsRoom781(t *testing.T) {
	if !regexp.MustCompile(`(?m)^html \{[^}]*scrollbar-gutter: stable;`).Match(assets["/style.css"]) {
		t.Error("style.css doesn't keep the scrollbar's room on the root: html needs scrollbar-gutter: stable")
	}
}
