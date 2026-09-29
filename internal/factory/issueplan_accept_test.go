package factory

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
)

// prdBody is an issue that holds a PRD. Its own Kind: line doesn't make it
// one plumbing change once a writer asks for a plan of issues.
const prdBody = "Draw the decision graph on the dashboard, as docs/PRD.md says.\n\nKind: plumbing"

// graphPlan is a plan of issues for prdBody: a plumbing issue, then a
// modeled one that names its project.
func graphPlan() *formalize.IssuePlan {
	issues := []formalize.PlannedIssue{
		{Title: "Serve the decision graph as JSON", Body: "The dashboard serves every decision and its edges at /api/graph.json.\n\nKind: plumbing"},
		{Title: "Prove how the graph's layout settles", Body: "The layout moves each node toward its neighbors until nothing moves.\n\nProject: examples/09-graph-layout"},
	}
	return &formalize.IssuePlan{Name: "a decision graph on the dashboard", Summary: "Serve the graph, then prove its layout settles.", Issues: issues}
}

// prdPlanner drafts plans of issues, as the formalizer's agent does for a
// PRD: its forks until people answer them, then its plans, one a draft,
// repeating the last. A draft that isn't asked for as a PRD's plan of
// issues, with the base branch to read, gets a problem back.
type prdPlanner struct {
	t        *testing.T
	forks    []formalize.Fork
	plans    []*formalize.IssuePlan
	requests []formalize.Request
}

func (p *prdPlanner) Formalize(_ context.Context, req formalize.Request, _ string) (*formalize.Result, error) {
	p.requests = append(p.requests, req)
	if req.PRD == "" || req.Plumbing != "" {
		return &formalize.Result{Problem: "the draft wasn't asked for as a PRD's plan of issues"}, nil
	}
	if _, err := os.Stat(filepath.Join(req.PRD, "README.md")); err != nil {
		p.t.Errorf("the planner should get the base branch to read: %v", err)
	}
	if len(req.Answers) < len(p.forks) {
		return &formalize.Result{Proposal: &formalize.Proposal{Draft: formalize.Draft{Forks: p.forks}}}, nil
	}
	plan := *p.plans[0]
	plan.Issues = append([]formalize.PlannedIssue(nil), plan.Issues...)
	if len(p.plans) > 1 {
		p.plans = p.plans[1:]
	}
	return &formalize.Result{Proposal: &formalize.Proposal{Draft: formalize.Draft{Name: plan.Name}, IssuePlan: &plan, Hash: plan.Hash()}}, nil
}

// bodyLines is every line of a plan's issue bodies that says something.
func bodyLines(plan *formalize.IssuePlan) []string {
	var out []string
	for _, is := range plan.Issues {
		for _, l := range strings.Split(is.Body, "\n") {
			if strings.TrimSpace(l) != "" {
				out = append(out, l)
			}
		}
	}
	return out
}

// A writer's /invariant plan on an issue that holds a PRD has the factory
// draft a plan of issues against the base branch, whatever the issue's own
// Kind: line says, and post it for ratification: its name, each issue in
// order with its title and its whole body, and the ratify command that names
// the plan's hash. The same command from someone who can't write to the
// repository does nothing.
func TestAPlanCommandProposesAPlanOfIssues(t *testing.T) {
	r := newRig(t)
	planner := &prdPlanner{t: t, plans: []*formalize.IssuePlan{graphPlan()}}
	r.f.Formalizer = planner
	r.gh.open(40, "gitdek", "Draw the decision graph", prdBody)
	r.gh.say(40, "mallory", "/invariant plan")
	r.poll()
	if len(r.gh.posts(40)) != 0 || len(planner.requests) != 0 {
		t.Fatal("the factory took /invariant plan from someone who can't write to the repository")
	}

	r.gh.say(40, "gitdek", "/invariant plan")
	r.poll()
	proposed := r.expect(40, KindProposal, LabelProposal)
	p, want := proposed.Marker.Proposal, graphPlan()
	if p == nil || p.IssuePlan == nil || len(p.IssuePlan.Issues) != 2 || p.Hash != want.Hash() {
		t.Fatalf("the proposal should be the plan of issues, pinned by its hash %s: %+v", want.Hash(), p)
	}
	if len(planner.requests) != 1 || planner.requests[0].Issue != 40 {
		t.Fatalf("drafts: %+v", planner.requests)
	}
	body := proposed.Comment.Body
	for _, s := range append([]string{want.Name, want.Issues[0].Title, want.Issues[1].Title, "/invariant ratify " + strings.TrimPrefix(p.Hash, "sha256:")[:hashChars]}, bodyLines(want)...) {
		if !strings.Contains(body, s) {
			t.Errorf("the plan's post lacks %q:\n%s", s, body)
		}
	}
	if strings.Index(body, want.Issues[0].Title) > strings.Index(body, want.Issues[1].Title) {
		t.Errorf("the plan's post should show its issues in order:\n%s", body)
	}
}

// A plan of issues asks the forks its PRD leaves open first, as any draft
// does. Answering them drafts the plan of issues, and so does asking for a
// revision, which starts from the plan people asked to revise.
func TestAPlanOfIssuesAsksItsForksAndRevises(t *testing.T) {
	r := newRig(t)
	revised := graphPlan()
	revised.Issues = append(revised.Issues, formalize.PlannedIssue{Title: "Draw the graph on the page", Body: "The page draws the graph from /api/graph.json.\n\nKind: plumbing"})
	planner := &prdPlanner{t: t, plans: []*formalize.IssuePlan{graphPlan(), revised}, forks: []formalize.Fork{{ID: "F1", Question: "Does the graph show superseded decisions?",
		Options: []formalize.Option{{ID: "A", Says: "Yes, greyed out."}, {ID: "B", Says: "No, only the ones in force."}}}}}
	r.f.Formalizer = planner
	r.gh.open(40, "gitdek", "Draw the decision graph", prdBody)
	r.gh.say(40, "gitdek", "/invariant plan")
	r.poll()
	r.expect(40, KindForks, LabelAsking)

	r.gh.say(40, "gitdek", "/invariant choose F1 B")
	r.poll()
	first := r.expect(40, KindProposal, LabelProposal)
	if p := first.Marker.Proposal; p.IssuePlan == nil || p.Hash != graphPlan().Hash() {
		t.Fatalf("answering the fork should draft the plan of issues: %+v", p)
	}
	if len(planner.requests) != 2 {
		t.Fatalf("%d drafts; want the forks, then the plan", len(planner.requests))
	}
	if a := planner.requests[1].Answers; len(a) != 1 || a[0].Fork != "F1" || a[0].Option != "B" {
		t.Errorf("the plan should be drafted with the answer: %+v", a)
	}

	r.gh.say(40, "gitdek", "Draw the graph on the page as well.\n\n/invariant revise")
	r.poll()
	second := r.expect(40, KindProposal, LabelProposal)
	if p := second.Marker.Proposal; p.IssuePlan == nil || p.Hash != revised.Hash() || !strings.Contains(second.Comment.Body, "Draw the graph on the page") {
		t.Fatalf("the revision should propose the revised plan of issues: %+v", p)
	}
	if len(planner.requests) != 3 {
		t.Fatalf("%d drafts; want the forks, the plan and its revision", len(planner.requests))
	}
	if prev := planner.requests[2].Previous; prev == nil || prev.IssuePlan == nil || prev.Hash != graphPlan().Hash() {
		t.Errorf("the revision should start from the plan people asked to revise: %+v", prev)
	}
}

// Ratifying a plan of issues is recorded on its issue once, in answer to
// the ratifying comment, naming who ratified it and the plan's hash, and the
// issue no longer waits for ratification. The plan itself builds nothing: no
// agent run, no branch and no pull request.
func TestRatifyingAPlanOfIssuesRecordsItAndBuildsNothing(t *testing.T) {
	r := newRig(t)
	r.f.Formalizer = &prdPlanner{t: t, plans: []*formalize.IssuePlan{graphPlan()}}
	r.gh.open(40, "gitdek", "Draw the decision graph", prdBody)
	r.gh.say(40, "gitdek", "/invariant plan")
	r.poll()
	p := r.expect(40, KindProposal, LabelProposal).Marker.Proposal
	named := strings.TrimPrefix(p.Hash, "sha256:")[:hashChars]

	ratify := r.gh.say(40, "gitdek", "/invariant ratify "+named)
	r.poll()
	r.poll()
	var ratified []Post
	for _, each := range r.gh.posts(40) {
		if each.Marker.Kind == KindRatified {
			ratified = append(ratified, each)
		}
		if each.Marker.Kind == KindFailed {
			t.Errorf("the factory says the plan failed:\n%s", each.Comment.Body)
		}
	}
	if len(ratified) != 1 {
		t.Fatalf("the factory should say once that the plan is ratified; %d posts say so", len(ratified))
	}
	m := ratified[0].Marker
	if m.Hash != p.Hash || m.Proposal == nil || m.Proposal.IssuePlan == nil || !slices.Contains(m.ReplyTo, ratify.ID) {
		t.Errorf("the ratified post should record the plan, in answer to the ratifying comment: %+v", m)
	}
	if body := ratified[0].Comment.Body; !strings.Contains(body, "@gitdek") || !strings.Contains(body, named) {
		t.Errorf("the ratified post should name who ratified it and the plan's hash:\n%s", body)
	}
	if slices.Contains(r.gh.labelsOf(40), LabelProposal) {
		t.Errorf("a ratified plan still waits for ratification: labels %v", r.gh.labelsOf(40))
	}
	if len(r.build.built) != 0 || len(r.gh.prs) != 0 {
		t.Errorf("a plan of issues builds nothing itself: builds %v, %d pull requests", r.build.built, len(r.gh.prs))
	}
	if refs := git(t, r.origin, "for-each-ref", "--format=%(refname)", "refs/heads"); refs != "refs/heads/main" {
		t.Errorf("a plan of issues pushes no branch: %s", refs)
	}
}
