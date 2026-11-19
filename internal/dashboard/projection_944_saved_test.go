package dashboard

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/statechart"
)

func TestProjection944SavedVolumeRecovery(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	include := strings.Replace(reader603Include, "====", "\\* "+strings.Repeat("x", 32<<20)+"\n====", 1)
	reader603Write(t, filepath.Join(ws, "Helper603.tla"), include)
	base, _, err := formalize.Check(t.Context(), ws, tc)
	if err != nil {
		t.Fatal(err)
	}
	authority := (formalize.Request{Previous: base}).CheckContext()
	reader603Write(t, filepath.Join(ws, "view.json"), strings.ReplaceAll(reader603View, "Working", "Revised"))
	p, report, err := formalize.CheckWithContext(t.Context(), ws, tc, authority)
	if err != nil {
		t.Fatal(err)
	}
	p.Baseline = authority.Baseline
	result := &formalize.Result{Proposal: p, Report: report, CheckContext: &authority}
	f := saved897FixtureFor(t, result)
	if len(f.payload) > statechart.MaxEvidenceBytes || len(f.companions) == 0 {
		t.Fatal("saved result did not use bounded companions")
	}
	var wire formalize.Result
	if err := json.Unmarshal(f.payload, &wire); err != nil || wire.Proposal.Reader.ComparisonRef == nil {
		t.Fatal("missing saved volume reference", err)
	}
	f.s.prepare()
	f.refresh(t)
	w := saved897Healthy(t, f.s, f.ref, result)
	saved897Evidence(t, f.s, w.SavedDraft, f.payload)
	cold := f.restart(t, saved897Author)
	saved897Evidence(t, cold, w.SavedDraft, f.payload)
	// A companion's exact bytes, not a newly calculated seal, decide recovery.
	for _, mode := range []string{"missing", "changed"} {
		t.Run(mode, func(t *testing.T) {
			var decoded formalize.Result
			if err := json.Unmarshal(f.payload, &decoded); err != nil {
				t.Fatal(err)
			}
			err := decoded.ResolveEvidence(func(name string) ([]byte, error) {
				b := f.companions[name]
				if mode == "missing" {
					return nil, nil
				}
				return append(append([]byte{}, b...), ' '), nil
			})
			if err == nil {
				t.Fatal("damaged saved companion was accepted")
			}
		})
	}
}

func TestProjection944SavedQuestionRecovery(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	draft := preview639Draft(t, 1, nil)
	include := strings.Replace(reader603Include, "====", "\\* "+strings.Repeat("x", 32<<20)+"\n====", 1)
	for j := range 2 {
		preview639Input(draft, 0, j)["model"].(map[string]any)["includes"] = map[string]string{"Helper603.tla": include}
	}
	preview639Write(t, ws, draft)
	p := preview639Checked(t, ws, tc)
	result := &formalize.Result{Proposal: p, CheckContext: &formalize.CheckContext{}}
	f := saved897FixtureFor(t, result)
	if len(f.payload) > statechart.MaxEvidenceBytes || len(f.companions) != 2 {
		t.Fatal("complete questions did not retain independently fitting alternatives")
	}
	for name, b := range f.companions {
		if len(b) > statechart.MaxEvidenceBytes || !formalize.EvidenceCompanion(name) {
			t.Fatal("unbounded or unexpected preview companion", name)
		}
	}
	f.s.prepare()
	f.refresh(t)
	w := saved897Healthy(t, f.s, f.ref, result)
	saved897Evidence(t, f.s, w.SavedDraft, f.payload)
	cold := f.restart(t, saved897Author)
	saved897Healthy(t, cold, f.ref, result)
	saved897Evidence(t, cold, w.SavedDraft, f.payload)
}
