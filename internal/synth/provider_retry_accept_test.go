package synth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// These are CLI stand-ins, not live providers. The public Backend.Run and
// RunAgent APIs exercise the actual parsing, process lifetime and turn locks.
// The policy under test is four attempts, with one-, two- and four-minute
// waits. Tests shorten the actual waits through WithProviderWait, leaving
// the schedule requested by the backend unchanged.
type providerAttempt struct {
	out, stderr, before string
	exit                int
}

func providerCLI(t *testing.T, attempts ...providerAttempt) (string, string) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
n=0
[ ! -f "$here/count" ] || n=$(cat "$here/count")
n=$((n + 1))
printf '%s\n' "$n" > "$here/count"
printf '%s\n' "$@" > "$here/args.$n"
pwd > "$here/workspace.$n"
i=$n
[ -f "$here/out.$i" ] || i=LAST
. "$here/before.$i"
cat "$here/out.$i"
cat "$here/stderr.$i" >&2
exit "$(cat "$here/exit.$i")"
`
	script = strings.ReplaceAll(script, "i=LAST", fmt.Sprintf("i=%d", len(attempts)))
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write("agent", script)
	for i, a := range attempts {
		n := strconv.Itoa(i + 1)
		write("out."+n, a.out)
		write("stderr."+n, a.stderr)
		write("before."+n, a.before)
		write("exit."+n, strconv.Itoa(a.exit))
	}
	return filepath.Join(dir, "agent"), dir
}

func providerJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b) + "\n"
}

const providerCapacity = "Selected model is at capacity. Please try a different model."

func providerCodexFailure(message string) providerAttempt {
	return providerAttempt{out: providerJSON(map[string]any{
		"type": "turn.failed", "error": map[string]string{"message": message},
	}), exit: 1}
}

func providerClaudeFailure(message string) providerAttempt {
	return providerAttempt{out: providerJSON(map[string]any{
		"type": "result", "subtype": "error_during_execution", "is_error": true,
		"errors": []string{message}, "num_turns": 1, "total_cost_usd": .125,
	}), exit: 1}
}

func providerSuccess(agent string) providerAttempt {
	if agent == "codex" {
		return providerAttempt{out: providerJSON(map[string]any{
			"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": "Done."},
		}) + "{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}\n"}
	}
	return providerAttempt{out: "{\"type\":\"result\",\"subtype\":\"success\",\"result\":\"Done.\",\"num_turns\":1}\n"}
}

func providerBackend(t *testing.T, agent string, attempts ...providerAttempt) (Backend, string) {
	t.Helper()
	bin, dir := providerCLI(t, attempts...)
	var b Backend
	if agent == "codex" {
		b = Codex{Binary: bin, Home: t.TempDir(), Model: "fixture-model", Effort: "high", MaxTurns: 20}
	} else {
		b = ClaudeCode{Binary: bin, Model: "fixture-model", Effort: "high", MaxTurns: 20, BudgetUSD: 1}
	}
	return providerWithWait(b, providerScaledWait(12)), dir
}

// WithProviderWait returns the same concrete backend value with a waiting
// function for this instance only; nil restores its cancellable timer.
// The assertion keeps these tests runnable on the base branch, where the
// method is absent and the real Run behavior must still fail each test.
func providerWithWait(b Backend, wait func(context.Context, time.Duration) error) Backend {
	if c, ok := b.(interface {
		WithProviderWait(func(context.Context, time.Duration) error) Backend
	}); ok {
		return c.WithProviderWait(wait)
	}
	return b
}

func providerScaledWait(divisor int64) func(context.Context, time.Duration) error {
	return func(ctx context.Context, delay time.Duration) error {
		timer := time.NewTimer(delay / time.Duration(divisor))
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
}

func providerCount(t *testing.T, dir string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "count"))
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func providerContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func providerUnavailable(t *testing.T, err error, last string) {
	t.Helper()
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "provider") ||
		!strings.Contains(strings.ToLower(err.Error()), "unavailable") || !strings.Contains(err.Error(), last) {
		t.Errorf("failure = %v; want provider unavailable and the last provider error %q", err, last)
	}
}

func TestCodexRetriesCapacityWithTheSameJob(t *testing.T) {
	t.Parallel()
	first := providerCodexFailure(providerCapacity)
	first.before = "printf 'work already done' > partial.txt\n"
	second := providerSuccess("codex")
	second.before = "[ \"$(cat partial.txt)\" = 'work already done' ] || exit 42\n"
	b, dir := providerBackend(t, "codex", first, second)
	var transcript bytes.Buffer
	job := Job{Workspace: t.TempDir(), Prompt: "finish this build", GateServer: []string{"invariant", "mcp"},
		Tools: []string{"test"}, Effort: "high", Transcript: &transcript}
	start := time.Now()
	u, fallback, err := RunAgent(providerContext(t), b, job, 30*time.Second, "xhigh")
	if err != nil || u.Outcome != "success" || fallback != nil {
		t.Fatalf("capacity then success: usage=%+v, fallback=%+v, error=%v", u, fallback, err)
	}
	if n := providerCount(t, dir); n != 2 {
		t.Fatalf("ran %d attempts; want two", n)
	}
	if time.Since(start) < 5*time.Second {
		t.Error("the retry did not observe the configured five-second wait")
	}
	for _, prefix := range []string{"args.", "workspace."} {
		a, err1 := os.ReadFile(filepath.Join(dir, prefix+"1"))
		b, err2 := os.ReadFile(filepath.Join(dir, prefix+"2"))
		// The command's per-attempt temporary directory is allowed to change.
		if prefix == "args." {
			strip := func(s []byte) string {
				var kept []string
				for _, line := range strings.Split(string(s), "\n") {
					if !strings.HasPrefix(line, "shell_environment_policy.set=") {
						kept = append(kept, line)
					}
				}
				return strings.Join(kept, "\n")
			}
			if err1 != nil || err2 != nil || strip(a) != strip(b) {
				t.Errorf("retry changed the job: %q / %q (%v, %v)", a, b, err1, err2)
			}
		} else if err1 != nil || err2 != nil || !bytes.Equal(a, b) {
			t.Errorf("retry changed workspace: %q / %q (%v, %v)", a, b, err1, err2)
		}
	}
	for _, want := range []string{providerCapacity, "turn.completed"} {
		if !strings.Contains(transcript.String(), want) {
			t.Errorf("transcript lost %q", want)
		}
	}
}

func TestCodexRetriesRateLimitsAndDroppedConnections(t *testing.T) {
	t.Parallel()
	cases := map[string]providerAttempt{
		"rate limit":           providerCodexFailure("Rate limit reached for requests; please try again later."),
		"connection":           providerCodexFailure("stream disconnected before completion: connection reset by peer"),
		"error without result": {out: providerJSON(map[string]string{"type": "error", "message": providerCapacity}), exit: 1},
		"stderr":               {stderr: "HTTP 429 Too Many Requests: rate limit exceeded\n", exit: 1},
	}
	for name, first := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b, dir := providerBackend(t, "codex", first, providerSuccess("codex"))
			u, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), Prompt: "review", ReadOnly: true, Timeout: 30 * time.Second})
			if err != nil || u.Outcome != "success" || providerCount(t, dir) != 2 {
				t.Fatalf("temporary provider error was not retried successfully: %+v, %v, attempts=%d", u, err, providerCount(t, dir))
			}
		})
	}
}

func TestClaudeRetriesOverloadAndRateLimits(t *testing.T) {
	t.Parallel()
	cases := map[string]providerAttempt{
		"overloaded result":   providerClaudeFailure("API Error: 529 {\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}"),
		"rate-limited result": providerClaudeFailure("API Error: 429 {\"error\":{\"type\":\"rate_limit_error\",\"message\":\"Too many requests\"}}"),
		"error result text":   {out: "{\"type\":\"result\",\"subtype\":\"error_during_execution\",\"is_error\":true,\"result\":\"API Error: 529 overloaded_error\"}\n", exit: 1},
		"assistant API error": {out: "{\"type\":\"assistant\",\"error\":\"rate_limit\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"API Error: 429 rate_limit_error\"}]}}\n{\"type\":\"result\",\"subtype\":\"error_during_execution\",\"is_error\":true}\n", exit: 1},
		"dropped connection":  {stderr: "API request failed: connection reset by peer\n", exit: 1},
	}
	for name, first := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b, dir := providerBackend(t, "claude-code", first, providerSuccess("claude-code"))
			u, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), Prompt: "draft", GateServer: []string{"invariant", "mcp"}, Tools: []string{"check"}, Timeout: 30 * time.Second})
			if err != nil || u.Outcome != "success" || providerCount(t, dir) != 2 {
				t.Fatalf("temporary provider error was not retried successfully: %+v, %v, attempts=%d", u, err, providerCount(t, dir))
			}
		})
	}
}

func TestProviderRetriesAreBoundedAndKeepTheLastError(t *testing.T) {
	t.Parallel()
	for _, agent := range []string{"codex", "claude-code"} {
		t.Run(agent, func(t *testing.T) {
			t.Parallel()
			failure := providerCodexFailure
			message := providerCapacity
			if agent == "claude-code" {
				failure, message = providerClaudeFailure, "API Error: 529 overloaded_error"
			}
			last := message + " (last attempt)"
			b, dir := providerBackend(t, agent, failure(message+" (first attempt)"), failure(message+" (second attempt)"),
				failure(message+" (third attempt)"), failure(last), providerSuccess(agent))
			b = providerWithWait(b, providerScaledWait(600))
			start := time.Now()
			u, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 35 * time.Second})
			providerUnavailable(t, err, last)
			if n := providerCount(t, dir); n != 4 || u.Outcome == "success" {
				t.Errorf("attempts=%d, usage=%+v; want failure after exactly four attempts", n, u)
			}
			if elapsed := time.Since(start); elapsed < 700*time.Millisecond || elapsed > 5*time.Second {
				t.Errorf("elapsed %s; want configured waits of 100, 200 and 400 milliseconds", elapsed)
			}
		})
	}
}

func TestProviderRetriesUseTheMinuteSchedule(t *testing.T) {
	// Observe the actual delays Run requests for both providers, without
	// sleeping for seven minutes or replacing the retrying code itself.
	for _, agent := range []string{"codex", "claude-code"} {
		t.Run(agent, func(t *testing.T) {
			failure, message := providerCodexFailure, providerCapacity
			if agent == "claude-code" {
				failure, message = providerClaudeFailure, "API Error: 529 overloaded_error"
			}
			last := message + " (fourth attempt)"
			for _, recovers := range []bool{false, true} {
				t.Run(fmt.Sprintf("fourth attempt succeeds=%v", recovers), func(t *testing.T) {
					fourth := failure(last)
					if recovers {
						fourth = providerSuccess(agent)
					}
					b, dir := providerBackend(t, agent, failure(message), failure(message), failure(message), fourth, providerSuccess(agent))
					var delays []time.Duration
					b = providerWithWait(b, func(ctx context.Context, delay time.Duration) error {
						delays = append(delays, delay)
						return ctx.Err()
					})
					u, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 10 * time.Second})
					if recovers {
						if err != nil || u.Outcome != "success" {
							t.Errorf("recovery on fourth attempt: usage=%+v, error=%v", u, err)
						}
					} else {
						providerUnavailable(t, err, last)
						if u.Outcome == "success" {
							t.Error("four failed attempts reported success")
						}
					}
					if n := providerCount(t, dir); n != 4 {
						t.Errorf("attempts=%d; want exactly four", n)
					}
					want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute}
					if !reflect.DeepEqual(delays, want) {
						t.Errorf("requested waits=%v; want %v with no wait before the first attempt or after the fourth", delays, want)
					}
				})
			}
		})
	}
}

func TestProviderRetriesStopAtOrdinaryFailures(t *testing.T) {
	t.Parallel()
	for _, agent := range []string{"codex", "claude-code"} {
		t.Run(agent, func(t *testing.T) {
			t.Parallel()
			failure, temporary := providerCodexFailure, providerCapacity
			if agent == "claude-code" {
				failure, temporary = providerClaudeFailure, "API Error: 529 overloaded_error"
			}
			ordinary := failure("Invalid API key: authentication failed")
			b, dir := providerBackend(t, agent, failure(temporary), ordinary, providerSuccess(agent))
			_, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 30 * time.Second})
			if err == nil || providerCount(t, dir) != 2 || strings.Contains(strings.ToLower(err.Error()), "provider unavailable") {
				t.Errorf("capacity then authentication failure: attempts=%d, error=%v; want stop on the second attempt's ordinary failure", providerCount(t, dir), err)
			}
			// An ordinary failure on the first attempt is never retried either.
			// Words in normal agent output or failed tools are not provider errors.
			for _, message := range []string{"Invalid API key", "usage limit reached", "invalid model", "MCP servers failed to initialize"} {
				b, dir := providerBackend(t, agent, failure(message), providerSuccess(agent))
				_, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true})
				if err == nil || providerCount(t, dir) != 1 {
					t.Errorf("ordinary failure %q: attempts=%d, error=%v", message, providerCount(t, dir), err)
				}
			}
			// Startup errors carry connection language too. Preserve the full
			// diagnostic so this exercises its MCP context, including a failure
			// before any terminal agent event, rather than a bare connection
			// message that could legitimately describe the model provider.
			diagnostic := "mcp: invariant starting\n" +
				"mcp: invariant failed: MCP client for `invariant` failed to start: MCP startup failed: handshaking with MCP server failed: connection closed: initialize response\n" +
				"mcp startup: failed: invariant\n" +
				"Error: required MCP servers failed to initialize: invariant\n"
			cause := "connection closed: initialize response"
			if agent == "claude-code" {
				diagnostic = "MCP server \"invariant\": Connection failed: McpError: MCP error -32000: Connection closed\n" +
					"MCP server \"invariant\": Error message: MCP error -32000: Connection closed\n" +
					"Error: Failed to initialize MCP server \"invariant\"\n"
				cause = "MCP error -32000: Connection closed"
			}
			for _, afterTransient := range []bool{false, true} {
				t.Run(fmt.Sprintf("MCP startup after provider outage=%v", afterTransient), func(t *testing.T) {
					attempts := []providerAttempt{{stderr: diagnostic, exit: 1}, providerSuccess(agent)}
					wantAttempts, wantWaits := 1, 0
					if afterTransient {
						attempts = append([]providerAttempt{failure(temporary)}, attempts...)
						wantAttempts, wantWaits = 2, 1
					}
					b, dir := providerBackend(t, agent, attempts...)
					var waits []time.Duration
					b = providerWithWait(b, func(ctx context.Context, delay time.Duration) error {
						waits = append(waits, delay)
						return ctx.Err()
					})
					u, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(),
						GateServer: []string{"invariant", "mcp"}, Tools: []string{"test"}, Timeout: 10 * time.Second})
					if n := providerCount(t, dir); n != wantAttempts || len(waits) != wantWaits {
						t.Errorf("MCP startup failure: attempts=%d, waits=%v; want %d attempts and %d waits", n, waits, wantAttempts, wantWaits)
					}
					if err == nil || u.Outcome == "success" {
						t.Fatalf("MCP startup failure was accepted: usage=%+v, error=%v", u, err)
					}
					if !strings.Contains(err.Error(), cause) {
						t.Errorf("MCP startup error lost its cause %q: %v", cause, err)
					}
					message := strings.ToLower(err.Error())
					if strings.Contains(message, "provider") && strings.Contains(message, "unavailable") {
						t.Errorf("ordinary MCP startup failure was reported as provider unavailability: %v", err)
					}
				})
			}
			success := providerSuccess(agent)
			if agent == "codex" {
				success.out = "{\"type\":\"error\",\"message\":\"Reconnecting... 1/5\"}\n" +
					"{\"type\":\"item.completed\",\"item\":{\"type\":\"command_execution\",\"aggregated_output\":\"API Error: 429 rate limit exceeded\",\"exit_code\":1,\"status\":\"failed\"}}\n" + success.out
			} else {
				success.out = "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"I fixed the overloaded_error and rate_limit_error handling.\"}]}}\n" + success.out
			}
			b, dir = providerBackend(t, agent, success)
			if _, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true}); err != nil || providerCount(t, dir) != 1 {
				t.Errorf("successful run was retried or rejected: attempts=%d, error=%v", providerCount(t, dir), err)
			}
		})
	}
}

func TestProviderWaitCountsAgainstTheRunTimeout(t *testing.T) {
	t.Parallel()
	for _, agent := range []string{"codex", "claude-code"} {
		t.Run(agent, func(t *testing.T) {
			t.Parallel()
			first, last := providerCodexFailure(providerCapacity), providerCapacity
			if agent == "claude-code" {
				last = "API Error: 529 overloaded_error"
				first = providerClaudeFailure(last)
			}
			b, dir := providerBackend(t, agent, first, providerSuccess(agent))
			b = providerWithWait(b, nil) // exercise the production timer
			start := time.Now()
			_, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 300 * time.Millisecond})
			providerUnavailable(t, err, last)
			if !errors.Is(err, context.DeadlineExceeded) || providerCount(t, dir) != 1 {
				t.Errorf("timeout during backoff: attempts=%d, error=%v; want deadline exceeded without another attempt", providerCount(t, dir), err)
			}
			if elapsed := time.Since(start); elapsed < 250*time.Millisecond || elapsed > 3*time.Second {
				t.Errorf("timeout returned after %s; want the original 300ms budget", elapsed)
			}
		})
	}
}

func TestProviderRetriesKeepTheOriginalDeadline(t *testing.T) {
	t.Parallel()
	b, dir := providerBackend(t, "codex", providerCodexFailure(providerCapacity),
		providerCodexFailure("Rate limit reached on second attempt"), providerSuccess("codex"))
	start := time.Now()
	_, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 6 * time.Second})
	providerUnavailable(t, err, "Rate limit reached on second attempt")
	if !errors.Is(err, context.DeadlineExceeded) || providerCount(t, dir) != 2 {
		t.Errorf("retry reset the time limit: attempts=%d, error=%v", providerCount(t, dir), err)
	}
	if elapsed := time.Since(start); elapsed < 6*time.Second || elapsed > 9*time.Second {
		t.Errorf("elapsed %s; want the original six-second deadline", elapsed)
	}
}

type providerNotice struct {
	once sync.Once
	seen chan struct{}
}

func (w *providerNotice) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("turn.failed")) {
		w.once.Do(func() { close(w.seen) })
	}
	return len(p), nil
}

type providerRunResult struct {
	usage Usage
	err   error
}

func providerAsync(ctx context.Context, b Backend, job Job) <-chan providerRunResult {
	done := make(chan providerRunResult, 1)
	go func() {
		u, err := b.Run(ctx, job)
		done <- providerRunResult{u, err}
		close(done)
	}()
	return done
}

func TestCodexReleasesBothTurnsDuringProviderWait(t *testing.T) {
	// No parallel peers in this package can take the process-wide Codex turn
	// during this assertion; the second backend below is the competing step.
	b, dir := providerBackend(t, "codex", providerCodexFailure(providerCapacity), providerSuccess("codex"))
	c := b.(Codex)
	notice := &providerNotice{seen: make(chan struct{})}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	done := providerAsync(ctx, c, Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 15 * time.Second, Transcript: notice})
	defer func() { cancel(); <-done }()
	select {
	case <-notice.seen:
	case <-ctx.Done():
		t.Fatal("first attempt did not report capacity")
	}
	select {
	case result := <-done:
		t.Fatalf("run returned instead of waiting to retry: %v", result.err)
	case <-time.After(200 * time.Millisecond):
	}
	// A separate descriptor represents another process using the same home.
	f, err := os.OpenFile(filepath.Join(c.Home, "invariant.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		t.Fatalf("provider wait still holds the cross-process turn: %v", err)
	}
	f.Close()
	other, otherDir := providerBackend(t, "codex", providerSuccess("codex"))
	second := other.(Codex)
	second.Home = c.Home
	secondCtx, stop := context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	if u, err := second.Run(secondCtx, Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: time.Second}); err != nil || u.Outcome != "success" {
		t.Fatalf("another step could not take the Codex turn during backoff: %+v, %v", u, err)
	}
	if providerCount(t, dir) != 1 || providerCount(t, otherDir) != 1 {
		t.Fatal("the other step did not run between the first step's attempts")
	}
	result := <-done
	if result.err != nil || result.usage.Outcome != "success" || providerCount(t, dir) != 2 {
		t.Fatalf("first step did not resume successfully: %+v, attempts=%d", result, providerCount(t, dir))
	}
}

func TestCancellingAProviderWaitStopsRetries(t *testing.T) {
	b, dir := providerBackend(t, "codex", providerCodexFailure(providerCapacity), providerSuccess("codex"))
	b = providerWithWait(b, nil) // cancellation must interrupt the real minute wait
	notice := &providerNotice{seen: make(chan struct{})}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	done := providerAsync(ctx, b, Job{Workspace: t.TempDir(), ReadOnly: true, Transcript: notice})
	defer func() { cancel(); <-done }()
	select {
	case <-notice.seen:
	case <-ctx.Done():
		t.Fatal("first attempt did not run")
	}
	select {
	case result := <-done:
		t.Fatalf("run returned instead of waiting to retry: %v", result.err)
	case <-time.After(200 * time.Millisecond):
	}
	cancel()
	select {
	case result := <-done:
		if !errors.Is(result.err, context.Canceled) || providerCount(t, dir) != 1 {
			t.Fatalf("cancellation did not stop backoff: %+v, attempts=%d", result, providerCount(t, dir))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation waited for the retry delay")
	}
}

func TestCodexRetryTurnWaitKeepsTheDeadline(t *testing.T) {
	b, dir := providerBackend(t, "codex", providerCodexFailure(providerCapacity), providerSuccess("codex"))
	c := b.(Codex)
	notice := &providerNotice{seen: make(chan struct{})}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	start := time.Now()
	done := providerAsync(ctx, c, Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 6 * time.Second, Transcript: notice})
	defer func() { cancel(); <-done }()
	select {
	case <-notice.seen:
	case <-ctx.Done():
		t.Fatal("first attempt did not run")
	}
	select {
	case result := <-done:
		t.Fatalf("run returned instead of waiting to retry: %v", result.err)
	case <-time.After(200 * time.Millisecond):
	}
	long := providerSuccess("codex")
	long.before = "while [ ! -f \"$here/release\" ]; do sleep 0.05; done\n"
	other, otherDir := providerBackend(t, "codex", long)
	second := other.(Codex)
	second.Home = c.Home
	otherDone := providerAsync(ctx, second, Job{Workspace: t.TempDir(), ReadOnly: true})
	defer func() { cancel(); <-otherDone }()
	for {
		if _, err := os.Stat(filepath.Join(otherDir, "count")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("the competing step never took its turn")
		case <-time.After(10 * time.Millisecond):
		}
	}
	select {
	case result := <-done:
		providerUnavailable(t, result.err, providerCapacity)
		if !errors.Is(result.err, context.DeadlineExceeded) || providerCount(t, dir) != 1 {
			t.Errorf("waiting to reacquire the turn reset the deadline: %+v, attempts=%d", result, providerCount(t, dir))
		}
		if elapsed := time.Since(start); elapsed < 6*time.Second || elapsed > 9*time.Second {
			t.Errorf("elapsed %s; want the original six-second deadline", elapsed)
		}
	case <-time.After(9 * time.Second):
		t.Fatal("the retry's turn wait did not observe the original deadline")
	}
	if err := os.WriteFile(filepath.Join(otherDir, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if result := <-otherDone; result.err != nil || result.usage.Outcome != "success" {
		t.Errorf("one step's expired retry canceled the competing step: %+v", result)
	}
}
