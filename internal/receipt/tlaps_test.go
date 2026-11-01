package receipt

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/verify"
)

// TLAPS's evidence is a row of its own, about the model alone, and changes
// nothing the receipt says of the code (#360).
func TestTLAPSRowStandsApart(t *testing.T) {
	r := &verify.Report{
		Project:   "pool",
		Passed:    true,
		Bounds:    map[string]string{"Size": "2"},
		Design:    verify.Design{Passed: true, Outcome: "passed", DistinctStates: 13, StatesGenerated: 55, Depth: 3},
		Assurance: "tested against the model",
		Agreement: &verify.Agreement{Passed: true, States: 13, Depth: 3, WantStates: 13, WantDepth: 3},
		TLAPS: []verify.TLAPSProof{{Invariant: "NoSharedConnection", Proved: true, Obligations: 8,
			Provers: map[string]int{"smt": 3, "ls4": 1, "trivial": 4}, Cites: []string{"FiniteSetTheorems FS_CardinalityType"}}},
		Toolchain: verify.Toolchain{TLAPS: &verify.TLAPSTool{Release: "1.6.0-pre", Version: "4744c0f", Tarball: "sha256:e3e0732108a409f3",
			Recipe: "sha256:eedae1cf454c0000", Backends: map[string]string{"z3": "4.8.9 - 64 bit", "zenon": "0.8.4", "isabelle": "Isabelle2025", "ls4": "unknown"}}},
	}
	md := Markdown(r)
	for _, want := range []string{
		"| Design · TLC | ✅",
		"| Design · TLAPS | ✅ model invariant proved at every size (TLAPS) | `NoSharedConnection`: 8 obligations (1 ls4, 3 smt, 4 trivial), citing `FiniteSetTheorems!FS_CardinalityType`, which TLAPS's library states · tlapm `4744c0f` |",
		"Checked within `Size = 2`. Within these bounds TLC's search is exhaustive. TLAPS proves `NoSharedConnection` of the model at every size, under its own trust model; that says nothing of the code. Nothing else is claimed outside them.",
		"| TLAPS | tlapm 1.6.0-pre (`4744c0f`), tarball `sha256:e3e0732108a4`, recipe `sha256:eedae1cf454c` · z3 4.8.9 - 64 bit, zenon 0.8.4, isabelle Isabelle2025, ls4 unknown |",
		"The code is **tested against the model**.",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("receipt lacks %q:\n%s", want, md)
		}
	}
	if strings.Index(md, "| Design · TLAPS |") < strings.Index(md, "| Design · TLC |") {
		t.Error("the TLAPS row comes before TLC's")
	}

	// An invariant TLAPS didn't prove fails its row and says why.
	r.Passed = false
	r.TLAPS = []verify.TLAPSProof{{Invariant: "NoSharedConnection", Message: "obligation 2 failed"}}
	md = Markdown(r)
	for _, want := range []string{
		"| Design · TLAPS | ❌ 0 of 1 model invariants proved at every size (TLAPS) | `NoSharedConnection` not proved · tlapm `4744c0f` |",
		"- TLAPS didn't prove `NoSharedConnection` at every size: obligation 2 failed",
		"Nothing is claimed outside them.",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("receipt lacks %q:\n%s", want, md)
		}
	}

	// A project that selects none shows no row, as before.
	r.TLAPS, r.Toolchain.TLAPS = nil, nil
	if md := Markdown(r); strings.Contains(md, "TLAPS") {
		t.Errorf("a receipt without TLAPS mentions it:\n%s", md)
	}
}
