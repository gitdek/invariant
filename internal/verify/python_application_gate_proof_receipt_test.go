package verify_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

// #359: the receipt shows an in-place proof as what it is: the selected
// function's own body proved against its own contract, on the condition
// that the trusted contracts it assumed hold, which tests support and
// nothing proves, with tested behavior and unchecked components apart.
func TestInPlaceProofReceiptsSayWhatIsProvedAndWhatIsTested(t *testing.T) {
	r := verify.ProvedCounterReport(t, nil)
	if !r.Passed {
		t.Fatalf("the fixture must pass: %+v", r)
	}
	md := receipt.Markdown(r)
	for _, want := range []string{
		"### Python application · in-place proof",
		"verified by Nagini",
		"proved only on the condition that these trusted contracts hold: <code>keyword.iskeyword</code> (trusted · tested)",
		"It covers no dependency, no unselected function, its neighbors in the same module included, and not the whole application.",
		"Component proofs: **1 proved;",
		"<code>STEP</code> → a literal constant, copied",
		"<code>iskeyword</code> → trusted contract <code>keyword.iskeyword</code>",
		"Left out of the verifier input:",
		"| Nagini sandbox | recipe ",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("the receipt lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "**What failed**") {
		t.Errorf("a passing in-place proof lists failures:\n%s", md)
	}

	// A failed required contract leaves the proof without its condition,
	// and the receipt says so.
	failed := verify.ProvedCounterReport(t, func(root string) {
		name := filepath.Join(root, "tests", "test_contracts.py")
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		edited := strings.Replace(string(b), "self.assertTrue(iskeyword('if'))", "self.assertTrue(iskeyword('counter'))", 1)
		if err := os.WriteFile(name, []byte(edited), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	md = receipt.Markdown(failed)
	if failed.Passed || !strings.Contains(md, "In-place proof: ") || !strings.Contains(md, "keyword.iskeyword, which isn") {
		t.Errorf("a failed required contract isn't named among the failures:\n%s", md)
	}
	// Nagini verified it, and it isn't counted as proved (#359's review).
	if !strings.Contains(md, "Component proofs: **0 proved; 1 not qualified;") || !strings.Contains(md, "| not qualified |") {
		t.Errorf("a proof without its condition is counted as proved:\n%s", md)
	}

	// A receipt whose in-place attempts are gone can't show the proof.
	stripped := *r
	application := *r.Application
	application.Proofs = nil
	stripped.Application = &application
	if md := receipt.Markdown(&stripped); !strings.Contains(md, "Component proofs: **0 proved;") {
		t.Errorf("a proof whose attempt the receipt lacks still counts:\n%s", md)
	}
}
