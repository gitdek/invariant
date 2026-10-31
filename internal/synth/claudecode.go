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
		return try{err: err}
	}
	usage, end, readErr := readClaudeStream(stdout, job.Transcript)
	if readErr != nil {
		stop() // stop the agent, rather than wait for it
	}
	waitErr := cmd.Wait()
	// The result's own words, when there's a result, alone say why it
	// failed. Without one, an API error Claude Code reported in place of a
	// reply does, and only without that, stderr. stderr is diagnostic only.
	failure := end.failure
	if failure == "" {
		failure = end.api
	}
	if failure == "" && !end.result {
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
	case usage.Outcome == "success" && !(end.isError && transient(failure)):
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
	if cut := ctx.Err(); cut != nil {
		// The run's deadline or its cancellation ended this attempt.
		t.err, t.cut = fmt.Errorf("the run was cut off (%w): %w", cut, err), true
	} else if readErr == nil {
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
	replies int    // the agent's replies, each a turn, as the events showed them
	paid    bool   // the provider sent a reply, which costs, before any result
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
			Type    string          `json:"type"`
			Subtype string          `json:"subtype"`
			Model   string          `json:"model"`
			Result  string          `json:"result"`
			Cost    float64         `json:"total_cost_usd"`
			Turns   int             `json:"num_turns"`
			IsError bool            `json:"is_error"`
			Errors  json.RawMessage `json:"errors"`
			Error   json.RawMessage `json:"error"`
			Servers []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"mcp_servers"`
			Message struct {
				ID      string          `json:"id"`
				Content json.RawMessage `json:"content"`
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
				end.api = strings.TrimSpace(kind + ": " + strings.Join(text, "\n"))
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
