package factory

import (
	"context"
	"testing"

	"github.com/gitdek/invariant/internal/github"
)

// unlistedHub is a GitHub whose list of open issues leaves out the issues it
// hides, as GitHub's REST list left #523 out for 23 minutes after the
// factory opened it for a plan's step, though the issue read as open (#526).
// With hideNew, it hides each issue it opens.
type unlistedHub struct {
	*planHub
	hideNew bool
	hidden  map[int]bool
}

func (g *unlistedHub) CreateIssue(ctx context.Context, is github.NewIssue) (github.Issue, error) {
	created, err := g.planHub.CreateIssue(ctx, is)
	if err == nil && g.hideNew {
		g.hidden[created.Number] = true
	}
	return created, err
}

func (g *unlistedHub) OpenIssues(ctx context.Context) ([]github.Issue, error) {
	all, err := g.planHub.OpenIssues(ctx)
	var listed []github.Issue
	for _, is := range all {
		if !g.hidden[is.Number] {
			listed = append(listed, is)
		}
	}
	return listed, err
}

// A plan's just-opened issue is drafted on the next poll even while GitHub's
// list of open issues leaves it out, and only once when the list includes
// it again.
func TestAPlansUnlistedIssueIsStillTaken526(t *testing.T) {
	r, hub := pipelineRig(t)
	unlisted := &unlistedHub{planHub: hub, hidden: map[int]bool{}}
	r.f.GitHub = unlisted
	plan := ratifyPipeline(t, r, draftPipeline(t, r))
	unlisted.hideNew = true
	r.poll()
	if len(hub.opened) != 1 {
		t.Fatalf("the poll after @gitdek ratified the plan opened %v; want its first issue alone", hub.opened)
	}
	first := hub.opened[0]
	expectRecords(t, r, plan, first)
	pollToProposal(t, r, first)
	delete(unlisted.hidden, first)
	r.poll()
	r.poll()
	drafts := 0
	for _, req := range r.form.requests {
		if req.Issue == first {
			drafts++
		}
	}
	if drafts != 1 {
		t.Errorf("#%d was drafted %d times; want once", first, drafts)
	}
	if len(hub.opened) != 1 {
		t.Errorf("the plan opened %v before its first issue merged; want the first alone", hub.opened)
	}
}
