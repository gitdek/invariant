package tlc

import (
	"context"
	"sync"
	"time"
)

// ConformanceRange is a zero-based, half-open window of logical obligations.
// Kind is init, steps, strict or tried; strict units are individual attempts.
type ConformanceRange struct {
	Kind  string `json:"kind"`
	Start int    `json:"start"`
	Count int    `json:"count"`
}

// ConformanceCheck describes one generated window. Children share Recovery
// with their original batch and name the exhausted attempt they replace.
type ConformanceCheck struct {
	Batch         string
	Range         ConformanceRange
	ParentCheck   string
	ParentAttempt int
	Recovery      *ConformanceRecovery
	StallRetried  bool // inherited from the parent, never spent by a sibling
}

// ConformanceRunner is optional: ordinary Check implementations need not
// classify exhaustion or understand recovery. Completion describes the whole
// original batch, after all started children have joined, without TLC counts.
// A positive concurrency limit can further restrict the checker's scheduler.
type ConformanceRunner interface {
	CheckConformance(context.Context, string, string, Config, ConformanceCheck) (Result, error)
	CompleteConformance(ConformanceCheck, Result, error)
	ConformanceConcurrency() int
}

// MemoryExhaustion is returned only after a divisible window's confirmed Java
// memory failure and joined cleanup. Its Result contains no partial evidence.
type MemoryExhaustion struct {
	Check        string
	Attempt      int
	Result       Result
	StallRetried bool
	budget       *Budget
	check        int
}

func (e *MemoryExhaustion) Error() string { return e.Result.Message }

// Split records the scheduler's decision to replace this attempt with halves.
// Generation cleanup or cancellation can still prevent recovery after TLC
// returns, so offering recovery alone does not mark the attempt as split.
func (e *MemoryExhaustion) Split() {
	if e.budget != nil {
		e.budget.mu.Lock()
		defer e.budget.mu.Unlock()
		e.budget.checks[e.check].attempts[e.Attempt-1].Split = true
	}
}

// ConformanceRecovery owns the original admitted execution allowance across
// a recovery tree. Set Timeout before its first use;
// zero leaves only the caller's deadline. Admission waits after the first
// attempt consume the same allowance, including waits for singleton retries.
type ConformanceRecovery struct {
	Timeout  time.Duration
	mu       sync.Mutex
	deadline time.Time
}

func (r *ConformanceRecovery) context(ctx context.Context, admitted bool) (context.Context, context.CancelFunc) {
	r.mu.Lock()
	if admitted && r.deadline.IsZero() && r.Timeout > 0 {
		r.deadline = time.Now().Add(r.Timeout)
	}
	deadline := r.deadline
	r.mu.Unlock()
	if !deadline.IsZero() {
		return context.WithDeadline(ctx, deadline)
	}
	return ctx, func() {}
}

func (r Runner) CheckConformance(ctx context.Context, dir, module string, cfg Config, c ConformanceCheck) (Result, error) {
	if c.Recovery == nil {
		c.Recovery = &ConformanceRecovery{}
	}
	return r.check(ctx, dir, module, cfg, 0, &c)
}

func (r Runner) CompleteConformance(ConformanceCheck, Result, error) {}

func (r Runner) ConformanceConcurrency() int {
	if r.Budget != nil {
		return r.Budget.Parallel
	}
	return 1
}
