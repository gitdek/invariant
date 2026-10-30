package formalize

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/synth"
)

func wholeIssueRequest(t *testing.T) Request {
	t.Helper()
	req := prdRequest(t)
	req.Repo = "gitdek/invariant"
	req.Plumbing, req.PRD = req.PRD, ""
	req.Body += "\n\nKind: plumbing"
	return req
}

func TestPlumbingDraftReturnsAPlanOfIssues(t *testing.T) {
	agent := &prdAgent{draft: map[string]string{"issues.json": issuesDraft}}
	f := Formalizer{Backend: agent, CheckRuns: 2, Timeout: time.Minute}
	out := t.TempDir()
	r, err := f.Formalize(context.Background(), wholeIssueRequest(t), out)
	if err != nil {
		t.Fatal(err)
	}
	if r.Problem != "" || r.Proposal == nil || r.Proposal.IssuePlan == nil {
		t.Fatalf("a plumbing planner's issues.json must become a plan-of-issues proposal: %+v", r)
	}
	p := r.Proposal
	if !reflect.DeepEqual(p.IssuePlan, prdPlan()) || p.Hash != prdPlan().Hash() || !p.Ratifiable() || p.Plan != nil || len(p.Statements) != 0 {
		t.Fatalf("the proposal must pin exactly the plan of issues: %+v", p)
	}
	if !strings.Contains(agent.job.Prompt, "issues.json") {
		t.Error("the plumbing planner was not told how to return a plan of issues")
	}
	if b, err := os.ReadFile(filepath.Join(out, "issues.json")); err != nil || string(b) != issuesDraft {
		t.Errorf("the run must retain the issue-plan draft: text %q, err %v", b, err)
	}
	var saved Result
	b, err := os.ReadFile(filepath.Join(out, "formalization.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &saved); err != nil || saved.Proposal == nil || saved.Proposal.Hash != p.Hash {
		t.Errorf("the saved result must retain the proposal: %+v, err %v", saved, err)
	}

	// Returning issues.json must use the same validation as /invariant plan.
	for name, draft := range map[string]string{
		"empty plan":        `{"name":"empty","summary":"No steps.","issues":[]}`,
		"command in a body": strings.Replace(issuesDraft, "Kind: plumbing", "Kind: plumbing\\n/invariant solve", 1),
		"unknown field":     strings.Replace(issuesDraft, `"name":`, `"extra":true,"name":`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			f.Backend = &prdAgent{draft: map[string]string{"issues.json": draft}}
			r, err := f.Formalize(context.Background(), wholeIssueRequest(t), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if r.Problem == "" || (r.Proposal != nil && r.Proposal.Ratifiable()) {
				t.Fatalf("invalid issue plan must not be offered for ratification: %+v", r)
			}
		})
	}
}

type wholeIssueRevisionAgent struct {
	t        *testing.T
	previous *IssuePlan
	next     *IssuePlan
}

func (a wholeIssueRevisionAgent) Name() string { return "scripted" }

func (a wholeIssueRevisionAgent) Run(_ context.Context, job synth.Job) (synth.Usage, error) {
	b, err := os.ReadFile(filepath.Join(job.Workspace, "previous", "issues.json"))
	if err != nil {
		a.t.Errorf("the revision planner must receive previous/issues.json: %v", err)
	} else {
		var previous IssuePlan
		if err := json.Unmarshal(b, &previous); err != nil || !reflect.DeepEqual(&previous, a.previous) {
			a.t.Errorf("the previous draft was not preserved: %s, err %v", b, err)
		}
	}
	if !strings.Contains(job.Prompt, "previous/issues.json") {
		a.t.Error("the revision prompt must point to the previous plan of issues")
	}
	b, err = json.Marshal(a.next)
	if err != nil {
		return synth.Usage{}, err
	}
	return synth.Usage{}, os.WriteFile(filepath.Join(job.Workspace, "issues.json"), b, 0o644)
}

func TestPlumbingIssuePlanRevisionKeepsPreviousDraft(t *testing.T) {
	previous, next := prdPlan(), prdPlan()
	next.Summary = "Serve all decisions, including superseded ones, then prove the layout settles."
	req := wholeIssueRequest(t)
	req.Previous = &Proposal{Draft: Draft{Name: previous.Name}, IssuePlan: previous, Hash: previous.Hash()}
	req.Thread = append(req.Thread, Message{By: "gitdek", Body: "Include superseded decisions too."})
	f := Formalizer{Backend: wholeIssueRevisionAgent{t: t, previous: previous, next: next}, CheckRuns: 2, Timeout: time.Minute}
	r, err := f.Formalize(context.Background(), req, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if r.Problem != "" || r.Proposal == nil || !reflect.DeepEqual(r.Proposal.IssuePlan, next) || r.Proposal.Hash != next.Hash() || r.Proposal.Hash == previous.Hash() {
		t.Fatalf("revision must propose the revised plan under its own hash: %+v", r)
	}
}
