package factory

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
)

// These tests work the plans of issues /invariant plan ratifies (#148). Each
// poll hands a ratified plan on an open issue to WorkPlan, which opens its
// issues one at a time, each once the one before has merged, and takes each
// on the authority of the writer who ratified the plan (#144). The plan's
// issue gets no other step, and none at all once it says the plan is done.

// pipelinePRD is the issue that holds a writer's PRD for a log pipeline.
const pipelinePRD = 10

// pipelinePlan is the plan of issues drafted for the log pipeline's PRD:
// three issues the factory takes as it takes any other.
func pipelinePlan() *formalize.IssuePlan {
	plan := &formalize.IssuePlan{Name: "a log pipeline", Summary: "Buffer the logs, ship them, then rotate them."}
	for i, title := range logPipeline {
		plan.Issues = append(plan.Issues, formalize.PlannedIssue{Title: title, Body: fmt.Sprintf("Step %d of the log pipeline: %s.", i+1, strings.ToLower(title))})
	}
	return plan
}

// pipelinePlanner drafts pipelinePlan for a PRD, and any other issue as rest
// does.
type pipelinePlanner struct{ rest Formalizer }

func (p pipelinePlanner) Formalize(ctx context.Context, req formalize.Request, out string) (*formalize.Result, error) {
	if req.PRD == "" {
		return p.rest.Formalize(ctx, req, out)
	}
	plan := pipelinePlan()
	return &formalize.Result{Proposal: &formalize.Proposal{Draft: formalize.Draft{Name: plan.Name}, IssuePlan: plan, Hash: plan.Hash()}}, nil
}

// pipelineRig is a rig whose watcher can open issues, with the PRD on
// pipelinePRD. The issues its plan opens have no forks to decide.
func pipelineRig(t *testing.T) (*rig, *planHub) {
	t.Helper()
	r := newRig(t)
	r.form.forks = nil
	hub := &planHub{fakeGitHub: r.gh}
	r.f.GitHub = hub
	r.f.Formalizer = pipelinePlanner{rest: r.form}
	r.gh.open(pipelinePRD, "gitdek", "A log pipeline", "Build the log pipeline docs/PRD.md describes.")
	return r, hub
}

// draftPipeline has @gitdek ask for a plan of issues on the PRD, polls, and
// returns the plan the factory proposes there.
func draftPipeline(t *testing.T, r *rig) *formalize.Proposal {
	t.Helper()
	r.gh.say(pipelinePRD, "gitdek", "/invariant plan")
	r.poll()
	proposed := r.gh.last(pipelinePRD)
	if p := proposed.Marker.Proposal; proposed.Marker.Kind == KindProposal && p != nil && p.IssuePlan != nil {
		return p
	}
	t.Fatalf("the PRD's latest post is %q; want a plan of issues for ratification:\n%s", proposed.Marker.Kind, proposed.Comment.Body)
	return nil
}

// ratifyPipeline has @gitdek ratify the proposed plan, and polls once, so
// the factory records the ratification. It returns the plan as ratified.
func ratifyPipeline(t *testing.T, r *rig, p *formalize.Proposal) PlanOfIssues {
	t.Helper()
	ratify := r.gh.say(pipelinePRD, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Hash, "sha256:")[:hashChars])
	r.poll()
	plan := PlanOfIssues{Issue: pipelinePRD, Hash: p.Hash, By: "gitdek", Comment: ratify.URL, Issues: p.IssuePlan.Issues}
	if n := len(postsOnPlan(r, plan, KindRatified)); n != 1 {
		t.Fatalf("the PRD has %d posts saying the plan is ratified; want one", n)
	}
	return plan
}

// A plan of three issues, drafted with /invariant plan and ratified, opens
// its first issue on the next poll: titled as its first step, naming the
// plan's issue and the ratifying comment, recorded there under the plan's
// hash and its ratifier, and taken as the ratifier's. The second opens only
// once the first has merged, and the plan's issue builds nothing of its own.
func TestARatifiedPlanOpensItsFirstIssueOnTheNextPoll(t *testing.T) {
	r, hub := pipelineRig(t)
	plan := ratifyPipeline(t, r, draftPipeline(t, r))
	r.poll()
	if len(hub.opened) != 1 {
		t.Fatalf("the poll after @gitdek ratified the plan opened %v; want its first issue alone", hub.opened)
	}
	first := hub.opened[0]
	expectOpenedFor(t, r, plan, first, 0)
	expectRecords(t, r, plan, first)
	if m := postsOnPlan(r, plan, KindPlanOpened)[0].Marker; m.Hash != plan.Hash || m.Ratifier != plan.By {
		t.Errorf("the plan's issue records #%d under the plan %s, ratified by %q; want the plan %s, ratified by %q", first, m.Hash, m.Ratifier, plan.Hash, plan.By)
	}

	// Nothing more opens while the first is worked, until it merges.
	pr := pollToPullRequest(t, r, first)
	r.poll()
	if len(hub.opened) != 1 {
		t.Fatalf("the plan opened %v before its first issue merged; want the first alone", hub.opened)
	}
	r.gh.ci(pr.Marker.PR, "success")
	r.poll()
	if kind := lastKindOn(r, first); kind != KindMerged {
		t.Fatalf("#%d's latest post is %q; want its merge", first, kind)
	}
	r.poll()
	if len(hub.opened) != 2 {
		t.Fatalf("once its first issue merged, the plan has opened %v; want its first two", hub.opened)
	}
	second := hub.opened[1]
	expectOpenedFor(t, r, plan, second, 1)
	expectRecords(t, r, plan, first, second)
	pollToProposal(t, r, second)
	for _, each := range r.gh.prs {
		if strings.HasPrefix(each.Head.Ref, fmt.Sprintf("invariant/issue-%d-", plan.Issue)) {
			t.Errorf("the plan's issue built something of its own: a pull request from %s", each.Head.Ref)
		}
	}
}

// A writer's /invariant stop on a ratified plan's issue opens nothing more.
// A poll answers it once, the issue already open goes on as it is, to a
// merge, and the plan isn't said to be done.
func TestAStopOnARatifiedPlanOpensNothingMore(t *testing.T) {
	r, hub := pipelineRig(t)
	plan := ratifyPipeline(t, r, draftPipeline(t, r))
	r.poll()
	if len(hub.opened) != 1 {
		t.Fatalf("the poll after @gitdek ratified the plan opened %v; want its first issue alone", hub.opened)
	}
	first := hub.opened[0]
	stop := r.gh.say(plan.Issue, "gitdek", "/invariant stop")
	mergeThroughFactory(t, r, first)
	r.poll()
	r.poll()
	if len(hub.opened) != 1 {
		t.Fatalf("after @gitdek's stop, the plan has opened %v; want nothing past its first issue", hub.opened)
	}
	answers := 0
	for _, p := range r.gh.posts(plan.Issue) {
		if slices.Contains(p.Marker.ReplyTo, stop.ID) {
			answers++
		}
	}
	if answers != 1 {
		t.Errorf("@gitdek's stop was answered %d times; want once", answers)
	}
	if done := postsOnPlan(r, plan, KindPlanDone); len(done) != 0 {
		t.Errorf("a stopped plan was said to be done:\n%s", done[0].Comment.Body)
	}
	expectRecords(t, r, plan, first)
}

// A plan of issues that hasn't been ratified opens nothing, however many
// polls pass: not while it waits for ratification, nor after someone who
// can't write says to ratify it, nor after a writer's ratify that names
// another hash. Once a writer ratifies it, the next poll opens its first
// issue.
func TestAPlanThatIsntRatifiedOpensNothing(t *testing.T) {
	r, hub := pipelineRig(t)
	p := draftPipeline(t, r)
	named := strings.TrimPrefix(p.Hash, "sha256:")[:hashChars]
	other := strings.Repeat("0", hashChars)
	if named == other {
		other = strings.Repeat("1", hashChars)
	}
	r.poll()
	r.gh.say(pipelinePRD, "mallory", "/invariant ratify "+named)
	r.poll()
	r.gh.say(pipelinePRD, "gitdek", "/invariant ratify "+other)
	r.poll()
	r.poll()
	if len(hub.opened) != 0 {
		t.Fatalf("a plan no writer ratified opened %v; want nothing", hub.opened)
	}
	for _, each := range r.gh.posts(pipelinePRD) {
		if kind := each.Marker.Kind; kind == KindRatified || kind == KindPlanOpened {
			t.Fatalf("a plan no writer ratified has a %q post:\n%s", kind, each.Comment.Body)
		}
	}

	ratifyPipeline(t, r, p)
	r.poll()
	if len(hub.opened) != 1 {
		t.Fatalf("once @gitdek ratified the plan, the next poll opened %v; want its first issue", hub.opened)
	}
}

// Once every one of a plan's issues has merged, the plan's issue says the
// plan is done, once, naming each, and then gets no more steps: a writer's
// /invariant stop there goes unanswered, and nothing more opens.
func TestADonePlansIssueGetsNoMoreSteps(t *testing.T) {
	r, hub := pipelineRig(t)
	plan := ratifyPipeline(t, r, draftPipeline(t, r))
	for i := range plan.Issues {
		r.poll()
		if len(hub.opened) != i+1 {
			t.Fatalf("the plan has opened %v; want its first %d", hub.opened, i+1)
		}
		expectOpenedFor(t, r, plan, hub.opened[i], i)
		mergeThroughFactory(t, r, hub.opened[i])
	}
	r.poll()
	done := postsOnPlan(r, plan, KindPlanDone)
	if len(done) != 1 {
		t.Fatalf("once every issue merged, the plan's issue says it's done %d times; want once", len(done))
	}
	for _, n := range hub.opened {
		if !postNamesIssue(done[0], n) {
			t.Errorf("the plan's issue says it's done without naming #%d:\n%s", n, done[0].Comment.Body)
		}
	}

	before := len(r.gh.posts(plan.Issue))
	r.gh.say(plan.Issue, "gitdek", "/invariant stop")
	r.poll()
	r.poll()
	if after := r.gh.posts(plan.Issue); len(after) != before {
		t.Errorf("the plan's issue got a step after the plan was done:\n%s", after[len(after)-1].Comment.Body)
	}
	if len(hub.opened) != len(plan.Issues) {
		t.Errorf("the plan has opened %v; want its %d issues", hub.opened, len(plan.Issues))
	}
}
