package synth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrProviderUnavailable identifies a run that could not finish because its
// provider stayed unavailable. Its error also retains the last provider
// diagnostic and, when applicable, the deadline, cancellation or limit.
var ErrProviderUnavailable = errors.New("agent provider unavailable")

func IsProviderUnavailable(err error) bool { return errors.Is(err, ErrProviderUnavailable) }

// temporaryProviderError is only made from a terminal CLI diagnostic, never
// from a tool's output or a successful agent's message.
type temporaryProviderError struct{ error }

func (e temporaryProviderError) Unwrap() error { return e.error }

func providerError(err error, diagnostic string) error {
	message := strings.ToLower(diagnostic)
	for _, marker := range []string{
		"at capacity", "overloaded", "overload_error", "rate limit", "rate_limit", "rate-limit",
		"too many requests", "http 429", "api error: 429", "api error: 529",
		"stream disconnected", "connection reset", "connection closed", "connection aborted",
		"connection terminated", "server disconnected", "socket hang up", "unexpected eof", "broken pipe",
	} {
		if strings.Contains(message, marker) {
			return temporaryProviderError{err}
		}
	}
	return err
}

func unavailable(last, cause error) error {
	if cause != nil {
		return fmt.Errorf("%w: %v; %w", ErrProviderUnavailable, last, cause)
	}
	return fmt.Errorf("%w after four attempts: %v", ErrProviderUnavailable, last)
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

// runProvider keeps one deadline and one usage account for every attempt.
// Only the first turn wait precedes that deadline; retry waits and subsequent
// turn acquisition spend the same original allowance. Every attempt releases
// its turn before inspecting or waiting on a provider failure.
func runProvider(ctx context.Context, job Job, wait func(context.Context, time.Duration) error,
	turn func(context.Context) (func(), error), attempt func(context.Context, Usage) (Usage, error),
	limit func(Usage) error) (Usage, error) {
	if wait == nil {
		wait = waitForProvider
	}
	if turn == nil {
		turn = func(ctx context.Context) (func(), error) { return func() {}, ctx.Err() }
	}
	release, err := turn(ctx)
	if err != nil {
		return Usage{}, err
	}
	ctx, cancel := within(ctx, job)
	defer cancel()
	var total Usage
	var last error
	for i := 0; i < 4; i++ {
		if err := ctx.Err(); err != nil {
			release()
			if last != nil {
				return total, unavailable(last, err)
			}
			return total, err
		}
		if i > 0 && limit != nil {
			if err := limit(total); err != nil {
				release()
				return total, unavailable(last, err)
			}
		}
		usage, err := attempt(ctx, total)
		release()
		total = addProviderUsage(total, usage)
		if err == nil {
			return total, nil
		}
		var temporary temporaryProviderError
		if !errors.As(err, &temporary) {
			if last != nil && ctx.Err() != nil {
				return total, unavailable(last, ctx.Err())
			}
			return total, err
		}
		last = err
		if err := ctx.Err(); err != nil {
			return total, unavailable(last, err)
		}
		if limit != nil {
			if err := limit(total); err != nil {
				return total, unavailable(last, err)
			}
		}
		if i == 3 {
			return total, unavailable(last, nil)
		}
		if err := wait(ctx, time.Minute<<i); err != nil {
			return total, unavailable(last, err)
		}
		// An injected waiter may return just as cancellation arrives.
		if err := ctx.Err(); err != nil {
			return total, unavailable(last, err)
		}
		release, err = turn(ctx)
		if err != nil {
			return total, unavailable(last, err)
		}
	}
	panic("unreachable provider attempt")
}

// addProviderUsage preserves the most recent outcome and sums work even
// when an earlier attempt stopped without a closing result.
func addProviderUsage(previous, current Usage) Usage {
	current.CostUSD += previous.CostUSD
	current.Turns += previous.Turns
	if current.ToolCalls == nil {
		current.ToolCalls = make(map[string]int)
	}
	for name, count := range previous.ToolCalls {
		current.ToolCalls[name] += count
	}
	if previous.Tokens != nil {
		if current.Tokens == nil {
			current.Tokens = &Tokens{}
		}
		current.Tokens.Input += previous.Tokens.Input
		current.Tokens.CachedInput += previous.Tokens.CachedInput
		current.Tokens.Output += previous.Tokens.Output
		current.Tokens.Reasoning += previous.Tokens.Reasoning
	}
	return current
}
