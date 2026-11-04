package formalize

import (
	"context"
	"strings"
	"testing"
)

func TestStepRefusesIssuePlanAlongsidePlumbingPlan(t *testing.T) {
	req := wholeIssueRequest(t)
	req.ParentPlan = 10
	agent := &prdAgent{draft: map[string]string{
		"issues.json":   issuesDraft,
		"plan.json":     `{}`,
		"proposal.json": stepFork560,
	}}
	r, err := (Formalizer{Backend: agent, CheckRuns: 2}).Formalize(context.Background(), req, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if r == nil || r.Proposal != nil || !strings.Contains(r.Problem, "issues.json") || !strings.Contains(r.Problem, "#10") {
		t.Fatalf("issues.json must be refused before checking another draft: %+v", r)
	}
}
