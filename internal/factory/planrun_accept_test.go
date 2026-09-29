package factory

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/github"
)

// These tests work a ratified plan of issues (#126), as factory/plans proves
// it. The watcher opens the plan's issues in order, one at a time, each once
// the one before it has merged, records each on the plan's issue with its
// number, and says there when every one has merged. It solves each issue it
// opens on the authority of the writer who ratified the plan. #112 drafts
// and ratifies the plan; here, WorkPlan is handed the plan @gitdek ratified
// on #10.

// logPipeline is a plan's three issues, by title.
var logPipeline = []string{"Add the log buffer", "Add the shipper", "Rotate the logs"}

// planHub is the fake GitHub, opening issues the way GitHub does: each gets
// the next number, and whoever the factory acts as opens it. It lists the
// issues it opened, in order.
type planHub struct {
	*fakeGitHub
	opened []int
}

func (g *planHub) CreateIssue(_ context.Context, is github.NewIssue) (github.Issue, error) {
	n := 1
	for k := range g.issues {
		n = max(n, k+1)
	}
	by := github.User{Login: g.me, Type: "User"}
	if strings.HasSuffix(g.me, "[bot]") {
		by.Type = "Bot"
	}
	issue := &github.Issue{Number: n, Title: is.Title, Body: is.Body, User: by, State: "open",
		URL: fmt.Sprintf("https://github.com/o/r/issues/%d", n), CreatedAt: "2026-09-25T12:00:00Z"}
	for _, l := range is.Labels {
		issue.Labels = append(issue.Labels, github.Label{Name: l})
	}
	g.issues[n] = issue
	g.opened = append(g.opened, n)
	return *issue, nil
}

// stopAtRecord is the GitHub of a watcher that stops just before it records
// an issue it opened: its post on the plan's issue fails, and so does every
// effect after it, since a watcher that stopped takes none.
type stopAtRecord struct {
	*planHub
	plan    int
	stopped bool
}

func (g *stopAtRecord) PostComment(ctx context.Context, n int, body string) (github.Comment, error) {
	if g.stopped || (n == g.plan && len(g.opened) > 0) {
		g.stopped = true
		return github.Comment{}, errCrash
	}
	return g.planHub.PostComment(ctx, n, body)
}

func (g *stopAtRecord) CreateIssue(ctx context.Context, is github.NewIssue) (github.Issue, error) {
	if g.stopped {
		return github.Issue{}, errCrash
	}
	return g.planHub.CreateIssue(ctx, is)
}

func (g *stopAtRecord) AddLabels(ctx context.Context, n int, labels ...string) error {
	if g.stopped {
		return errCrash
	}
	return g.planHub.AddLabels(ctx, n, labels...)
}

func (g *stopAtRecord) RemoveLabel(ctx context.Context, n int, label string) error {
	if g.stopped {
		return errCrash
	}
	return g.planHub.RemoveLabel(ctx, n, label)
}

// planRig is a rig whose watcher can open issues, with a plan of issues by
// these titles that @gitdek ratified on #10. The issues it opens have no
// forks to decide.
func planRig(t *testing.T, titles ...string) (*rig, *planHub, PlanOfIssues) {
	t.Helper()
	r := newRig(t)
	r.form.forks = nil
	hub := &planHub{fakeGitHub: r.gh}
	r.f.GitHub = hub
	r.gh.open(10, "gitdek", "A log pipeline", "The PRD for a log pipeline.")
	ratifying := r.gh.say(10, "gitdek", "This plan is right, and I ratify it.")
	plan := PlanOfIssues{Issue: 10, Hash: "sha256:" + strings.Repeat("5e", 32), By: "gitdek", Comment: ratifying.URL}
	for i, title := range titles {
		plan.Issues = append(plan.Issues, PlannedIssue{Title: title, Body: fmt.Sprintf("Step %d of the log pipeline: %s.", i+1, strings.ToLower(title))})
	}
	return r, hub, plan
}

// workOn has watcher f take the plan's next steps, as a poll would.
func workOn(t *testing.T, f *Factory, plan PlanOfIssues) {
	t.Helper()
	if err := f.WorkPlan(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
}

// freshWatcher starts in the place of r's watcher, on the same machine, and
// reaches GitHub through gh. It remembers nothing of the watcher before it.
func freshWatcher(r *rig, gh GitHub) *Factory {
	return &Factory{Repository: r.f.Repository, GitHub: gh, Repo: r.f.Repo, Formalizer: r.f.Formalizer, Builder: r.f.Builder,
		Base: r.f.Base, Projects: r.f.Projects, Work: r.f.Work, Check: r.f.Check, Self: r.f.Self, Log: r.t.Logf}
}

// postsOnPlan are the factory's posts of one kind on the plan's issue.
func postsOnPlan(r *rig, plan PlanOfIssues, kind string) []Post {
	var out []Post
	for _, p := range r.gh.posts(plan.Issue) {
		if p.Marker.Kind == kind {
			out = append(out, p)
		}
	}
	return out
}

// postNamesIssue says whether a post's words name issue n, as #n.
func postNamesIssue(p Post, n int) bool {
	words, _, _ := strings.Cut(p.Comment.Body, "<!--")
	return regexp.MustCompile(fmt.Sprintf(`#%d\b`, n)).MatchString(words)
}

// expectRecords checks that the plan's issue records exactly these issues,
// in order, each in a post of its own that names it.
func expectRecords(t *testing.T, r *rig, plan PlanOfIssues, want ...int) {
	t.Helper()
	records := postsOnPlan(r, plan, KindPlanOpened)
	if len(records) != len(want) {
		t.Fatalf("the plan's issue records %d issues opened; want %d, %v", len(records), len(want), want)
	}
	for i, n := range want {
		if !postNamesIssue(records[i], n) {
			t.Errorf("record %d on the plan's issue doesn't name #%d:\n%s", i+1, n, records[i].Comment.Body)
		}
	}
}

// expectOpenedFor checks that issue n is the plan's step i, counting from 0:
// it's open, with the step's title, and its body holds the step's text and
// names the plan's issue and the ratifying comment.
func expectOpenedFor(t *testing.T, r *rig, plan PlanOfIssues, n, i int) {
	t.Helper()
	is, step := r.gh.issues[n], plan.Issues[i]
	switch {
	case is.State != "open" || is.Title != step.Title:
		t.Errorf("#%d is %s, titled %q; want it open, titled %q, as step %d", n, is.State, is.Title, step.Title, i+1)
	case !strings.Contains(is.Body, step.Body):
		t.Errorf("#%d's body doesn't hold step %d's text, %q:\n%s", n, i+1, step.Body, is.Body)
	case !strings.Contains(is.Body, fmt.Sprintf("#%d", plan.Issue)) || !strings.Contains(is.Body, plan.Comment):
		t.Errorf("#%d's body doesn't name the plan's issue, #%d, and the ratifying comment, %s:\n%s", n, plan.Issue, plan.Comment, is.Body)
	}
}

// expectUntaken checks that the watcher hasn't taken issue n: it hasn't
// drafted it or posted on it.
func expectUntaken(t *testing.T, r *rig, n int, why string) {
	t.Helper()
	if posts := r.gh.posts(n); len(posts) != 0 {
		t.Errorf("#%d %s, and the factory posted on it:\n%s", n, why, posts[0].Comment.Body)
	}
	for _, req := range r.form.requests {
		if req.Issue == n {
			t.Errorf("#%d %s, and the factory drafted it", n, why)
			break
		}
	}
}

// lastKindOn is the kind of the factory's latest post on issue n, or "" when
// it hasn't posted there.
func lastKindOn(r *rig, n int) string {
	posts := r.gh.posts(n)
	if len(posts) == 0 {
		return ""
	}
	return posts[len(posts)-1].Marker.Kind
}

// pollToProposal polls, and checks that the watcher has taken issue n and
// drafted it: its latest post is a proposal, which it returns.
func pollToProposal(t *testing.T, r *rig, n int) Post {
	t.Helper()
	r.poll()
	posts := r.gh.posts(n)
	if len(posts) == 0 {
		t.Fatalf("#%d has no posts; want the watcher to have taken it and drafted a proposal", n)
	}
	p := posts[len(posts)-1]
	if p.Marker.Kind != KindProposal {
		t.Fatalf("#%d's latest post is %q; want the proposal the factory drafts once it takes the issue:\n%s", n, p.Marker.Kind, p.Comment.Body)
	}
	return p
}

// pollToPullRequest takes issue n, which the factory opened for the plan, to
// an open pull request, as people would: the watcher drafts it, and @gitdek
// ratifies its proposal. It returns the factory's post of the pull request.
func pollToPullRequest(t *testing.T, r *rig, n int) Post {
	t.Helper()
	p := pollToProposal(t, r, n)
	r.gh.say(n, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Marker.Proposal.Hash, "sha256:")[:hashChars])
	r.poll()
	pr := r.gh.last(n)
	if pr.Marker.Kind != KindPR {
		t.Fatalf("#%d's latest post is %q; want its pull request:\n%s", n, pr.Marker.Kind, pr.Comment.Body)
	}
	return pr
}

// mergeThroughFactory takes issue n, which the factory opened for the plan,
// to a merge through the factory: @gitdek ratifies its proposal, and CI's
// gate passes on its pull request.
func mergeThroughFactory(t *testing.T, r *rig, n int) {
	t.Helper()
	pr := pollToPullRequest(t, r, n)
	r.gh.ci(pr.Marker.PR, "success")
	r.poll()
	if kind := lastKindOn(r, n); kind != KindMerged {
		t.Fatalf("#%d's latest post is %q; want its merge", n, kind)
	}
}

// A ratified plan of three issues opens the first, and opens the second
// only once the first has merged. The plan's issue records each by number,
// and the watcher takes each as if the writer who ratified the plan had
// written it.
func TestAPlanOpensItsIssuesOneAtATime(t *testing.T) {
	r, hub, plan := planRig(t, logPipeline...)
	workOn(t, r.f, plan)
	if len(hub.opened) != 1 {
		t.Fatalf("the plan opened %v at first; want its first issue alone", hub.opened)
	}
	first := hub.opened[0]
	expectOpenedFor(t, r, plan, first, 0)
	expectRecords(t, r, plan, first)

	// Nothing more opens while the first is worked, until it merges.
	pr := pollToPullRequest(t, r, first)
	workOn(t, r.f, plan)
	if len(hub.opened) != 1 {
		t.Fatalf("the plan opened %v before its first issue merged; want the first alone", hub.opened)
	}
	expectRecords(t, r, plan, first)
	r.gh.ci(pr.Marker.PR, "success")
	r.poll()
	if kind := lastKindOn(r, first); kind != KindMerged {
		t.Fatalf("#%d's latest post is %q; want its merge", first, kind)
	}

	workOn(t, r.f, plan)
	if len(hub.opened) != 2 {
		t.Fatalf("once its first issue merged, the plan has opened %v; want its first two", hub.opened)
	}
	second := hub.opened[1]
	expectOpenedFor(t, r, plan, second, 1)
	expectRecords(t, r, plan, first, second)
	pollToProposal(t, r, second)
}

// A watcher that stops between opening an issue and recording it on the
// plan's issue leaves one issue, not two. Nothing takes the issue until it's
// recorded: a fresh watcher finds it, records it without opening another,
// and then takes it.
func TestACrashBetweenOpeningAndRecordingLeavesOneIssue(t *testing.T) {
	r, hub, plan := planRig(t, logPipeline...)
	r.f.GitHub = &stopAtRecord{planHub: hub, plan: plan.Issue}
	if err := r.f.WorkPlan(context.Background(), plan); err != nil {
		t.Logf("the watcher stopped: %v", err)
	}
	if records := postsOnPlan(r, plan, KindPlanOpened); len(hub.opened) != 1 || len(records) != 0 {
		t.Fatalf("the watcher that stopped opened %v and recorded %d; want one issue, not recorded", hub.opened, len(records))
	}
	n := hub.opened[0]

	r.f = freshWatcher(r, hub)
	r.poll()
	expectUntaken(t, r, n, "isn't recorded on the plan's issue yet")
	workOn(t, r.f, plan)
	workOn(t, r.f, plan)
	if len(hub.opened) != 1 {
		t.Fatalf("after the crash, the plan has opened %v; want its one issue, not a second", hub.opened)
	}
	expectOpenedFor(t, r, plan, n, 0)
	expectRecords(t, r, plan, n)
	pollToProposal(t, r, n)
}

// No one can open an issue that claims a plan's authority. As the App's
// bot, the factory opens a plan's first issue and stops before recording
// it. Meanwhile a writer who didn't ratify the plan opens a copy of it,
// numbered before it, and someone who can't write opens another. A fresh
// watcher records only the factory's issue, and takes only that one.
func TestAnIssueClaimingAPlanIsntTakenOnItsAuthority(t *testing.T) {
	r, hub, plan := planRig(t, logPipeline...)
	const bot = "invariant-factory[bot]"
	r.gh.me, r.f.Self = bot, bot
	r.gh.perms["alice"] = "write"
	r.f.GitHub = &stopAtRecord{planHub: hub, plan: plan.Issue}
	if err := r.f.WorkPlan(context.Background(), plan); err != nil {
		t.Logf("the watcher stopped: %v", err)
	}
	if len(hub.opened) != 1 {
		t.Fatalf("the watcher that stopped opened %v; want one issue", hub.opened)
	}
	n := hub.opened[0]
	opened := r.gh.issues[n]
	r.gh.open(5, "alice", opened.Title, opened.Body)
	r.gh.open(6, "mallory", opened.Title, opened.Body)

	r.f = freshWatcher(r, hub)
	workOn(t, r.f, plan)
	expectRecords(t, r, plan, n)
	for _, record := range postsOnPlan(r, plan, KindPlanOpened) {
		for _, copied := range []int{5, 6} {
			if postNamesIssue(record, copied) {
				t.Errorf("the plan's issue records #%d, which the factory didn't open:\n%s", copied, record.Comment.Body)
			}
		}
	}
	pollToProposal(t, r, n)
	workOn(t, r.f, plan)
	r.poll()
	for _, copied := range []int{5, 6} {
		expectUntaken(t, r, copied, "names a plan the factory didn't open it for")
	}
	if len(hub.opened) != 1 {
		t.Errorf("the plan has opened %v; want its one issue", hub.opened)
	}
	expectRecords(t, r, plan, n)
}

// A writer's /invariant stop on the plan's issue stops the plan: it's
// answered once, nothing more opens, and the issue already open goes on as
// it is, to a merge. The plan isn't said to be done. Someone who can't write
// can't stop it.
func TestNothingOpensAfterAStop(t *testing.T) {
	r, hub, plan := planRig(t, logPipeline...)
	workOn(t, r.f, plan)
	mergeThroughFactory(t, r, hub.opened[0])
	unheard := r.gh.say(plan.Issue, "mallory", "/invariant stop")
	workOn(t, r.f, plan)
	if len(hub.opened) != 2 {
		t.Fatalf("with only a reader's stop, the plan has opened %v; want its second issue too", hub.opened)
	}
	second := hub.opened[1]

	stop := r.gh.say(plan.Issue, "gitdek", "/invariant stop")
	workOn(t, r.f, plan)
	workOn(t, r.f, plan)
	answers := 0
	for _, p := range r.gh.posts(plan.Issue) {
		if slices.Contains(p.Marker.ReplyTo, stop.ID) {
			answers++
		}
		if slices.Contains(p.Marker.ReplyTo, unheard.ID) {
			t.Errorf("the factory answered a reader's stop:\n%s", p.Comment.Body)
		}
	}
	if answers != 1 {
		t.Errorf("@gitdek's stop was answered %d times; want once", answers)
	}

	mergeThroughFactory(t, r, second)
	workOn(t, r.f, plan)
	if len(hub.opened) != 2 {
		t.Fatalf("after the stop, the plan has opened %v; want nothing past its second issue", hub.opened)
	}
	if done := postsOnPlan(r, plan, KindPlanDone); len(done) != 0 {
		t.Errorf("a stopped plan was said to be done:\n%s", done[0].Comment.Body)
	}
	expectRecords(t, r, plan, hub.opened...)
}

// When the last of a plan's issues merges, the plan's issue says the plan is
// done, once, naming every issue, and not before. The last merge is a
// person's, which closed the issue as GitHub does, so the factory never
// posts on that issue again: its pull request says it merged.
func TestThePlansIssueSaysWhenEveryIssueHasMerged(t *testing.T) {
	r, hub, plan := planRig(t, logPipeline...)
	for i := 0; i < 2; i++ {
		workOn(t, r.f, plan)
		if len(hub.opened) != i+1 {
			t.Fatalf("the plan has opened %v; want its first %d", hub.opened, i+1)
		}
		mergeThroughFactory(t, r, hub.opened[i])
	}
	workOn(t, r.f, plan)
	if len(hub.opened) != 3 {
		t.Fatalf("the plan has opened %v; want all three", hub.opened)
	}
	last := hub.opened[2]
	pr := pollToPullRequest(t, r, last)
	workOn(t, r.f, plan)
	if done := postsOnPlan(r, plan, KindPlanDone); len(done) != 0 {
		t.Fatalf("the plan's issue says it's done before its last issue merged:\n%s", done[0].Comment.Body)
	}

	merged := r.gh.prs[pr.Marker.PR]
	merged.Merged, merged.State, merged.MergeCommitSHA = true, "closed", "def456merge"
	r.gh.issues[last].State = "closed"
	workOn(t, r.f, plan)
	workOn(t, r.f, plan)
	done := postsOnPlan(r, plan, KindPlanDone)
	if len(done) != 1 {
		t.Fatalf("once every issue merged, the plan's issue says it's done %d times; want once", len(done))
	}
	for _, n := range hub.opened {
		if !postNamesIssue(done[0], n) {
			t.Errorf("the plan's issue says it's done without naming #%d:\n%s", n, done[0].Comment.Body)
		}
	}
	if len(hub.opened) != 3 {
		t.Errorf("the plan has opened %v; want its three issues", hub.opened)
	}
	expectRecords(t, r, plan, hub.opened...)
}
