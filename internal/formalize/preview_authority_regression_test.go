package formalize_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/synth"
)

func TestPreviewAuthoritySurvivesAgentEdits(t *testing.T) {
	tc, _ := reader603Tools(t)
	for _, authorized := range []bool{true, false} {
		t.Run(map[bool]string{true: "authorized revision", false: "invented revision"}[authorized], func(t *testing.T) {
			req := formalize.Request{Answers: []formalize.Answer{{Fork: "F0", Option: "A", By: "owner"}}}
			revision := formalize.Revision{Fork: "F0", Option: "B", By: "owner"}
			if authorized {
				req.RevisionChoices = []formalize.Revision{revision}
			}
			agent := reader603Agent{run: func(ctx context.Context, job synth.Job) (synth.Usage, error) {
				// The visible context is guidance, never the check's authority.
				reader603Write(t, filepath.Join(job.Workspace, "check-context.json"), `{"parent_plan":571,"choices":{"F0":"B"},"revisions":[{"fork":"F0","option":"B","by":"owner"}]}`)
				d := preview639Draft(t, 1, map[string]string{"F0": "B"})
				d["revised"] = []formalize.Revision{revision}
				preview639Write(t, job.Workspace, d)
				_, _, err := formalize.Check(ctx, job.Workspace, tc)
				if (err == nil) != authorized {
					t.Fatalf("direct check authorization: %v", err)
				}
				staged := loadDraftContext(t, ctx, job, 0)
				if staged.Choices["F0"] != "A" || staged.ParentPlan != 0 {
					t.Fatal("MCP launch lost caller authority")
				}
				_, _, err = formalize.CheckWithContext(ctx, job.Workspace, tc, staged)
				if (err == nil) != authorized {
					t.Fatalf("separate check authorization: %v", err)
				}
				return synth.Usage{}, nil
			}}
			r, err := (formalize.Formalizer{Backend: agent, Toolchain: tc}).Formalize(t.Context(), req, t.TempDir())
			if err != nil || (r.Problem == "") != authorized {
				t.Fatalf("final check: %+v %v", r, err)
			}
		})
	}
}

func TestStepFitCannotExemptBehaviorChoices(t *testing.T) {
	f := formalize.Fork{ID: "F1", Kind: "step_fit", Question: "The step cannot fit; split parent #571?", Options: []formalize.Option{
		{ID: "A", Says: "Build the complete step as one change."},
		{ID: "B", Says: "A writer stops parent #571 with /invariant stop and requests a replacement plan on a new issue with /invariant plan."},
	}}
	for _, change := range []string{"no parent", "different parent", "behavior", "extra option", "preview"} {
		t.Run(change, func(t *testing.T) {
			copyFork := f
			copyFork.Options = append([]formalize.Option(nil), f.Options...)
			parent := 571
			switch change {
			case "no parent":
				parent = 0
			case "different parent":
				parent = 572
			case "behavior":
				copyFork.Options[0].Says = "Retry the failed operation."
			case "extra option":
				copyFork.Options = append(copyFork.Options, formalize.Option{ID: "C", Says: "Retry."})
			case "preview":
				copyFork.Options[0].Preview = &formalize.Preview{}
			}
			if err := copyFork.ValidateStepFit(parent); err == nil || !strings.Contains(err.Error(), "F1") {
				t.Fatalf("invalid step-fit accepted: %v", err)
			}
		})
	}
}
