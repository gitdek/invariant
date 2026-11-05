package factory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

// Count observable GitHub operations, not scheduler fields. ClosedIssues is
// the new list API: an empty since discovers old unfinished work on startup.
type pollHub514 struct {
	GitHub
	raw         *fakeGitHub
	rawMu       *sync.Mutex
	mu          sync.Mutex
	calls       map[string]int
	since       []string
	failRead    int
	failClosed  bool
	failCleanup bool
}

var errPoll514 = errors.New("temporary GitHub read or cleanup failure")

func hub514(r *rig) *pollHub514 {
	g := &pollHub514{GitHub: r.f.GitHub, raw: r.gh, calls: map[string]int{}}
	r.f.GitHub = g
	return g
}

func (g *pollHub514) count(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls[key]++
}

func (g *pollHub514) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = map[string]int{}
}

func (g *pollHub514) counts() map[string]int {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := map[string]int{}
	for k, v := range g.calls {
		out[k] = v
	}
	return out
}

func (g *pollHub514) OpenIssues(ctx context.Context) ([]github.Issue, error) {
	g.count("open")
	return g.GitHub.OpenIssues(ctx)
}

func (g *pollHub514) Issues(ctx context.Context, label string) ([]github.Issue, error) {
	g.count("legacy-label-list")
	return g.GitHub.Issues(ctx, label)
}

func (g *pollHub514) ClosedIssues(_ context.Context, since string) ([]github.Issue, error) {
	g.count("closed")
	g.mu.Lock()
	g.since = append(g.since, since)
	fail := g.failClosed
	g.failClosed = false
	g.mu.Unlock()
	if fail {
		return nil, errPoll514
	}
	var after time.Time
	if since != "" {
		var err error
		after, err = time.Parse(time.RFC3339, since)
		if err != nil {
			return nil, fmt.Errorf("invalid since %q: %w", since, err)
		}
	}
	if g.rawMu != nil {
		g.rawMu.Lock()
		defer g.rawMu.Unlock()
	}
	var out []github.Issue
	for _, is := range g.raw.issues {
		updated, _ := time.Parse(time.RFC3339, is.UpdatedAt)
		if is.State == "closed" && is.PullRequest == nil && (after.IsZero() || updated.IsZero() || !updated.Before(after)) {
			out = append(out, *is)
		}
	}
	return out, nil
}

func (g *pollHub514) MergedPullRequests(ctx context.Context) ([]github.PullRequest, error) {
	g.count("merges")
	return g.GitHub.MergedPullRequests(ctx)
}

func (g *pollHub514) Issue(ctx context.Context, n int) (github.Issue, error) {
	g.count(fmt.Sprintf("issue/%d", n))
	return g.GitHub.Issue(ctx, n)
}

func (g *pollHub514) Comments(ctx context.Context, n int) ([]github.Comment, error) {
	g.count(fmt.Sprintf("comments/%d", n))
	g.mu.Lock()
	fail := g.failRead == n
	if fail {
		g.failRead = 0
	}
	g.mu.Unlock()
	if fail {
		return nil, errPoll514
	}
	return g.GitHub.Comments(ctx, n)
}

func (g *pollHub514) Events(ctx context.Context, n int) ([]github.Event, error) {
	g.count(fmt.Sprintf("events/%d", n))
	return g.GitHub.Events(ctx, n)
}

func (g *pollHub514) Permission(ctx context.Context, login string) (string, error) {
	g.count("permission/" + login)
	return g.GitHub.Permission(ctx, login)
}

func (g *pollHub514) RemoveLabel(ctx context.Context, n int, label string) error {
	g.mu.Lock()
	fail := g.failCleanup
	g.failCleanup = false
	g.mu.Unlock()
	if fail {
		return errPoll514
	}
	return g.GitHub.RemoveLabel(ctx, n, label)
}

func stamp514(r *rig, n, second int) {
	r.gh.issues[n].UpdatedAt = time.Date(2026, 11, 3, 12, 0, second, 0, time.UTC).Format(time.RFC3339)
}

func idle514(r *rig, n int) {
	r.gh.open(n, "gitdek", "Unchanged issue", "No factory command.")
	stamp514(r, n, 0)
}

func quiet514(t *testing.T, g *pollHub514, numbers ...int) {
	t.Helper()
	calls := g.counts()
	for _, n := range numbers {
		if calls[fmt.Sprintf("issue/%d", n)] != 0 || calls[fmt.Sprintf("comments/%d", n)] != 0 {
			t.Errorf("unchanged idle #%d was stepped: %v", n, calls)
		}
	}
}

func TestPoll514IdleRequestCost(t *testing.T) {
	want := -1
	for _, size := range []int{1, 40, 250} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			r := newRig(t)
			g := hub514(r)
			for n := 1; n <= size; n++ {
				idle514(r, n)
			}
			r.poll()
			for n := 1; n <= size; n++ {
				if g.counts()[fmt.Sprintf("comments/%d", n)] == 0 {
					t.Fatalf("new issue #%d was not discovered", n)
				}
			}
			for round := 0; round < 3; round++ {
				g.reset()
				heartbeats := 0
				r.f.Activity = func([]Step) { heartbeats++ }
				r.poll()
				if heartbeats == 0 {
					t.Error("an idle poll emitted no heartbeat")
				}
				calls, total, individual := g.counts(), 0, 0
				for k, n := range calls {
					total += n
					if k != "open" && k != "closed" && k != "merges" {
						individual += n
					}
				}
				if individual != 0 {
					t.Errorf("idle poll made %d calls beyond the shared lists", individual)
				}
				if want < 0 {
					want = total
				}
				if total != want || total > 3 || calls["open"] != 1 || calls["closed"] != 1 {
					t.Errorf("%d idle issues cost %d calls; want the same <=3 shared list calls (open=%d closed=%d merges=%d)", size, total, calls["open"], calls["closed"], calls["merges"])
				}
			}
			// A process-local optimization must not suppress a successor's discovery.
			fresh := &Factory{GitHub: g, Repo: r.f.Repo, Repository: "o/r", Work: t.TempDir()}
			g.reset()
			if err := fresh.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			if g.counts()["comments/1"] == 0 {
				t.Error("a fresh watcher inherited another watcher's idle observations")
			}
		})
	}
}

func TestPoll514ChangesAndReadRetries(t *testing.T) {
	r := newRig(t)
	g := hub514(r)
	for n := 1; n <= 5; n++ {
		idle514(r, n)
	}
	r.poll()
	r.gh.say(1, "gitdek", "/invariant solve")
	r.gh.say(1, "gitdek", "/invariant ratify deadbeefdead")
	r.gh.issues[2].Body = "A buffer.\n\n/invariant solve"
	if err := r.gh.AddLabels(context.Background(), 3, LabelTrigger); err != nil {
		t.Fatal(err)
	}
	r.gh.events[3] = append(r.gh.events[3], github.Event{Event: "labeled", Actor: github.User{Login: "gitdek"}, Label: &github.Label{Name: LabelTrigger}})
	r.gh.say(4, "gitdek", "/invariant solve")
	for n := 1; n <= 4; n++ {
		stamp514(r, n, 1)
	}
	g.failRead = 4
	g.reset()
	if err := r.f.Poll(context.Background()); !errors.Is(err, errPoll514) {
		t.Fatalf("the failed read was not returned: %v", err)
	}
	for n := 1; n <= 2; n++ {
		r.expect(n, KindForks, LabelAsking)
	}
	r.expect(3, KindForks, LabelTrigger, LabelAsking)
	if len(r.gh.posts(4)) != 0 {
		t.Fatal("an unread command was answered")
	}
	quiet514(t, g, 5)
	g.reset()
	r.poll() // #4's timestamp has not changed since the failed attempt.
	r.expect(4, KindForks, LabelAsking)
	if last := r.gh.last(1); last.Marker.Kind != KindNote || !strings.Contains(last.Comment.Body, "no proposal waiting") {
		t.Fatalf("a second pending command was lost without another timestamp change: %+v", last.Marker)
	}
	quiet514(t, g, 5)
	if len(r.form.requests) != 4 {
		t.Fatalf("want exactly four drafts, got %d", len(r.form.requests))
	}
	// Settle any update caused by the bot's own replies, then check true idleness.
	r.poll()
	g.reset()
	r.poll()
	quiet514(t, g, 1, 2, 3, 4, 5)
}

func TestPoll514CIAndPullRequestChanges(t *testing.T) {
	r := newRig(t)
	opened := ratified(t, r)
	stamp514(r, 1, 0)
	idle514(r, 99)
	g := hub514(r)
	r.poll()
	r.poll()
	g.reset()
	r.gh.ci(opened.Marker.PR, "failure")
	r.poll()
	r.expect(1, KindFailed, LabelHumanReview)
	quiet514(t, g, 99)
	// A failed PR still has an external outcome to observe, without a comment.
	pr := r.gh.prs[opened.Marker.PR]
	pr.State = "closed"
	g.reset()
	r.poll()
	if got := r.gh.last(1).Marker.Kind; got != KindClosed {
		t.Fatalf("PR closure was not observed on the next poll: %s", got)
	}
	quiet514(t, g, 99)
}

func TestPoll514ClosedScanAndCleanupRetry(t *testing.T) {
	r := newRig(t)
	g := hub514(r)
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	r.f.Now = func() time.Time { return now }
	makeClosed := func(n int, active bool, at time.Time) {
		r.gh.open(n, "gitdek", "Closed issue", "", "bug")
		if active {
			r.gh.issues[n].Labels = append(r.gh.issues[n].Labels, github.Label{Name: LabelAsking})
			r.gh.say(n, "gitdek", post("asking", "Choose.", Marker{Kind: KindForks, Forks: r.form.forks}))
		}
		r.gh.issues[n].State = "closed"
		r.gh.issues[n].UpdatedAt = at.Format(time.RFC3339)
	}
	makeClosed(1, true, now.Add(-365*24*time.Hour))
	makeClosed(2, false, now.Add(-365*24*time.Hour))
	g.failCleanup = true
	if err := r.f.Poll(context.Background()); !errors.Is(err, errPoll514) {
		t.Fatalf("want cleanup failure, got %v", err)
	}
	posts := len(r.gh.posts(1))
	now = now.Add(time.Minute)
	r.poll()
	if r.gh.issues[1].HasLabel(LabelAsking) || !r.gh.issues[1].HasLabel("bug") || len(r.gh.posts(1)) != posts {
		t.Fatal("retry failed to finish cleanup once while preserving unrelated labels")
	}
	if g.counts()["comments/2"] != 0 {
		t.Error("unrelated closed history was stepped")
	}
	if len(g.since) < 2 || g.since[0] != "" || g.counts()["legacy-label-list"] != 0 {
		t.Fatalf("closed discovery must use one new query, initially without since: queries=%v calls=%v", g.since, g.counts())
	}
	// An update at the previous successful poll boundary must not be lost.
	makeClosed(3, true, now)
	now = now.Add(time.Minute)
	g.failClosed = true
	if err := r.f.Poll(context.Background()); !errors.Is(err, errPoll514) {
		t.Fatalf("want list failure, got %v", err)
	}
	failedSince := g.since[len(g.since)-1]
	if failedSince == "" {
		t.Fatal("successful polls never advanced the closed-issue watermark")
	}
	now = now.Add(time.Minute)
	r.poll()
	if g.since[len(g.since)-1] != failedSince {
		t.Error("a failed discovery advanced its watermark")
	}
	if !r.gh.last(3).Marker.IssueClosed || r.gh.issues[3].HasLabel(LabelAsking) {
		t.Fatal("the close at the poll boundary was lost")
	}
	g.reset()
	now = now.Add(time.Minute)
	r.poll()
	quiet514(t, g, 1, 2, 3)
	if g.counts()["closed"] != 1 || g.counts()["legacy-label-list"] != 0 {
		t.Fatalf("want one closed query, got %v", g.counts())
	}
}

func TestPoll514MergeAfterClosure(t *testing.T) {
	r, opened := closedPoll434PR(t, true)
	g := hub514(r)
	stamp514(r, 1, 0)
	idle514(r, 99)
	r.gh.issues[1].State = "closed"
	r.poll()
	r.poll()
	before := len(r.gh.posts(1))
	pr := r.gh.prs[opened.Marker.PR]
	pr.Merged, pr.State, pr.MergeCommitSHA = true, "closed", "human-merge-514"
	g.reset()
	r.poll()
	if last := r.gh.last(1); last.Marker.Kind != KindMerged || last.Marker.PR != pr.Number || len(r.gh.posts(1)) != before+1 {
		t.Fatalf("the later merge was not recorded once: %+v", last.Marker)
	}
	if len(r.gh.merged) != 0 {
		t.Fatal("the factory attempted a person's merge itself")
	}
	quiet514(t, g, 99)
	g.reset()
	r.poll()
	quiet514(t, g, 1, 99)
	if len(r.gh.posts(1)) != before+1 {
		t.Fatal("a later poll repeated the merge record")
	}
}

func TestPoll514RunningAndQueuedWork(t *testing.T) {
	p := newParRig(t, 1)
	one, two := p.proposed(1), p.proposed(2)
	p.mu.Lock()
	idle514(p.rig, 99)
	stamp514(p.rig, 1, 0)
	stamp514(p.rig, 2, 0)
	p.mu.Unlock()
	g := hub514(p.rig)
	g.rawMu = p.mu
	p.settle()
	p.held.holdOpen(1)
	p.say(1, one)
	p.mu.Lock()
	stamp514(p.rig, 1, 1)
	p.mu.Unlock()
	returned(t, p.pollAsync(), "start the first build")
	soon(t, "the first build to hold its slot", func() bool { return p.held.started("build", 1) == 1 })
	p.say(2, two)
	p.mu.Lock()
	stamp514(p.rig, 2, 1)
	p.mu.Unlock()
	returned(t, p.pollAsync(), "queue the second build")
	soon(t, "one running build and one queued step", func() bool {
		return p.held.started("build", 1) == 1 && len(p.f.Steps()) == 2
	})
	g.reset()
	for i := 0; i < 3; i++ {
		returned(t, p.pollAsync(), "poll during a held build")
	}
	if p.held.started("build", 1) != 1 || p.held.started("build", 2) != 0 {
		t.Error("polling duplicated a run or bypassed the agent slot")
	}
	p.held.let(1)
	p.f.Wait()
	p.settle()
	for _, n := range []int{1, 2} {
		opened := p.last(n)
		if opened.Marker.Kind != KindPR || p.held.started("build", n) != 1 {
			t.Fatalf("#%d did not publish exactly one build: %+v", n, opened.Marker)
		}
		p.ci(opened.Marker.PR)
	}
	p.settle()
	for _, n := range []int{1, 2} {
		if p.last(n).Marker.Kind != KindMerged {
			t.Errorf("#%d's CI completion was skipped after its running step ended", n)
		}
	}
	quiet514(t, g, 99)
}

func TestPoll514RatifiedIssuePlanContinues(t *testing.T) {
	r, planHub := pipelineRig(t)
	plan := ratifyPipeline(t, r, draftPipeline(t, r))
	stamp514(r, pipelinePRD, 0)
	idle514(r, 99)
	g := hub514(r)
	r.poll()
	if len(planHub.opened) != 1 {
		t.Fatalf("plan did not open its first issue: %v", planHub.opened)
	}
	first := planHub.opened[0]
	pr := pollToPullRequest(t, r, first)
	r.poll()
	g.reset()
	r.gh.ci(pr.Marker.PR, "success")
	r.poll()
	r.poll()
	if len(planHub.opened) != 2 {
		t.Fatalf("unchanged plan stopped after its child's merge: %v", planHub.opened)
	}
	expectOpenedFor(t, r, plan, planHub.opened[1], 1)
	quiet514(t, g, 99)
	if !strings.HasPrefix(pr.Marker.Branch, fmt.Sprintf("invariant/issue-%d-", first)) {
		t.Fatal("fixture did not produce the child's pull request")
	}
}

type delayedRead514 struct {
	*pollHub514
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *delayedRead514) Comments(ctx context.Context, n int) ([]github.Comment, error) {
	comments, err := g.pollHub514.Comments(ctx, n)
	if n != 1 || err != nil {
		return comments, err
	}
	first := false
	g.once.Do(func() { first = true })
	if first {
		close(g.entered)
		select {
		case <-g.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return comments, nil
}

func TestPoll514ChangeDuringStep(t *testing.T) {
	p := newParRig(t, 2)
	p.mu.Lock()
	idle514(p.rig, 99)
	p.mu.Unlock()
	g := hub514(p.rig)
	g.rawMu = p.mu
	p.settle()
	delayed := &delayedRead514{pollHub514: g, entered: make(chan struct{}), release: make(chan struct{})}
	p.f.GitHub = delayed
	var release sync.Once
	unblock := func() { release.Do(func() { close(delayed.release) }) }
	defer unblock()
	p.mu.Lock()
	idle514(p.rig, 1)
	p.mu.Unlock()
	returned(t, p.pollAsync(), "read the new idle issue")
	select {
	case <-delayed.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the issue's first read did not start")
	}
	p.say(1, "/invariant solve")
	p.mu.Lock()
	stamp514(p.rig, 1, 1)
	p.mu.Unlock()
	g.reset()
	returned(t, p.pollAsync(), "observe a change while the previous read is running")
	unblock()
	p.f.Wait()
	p.settle()
	if got := p.last(1).Marker.Kind; got != KindProposal || p.held.started("draft", 1) != 1 {
		t.Fatalf("the change during the previous step was lost or repeated: kind=%s", got)
	}
	quiet514(t, g, 99)
}
