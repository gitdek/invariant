package verify_test

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

// Even an optimistic run flag cannot override explicit skipped reasons.
func TestPythonApplicationSkippedProofCannotGainReceiptAssurance(t *testing.T) {
	r, calls := verify.PrerequisiteProofReport849(t, "contract")
	if calls != 0 || len(r.Application.Proofs) != 1 || len(r.Application.Proofs[0].Skipped) == 0 {
		t.Fatal("fixture must skip Nagini")
	}
	p := &r.Application.Proofs[0]
	p.Ran, p.Result.Passed = true, true
	p.Result.Verified = []string{p.Result.Target}
	for i := range r.Components.Proofs {
		row := &r.Components.Proofs[i]
		if row.Target == p.Result.Target {
			row.Status, row.Result = "proved", &p.Result
		}
	}
	md := receipt.Markdown(r)
	if !strings.Contains(md, "Component proofs: **0 proved;") || !strings.Contains(md, "skipped") || strings.Contains(md, "verified by Nagini") || strings.Contains(md, "Nagini ran from its pinned recipe") {
		t.Fatal("optimistic fields overrode the skipped proof")
	}
	if !strings.Contains(md, "**What failed**") || !strings.Contains(md, "keyword.iskeyword") {
		t.Fatal("the skip hid its failed prerequisite")
	}
}
