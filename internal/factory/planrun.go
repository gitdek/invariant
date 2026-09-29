package factory

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/gitdek/invariant/factory/plans/plans"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
)

// A ratified plan of issues is worked one issue at a time (#126), as
// factory/plans has it. #94 ratified its rules, and the factory wrote them
// as Go that Gobra proves, with a crash between any two effects and a second
// watcher. The watcher opens the plan's issues in order, each once the one
// before it has merged, records each on the plan's issue with its number,
// and says there once every one has merged. It solves each on the authority
// of the writer who ratified the plan. After a writer's /invariant stop on
// the plan's issue, it opens no more, and one already open goes on as it is.
//
// Before each effect, opening an issue, recording it or saying the plan is
// done, the watcher describes the plan in the core's terms, as GitHub shows
// it, and takes the effect only if the core allows it. It remembers nothing
// across a restart, so before it opens an issue, it looks for one it already
// opened for that step: a watcher can stop between opening an issue and
// recording it, and the issue it left is recorded, not opened again.
//
// #112 drafts and ratifies a plan, and WorkPlan is handed one to work,
// within a poll or on its own.

// PlanOfIssues is a ratified plan of issues: the issue it was ratified on,
// its hash, the writer who ratified it, the ratifying comment's URL, and its
// issues, in order.
type PlanOfIssues struct {
	Issue   int
	Hash    string
	By      string
	Comment string
	Issues  []PlannedIssue
}

// PlannedIssue is one of a plan's issues, as it's opened: its title and
// body.
type PlannedIssue = formalize.PlannedIssue

// The plan's ratifier, this watcher and any other, in the core's terms.
const (
	theRatifier  = 1
	thisWatcher  = 1
	otherWatcher = 2
)

// errOutsidePlan is what an effect the core doesn't allow fails with.
var errOutsidePlan = errors.New("the factory's rules for a plan of issues don't allow this effect")

// WorkPlan takes a ratified plan's next steps. It answers each writer's
// /invariant stop on the plan's issue once, with a note, and records any
// issue it opened for the plan and hasn't recorded. Unless a writer has
// stopped the plan, it opens the next issue once the one before it has
// merged, and records it. Once every issue has merged, it says so, naming
// each.
func (f *Factory) WorkPlan(ctx context.Context, plan PlanOfIssues) error {
	if len(plan.Issues) == 0 {
		return fmt.Errorf("the plan on #%d has no issues", plan.Issue)
	}
	// On its own, outside a poll, the watcher may not have asked who can
	// write yet.
	f.mu.Lock()
	if f.writers == nil {
		f.writers, f.asked = map[string]bool{}, map[string]bool{}
	}
	f.mu.Unlock()
	issue, err := f.GitHub.Issue(ctx, plan.Issue)
	if err != nil {
		return err
	}
	t, err := f.read(ctx, issue)
	if err != nil {
		return err
	}
	answered := map[int64]bool{}
	for _, c := range t.Pending() {
		if c.Verb != Stop || answered[c.Comment] {
			continue
		}
		if err := f.note(ctx, t, c, "Stopped: I won't open any more of this plan's issues. Any of them that's open goes on as it is."); err != nil {
			return err
		}
		answered[c.Comment] = true
	}
	run, err := f.planRun(ctx, plan, t)
	if err != nil {
		return err
	}
	for k, s := range run.steps {
		if s.number != 0 && !s.recorded {
			if err := f.recordStep(ctx, plan, &run, k); err != nil {
				return err
			}
		}
	}
	if k := run.opened(); !run.stopped && k < len(run.steps) && (k == 0 || run.steps[k-1].status == plans.Merged) {
		if err := f.openStep(ctx, plan, &run, k); err != nil {
			return err
		}
		if err := f.recordStep(ctx, plan, &run, k); err != nil {
			return err
		}
	}
	if k := run.opened(); !run.done && k == len(run.steps) && run.steps[k-1].status == plans.Merged {
		return f.finishPlan(ctx, plan, run)
	}
	return nil
}

// planRun is a plan as GitHub shows it: the issue opened for each of its
// steps, whether a writer has stopped it, and whether its issue says it's
// done.
type planRun struct {
	steps   []stepIssue
	stopped bool
	done    bool
}

// stepIssue is the issue opened for one of a plan's steps: its number, 0
// until it's opened, whether the plan's issue records it, and its status in
// the core's terms.
type stepIssue struct {
	number   int
	recorded bool
	status   int
}

// opened is how many of the plan's steps have their issue, from the first.
func (r planRun) opened() int {
	for k, s := range r.steps {
		if s.number == 0 {
			return k
		}
	}
	return len(r.steps)
}

// planRun reads a plan as GitHub shows it: the factory's records and its
// post that the plan is done, on the plan's issue t; a writer's stop there;
// each recorded issue's status; and, once the last of those has merged, an
// issue opened for the next step that isn't recorded yet.
func (f *Factory) planRun(ctx context.Context, plan PlanOfIssues, t Thread) (planRun, error) {
	run := planRun{steps: make([]stepIssue, len(plan.Issues)), stopped: hasVerb(t.Commands, Stop)}
	for _, p := range t.Posts {
		switch m := p.Marker; {
		case m.Hash != plan.Hash:
		case m.Kind == KindPlanDone:
			run.done = true
		case m.Kind == KindPlanOpened && m.Step >= 1 && m.Step <= len(run.steps) && m.Opened > 0 && run.steps[m.Step-1].number == 0:
			run.steps[m.Step-1] = stepIssue{number: m.Opened, recorded: true}
		}
	}
	for k, s := range run.steps {
		if s.number == 0 {
			continue
		}
		status, err := f.planStatus(ctx, s.number)
		if err != nil {
			return run, err
		}
		run.steps[k].status = status
	}
	if k := run.opened(); k < len(run.steps) && (k == 0 || run.steps[k-1].status == plans.Merged) {
		n, err := f.openedFor(ctx, plan, run, k)
		if err != nil || n == 0 {
			return run, err
		}
		status, err := f.planStatus(ctx, n)
		if err != nil {
			return run, err
		}
		run.steps[k] = stepIssue{number: n, status: status}
	}
	return run, nil
}

// openedFor finds an issue opened for the plan's step k, counting from 0,
// that the plan's issue doesn't record: an open issue whose marker names the
// plan's issue, hash and step, opened by the factory's bot, or by a writer
// when there's no bot. It's 0 when there's none.
func (f *Factory) openedFor(ctx context.Context, plan PlanOfIssues, run planRun, k int) (int, error) {
	issues, err := f.GitHub.OpenIssues(ctx)
	if err != nil {
		return 0, err
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	want := stepMark{plan: plan.Issue, hash: plan.Hash, step: k + 1}
	for _, is := range issues {
		mark, ok := markOf(is.Body)
		if !ok || mark != want || slices.ContainsFunc(run.steps, func(s stepIssue) bool { return s.number == is.Number }) {
			continue
		}
		if f.Self != "" {
			if is.User.Login == f.Self {
				return is.Number, nil
			}
			continue
		}
		if ok, err := f.writer(ctx, is.User.Login); err != nil {
			return 0, err
		} else if ok {
			return is.Number, nil
		}
	}
	return 0, nil
}

// planStatus is issue n's status in the core's terms. It has merged once
// the factory's latest post on it says so, or the pull request that post
// names has merged, as when a person merged it and so closed the issue.
func (f *Factory) planStatus(ctx context.Context, n int) (int, error) {
	posts, err := f.postsOn(ctx, n)
	if err != nil {
		return 0, err
	}
	state, _ := Thread{Posts: posts}.State()
	m := state.Marker
	if m.Kind == KindMerged {
		return plans.Merged, nil
	}
	if m.PR != 0 {
		pr, err := f.GitHub.PullRequest(ctx, m.PR)
		if err != nil {
			return 0, err
		}
		if pr.Merged {
			return plans.Merged, nil
		}
	}
	if m.Kind == KindFailed {
		return plans.Failed, nil
	}
	return plans.Open, nil
}

// postsOn reads the factory's posts on issue n, as read does.
func (f *Factory) postsOn(ctx context.Context, n int) ([]Post, error) {
	comments, err := f.GitHub.Comments(ctx, n)
	if err != nil {
		return nil, err
	}
	var posts []Post
	for _, c := range comments {
		if m, ok := DecodeMarker(c.Body); ok && (f.Self == "" || c.User.Login == f.Self) {
			posts = append(posts, Post{Comment: c, Marker: m})
		}
	}
	return posts, nil
}

// plannedBy is the writer on whose authority an issue the factory opened for
// a plan is solved: the plan's ratifier, once the factory's own record on
// the plan's issue names the issue for the step its marker names, and while
// the ratifier can still write. It's "" for any other issue, such as a copy
// of one that claims a plan's authority.
func (f *Factory) plannedBy(ctx context.Context, issue github.Issue) (string, error) {
	mark, ok := markOf(issue.Body)
	if !ok {
		return "", nil
	}
	// Anyone can write the marker, naming any issue, or none.
	posts, err := f.postsOn(ctx, mark.plan)
	if errors.Is(err, github.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	for _, p := range posts {
		m := p.Marker
		if m.Kind != KindPlanOpened || m.Opened != issue.Number || m.Hash != mark.hash || m.Step != mark.step || m.Ratifier == "" {
			continue
		}
		if ok, err := f.writer(ctx, m.Ratifier); err != nil || !ok {
			return "", err
		}
		return m.Ratifier, nil
	}
	return "", nil
}

// planCore is the plan in the core's terms, as GitHub shows it, with this
// watcher holding the lease or not. Issue k+1 is the one for step k+1, and
// each is solved on the ratifier's authority.
func (f *Factory) planCore(plan PlanOfIssues, run planRun) *plans.Plan {
	pl := plans.New(len(run.steps))
	if plan.By != "" {
		pl.Ratifier = theRatifier
	}
	pl.Count = run.opened()
	for k := 0; k < pl.Count; k++ {
		s := run.steps[k]
		pl.Status[k], pl.Author[k], pl.Recorded[k] = s.status, pl.Ratifier, s.recorded
	}
	if run.stopped {
		// No issue opens after a stop, so as many have opened as when it came.
		pl.StopMark = pl.Count
	}
	pl.Done = run.done
	pl.Leader = otherWatcher
	if f.holds() {
		pl.Leader = thisWatcher
	}
	return pl
}

// planAllows checks an effect on a plan against the core. A refused effect
// labels the plan's issue for a person, as a step outside the protocol does.
func (f *Factory) planAllows(ctx context.Context, issue int, what string, ok bool) error {
	if ok {
		return nil
	}
	f.logf("#%d: refused to %s: the plan's rules don't allow it", issue, what)
	if err := f.status(ctx, issue, LabelHumanReview); err != nil {
		f.logf("#%d: %v", issue, err)
	}
	return fmt.Errorf("%w: %s", errOutsidePlan, what)
}

// openStep opens the issue for the plan's step k, counting from 0, once the
// core allows it: titled as the step, with no labels.
func (f *Factory) openStep(ctx context.Context, plan PlanOfIssues, run *planRun, k int) error {
	what := fmt.Sprintf("open the issue for step %d", k+1)
	if err := f.planAllows(ctx, plan.Issue, what, f.planCore(plan, *run).Create(thisWatcher)); err != nil {
		return err
	}
	is, err := f.GitHub.CreateIssue(ctx, github.NewIssue{Title: plan.Issues[k].Title, Body: plannedBody(plan, k)})
	if err != nil {
		return err
	}
	f.logf("#%d: opened #%d for step %d of its plan", plan.Issue, is.Number, k+1)
	run.steps[k] = stepIssue{number: is.Number, status: plans.Open}
	return nil
}

// recordStep records the issue opened for the plan's step k on the plan's
// issue, once the core allows it.
func (f *Factory) recordStep(ctx context.Context, plan PlanOfIssues, run *planRun, k int) error {
	n := run.steps[k].number
	if err := f.planAllows(ctx, plan.Issue, fmt.Sprintf("record #%d", n), f.planCore(plan, *run).Record(thisWatcher, k+1)); err != nil {
		return err
	}
	m := Marker{Kind: KindPlanOpened, Hash: plan.Hash, Step: k + 1, Opened: n, Ratifier: plan.By}
	if _, err := f.GitHub.PostComment(ctx, plan.Issue, planOpenedComment(plan, k, n, m)); err != nil {
		return err
	}
	run.steps[k].recorded = true
	return nil
}

// finishPlan says on the plan's issue that every one of its issues has
// merged, once the core allows it.
func (f *Factory) finishPlan(ctx context.Context, plan PlanOfIssues, run planRun) error {
	if err := f.planAllows(ctx, plan.Issue, "say the plan is done", f.planCore(plan, run).Finish(thisWatcher)); err != nil {
		return err
	}
	var numbers []int
	for _, s := range run.steps {
		numbers = append(numbers, s.number)
	}
	_, err := f.GitHub.PostComment(ctx, plan.Issue, planDoneComment(numbers, Marker{Kind: KindPlanDone, Hash: plan.Hash}))
	return err
}

// stepMark is the hidden line that ties an issue to the step of a plan it
// was opened for: the plan's issue, its hash, and the step, counting from 1.
type stepMark struct {
	plan int
	hash string
	step int
}

var stepMarkRE = regexp.MustCompile(`<!-- invariant-plan #(\d+) (\S+) step (\d+) -->`)

func (s stepMark) String() string {
	return fmt.Sprintf("<!-- invariant-plan #%d %s step %d -->", s.plan, s.hash, s.step)
}

// markOf reads the last such line in an issue's body, where the factory
// writes it.
func markOf(body string) (stepMark, bool) {
	found := stepMarkRE.FindAllStringSubmatch(body, -1)
	if len(found) == 0 {
		return stepMark{}, false
	}
	last := found[len(found)-1]
	plan, err := strconv.Atoi(last[1])
	if err != nil {
		return stepMark{}, false
	}
	step, err := strconv.Atoi(last[3])
	if err != nil {
		return stepMark{}, false
	}
	return stepMark{plan: plan, hash: last[2], step: step}, true
}

// plannedBody is the body of the issue for the plan's step k, counting from
// 0: the step's text without command lines, since the factory takes the
// issue itself; a line naming the plan's issue, its ratifier and the
// ratifying comment; and the hidden line that ties the issue to its step.
func plannedBody(plan PlanOfIssues, k int) string {
	mark := stepMark{plan: plan.Issue, hash: plan.Hash, step: k + 1}
	return fmt.Sprintf("%s\n\n◉ Opened by Invariant for step %d of %d of the plan of issues on #%d, which @%s [ratified](%s).\n\n%s\n",
		withoutCommands(plan.Issues[k].Body), k+1, len(plan.Issues), plan.Issue, plan.By, plan.Comment, mark)
}

// planOpenedComment records on the plan's issue the issue n, opened for its
// step k, counting from 0.
func planOpenedComment(plan PlanOfIssues, k, n int, m Marker) string {
	then := "I'll open the next step's issue once it merges, unless a writer comments `/invariant stop` here."
	if k == len(plan.Issues)-1 {
		then = "It's the plan's last issue."
	}
	return post("issue opened", fmt.Sprintf("I opened #%d for step %d of %d of this plan: **%s**. I'll solve it on the authority of @%s, who ratified the plan. %s",
		n, k+1, len(plan.Issues), plan.Issues[k].Title, plan.By, then), m)
}

// planDoneComment says on the plan's issue that every one of its issues has
// merged, naming each.
func planDoneComment(numbers []int, m Marker) string {
	named := make([]string, len(numbers))
	for i, n := range numbers {
		named[i] = fmt.Sprintf("#%d", n)
	}
	issues := named[0]
	if len(named) > 1 {
		issues = strings.Join(named[:len(named)-1], ", ") + " and " + named[len(named)-1]
	}
	return post("plan done", fmt.Sprintf("Every issue of this plan has merged: %s. The plan is done.", issues), m)
}
