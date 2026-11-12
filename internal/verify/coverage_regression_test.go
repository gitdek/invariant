package verify_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/verify"
)

func TestCoverageRejectsIncompleteModelExploration802(t *testing.T) {
	for _, tc := range []struct {
		name    string
		design  verify.Design
		unknown bool
	}{
		{"violated", verify.Design{Outcome: "violated"}, true},
		{"deadlock", verify.Design{Outcome: "deadlock"}, true},
		{"property violated", verify.Design{Outcome: "property violated"}, true},
		{"error", verify.Design{Outcome: "error"}, true},
		{"unknown outcome", verify.Design{Outcome: "interrupted"}, true},
		{"stale passed flag", verify.Design{Passed: true, Outcome: "violated"}, true},
		{"contradictory passed outcome", verify.Design{Outcome: "passed"}, true},
		{"completed", verify.Design{Passed: true, Outcome: "passed"}, false},
		{"legacy missing outcome", verify.Design{}, false},
	} {
		for _, source := range []string{"conformance", "python_application"} {
			for _, mode := range []string{"sampled", "bounded-exhaustive"} {
				t.Run(tc.name+"/"+source+"/"+mode, func(t *testing.T) {
					r := verify.Report{Design: tc.design, Bounds: map[string]string{"Limit": "4"}}
					r.Design.DistinctStates = 3
					result := conformance.Result{Passed: true, States: 3, ModelStates: 3, Steps: 5,
						Exhaustive: mode == "bounded-exhaustive", Tried: &conformance.Tried{Passed: true, States: 3, Attempts: 5}}
					if source == "conformance" {
						r.Conformance = &result
					} else {
						r.Application = &verify.PythonApplicationReport{Conformance: []verify.PythonApplicationConformance{{
							Key: verify.PythonApplicationInvocationKey{Target: "app", Check: "drive", ID: "one"},
							Bounds: r.Bounds, Mode: mode, Result: result,
						}}}
					}
					before, err := r.EvidenceJSON()
					if err != nil {
						t.Fatal(err)
					}
					coverage := r.Coverage()
					if len(coverage) != 1 {
						t.Fatalf("coverage rows: %+v", coverage)
					}
					c := coverage[0]
					if !c.Checked || c.States != 3 || c.ModelStates != 3 || c.Source != source || c.Mode != mode {
						t.Fatalf("successful conformance evidence changed: %+v", c)
					}
					if tc.unknown {
						if c.Status != "unknown" || !c.Sampled || c.Fraction != "" || c.Percent != "" || c.Detail() != "coverage unknown" || c.Qualification() != "tested (sampled)" {
							t.Errorf("incomplete model exploration supplied coverage: %+v", c)
						}
					} else if c.Status != "full" || c.Sampled || c.Fraction != "3/3" || c.Percent != "100%" {
						t.Errorf("valid counts lost coverage: %+v", c)
					}
					raw, err := json.Marshal(r)
					if err != nil {
						t.Fatal(err)
					}
					var view struct {
						Coverage []verify.Coverage `json:"coverage"`
					}
					if err := json.Unmarshal(raw, &view); err != nil {
						t.Fatal(err)
					}
					if len(view.Coverage) != 1 || view.Coverage[0].Status != c.Status || view.Coverage[0].Percent != c.Percent || view.Coverage[0].Checked != c.Checked || view.Coverage[0].Sampled != c.Sampled {
						t.Fatalf("JSON coverage differs: %s", raw)
					}
					var round verify.Report
					if err := json.Unmarshal(raw, &round); err != nil {
						t.Fatal(err)
					}
					after, err := round.EvidenceJSON()
					if err != nil || !bytes.Equal(before, after) || verify.Fingerprint(r) != verify.Fingerprint(round) || round.Passed {
						t.Fatal("coverage changed checked evidence, fingerprint or gate failure")
					}
				})
			}
		}
	}
}

func TestCoverageTruncationAndPythonBounds802(t *testing.T) {
	for _, tc := range []struct {
		states, total int64
		percent       string
	}{
		{1, 8, "12.5%"}, {1, 3, "33.33%"}, {1, 1000000, "0%"},
		{922337203685477580, 9223372036854775807, "9.99%"},
	} {
		r := verify.Report{Conformance: &conformance.Result{Passed: true, States: int(tc.states), ModelStates: tc.total}}
		if c := r.ConformanceCoverage(); c.Percent != tc.percent || !c.Sampled || c.Status != "partial" {
			t.Errorf("%d/%d: %+v, want %s", tc.states, tc.total, c, tc.percent)
		}
	}
	r := verify.Report{Bounds: map[string]string{"Limit": "4"}}
	c := verify.PythonApplicationConformance{Key: verify.PythonApplicationInvocationKey{Target: "app", Check: "drive", ID: "one"},
		Bounds: map[string]string{"Limit": "5"}, Result: conformance.Result{Passed: true, States: 2, ModelStates: 5}}
	if got := r.PythonCoverage(c); got.Status != "unknown" || !got.Sampled || got.Percent != "" {
		t.Fatalf("other bounds supplied a share: %+v", got)
	}
	c.Bounds = r.Bounds
	c.OperationalError = true
	if got := r.PythonCoverage(c); got.Status != "unknown" || got.Checked || got.Sampled || got.Percent != "" {
		t.Fatalf("operational failure supplied a share: %+v", got)
	}
	c.OperationalError = false
	r.Application = &verify.PythonApplicationReport{Conformance: []verify.PythonApplicationConformance{c}}
	before := verify.Fingerprint(r)
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var round verify.Report
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatal(err)
	}
	if verify.Fingerprint(round) != before || round.Coverage()[0].Percent != "40%" {
		t.Fatal("coverage JSON changed evidence or lost its exact share")
	}
}
