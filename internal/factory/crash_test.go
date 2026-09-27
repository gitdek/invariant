package factory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/synth"
)

// These tests stop the watcher just before each effect it takes, from
// opening an issue to merging its pull request, and start a fresh one in
// its place: on the same machine, or on another (D-0069, slice 9). The issue
// must still merge, with every effect done once: one post per command, one
// agent run per command or build, one pull request and one merge, recorded
// as the factory's.

// errCrash stands for the watcher stopping.
var errCrash = errors.New("the watcher stopped here")

// crasher counts effects and stops the watcher at effect number at, by
// panicking, which aborts the step like a crash: nothing after it happens.
type crasher struct {
	n, at int
	what  string // the effect it stopped at
}

func (c *crasher) effect(what string) {
	c.n++
	if c.n == c.at {
		c.what = what
		panic(errCrash)
	}
}

type crashGitHub struct {
	GitHub
	c *crasher
}

func (g crashGitHub) PostComment(ctx context.Context, issue int, body string) (github.Comment, error) {
	g.c.effect("post " + firstLine(body))
	return g.GitHub.PostComment(ctx, issue, body)
}

func (g crashGitHub) AddLabels(ctx context.Context, issue int, labels ...string) error {
	g.c.effect("add labels " + strings.Join(labels, ", "))
	return g.GitHub.AddLabels(ctx, issue, labels...)
}

func (g crashGitHub) CreatePullRequest(ctx context.Context, pr github.NewPullRequest) (github.PullRequest, error) {
	g.c.effect("open a pull request")
	return g.GitHub.CreatePullRequest(ctx, pr)
}

func (g crashGitHub) Merge(ctx context.Context, n int, sha, method string) (string, error) {
	g.c.effect("merge")
	return g.GitHub.Merge(ctx, n, sha, method)
}

func (g crashGitHub) DeleteBranch(ctx context.Context, branch string) error {
	g.c.effect("delete the branch")
	return g.GitHub.DeleteBranch(ctx, branch)
}

type crashRepo struct {
	Repo
	c *crasher
}

func (r crashRepo) Push(ctx context.Context, worktree, branch string) error {
	r.c.effect("push " + branch)
	return r.Repo.Push(ctx, worktree, branch)
}

func (r crashRepo) PushCommit(ctx context.Context, commit, branch string) error {
	r.c.effect("push the code to " + branch)
	return r.Repo.PushCommit(ctx, commit, branch)
}

func (r crashRepo) Record(ctx context.Context, issue int, step, what string) error {
	r.c.effect("record " + step)
	return r.Repo.Record(ctx, issue, step, what)
}

func (r crashRepo) Finish(ctx context.Context, issue int, step, result string) error {
	r.c.effect("record the result of " + step)
	return r.Repo.Finish(ctx, issue, step, result)
}

// runs counts the agent runs started, by what started them. A crash during
// a run stops the watcher after the run started and before it finished.
type runs struct {
	started []string
	c       *crasher
}

type crashFormalizer struct {
	Formalizer
	r *runs
}

func (f crashFormalizer) Formalize(ctx context.Context, req formalize.Request, out string) (*formalize.Result, error) {
	f.r.started = append(f.r.started, "draft")
	if f.r.c != nil {
		f.r.c.effect("during a draft's agent run")
	}
	return f.Formalizer.Formalize(ctx, req, out)
}

type crashBuilder struct {
	Builder
	r *runs
}

func (b crashBuilder) Build(ctx context.Context, dir, out string, amend bool) (*synth.Result, error) {
	b.r.started = append(b.r.started, "build")
	if b.r.c != nil {
		b.r.c.effect("during a build's agent run")
	}
	return b.Builder.Build(ctx, dir, out, amend)
}

func firstLine(s string) string {
	s = strings.TrimPrefix(s, header)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// crashRun takes an issue to a merge, stopping the watcher at effect at,
// and starting a fresh one in its place. With elsewhere, the new watcher
// runs on another machine, with its own clone and logs. It returns what the
// watcher stopped at, the rig and runs to check, and how many effects it
// counted. With at 0, it never stops.
func crashRun(t *testing.T, at int, elsewhere bool) (string, *rig, *runs, int) {
	t.Helper()
	ctx := context.Background()
	r := newRig(t)
	r.form.forks = nil
	c := &crasher{at: at}
	started := &runs{c: c}
	watcher := func(repo Repo, work string) *Factory {
		return &Factory{Repository: r.f.Repository, GitHub: crashGitHub{r.gh, c}, Repo: crashRepo{repo, c},
			Formalizer: crashFormalizer{r.form, started}, Builder: crashBuilder{r.build, started},
			Base: r.f.Base, Projects: r.f.Projects, Work: work, Check: r.f.Check, Log: t.Logf}
	}
	f := watcher(r.f.Repo, r.f.Work)
	r.gh.open(1, "gitdek", "Add a bounded buffer", "Producers put log lines in a buffer; a shipper takes them out in order.\n\n/invariant solve")

	said := map[string]bool{}
	for i := 0; i < 30; i++ {
		err := f.Poll(ctx)
		switch {
		case err != nil && strings.Contains(err.Error(), errCrash.Error()) && c.at > 0:
			// A new watcher starts in the old one's place, and nothing
			// stops again.
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
			return c.what, r, started, c.n
		case last.Marker.Kind == KindProposal && !said["ratify"]:
			said["ratify"] = true
			r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(last.Marker.Proposal.Hash, "sha256:")[:hashChars])
		case last.Marker.Kind == KindPR && !said["ci"]:
			said["ci"] = true
			r.gh.ci(last.Marker.PR, "success")
		case last.Marker.Kind == KindFailed && stoppedBuild(last) && !said["retry"]:
			said["retry"] = true
			r.gh.say(1, "gitdek", "/invariant retry")
		case last.Marker.Kind == KindStuck && !said["revise"]:
			said["revise"] = true
			r.gh.say(1, "gitdek", "/invariant revise")
		}
	}
	t.Fatalf("after stopping at %q, the issue never merged: its last post is %q", c.what, r.gh.last(1).Marker.Kind)
	return "", nil, nil, 0
}

// everyEffectOnce checks what a crash must not do: answer a command twice,
// start an agent run twice for one command or build, open a second pull
// request, merge twice, or record the factory's merge as someone else's.
func everyEffectOnce(t *testing.T, r *rig, started *runs) {
	t.Helper()
	answers := map[int64]int{}
	for _, p := range r.gh.posts(1) {
		for _, id := range p.Marker.ReplyTo {
			answers[id]++
		}
	}
	commands := 0
	for _, c := range r.gh.comments[1] {
		if len(ParseCommands(c.Body)) == 0 || c.User.Login != "gitdek" {
			continue
		}
		commands++
		if answers[c.ID] > 1 {
			t.Errorf("%q was answered %d times", firstLine(c.Body), answers[c.ID])
		}
	}
	drafts, builds, retries, revises := 0, 0, 0, 0
	for _, s := range started.started {
		switch s {
		case "draft":
			drafts++
		case "build":
			builds++
		}
	}
	for _, c := range r.gh.comments[1] {
		switch {
		case c.User.Login != "gitdek":
		case strings.Contains(c.Body, "/invariant retry"):
			retries++
		case strings.Contains(c.Body, "/invariant revise"):
			revises++
		}
	}
	if drafts > 1+revises {
		t.Errorf("%d agent runs drafted, for one solve and %d revises", drafts, revises)
	}
	if builds > 1+retries {
		t.Errorf("%d agent runs built the one ratification, with %d retries", builds, retries)
	}
	if len(r.gh.prs) != 1 {
		t.Errorf("%d pull requests for the one ratification", len(r.gh.prs))
	}
	if len(r.gh.merged) != 1 {
		t.Errorf("%d merges", len(r.gh.merged))
	}
	if last := r.gh.last(1); last.Marker.Kind != KindMerged || last.Marker.Numbers == nil {
		t.Errorf("the merge isn't recorded as the factory's:\n%s", last.Comment.Body)
	}
}

func TestCrashAnywhere(t *testing.T) {
	// A flow with no crash counts the effects there are to stop before.
	_, _, _, effects := crashRun(t, 0, false)
	for _, elsewhere := range []bool{false, true} {
		where := "the same machine"
		if elsewhere {
			where = "another machine"
		}
		for at := 1; at <= effects; at++ {
			t.Run(fmt.Sprintf("%s/effect %d", where, at), func(t *testing.T) {
				t.Parallel()
				what, r, started, _ := crashRun(t, at, elsewhere)
				t.Logf("stopped %s", what)
				everyEffectOnce(t, r, started)
			})
		}
	}
}
