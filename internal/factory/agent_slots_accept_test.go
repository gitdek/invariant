package factory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
)

// These tests exercise Poll, Watch and the actual issue/recovery flow with
// local git repositories. Only the coding agents and GitHub are fakes.
// A held agent is released explicitly, never by the progress deadline.
const slots455Deadline = 15 * time.Second

func slots455Soon(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.NewTimer(slots455Deadline)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for !ok() {
		select {
		case <-deadline.C:
			t.Fatalf("%s did not happen while the agent slots stayed occupied", what)
		case <-tick.C:
		}
	}
}

func slots455Returned(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Poll: %v", err)
		}
	case <-time.After(slots455Deadline):
		t.Fatal("Poll waited for an agent slot instead of returning")
	}
}

func slots455Wait(t *testing.T, f *Factory) {
	t.Helper()
	done := make(chan struct{})
	go func() { f.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(slots455Deadline):
		t.Fatal("Wait did not drain the running and queued steps")
	}
}

// Complete the concurrent GitHub wrapper for the newer interface methods,
// and observe issue reads without inspecting the scheduler's internals.
type slots455Hub struct {
	*syncHub
	readMu sync.Mutex
	reads  map[int]int
}

func (h *slots455Hub) Issue(ctx context.Context, n int) (github.Issue, error) {
	is, err := h.syncHub.Issue(ctx, n)
	h.readMu.Lock()
	h.reads[n]++
	h.readMu.Unlock()
	return is, err
}

func (h *slots455Hub) readCount(n int) int {
	h.readMu.Lock()
	defer h.readMu.Unlock()
	return h.reads[n]
}

func (h *slots455Hub) CreateIssue(ctx context.Context, is github.NewIssue) (github.Issue, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.gh.CreateIssue(ctx, is)
}

func (h *slots455Hub) PullRequestFrom(ctx context.Context, branch string) (github.PullRequest, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.gh.PullRequestFrom(ctx, branch)
}

func (h *slots455Hub) ClosePullRequest(ctx context.Context, n int) (github.PullRequest, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.gh.ClosePullRequest(ctx, n)
}

func (h *slots455Hub) MarkReady(ctx context.Context, n int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.gh.MarkReady(ctx, n)
}

type slots455Planner struct {
	Formalizer
	prd Formalizer
}

func (a slots455Planner) Formalize(ctx context.Context, req formalize.Request, out string) (*formalize.Result, error) {
	if req.PRD != "" {
		return a.prd.Formalize(ctx, req, out)
	}
	return a.Formalizer.Formalize(ctx, req, out)
}

// A plan's builder encompasses its second agent's review and any repairs,
// just as the real PlanBuilder does; it uses the same capacity as drafts
// and modeled builds.
type slots455PlanBuilder struct {
	PlanBuilder
	held *heldRuns
}

func (b slots455PlanBuilder) Build(ctx context.Context, root string, n int, out string) (*plumbing.BuildResult, error) {
	var res *plumbing.BuildResult
	var err error
	if heldErr := b.held.run(ctx, "plan-build", n, func() {
		res, err = b.PlanBuilder.Build(ctx, root, n, out)
	}); heldErr != nil {
		return nil, heldErr
	}
	return res, err
}

func slots455Rig(t *testing.T, parallel int) (*parRig, *slots455Hub) {
	t.Helper()
	p := newParRig(t, parallel)
	h := &slots455Hub{syncHub: p.hub, reads: map[int]int{}}
	p.f.GitHub = h
	p.f.Repository = "gitdek/invariant"
	p.form.plan = &plumbing.Plan{Name: "a hello page", Summary: "Add a page that says hello.",
		Files:   []string{"page/page.go"},
		Tests:   []plumbing.Test{{Name: "TestPageSaysHello", File: "page/page_accept_test.go", Says: "The page says hello."}},
		Sources: map[string]string{"page/page_accept_test.go": pageTest}}
	p.f.Formalizer = heldFormalizer{slots455Planner{p.form, &prdPlanner{t: t, plans: []*formalize.IssuePlan{graphPlan()}}}, p.held}
	p.f.Plumbing = slots455PlanBuilder{&fakePlanBuilder{pass: true, approve: true}, p.held}
	return p, h
}

func slots455Open(p *parRig, n int, body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gh.open(n, "gitdek", fmt.Sprintf("Work on issue %d", n), body)
}

func slots455Proposal(t *testing.T, p *parRig, n int, body string) string {
	t.Helper()
	slots455Open(p, n, body)
	slots455Returned(t, p.pollAsync())
	slots455Wait(t, p.f)
	post := p.last(n)
	if post.Marker.Kind != KindProposal || post.Marker.Proposal == nil {
		t.Fatalf("#%d has no proposal: %+v", n, post.Marker)
	}
	return "/invariant ratify " + strings.TrimPrefix(post.Marker.Proposal.Hash, "sha256:")
}

const slots455Model = "A bounded buffer.\n\n/invariant solve"
const slots455Plumbing = "A page that says hello.\n\nKind: plumbing\n\n/invariant solve"

// Start the first n agent steps before introducing a waiting issue. This
// avoids depending on goroutine launch order when testing a full pool.
func slots455Fill(t *testing.T, p *parRig, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		p.held.holdOpen(i)
		p.open(i)
	}
	done := p.pollAsync()
	slots455Soon(t, "the initial drafts to start", func() bool {
		for i := 1; i <= n; i++ {
			if p.held.started("draft", i) != 1 {
				return false
			}
		}
		return true
	})
	slots455Returned(t, done)
}

func slots455NoRun(t *testing.T, p *parRig, n int, step string) {
	t.Helper()
	state, _, err := p.clone.Run(p.ctx, n, step)
	if err != nil || state != RunNone {
		t.Fatalf("queued #%d has run state %v, error %v; want no recorded agent run", n, state, err)
	}
	for _, s := range p.f.Steps() {
		if s.Issue == n && s.Run != "" {
			t.Fatalf("queued #%d advertises an agent run before it acquired a slot: %+v", n, s)
		}
	}
}

func slots455Replies(p *parRig, n int, id int64) int {
	count := 0
	for _, post := range p.posts(n) {
		for _, reply := range post.Marker.ReplyTo {
			if reply == id {
				count++
			}
		}
	}
	return count
}

func TestQuickStepsRunWithAllAgentSlotsFull455(t *testing.T) {
	p, h := slots455Rig(t, 3)
	// Prepare a reviewed plumbing PR, another proposal, and three modeled
	// builds, without making the PR's CI gate pass until the pool is full.
	mergeCommand := slots455Proposal(t, p, 10, slots455Plumbing)
	p.say(10, mergeCommand)
	slots455Returned(t, p.pollAsync())
	slots455Wait(t, p.f)
	pr := p.expect(10, KindPR, LabelPR).Marker.PR
	ratifyCommand := slots455Proposal(t, p, 11, slots455Model)
	var buildCommands []string
	for n := 1; n <= 3; n++ {
		buildCommands = append(buildCommands, slots455Proposal(t, p, n, slots455Model))
	}
	for n, command := range buildCommands {
		p.held.holdOpen(n + 1)
		p.say(n+1, command)
	}
	done := p.pollAsync()
	slots455Soon(t, "three builds to start", func() bool {
		return p.held.started("build", 1) == 1 && p.held.started("build", 2) == 1 && p.held.started("build", 3) == 1
	})
	// The base implementation may already be stuck on #10 here. Joining
	// this first poll is itself part of the new contract.
	slots455Returned(t, done)
	p.held.holdOpen(4)
	p.open(4)
	p.held.holdOpen(11)
	ratify := p.say(11, ratifyCommand)
	slots455Open(p, 12, "An issue with no proposal.")
	command := p.say(12, "/invariant ratify ffffffffffff")
	p.ci(pr)
	before := p.polls()
	done = p.pollAsync()
	slots455Soon(t, "the approved PR to merge, the ratification to be answered and the command to be answered", func() bool {
		return p.last(10).Marker.Kind == KindMerged && p.last(11).Marker.Kind == KindRatified && slots455Replies(p, 12, command.ID) == 1
	})
	slots455Returned(t, done)
	if p.polls()-before != 1 || h.readCount(4) == 0 {
		t.Fatal("the quick work must finish in one poll, even past an issue waiting for an agent")
	}
	if slots455Replies(p, 11, ratify.ID) != 1 {
		t.Fatal("the ratification must be answered exactly once")
	}
	ratified := p.last(11)
	if b, err := p.clone.Show(p.ctx, ratified.Marker.Branch, lockFileOf(ratified.Marker.Project)); err != nil || lockAt(ratified.Marker.Project, b, err) != ratified.Marker.Hash {
		t.Fatalf("ratification must be committed before waiting for an agent: %v", err)
	}
	slots455NoRun(t, p, 4, "draft-issue")
	slots455NoRun(t, p, 11, buildStepName(ratified))
	if now, most := p.held.going(); now != 3 || most > 3 || p.held.started("draft", 4) != 0 || p.held.started("build", 11) != 0 {
		t.Fatalf("quick steps consumed or bypassed the agent limit: active=%d peak=%d", now, most)
	}
	p.mu.Lock()
	merged := append([]int(nil), p.gh.merged...)
	p.mu.Unlock()
	if len(merged) != 1 || merged[0] != pr {
		t.Fatalf("merged %v; want only reviewed PR #%d", merged, pr)
	}
}

func TestWaitingAgentStepsRemainSingleAndRespectCapacity455(t *testing.T) {
	for _, capacity := range []int{1, 3} {
		for _, kind := range []string{"draft", "plumbing-plan", "issue-plan", "build", "plan-build"} {
			t.Run(fmt.Sprintf("slots-%d/%s", capacity, kind), func(t *testing.T) {
				p, h := slots455Rig(t, capacity)
				const queued = 10
				var ratifyCommand string
				if kind == "build" || kind == "plan-build" {
					body := slots455Model
					if kind == "plan-build" {
						body = slots455Plumbing
					}
					ratifyCommand = slots455Proposal(t, p, queued, body)
				}
				slots455Fill(t, p, capacity)
				reads := h.readCount(queued)
				p.held.holdOpen(queued)
				var command github.Comment
				runKind := kind
				if ratifyCommand != "" {
					command = p.say(queued, ratifyCommand)
				} else {
					runKind = "draft"
					body := slots455Model
					switch kind {
					case "plumbing-plan":
						body = slots455Plumbing
					case "issue-plan":
						body = "Plan a decision graph.\n\n/invariant plan"
					}
					slots455Open(p, queued, body)
				}
				done := p.pollAsync()
				slots455Soon(t, "the queued issue to be read", func() bool { return h.readCount(queued) > reads })
				slots455Returned(t, done)
				step := "draft-issue"
				if ratifyCommand != "" {
					slots455Soon(t, "the queued build's ratification to be recorded", func() bool { return p.last(queued).Marker.Kind == KindRatified })
					step = buildStepName(p.last(queued))
				}
				// Repeated polls cannot start another step for the waiting
				// issue, answer its command twice or mistake it for a crash.
				for i := 0; i < 3; i++ {
					slots455Returned(t, p.pollAsync())
				}
				slots455NoRun(t, p, queued, step)
				if p.held.started(runKind, queued) != 0 {
					t.Fatal("the waiting agent started before a slot was released")
				}
				if ratifyCommand != "" && slots455Replies(p, queued, command.ID) != 1 {
					t.Fatal("polling a queued build answered its ratification more than once")
				}
				// No further poll is needed to give the waiting agent the
				// next free slot. Other running agents remain held.
				p.held.let(1)
				slots455Soon(t, "the queued agent to acquire the released slot", func() bool { return p.held.started(runKind, queued) == 1 })
				for i := 0; i < 3; i++ {
					slots455Returned(t, p.pollAsync())
				}
				if now, most := p.held.going(); now != capacity || most > capacity || p.held.started(runKind, queued) != 1 {
					t.Fatalf("agent capacity or per-issue exclusion failed: active=%d peak=%d capacity=%d", now, most, capacity)
				}
				p.held.letAll()
				slots455Wait(t, p.f)
				want := KindProposal
				if ratifyCommand != "" {
					want = KindPR
				}
				if got := p.last(queued).Marker.Kind; got != want {
					t.Fatalf("queued work ended as %q, want %q", got, want)
				}
				if p.held.started(runKind, queued) != 1 {
					t.Fatal("the queued agent ran more than once")
				}
			})
		}
	}
}

func TestWatchHeartbeatsAndCancelsWithFullAgentSlots455(t *testing.T) {
	for _, capacity := range []int{1, 3} {
		t.Run(fmt.Sprintf("slots-%d", capacity), func(t *testing.T) {
			p, h := slots455Rig(t, capacity)
			var beats atomic.Int64
			p.f.Activity = func([]Step) { beats.Add(1) }
			for n := 1; n <= capacity; n++ {
				p.held.holdOpen(n)
				p.open(n)
			}
			ctx, cancel := context.WithCancel(p.ctx)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- p.f.Watch(ctx, 20*time.Millisecond) }()
			slots455Soon(t, "all watcher agent slots to fill", func() bool {
				for n := 1; n <= capacity; n++ {
					if p.held.started("draft", n) != 1 {
						return false
					}
				}
				return true
			})
			p.held.holdOpen(10)
			p.open(10)
			slots455Soon(t, "the watcher to read another issue with its slots full", func() bool { return h.readCount(10) > 0 })
			// Once the waiting step has settled, no agent or command can
			// finish: repeated Activity calls must come from the heartbeat.
			polls := p.polls()
			slots455Soon(t, "three more polls", func() bool { return p.polls() >= polls+3 })
			before := beats.Load()
			polls = p.polls()
			slots455Soon(t, "three more heartbeat updates", func() bool { return p.polls() >= polls+3 && beats.Load() >= before+3 })
			slots455NoRun(t, p, 10, "draft-issue")
			cancel()
			select {
			case err := <-done:
				if err != context.Canceled {
					t.Fatalf("Watch returned %v after cancellation", err)
				}
			case <-time.After(slots455Deadline):
				t.Fatal("Watch failed to cancel and drain its agent-slot waiters")
			}
			if len(p.f.Steps()) != 0 || p.held.started("draft", 10) != 0 {
				t.Fatal("shutdown left a step running or started the queued agent")
			}
			slots455NoRun(t, p, 10, "draft-issue")
		})
	}
}

func TestClosedQueuedIssueDoesNotStartAnAgent455(t *testing.T) {
	p, h := slots455Rig(t, 3)
	slots455Fill(t, p, 3)
	p.held.holdOpen(10)
	p.open(10)
	done := p.pollAsync()
	slots455Soon(t, "the queued issue to be read before capacity is free", func() bool { return h.readCount(10) > 0 })
	slots455Returned(t, done)
	p.shut(10)
	p.held.letAll()
	slots455Wait(t, p.f)
	if p.held.started("draft", 10) != 0 || len(p.posts(10)) != 0 {
		t.Fatal("an issue closed while waiting still started an agent or posted a draft")
	}
	slots455NoRun(t, p, 10, "draft-issue")
}
