package factory

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/verify"
)

// Like checkedBufferReader, this is the formalizer double's saved-result
// contract. Production evidence is computed through the model gate.
func checkedForks(t *testing.T, forks []formalize.Fork, choices map[string]string) []formalize.Fork {
	t.Helper()
	hash := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("sha256:%x", sha256.Sum256(b))
	}
	b, err := json.Marshal(forks)
	if err != nil {
		t.Fatal(err)
	}
	var out []formalize.Fork
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	for i := range out {
		f := &out[i]
		for j := range f.Options {
			o := &f.Options[j]
			m := bufferProposal(t)
			scope := statechart.Preview{Fork: f.ID, Option: o.ID, Choices: choices}
			checkedBufferReader(t, m, scope)
			rr := &verify.Reader{Version: 1, Preview: scope, Chart: m.Reader.Chart}
			rr.Inputs = verify.ReaderInputs{Module: m.Module, Includes: map[string]string{}}
			sourceHash := func(text string) string { return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(text))) }
			rr.Inputs.Model, rr.Inputs.View = sourceHash(m.ModuleText), sourceHash(m.View)
			rr.Fingerprint = hash(struct {
				Reader verify.Reader `json:"reader"`
				Lock   project.Lock  `json:"lock"`
			}{*rr, project.Lock{Bounds: m.Bounds, Statements: m.Statements}})
			r := &verify.Report{Project: m.Name, ModelOnly: true, Passed: true, Proposal: m.Hash, Bounds: m.Bounds, Reader: rr,
				Design: verify.Design{Passed: true, Outcome: "passed", Spec: "Spec", DistinctStates: 7, StatesGenerated: 13}}
			for _, st := range m.Statements {
				r.Pins = append(r.Pins, verify.Pin{Name: st.Name, Kind: st.Kind, Says: st.Says, Want: st.SHA256, Got: st.SHA256, Match: true})
				if st.Kind == project.Invariant {
					r.Design.Invariants = append(r.Design.Invariants, st.Name)
				}
			}
			r.Fingerprint = verify.Fingerprint(*r)
			v := &formalize.Preview{Fork: f.ID, Option: o.ID, Choices: choices, Model: m, Receipt: r}
			o.Preview = v
			var err error
			if v.Fingerprint, err = f.PreviewFingerprint(*o); err != nil {
				t.Fatal(err)
			}
			if err := f.ValidatePreview(*o); err != nil {
				t.Fatal(err)
			}
		}
	}
	return out
}
