package synth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// A provider's outage is what's retried, and nothing else: not a limit on
// the account a few minutes won't lift, not Invariant's MCP server failing
// to start, however its words read, and not Codex's notice that it's
// reconnecting, which says nothing of how a run ended (#261).
func TestTransientTellsAnOutageFromAnOrdinaryFailure(t *testing.T) {
	for failure, want := range map[string]bool{
		"Selected model is at capacity. Please try a different model.":                                             true,
		"stream disconnected before completion: error sending request for url (https://example.invalid/responses)": true,
		"Reconnecting... 2/5": false,
		`API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`:                               true,
		"rate_limit: API Error: 429 rate_limit_error":                                                                              true,
		"You've hit your usage limit. Try again in 3 days.":                                                                        false,
		"exceeded retry limit, last status: 429 Too Many Requests: you exceeded your current quota":                                false,
		"Error: required MCP servers failed to initialize: invariant":                                                              false,
		"mcp: invariant failed: MCP client for `invariant` failed to start: handshaking with MCP server failed: connection closed": false,
		"Invalid API key: authentication failed":                                                                                   false,
		"":                                                                                                                         false,
	} {
		if got := transient(failure); got != want {
			t.Errorf("transient(%q) = %v; want %v", failure, got, want)
		}
	}
}

// A watcher reads a build's error back as text, after a restart too, so a
// provider's unavailability is known from its words as well as its type.
// A limit a retry couldn't renew isn't one, though its error names the
// provider error that came before it.
func TestProviderUnavailableIsReadFromAnErrorOrItsText(t *testing.T) {
	err := &ProviderError{Backend: "codex", Attempts: 2, Last: errors.New("codex stopped: Selected model is at capacity."),
		Stopped: fmt.Errorf("it stopped waiting to try again: %w", context.DeadlineExceeded)}
	for _, e := range []error{err, fmt.Errorf("the agent's run failed: %w", err), errors.New("the review: " + err.Error())} {
		if !ProviderUnavailable(e) {
			t.Errorf("ProviderUnavailable(%v) = false", e)
		}
	}
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrProviderUnavailable) {
		t.Errorf("the error lost its cause: %v", err)
	}
	if !strings.Contains(err.Error(), "codex provider unavailable after 2 attempts: codex stopped: Selected model is at capacity.") {
		t.Errorf("error = %v", err)
	}
	for _, e := range []error{nil, errors.New("claude's run has spent its $1 budget, so it can't try its provider again: claude stopped: error_during_execution: API Error: 529 overloaded_error"),
		fmt.Errorf("%w: all 3 of its steps were spent when its provider failed, so it can't try again: codex stopped: at capacity", ErrTooManyTurns)} {
		if ProviderUnavailable(e) {
			t.Errorf("ProviderUnavailable(%v) = true", e)
		}
	}
}

// Claude Code can report an API error in a result whose subtype is
// success. One that's a provider's outage is retried; any other keeps the
// result it always had.
func TestClaudeRetriesAnOutageItReportsAsASuccessfulError(t *testing.T) {
	outage := providerAttempt{out: `{"type":"result","subtype":"success","is_error":true,"result":"API Error: 529 overloaded_error","num_turns":1,"total_cost_usd":0.125}` + "\n"}
	b, dir := providerBackend(t, "claude-code", outage, providerSuccess("claude-code"))
	b = providerWithWait(b, providerNoWait)
	u, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 5 * time.Second})
	if err != nil || u.Outcome != "success" || providerCount(t, dir) != 2 || u.CostUSD != .125 {
		t.Fatalf("an outage reported as a successful error: usage=%+v, error=%v, attempts=%d", u, err, providerCount(t, dir))
	}
	other := providerAttempt{out: `{"type":"result","subtype":"success","is_error":true,"result":"Credit balance is too low","num_turns":1}` + "\n"}
	b, dir = providerBackend(t, "claude-code", other, providerSuccess("claude-code"))
	b = providerWithWait(b, providerNoWait)
	if u, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 5 * time.Second}); err != nil || u.Outcome != "success" || providerCount(t, dir) != 1 {
		t.Errorf("an ordinary error in a successful result changed: usage=%+v, error=%v, attempts=%d", u, err, providerCount(t, dir))
	}
}

// Codex starts its servers with only the environment it names, so a gated
// job's allowance goes in the server's own configuration, after the server
// itself, and a reviewer, with no server, names none.
func TestCodexNamesTheAllowanceInItsServersEnvironment(t *testing.T) {
	c := Codex{Binary: "codex", Home: t.TempDir()}
	job := Job{Workspace: t.TempDir(), Prompt: "build", GateServer: []string{"/bin/invariant", "mcp"}, allowance: "127.0.0.1:7"}
	args, err := c.args(job, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server, env := -1, -1
	for i, a := range args {
		switch {
		case strings.HasPrefix(a, "mcp_servers.invariant={"):
			server = i
		case a == `mcp_servers.invariant.env={INVARIANT_TOOL_ALLOWANCE="127.0.0.1:7"}`:
			env = i
		}
	}
	if server < 0 || env < server || args[env-1] != "-c" {
		t.Errorf("codex's arguments %q don't name the allowance after its server", args)
	}
	job.ReadOnly = true
	if args, _ := c.args(job, t.TempDir()); strings.Contains(strings.Join(args, " "), ToolAllowanceEnv) {
		t.Errorf("a reviewer's arguments name an allowance: %q", args)
	}
}

// A run's allowance gives each tool its own calls, never more than the most
// its server allows, counts every one it gave, and gives nothing once the
// run has ended.
func TestTheAllowanceGivesEachToolItsCallsOnce(t *testing.T) {
	a, err := newAllowance(Job{GateServer: []string{"invariant", "mcp"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var got []int
	for _, tool := range []string{"check", "check", "check", "test"} {
		n, err := TakeToolCall(ctx, a.address(), tool, 2)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, n)
	}
	if fmt.Sprint(got) != "[1 2 0 1]" {
		t.Errorf("calls given = %v; want [1 2 0 1]", got)
	}
	u := Usage{ToolCalls: map[string]int{"mcp__invariant__check": 3, "Read": 1}}
	a.reconcile(&u, map[string]int{"test": 1})
	if fmt.Sprint(u.ToolCalls) != "map[Read:1 mcp__invariant__check:3]" {
		t.Errorf("tool calls = %v; want the agent's three reported checks, more than the two given, and no test call from before the attempt", u.ToolCalls)
	}
	a.close()
	if n, err := TakeToolCall(ctx, a.address(), "check", 9); err == nil {
		t.Errorf("an ended run's allowance gave call %d", n)
	}
	if a, err := newAllowance(Job{ReadOnly: true, GateServer: []string{"invariant"}}); a != nil || err != nil {
		t.Errorf("a read-only job got an allowance: %v, %v", a, err)
	}
}
