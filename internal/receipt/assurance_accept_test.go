package receipt_test

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

// Successful legacy reports retain every byte of their Markdown and their
// fingerprints, including Go and Python proofs with unverified explorer code.
// This compatibility slice corrects unsuccessful evidence; it does not assign
// component assurance to those unverified neighbors yet.
func TestReceiptsUseSuccessfulEvidenceAndPreserveLegacyOutput(t *testing.T) {
	goldens := []struct{ fingerprint, markdown string }{
		{"sha256:12362a3a9c164fea85ae6ea6ff4bf623d16ed3136adfbbdb211ea8161007453a", "fa2c20aa584f9ba051a6e8f89b97843086750f307ac6e09d0c1b646273381e15"},
		{"sha256:8d0e0ac4ecf82433ffb6f0971b45a14bafc96fa10407766f3bcae0b76dc50fda", "dd24cf0587a3a16d7f372d38f562402c2f3a6738b4587ee064184dd192d91728"},
		{"sha256:295f04994adf44460b659ba6ded42792ceab506707fae3516ef90e7fab2d475e", "040b3e9231b957ca2bd8e2ff20585fde0798b6b392d6ab9e750b51c5908e447d"},
	}
	for i, r := range receiptAssuranceFixtures() {
		t.Logf("case: %s", r.Project)
		r.Fingerprint = verify.Fingerprint(r)
		md := receipt.Markdown(&r)
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(md)))
		if r.Fingerprint != goldens[i].fingerprint || digest != goldens[i].markdown {
			t.Errorf("successful legacy output changed: fingerprint %s, Markdown SHA256 %s\n%s", r.Fingerprint, digest, md)
		}
	}
	for _, tc := range []struct {
		name      string
		code      *verify.Code
		agreement *verify.Agreement
		want      string
	}{
		{"missing proof", nil, nil, "unchecked"},
		{"failed proof", &verify.Code{Verifier: "Gobra", Functions: []string{"Step"}}, nil, "unchecked"},
		{"failed proof with agreement", &verify.Code{Verifier: "Gobra", Functions: []string{"Step"}}, &verify.Agreement{Passed: true}, "tested against the model"},
		{"empty successful proof", &verify.Code{Verifier: "Gobra", Passed: true}, nil, "unchecked"},
		{"empty proof with agreement", &verify.Code{Verifier: "Gobra", Passed: true}, &verify.Agreement{Passed: true}, "tested against the model"},
	} {
		t.Logf("case: %s", tc.name)
		// Stale serialized labels and an optimistic gate result cannot
		// replace a successful proof that actually names functions.
		r := verify.Report{Project: tc.name, Passed: true, Assurance: "proved",
			Code: tc.code, Agreement: tc.agreement, Bounds: map[string]string{"Limit": "2"},
			Larger: &verify.Larger{Passed: true, Bounds: map[string]string{"Limit": "3"}}}
		md := receipt.Markdown(&r)
		if want := "The code is **" + tc.want + "**."; !strings.Contains(md, want) {
			t.Errorf("receipt lacks evidence-derived claim %q:\n%s", want, md)
		}
		for _, overclaim := range []string{"at every size", "proved: 0 of 0", "The code is **proved"} {
			if strings.Contains(md, overclaim) {
				t.Errorf("receipt claims %q without a successful nonempty proof:\n%s", overclaim, md)
			}
		}
	}
	{
		t.Log("case: independent proof survives an overall gate failure")
		r := receiptAssuranceFixtures()[0]
		r.Passed, r.Build.Passed = false, false
		r.Assurance = "unchecked"
		md := receipt.Markdown(&r)
		if !r.EverySize() || !strings.Contains(md, "proved: 2 of 2 functions verified") ||
			!strings.Contains(md, "Gobra proves the code against its contracts at every size") {
			t.Errorf("an unrelated failure erased successful proof evidence:\n%s", md)
		}
	}
}

func receiptAssuranceFixtures() []verify.Report {
	var reports []verify.Report
	for _, language := range []string{"Go", "Python", "TypeScript"} {
		r := verify.Report{
			Project: "legacy " + language, Passed: true, Decision: "D-0024",
			Bounds: map[string]string{"Limit": "2"}, Assurance: "proved",
			Pins: []verify.Pin{{Name: "TypeOK", Kind: "invariant", Says: "The state remains valid.",
				Want: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Got:  "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Match: true}},
			Design:      verify.Design{Passed: true, Outcome: "passed", Spec: "Spec", Invariants: []string{"TypeOK"}, DistinctStates: 5, StatesGenerated: 9, Depth: 3},
			Witnesses:   []verify.Witness{{Name: "Full", Reached: true, Steps: 2}},
			Bugs:        []verify.Bug{{Name: "Overflow", Label: "overflow", Caught: true, Violated: "TypeOK", Steps: 3}},
			Build:       verify.Build{Passed: true, Steps: []verify.BuildStep{{Name: "tests", Passed: true}}},
			Larger:      &verify.Larger{Passed: true, Required: true, Bounds: map[string]string{"Limit": "3"}, States: 8, Depth: 4, WantStates: 8, WantDepth: 4},
			Toolchain:   verify.Toolchain{TLC: "2.19", TLCRelease: "v1.8.0", TLCJarSHA256: strings.Repeat("b", 64), JavaImage: "java@sha256:" + strings.Repeat("c", 64)},
			GeneratedAt: "2026-10-29T12:00:00Z",
		}
		if language == "Go" {
			r.Code = &verify.Code{Verifier: "Gobra", Passed: true, Functions: []string{"New", "Step"},
				Contracts: []string{"New", "Step"}, Unverified: []string{"Init", "Try", "Abstract"}, Overflow: true}
			r.Agreement = &verify.Agreement{Passed: true, States: 5, Depth: 3, WantStates: 5, WantDepth: 3}
		} else {
			r.Conformance = &conformance.Result{Passed: true, Exhaustive: true, Runs: 1, Steps: 12, States: 5, ModelStates: 5,
				Tried: &conformance.Tried{Passed: true, Attempts: 12, States: 5}}
			if language == "Python" {
				r.Code = &verify.Code{Verifier: "Nagini", Passed: true, Functions: []string{"Core.new", "Core.step"},
					Contracts: []string{"Core.new", "Core.step"}, Unverified: []string{"Explorer.successors"}}
			} else {
				r.Assurance = "tested against the model"
			}
		}
		reports = append(reports, r)
	}
	return reports
}
