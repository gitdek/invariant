package synth

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ClaudeCode runs the official Claude Code CLI headlessly (claude -p), on the
// user's own account and machine (D-0028). The agent has the file tools and
// Invariant's own MCP tools, and nothing else: no shell, no web, no
// subagents, so any code it writes runs only inside the gate's sandbox. It
// can't read anything in the user's home directory either, so nothing it
// writes, which the factory may commit, can carry a secret from the host
// (D-0037). Its workspace must be outside the home directory, as the system
// temporary directory is.
type ClaudeCode struct {
	Binary    string  // the claude CLI
	Model     string  // "opus", "sonnet" or a full model name
	BudgetUSD float64 // --max-budget-usd: a cap on the run's estimated cost
	MaxTurns  int
	// Effort is --effort: how hard the agent thinks, low, medium, high, xhigh
	// or max. Empty leaves Claude Code's own default, which let one build's
	// agent spend every turn's whole output on thinking, and never act. A job
	// that names its own effort, as a build's fallback run does, thinks at
	// that instead (D-0125).
	Effort string
	// wait is how a run waits before it tries its provider again, or nil
	// for the timer (#261).
	wait func(context.Context, time.Duration) error
}

func (c ClaudeCode) Name() string { return "claude-code" }

// WithProviderWait returns c with wait in place of the timer a run waits on
// before it tries its provider again, as a test does to keep the minutes
// short; nil restores the timer. The waits the run asks for are the same.
func (c ClaudeCode) WithProviderWait(wait func(context.Context, time.Duration) error) Backend {
	c.wait = wait
	return c
}

// fileTools are the only built-in tools an agent gets.
const fileTools = "Read,Write,Edit,Glob,Grep"

// readTools are a reviewer's tools: it reads, and changes nothing.
const readTools = "Read,Glob,Grep"

func (c ClaudeCode) args(job Job) ([]string, error) {
	if job.ReadOnly {
		return append([]string{
			"-p", job.Prompt,
			"--output-format", "stream-json", "--verbose",
			"--model", c.Model,
			"--max-budget-usd", usd(c.BudgetUSD),
			"--max-turns", strconv.Itoa(c.MaxTurns),
			"--strict-mcp-config",
			"--tools", readTools,
			"--allowedTools", readTools,
			"--disallowedTools", "Bash,Write,Edit,WebFetch,WebSearch,Task,NotebookEdit,Read(~/**),Glob(~/**),Grep(~/**)",
			"--setting-sources", "project",
			"--no-session-persistence",
		}, c.effort(job)...), nil
	}
	server := map[string]any{"command": job.GateServer[0], "args": job.GateServer[1:]}
	if job.allowance != "" {
		server["env"] = map[string]string{ToolAllowanceEnv: job.allowance}
	}
	config, err := json.Marshal(map[string]any{"mcpServers": map[string]any{"invariant": server}})
	if err != nil {
		return nil, err
	}
	allowed := fileTools
	tools := job.Tools
	if len(tools) == 0 {
		tools = []string{"gate"}
	}
	for _, t := range tools {
		allowed += ",mcp__invariant__" + t
	}
	return append([]string{
		"-p", job.Prompt,
		"--output-format", "stream-json", "--verbose",
		"--model", c.Model,
		"--max-budget-usd", usd(c.BudgetUSD),
		"--max-turns", strconv.Itoa(c.MaxTurns),
		"--mcp-config", string(config), "--strict-mcp-config",
		// --tools sets which built-in tools exist at all; --allowedTools only
		// pre-approves some of them.
		"--tools", fileTools,
		"--allowedTools", allowed,
		"--disallowedTools", "Bash,WebFetch,WebSearch,Task,NotebookEdit,Read(~/**),Edit(~/**),Write(~/**),Glob(~/**),Grep(~/**)",
		"--permission-mode", "acceptEdits",
		"--setting-sources", "project",
		"--no-session-persistence",
	}, c.effort(job)...), nil
}

// effort is the --effort flag: the job's effort, when it names one, or else
// the agent's own, when one is set.
func (c ClaudeCode) effort(job Job) []string {
	effort := job.Effort
	if effort == "" {
		effort = c.Effort
	}
	if effort == "" {
		return nil
	}
	return []string{"--effort", effort}
}

// Run runs the job, trying again when Claude Code's provider is briefly
// unavailable (#261). Its attempts share the run's time, its tool calls,
// and its budget and turns: each attempt gets only what the ones before it
// left.
func (c ClaudeCode) Run(ctx context.Context, job Job) (Usage, error) {
	if _, err := c.args(job); err != nil {
		return Usage{}, err
	}
	ctx, cancel := within(ctx, job)
	defer cancel()
	tools, err := newAllowance(job)
	if err != nil {
		return Usage{}, err
	}
	defer tools.close()
	job.allowance = tools.address()
	return retrying(ctx, c.Name(), c.wait,
		func(spent Usage) try {
			before := tools.taken()
			t := c.attempt(ctx, job, spent)
			tools.reconcile(&t.usage, before)
			return t
		}, nil,
		func(total Usage, last error) error {
			if c.BudgetUSD > 0 && leftUSD(c.BudgetUSD, total.CostUSD) <= 0 {
				return fmt.Errorf("claude's run has spent its $%s budget, so it can't try its provider again: %v", usd(c.BudgetUSD), last)
			}
			if c.MaxTurns > 0 && total.Turns >= c.MaxTurns {
				return fmt.Errorf("claude's run has taken all %d of its turns, so it can't try its provider again: %v", c.MaxTurns, last)
			}
			return nil
		})
}

// attempt runs claude once, under ctx, the run's deadline, with what spent,
// the attempts before it, left of the run's budget and turns.
func (c ClaudeCode) attempt(ctx context.Context, job Job, spent Usage) try {
	left := c
	if c.BudgetUSD > 0 {
		left.BudgetUSD = leftUSD(c.BudgetUSD, spent.CostUSD)
	}
	if c.MaxTurns > 0 {
		left.MaxTurns = c.MaxTurns - spent.Turns
	}
	args, err := left.args(job)
	if err != nil {
		return try{err: err}
	}
	run, stop := context.WithCancel(ctx)
	defer stop()
	cmd := exec.CommandContext(run, c.Binary, args...)
	cmd.Dir = job.Workspace
	// The gate takes a while; give its tool calls time.
	cmd.Env = append(os.Environ(), "MCP_TOOL_TIMEOUT=900000", "MCP_TIMEOUT=60000")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return try{err: err}
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		// A run whose deadline has passed starts nothing.
		return try{err: err, cut: ctx.Err() != nil}
	}
	usage, end, readErr := readClaudeStream(stdout, job.Transcript)
	if readErr != nil {
		stop() // stop the agent, rather than wait for it
	}
	waitErr := cmd.Wait()
	// The result, when there's one, alone says why the attempt failed, in
	// its fields and its words. Without one, an API error Claude Code
	// reported in place of a reply does, and only without that, stderr's
	// words. stderr is otherwise diagnostic only.
	reported := end.result || end.api != ""
	failure := end.failure
	if failure == "" {
		failure = end.api
	}
	if !reported {
		failure = stderr.String()
	}
	diag := lastLines(stderr.String(), 20)
	var why string
	switch {
	case readErr != nil:
	case !end.result:
		why = fmt.Sprintf("claude ended without a result: %v\n%s", waitErr, diag)
		// What the agent did before the stream ended is all the run has of
		// its turns.
		usage.Turns = end.replies
	case usage.Outcome == "success" && !(end.isError && end.unavailable(failure)):
		return try{usage: usage}
	default:
		if usage.Outcome == "success" || usage.Outcome == "" {
			usage.Outcome = "error" // a result that's an error, as an API error's is
		}
		why = "claude stopped: " + usage.Outcome
		if failure != "" {
			why += ": " + failure
		}
		if diag != "" {
			why += "\n" + diag
		}
	}
	err = readErr
	if err == nil {
		err = errors.New(strings.TrimRight(why, "\n"))
	}
	t := try{usage: usage, err: err}
	switch cut := ctx.Err(); {
	case readErr == nil && reported && end.unavailable(failure):
		// A provider's failure Claude Code reported is why the attempt ended,
		// even when the run's deadline passed before the attempt saw it end.
		t.provider = true
	case cut != nil:
		// Otherwise the run's deadline or its cancellation ended it.
		t.err, t.cut = fmt.Errorf("the run was cut off (%w): %w", cut, err), true
	case readErr == nil && !reported:
		t.provider = transient(failure)
	}
	// Claude Code says what a run cost only in its result. One that ended
	// without it, after the provider sent paid replies, may have spent all
	// the run had left, so it counts as having spent exactly that, and no
	// retry can renew the budget: a conservative estimate (#261).
	if readErr == nil && !end.result && end.paid && c.BudgetUSD > 0 {
		t.usage.CostUSD, t.usage.CostIncomplete, t.final = left.BudgetUSD, true, true
		t.err = fmt.Errorf("%w; this attempt ended before Claude Code said what it cost, so the rest of the run's budget, $%s, counts as spent: a conservative estimate, since its real cost is unknown", t.err, usd(left.BudgetUSD))
	}
	return t
}

// ErrThinkingLoop is a run the factory stopped because the agent's replies
// kept ending at the output limit with nothing but thinking. Claude Code
// asks the agent to carry on each time, and without the thinking it lost,
// it can start the same reasoning again, and never act.
var ErrThinkingLoop = fmt.Errorf("the agent's last %d replies each ran out of room while it was still thinking, before it did anything, so the factory stopped it", maxThinkingOnly)

// maxThinkingOnly is how many replies in a row may end at the output limit
// with nothing but thinking before the factory stops the run.
const maxThinkingOnly = 2

// ReadStream reads Claude Code's stream-json events, copying each to
// transcript, and returns what the closing result event reports. It stops
// early, with ErrThinkingLoop, once the agent's replies keep ending with
// nothing but thinking.
func ReadStream(r io.Reader, transcript io.Writer) (Usage, error) {
	u, _, err := readClaudeStream(r, transcript)
	return u, err
}

// claudeEnd is what Claude Code's events said of how a run ended, beyond
// its Usage (#261).
type claudeEnd struct {
	result  bool   // a result closed the run, with what it cost and its turns
	isError bool   // the result says the run failed
	failure string // what the result said went wrong: its errors, or else its text
	api     string // the last API error Claude Code reported in place of a reply
	kind    string // that API error's kind, such as rate_limit or server_error
	status  int    // the API's HTTP status for the error, from the result or that message
	cause   string // Claude Code's typed cause for it, such as usage_limit_reached
	replies int    // the agent's replies, each a turn, as the events showed them
	paid    bool   // the provider sent a reply, which costs, before any result
}

// claudeTerminal are the typed causes Claude Code gives an API error that no
// wait lifts: limits on the account, and faults of the request, the model or
// the setup. Claude Code adds causes over time, and one not named here, as
// its own documentation says of an unknown one, says nothing.
var claudeTerminal = map[string]bool{
	"usage_limit_reached": true, "model_requires_usage_credits": true, "long_context_credits_required": true,
	"max_output_tokens": true, "dlp_request_denied": true, "claude_code_version_too_old": true, "safety_monitor_blocked": true,
	"effort_requires_thinking": true, "advisor_incompatible": true, "tool_history_mismatch": true, "autocompact_thrashing": true,
	"pdf_too_large": true, "pdf_password_protected": true, "media_removed": true, "tls_untrusted_ca": true,
	"gateway_content_type": true, "provider_credentials": true, "gateway_signin_required": true, "gateway_session_expired": true,
	"api_key_auth_disabled": true, "org_disabled_credential": true, "invalid_credential_header": true, "consent_unanswered": true,
	"no_allowed_fallback": true, "model_substitution_disabled": true, "field_not_granted": true, "empty_completion": true,
	"unattended_start_limit": true, "unattended_start_unconfirmed": true,
}

// unavailable says whether the attempt ended because Claude Code's provider
// was briefly unavailable. Claude Code's own fields say so where it gave
// them: its typed cause, the API's status, such as 429, 529 or another 5xx,
// and the error's kind, rate_limit or server_error. Even then, words that
// name a limit on the account win, since Claude Code reports a usage limit
// as a rate limit too. Only without those fields do failure's words decide.
func (e claudeEnd) unavailable(failure string) bool {
	known, outage := false, false
	switch {
	case e.cause == "no_response":
		known, outage = true, true
	case claudeTerminal[e.cause]:
		return false
	case e.status == 408 || e.status == 429 || e.status >= 500 && e.status <= 599:
		known, outage = true, true
	case e.status >= 400 && e.status <= 499:
		return false
	case e.kind == "rate_limit" || e.kind == "server_error":
		known, outage = true, true
	case e.kind == "authentication_failed" || e.kind == "billing_error" || e.kind == "invalid_request" ||
		e.kind == "model_not_found" || e.kind == "max_output_tokens" || e.kind == "verification_required":
		return false
	}
	if known {
		return outage && !lasting(strings.ToLower(failure+"\n"+e.api))
	}
	return transient(failure)
}

// apiStatus is an API error's HTTP status as an event gives it, or 0.
func apiStatus(v json.RawMessage) int {
	n, err := strconv.Atoi(strings.TrimSpace(string(v)))
	if err != nil {
		return 0
	}
	return n
}

// readClaudeStream is ReadStream, with what the events said of how the run
// ended.
func readClaudeStream(r io.Reader, transcript io.Writer) (Usage, claudeEnd, error) {
	u := Usage{ToolCalls: map[string]int{}}
	var end claudeEnd
	seen := map[string]bool{}
	// A reply is one message, whose blocks can come in several events. It
	// ran out of room if it held only thinking when the next turn began.
	var reply string
	var thinkingOnly bool
	cutOff := 0
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if transcript != nil {
			transcript.Write(append(append([]byte{}, line...), '\n'))
		}
		var ev struct {
			Type      string          `json:"type"`
			Subtype   string          `json:"subtype"`
			Model     string          `json:"model"`
			Result    string          `json:"result"`
			Cost      float64         `json:"total_cost_usd"`
			Turns     int             `json:"num_turns"`
			IsError   bool            `json:"is_error"`
			Errors    json.RawMessage `json:"errors"`
			Error     json.RawMessage `json:"error"`
			APIStatus json.RawMessage `json:"api_error_status"`
			APIError  json.RawMessage `json:"api_error"`
			Servers   []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"mcp_servers"`
			Message struct {
				ID        string          `json:"id"`
				Content   json.RawMessage `json:"content"`
				APIStatus json.RawMessage `json:"api_error_status"`
				APIError  json.RawMessage `json:"api_error"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		var content []struct {
			Type string `json:"type"`
			Name string `json:"name"`
			Text string `json:"text"`
		}
		if bytes.HasPrefix(bytes.TrimSpace(ev.Message.Content), []byte("[")) {
			json.Unmarshal(ev.Message.Content, &content)
		}
		switch ev.Type {
		case "system":
			if ev.Subtype == "init" {
				u.Model = ev.Model
				for _, s := range ev.Servers {
					if s.Name == "invariant" {
						u.GateTool = s.Status
					}
				}
			}
		case "assistant":
			// Claude Code reports an API error that took a reply's place as a
			// message with an error, which the provider didn't send.
			if kind := words(ev.Error); kind != "" {
				var text []string
				for _, c := range content {
					if c.Text != "" {
						text = append(text, c.Text)
					}
				}
				end.api, end.kind = strings.TrimSpace(kind+": "+strings.Join(text, "\n")), kind
				for _, v := range []json.RawMessage{ev.APIStatus, ev.Message.APIStatus} {
					if n := apiStatus(v); n != 0 {
						end.status = n
					}
				}
				for _, v := range []json.RawMessage{ev.APIError, ev.Message.APIError} {
					if w := words(v); w != "" {
						end.cause = w
					}
				}
				break
			}
			end.paid = true
			if ev.Message.ID == "" || !seen[ev.Message.ID] {
				seen[ev.Message.ID] = true
				end.replies++
			}
			if ev.Message.ID != reply {
				reply, thinkingOnly = ev.Message.ID, true
			}
			for _, c := range content {
				if c.Type == "tool_use" {
					u.ToolCalls[c.Name]++
				}
				if c.Type != "thinking" && c.Type != "redacted_thinking" {
					thinkingOnly = false
				}
			}
		case "user":
			if reply == "" {
				break
			}
			if thinkingOnly {
				cutOff++
			} else {
				cutOff = 0
			}
			reply = ""
			if cutOff >= maxThinkingOnly {
				return u, end, ErrThinkingLoop
			}
		case "result":
			u.Outcome, u.Summary, u.CostUSD, u.Turns = ev.Subtype, ev.Result, ev.Cost, ev.Turns
			end.result, end.isError, end.failure = true, ev.IsError, words(ev.Errors)
			if end.failure == "" {
				end.failure = strings.TrimSpace(ev.Result)
			}
			// The result's own account of the API error that ended the turn
			// is the last word on it.
			if n := apiStatus(ev.APIStatus); n != 0 {
				end.status = n
			}
			if w := words(ev.APIError); w != "" {
				end.cause = w
			}
		}
	}
	return u, end, scanner.Err()
}

// words is a JSON value's text, as an event's error or errors may hold it:
// a string, a list of them, or anything else, as it reads in JSON. Nothing,
// null and false say nothing.
func words(v json.RawMessage) string {
	v = bytes.TrimSpace(v)
	if len(v) == 0 || bytes.Equal(v, []byte("null")) || bytes.Equal(v, []byte("false")) {
		return ""
	}
	var s string
	if json.Unmarshal(v, &s) == nil {
		return strings.TrimSpace(s)
	}
	var list []json.RawMessage
	if json.Unmarshal(v, &list) == nil {
		var out []string
		for _, item := range list {
			if w := words(item); w != "" {
				out = append(out, w)
			}
		}
		return strings.Join(out, "\n")
	}
	return string(v)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
