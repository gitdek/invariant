package factory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/statechart"
)

// A limited candidate has no chart of its own to move into a companion, but
// its captured baseline copies repeat the sources the comparison's drawable
// baseline already retains. Those copies are bounded through
// reader-baseline.json, the candidate's own sources and limitation stay
// inline, and the saved draft recovers exactly (#944).
func TestProjection944LimitedCandidateSourcesRecovery(t *testing.T) {
	tc, _ := reader603Tools(t)
	include := strings.Replace(reader603Include, "====", "\\* "+strings.Repeat("x", 32<<20)+"\n====", 1)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	reader603Write(t, filepath.Join(ws, "Helper603.tla"), include)
	base, _ := reader603Checked(t, ws, tc)
	authority := (formalize.Request{Previous: base}).CheckContext()
	reader603Write(t, filepath.Join(ws, "proposal.json"), strings.Replace(reader603Draft, `"Limit":"2"`, `"Limit":"501"`, 1))
	reader603Write(t, filepath.Join(ws, "view.json"), reader944PartitionView(501))
	p, report, err := formalize.CheckWithContext(t.Context(), ws, tc, authority)
	if err != nil {
		t.Fatal(err)
	}
	if p.Reader.Chart != nil || p.Reader.Limitation == nil || p.Reader.Comparison == nil || p.Reader.Comparison.Baseline.Chart == nil {
		t.Fatal("fixture needs a limited candidate compared with a drawable captured baseline")
	}
	// The selected baseline also travels with the proposal, as it does
	// through an answered question: every retained copy keeps its sources.
	p.Baseline = authority.Baseline
	res := &formalize.Result{Proposal: p, Report: report, CheckContext: &authority}
	original, err := json.Marshal(res)
	if err != nil || len(original) <= statechart.MaxEvidenceBytes {
		t.Fatal("complete record must overflow", len(original), err)
	}
	r := newRig(t)
	result := &savedDraftResult{Result: *res, Publication: &draftPublication{}}
	commit, err := r.f.saveResult(t.Context(), result, "invariant: the result of a draft for #944", "")
	if err != nil {
		t.Fatal("the limited candidate's complete record could not be saved:", err)
	}
	payload, err := r.f.Repo.Show(t.Context(), commit, "result.json")
	if err != nil || len(payload) > statechart.MaxEvidenceBytes {
		t.Fatal("saved record exceeds its entry budget", len(payload), err)
	}
	var wire savedDraftResult
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.BaselineSources == nil || wire.Proposal.Reader.Limitation == nil || wire.Proposal.Reader.CandidateRef != nil ||
		wire.Proposal.Includes["Helper603.tla"] != include || len(wire.CheckContext.Baseline.Includes) != 0 || len(wire.Proposal.Baseline.Includes) != 0 {
		t.Fatal("only the repeated baseline copies may move; the limited candidate keeps its own outcome and sources inline")
	}
	for _, name := range []string{"reader-comparison.json", "reader-baseline.json"} {
		b, err := r.f.Repo.Show(t.Context(), commit, name)
		if err != nil || len(b) == 0 || len(b) > statechart.MaxEvidenceBytes {
			t.Fatalf("%s: missing or oversized companion: %v", name, err)
		}
	}
	ref := DraftReference{Version: 1, Repository: r.f.Repository, Issue: 944, Step: "draft-issue", Commit: commit, SHA256: draftDigest(payload), ProposalHash: p.Hash}
	b, _ := json.Marshal(ref)
	m := Marker{Kind: KindProposal, ProposalRef: b}
	run := func(context.Context, int, string) (RunState, string, error) { return RunDone, commit, nil }
	_, got, err := m.ResolveDraft(t.Context(), r.f.Repository, 944, run, r.f.Repo.Show)
	if err != nil || !draftEqual(got, &result.Result) {
		t.Fatal("published draft lost the limited candidate's complete evidence", err)
	}
	loaded, err := r.f.loadDraft(t.Context(), commit)
	if err != nil || !draftEqual(loaded.Result, result.Result) {
		t.Fatal("factory recovery changed the saved evidence", err)
	}
	after, _ := json.Marshal(res)
	if !bytes.Equal(original, after) {
		t.Fatal("saving mutated the checked result")
	}
	for _, damage := range []string{"missing", "changed", "swapped"} {
		t.Run(damage, func(t *testing.T) {
			_, bad, err := m.ResolveDraft(t.Context(), r.f.Repository, 944, run, func(ctx context.Context, sha, file string) ([]byte, error) {
				data, err := r.f.Repo.Show(ctx, sha, file)
				if file == "reader-baseline.json" {
					switch damage {
					case "missing":
						return nil, fmt.Errorf("missing companion")
					case "changed":
						data = append(data, ' ')
					case "swapped":
						data, err = r.f.Repo.Show(ctx, sha, "reader-candidate.json")
					}
				}
				return data, err
			})
			if err == nil || bad != nil {
				t.Fatal("a damaged baseline companion produced a usable draft")
			}
		})
	}
}

// A question set carries the captured baseline twice, in its check context
// and as the proposal's baseline. When the alternatives already moved into
// their option companions and the record still overflows, both copies move
// into one reader-baseline.json and the saved draft recovers exactly (#944).
func TestProjection944QuestionsBoundCapturedBaseline(t *testing.T) {
	tc, _ := reader603Tools(t)
	include := strings.Replace(reader603Include, "====", "\\* "+strings.Repeat("x", 32<<20)+"\n====", 1)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	reader603Write(t, filepath.Join(ws, "Helper603.tla"), include)
	base, _ := reader603Checked(t, ws, tc)
	authority := (formalize.Request{Previous: base}).CheckContext()
	draft := preview639Draft(t, 1, nil)
	for j := range 2 {
		preview639Input(draft, 0, j)["model"].(map[string]any)["includes"] = map[string]string{"Helper603.tla": include}
	}
	preview639Write(t, ws, draft)
	p, _, err := formalize.CheckWithContext(t.Context(), ws, tc, authority)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Forks) != 1 || p.Baseline == nil || p.Baseline.Includes["Helper603.tla"] != include {
		t.Fatal("fixture needs a question set that carries the captured baseline")
	}
	for _, option := range p.Forks[0].Options {
		if option.Preview == nil || option.Preview.Model == nil || option.Preview.Model.Reader == nil || option.Preview.Model.Reader.Chart == nil {
			t.Fatal("fixture needs drawable alternatives")
		}
	}
	res := &formalize.Result{Proposal: p, CheckContext: &authority}
	original, err := json.Marshal(res)
	if err != nil || len(original) <= statechart.MaxEvidenceBytes {
		t.Fatal("complete question record must overflow", len(original), err)
	}
	r := newRig(t)
	result := &savedDraftResult{Result: *res, Publication: &draftPublication{}}
	commit, err := r.f.saveResult(t.Context(), result, "invariant: the result of a draft for #944", "")
	if err != nil {
		t.Fatal("the complete question record could not be saved:", err)
	}
	payload, err := r.f.Repo.Show(t.Context(), commit, "result.json")
	if err != nil || len(payload) > statechart.MaxEvidenceBytes {
		t.Fatal("saved record exceeds its entry budget", len(payload), err)
	}
	var wire savedDraftResult
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.BaselineSources == nil || len(wire.CheckContext.Baseline.Includes) != 0 || len(wire.Proposal.Baseline.Includes) != 0 {
		t.Fatal("the captured baseline's repeated copies stayed inline")
	}
	for _, name := range []string{"reader-baseline.json", "previews/F1/A/reader-candidate.json", "previews/F1/B/reader-candidate.json"} {
		b, err := r.f.Repo.Show(t.Context(), commit, name)
		if err != nil || len(b) == 0 || len(b) > statechart.MaxEvidenceBytes {
			t.Fatalf("%s: missing or oversized companion: %v", name, err)
		}
	}
	ref := DraftReference{Version: 1, Repository: r.f.Repository, Issue: 944, Step: "draft-issue", Commit: commit, SHA256: draftDigest(payload)}
	b, _ := json.Marshal(ref)
	m := Marker{Kind: KindForks, ProposalRef: b, Forks: []formalize.Fork{{ID: "F1"}}}
	run := func(context.Context, int, string) (RunState, string, error) { return RunDone, commit, nil }
	_, got, err := m.ResolveDraft(t.Context(), r.f.Repository, 944, run, r.f.Repo.Show)
	if err != nil || !draftEqual(got, &result.Result) {
		t.Fatal("published questions lost the captured baseline's sources", err)
	}
	loaded, err := r.f.loadDraft(t.Context(), commit)
	if err != nil || !draftEqual(loaded.Result, result.Result) {
		t.Fatal("factory recovery changed the saved questions", err)
	}
	after, _ := json.Marshal(res)
	if !bytes.Equal(original, after) {
		t.Fatal("saving mutated the checked questions")
	}
	for _, damage := range []string{"missing", "changed"} {
		t.Run(damage, func(t *testing.T) {
			_, bad, err := m.ResolveDraft(t.Context(), r.f.Repository, 944, run, func(ctx context.Context, sha, file string) ([]byte, error) {
				data, err := r.f.Repo.Show(ctx, sha, file)
				if file == "reader-baseline.json" {
					if damage == "missing" {
						return nil, fmt.Errorf("missing companion")
					}
					data = append(data, ' ')
				}
				return data, err
			})
			if err == nil || bad != nil {
				t.Fatal("a damaged baseline companion produced usable questions")
			}
		})
	}
}
