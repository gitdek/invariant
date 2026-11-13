package factory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/project"
)

func classify857(t *testing.T, p *formalize.Proposal, value string) {
	t.Helper()
	b, _ := json.Marshal(p.Statements)
	var rows []map[string]any
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	for _, s := range rows {
		if s["kind"] == "bug" {
			delete(s, "code_behavior")
			if value != "" {
				s["code_behavior"] = value
			}
		}
	}
	b, _ = json.Marshal(rows)
	p.Statements = nil
	if err := json.Unmarshal(b, &p.Statements); err != nil {
		t.Fatal(err)
	}
	for _, s := range p.Statements {
		if s.Kind == project.Bug && s.CodeBehavior != value {
			t.Fatalf("proposal lost classification: %+v", s)
		}
	}
	p.Hash = project.ProposalHash(p.Bounds, p.Statements)
}

func TestProposalAndRatificationBindBugClassification857(t *testing.T) {
	p := bufferProposal(t)
	classify857(t, p, "current")
	before := *p
	before.Statements = append([]project.Statement(nil), p.Statements...)
	body := proposalComment(p, nil, Marker{Kind: KindProposal, Proposal: p})
	if !strings.Contains(body, "current") || !strings.Contains(body, "PutWhenFull") {
		t.Errorf("proposal hides current-code classification: %s", body)
	}
	lock := project.Lock{Bounds: p.Bounds, Statements: p.Statements, Ratified: &project.Ratification{By: "gitdek", Issue: 857, Comment: "https://github.com/o/r/issues/857#issuecomment-1", Proposal: p.Hash}}
	gh := fakeComments{perms: map[string]string{"gitdek": "write"}, comments: map[int64]github.Comment{1: {ID: 1, User: github.User{Login: "gitdek"}, IssueURL: "https://api.github.com/repos/o/r/issues/857", Body: "/invariant ratify " + strings.TrimPrefix(p.Hash, "sha256:")}}}
	if err := VerifyRatification(context.Background(), gh, "o/r", lock); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"hypothetical", ""} {
		classify857(t, p, value)
		lock.Statements = p.Statements
		if err := VerifyRatification(context.Background(), gh, "o/r", lock); err == nil {
			t.Errorf("classification %q can replace ratified current without ratification", value)
		}
		c := &formalize.Current{ModuleText: before.ModuleText, Includes: before.Includes, Lock: project.Lock{Bounds: before.Bounds, Statements: before.Statements}}
		p.Target = &formalize.Target{Dir: "examples/buffer", Previous: "#1", Amends: before.Hash}
		changes := formalize.Diff(c, p)
		// This is the actual factory amendment rendering, including old/new words.
		body = amendmentComment(p, nil, changes, Marker{Kind: KindProposal, Proposal: p})
		if !strings.Contains(body, "current") || (value != "" && !strings.Contains(body, value)) || strings.Contains(body, "No statement changes") {
			t.Errorf("amendment hides classification %q: %s", value, body)
		}
	}
}
