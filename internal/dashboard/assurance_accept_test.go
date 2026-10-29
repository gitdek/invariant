package dashboard

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

// The dashboard derives assurance from successful evidence, including when it
// reads an older report with a stale assurance string. A nonempty successful
// proof establishes the legacy proved claim. Otherwise successful agreement
// or conformance establishes tested; only successful exhaustive conformance
// with a positive matching state count and no failed attempt check establishes
// tested in every state. A failed attempt check still leaves the successful
// bounded conformance evidence. Build success alone establishes none of these.
//
// Evidence remains independent of the overall gate result. This slice retains
// successful legacy proof claims and the list of unverified neighbors; assigning
// separate assurance to those neighbors remains component-evidence work.
func TestDashboardAssuranceRequiresSuccessfulEvidence(t *testing.T) {
	cases := []struct {
		name         string
		language     string
		report       verify.Report
		want         string
		wantVerified int
	}{
		{
			name: "stale proof label without evidence", language: "go",
			report: verify.Report{Passed: true, Assurance: "proved"},
			want:   "unchecked",
		},
		{
			name: "build and model success alone", language: "typescript",
			report: verify.Report{Passed: true, Assurance: "tested against the model",
				Build: verify.Build{Passed: true}, Design: verify.Design{Passed: true}},
			want: "unchecked",
		},
		{
			name: "failed proof still lists source functions", language: "go",
			report: verify.Report{Assurance: "proved",
				Code: &verify.Code{Verifier: "Gobra", Functions: []string{"Step"}, Contracts: []string{"Step"}}},
			want: "unchecked",
		},
		{
			name: "successful proof without functions", language: "python",
			report: verify.Report{Assurance: "proved",
				Code: &verify.Code{Verifier: "Nagini", Passed: true, Contracts: []string{"Step"}}},
			want: "unchecked",
		},
		{
			name: "legacy Go proof keeps unverified explorer", language: "go",
			report: verify.Report{Passed: true, Assurance: "unchecked",
				Code: &verify.Code{Verifier: "Gobra", Passed: true, Functions: []string{"New", "Step"},
					Contracts: []string{"New", "Step"}, Unverified: []string{"Init", "Try", "Abstract"}}},
			want: "proved", wantVerified: 2,
		},
		{
			name: "legacy Python proof overrides stale weaker label", language: "python",
			report: verify.Report{Passed: true, Assurance: "tested against the model",
				Code: &verify.Code{Verifier: "Nagini", Passed: true, Functions: []string{"Core.step"},
					Unverified: []string{"Explorer.successors"}}},
			want: "proved", wantVerified: 1,
		},
		{
			name: "overall failure preserves successful proof", language: "go",
			report: verify.Report{Passed: false,
				Code:        &verify.Code{Verifier: "Gobra", Passed: true, Functions: []string{"Step"}},
				Agreement:   &verify.Agreement{Passed: false},
				Conformance: &conformance.Result{Passed: false}},
			want: "proved", wantVerified: 1,
		},
		{
			name: "successful agreement without proof", language: "go",
			report: verify.Report{Assurance: "proved", Agreement: &verify.Agreement{Passed: true}},
			want:   "tested",
		},
		{
			name: "failed agreement", language: "go",
			report: verify.Report{Assurance: "tested against the model", Agreement: &verify.Agreement{Passed: false}},
			want:   "unchecked",
		},
		{
			name: "failed proof with successful agreement", language: "go",
			report: verify.Report{Assurance: "proved",
				Code:      &verify.Code{Verifier: "Gobra", Functions: []string{"Step"}},
				Agreement: &verify.Agreement{Passed: true}},
			want: "tested",
		},
		{
			name: "empty proof with successful conformance", language: "python",
			report: verify.Report{Assurance: "proved", Code: &verify.Code{Verifier: "Nagini", Passed: true},
				Conformance: &conformance.Result{Passed: true, States: 2, ModelStates: 5}},
			want: "tested",
		},
		{
			name: "sampled conformance", language: "typescript",
			report: verify.Report{Passed: true, Assurance: "unchecked",
				Conformance: &conformance.Result{Passed: true, States: 5, ModelStates: 5}},
			want: "tested",
		},
		{
			name: "failed conformance with matching exhaustive counts", language: "typescript",
			report: verify.Report{Assurance: "proved",
				Conformance: &conformance.Result{Passed: false, Exhaustive: true, States: 5, ModelStates: 5}},
			want: "unchecked",
		},
		{
			name: "legacy exhaustive conformance without attempts", language: "typescript",
			report: verify.Report{Passed: true, Assurance: "tested against the model",
				Conformance: &conformance.Result{Passed: true, Exhaustive: true, States: 5, ModelStates: 5}},
			want: "tested in every state",
		},
		{
			name: "legacy exhaustive conformance with successful attempts", language: "python",
			report: verify.Report{Passed: true, Assurance: "unchecked",
				Conformance: &conformance.Result{Passed: true, Exhaustive: true, States: 5, ModelStates: 5,
					Tried: &conformance.Tried{Passed: true}}},
			want: "tested in every state",
		},
		{
			name: "failed attempts preserve bounded conformance", language: "typescript",
			report: verify.Report{Assurance: "proved",
				Conformance: &conformance.Result{Passed: true, Exhaustive: true, States: 5, ModelStates: 5,
					Tried: &conformance.Tried{Passed: false}}},
			want: "tested",
		},
		{
			name: "empty exhaustive counts", language: "typescript",
			report: verify.Report{Conformance: &conformance.Result{Passed: true, Exhaustive: true}},
			want:   "tested",
		},
		{
			name: "negative matching exhaustive counts", language: "typescript",
			report: verify.Report{Conformance: &conformance.Result{Passed: true, Exhaustive: true, States: -1, ModelStates: -1}},
			want:   "tested",
		},
		{
			name: "unequal exhaustive counts", language: "typescript",
			report: verify.Report{Conformance: &conformance.Result{Passed: true, Exhaustive: true, States: 4, ModelStates: 5}},
			want:   "tested",
		},
		{
			name: "failed conformance does not erase agreement", language: "go",
			report: verify.Report{Assurance: "unchecked", Agreement: &verify.Agreement{Passed: true},
				Conformance: &conformance.Result{Passed: false, Exhaustive: true, States: 5, ModelStates: 5,
					Tried: &conformance.Tried{Passed: false}}},
			want: "tested",
		},
		{
			name: "failed agreement does not erase conformance", language: "go",
			report: verify.Report{Assurance: "unchecked", Agreement: &verify.Agreement{Passed: false},
				Conformance: &conformance.Result{Passed: true, States: 3, ModelStates: 5}},
			want: "tested",
		},
		{
			name: "overall failure preserves exhaustive evidence", language: "typescript",
			report: verify.Report{Passed: false, Assurance: "unchecked",
				Conformance: &conformance.Result{Passed: true, Exhaustive: true, States: 5, ModelStates: 5,
					Tried: &conformance.Tried{Passed: true}}},
			want: "tested in every state",
		},
	}
	for _, tc := range cases {
		t.Logf("case: %s", tc.name)
		r := tc.report
		r.Project = "legacy component"
		r.Fingerprint = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		before, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		ps := projectSource{dir: "example/core", key: "model-identity", manifest: project.Manifest{Language: tc.language}}
		encoded, err := json.Marshal(projectOf(ps, &r))
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Assurance   string   `json:"assurance"`
			Passed      bool     `json:"passed"`
			Verified    int      `json:"verified"`
			Unverified  []string `json:"unverified"`
			Fingerprint string   `json:"fingerprint"`
		}
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatal(err)
		}
		if got.Assurance != tc.want || got.Verified != tc.wantVerified {
			t.Errorf("dashboard assurance = %q, verified = %d; want %q, %d; JSON: %s",
				got.Assurance, got.Verified, tc.want, tc.wantVerified, encoded)
		}
		if got.Passed != r.Passed || got.Fingerprint != r.Fingerprint {
			t.Errorf("dashboard changed the gate result or fingerprint: %s", encoded)
		}
		var unverified []string
		if r.Code != nil {
			unverified = r.Code.Unverified
		}
		if !reflect.DeepEqual(got.Unverified, unverified) {
			t.Errorf("dashboard lost unverified neighbors: got %v, want %v", got.Unverified, unverified)
		}
		after, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Errorf("projectOf mutated the verification report:\nbefore: %s\nafter: %s", before, after)
		}
	}
}
