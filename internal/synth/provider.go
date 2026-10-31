package synth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A coding agent's provider is sometimes briefly unavailable (#261): its
// model is at capacity, it limits the account's rate, it's overloaded, or
// it drops the connection. A run that ends that way tries again, in the
// same workspace with the same job and agent, after waits of one, two and
// four minutes: four attempts in all, about seven minutes for the provider
// to come back. A run's attempts are one run. They share its one deadline,
// which starts when its first attempt does, and its limits: its tool calls,
// Claude Code's budget and turns, and Codex's steps. Its usage is theirs
// together, and how it ended is the last attempt's.

// retryWaits are the waits before a run's second, third and fourth
// attempts.
var retryWaits = []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute}

// ErrProviderUnavailable is a run whose provider stayed unavailable: every
// attempt the run could make ended that way.
var ErrProviderUnavailable = errors.New("provider unavailable")

// ProviderError is a run that stopped because its provider stayed
// unavailable: through its four attempts, or until something stopped its
// retries first, such as its deadline or a cost it can't know.
type ProviderError struct {
	Backend  string // the coding agent: codex or claude-code
	Attempts int    // the attempts the run made
	Last     error  // its last attempt's provider error
	Stopped  error  // what stopped its retries before the last one, or nil
}

func (e *ProviderError) Error() string {
	s := fmt.Sprintf("%s provider unavailable after %d attempt", e.Backend, e.Attempts)
	if e.Attempts != 1 {
		s += "s"
	}
	s += ": " + e.Last.Error()
	if e.Stopped != nil {
		s += "; " + e.Stopped.Error()
	}
	return s
}

// Unwrap is the provider's unavailability, and what stopped the retries,
// such as the run's deadline or its cancellation.
func (e *ProviderError) Unwrap() []error {
	if e.Stopped == nil {
		return []error{ErrProviderUnavailable}
	}
	return []error{ErrProviderUnavailable, e.Stopped}
}

// unavailableText is how a ProviderError reads, which is all a saved build
// result or post keeps of it.
var unavailableText = regexp.MustCompile(`provider unavailable after [0-9]+ attempts?`)

// ProviderUnavailable says whether err is a run that stopped because its
// provider stayed unavailable, or is the text of one: a watcher that reads
// a build's error back, after a restart, has only its words (#261).
func ProviderUnavailable(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrProviderUnavailable) || unavailableText.MatchString(err.Error())
}

// unavailableWords are what a provider that's briefly unavailable ends a
// run with: at capacity, limiting the rate, overloaded, or dropping the
// connection, in Codex's words and Claude Code's.
var unavailableWords = []string{
	"at capacity", "over capacity", "capacity unavailable", "high demand", "temporary capacity issue",
	"rate limit", "rate_limit", "rate-limit", "ratelimit", "too many requests", "temporarily limiting requests",
	"overloaded", "high load", "service unavailable", "temporarily unavailable", "usually temporary",
	"connection reset", "connection closed", "connection aborted", "connection dropped", "connection lost",
	"connection to the api was lost", "stream disconnected", "broken pipe", "econnreset", "econnaborted", "epipe",
	"connectionclosed", "und_err_socket", "err_socket_closed", "socket hang up", "error sending request",
	"unexpected eof", "request timed out", "no response from api",
}

// serverStatus is an HTTP status that says the provider failed or limited
// the rate, as Codex reports one after its own retries ran out.
var serverStatus = regexp.MustCompile(`last status: (?:429|5[0-9][0-9])\b`)

// lastingWords are limits on the account that a few minutes won't lift.
var lastingWords = []string{"usage limit", "usage credit", "usage not included", "quota", "billing", "credit", "spend limit", "spend cap"}

// lasting says whether failure, in lower case, names a limit on the account,
// such as its usage limit. A provider that says its limit isn't the
// account's, as Claude Code's "(not your usage limit)" does, names none.
func lasting(failure string) bool {
	failure = strings.ReplaceAll(failure, "not your usage limit", "")
	for _, w := range lastingWords {
		if strings.Contains(failure, w) {
			return true
		}
	}
	return false
}

// transient says whether failure, the words a run's attempt ended with, is
// its provider being briefly unavailable. Invariant's MCP server failing to
// start isn't, even where it says a connection closed: a line that names
// MCP and a failure or an error makes it an ordinary failure. Neither is a
// limit on the account.
func transient(failure string) bool {
	s := strings.ToLower(failure)
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "mcp") && (strings.Contains(line, "fail") || strings.Contains(line, "error") ||
			strings.Contains(line, "handshak") || strings.Contains(line, "initializ")) {
			return false
		}
	}
	if lasting(s) {
		return false
	}
	for _, w := range unavailableWords {
		if strings.Contains(s, w) {
			return true
		}
	}
	return serverStatus.MatchString(s)
}

// progressNotice says whether a Codex error event is only Codex saying it's
// trying again itself, such as "Reconnecting... 2/5", which says nothing of
// how the run ended.
func progressNotice(message string) bool {
	s := strings.ToLower(strings.TrimSpace(message))
	return strings.HasPrefix(s, "reconnecting") || strings.Contains(s, "retrying sampling request") ||
		strings.Contains(s, "waiting to retry") || strings.Contains(s, "waiting for network")
}

// sleep waits d, or until ctx ends, and says which.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// try is how one attempt at a run ended.
type try struct {
	usage    Usage
	err      error // nil when it finished
	provider bool  // it ended because its provider was briefly unavailable
	cut      bool  // the run's deadline or its cancellation ended it
	final    bool  // no attempt may follow it, as when its cost is unknown
}

// retrying runs a job's attempts, one after another, until one finishes,
// one fails another way, or the provider stays unavailable. attempt runs
// one, given what the run has used so far. Before each retry, the run waits
// under ctx, which carries its one deadline, then resume, when there is
// one, takes back what an attempt needs, as Codex's turn. spent says why no
// retry can run, when the run has used up a limit it would need: that's
// the run's error, an ordinary one, since the run would have stopped there
// whatever its provider did. An attempt that ended once ctx was done, after
// an earlier one found the provider unavailable, leaves the provider's
// unavailability as why the run didn't finish: whatever else the attempt
// said, such as an error from a command its deadline refused to start,
// that's the safe reading, since a run its provider stopped publishes
// nothing.
func retrying(ctx context.Context, name string, wait func(context.Context, time.Duration) error,
	attempt func(spent Usage) try, resume func(context.Context) error, spent func(total Usage, last error) error) (Usage, error) {
	if wait == nil {
		wait = sleep
	}
	var total Usage
	var last error
	for n := 1; ; n++ {
		t := attempt(total)
		total = addAttempt(total, t.usage)
		switch {
		case t.err == nil:
			return total, nil
		case t.provider:
			last = t.err
		case last != nil && (t.cut || ctx.Err() != nil):
			stopped := fmt.Errorf("its next attempt didn't finish: %w", t.err)
			if cut := ctx.Err(); cut != nil && !errors.Is(t.err, cut) {
				stopped = fmt.Errorf("its next attempt didn't finish (%w): %w", cut, t.err)
			}
			return total, &ProviderError{Backend: name, Attempts: n, Last: last, Stopped: stopped}
		default:
			return total, t.err
		}
		if t.final || n > len(retryWaits) {
			return total, &ProviderError{Backend: name, Attempts: n, Last: last}
		}
		if err := spent(total, last); err != nil {
			return total, err
		}
		// Even a wait that returns early can't start an attempt past the
		// run's deadline.
		stop := wait(ctx, retryWaits[n-1])
		if stop == nil {
			stop = ctx.Err()
		}
		if stop == nil && resume != nil {
			stop = resume(ctx)
		}
		if stop != nil {
			return total, &ProviderError{Backend: name, Attempts: n, Last: last, Stopped: fmt.Errorf("it stopped waiting to try again: %w", stop)}
		}
	}
}

// addAttempt is a run's usage with one more attempt's: their work summed,
// and how the last one ended.
func addAttempt(total, next Usage) Usage {
	sum := AddUsage(total, next)
	if sum.Model == "" {
		sum.Model = total.Model
	}
	return sum
}

// leftUSD is what's left of a dollar budget once spent is gone, to the
// millionth of a dollar, rounded down: a retry gets what's left of its
// run's budget, never more.
func leftUSD(budget, spent float64) float64 {
	return math.Max(0, math.Floor((budget-spent)*1e6)/1e6)
}

// usd is a dollar amount as --max-budget-usd takes it, to the millionth of
// a dollar, rounded down.
func usd(dollars float64) string {
	return strconv.FormatFloat(math.Floor(dollars*1e6)/1e6, 'f', -1, 64)
}

// ToolAllowanceEnv names, in Invariant's MCP server's environment, the
// allowance it counts its tool calls against: the address where the run
// that started it keeps them (#261). A provider retry starts the server
// again, and a server's own count starts at nothing, so the run keeps the
// count where neither its agent nor a new server can reset it, and every
// server it starts counts each call there before the call does any work.
// Codex gives its servers only the environment it's configured to, so a
// Codex run names it in the server's configuration.
const ToolAllowanceEnv = "INVARIANT_TOOL_ALLOWANCE"

// allowance is the calls one run's servers have made to each tool. The run
// keeps it, and a server can only take a call from it, never give one back.
type allowance struct {
	ln     net.Listener
	mu     sync.Mutex
	calls  map[string]int
	wg     sync.WaitGroup
	closed chan struct{}
}

// newAllowance starts keeping a run's tool calls, for a job with
// Invariant's server, or returns nil for a job without one.
func newAllowance(job Job) (*allowance, error) {
	if job.ReadOnly || len(job.GateServer) == 0 {
		return nil, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("keeping the run's tool calls: %w", err)
	}
	return serveAllowance(ln), nil
}

// acceptBackoff is the most an allowance waits after a failed accept before
// it tries again.
const acceptBackoff = time.Second

// serveAllowance keeps a run's tool calls, answering servers on ln until the
// run closes it. An accept that fails for a while, as when the process has
// run out of file descriptors, stops nothing: it waits a little, longer
// each time up to acceptBackoff, and accepts again.
func serveAllowance(ln net.Listener) *allowance {
	a := &allowance{ln: ln, calls: map[string]int{}, closed: make(chan struct{})}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		wait := 5 * time.Millisecond
		for {
			conn, err := ln.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				select {
				case <-a.closed:
					return
				case <-time.After(wait):
				}
				wait = min(2*wait, acceptBackoff)
				continue
			}
			wait = 5 * time.Millisecond
			a.wg.Add(1)
			go func() {
				defer a.wg.Done()
				a.answer(conn)
			}()
		}
	}()
	return a
}

// answer takes one call from the allowance for a server that asks, of the
// most calls its tool may have, and says which call it is, or 0 for none.
func (a *allowance) answer(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	var ask struct {
		Tool string `json:"tool"`
		Max  int    `json:"max"`
	}
	if json.NewDecoder(conn).Decode(&ask) != nil {
		return
	}
	a.mu.Lock()
	call := 0
	if a.calls[ask.Tool] < ask.Max {
		a.calls[ask.Tool]++
		call = a.calls[ask.Tool]
	}
	a.mu.Unlock()
	json.NewEncoder(conn).Encode(map[string]int{"call": call})
}

// address is where the run's servers find the allowance, or "" for a run
// without one.
func (a *allowance) address() string {
	if a == nil {
		return ""
	}
	return a.ln.Addr().String()
}

// taken is the calls the allowance has given each tool so far.
func (a *allowance) taken() map[string]int {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]int, len(a.calls))
	for tool, n := range a.calls {
		out[tool] = n
	}
	return out
}

// reconcile counts in u the calls the allowance gave during one attempt,
// which began when it had given before. An agent's own events can lose
// calls, as when its provider drops it, and they count the calls the
// allowance refused, so each tool's count is the larger of the two, never
// their sum.
func (a *allowance) reconcile(u *Usage, before map[string]int) {
	for tool, n := range a.taken() {
		n -= before[tool]
		name := "mcp__" + codexServer + "__" + tool
		if n > u.ToolCalls[name] {
			if u.ToolCalls == nil {
				u.ToolCalls = map[string]int{}
			}
			u.ToolCalls[name] = n
		}
	}
}

// close stops keeping the run's calls. A server that asks after it isn't
// given one.
func (a *allowance) close() {
	if a == nil {
		return
	}
	close(a.closed)
	a.ln.Close()
	a.wg.Wait()
}

// TakeToolCall takes one call of tool from the allowance at address, of the
// most calls max its run gives the tool, before the call does any work. It
// returns which call it is, or 0 when none are left. A call it can't count
// mustn't run.
func TakeToolCall(ctx context.Context, address, tool string, max int) (int, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := json.NewEncoder(conn).Encode(map[string]any{"tool": tool, "max": max}); err != nil {
		return 0, err
	}
	var got struct {
		Call *int `json:"call"`
	}
	if err := json.NewDecoder(conn).Decode(&got); err != nil {
		return 0, err
	}
	if got.Call == nil || *got.Call < 0 || *got.Call > max {
		return 0, fmt.Errorf("the run's allowance gave an answer that isn't a call of %d", max)
	}
	return *got.Call, nil
}
