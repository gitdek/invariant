package plumbing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/synth"
)

// review281Call records the budget visible through the existing Backend
// contract. A caller may limit a run with its context or with Job.Timeout.
type review281Call struct {
	workspace string
	limit     time.Duration
	elapsed   time.Duration
}

// review281Agent leaves a valid build on every writing run. Its reviewer
// asks for one repair, then approves. Delays exercise the time actually
// available to a Backend, without a real agent or a Docker daemon.
type review281Agent struct {
	reviewer bool
	loop     bool
	delays   []time.Duration
	calls    []review281Call
}

const review281Finding = "page/page.go:4: Preserve the requested behavior for the other input too."

func (a *review281Agent) Name() string {
	if a.reviewer {
		return "review-281-reviewer"
	}
	return "review-281-builder"
}

func review281Limit(ctx context.Context, job synth.Job) time.Duration {
	limit := job.Timeout
	if deadline, ok := ctx.Deadline(); ok {
		left := time.Until(deadline)
		if limit <= 0 || left < limit {
			limit = left
		}
	}
	return limit
}

func (a *review281Agent) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	start := time.Now()
	n := len(a.calls) + 1
	a.calls = append(a.calls, review281Call{workspace: job.Workspace, limit: review281Limit(ctx, job)})
	defer func() { a.calls[n-1].elapsed = time.Since(start) }()
	units := n
	tool := "Write"
	if a.reviewer {
		units += 3
		tool = "Read"
	}
	u := synth.Usage{
		Backend: a.Name(), Model: "stand-in", Turns: units, CostUSD: float64(units) / 8,
		Outcome: "success", Summary: "The change is written.", ToolCalls: map[string]int{tool: units},
		Tokens: &synth.Tokens{Input: 100 * units, CachedInput: 20 * units, Output: 40 * units, Reasoning: 10 * units},
	}
	if a.reviewer {
		u.Summary = review281Finding + "\n\nVERDICT: changes needed"
		if n > 1 {
			u.Summary = "The repair addresses the finding.\n\nVERDICT: approve"
		}
	} else {
		file := filepath.Join(job.Workspace, "page", "page.go")
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return u, err
		}
		text := fmt.Sprintf("package page\n\n// Hello says hello after writing run %d.\nfunc Hello() string { return \"hello\" }\n", n)
		if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
			return u, err
		}
	}
	if n <= len(a.delays) && a.delays[n-1] > 0 {
		runCtx := ctx
		cancel := func() {}
		if job.Timeout > 0 {
			runCtx, cancel = context.WithTimeout(ctx, job.Timeout)
		}
		defer cancel()
		timer := time.NewTimer(a.delays[n-1])
		defer timer.Stop()
		select {
		case <-runCtx.Done():
			u.Outcome, u.Summary = "timeout", "The shared build time limit expired."
			return u, runCtx.Err()
		case <-timer.C:
		}
	}
	if !a.reviewer && a.loop && n == 1 {
		u.Outcome = "thinking_loop"
		return u, synth.ErrThinkingLoop
	}
	return u, nil
}

// The initial attempt, its loop-guard fallback, the repair, and both
// reviews all belong to the same build's account. A fallback's partial
// usage is still usage even though its successor finishes the change.
func TestPlumbingReviewRepairCountsEveryAgentsUsage(t *testing.T) {
	root := loopyCheckout(t)
	agent := &review281Agent{loop: true}
	reviewer := &review281Agent{reviewer: true}
	b := Builder{Backend: agent, Reviewer: reviewer, Binary: "invariant", TestRuns: 6,
		Timeout: time.Minute, FallbackEffort: "xhigh", Sandbox: Sandbox{Image: "golang", ModCache: t.TempDir()}}
	r, err := b.Build(context.Background(), root, 7, filepath.Join(t.TempDir(), "build"))
	if err != nil || r == nil {
		t.Fatalf("build = %+v, %v", r, err)
	}
	if len(agent.calls) != 3 || len(reviewer.calls) != 2 || !r.Passed || !r.Approved {
		t.Fatalf("want initial attempt, fallback, repair and two reviews ending approved; builder calls=%d, reviews=%d, result=%+v",
			len(agent.calls), len(reviewer.calls), r)
	}
	for _, call := range agent.calls[1:] {
		if call.workspace != agent.calls[0].workspace {
			t.Errorf("fallback or repair changed workspace from %q to %q", agent.calls[0].workspace, call.workspace)
		}
	}
	if r.Fallback == nil {
		t.Error("the result lost the initial loop-guard fallback")
	}
	// The five calls contribute 1, 2, 3, 4 and 5 units respectively.
	if r.Usage.Turns != 15 || r.Usage.CostUSD != 15.0/8 || r.Spend != 15.0/8 {
		t.Errorf("turns=%d, usage cost=%v, spend=%v; want 15 turns and 1.875 in both cost totals", r.Usage.Turns, r.Usage.CostUSD, r.Spend)
	}
	wantTokens := synth.Tokens{Input: 1500, CachedInput: 300, Output: 600, Reasoning: 150}
	if r.Usage.Tokens == nil || *r.Usage.Tokens != wantTokens {
		t.Errorf("tokens=%+v; want %+v, including the interrupted attempt and every review", r.Usage.Tokens, wantTokens)
	}
	if want := map[string]int{"Write": 6, "Read": 9}; !reflect.DeepEqual(r.Usage.ToolCalls, want) {
		t.Errorf("tool calls=%v; want %v", r.Usage.ToolCalls, want)
	}
}

// Each run fits within a fresh two-second limit, but the initial build,
// review and repair together do not. The repair must receive what remains
// of one build limit, and cannot earn approval after that limit expires.
func TestPlumbingReviewRepairsShareOneTimeLimit(t *testing.T) {
	root := loopyCheckout(t)
	const budget = 2 * time.Second
	const first = 450 * time.Millisecond
	agent := &review281Agent{delays: []time.Duration{first, 1600 * time.Millisecond}}
	reviewer := &review281Agent{reviewer: true, delays: []time.Duration{first}}
	b := Builder{Backend: agent, Reviewer: reviewer, Binary: "invariant", TestRuns: 6,
		Timeout: budget, Sandbox: Sandbox{Image: "golang", ModCache: t.TempDir()}}
	r, err := b.Build(context.Background(), root, 7, filepath.Join(t.TempDir(), "build"))
	if len(agent.calls) < 2 {
		// On a slow host, the shared deadline may expire during checks,
		// before the repair starts. A rejection without a timeout does not
		// establish that a repair was attempted within a shared budget.
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("no repair received the review findings, and the shared deadline did not expire: builder calls=%d, reviews=%d, result=%+v, err=%v",
				len(agent.calls), len(reviewer.calls), r, err)
		}
	} else {
		// Leave scheduling tolerance, but substantially less than the
		// fresh two seconds that would let the 1.6-second repair finish.
		if got, max := agent.calls[1].limit, budget-2*first+150*time.Millisecond; got <= 0 || got > max {
			t.Errorf("repair received %v; want a positive remaining budget no greater than %v, through context or Job.Timeout", got, max)
		}
	}
	if r != nil && r.Approved {
		t.Errorf("a repair that exceeds the build's time limit was approved: %+v", r)
	}
	if len(agent.calls) >= 2 && r == nil {
		t.Errorf("the timed-out repair lost the result needed to publish its draft and findings: %v", err)
	}
	if len(reviewer.calls) > 0 && r != nil && !strings.Contains(r.Review, review281Finding) {
		t.Errorf("the time limit lost the unresolved finding: review=%q", r.Review)
	}
	if len(agent.calls) > 2 || len(reviewer.calls) > 1 {
		t.Errorf("work continued after the repair exhausted the build limit: builder calls=%d, reviews=%d", len(agent.calls), len(reviewer.calls))
	}
	// Measure the stand-ins, so a platform's git startup or workspace
	// cleanup does not turn this into a machine-speed assertion.
	var active time.Duration
	for _, calls := range [][]review281Call{agent.calls, reviewer.calls} {
		for _, call := range calls {
			active += call.elapsed
		}
	}
	if active > budget+250*time.Millisecond {
		t.Errorf("agents used %v with a shared %v build limit", active, budget)
	}
}
