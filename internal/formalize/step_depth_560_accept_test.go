package formalize

import (
	"context"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/synth"
)

const stepFork560 = `{"forks":[{"id":"F1","question":"The storage and UI work will not fit one reviewable pull request; should these become two steps of parent #10?","options":[{"id":"A","says":"Build the complete step as one change."},{"id":"B","says":"A writer stops parent #10 with /invariant stop and requests a replacement plan on a new issue with /invariant plan, splitting storage and UI into sibling steps."}]}]}`

// ParentPlan is caller-supplied provenance, never parsed from request.md by
// the agent's checker. Zero preserves ordinary issue planning.
func TestStepDraftValidationAndPrompts560(t *testing.T) {
	for _, mode := range []string{"plumbing", "modeled", "issues"} {
		for _, mixed := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/issues-only", true: "/mixed"}[mixed], func(t *testing.T) {
				req := wholeIssueRequest(t)
				req.ParentPlan = 10
				if mode == "modeled" {
					req.Plumbing = ""
				} else if mode == "issues" {
					req.PRD, req.Plumbing = req.Plumbing, ""
				}
				draft := map[string]string{"issues.json": issuesDraft}
				if mixed {
					draft["proposal.json"] = stepFork560
				}
				agent := &prdAgent{draft: draft}
				f := Formalizer{Backend: agent, Binary: "invariant", CheckRuns: 2}
				r, err := f.Formalize(context.Background(), req, t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				if r == nil || r.Problem == "" || (r.Proposal != nil && r.Proposal.Ratifiable()) {
					t.Fatalf("step issues.json must be refused without trusting an agent check: %+v", r)
				}
				if !strings.Contains(r.Problem, "issues.json") || !strings.Contains(r.Problem, "#10") {
					t.Errorf("refusal must identify the forbidden draft and its parent: %s", r.Problem)
				}
			})
		}
	}
	for _, mode := range []string{"plumbing", "modeled"} {
		t.Run(mode+"/question", func(t *testing.T) {
			req := wholeIssueRequest(t)
			req.ParentPlan = 10
			if mode == "modeled" {
				req.Plumbing = ""
			}
			agent := &prdAgent{draft: map[string]string{"proposal.json": stepFork560}}
			f := Formalizer{Backend: agent, Binary: "invariant", CheckRuns: 2}
			r, err := f.Formalize(context.Background(), req, t.TempDir())
			if err != nil || r == nil || r.Problem != "" || r.Proposal == nil || len(r.Proposal.Forks) != 1 || r.Proposal.Ratifiable() {
				t.Fatalf("an oversized step must still be able to ask its fork: %+v, %v", r, err)
			}
			guidance560(t, agent.job.Prompt)
			if got := flag560(agent.job, "-parent-plan"); got != "10" {
				t.Errorf("the separate checker lost caller-supplied parent context: %v", agent.job.GateServer)
			}
			// The same instructions apply when a writer answers or revises.
			req.Previous = r.Proposal
			req.Answers = []Answer{{Fork: "F1", Option: "A", Says: "Build the complete step as one change.", By: "gitdek"}}
			prompt := Prompt(req, 2)
			if mode == "plumbing" {
				prompt = PlanPrompt(req, 2)
			}
			guidance560(t, prompt)
		})
	}
	for _, mode := range []string{"plumbing", "issues"} {
		t.Run(mode+"/ordinary-issue", func(t *testing.T) {
			req := wholeIssueRequest(t)
			if mode == "issues" {
				req.PRD, req.Plumbing = req.Plumbing, ""
			}
			agent := &prdAgent{draft: map[string]string{"issues.json": issuesDraft}}
			r, err := (Formalizer{Backend: agent, CheckRuns: 2}).Formalize(context.Background(), req, t.TempDir())
			if err != nil || r == nil || r.Problem != "" || r.Proposal == nil || r.Proposal.IssuePlan == nil || r.Proposal.Hash != prdPlan().Hash() {
				t.Fatalf("ordinary %s planning must retain its plan and hash: %+v, %v", mode, r, err)
			}
			if strings.Contains(agent.job.Prompt, "can itself become a plan of issues") {
				t.Error("a top-level planner still promises its steps may recursively split")
			}
		})
	}
}

func guidance560(t *testing.T, prompt string) {
	t.Helper()
	for _, want := range []string{"#10", "issues.json", "proposal.json", "/invariant stop", "/invariant plan"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("step guidance omits %q", want)
		}
	}
	lower := strings.ToLower(prompt)
	if !strings.Contains(lower, "parent") || !strings.Contains(lower, "fit") || !strings.Contains(lower, "split") {
		t.Error("step guidance must explain how to ask about work that does not fit and suggest splitting its parent's steps")
	}
	if strings.Contains(prompt, "Otherwise write issues.json") || strings.Contains(prompt, "can itself become a plan of issues") {
		t.Error("step prompt still instructs the planner to create another plan of issues")
	}
}

func flag560(job synth.Job, name string) string {
	for i := 0; i+1 < len(job.GateServer); i++ {
		if job.GateServer[i] == name {
			return job.GateServer[i+1]
		}
	}
	return ""
}
