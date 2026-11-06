package verify_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/verify"
)

// This exercises verification's public receipt contract, using the existing
// executable TLC fake, its command-line checks and complete graph/replay data.
// Reader.Comparison carries the captured proposal comparison separately from
// Reader.Chart, which always describes freshly checked final-model inputs.
func TestComparison648VerificationValidatesCapturedEvidence(t *testing.T) {
	tc, _ := reader615Tools(t)
	dir := reader615Project(t, 2)
	old := reader615Run(t, dir, tc, true)
	p, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := statechart.ComparisonSnapshot{Kind: "ratification", Name: "Ratification receipt-fixture", Module: p.ModuleName(), Model: reader615Model, Includes: map[string]string{"Helper615.tla": reader615Include}, View: reader615View, Lock: p.Lock, Chart: old.Reader.Chart}
	view := strings.ReplaceAll(reader615View, "Working", "Proposed working")
	reader615Write(t, filepath.Join(dir, ".invariant/view.json"), view)
	proposed := reader615Run(t, dir, tc, true)
	b := statechart.ComparisonSnapshot{Kind: "proposal", Name: "Proposal " + project.ProposalHash(p.Lock.Bounds, p.Lock.Statements), Module: p.ModuleName(), Model: reader615Model, Includes: map[string]string{"Helper615.tla": reader615Include}, View: view, Lock: p.Lock, Chart: proposed.Reader.Chart}
	comparison, err := statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(dir, ".invariant/comparison.json")
	original := reader615JSON(t, comparison)
	reader615Write(t, name, string(original))
	finalModel := reader615Model + "\\* Final synthesized source, distinct from proposed source.\n"
	reader615Write(t, filepath.Join(dir, p.Manifest.Module), finalModel)
	r := reader615Run(t, dir, tc, true)
	if !r.Passed || r.Reader == nil || r.Reader.Chart == nil || r.Reader.Comparison == nil {
		t.Fatalf("fresh model verification lost its captured comparison: %+v", r)
	}
	if !reflect.DeepEqual(r.Reader.Comparison, comparison) || r.Reader.Inputs.Model != reader615Hash([]byte(finalModel)) || r.Reader.Comparison.CandidateIdentity.ModelSHA256 == r.Reader.Inputs.Model {
		t.Fatal("verification replaced proposal source provenance with final-model provenance")
	}
	if after, err := os.ReadFile(name); err != nil || string(after) != string(original) {
		t.Fatal("verification rewrote captured proposal evidence")
	}
	// A changed baseline with the original identities is damaged evidence.
	// Diagnose it, retain the independently computed final chart, and keep it
	// out of the model verdict, claim and gate verification fingerprint.
	var damaged statechart.Comparison
	if err := json.Unmarshal(original, &damaged); err != nil {
		t.Fatal(err)
	}
	damaged.Baseline.Model += "\\* substituted old source\n"
	reader615Write(t, name, string(reader615JSON(t, &damaged)))
	bad := reader615Run(t, dir, tc, true)
	if !bad.Passed || bad.Reader == nil || bad.Reader.Chart == nil || bad.Reader.Comparison != nil {
		t.Fatal("damaged comparison was accepted or displaced independent final-model evidence")
	}
	if !strings.Contains(strings.ToLower(string(reader615JSON(t, bad.Reader))), "comparison") {
		t.Fatal("damaged comparison disappeared without an explanatory diagnostic")
	}
	if verify.Fingerprint(*bad) != verify.Fingerprint(*r) || bad.Claim() != r.Claim() {
		t.Fatal("comparison damage changed model verification identity or assurance")
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	plain := reader615Run(t, dir, tc, true)
	if plain.Reader == nil || plain.Reader.Comparison != nil || !plain.Passed || verify.Fingerprint(*plain) != verify.Fingerprint(*r) || plain.Claim() != r.Claim() {
		t.Fatal("optional comparison altered ordinary verification")
	}
}
