package factory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/synth"
)

func nestedIssuePlan(name string, titles ...string) *formalize.IssuePlan {
	p := &formalize.IssuePlan{Name: name, Summary: "Complete every part of " + name + ", in order."}
	for _, title := range titles {
		p.Issues = append(p.Issues, formalize.PlannedIssue{Title: title, Body: title + ".\n\nKind: plumbing"})
	}
	return p
}

type wholeIssueFactoryAgent struct{ plan *formalize.IssuePlan }

func (wholeIssueFactoryAgent) Name() string { return "scripted" }

func (a wholeIssueFactoryAgent) Run(_ context.Context, job synth.Job) (synth.Usage, error) {
	b, err := json.Marshal(a.plan)
	if err != nil {
		return synth.Usage{}, err
	}
	return synth.Usage{}, os.WriteFile(filepath.Join(job.Workspace, "issues.json"), b, 0o644)
}

func TestPlumbingDraftProposesAndRatifiesIssuePlan(t *testing.T) {
	r, builder := plumbingRig(t, "page/page.go")
	hub := &planHub{fakeGitHub: r.gh}
	r.f.GitHub = hub
	want := nestedIssuePlan("a complete decision graph", "Serve every decision", "Draw every decision")
	r.f.Formalizer = formalize.Formalizer{Backend: wholeIssueFactoryAgent{plan: want}, CheckRuns: 2}
	r.gh.open(10, "gitdek", want.Name, "Serve and draw the decision graph.\n\nKind: plumbing\n\n/invariant solve")
	r.poll()
	proposed := r.expect(10, KindProposal, LabelProposal)
	p := proposed.Marker.Proposal
	if p == nil || !reflect.DeepEqual(p.IssuePlan, want) || p.Plan != nil || p.Hash != want.Hash() {
		t.Fatalf("a plumbing draft's issues.json must be posted as a plan of issues: %+v", p)
	}
	for _, text := range append([]string{want.Name, want.Summary, "/invariant ratify " + strings.TrimPrefix(want.Hash(), "sha256:")[:hashChars]}, bodyLines(want)...) {
		if !strings.Contains(proposed.Comment.Body, text) {
			t.Errorf("the proposal must show %q:\n%s", text, proposed.Comment.Body)
		}
	}
	for _, issue := range want.Issues {
		if !strings.Contains(proposed.Comment.Body, issue.Title) {
			t.Errorf("the proposal omitted an issue title: %q", issue.Title)
		}
	}
	if len(hub.opened) != 0 {
		t.Fatalf("unratified plan opened issues: %v", hub.opened)
	}
	ratify := r.gh.say(10, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Hash, "sha256:")[:hashChars])
	r.poll()
	r.expect(10, KindRatified)
	if len(hub.opened) != 0 {
		t.Fatalf("ratifying a plan should record it before its next poll: %v", hub.opened)
	}
	r.poll()
	if len(hub.opened) != 1 {
		t.Fatalf("the ratified plan should open its first issue: %v", hub.opened)
	}
	plan := PlanOfIssues{Issue: 10, Hash: p.Hash, By: "gitdek", Comment: ratify.URL, Issues: want.Issues}
	expectOpenedFor(t, r, plan, hub.opened[0], 0)
	expectRecords(t, r, plan, hub.opened[0])
	if m := postsOnPlan(r, plan, KindPlanOpened)[0].Marker; m.Hash != plan.Hash || m.Ratifier != plan.By {
		t.Errorf("the first step must be opened under the ratified hash and writer's authority: %+v", m)
	}
	if len(builder.built) != 0 || len(r.build.built) != 0 || len(r.gh.prs) != 0 {
		t.Fatal("a plan of issues must not build its parent or give it a pull request")
	}
}

// Supply the persisted ratification a watcher reads after a restart. The
// runners below must produce their own opened-step and completion posts.
func nestedRatification(r *rig, n int, p *formalize.IssuePlan) PlanOfIssues {
	ratify := r.gh.say(n, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Hash(), "sha256:")[:hashChars])
	proposal := &formalize.Proposal{Draft: formalize.Draft{Name: p.Name}, IssuePlan: p, Hash: p.Hash()}
	r.gh.say(n, r.gh.me, post("ratified", "The plan is ratified.", Marker{
		Kind: KindRatified, Hash: p.Hash(), Proposal: proposal, ReplyTo: []int64{ratify.ID},
	}))
	return PlanOfIssues{Issue: n, Hash: p.Hash(), By: "gitdek", Comment: ratify.URL, Issues: p.Issues}
}

func nestedLeafMerged(r *rig, n int) {
	r.gh.say(n, r.gh.me, post("merged", "The leaf issue's pull request merged.", Marker{Kind: KindMerged}))
	r.gh.issues[n].State = "closed"
}

func TestParentAdvancesOnlyForItsChildsRatifiedPlanCompletion(t *testing.T) {
	r, hub, parent := planRig(t, "Complete the component", "Use the component")
	r.f.Self, r.gh.me = "invariant-code-factory[bot]", "invariant-code-factory[bot]"
	workOn(t, r.f, parent)
	child := hub.opened[0]
	plan := nestedRatification(r, child, nestedIssuePlan("the whole component", "Build the component"))
	wrong := nestedIssuePlan("an older component plan", "Build the old component").Hash()
	r.gh.say(child, r.gh.me, post("done", "A different plan finished.", Marker{Kind: KindPlanDone, Hash: wrong}))
	r.gh.say(child, "mallory", post("done", "A copied completion marker.", Marker{Kind: KindPlanDone, Hash: plan.Hash}))
	workOn(t, r.f, parent)
	if len(hub.opened) != 1 {
		t.Fatalf("another hash or author's completion must not release the parent: %v", hub.opened)
	}
	r.gh.say(child, r.gh.me, post("done", "The ratified plan finished.", Marker{Kind: KindPlanDone, Hash: plan.Hash}))
	r.gh.say(child, r.gh.me, post("note", "The completion remains recorded.", Marker{Kind: KindNote}))
	restarted := freshWatcher(r, hub)
	workOn(t, restarted, parent)
	workOn(t, restarted, parent)
	if len(hub.opened) != 2 {
		t.Fatalf("a matching factory completion must release the next parent step exactly once, after a restart too: %v", hub.opened)
	}
	expectRecords(t, r, parent, child, hub.opened[1])
	expectOpenedFor(t, r, parent, hub.opened[1], 1)
}

func TestUnfinishedAndStoppedChildPlansHoldTheirParent(t *testing.T) {
	for _, state := range []string{"unfinished", "stopped", "completed"} {
		t.Run(state, func(t *testing.T) {
			r, hub, parent := planRig(t, "Complete the component", "Use the component")
			workOn(t, r.f, parent)
			child := nestedRatification(r, hub.opened[0], nestedIssuePlan("the component", "Inventory the component", "Verify the component"))
			workOn(t, r.f, child)
			workOn(t, r.f, parent)
			if len(hub.opened) != 2 {
				t.Fatalf("parent must wait while the child has work left: %v", hub.opened)
			}
			first := hub.opened[1]
			if state == "stopped" {
				r.gh.say(child.Issue, "gitdek", "/invariant stop")
				workOn(t, r.f, child)
			}
			nestedLeafMerged(r, first)
			workOn(t, r.f, child)
			if state != "stopped" {
				if len(hub.opened) != 3 {
					t.Fatalf("the child must open its second step after the first merges: %v", hub.opened)
				}
				if state == "completed" {
					nestedLeafMerged(r, hub.opened[2])
					workOn(t, r.f, child)
				}
			}
			workOn(t, r.f, parent)
			wantOpened, wantDone := 3, 0
			if state == "stopped" {
				wantOpened = 2
			} else if state == "completed" {
				wantOpened, wantDone = 4, 1
			}
			if len(hub.opened) != wantOpened || len(postsOnPlan(r, child, KindPlanDone)) != wantDone {
				t.Fatalf("%s child: opened %v, child completion posts %d; want %d opened and %d completions", state, hub.opened, len(postsOnPlan(r, child, KindPlanDone)), wantOpened, wantDone)
			}
			if state == "completed" {
				expectRecords(t, r, parent, child.Issue, hub.opened[3])
			} else {
				expectRecords(t, r, parent, child.Issue)
			}
		})
	}
}

func TestPlansNestedTwoDeepFinishThroughPolling(t *testing.T) {
	r := newRig(t)
	hub := &planHub{fakeGitHub: r.gh}
	r.f.GitHub = hub
	r.gh.open(10, "gitdek", "Complete the system", "Build every part of the system.")
	parent := nestedRatification(r, 10, nestedIssuePlan("the system", "Complete the component", "Use the component"))
	r.poll()
	if len(hub.opened) != 1 {
		t.Fatalf("the parent must open its first issue: %v", hub.opened)
	}
	child := nestedRatification(r, hub.opened[0], nestedIssuePlan("the component", "Complete the evidence", "Use the evidence"))
	r.poll()
	if len(hub.opened) != 2 {
		t.Fatalf("the child must open its first issue while its parent waits: %v", hub.opened)
	}
	grandchild := nestedRatification(r, hub.opened[1], nestedIssuePlan("the evidence", "Produce the evidence"))
	r.poll()
	if len(hub.opened) != 3 {
		t.Fatalf("the grandchild must open its leaf while both parents wait: %v", hub.opened)
	}
	leaf := hub.opened[2]
	nestedLeafMerged(r, leaf)
	r.poll()
	if len(postsOnPlan(r, grandchild, KindPlanDone)) != 1 || len(hub.opened) != 3 {
		t.Fatal("the grandchild must record completion before its parent continues")
	}
	r.poll()
	if len(hub.opened) != 4 {
		t.Fatalf("the child must advance after its grandchild's completion: %v", hub.opened)
	}
	childTail := hub.opened[3]
	nestedLeafMerged(r, childTail)
	r.poll()
	r.poll()
	if len(hub.opened) != 5 {
		t.Fatalf("the parent must advance after its child's completion: %v", hub.opened)
	}
	parentTail := hub.opened[4]
	nestedLeafMerged(r, parentTail)
	r.poll()
	r.f = freshWatcher(r, hub)
	r.poll()
	r.poll()
	if len(hub.opened) != 5 {
		t.Fatalf("repeated polls and a restart must not open any step twice: %v", hub.opened)
	}
	var titles []string
	for _, n := range hub.opened {
		titles = append(titles, r.gh.issues[n].Title)
	}
	wantTitles := []string{"Complete the component", "Complete the evidence", "Produce the evidence", "Use the evidence", "Use the component"}
	if !reflect.DeepEqual(titles, wantTitles) {
		t.Errorf("nested steps must open in dependency order: %v", titles)
	}
	expectRecords(t, r, parent, child.Issue, parentTail)
	expectRecords(t, r, child, grandchild.Issue, childTail)
	expectRecords(t, r, grandchild, leaf)
	for _, plan := range []PlanOfIssues{parent, child, grandchild} {
		done := postsOnPlan(r, plan, KindPlanDone)
		if len(done) != 1 || done[0].Marker.Hash != plan.Hash {
			t.Errorf("plan on #%d must finish once under its ratified hash: %+v", plan.Issue, done)
		}
	}
	if len(r.gh.prs) != 0 || len(r.form.requests) != 0 {
		t.Fatal("the plan issues themselves must not draft or build anything after ratification")
	}
}
