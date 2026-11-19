package factory

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
)

func TestReader944PublicationRetainsLargeProjection(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	reader603Write(t, filepath.Join(ws, "proposal.json"), strings.Replace(reader603Draft, `"Limit":"2"`, `"Limit":"500"`, 1))
	p, report := reader603Checked(t, ws, tc)
	if p.Reader.Chart == nil || p.Reader.Chart.Report.Counts["tlc_states"] != 501 {
		t.Fatal("factory input still limits a three-state projection at 501 concrete states")
	}
	// Force expanded-marker overflow using exact source bytes, without making
	// the statements or the complete three-state diagram too large to read.
	reader603Write(t, filepath.Join(ws, "Helper603.tla"), strings.Replace(reader603Include, "====", "\\* "+strings.Repeat("x", (4<<20)+1024)+"\n====", 1))
	p, report = reader603Checked(t, ws, tc)
	res := &formalize.Result{Proposal: p, Report: report, CheckContext: &formalize.CheckContext{}}
	for _, dashboard := range []string{"", compact911DashboardURL} {
		r := newRig(t)
		r.f.DashboardURL = dashboard
		r.f.Formalizer = reader603Formalizer{run: func(context.Context, formalize.Request, string) (*formalize.Result, error) { return res, nil }}
		r.gh.open(944, "gitdek", "Draw a complete projection", "/invariant solve")
		r.poll()
		if dashboard == "" {
			post := r.expect(944, KindStuck, LabelHumanReview)
			if strings.Contains(post.Comment.Body, "/invariant ratify ") {
				t.Fatal("oversize inline evidence enabled ratification")
			}
			continue
		}
		post := r.expect(944, KindProposal, LabelProposal)
		if len(post.Comment.Body) > 60000 {
			t.Fatal("comment capacity increased")
		}
		m, ok := DecodeMarker(post.Comment.Body)
		if !ok || m.ProposalRef == nil || m.Proposal != nil {
			t.Fatal("large checked evidence needs configured compact publication")
		}
		data, err := json.Marshal(m)
		if err != nil || len(data) > 4<<20 {
			t.Fatal("expanded marker capacity increased", err)
		}
		compact911Topology(t, post.Comment.Body, p.Reader.Chart)
		_, saved, err := m.ResolveDraft(t.Context(), r.f.Repository, 944, r.f.Repo.Run, r.f.Repo.Show)
		if err != nil || !draftEqual(saved.Proposal.Reader, p.Reader) || !draftEqual(saved.Report, report) {
			t.Fatal("saved evidence lost exact large projection", err)
		}
		compact911Fresh(t, r)
		r.poll()
		if len(r.gh.comments[944]) != 1 {
			t.Fatal("recovery republished or redrafted completed evidence")
		}
	}
}
