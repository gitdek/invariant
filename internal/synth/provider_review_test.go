package synth

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Codex says its provider is briefly unavailable in its own words, as
// codex-cli 0.163.0 has them: high demand after its own retries, a 5xx
// status once those ran out, and flex capacity. Limits on the account, in
// its words too, and an MCP server that didn't start are still ordinary.
func TestCodexsOwnOutageMessagesAreTransient(t *testing.T) {
	for failure, want := range map[string]bool{
		"We're currently experiencing high demand, which may cause temporary errors.":                    true,
		"exceeded retry limit, last status: 500 Internal Server Error, request id: req_1":                true,
		"exceeded retry limit, last status: 502 Bad Gateway":                                             true,
		"exceeded retry limit, last status: 504 Gateway Timeout":                                         true,
		"Flex capacity unavailable.":                                                                     true,
		"exceeded retry limit, last status: 401 Unauthorized":                                            false,
		"Quota exceeded. Check your plan and billing details.":                                           false,
		"You're out of credits. Your workspace is out of credits. Add credits to continue using Codex.":  false,
		"You've reached your workspace credit limit":                                                     false,
		"You hit your spend cap for this month":                                                          false,
		"You've hit your usage limit. Visit https://chatgpt.com/settings/usage to purchase more credits": false,
		"mcp: invariant failed: MCP client for `invariant` failed to start: MCP startup failed: handshaking with MCP server failed: connection closed: initialize response": false,
	} {
		if got := transient(failure); got != want {
			t.Errorf("transient(%q) = %v; want %v", failure, got, want)
		}
	}
	b, dir := providerBackend(t, "codex", providerCodexFailure("We're currently experiencing high demand, which may cause temporary errors."), providerSuccess("codex"))
	b = providerWithWait(b, providerNoWait)
	if u, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 5 * time.Second}); err != nil || u.Outcome != "success" || providerCount(t, dir) != 2 {
		t.Errorf("high demand: usage=%+v, error=%v, attempts=%d; want a retry that succeeds", u, err, providerCount(t, dir))
	}
}

// Claude Code says what an API error was in its own fields: the error's
// kind, the API's status and a typed cause, as Claude Code 2.1.311 emits
// them. They decide where they're given, with the words that name a limit
// on the account still winning, since Claude Code reports a usage limit as a
// rate limit; its own "(not your usage limit)" names none. Only without the
// fields do the words decide, and Claude Code's own words for an outage and
// a dropped connection are an outage's.
func TestClaudeOutagesAreReadFromItsOwnFields(t *testing.T) {
	for _, c := range []struct {
		end     claudeEnd
		failure string
		want    bool
	}{
		{claudeEnd{kind: "rate_limit", status: 429}, "API Error: Server is temporarily limiting requests (not your usage limit) · Rate limited", true},
		{claudeEnd{kind: "rate_limit", status: 429}, "API Error: Request rejected (429) · this may be a temporary capacity issue.", true},
		{claudeEnd{status: 529}, "API Error: something new", true},
		{claudeEnd{kind: "server_error"}, "API Error: Connection lost before a response was produced. Try again.", true},
		{claudeEnd{cause: "no_response"}, "API Error: No response from API", true},
		{claudeEnd{cause: "a_cause_claude_adds_later", status: 503}, "API Error: Service busy", true},
		{claudeEnd{kind: "rate_limit", status: 429, cause: "usage_limit_reached"}, "Usage limit reached · limit resets 5pm", false},
		{claudeEnd{kind: "rate_limit", status: 429}, "You're out of usage credits", false},
		{claudeEnd{kind: "authentication_failed", status: 401}, "API Error: rate limit or not, the key is invalid", false},
		{claudeEnd{kind: "billing_error"}, "spend limit reached (daily)", false},
		{claudeEnd{kind: "unknown"}, "API Error: Connection dropped (EPIPE)", true},
		{claudeEnd{}, "Opus is experiencing high load. Switch to Sonnet.", true},
		{claudeEnd{}, "API Error: Connection dropped (ECONNABORTED)", true},
		{claudeEnd{}, "API Error: Connection dropped (ConnectionClosed)", true},
		{claudeEnd{}, "API Error: Connection dropped (UND_ERR_SOCKET)", true},
		{claudeEnd{}, "API Error: Request timed out", true},
		{claudeEnd{}, "API Error: No response from API within the first-byte window", true},
		{claudeEnd{}, "API Error: Server is temporarily limiting requests (not your usage limit)", true},
		{claudeEnd{}, "Usage limit reached · limit resets 5pm", false},
		{claudeEnd{}, "Invalid API key · Please run /login", false},
	} {
		if got := c.end.unavailable(c.failure); got != c.want {
			t.Errorf("%+v.unavailable(%q) = %v; want %v", c.end, c.failure, got, c.want)
		}
	}
	limiting := `{"type":"assistant","error":"rate_limit","api_error_status":429,"message":{"id":"m1","content":[{"type":"text","text":"API Error: Server is temporarily limiting requests (not your usage limit) · Rate limited"}]}}` + "\n" +
		`{"type":"result","subtype":"success","is_error":true,"api_error_status":429,"result":"API Error: Server is temporarily limiting requests (not your usage limit) · Rate limited","num_turns":1}` + "\n"
	b, dir := providerBackend(t, "claude-code", providerAttempt{out: limiting, exit: 1}, providerSuccess("claude-code"))
	b = providerWithWait(b, providerNoWait)
	if u, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 5 * time.Second}); err != nil || u.Outcome != "success" || providerCount(t, dir) != 2 {
		t.Errorf("a server limiting requests: usage=%+v, error=%v, attempts=%d; want a retry that succeeds", u, err, providerCount(t, dir))
	}
	limited := `{"type":"assistant","error":"rate_limit","api_error_status":429,"api_error":"usage_limit_reached","message":{"id":"m1","content":[{"type":"text","text":"Usage limit reached · limit resets 5pm"}]}}` + "\n" +
		`{"type":"result","subtype":"success","is_error":true,"api_error_status":429,"api_error":"usage_limit_reached","result":"Usage limit reached · limit resets 5pm","num_turns":1}` + "\n"
	b, dir = providerBackend(t, "claude-code", providerAttempt{out: limited, exit: 1}, providerSuccess("claude-code"))
	b = providerWithWait(b, providerNoWait)
	if _, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 5 * time.Second}); providerCount(t, dir) != 1 || ProviderUnavailable(err) {
		t.Errorf("a usage limit: error=%v, attempts=%d; want one attempt and no provider unavailability", err, providerCount(t, dir))
	}
}

// An attempt that ends once the run's deadline has passed, after an earlier
// attempt found the provider unavailable, leaves the provider's
// unavailability as why the run didn't finish, whatever the attempt said,
// as when its command couldn't start because the deadline had passed.
func TestADeadlineAfterAProviderFailureStaysTheProvidersFault(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := 0
	_, err := retrying(ctx, "codex", providerNoWait, func(Usage) try {
		n++
		if n == 1 {
			return try{err: errors.New("codex stopped: Selected model is at capacity."), provider: true}
		}
		cancel()
		return try{err: ctx.Err()} // as exec.Cmd.Start reports a done context
	}, nil, func(Usage, error) error { return nil })
	if !ProviderUnavailable(err) || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "at capacity") {
		t.Errorf("error = %v; want provider unavailable, with the cancellation and the last provider error", err)
	}
}

// A provider failure an agent reported is why its attempt ended, even when
// the run's deadline passes before the attempt sees its agent end: a first
// attempt's outage doesn't become an ordinary timeout.
func TestAReportedOutageOutlivesTheDeadlineThatEndsItsAttempt(t *testing.T) {
	for agent, report := range map[string]string{
		"codex":       providerJSON(map[string]any{"type": "turn.failed", "error": map[string]string{"message": providerCapacity}}),
		"claude-code": `{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["API Error: 529 overloaded_error"],"num_turns":1,"total_cost_usd":0.125}` + "\n",
	} {
		t.Run(agent, func(t *testing.T) {
			hang := providerAttempt{out: report, before: "cat \"$here/out.$i\"\nexec sleep 30\n"}
			b, dir := providerBackend(t, agent, hang, providerSuccess(agent))
			b = providerWithWait(b, nil) // the real wait, which the deadline cuts
			_, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 500 * time.Millisecond})
			if !ProviderUnavailable(err) || !errors.Is(err, context.DeadlineExceeded) || providerCount(t, dir) != 1 {
				t.Errorf("error = %v, attempts = %d; want provider unavailable at the deadline after one attempt", err, providerCount(t, dir))
			}
		})
	}
}

// Codex says "Reconnecting..." while it tries again itself. That notice says
// nothing of how a run ended: a crash after it is an ordinary failure, and an
// outage after it, or before it, is still an outage.
func TestCodexsReconnectNoticesDontSayHowARunEnded(t *testing.T) {
	notice := providerJSON(map[string]string{"type": "error", "message": "Reconnecting... 2/5"})
	capacity := providerJSON(map[string]string{"type": "error", "message": providerCapacity})
	crash := providerAttempt{out: notice, stderr: "thread 'main' panicked at core/src/codex.rs\n", exit: 101}
	b, dir := providerBackend(t, "codex", crash, providerSuccess("codex"))
	b = providerWithWait(b, providerNoWait)
	if _, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 5 * time.Second}); err == nil || ProviderUnavailable(err) || providerCount(t, dir) != 1 {
		t.Errorf("a crash after a reconnect notice: error=%v, attempts=%d; want one ordinary failure", err, providerCount(t, dir))
	}
	for name, out := range map[string]string{"after": notice + capacity, "before": capacity + notice} {
		b, dir := providerBackend(t, "codex", providerAttempt{out: out, exit: 1}, providerSuccess("codex"))
		b = providerWithWait(b, providerNoWait)
		if u, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 5 * time.Second}); err != nil || u.Outcome != "success" || providerCount(t, dir) != 2 {
			t.Errorf("an outage %s a reconnect notice: usage=%+v, error=%v, attempts=%d; want a retry that succeeds", name, u, err, providerCount(t, dir))
		}
	}
}

// flakyListener fails its first accepts, as a process out of file
// descriptors does, then accepts as its listener does.
type flakyListener struct {
	net.Listener
	fails int
}

func (l *flakyListener) Accept() (net.Conn, error) {
	if l.fails > 0 {
		l.fails--
		return nil, &net.OpError{Op: "accept", Net: "tcp", Err: syscall.EMFILE}
	}
	return l.Listener.Accept()
}

// A run's allowance keeps answering after accepts that fail for a while, and
// stops only when the run closes it.
func TestTheAllowanceOutlivesAFailedAccept(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := serveAllowance(&flakyListener{Listener: ln, fails: 3})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if n, err := TakeToolCall(ctx, a.address(), "check", 2); err != nil || n != 1 {
		t.Errorf("after failed accepts: call %d, %v; want the first call", n, err)
	}
	done := make(chan struct{})
	go func() { a.close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the allowance didn't stop when the run closed it")
	}
}
