package factory

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/synth"
)

// The crash test with two issues whose steps run at once (D-0113). The
// watcher stops before each effect either issue takes, and a fresh one in its
// place, on the same machine or another, finishes both, with every effect
// done once.

// stopper counts the effects of a watcher that runs steps at once, and stops
// it at effect number at. The step taking that effect panics, as in a crash,
// and so does any later effect of the same watcher, whichever step takes it:
// a watcher that stopped takes none. Watchers count from 0, the first.
type stopper struct {
	mu   sync.Mutex
	at   int
	seen int
	what string
	runs map[string]int
}

func (s *stopper) effect(gen int, what string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen == 0 && s.what != "" {
		panic(errCrash)
	}
	s.seen++
	if s.seen == s.at {
		s.what = what
		panic(errCrash)
	}
}

// agentRun counts an agent run of kind, draft or build, for issue n. Its
// start is an effect: a stop there stops the watcher during the run, which
// started and never finishes.
func (s *stopper) agentRun(gen int, kind string, n int) {
	s.mu.Lock()
	if gen != 0 || s.what == "" {
		s.runs[fmt.Sprintf("%s %d", kind, n)]++
	}
	s.mu.Unlock()
	s.effect(gen, fmt.Sprintf("during #%d's %s", n, kind))
}

func (s *stopper) stopped() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.what
}

func (s *stopper) counted() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen
}

func (s *stopper) started(kind string, n int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs[fmt.Sprintf("%s %d", kind, n)]
}

type stopHub struct {
	GitHub
	s   *stopper
	gen int
}

func (g stopHub) PostComment(ctx context.Context, issue int, body string) (github.Comment, error) {
	g.s.effect(g.gen, "post "+firstLine(body))
	return g.GitHub.PostComment(ctx, issue, body)
}

func (g stopHub) AddLabels(ctx context.Context, issue int, labels ...string) error {
	g.s.effect(g.gen, "add labels "+strings.Join(labels, ", "))
	return g.GitHub.AddLabels(ctx, issue, labels...)
}

func (g stopHub) CreatePullRequest(ctx context.Context, pr github.NewPullRequest) (github.PullRequest, error) {
	g.s.effect(g.gen, "open a pull request")
	return g.GitHub.CreatePullRequest(ctx, pr)
}

func (g stopHub) Merge(ctx context.Context, n int, sha, method string) (string, error) {
	g.s.effect(g.gen, "merge")
	return g.GitHub.Merge(ctx, n, sha, method)
}

func (g stopHub) DeleteBranch(ctx context.Context, branch string) error {
	g.s.effect(g.gen, "delete the branch")
	return g.GitHub.DeleteBranch(ctx, branch)
}

type stopRepo struct {
	Repo
	s   *stopper
	gen int
}

func (r stopRepo) Push(ctx context.Context, worktree, branch string) error {
	r.s.effect(r.gen, "push "+branch)
	return r.Repo.Push(ctx, worktree, branch)
}

func (r stopRepo) PushCommit(ctx context.Context, commit, branch string) error {
	r.s.effect(r.gen, "push the code to "+branch)
	return r.Repo.PushCommit(ctx, commit, branch)
}

func (r stopRepo) Record(ctx context.Context, issue int, step, what string) error {
	r.s.effect(r.gen, "record "+step)
	return r.Repo.Record(ctx, issue, step, what)
}

func (r stopRepo) Finish(ctx context.Context, issue int, step, result string) error {
	r.s.effect(r.gen, "record the result of "+step)
	return r.Repo.Finish(ctx, issue, step, result)
}

type stopFormalizer struct {
	Formalizer
	s   *stopper
	gen int
}

func (f stopFormalizer) Formalize(ctx context.Context, req formalize.Request, out string) (*formalize.Result, error) {
	f.s.agentRun(f.gen, "draft", req.Issue)
	return f.Formalizer.Formalize(ctx, req, out)
}

type stopBuilder struct {
	Builder
	s   *stopper
	gen int
}

func (b stopBuilder) Build(ctx context.Context, dir, out string, amend bool) (*synth.Result, error) {
	b.s.agentRun(b.gen, "build", issueIn(out))
	return b.Builder.Build(ctx, dir, out, amend)
}

// twoAtOnce opens #1 and #2 and takes both to a merge, two steps at once,
// stopping the watcher at effect at. Once every step of the stopped watcher
// has ended, a fresh one starts in its place: with elsewhere, on another
// machine, with its own clone and work directory. With at 0, it never stops.
func twoAtOnce(t *testing.T, at int, elsewhere bool) (*parRig, *stopper) {
	t.Helper()
	p := newParRig(t, 2)
	s := &stopper{at: at, runs: map[string]int{}}
	watcherOf := func(gen int, repo Repo, work string) *Factory {
		f := &Factory{Repository: p.f.Repository, GitHub: stopHub{p.hub, s, gen}, Repo: stopRepo{repo, s, gen},
			Formalizer: stopFormalizer{p.f.Formalizer, s, gen}, Builder: stopBuilder{p.f.Builder, s, gen},
			Base: p.f.Base, Projects: p.f.Projects, Work: work, Check: p.f.Check, Log: t.Logf, Parallel: 2}
		t.Cleanup(f.Wait)
		return f
	}
	f := watcherOf(0, p.clone, p.f.Work)
	p.open(1)
	p.open(2)
	said := map[string]bool{}
	restarted := false
	for round := 0; round < 40; round++ {
		returned(t, pollOf(p.ctx, f), "a poll")
		f.Wait()
		if s.stopped() != "" && !restarted {
			// A fresh watcher starts in the stopped one's place, and nothing
			// stops again.
			restarted = true
			repo, work := p.clone, p.f.Work
			if elsewhere {
				repo.Dir = filepath.Join(t.TempDir(), "clone")
				if err := repo.Ensure(p.ctx); err != nil {
					t.Fatal(err)
				}
				work = t.TempDir()
			}
			f = watcherOf(1, repo, work)
			continue
		}
		merged := 0
		for n := 1; n <= 2; n++ {
			last := p.last(n)
			once := func(what string) bool {
				key := fmt.Sprintf("#%d %s", n, what)
				first := !said[key]
				said[key] = true
				return first
			}
			switch {
			case last.Marker.Kind == KindMerged:
				merged++
			case last.Marker.Kind == KindProposal && once("ratify"):
				p.say(n, "/invariant ratify "+strings.TrimPrefix(last.Marker.Proposal.Hash, "sha256:")[:hashChars])
			case last.Marker.Kind == KindPR && once("ci"):
				p.ci(last.Marker.PR)
			case stoppedBuild(last) && once("retry"):
				p.say(n, "/invariant retry")
			case last.Marker.Kind == KindStuck && once("revise"):
				p.say(n, "/invariant revise")
			}
		}
		if merged == 2 {
			return p, s
		}
	}
	t.Fatalf("after stopping at %q, #1 and #2 didn't both merge: their last posts are %q and %q", s.stopped(), p.last(1).Marker.Kind, p.last(2).Marker.Kind)
	return nil, nil
}

// eachOnce checks what a crash must not do to issue n: answer a command
// twice, start an agent run twice for one command or build, open a second
// pull request, merge twice, or record the factory's merge as someone
// else's.
func eachOnce(t *testing.T, p *parRig, s *stopper, n int) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	answers := map[int64]int{}
	for _, post := range p.gh.posts(n) {
		for _, id := range post.Marker.ReplyTo {
			answers[id]++
		}
	}
	retries, revises := 0, 0
	for _, c := range p.gh.comments[n] {
		if _, posted := DecodeMarker(c.Body); posted {
			continue
		}
		for _, cmd := range ParseCommands(c.Body) {
			switch cmd.Verb {
			case Retry:
				retries++
			case Revise:
				revises++
			}
		}
		if answers[c.ID] > 1 {
			t.Errorf("#%d: %q was answered %d times", n, firstLine(c.Body), answers[c.ID])
		}
	}
	if drafts := s.started("draft", n); drafts > 1+revises {
		t.Errorf("#%d: %d agent runs drafted, for one solve and %d revises", n, drafts, revises)
	}
	if builds := s.started("build", n); builds > 1+retries {
		t.Errorf("#%d: %d agent runs built the one ratification, with %d retries", n, builds, retries)
	}
	branch := fmt.Sprintf("invariant/issue-%d-", n)
	prs, merges := 0, 0
	for number, pr := range p.gh.prs {
		if !strings.HasPrefix(pr.Head.Ref, branch) {
			continue
		}
		prs++
		for _, m := range p.gh.merged {
			if m == number {
				merges++
			}
		}
	}
	if prs != 1 || merges != 1 {
		t.Errorf("#%d: %d pull requests and %d merges; want one of each", n, prs, merges)
	}
	if last := p.gh.last(n); last.Marker.Kind != KindMerged || last.Marker.Numbers == nil {
		t.Errorf("#%d: the merge isn't recorded as the factory's:\n%s", n, last.Comment.Body)
	}
}

func TestTwoIssuesAtOnceSurviveACrashAnywhere(t *testing.T) {
	// A run with no stop counts the effects there are to stop before.
	p, s := twoAtOnce(t, 0, false)
	for n := 1; n <= 2; n++ {
		eachOnce(t, p, s, n)
	}
	effects := s.counted()
	for _, elsewhere := range []bool{false, true} {
		where := "the same machine"
		if elsewhere {
			where = "another machine"
		}
		for at := 1; at <= effects; at++ {
			t.Run(fmt.Sprintf("%s/effect %d", where, at), func(t *testing.T) {
				t.Parallel()
				p, s := twoAtOnce(t, at, elsewhere)
				t.Logf("stopped %s", s.stopped())
				for n := 1; n <= 2; n++ {
					eachOnce(t, p, s, n)
				}
			})
		}
	}
}
