package synth

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// providerUnavailableError preserves the last provider diagnostic and the
// reason no further attempt could run, including cancellation and limits.
type providerUnavailableError struct {
	backend  string
	attempts int
	last     error
	stop     error
}

func (e *providerUnavailableError) Error() string {
	s := fmt.Sprintf("%s provider unavailable after %d attempts: %v", e.backend, e.attempts, e.last)
	if e.stop != nil {
		s += "; retries stopped: " + e.stop.Error()
	}
	return s
}

func (e *providerUnavailableError) Unwrap() []error {
	if e.stop == nil {
		return []error{e.last}
	}
	return []error{e.last, e.stop}
}

// IsProviderUnavailable also recognizes the diagnostic saved in a build's
// result: restarting the watcher must not turn an exhausted provider into
// an acceptable build merely because its leftover files pass the gate.
func IsProviderUnavailable(err error) bool {
	if err == nil {
		return false
	}
	var unavailable *providerUnavailableError
	return errors.As(err, &unavailable) || strings.Contains(strings.ToLower(err.Error()), "provider unavailable")
}

// providerFailure classifies only terminal diagnostics, never normal agent
// text or tool output. MCP startup failures have connection language too;
// keep their full diagnostic here so its context is not lost to truncation.
func providerFailure(err error) bool {
	if err == nil || errors.Is(err, ErrThinkingLoop) || errors.Is(err, ErrTooManyTurns) {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "mcp") && (strings.Contains(line, "failed to start") ||
			strings.Contains(line, "failed to initialize") || strings.Contains(line, "servers failed") ||
			strings.Contains(line, "startup failed") || strings.Contains(line, "startup: failed") ||
			strings.Contains(line, "handshak") && strings.Contains(line, "failed") ||
			strings.Contains(line, "connection closed") || strings.Contains(line, "connection failed")) {
			return false
		}
	}
	for _, phrase := range []string{
		"at capacity", "capacity exceeded", "rate limit", "rate-limit", "rate_limit", "too many requests",
		"overloaded", "overload_error", "http 429", "http 529", "api error: 429", "api error: 529",
		"stream disconnected", "connection reset", "connection closed", "connection aborted",
		"connection dropped", "unexpected eof",
	} {
		if strings.Contains(s, phrase) {
			return true
		}
	}
	return false
}

func waitForProvider(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// A terminal ordinary error wins over a coincident deadline. Attach the
// deadline only when cancellation actually stopped an unfinished process.
func providerAttemptError(ctx context.Context, waitErr, diagnostic error) error {
	if ctx.Err() == nil {
		return diagnostic
	}
	var exit *exec.ExitError
	if errors.Is(waitErr, ctx.Err()) {
		return errors.Join(diagnostic, ctx.Err())
	}
	if errors.As(waitErr, &exit) {
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() && status.Signal() == syscall.SIGKILL {
			return errors.Join(diagnostic, ctx.Err())
		}
	}
	return diagnostic
}

// runProvider keeps a single deadline and allowance across four attempts.
// next receives all prior usage. An attempt must release any turn before
// returning; beforeRetry reacquires it under the same deadline after waiting.
func runProvider(ctx context.Context, backend string, wait func(context.Context, time.Duration) error,
	next func(Usage) (Usage, error), exhausted func(Usage) error, beforeRetry func() error,
) (Usage, error) {
	if wait == nil {
		wait = waitForProvider
	}
	var total Usage
	var last error
	for attempt := 1; attempt <= 4; attempt++ {
		u, err := next(total)
		total = sumProviderUsage(total, u)
		if err == nil {
			return total, nil
		}
		if !providerFailure(err) {
			if last != nil && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return total, &providerUnavailableError{backend, attempt, last, ctx.Err()}
			}
			return total, err
		}
		last = err
		stop := ctx.Err()
		if stop == nil && exhausted != nil {
			stop = exhausted(total)
		}
		if stop != nil || attempt == 4 {
			return total, &providerUnavailableError{backend, attempt, last, stop}
		}
		if err := wait(ctx, time.Minute*time.Duration(1<<(attempt-1))); err != nil {
			return total, &providerUnavailableError{backend, attempt, last, err}
		}
		// Even a custom wait cannot authorize a run after its deadline.
		if err := ctx.Err(); err != nil {
			return total, &providerUnavailableError{backend, attempt, last, err}
		}
		if beforeRetry != nil {
			if err := beforeRetry(); err != nil {
				return total, &providerUnavailableError{backend, attempt, last, err}
			}
		}
		if exhausted != nil {
			if err := exhausted(total); err != nil {
				return total, &providerUnavailableError{backend, attempt, last, err}
			}
		}
	}
	panic("unreachable provider attempt")
}

// sumProviderUsage retains the last attempt's outcome and summary, and sums
// every attempt's work, including attempts with no closing result.
func sumProviderUsage(total, last Usage) Usage {
	last.Turns += total.Turns
	last.CostUSD += total.CostUSD
	last.CostIncomplete = last.CostIncomplete || total.CostIncomplete
	calls := make(map[string]int, len(total.ToolCalls)+len(last.ToolCalls))
	for name, n := range total.ToolCalls {
		calls[name] += n
	}
	for name, n := range last.ToolCalls {
		calls[name] += n
	}
	last.ToolCalls = calls
	if total.Tokens != nil || last.Tokens != nil {
		tokens := Tokens{}
		if total.Tokens != nil {
			tokens = *total.Tokens
		}
		if last.Tokens != nil {
			tokens.Input += last.Tokens.Input
			tokens.Output += last.Tokens.Output
			tokens.CachedInput += last.Tokens.CachedInput
			tokens.Reasoning += last.Tokens.Reasoning
		}
		last.Tokens = &tokens
	}
	return last
}
