package factory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gitdek/invariant/factory/protocol/protocol"
	"github.com/gitdek/invariant/factory/recovery/recovery"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
)

// Plumbing issues (D-0105). An issue marked Kind: plumbing gets a plan in
// place of statements. Its ratification records the plan and adds its
// acceptance tests, its build writes only the files the plan names, a second
// agent reviews the change against the plan, and a change to the trusted
// base waits for a person to merge it. The protocol is the same: a plan's
// hash takes a proposal's place, and its record takes a lock's.

// PlanBuilder builds a ratified plan in a checkout of its branch.
// plumbing.Builder is the real one.
type PlanBuilder interface {
	Build(ctx context.Context, root string, n int, out string) (*plumbing.BuildResult, error)
}

// invariantRepository is Invariant's own repository, the only one the
// factory plans plumbing on for now (#103). What a plan's build and checks
// rest on is Invariant's own: the gate, the prompts, the trusted base and
// the sandbox's dependencies.
const invariantRepository = "gitdek/invariant"

// plansPlumbing says whether the factory plans plumbing issues on the
// repository it watches.
func (f *Factory) plansPlumbing() bool {
	return strings.EqualFold(f.Repository, invariantRepository)
}

// isPlan says whether a marker's project is a plumbing plan's record.
func isPlan(project string) bool { return strings.HasPrefix(project, plumbing.LockDir+"/") }

// kindLine reads an issue's Kind: line, such as Kind: plumbing.
func kindLine(body string) string {
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if name, kind, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "kind") {
			return strings.ToLower(strings.Trim(strings.TrimSpace(kind), "`"))
		}
	}
	return ""
}

// lockFileOf is where a ratified proposal is recorded: a project's lock, or
// a plan's record.
func lockFileOf(project string) string {
	if isPlan(project) {
		return project
	}
	return project + "/.invariant/ratified.lock"
}

// lockAt is the ratified proposal at a pull request's head, as the protocol
// sees it: the hash it records, when what's recorded hashes to that.
func lockAt(project string, b []byte, err error) string {
	if !isPlan(project) {
		return headLock(b, err)
	}
	if err != nil {
		return "no ratified plan"
	}
	l, err := plumbing.ReadLock(b)
	if err != nil {
		return "a plan that isn't the one it records: " + err.Error()
	}
	return l.Ratified.Proposal
}

// planBase exports the base branch for a plumbing issue's planner to read
// and check its plan against.
func (f *Factory) planBase(ctx context.Context) (string, error) {
	root, err := os.MkdirTemp("", "invariant-plan-base-")
	if err != nil {
		return "", err
	}
	if err := f.Repo.Fetch(ctx); err != nil {
		os.RemoveAll(root)
		return "", err
	}
	if err := f.Repo.Export(ctx, "origin/"+f.Base, []string{"."}, root); err != nil {
		os.RemoveAll(root)
		return "", err
	}
	return root, nil
}

var notSlug = regexp.MustCompile(`[^a-z0-9]+`)

// planSlug is a branch name's ending, from a plan's name.
func planSlug(name string) string {
	s := strings.Trim(notSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	if s == "" {
		s = "plan"
	}
	return s
}

// planBranch picks the branch for a ratified plan, and the ref to start
// from: a branch an earlier attempt pushed with this plan picks up where it
// stopped, and one left from a different plan gets a numbered successor.
func (f *Factory) planBranch(ctx context.Context, n int, p *formalize.Proposal) (branch, from string, err error) {
	for i := 1; ; i++ {
		branch = fmt.Sprintf("invariant/issue-%d-%s", n, planSlug(p.Plan.Name))
		if i > 1 {
			branch += fmt.Sprintf("-%d", i)
		}
		if _, err := f.Repo.RevParse(ctx, "origin/"+branch); err != nil {
			return branch, "origin/" + f.Base, nil
		}
		if b, err := f.Repo.Show(ctx, "origin/"+branch, plumbing.LockPath(n)); err == nil {
			if l, err := plumbing.ReadLock(b); err == nil && l.Ratified.Proposal == p.Hash {
				return branch, "origin/" + branch, nil
			}
		}
	}
}

// commitPlan pushes a plan's ratification: its record and its acceptance
// tests, on the issue's branch. Then it says so on the issue.
func (f *Factory) commitPlan(ctx context.Context, t Thread, state Post, c Command) (Post, error) {
	n, p := t.Issue.Number, state.Marker.Proposal
	branch, from, err := f.planBranch(ctx, n, p)
	if err != nil {
		return Post{}, err
	}
	named := p.Hash
	if !matches(c.Args, p.Hash) {
		named = "the proposal " + strings.Join(c.Args, " ")
	}
	before, after := f.ratifyStep(t, state, c, named, "")
	if err := f.allowed(ctx, n, "ratify "+named, before, after); err != nil {
		return Post{}, err
	}
	wt, err := f.Repo.Worktree(ctx, branch, from)
	if err != nil {
		return Post{}, err
	}
	defer f.Repo.RemoveWorktree(ctx, wt)
	lock := plumbing.Lock{Ratified: plumbing.Ratification{By: c.By, At: c.At, Issue: n, Comment: c.URL, Proposal: p.Hash}, Plan: *p.Plan}
	if err := lock.Write(wt); err != nil {
		return Post{}, err
	}
	// What's committed must be exactly what was proposed.
	if written, err := plumbing.ReadLockFile(wt, n); err != nil || written.Plan.Hash() != p.Hash {
		return Post{}, fmt.Errorf("the plan written for ratification isn't the proposal %s: %v", p.Hash, err)
	}
	msg := fmt.Sprintf("Ratify the plan for #%d\n\nRatified by @%s in %s.\nPlan %s.", n, c.By, c.URL, p.Hash)
	_, err = f.Repo.Commit(ctx, wt, ".", msg)
	switch {
	case errors.Is(err, ErrNothingToCommit) && from == "origin/"+branch:
	case err != nil && !errors.Is(err, ErrNothingToCommit):
		return Post{}, err
	default:
		if err := f.recovers(ctx, n, "push the ratification", f.canPushRatification(ratifying(false))); err != nil {
			return Post{}, err
		}
		if err := f.Repo.Push(ctx, wt, branch); err != nil {
			return Post{}, err
		}
	}
	if err := f.recovers(ctx, n, "say it's ratified", f.canPost(ratifying(true), recovery.Ratify)); err != nil {
		return Post{}, err
	}
	m := Marker{Kind: KindRatified, ReplyTo: []int64{c.Comment}, Answers: state.Marker.Answers, Proposal: p,
		Project: plumbing.LockPath(n), Branch: branch, Hash: p.Hash}
	posted, err := f.GitHub.PostComment(ctx, n, planRatifiedComment(c.By, branch, n, p, m))
	if err != nil {
		return Post{}, err
	}
	return Post{Comment: posted, Marker: m}, f.status(ctx, n, LabelBuilding)
}

// runPlanBuild records a plan's build, runs it, and commits what it wrote
// on top of the branch, without pushing it, as runBuild does for a project.
func (f *Factory) runPlanBuild(ctx context.Context, t Thread, ratified Post, step string) (string, *plumbing.BuildResult, error, error) {
	n, m := t.Issue.Number, ratified.Marker
	if f.Plumbing == nil {
		return "", nil, errors.New("this watcher can't build plumbing"), nil
	}
	if err := f.Repo.Fetch(ctx); err != nil {
		return "", nil, nil, err
	}
	wt, err := f.Repo.Worktree(ctx, m.Branch, "origin/"+m.Branch)
	if err != nil {
		return "", nil, nil, err
	}
	defer f.Repo.RemoveWorktree(ctx, wt)
	out := filepath.Join(f.Work, fmt.Sprintf("issue-%d", n), "build-"+f.now().Format("20060102-150405"))
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", nil, nil, err
	}
	if err := f.recovers(ctx, n, "start the build's agent run", f.canStartRun(building(RunNone, false, false), recovery.Build)); err != nil {
		return "", nil, nil, err
	}
	if err := f.Repo.Record(ctx, n, step, fmt.Sprintf("the build of #%d's plan", n)); err != nil {
		return "", nil, nil, err
	}
	res, runErr := f.Plumbing.Build(ctx, wt, n, out)
	if !f.holds() {
		return "", nil, nil, errLeaseLost
	}
	if res == nil {
		return "", nil, runErr, nil
	}
	verdict := "It passed every test, and the review approves it."
	switch {
	case !res.Passed:
		verdict = "It didn't pass every test."
	case !res.Approved:
		verdict = "It passed every test, but the review asks for changes."
	}
	msg := fmt.Sprintf("Implement #%d: %s\n\nWritten by Invariant to the plan ratified in the previous commit. %s", n, t.Issue.Title, verdict)
	code, err := f.Repo.Commit(ctx, wt, ".", msg)
	if errors.Is(err, ErrNothingToCommit) {
		code, err = f.Repo.RevParse(ctx, m.Branch)
	}
	if err != nil {
		return "", nil, nil, err
	}
	saved := builtResult{Plumbing: res}
	if runErr != nil {
		saved.Error = runErr.Error()
	}
	result, err := f.saveResult(ctx, saved, fmt.Sprintf("invariant: the result of the build for #%d", n), code)
	if err != nil {
		return "", nil, nil, err
	}
	if err := f.recovers(ctx, n, "record the build's result", f.canFinishRun(building(RunRecorded, false, false), recovery.Build)); err != nil {
		return "", nil, nil, err
	}
	if err := f.Repo.Finish(ctx, n, step, result); err != nil {
		return "", nil, nil, err
	}
	return result, res, runErr, nil
}

// publishPlan takes a finished plan build to its post, as publish does for
// a project: it pushes the code, opens the pull request, and says how the
// build and the review went.
func (f *Factory) publishPlan(ctx context.Context, t Thread, ratified Post, result string, built builtResult, next Marker, stops int) error {
	n, m, res := t.Issue.Number, ratified.Marker, built.Plumbing
	var runErr error
	if built.Error != "" {
		runErr = errors.New(built.Error)
	}
	next.Spend, next.GateRuns = res.Spend, len(res.TestRuns)
	if err := f.Repo.Fetch(ctx); err != nil {
		return err
	}
	code, err := f.Repo.RevParse(ctx, result+"^")
	if err != nil {
		return err
	}
	head := "origin/" + m.Branch
	if has, err := f.Repo.Holds(ctx, head, code); err != nil {
		return err
	} else if !has {
		if err := f.recovers(ctx, n, "push the code", f.canPushCode(building(RunDone, false, false))); err != nil {
			return err
		}
		if err := f.Repo.PushCommit(ctx, code, m.Branch); err != nil {
			return err
		}
		if err := f.Repo.Fetch(ctx); err != nil {
			return err
		}
	}
	at := pullRequest{head: code, gate: protocol.GatePending}
	b, lockErr := f.Repo.Show(ctx, code, m.Project)
	at.lock = lockAt(m.Project, b, lockErr)
	sc, err := f.Repo.Scope(ctx, "origin/"+f.Base, head, n)
	if err != nil {
		return err
	}
	at.scopeMany = len(sc.Problems) > 0 || sc.Project != m.Project
	ok := res.Passed && res.Approved
	from, to := buildStep(ratified, stops, at)
	if !ok {
		to.kind, to.failure = protocol.KindFailed, FailGate
	}
	if err := f.allowed(ctx, n, "open a pull request", from, to); err != nil {
		return err
	}
	pr, open, err := f.GitHub.OpenPullRequest(ctx, m.Branch)
	if err != nil {
		return err
	}
	if !open {
		if err := f.recovers(ctx, n, "open a pull request", f.canOpenPullRequest(building(RunDone, true, false))); err != nil {
			return err
		}
		if pr, err = f.GitHub.CreatePullRequest(ctx, github.NewPullRequest{
			Title: t.Issue.Title, Head: m.Branch, Base: f.Base, Draft: !ok, Body: planPullRequestBody(t, m, res, sc.Trusted),
		}); err != nil {
			return err
		}
	}
	next.PR = pr.Number
	if err := f.recovers(ctx, n, "say how the build went", f.canPost(building(RunDone, true, true), recovery.Build)); err != nil {
		return err
	}
	if !ok {
		if err := f.GitHub.AddLabels(ctx, pr.Number, LabelHumanReview); err != nil {
			return err
		}
		next.Failure = FailGate
		return f.say(ctx, n, planFailedComment(&pr, res, runErr, next), LabelHumanReview)
	}
	next.Kind = KindPR
	return f.say(ctx, n, planPRComment(pr, res, sc.Trusted, next), LabelPR)
}

// waitForPerson says a plan's pull request passed everything, but changes
// the trusted base, so a person merges it (D-0105).
func (f *Factory) waitForPerson(ctx context.Context, t Thread, state Post, pr github.PullRequest, at pullRequest, trusted []string, next Marker) error {
	n := t.Issue.Number
	from, to := prStep(t, state, at, protocol.KindFailed, protocol.Nobody)
	to.failure = FailTrusted
	if err := f.allowed(ctx, n, fmt.Sprintf("say #%d waits for a person to merge it", pr.Number), from, to); err != nil {
		return err
	}
	if err := f.recovers(ctx, n, "say a person merges it", f.canPost(merging(false, false), recovery.Merge)); err != nil {
		return err
	}
	next.Failure = FailTrusted
	return f.say(ctx, n, trustedComment(pr, trusted, next), LabelHumanReview)
}
