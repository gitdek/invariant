package factory

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

func TestPollCommentCountWithoutTimestampChange(t *testing.T) {
	r := newRig(t)
	g := hub514(r)
	r.f.Now = func() time.Time { return time.Date(2026, 11, 3, 12, 5, 0, 0, time.UTC) }
	idle514(r, 1)
	idle514(r, 99)
	r.poll()
	updated := r.gh.issues[1].UpdatedAt
	r.gh.say(1, "gitdek", "/invariant solve")
	g.reset()
	r.poll()
	r.expect(1, KindForks, LabelAsking)
	quiet514(t, g, 99)
	if r.gh.issues[1].UpdatedAt != updated || len(r.form.requests) != 1 {
		t.Fatal("the comment did not wake its unchanged timestamp exactly once")
	}
	r.poll()
	g.reset()
	r.poll()
	quiet514(t, g, 1, 99)
	if len(r.form.requests) != 1 {
		t.Fatal("settling the comment repeated its command")
	}
}

func TestPollRecentCommentEditWithoutTimestampChange(t *testing.T) {
	p := newParRig(t, 1)
	g := hub514(p.rig)
	g.rawMu = p.mu
	var clockMu sync.Mutex
	now := time.Date(2026, 11, 3, 12, 0, 0, 100000000, time.UTC)
	p.f.Now = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	p.mu.Lock()
	idle514(p.rig, 1)
	idle514(p.rig, 99)
	p.gh.issues[99].UpdatedAt = "2025-01-01T00:00:00Z"
	p.gh.say(1, "gitdek", "Still writing the request.")
	updated, count := p.gh.issues[1].UpdatedAt, p.gh.issues[1].Comments
	p.mu.Unlock()
	delayed := &delayedRead514{pollHub514: g, entered: make(chan struct{}), release: make(chan struct{})}
	p.f.GitHub = delayed
	var release sync.Once
	unblock := func() { release.Do(func() { close(delayed.release) }) }
	defer unblock()
	returned(t, p.pollAsync(), "read the issue in its update second")
	select {
	case <-delayed.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the comment read did not start")
	}
	// An edit in the same second changes neither count nor timestamp. The
	// read finishes after the overlap, but must not settle that earlier view.
	p.mu.Lock()
	p.gh.comments[1][0].Body = "/invariant solve"
	unchanged := p.gh.issues[1].UpdatedAt == updated && p.gh.issues[1].Comments == count
	p.mu.Unlock()
	if !unchanged {
		t.Fatal("the edit changed the issue's list representation")
	}
	clockMu.Lock()
	now = now.Add(2 * time.Minute)
	clockMu.Unlock()
	unblock()
	p.f.Wait()
	g.reset()
	p.settle()
	if got := p.last(1).Marker.Kind; got != KindProposal || p.held.started("draft", 1) != 1 {
		t.Fatalf("the same-second edit was lost or repeated: kind=%s", got)
	}
	quiet514(t, g, 99)
	p.settle()
	g.reset()
	p.settle()
	quiet514(t, g, 1, 99)
	if p.held.started("draft", 1) != 1 {
		t.Fatal("settling the timestamp bucket repeated its command")
	}
}

type issueNotFoundOnce struct {
	GitHub
	n int
}

func (g *issueNotFoundOnce) Issue(ctx context.Context, n int) (github.Issue, error) {
	if n == g.n {
		g.n = 0
		return github.Issue{}, github.ErrNotFound
	}
	return g.GitHub.Issue(ctx, n)
}

func TestPollRetriesListedIssueNotFound(t *testing.T) {
	for _, closed := range []bool{false, true} {
		name := "open command"
		if closed {
			name = "closed cleanup"
		}
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			missing := &issueNotFoundOnce{GitHub: r.f.GitHub}
			r.f.GitHub = missing
			g := hub514(r)
			r.f.Now = func() time.Time { return time.Date(2026, 11, 3, 12, 5, 0, 0, time.UTC) }
			idle514(r, 1)
			idle514(r, 99)
			if !closed {
				r.poll()
				r.gh.say(1, "gitdek", "/invariant solve")
			} else {
				r.gh.say(1, "gitdek", post("asking", "Choose.", Marker{Kind: KindForks, Forks: r.form.forks}))
				r.gh.issues[1].State = "closed"
				r.gh.issues[1].Labels = []github.Label{{Name: LabelAsking}}
			}
			missing.n = 1
			updated := r.gh.issues[1].UpdatedAt
			if err := r.f.Poll(context.Background()); !errors.Is(err, github.ErrNotFound) {
				t.Fatalf("the failed issue read was not returned: %v", err)
			}
			g.reset()
			r.poll()
			if g.counts()["issue/1"] == 0 || r.gh.issues[1].UpdatedAt != updated {
				t.Fatal("the unchanged listed issue was not retried after its 404")
			}
			if closed {
				if !r.gh.last(1).Marker.IssueClosed || r.gh.issues[1].HasLabel(LabelAsking) {
					t.Fatal("the recovered read did not finish closed cleanup")
				}
			} else {
				r.expect(1, KindForks, LabelAsking)
				if len(r.form.requests) != 1 {
					t.Fatal("the recovered read did not handle its command once")
				}
			}
			quiet514(t, g, 99)
			r.poll()
			g.reset()
			r.poll()
			quiet514(t, g, 1, 99)
		})
	}
}
