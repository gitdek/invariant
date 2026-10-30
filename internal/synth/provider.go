package synth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrProviderUnavailable identifies a run whose provider remained unavailable,
// or whose deadline or cancellation stopped it while retrying that failure.
var ErrProviderUnavailable = errors.New("provider unavailable")

// IsProviderUnavailable also recognizes an error restored from a saved run,
// where only its diagnostic string survives.
func IsProviderUnavailable(err error) bool {
	return err != nil && (errors.Is(err, ErrProviderUnavailable) ||
		strings.Contains(strings.ToLower(err.Error()), ErrProviderUnavailable.Error()))
}

// providerError is only made from a terminal provider error channel, never an
// agent's message or a tool's output. It is internal to a single attempt.
type providerError struct{ message string }

func (e *providerError) Error() string { return e.message }

type providerWait func(context.Context, time.Duration) error

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

// retryProvider gives a run at most four attempts, waiting one, two and four
// minutes. Its initial turn wait precedes the timeout; every subsequent wait,
// including another turn's acquisition, shares the first attempt's deadline.
// A turn is held only while an attempt runs, never during provider backoff.
func retryProvider(ctx context.Context, job Job, wait providerWait,
	turn func(context.Context) (func(), error), run func(context.Context) (Usage, error),
) (Usage, error) {
	if wait == nil {
		wait = waitForProvider
	}
	if turn == nil {
		turn = func(context.Context) (func(), error) { return func() {}, nil }
	}
	release, err := turn(ctx)
	if err != nil {
		return Usage{}, err
	}
	ctx, cancel := within(ctx, job)
	defer cancel()
	var usage Usage
	var last error
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			release()
			return usage, providerStopped(last, err)
		}
		usage, err = run(ctx)
		release()
		if err == nil {
			return usage, nil
		}
		var temporary *providerError
		if !errors.As(err, &temporary) {
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return usage, providerStopped(last, err)
			}
			return usage, err
		}
		last = err
		if ctx.Err() != nil {
			return usage, providerStopped(last, ctx.Err())
		}
		if attempt == 3 {
			return usage, providerStopped(last, nil)
		}
		if err := wait(ctx, time.Minute<<attempt); err != nil {
			return usage, providerStopped(last, err)
		}
		if err := ctx.Err(); err != nil {
			return usage, providerStopped(last, err)
		}
		release, err = turn(ctx)
		if err != nil {
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return usage, providerStopped(last, err)
			}
			return usage, err
		}
	}
}

func providerStopped(last, cause error) error {
	if last == nil {
		return cause
	}
	if cause != nil {
		return fmt.Errorf("%w: %w: %w", ErrProviderUnavailable, last, cause)
	}
	return fmt.Errorf("%w: %w", ErrProviderUnavailable, last)
}

// temporaryProviderMessage recognizes only service capacity, request rate,
// overload and dropped connections. Account limits and configuration errors
// require intervention and are not retried.
func temporaryProviderMessage(message string) bool {
	s := strings.ToLower(message)
	for _, ordinary := range []string{
		"invalid api key", "invalid_api_key", "authentication failed", "authentication_error",
		"unauthorized", "permission denied", "permission_denied", "usage limit", "usage_limit",
		"insufficient_quota", "quota exceeded", "quota_exceeded", "credit balance", "billing",
		"invalid model", "model not found", "model_not_found", "mcp servers failed",
	} {
		if strings.Contains(s, ordinary) {
			return false
		}
	}
	for _, temporary := range []string{
		"at capacity", "capacity exceeded", "capacity_exceeded",
		"rate limit", "rate_limit", "too many requests", "http 429", "api error: 429",
		"overloaded", "overload_error", "http 529", "api error: 529",
		"connection reset", "connection closed", "connection dropped", "connection lost",
		"stream disconnected", "stream closed unexpectedly", "unexpected eof",
		"broken pipe", "socket hang up", "econnreset", "econnaborted",
	} {
		if strings.Contains(s, temporary) {
			return true
		}
	}
	return false
}

// providerErrorText preserves structured error codes as well as their message.
// A plain message retains its existing human-readable form.
func providerErrorText(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	var message string
	if json.Unmarshal(raw, &message) == nil {
		return message
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) == nil {
		if len(fields) == 0 {
			return ""
		}
		if len(fields) == 1 {
			if json.Unmarshal(fields["message"], &message) == nil {
				return message
			}
		}
	}
	return s
}
