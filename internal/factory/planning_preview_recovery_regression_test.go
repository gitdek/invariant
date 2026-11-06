package factory

import (
	"context"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
)

func TestRecoveredModeledPreviewsCannotPublishForPlanning(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	preview639Write(t, ws, preview639Draft(t, 1, nil))
	p := preview639Checked(t, ws, tc)
	if err := p.ValidatePreviewContext(formalize.CheckContext{}); err != nil {
		t.Fatalf("modeled fixture is not publishable: %v", err)
	}
	for _, kind := range []string{"plumbing", "prd"} {
		t.Run(kind, func(t *testing.T) {
			r := newRig(t)
			r.f.Repository = invariantRepository
			calls := 0
			r.f.Formalizer = reader603Formalizer{run: func(context.Context, formalize.Request, string) (*formalize.Result, error) {
				calls++
				return &formalize.Result{Proposal: p}, nil
			}}
			r.gh.open(639, "gitdek", "Compare alternatives", "Model the alternatives.\n/invariant solve")
			saved, err := r.f.saveResult(t.Context(), &formalize.Result{Proposal: p}, "completed modeled draft", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := r.f.Repo.Record(t.Context(), 639, "draft-issue", "draft before restart"); err != nil {
				t.Fatal(err)
			}
			if err := r.f.Repo.Finish(t.Context(), 639, "draft-issue", saved); err != nil {
				t.Fatal(err)
			}
			// The completed result predates the issue's current planning mode.
			if kind == "plumbing" {
				r.gh.issues[639].Body = "Plan the change.\nKind: plumbing\n/invariant solve"
			} else {
				r.gh.issues[639].Body = "Plan the issues for this PRD.\n/invariant plan"
			}
			r.poll()
			post := r.expect(639, KindStuck, LabelHumanReview)
			if calls != 0 {
				t.Fatal("recovery reran the completed drafting agent")
			}
			if !strings.Contains(post.Comment.Body, "plumbing and PRD forks stay chart-free") {
				t.Fatalf("recovery omitted the planning-mode diagnostic: %s", post.Comment.Body)
			}
			for _, text := range []string{"stateDiagram-v2", "/invariant choose", "/invariant ratify"} {
				if strings.Contains(post.Comment.Body, text) {
					t.Fatalf("planning refusal published %q", text)
				}
			}
			if post.Marker.Proposal != nil || len(post.Marker.Forks) != 0 || len(r.gh.prs) != 0 || len(r.build.built) != 0 {
				t.Fatal("recovery published alternatives or started a build")
			}
		})
	}
}
