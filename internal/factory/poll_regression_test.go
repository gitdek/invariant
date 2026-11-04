package factory

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

func TestPollSettlesAMergeWhoseIssueIsGone(t *testing.T) {
	r := newRig(t)
	g := hub514(r)
	r.gh.prs[100] = &github.PullRequest{Number: 100, Merged: true, Head: github.Ref{Ref: "invariant/issue-1-gone"}}
	if err := r.f.Poll(context.Background()); !errors.Is(err, github.ErrNotFound) {
		t.Fatalf("missing issue: %v", err)
	}
	g.reset()
	r.poll()
	quiet514(t, g, 1)
}

func TestPollKeepsFailedCleanupPastAnAsyncWatermark(t *testing.T) {
	r := newRig(t)
	g := hub514(r)
	r.f.Parallel = 1
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	r.f.Now = func() time.Time { return now }
	r.gh.open(1, "gitdek", "Closed work", "", LabelAsking, "bug")
	r.gh.say(1, "gitdek", post("asking", "Choose.", Marker{Kind: KindForks, Forks: r.form.forks}))
	r.gh.issues[1].State = "closed"
	r.gh.issues[1].UpdatedAt = now.Add(-24 * time.Hour).Format(time.RFC3339)
	g.failCleanup = true
	if err := r.f.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.f.Wait()
	posts := len(r.gh.posts(1))
	now = now.Add(time.Hour)
	if err := r.f.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.f.Wait()
	if g.since[1] == "" || r.gh.issues[1].HasLabel(LabelAsking) || !r.gh.issues[1].HasLabel("bug") || len(r.gh.posts(1)) != posts {
		t.Fatal("cleanup was lost or its final post repeated after the watermark advanced")
	}
}

func TestPollRetainsAClosedUpdateListedDuringItsStep(t *testing.T) {
	p := newParRig(t, 1)
	g := hub514(p.rig)
	g.rawMu = p.mu
	delayed := &delayedRead514{pollHub514: g, entered: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	unblock := func() { release.Do(func() { close(delayed.release) }) }
	defer unblock()
	p.f.GitHub = delayed
	p.mu.Lock()
	idle514(p.rig, 1)
	p.mu.Unlock()
	returned(t, p.pollAsync(), "start the idle read")
	select {
	case <-delayed.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the first read did not start")
	}
	p.mu.Lock()
	p.gh.say(1, "gitdek", post("asking", "Choose.", Marker{Kind: KindForks, Forks: p.form.forks}))
	p.gh.issues[1].State = "closed"
	p.gh.issues[1].Labels = []github.Label{{Name: LabelAsking}}
	p.gh.issues[1].UpdatedAt = time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	p.mu.Unlock()
	returned(t, p.pollAsync(), "list the closed update")
	unblock()
	p.f.Wait()
	// The closed update has now fallen outside discovery; only retained
	// work can finish it. The earlier read had no factory post to close.
	p.mu.Lock()
	p.gh.issues[1].UpdatedAt = "2025-01-01T00:00:00Z"
	p.mu.Unlock()
	p.settle()
	if !p.last(1).Marker.IssueClosed {
		t.Fatal("the earlier step consumed a closed update it never read")
	}
}
