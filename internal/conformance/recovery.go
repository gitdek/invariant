package conformance

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"

	"github.com/gitdek/invariant/internal/tlc"
)

type recoveryBatch struct {
	check     tlc.ConformanceCheck
	remaining int
	first     int
	result    tlc.Result
	err       error
}

type recoveryJob struct {
	check tlc.ConformanceCheck
	batch *recoveryBatch
}

// checkRanges keeps only active windows and a bounded frontier of their
// halves. Splitting never nests worker pools or retains a parent's module or
// allocation. Earlier logical work survives later failures, including while
// it recovers; cancellation joins every started check before returning.
func checkRanges(ctx context.Context, runner Runner, kind string, count, size int, check func(context.Context, tlc.ConformanceCheck) (tlc.Result, error)) (tlc.ConformanceRange, tlc.Result, error) {
	capable, recovery := runner.(tlc.ConformanceRunner)
	limit := min(runtime.GOMAXPROCS(0), maxAtOnce)
	if recovery && capable.ConformanceConcurrency() > 0 {
		limit = min(limit, capable.ConformanceConcurrency())
	}
	type completion struct {
		job    *recoveryJob
		result tlc.Result
		err    error
	}
	done := make(chan completion, limit)
	active := map[*recoveryJob]context.CancelCauseFunc{}
	var pending []*recoveryJob
	n := batchCount(count, size)
	if kind == "init" {
		n = max(1, n)
	}
	next, first, firstErr := 0, count+1, count+1
	foundRange := tlc.ConformanceRange{Kind: kind, Start: count}
	found, foundErr := tlc.Result{Outcome: tlc.Passed}, error(nil)
	finish := func(job *recoveryJob, result tlc.Result, err error) {
		b := job.batch
		b.remaining--
		if (err != nil || result.Outcome != tlc.Passed) && job.check.Range.Start < b.first {
			b.first, b.result = job.check.Range.Start, result
		}
		b.err = errors.Join(b.err, err)
		if b.remaining == 0 && recovery {
			capable.CompleteConformance(b.check, b.result, b.err)
		}
	}
	for next < n || len(pending) > 0 || len(active) > 0 {
		for ctx.Err() == nil && len(active) < limit {
			var job *recoveryJob
			if len(pending) > 0 {
				job, pending = pending[0], pending[1:]
			} else if next < n && next*size < first {
				c := tlc.ConformanceCheck{Batch: fmt.Sprintf("%s-%d", kind, next+1),
					Range:    tlc.ConformanceRange{Kind: kind, Start: next * size, Count: min(size, count-next*size)},
					Recovery: &tlc.ConformanceRecovery{}}
				job = &recoveryJob{check: c, batch: &recoveryBatch{check: c, remaining: 1, first: count + 1, result: tlc.Result{Outcome: tlc.Passed}}}
				next++
			} else {
				break
			}
			jobCtx, cancel := context.WithCancelCause(ctx)
			active[job] = cancel
			go func() {
				res, err := check(jobCtx, job.check)
				if context.Cause(jobCtx) == errLaterBatch && batchCancellationOnly(err) {
					err = nil
				}
				done <- completion{job, res, err}
			}()
		}
		if len(active) == 0 {
			break
		}
		c := <-done
		active[c.job](nil)
		delete(active, c.job)
		window := c.job.check.Range
		var exhausted *tlc.MemoryExhaustion
		if recovery && window.Count > 1 && errors.As(c.err, &exhausted) && c.err == exhausted && ctx.Err() == nil && window.Start < first {
			exhausted.Split()
			left := c.job.check
			left.Batch += "-left"
			left.Range.Count /= 2
			left.ParentCheck, left.ParentAttempt = exhausted.Check, exhausted.Attempt
			left.StallRetried = exhausted.StallRetried
			right := left
			right.Batch = c.job.check.Batch + "-right"
			right.Range.Start += left.Range.Count
			right.Range.Count = window.Count - left.Range.Count
			c.job.batch.remaining++
			pending = append(pending, &recoveryJob{left, c.job.batch}, &recoveryJob{right, c.job.batch})
			sort.Slice(pending, func(i, j int) bool { return pending[i].check.Range.Start < pending[j].check.Range.Start })
			continue
		}
		if exhausted != nil {
			// Cutoff after cleanup must not turn an unfinished parent into
			// evidence, nor lose a storage error joined to its exhaustion.
			c.result = exhausted.Result
			if c.err == exhausted {
				c.err = ctx.Err()
			}
		}
		if c.err != nil && window.Start < firstErr {
			firstErr, foundErr = window.Start, c.err
		}
		if (c.err != nil || c.result.Outcome != tlc.Passed) && window.Start < first {
			first, foundRange, found = window.Start, window, c.result
			for job, cancel := range active {
				if job.check.Range.Start > first {
					cancel(errLaterBatch)
				}
			}
			kept := pending[:0]
			for _, job := range pending {
				if job.check.Range.Start > first {
					finish(job, tlc.Result{}, nil)
				} else {
					kept = append(kept, job)
				}
			}
			pending = kept
		}
		finish(c.job, c.result, c.err)
	}
	for _, job := range pending {
		finish(job, tlc.Result{}, ctx.Err())
	}
	return foundRange, found, errors.Join(foundErr, ctx.Err())
}
