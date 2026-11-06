package formalize_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/toolchain"
)

func TestDirectPlanningCallsKeepForksChartFree(t *testing.T) {
	for _, kind := range []string{"plumbing", "prd"} {
		for _, draft := range []string{"chart-free", "preview"} {
			t.Run(kind+"/"+draft, func(t *testing.T) {
				fork := formalize.Fork{ID: "F1", Question: "Which wording?", Options: []formalize.Option{
					{ID: "A", Says: "First."}, {ID: "B", Says: "Second."},
				}}
				if draft == "preview" {
					fork.Options[0].Preview = &formalize.Preview{Fork: "F1", Option: "A"}
				}
				agentRan := false
				a := reader603Agent{run: func(ctx context.Context, j synth.Job) (synth.Usage, error) {
					agentRan = true
					b, err := os.ReadFile(filepath.Join(j.Workspace, "check-context.json"))
					if err != nil {
						t.Fatal(err)
					}
					var staged formalize.CheckContext
					if err := json.Unmarshal(b, &staged); err != nil {
						t.Fatal(err)
					}
					if staged.Planning != kind {
						t.Fatalf("direct planner staged mode %q; want %q", staged.Planning, kind)
					}
					reader603Write(t, filepath.Join(j.Workspace, "check-context.json"), `{}`)
					preview639Write(t, j.Workspace, formalize.Draft{Forks: []formalize.Fork{fork}})
					p, report, err := formalize.Check(ctx, j.Workspace, toolchain.Toolchain{})
					if draft == "preview" {
						if err == nil || !strings.Contains(err.Error(), "stay chart-free") {
							t.Fatalf("agent check did not refuse planning previews: %v", err)
						}
					} else if err != nil || p == nil || len(p.Forks) != 1 || p.HasPreviews() || p.Ratifiable() || report != nil {
						t.Fatalf("agent check did not accept chart-free planning: %+v %v", p, err)
					}
					return synth.Usage{}, nil
				}}
				f := formalize.Formalizer{Backend: a}
				plan := f.Plan
				if kind == "prd" {
					plan = f.PlanIssues
				}
				// Direct callers supply the checkout separately, without routing fields.
				r, err := plan(t.Context(), formalize.Request{Repo: "o/r", Issue: 639}, t.TempDir(), t.TempDir())
				if err != nil || r == nil || !agentRan {
					t.Fatalf("direct planning: %+v %v", r, err)
				}
				if draft == "preview" {
					if !strings.Contains(r.Problem, "stay chart-free") {
						t.Fatalf("final check did not refuse planning previews: %+v", r)
					}
					return
				}
				if r.Problem != "" || r.Proposal == nil || len(r.Proposal.Forks) != 1 || r.Proposal.HasPreviews() || r.Proposal.Ratifiable() || r.Report != nil {
					t.Fatalf("final check did not accept chart-free planning: %+v", r)
				}
				if err := r.Proposal.ValidatePreviewContext(formalize.CheckContext{Planning: kind}); err != nil {
					t.Fatalf("publication rejected chart-free planning: %v", err)
				}
			})
		}
	}
}
