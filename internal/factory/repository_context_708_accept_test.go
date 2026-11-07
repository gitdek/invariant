package factory

import (
	"encoding/json"
	"html"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
)

func TestNativeProposalExplainsCapturedRegressionConventions708(t *testing.T) {
	p := proposal503(t)
	p.Plan.Tests = append(p.Plan.Tests, plumbing.Test{Name: "a label", Part: "admin", Framework: "jest", File: "admin/ready.test.ts", Selector: "widgets ready [one]", Says: "Admin widgets are ready."})
	p.Plan.Files = append(p.Plan.Files, "admin/main.ts")
	p.Plan.Sources["admin/ready.test.ts"] = "import {test, expect} from '@jest/globals';\ntest('widgets ready [one]', () => { expect(1).toBe(2); });\n"
	p.Plan.Trusted = []string{"worker/AGENTS.md"}
	p.Hash = p.Plan.Hash()
	body := planComment(p, Marker{Kind: KindProposal, Proposal: p})
	visible := strings.Split(body, "<!-- invariant:")[0]
	rendered := html.UnescapeString(visible)
	intro := strings.Split(visible, "**What changes.**")[0]
	for _, want := range []string{"Python", "TypeScript", "captured", "regression", "part"} {
		if !strings.Contains(intro, want) {
			t.Errorf("proposal's scope explanation omits %q", want)
		}
	}
	for _, test := range p.Plan.Tests {
		for _, want := range []string{test.Name, test.Part, test.Framework, test.File, test.Selector, test.Says} {
			if !strings.Contains(rendered, "<code>"+want+"</code>") {
				t.Errorf("proposal hides literal native table value %q", want)
			}
		}
		if source := p.Plan.Sources[test.File]; !strings.Contains(visible, source) {
			t.Errorf("proposal hides verbatim source for %s", test.File)
		}
	}
	for _, want := range []string{"```python", "```typescript", "worker/AGENTS.md", "person", "two repairs", "Existing unnamed tests"} {
		if !strings.Contains(visible, want) {
			t.Errorf("proposal omits %q", want)
		}
	}
	m, ok := DecodeMarker(body)
	if !ok || m.Proposal == nil || !reflect.DeepEqual(m.Proposal.Plan, p.Plan) || m.Proposal.Hash != p.Hash {
		t.Fatal("presentation changed the pinned proposal")
	}
}

func TestConfiguredPullRequestPostsDescribeActualChecks708(t *testing.T) {
	p := proposal503(t)
	p.Plan.Trusted = []string{"worker/AGENTS.md"}
	final := plumbing.Result{CommandResults: []plumbing.CommandResult{
		{Part: "worker", Phase: "format", Status: "passed"},
		{Part: "worker", Phase: "lint", Status: "passed"},
		{Part: "worker", Phase: "build", Status: "skipped", Reason: "No Python build phase708."},
		{Part: "worker", Phase: "test", Status: "passed"},
		{Part: "web", Phase: "format", Status: "passed"},
		{Part: "web", Phase: "lint", Status: "passed"},
		{Part: "web", Phase: "build", Status: "passed"},
		{Part: "web", Phase: "test", Status: "passed"},
	}, Accepted: map[string]bool{}}
	for _, test := range p.Plan.Tests {
		final.Accepted[test.Identity()] = true
		final.TestResults = append(final.TestResults, plumbing.TestResult{Name: test.Name, Part: test.Part, Framework: test.Framework, File: test.File, Selector: test.Selector, Phase: "accept", Kind: "test", Status: "passed", Reason: "completed"})
	}
	r := &plumbing.BuildResult{Passed: true, Approved: true, Tests: len(p.Plan.Tests), Final: final, Summary: final.Summary(), Review: "VERDICT: approve"}
	m := Marker{Kind: KindPR, Proposal: p, Project: plumbing.LockPath(708), Hash: p.Hash}
	thread := Thread{Issue: github.Issue{Number: 708}}
	for _, recovered := range []bool{false, true} {
		if recovered {
			data, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var saved plumbing.BuildResult
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			r = &saved
		}
		for name, body := range map[string]string{
			"issue post":     planPRComment(github.PullRequest{Number: 709, Draft: true}, r, p.Plan.Trusted, m),
			"PR description": planPullRequestBody(thread, m, r, p.Plan.Trusted),
		} {
			visible := strings.Split(body, "<!-- invariant:")[0]
			for _, want := range []string{"worker", "web", "format", "lint", "build", "test", "No Python build phase708.", "pytest", "vitest", "worker/AGENTS.md", "person"} {
				if !strings.Contains(visible, want) {
					t.Errorf("%s (recovered=%v) hides configured check or handoff %q", name, recovered, want)
				}
			}
			for _, absent := range []string{"gofmt", "go vet", "I'll merge it", "every package it touches"} {
				if strings.Contains(visible, absent) {
					t.Errorf("%s promises unconfigured behavior %q", name, absent)
				}
			}
		}
	}
	// The default Invariant post keeps its established check description.
	legacy := &formalize.Proposal{Plan: &plumbing.Plan{Name: "legacy"}}
	if body := planPRComment(github.PullRequest{Number: 710}, &plumbing.BuildResult{Tests: 1}, nil, Marker{Proposal: legacy}); !strings.Contains(body, "gofmt, go vet") {
		t.Error("the built-in Invariant post lost its actual checks")
	}
}

func TestExternalPlumbingRefusalNamesRemainingIntakeWork708(t *testing.T) {
	r, builder := plumbingRig(t, "page/page.go")
	r.f.Repository = otherRepo
	r.gh.open(708, "gitdek", "Add a widget", "Kind: plumbing\n\n/invariant solve")
	r.poll()
	post := r.expect(708, KindUnsupported, LabelHumanReview)
	body := strings.ToLower(post.Comment.Body)
	for _, want := range []string{"configuration", "ci", "external issue intake is still disabled", "person"} {
		if !strings.Contains(body, want) {
			t.Errorf("intake explanation omits %q", want)
		}
	}
	for _, obsolete := range []string{"build and review prompts describe invariant", "no way to fetch the repository's dependencies"} {
		if strings.Contains(body, obsolete) {
			t.Errorf("intake explanation still claims %q", obsolete)
		}
	}
	if len(r.form.requests) != 0 || len(builder.built) != 0 {
		t.Fatal("context support enabled external intake")
	}
	before := len(r.gh.comments[708])
	r.poll()
	if len(r.gh.comments[708]) != before {
		t.Error("unsupported intake was answered twice")
	}
}
