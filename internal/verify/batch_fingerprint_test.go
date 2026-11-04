package verify

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/tlc"
)

// A conformance check's batch is scheduling, not evidence (#572). ask3d's
// receipt changed fingerprint when #565 renamed its starting-state batch from
// "." to "init-1", with every count, verdict and state the same.
func batchReport(batch string, states int, outcome tlc.Outcome) Report {
	return Report{
		Project: "lifecycle",
		Passed:  true,
		Application: &PythonApplicationReport{Conformance: []PythonApplicationConformance{{
			Mode:   "bounded-exhaustive",
			Result: conformance.Result{Passed: true, Runs: 15, Steps: 14, States: states, ModelStates: 5, Transitions: 4},
			Checks: []PythonApplicationModelCheck{{
				Module:      "Invariant_ConformanceInit",
				Batch:       batch,
				InputSHA256: "sha256:00",
				Config:      tlc.Config{Specification: "Invariant_ISpec", Invariants: []string{"Init"}},
				Result:      tlc.Result{Outcome: outcome},
			}},
		}}},
	}
}

func TestFingerprintLeavesOutConformanceBatches(t *testing.T) {
	before := Fingerprint(batchReport(".", 5, tlc.Passed))
	if after := Fingerprint(batchReport("init-1", 5, tlc.Passed)); after != before {
		t.Errorf("renaming a batch changed the fingerprint: %s, then %s", before, after)
	}
	if other := Fingerprint(batchReport(".", 4, tlc.Passed)); other == before {
		t.Error("a different state count kept the fingerprint")
	}
	if other := Fingerprint(batchReport(".", 5, tlc.Violated)); other == before {
		t.Error("a different check outcome kept the fingerprint")
	}
}

func TestReportKeepsConformanceBatches(t *testing.T) {
	r := batchReport("init-1", 5, tlc.Passed)
	r.Fingerprint = Fingerprint(r)
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"batch":"init-1"`) {
		t.Errorf("the report lost the check's batch: %s", b)
	}
	if r.Application.Conformance[0].Checks[0].Batch != "init-1" {
		t.Error("fingerprinting changed the report's own batch")
	}
}
