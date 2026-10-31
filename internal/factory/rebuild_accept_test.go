package factory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
)

// A writer's retry of a plan's draft whose build failed in the factory's
// own run builds the ratified plan again and replaces the draft (#271): the
// protocol's Rebuild (D-0202), and the recovery core's replacement (D-0203).
// These scenarios run the factory's real poll, publication and command
// paths, with controlled builders and the GitHub and repository fakes.

// rebuild271Builder builds as base does, except that its first fails builds
// fail their tests. It records the ratified plan each build was given, and
// can stop the watcher during a build's agent run.
type rebuild271Builder struct {
	base  *fakePlanBuilder
	fails int
	calls int
	plans []string
	runs  *runs
}

func (b *rebuild271Builder) Build(ctx context.Context, root string, n int, out string) (*plumbing.BuildResult, error) {
	b.calls++
	lock, err := plumbing.ReadLockFile(root, n)
	if err != nil {
		return nil, err
	}
	b.plans = append(b.plans, lock.Plan.Hash())
	if b.runs != nil {
		b.runs.started = append(b.runs.started, "build")
		if b.runs.c != nil {
			b.runs.c.effect("during a build's agent run")
		}
	}
	res, err := b.base.Build(ctx, root, n, out)
	if err != nil || b.calls > b.fails {
		return res, err
	}
	res.Passed, res.Approved = false, false
	res.TestRuns[0].Passed = false
	res.Summary = fmt.Sprintf("The tests failed on build %d.", b.calls)
	res.Review = ""
	return res, nil
}

// rebuild271Start ratifies a plan to change file, whose first fails builds
// fail, and returns the post that the first build failed.
func rebuild271Start(t *testing.T, fails int, file string) (*rig, *rebuild271Builder, Post) {
	t.Helper()
	r, base := plumbingRig(t, file)
	b := &rebuild271Builder{base: base, fails: fails}
	r.f.Plumbing = b
	r.gh.open(1, "gitdek", "Explain retries", "Kind: plumbing\n\n/invariant solve")
	r.poll()
	proposal := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(proposal.Marker.Proposal.Hash, "sha256:")[:hashChars])
	r.poll()
	failed := r.expect(1, KindFailed, LabelHumanReview)
	if failed.Marker.Failure != FailGate || failed.Marker.PR == 0 || !r.gh.prs[failed.Marker.PR].Draft {
		t.Fatalf("fixture must leave a failed draft: %+v", failed.Marker)
	}
	return r, b, failed
}

// closes counts the pull requests the factory closes.
type closes struct {
	GitHub
	closed []int
}

func (g *closes) ClosePullRequest(ctx context.Context, n int) (github.PullRequest, error) {
	g.closed = append(g.closed, n)
	return g.GitHub.ClosePullRequest(ctx, n)
}

// A retry's build can fail again, and a writer can retry again: each retry
// closes the latest draft and builds the same ratified plan once more, on a
// branch of its own, and only the build that passes merges. Every post names
// the draft its build replaces.
func TestASecondFailedAttemptIsRetriedAgain(t *testing.T) {
	r, b, first := rebuild271Start(t, 2, "page/page.go")
	p1 := first.Marker.PR
	r.gh.say(1, "gitdek", "/invariant retry")
	r.poll()
	again := r.expect(1, KindRatified, LabelBuilding)
	r.poll()
	second := r.expect(1, KindFailed, LabelHumanReview)
	p2 := second.Marker.PR
	if p2 == 0 || p2 == p1 || second.Marker.Failure != FailGate || second.Marker.Replaces != p1 || !r.gh.prs[p2].Draft ||
		r.gh.prs[p2].Head.Ref != again.Marker.Branch || r.gh.prs[p1].State != "closed" {
		t.Fatalf("the retry's failed build must leave a draft of its own, replacing #%d: %+v", p1, second.Marker)
	}
	if !strings.Contains(retry263Visible(second.Comment.Body), fmt.Sprintf("replaces #%d", p1)) {
		t.Errorf("the second failure doesn't say which draft it replaced:\n%s", second.Comment.Body)
	}
	retry271RebuildWords(t, second.Comment.Body, p2, false)

	r.gh.say(1, "gitdek", "/invariant retry")
	r.poll()
	third := r.expect(1, KindRatified, LabelBuilding)
	if third.Marker.Replaces != p2 || slices.Contains([]string{first.Marker.Branch, again.Marker.Branch}, third.Marker.Branch) ||
		strings.Count(third.Marker.Branch, "-retry-") != 1 || r.gh.prs[p2].State != "closed" || r.gh.prs[p2].Merged {
		t.Fatalf("the second retry must close #%d and build on a branch of its own: %+v", p2, third.Marker)
	}
	r.poll()
	built := r.expect(1, KindPR, LabelPR)
	p3 := built.Marker.PR
	if slices.Contains([]int{0, p1, p2}, p3) || built.Marker.Replaces != p2 || r.gh.prs[p3].Draft {
		t.Fatalf("the third build passed, so it opens a ready pull request of its own, replacing #%d: %+v", p2, built.Marker)
	}
	r.gh.ci(p3, "success")
	r.poll()
	r.expect(1, KindMerged, LabelMerged)
	if !reflect.DeepEqual(r.gh.merged, []int{p3}) || len(r.gh.prs) != 3 || b.calls != 3 {
		t.Fatalf("only the third build's pull request merges: merged %v, %d pull requests, %d builds", r.gh.merged, len(r.gh.prs), b.calls)
	}
	for _, p := range []int{p1, p2} {
		if r.gh.prs[p].Merged || r.gh.prs[p].State != "closed" {
			t.Errorf("#%d, a draft a retry replaced, isn't closed as it was: %+v", p, r.gh.prs[p])
		}
	}
	if len(slices.Compact(slices.Clone(b.plans))) != 1 || b.plans[0] != first.Marker.Hash {
		t.Errorf("every build must build the one ratified plan, %s: %v", first.Marker.Hash, b.plans)
	}
}

// A retry after CI's gate failed on a plan's pull request still watches it
// again: the same pull request, on its current head, with no build, no
// close and no record that anything replaces it.
func TestACIRetryNeverReplacesThePullRequest(t *testing.T) {
	r, base := plumbingRig(t, "page/page.go")
	b := &rebuild271Builder{base: base}
	r.f.Plumbing = b
	r.gh.open(1, "gitdek", "Explain retries", "Kind: plumbing\n\n/invariant solve")
	r.poll()
	proposal := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(proposal.Marker.Proposal.Hash, "sha256:")[:hashChars])
	r.poll()
	pr := r.expect(1, KindPR, LabelPR).Marker.PR
	r.gh.ci(pr, "failure")
	r.poll()
	failed := r.expect(1, KindFailed, LabelHumanReview)
	if failed.Marker.Failure != FailCI || failed.Marker.Rebuilds() {
		t.Fatalf("fixture must fail CI's gate on a ready pull request: %+v", failed.Marker)
	}
	g := &closes{GitHub: r.f.GitHub}
	r.f.GitHub = g
	before := *r.gh.prs[pr]
	command := r.gh.say(1, "gitdek", "/invariant retry")
	r.poll()
	answer := r.expect(1, KindPR, LabelPR)
	retry263WatchWords(t, answer.Comment.Body, pr, false)
	recorded, err := r.f.Repo.Recorded(context.Background(), 1, replaceStepName(Command{Comment: command.ID}))
	if answer.Marker.PR != pr || answer.Marker.Replaces != 0 || b.calls != 1 || len(g.closed) != 0 || len(r.gh.prs) != 1 ||
		!reflect.DeepEqual(*r.gh.prs[pr], before) || recorded != "" || err != nil {
		t.Fatalf("a CI retry must watch #%d again, building, closing and recording nothing: %+v, %d builds, closed %v, record %q", pr, answer.Marker, b.calls, g.closed, recorded)
	}
	// It goes on checking the pull request's current head.
	r.gh.ci(pr, "success")
	r.poll()
	r.expect(1, KindMerged, LabelMerged)
	if !reflect.DeepEqual(r.gh.merged, []int{pr}) || b.calls != 1 {
		t.Fatalf("the watched pull request merges once CI's gate passes on its head: %v", r.gh.merged)
	}
}

// fileAt is a file as the origin holds it at commit.
func fileAt(t *testing.T, r *rig, commit, path string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", r.origin, "show", commit+":"+path).CombinedOutput()
	if err != nil {
		t.Fatalf("git show %s:%s: %v\n%s", commit, path, err, out)
	}
	return string(out)
}

// realCLI271 is a stand-in Codex for the agent or the review. The agent
// writes code that passes the checks, and, while fail exists in its
// directory, ends its run with an error that isn't its provider's. The
// review approves.
func realCLI271(t *testing.T, stage string, failing bool) (synth.Backend, string) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
n=0
[ ! -f "$here/count" ] || n=$(cat "$here/count")
n=$((n + 1))
printf '%s\n' "$n" > "$here/count"
if [ STAGE = agent ]; then
  mkdir -p page
  printf '%s\n' 'package page' 'func Hello() string { return "hello" }' "// build attempt $n" > page/page.go
fi
if [ -f "$here/fail" ]; then
  printf '%s\n' '{"type":"turn.failed","error":{"message":"the agent tripped over its own tool call (STAGE attempt '"$n"')"}}'
  exit 1
fi
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"VERDICT: approve"}}' '{"type":"turn.completed"}'
`
	bin := filepath.Join(dir, "codex")
	if err := os.WriteFile(bin, []byte(strings.ReplaceAll(script, "STAGE", stage)), 0o700); err != nil {
		t.Fatal(err)
	}
	if failing {
		if err := os.WriteFile(filepath.Join(dir, "fail"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return synth.Codex{Binary: bin, Home: t.TempDir(), MaxTurns: 20}, dir
}

// The real plumbing.Builder, with a stand-in agent that leaves work that
// passes the checks and then ends its run with an error: the build failed,
// so its work is published as a failed draft, unreviewed, and a writer's
// retry builds the plan again, reviewed this time, replacing the draft.
func TestTheRealBuilderAgentErrorLeavesADraftARetryRebuilds(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(providerFactoryDocker), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r, _ := plumbingRig(t, "page/page.go")
	agent, builds := realCLI271(t, "agent", true)
	reviewer, reviews := realCLI271(t, "review", false)
	r.f.Plumbing = plumbing.Builder{Backend: agent, Reviewer: reviewer, Binary: "invariant", TestRuns: 4,
		Timeout: 5 * time.Second, Sandbox: plumbing.Sandbox{Image: "fixture", ModCache: t.TempDir()}}
	r.gh.open(1, "gitdek", "Add a hello page", "Kind: plumbing\n\n/invariant solve")
	r.poll()
	proposal := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(proposal.Marker.Proposal.Hash, "sha256:"))
	r.poll()
	failed := r.expect(1, KindFailed, LabelHumanReview)
	old := failed.Marker.PR
	if failed.Marker.Failure != FailGate || old == 0 || !r.gh.prs[old].Draft || len(r.gh.merged) != 0 {
		t.Fatalf("an agent that ended with an error must leave a failed draft: %+v", failed.Marker)
	}
	for _, want := range []string{"agent stopped with an error", "tripped over its own tool call"} {
		if !strings.Contains(failed.Comment.Body, want) {
			t.Errorf("the failure doesn't say %q:\n%s", want, failed.Comment.Body)
		}
	}
	retry271RebuildWords(t, failed.Comment.Body, old, false)
	if providerFactoryAttempts(t, reviews) != 0 {
		t.Error("the review ran on work whose agent ended with an error")
	}
	if left := fileAt(t, r, r.gh.prs[old].Head.SHA, "page/page.go"); !strings.Contains(left, "build attempt 1") {
		t.Errorf("the failed draft must hold the work its agent left: %q", left)
	}

	if err := os.Remove(filepath.Join(builds, "fail")); err != nil {
		t.Fatal(err)
	}
	r.gh.say(1, "gitdek", "/invariant retry")
	r.poll()
	retrying := r.expect(1, KindRatified, LabelBuilding)
	if retrying.Marker.Replaces != old || retrying.Marker.Hash != failed.Marker.Hash || r.gh.prs[old].State != "closed" {
		t.Fatalf("the retry must close #%d and build the plan again: %+v", old, retrying.Marker)
	}
	r.poll()
	built := r.expect(1, KindPR, LabelPR)
	if built.Marker.PR == old || built.Marker.Replaces != old || r.gh.prs[built.Marker.PR].Draft || providerFactoryAttempts(t, builds) != 2 || providerFactoryAttempts(t, reviews) != 1 {
		t.Fatalf("the retry's build must run, be reviewed and open a ready pull request of its own: %+v", built.Marker)
	}
	r.gh.ci(built.Marker.PR, "success")
	r.poll()
	r.expect(1, KindMerged, LabelMerged)
	if !reflect.DeepEqual(r.gh.merged, []int{built.Marker.PR}) || r.gh.prs[old].Merged {
		t.Fatalf("only the rebuilt, reviewed pull request merges: %v", r.gh.merged)
	}
}

// recordHook does something just after the factory stores a retry's record
// that it replaces a draft, as someone else might.
type recordHook struct {
	Repo
	after func()
}

func (h recordHook) Record(ctx context.Context, issue int, step, what string) error {
	err := h.Repo.Record(ctx, issue, step, what)
	if strings.HasPrefix(step, "replace-") {
		h.after()
	}
	return err
}

// Someone else can close or merge the draft before the factory replaces it.
// A draft merged is kept, before the retry's record or after it, and the
// retry is answered without a build. A draft closed before the record is
// someone else's close, and the retry is answered without a build too. One
// closed after the record is the close the retry asked for, so its build
// goes on, without closing it again.
func TestADraftClosedOrMergedAroundTheRecord(t *testing.T) {
	merge := func(r *rig, n int) func() {
		return func() {
			pr := r.gh.prs[n]
			pr.State, pr.Merged, pr.MergedAt = "closed", true, "2026-09-25T12:30:00Z"
		}
	}
	shut := func(r *rig, n int) func() { return func() { r.gh.prs[n].State = "closed" } }
	for _, c := range []struct {
		name    string
		before  func(r *rig, n int) func()
		after   func(r *rig, n int) func()
		rebuild bool
	}{
		{"merged before the record", merge, nil, false},
		{"closed before the record", shut, nil, false},
		{"merged after the record", nil, merge, false},
		{"closed after the record", nil, shut, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, b, failed := rebuild271Start(t, 1, "page/page.go")
			old := failed.Marker.PR
			g := &closes{GitHub: r.f.GitHub}
			r.f.GitHub = g
			if c.before != nil {
				c.before(r, old)()
			}
			if c.after != nil {
				r.f.Repo = recordHook{Repo: r.f.Repo, after: c.after(r, old)}
			}
			command := r.gh.say(1, "gitdek", "/invariant retry")
			for i := 0; i < 3; i++ {
				r.poll()
			}
			answers := 0
			for _, p := range r.gh.posts(1) {
				if slices.Contains(p.Marker.ReplyTo, command.ID) {
					answers++
				}
			}
			if answers != 1 || len(g.closed) != 0 {
				t.Fatalf("the retry must be answered once, and the factory must close nothing someone else closed or merged: %d answers, closed %v", answers, g.closed)
			}
			if !c.rebuild {
				if b.calls != 1 || len(r.gh.prs) != 1 || len(r.gh.merged) != 0 {
					t.Fatalf("a retry whose draft someone else closed or merged first builds nothing: %d builds, %d pull requests", b.calls, len(r.gh.prs))
				}
				return
			}
			built := r.gh.last(1)
			if b.calls != 2 || len(r.gh.prs) != 2 || built.Marker.PR == old || built.Marker.Replaces != old {
				t.Fatalf("a close after the retry's record is the one it asked for, so its build goes on: %d builds, %+v", b.calls, built.Marker)
			}
		})
	}
}

// A watcher without the lease closes nothing.
func TestNoCloseWithoutTheLease(t *testing.T) {
	ctx := context.Background()
	origin, _ := gitRepos(t)
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	f := leaseFactory(t, origin, "watcher-a", &now)
	gh := f.GitHub.(*fakeGitHub)
	pr := &github.PullRequest{Number: 7, State: "open", Draft: true}
	gh.prs[pr.Number] = pr
	g := leasedGitHub{f.GitHub, f}
	if _, err := g.ClosePullRequest(ctx, pr.Number); !errors.Is(err, errLeaseLost) || gh.prs[pr.Number].State != "open" {
		t.Fatalf("a close without the lease = %v, and #%d is %s; want errLeaseLost, and open", err, pr.Number, gh.prs[pr.Number].State)
	}
	if held, err := f.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	if closed, err := g.ClosePullRequest(ctx, pr.Number); err != nil || closed.State != "closed" || gh.prs[pr.Number].Merged {
		t.Fatalf("a close with the lease = %+v, %v", closed, err)
	}
}

// rebuildCrashRun takes a plan's failed draft through a writer's retry to a
// merge, stopping the watcher at effect at, counted from the retry, and
// starting a fresh one in its place, on this machine or, with elsewhere, on
// another. It returns what the watcher stopped at, the rig, the builder, the
// draft the retry replaces, and how many effects it counted. With at 0, it
// never stops.
func rebuildCrashRun(t *testing.T, at int, elsewhere bool) (string, *rig, *rebuild271Builder, int, int) {
	t.Helper()
	ctx := context.Background()
	r, b, failed := rebuild271Start(t, 1, "page/page.go")
	c := &crasher{at: at}
	b.runs = &runs{c: c}
	watcher := func(repo Repo, work string) *Factory {
		return &Factory{Repository: r.f.Repository, GitHub: crashGitHub{r.gh, c}, Repo: crashRepo{repo, c},
			Formalizer: r.f.Formalizer, Builder: r.f.Builder, Plumbing: b,
			Base: r.f.Base, Projects: r.f.Projects, Work: work, Check: r.f.Check, Log: t.Logf}
	}
	f := watcher(r.f.Repo, r.f.Work)
	r.gh.say(1, "gitdek", "/invariant retry")
	said := map[string]bool{}
	for i := 0; i < 30; i++ {
		err := f.Poll(ctx)
		switch {
		case err != nil && strings.Contains(err.Error(), errCrash.Error()) && c.at > 0:
			c.at = -1
			if !elsewhere {
				f = watcher(r.f.Repo, r.f.Work)
				continue
			}
			clone := r.f.Repo.(Clone)
			clone.Dir = t.TempDir() + "/clone"
			if err := clone.Ensure(ctx); err != nil {
				t.Fatal(err)
			}
			f = watcher(clone, t.TempDir())
			continue
		case err != nil:
			t.Fatalf("after stopping at %q: %v", c.what, err)
		}
		last := r.gh.last(1)
		switch {
		case last.Marker.Kind == KindMerged:
			return c.what, r, b, failed.Marker.PR, c.n
		case last.Marker.Kind == KindPR && !said["ci"]:
			said["ci"] = true
			r.gh.ci(last.Marker.PR, "success")
		case last.Marker.Kind == KindFailed && stoppedBuild(last) && !said["retry"]:
			said["retry"] = true
			r.gh.say(1, "gitdek", "/invariant retry")
		}
	}
	t.Fatalf("after stopping at %q, the issue never merged: its last post is %q", c.what, r.gh.last(1).Marker.Kind)
	return "", nil, nil, 0, 0
}

// A watcher can stop just before any effect a retry's replacement takes:
// its record, the draft's close, the answer, the build's run and result,
// the push, the pull request, the merge. A fresh watcher, here or on
// another machine, still replaces the draft, with no retry lost or answered
// twice, no build run twice, no second successor and no merge of the draft.
func TestARebuildSurvivesAStopAtEachEffect(t *testing.T) {
	_, _, _, _, effects := rebuildCrashRun(t, 0, false)
	for _, elsewhere := range []bool{false, true} {
		where := "the same machine"
		if elsewhere {
			where = "another machine"
		}
		for at := 1; at <= effects; at++ {
			t.Run(fmt.Sprintf("%s/effect %d", where, at), func(t *testing.T) {
				t.Parallel()
				what, r, b, old, _ := rebuildCrashRun(t, at, elsewhere)
				t.Logf("stopped %s", what)
				answers := map[int64]int{}
				for _, p := range r.gh.posts(1) {
					for _, id := range p.Marker.ReplyTo {
						answers[id]++
					}
				}
				retries := 0
				for _, c := range r.gh.comments[1] {
					// The factory's own posts carry a marker, and offer
					// retries rather than give them.
					if _, ours := DecodeMarker(c.Body); ours || !hasVerb(ParseCommands(c.Body), Retry) {
						continue
					}
					retries++
					if answers[c.ID] != 1 {
						t.Errorf("a retry was answered %d times", answers[c.ID])
					}
				}
				if b.calls < 2 || b.calls > 1+retries {
					t.Errorf("%d builds for the first and %d retries", b.calls, retries)
				}
				last := r.gh.last(1)
				next := last.Marker.PR
				if len(r.gh.prs) != 2 || next == old || !reflect.DeepEqual(r.gh.merged, []int{next}) || last.Marker.Numbers == nil {
					t.Errorf("want the draft and one successor, the successor merged by the factory: %d pull requests, merged %v", len(r.gh.prs), r.gh.merged)
				}
				if pr := r.gh.prs[old]; pr.State != "closed" || pr.Merged {
					t.Errorf("#%d, the replaced draft, isn't closed as it was: %+v", old, pr)
				}
				for _, p := range r.gh.posts(1) {
					if p.Marker.Kind == KindPR && p.Marker.PR == next && p.Marker.Replaces != old {
						t.Errorf("the successor's post doesn't say it replaces #%d: %+v", old, p.Marker)
					}
				}
			})
		}
	}
}
