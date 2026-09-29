package factory

import (
	"slices"
	"strings"
	"testing"
)

// otherRepo is a repository that isn't Invariant's own.
const otherRepo = "gitdek/copythis-ad"

// A plumbing issue on a repository that isn't Invariant's own gets one
// answer, saying plumbing isn't supported there yet and why, and nothing is
// drafted for it: the planner never runs, no agent run is recorded, and no
// branch is pushed. The issue is left for a person (#103).
func TestPlumbingElsewhereGetsOneAnswerAndNoDraft(t *testing.T) {
	r, b := plumbingRig(t, "page/page.go")
	r.f.Repository = otherRepo
	r.gh.open(1, "gitdek", "Add a hello page", "A page that says hello.\n\nKind: plumbing\n\n/invariant solve")
	r.poll()
	answer := r.expect(1, KindUnsupported, LabelHumanReview)
	if !slices.Contains(answer.Marker.ReplyTo, 0) {
		t.Errorf("the answer should reply to the issue's /invariant solve: %v", answer.Marker.ReplyTo)
	}
	body := strings.ToLower(answer.Comment.Body)
	for _, want := range []string{"plumbing", "yet", "invariant init", "prompt", "trusted base", "dependencies", "person"} {
		if !strings.Contains(body, want) {
			t.Errorf("the answer doesn't say %q:\n%s", want, answer.Comment.Body)
		}
	}
	if len(r.form.requests) != 0 {
		t.Errorf("a plumbing issue on %s was drafted: %d requests to the planner", otherRepo, len(r.form.requests))
	}
	if runs := git(t, r.origin, "for-each-ref", "refs/invariant/runs/"); runs != "" {
		t.Errorf("an agent run was recorded:\n%s", runs)
	}
	if heads := git(t, r.origin, "for-each-ref", "--format=%(refname)", "refs/heads/"); heads != "refs/heads/main" {
		t.Errorf("branches = %q; want only main", heads)
	}

	// It answers once: later polls say nothing more, and draft and build
	// nothing.
	before := len(r.gh.comments[1])
	r.poll()
	r.poll()
	if len(r.gh.comments[1]) != before || len(r.form.requests) != 0 || len(b.built) != 0 {
		t.Errorf("the factory went on after its answer: %d comments, not %d; %d drafts; built %v",
			len(r.gh.comments[1]), before, len(r.form.requests), b.built)
	}
}

// The same plumbing issue gets a plan on Invariant's own repository, as it
// does now, and the answer instead on any other.
func TestOnlyInvariantsOwnRepositoryPlansPlumbing(t *testing.T) {
	for _, c := range []struct {
		repo    string
		planned bool
	}{{"gitdek/invariant", true}, {otherRepo, false}} {
		r, _ := plumbingRig(t, "page/page.go")
		r.f.Repository = c.repo
		r.gh.open(1, "gitdek", "Add a hello page", "Kind: plumbing\n\n/invariant solve")
		r.poll()
		if !c.planned {
			r.expect(1, KindUnsupported, LabelHumanReview)
			if len(r.form.requests) != 0 {
				t.Errorf("%s: a plumbing issue was drafted", c.repo)
			}
			continue
		}
		plan := r.expect(1, KindProposal, LabelProposal)
		if p := plan.Marker.Proposal; p == nil || p.Plan == nil || len(r.form.requests) != 1 || r.form.requests[0].Plumbing == "" {
			t.Errorf("%s: a plumbing issue should get a plan, drafted against the base branch: %+v", c.repo, p)
		}
		if !strings.Contains(plan.Comment.Body, "Here's my plan for **a hello page**") {
			t.Errorf("%s: the plan comment:\n%s", c.repo, plan.Comment.Body)
		}
	}
}

// A writer's /invariant revise on such an issue gets the same answer, and
// still nothing is drafted.
func TestARevisedPlumbingIssueElsewhereIsStillNotDrafted(t *testing.T) {
	r, _ := plumbingRig(t, "page/page.go")
	r.f.Repository = otherRepo
	r.gh.open(1, "gitdek", "Add a hello page", "Kind: plumbing\n\n/invariant solve")
	r.poll()
	r.expect(1, KindUnsupported, LabelHumanReview)
	revise := r.gh.say(1, "gitdek", "It's only a page. Plan it anyway.\n\n/invariant revise")
	r.poll()
	again := r.expect(1, KindUnsupported, LabelHumanReview)
	if !slices.Contains(again.Marker.ReplyTo, revise.ID) {
		t.Errorf("the answer should reply to the revise: %v", again.Marker.ReplyTo)
	}
	if posts := len(r.gh.posts(1)); posts != 2 || len(r.form.requests) != 0 {
		t.Errorf("%d posts and %d drafts; want two answers and no draft", posts, len(r.form.requests))
	}
	if runs := git(t, r.origin, "for-each-ref", "refs/invariant/runs/"); runs != "" {
		t.Errorf("an agent run was recorded:\n%s", runs)
	}
}
