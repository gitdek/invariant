package factory

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/statechart"
)

func TestProjection944SavedComparisonRecovery(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	reader603Write(t, filepath.Join(ws, "Helper603.tla"), strings.Replace(reader603Include, "====", "\\* "+strings.Repeat("x", 16<<20)+"\n====", 1))
	base, _ := reader603Checked(t, ws, tc)
	authority := (formalize.Request{Previous: base}).CheckContext()
	reader603Write(t, filepath.Join(ws, "view.json"), strings.ReplaceAll(reader603View, "Working", "Revised"))
	p, report, err := formalize.CheckWithContext(t.Context(), ws, tc, authority)
	if err != nil {
		t.Fatal(err)
	}
	r := newRig(t)
	result := &savedDraftResult{Result: formalize.Result{Proposal: p, Report: report, CheckContext: &authority}, Publication: &draftPublication{}}
	commit, err := r.f.saveResult(t.Context(), result, "invariant: the result of a draft for #944", "")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := r.f.Repo.Show(t.Context(), commit, "result.json")
	if err != nil || len(payload) > statechart.MaxEvidenceBytes {
		t.Fatal("saved record exceeds entry budget", err)
	}
	var wire savedDraftResult
	if err := json.Unmarshal(payload, &wire); err != nil || wire.Proposal.Reader.ComparisonRef == nil {
		t.Fatal("missing bounded comparison reference", err)
	}
	ref := DraftReference{Version: 1, Repository: r.f.Repository, Issue: 944, Step: "draft-issue", Commit: commit, SHA256: draftDigest(payload), ProposalHash: p.Hash}
	b, _ := json.Marshal(ref)
	m := Marker{Kind: KindProposal, ProposalRef: b}
	run := func(context.Context, int, string) (RunState, string, error) { return RunDone, commit, nil }
	_, got, err := m.ResolveDraft(t.Context(), r.f.Repository, 944, run, r.f.Repo.Show)
	if err != nil || !draftEqual(got, &result.Result) {
		t.Fatal("saved comparison lost original evidence", err)
	}
	loaded, err := r.f.loadDraft(t.Context(), commit)
	if err != nil || !draftEqual(loaded.Result, result.Result) {
		t.Fatal("factory recovery changed saved evidence", err)
	}
	for _, mode := range []string{"missing", "changed"} {
		t.Run(mode, func(t *testing.T) {
			_, _, err := m.ResolveDraft(t.Context(), r.f.Repository, 944, run, func(ctx context.Context, sha, file string) ([]byte, error) {
				data, err := r.f.Repo.Show(ctx, sha, file)
				if file == "reader-comparison.json" {
					if mode == "missing" {
						return nil, fmt.Errorf("missing companion")
					}
					data = append(data, ' ')
				}
				return data, err
			})
			if err == nil {
				t.Fatal("damaged companion authorized a recovered proposal")
			}
		})
	}
}
