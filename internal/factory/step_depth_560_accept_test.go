package factory

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
)

func stepRig560(t *testing.T, modeled bool) (*rig, *planHub, PlanOfIssues, int) {
	t.Helper()
	r, _ := plumbingRig(t, "page/page.go")
	r.f.Self, r.gh.me = "invariant-code-factory[bot]", "invariant-code-factory[bot]"
	hub := &planHub{fakeGitHub: r.gh}
	r.f.GitHub = hub
	r.gh.open(10, "gitdek", "Complete the system", "Complete every part.")
	p := nestedIssuePlan("the system", "Complete the component", "Use the component")
	if modeled {
		p.Issues[0].Body = "Build a bounded buffer.\n\nProject: examples/09-buffer"
	}
	parent := nestedRatification(r, 10, p)
	workOn(t, r.f, parent)
	if len(hub.opened) != 1 {
		t.Fatalf("parent did not open exactly its first step: %v", hub.opened)
	}
	return r, hub, parent, hub.opened[0]
}

func splitRefused560(t *testing.T, r *rig, hub *planHub, child, parent, opened int) {
	t.Helper()
	p := r.gh.last(child)
	if p.Marker.Kind != KindStuck && p.Marker.Kind != KindForks {
		t.Errorf("step #%d must refuse the split or ask a person, got %s: %s", child, p.Marker.Kind, p.Comment.Body)
	}
	if p.Marker.Proposal != nil && p.Marker.Proposal.IssuePlan != nil {
		t.Error("a refused step retained a plan of issues as a proposal")
	}
	if !strings.Contains(p.Comment.Body, fmt.Sprintf("#%d", parent)) {
		t.Errorf("the step's reply must identify its parent #%d: %s", parent, p.Comment.Body)
	}
	r.poll()
	if len(hub.opened) != opened || len(r.gh.prs) != 0 {
		t.Errorf("refusing a split must open neither issues nor pull requests: issues=%v, prs=%v", hub.opened, r.gh.prs)
	}
}

// The real formalizer reads the agent's issues.json. A command on a step
// cannot turn it back into a top-level PRD, including on a later revision.
func TestPlanStepRefusesFurtherIssuePlans560(t *testing.T) {
	for _, modeled := range []bool{false, true} {
		for _, command := range []string{"initial", "plan", "revise"} {
			t.Run(fmt.Sprintf("modeled=%v/%s", modeled, command), func(t *testing.T) {
				r, hub, parent, child := stepRig560(t, modeled)
				if command != "initial" {
					pollToProposal(t, r, child)
					r.gh.say(child, "gitdek", "/invariant "+command)
				}
				r.f.Formalizer = formalize.Formalizer{Backend: wholeIssueFactoryAgent{plan: nestedIssuePlan("an unwanted split", "Store the data", "Show the data")}, CheckRuns: 2}
				r.poll()
				splitRefused560(t, r, hub, child, parent.Issue, 1)
			})
		}
	}
	t.Run("saved-draft-after-restart", func(t *testing.T) {
		r, hub, parent, child := stepRig560(t, false)
		p := nestedIssuePlan("saved but not ratified", "Store the data", "Show the data")
		result := &formalize.Result{Proposal: &formalize.Proposal{Draft: formalize.Draft{Name: p.Name}, IssuePlan: p, Hash: p.Hash()}}
		ctx := context.Background()
		if err := r.f.Repo.Record(ctx, child, "draft-issue", "a completed draft before restart"); err != nil {
			t.Fatal(err)
		}
		commit, err := r.f.saveResult(ctx, result, "the saved draft", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := r.f.Repo.Finish(ctx, child, "draft-issue", commit); err != nil {
			t.Fatal(err)
		}
		r.f = freshWatcher(r, hub)
		r.poll()
		splitRefused560(t, r, hub, child, parent.Issue, 1)
		if len(r.form.requests) != 0 {
			t.Error("a saved result should be validated without rerunning its agent")
		}
	})
	t.Run("membership-outlives-ratifier-permission", func(t *testing.T) {
		r, hub, parent, child := stepRig560(t, false)
		pollToProposal(t, r, child)
		r.gh.perms["gitdek"], r.gh.perms["writer"] = "read", "write"
		r.gh.say(child, "writer", "/invariant plan")
		r.f.Formalizer = formalize.Formalizer{Backend: wholeIssueFactoryAgent{plan: nestedIssuePlan("unwanted split", "One part", "Another part")}}
		r.poll()
		splitRefused560(t, r, hub, child, parent.Issue, 1)
	})
	for _, command := range []string{"solve", "plan"} {
		for _, copied := range []bool{false, true} {
			t.Run(fmt.Sprintf("ordinary/%s/copied=%v", command, copied), func(t *testing.T) {
				r, _ := plumbingRig(t, "page/page.go")
				r.f.Self, r.gh.me = "invariant-code-factory[bot]", "invariant-code-factory[bot]"
				hub := &planHub{fakeGitHub: r.gh}
				r.f.GitHub = hub
				want := nestedIssuePlan("ordinary work", "Store the data", "Show the data")
				body := "Complete both parts.\n\nKind: plumbing\n\n/invariant " + command
				if copied {
					mark := stepMark{plan: 50, hash: want.Hash(), step: 1}
					body += "\n" + mark.String()
					r.gh.open(50, "gitdek", "Another plan", "A plan.")
					m := Marker{Kind: KindPlanOpened, Hash: mark.hash, Step: 1, Opened: 91, Ratifier: "gitdek"}
					r.gh.say(50, r.gh.me, post("opened", "The actual step is #91.", m))
					m.Opened = 90
					r.gh.say(50, "mallory", post("opened", "An unauthenticated copied record.", m))
				}
				r.gh.open(90, "gitdek", "Ordinary work", body)
				r.f.Formalizer = formalize.Formalizer{Backend: wholeIssueFactoryAgent{plan: want}}
				r.poll()
				p := r.expect(90, KindProposal, LabelProposal).Marker.Proposal
				if p == nil || p.IssuePlan == nil || p.Hash != want.Hash() || len(hub.opened) != 0 {
					t.Fatalf("ordinary issue should propose its exact plan without opening anything: %+v, %v", p, hub.opened)
				}
			})
		}
	}
}

type questionThenChange560 struct {
	t    *testing.T
	rest Formalizer
}

func (f questionThenChange560) Formalize(ctx context.Context, req formalize.Request, out string) (*formalize.Result, error) {
	if req.PRD != "" {
		return &formalize.Result{Problem: "a step was incorrectly routed to a plan of issues"}, nil
	}
	prompt := formalize.Prompt(req, 2)
	if req.Plumbing != "" {
		prompt = formalize.PlanPrompt(req, 2)
	}
	for _, want := range []string{"#10", "issues.json", "/invariant stop", "/invariant plan"} {
		if !strings.Contains(prompt, want) {
			f.t.Errorf("step planner lacks %q in its instructions", want)
		}
	}
	if strings.Contains(prompt, "Otherwise write issues.json") || strings.Contains(prompt, "can itself become a plan of issues") {
		f.t.Error("the step's instructions still invite a nested split")
	}
	if len(req.Answers) == 0 {
		fork := formalize.Fork{ID: "F1", Kind: "step_fit", Question: "Storage and display do not fit one reviewable pull request; should they be two sibling steps in parent #10?", Options: []formalize.Option{
			{ID: "A", Says: "Build the complete step as one change."},
			{ID: "B", Says: "A writer stops parent #10 with /invariant stop and requests a replacement plan on a new issue with /invariant plan, with storage and display as separate steps."},
		}}
		return &formalize.Result{Proposal: &formalize.Proposal{Draft: formalize.Draft{Forks: []formalize.Fork{fork}}}}, nil
	}
	return f.rest.Formalize(ctx, req, out)
}

func TestPlanStepCanAskThenProposeOneChange560(t *testing.T) {
	for _, modeled := range []bool{false, true} {
		t.Run(fmt.Sprint(modeled), func(t *testing.T) {
			r, hub, _, child := stepRig560(t, modeled)
			r.f.Formalizer = questionThenChange560{t: t, rest: r.form}
			r.poll()
			r.expect(child, KindForks, LabelAsking)
			r.gh.say(child, "gitdek", "/invariant plan")
			r.poll()
			question := r.expect(child, KindForks, LabelAsking)
			for _, want := range []string{"Storage and display", "#10", "/invariant stop", "/invariant plan"} {
				if !strings.Contains(question.Comment.Body, want) {
					t.Errorf("posted question lost %q: %s", want, question.Comment.Body)
				}
			}
			r.gh.say(child, "gitdek", "/invariant choose F1 A")
			r.poll()
			p := r.expect(child, KindProposal, LabelProposal).Marker.Proposal
			if p == nil || !p.Ratifiable() || p.IssuePlan != nil {
				t.Fatalf("the answered step must propose one complete change: %+v", p)
			}
			if modeled {
				if p.Plan != nil || len(p.Statements) == 0 || p.Dir != "examples/09-buffer" {
					t.Fatalf("modeled step lost its statements or project directory: %+v", p)
				}
			} else if p.Plan == nil || p.Hash != r.form.plan.Hash() {
				t.Fatalf("plumbing step lost its single-change plan: %+v", p)
			}
			if len(hub.opened) != 1 || len(r.gh.prs) != 0 {
				t.Fatalf("asking and proposing must open nothing: issues=%v, prs=%v", hub.opened, r.gh.prs)
			}
		})
	}
}

func TestExistingNestedPlansKeepWorking560(t *testing.T) {
	r, hub, parent, childNumber := stepRig560(t, false)
	child := nestedRatification(r, childNumber, nestedIssuePlan("previously ratified child", "Complete the evidence"))
	workOn(t, r.f, child)
	grandchildNumber := hub.opened[1]
	grandchild := nestedRatification(r, grandchildNumber, nestedIssuePlan("previously ratified grandchild", "Produce the evidence"))
	workOn(t, r.f, grandchild)
	leaf := hub.opened[2]
	// Previously ratified nesting remains executable, but an unratified leaf
	// of that tree still has to build one change.
	r.f.Formalizer = formalize.Formalizer{Backend: wholeIssueFactoryAgent{plan: nestedIssuePlan("yet another split", "More steps", "Still more steps")}}
	r.poll()
	splitRefused560(t, r, hub, leaf, grandchild.Issue, 3)
	nestedLeafMerged(r, leaf)
	r.f = freshWatcher(r, hub)
	r.poll()
	if len(postsOnPlan(r, grandchild, KindPlanDone)) != 1 || len(hub.opened) != 3 {
		t.Fatal("grandchild must complete before either ancestor advances")
	}
	r.poll()
	if len(postsOnPlan(r, child, KindPlanDone)) != 1 || len(hub.opened) != 3 {
		t.Fatal("child must complete under its original hash before parent advances")
	}
	r.poll()
	if len(hub.opened) != 4 {
		t.Fatalf("parent did not open its final step after nested completion: %v", hub.opened)
	}
	tail := hub.opened[3]
	nestedLeafMerged(r, tail)
	r.poll()
	r.f = freshWatcher(r, hub)
	r.poll()
	r.poll()
	if len(hub.opened) != 4 {
		t.Fatalf("restart opened a step twice: %v", hub.opened)
	}
	for _, p := range []PlanOfIssues{parent, child, grandchild} {
		done := postsOnPlan(r, p, KindPlanDone)
		if len(done) != 1 || done[0].Marker.Hash != p.Hash {
			t.Errorf("plan #%d did not complete exactly once under its original hash: %+v", p.Issue, done)
		}
	}
	expectRecords(t, r, parent, childNumber, tail)
	expectRecords(t, r, child, grandchildNumber)
	expectRecords(t, r, grandchild, leaf)
}
