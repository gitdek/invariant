package formalize

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

// A draft's check is told the language its project is written in, by the
// caller and over the service the drafting agent's checker reads (#910):
// the request's own, TypeScript for code checked as it is, and the language
// of the project an amendment changes.
func TestADraftsCheckKnowsItsProjectsLanguage910(t *testing.T) {
	c := current(t)
	c.Manifest.Language = "python"
	for _, tc := range []struct {
		name string
		req  Request
		want string
	}{
		{"a new project in Go", Request{Repo: "o/r", Issue: 5, Title: "t", Language: "go"}, "go"},
		{"a new project with no language named", Request{Repo: "o/r", Issue: 5, Title: "t"}, ""},
		{"a new project in TypeScript", Request{Repo: "o/r", Issue: 5, Title: "t", Language: "typescript"}, "typescript"},
		{"code checked as it is", Request{Repo: "o/r", Issue: 5, Title: "t", Existing: &Existing{Paths: []string{"hooks/use-buffer.ts"}}}, "typescript"},
		{"an amendment", Request{Repo: "o/r", Issue: 5, Title: "t", Language: "go", Current: c}, "python"},
	} {
		authority := tc.req.CheckContext()
		if authority.Language != tc.want {
			t.Errorf("%s: the check is told %q; want %q", tc.name, authority.Language, tc.want)
		}
		capture, release, err := stageCheckContext(t.TempDir(), authority)
		if err != nil {
			t.Fatal(err)
		}
		reference, closeContext, err := serveCheckContext(t.Context(), capture)
		if err != nil {
			t.Fatal(err)
		}
		fetched, err := LoadCheckContext(t.Context(), reference, 0)
		closeContext()
		release()
		if err != nil || fetched.Language != tc.want {
			t.Errorf("%s: the agent's checker is told %q, %v; want %q", tc.name, fetched.Language, err, tc.want)
		}
	}
}

// The check stages a draft in its project's language, and changes nothing
// else about it: the draft itself keeps no language until the factory gives
// it one, and a draft that already says what it is stays as it says.
func TestADraftIsStagedInItsProjectsLanguage910(t *testing.T) {
	draft := func() *Proposal {
		return &Proposal{Draft: Draft{Name: "buffer", Module: "Buffer", Package: "buffer"}, ModuleText: "---- MODULE Buffer ----\n====\n"}
	}
	for language, want := range map[string]string{"typescript": "typescript", "python": "python", "go": "go", "": "go", "rust": "go"} {
		p := draft()
		if got := p.stagedIn(language).Manifest().Language; got != want {
			t.Errorf("a draft staged for %q is checked as %q; want %q", language, got, want)
		}
		if p.Language != "" {
			t.Errorf("staging a draft for %q gave the draft itself the language %q", language, p.Language)
		}
	}
	existing := draft()
	existing.Existing = []string{"hooks/use-buffer.ts"}
	amendment := draft()
	amendment.Target = &Target{Manifest: project.Manifest{Language: "go", Module: ".invariant/specs/Buffer.tla"}}
	named := draft()
	named.Language = "python"
	for name, tc := range map[string]struct {
		p    *Proposal
		want string
	}{"code checked as it is": {existing, "typescript"}, "an amendment": {amendment, "go"}, "a draft that names its language": {named, "python"}} {
		if staged := tc.p.stagedIn("typescript"); staged != tc.p || staged.Manifest().Language != tc.want {
			t.Errorf("%s is staged as another draft, or as %q; want itself, as %q", name, staged.Manifest().Language, tc.want)
		}
	}
}

// A draft is refused while its current bug's replay path can't be prepared
// for a reason the drafter can fix, with TLC's reason and what to write. A
// path that's prepared, a bug the gate doesn't replay, and a temporal
// counterexample, which no rewrite of the model fixes, each let it stand.
func TestADraftWithAnUnpreparedReplayIsRefused910(t *testing.T) {
	bug := func(e *verify.BugReplayEvidence) *verify.Report {
		return &verify.Report{ModelOnly: true, Passed: true, Bugs: []verify.Bug{
			{Name: "Old", Caught: true}, {Name: "Maybe", CodeBehavior: "hypothetical", Caught: true},
			{Name: "Now", CodeBehavior: "current", Caught: true, Replay: e}}}
	}
	for name, e := range map[string]*verify.BugReplayEvidence{
		"a prepared path":            {Required: true, Status: "pending", Request: &verify.BugReplayRequest{}},
		"a bug Go's own checks hold": {Status: "not-replayed", Message: "Go uses its existing proof, agreement and exploration checks."},
		"a temporal counterexample":  {Required: true, Status: "replay-unsupported-temporal", Message: "replay-unsupported-temporal: the retained infinite behavior cannot be checked by executable replay version 1"},
		"no replay evidence":         nil,
	} {
		if err := replayPrepared(bug(e)); err != nil {
			t.Errorf("a draft with %s is refused: %v", name, err)
		}
	}
	for name, message := range map[string]string{
		"a hidden choice":               "replay-unsupported-action: edge 2 takes Put, whose body chooses part of its next state where no argument names the choice",
		"a shadowed standard operator":  "replay-unsupported-model: the model defines Len, which the path's check takes from the standard modules",
		"an edge with two readings":     "replay-ambiguous-edge: edge 1 has no unique interpretation",
		"a bug that names no operation": "replay-unsupported-action: Now must name its parameterized operations",
	} {
		err := replayPrepared(bug(&verify.BugReplayEvidence{Required: true, Status: "replay-unavailable", Message: message}))
		if err == nil || !strings.Contains(err.Error(), "bug Now is current") || !strings.Contains(err.Error(), message) || !strings.Contains(err.Error(), `Bug == \E x \in S : BugStep(x)`) {
			t.Errorf("a draft with %s: %v; want it refused, by the bug's name, with TLC's reason and what to write", name, err)
		}
	}
}
