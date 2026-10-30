package synth

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
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
	Effort       string
	providerWait providerWait
}

func (c ClaudeCode) Name() string { return "claude-code" }

// WithProviderWait configures this backend's retry waits. Nil restores the
// cancellable timer; copying the value leaves other backend instances alone.
func (c ClaudeCode) WithProviderWait(wait func(context.Context, time.Duration) error) Backend {
	c.providerWait = wait
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
			"--max-budget-usd", strconv.FormatFloat(c.BudgetUSD, 'f', 2, 64),
			"--max-turns", strconv.Itoa(c.MaxTurns),
			"--strict-mcp-config",
			"--tools", readTools,
			"--allowedTools", readTools,
			"--disallowedTools", "Bash,Write,Edit,WebFetch,WebSearch,Task,NotebookEdit,Read(~/**),Glob(~/**),Grep(~/**)",
			"--setting-sources", "project",
			"--no-session-persistence",
		}, c.effort(job)...), nil
	}
	config, err := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"invariant": map[string]any{"command": job.GateServer[0], "args": job.GateServer[1:]},
	}})
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
		"--max-budget-usd", strconv.FormatFloat(c.BudgetUSD, 'f', 2, 64),
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

func (c ClaudeCode) Run(ctx context.Context, job Job) (Usage, error) {
	return retryProvider(ctx, job, c.providerWait, nil, func(ctx context.Context) (Usage, error) {
		return c.runAttempt(ctx, job)
	})
}

func (c ClaudeCode) runAttempt(ctx context.Context, job Job) (Usage, error) {
	args, err := c.args(job)
	if err != nil {
		return Usage{}, err
	}
	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(attemptCtx, c.Binary, args...)
	cmd.Dir = job.Workspace
	// A deadline stops the CLI and its servers, including any process still
	// holding its output pipe open, before a retry can take over.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	// The gate takes a while; give its tool calls time.
	cmd.Env = append(os.Environ(), "MCP_TOOL_TIMEOUT=900000", "MCP_TIMEOUT=60000")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Usage{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return Usage{}, err
	}
	usage, terminalError, readErr := readStream(stdout, job.Transcript)
	if readErr != nil {
		cancel() // stop the agent, rather than wait for it
	}
	waitErr := cmd.Wait()
	switch {
	case readErr != nil:
		return usage, readErr
	case usage.Outcome == "success":
		return usage, nil
	}
	// An explicit cap or other non-execution stop is an ordinary failure,
	// even if a provider warning appeared earlier and the CLI recovered.
	if usage.Outcome != "" && usage.Outcome != "error_during_execution" {
		return usage, fmt.Errorf("claude stopped: %s: %s", usage.Outcome, usage.Summary)
	}
	message := terminalError
	if message == "" {
		message = lastLines(stderr.String(), 20)
	}
	if temporaryProviderMessage(message) {
		return usage, &providerError{message: "claude: " + message}
	}
	if err := ctx.Err(); err != nil {
		return usage, fmt.Errorf("claude stopped: %w", err)
	}
	switch {
	case usage.Outcome == "":
		return usage, fmt.Errorf("claude ended without a result: %v\n%s", waitErr, message)
	default:
		return usage, fmt.Errorf("claude stopped: %s: %s", usage.Outcome, message)
	}
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
	u, _, err := readStream(r, transcript)
	return u, err
}

func readStream(r io.Reader, transcript io.Writer) (Usage, string, error) {
	u := Usage{ToolCalls: map[string]int{}}
	var assistantError, terminalError string
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
			Error   json.RawMessage `json:"error"`
			Errors  []string        `json:"errors"`
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
			if message := providerErrorText(ev.Error); message != "" {
				assistantError = message
				for _, c := range content {
					if c.Type == "text" && c.Text != "" {
						assistantError += ": " + c.Text
					}
				}
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
				return u, "", ErrThinkingLoop
			}
		case "result":
			u.Outcome, u.Summary, u.CostUSD, u.Turns = ev.Subtype, ev.Result, ev.Cost, ev.Turns
			if ev.IsError || ev.Subtype != "success" {
				if ev.IsError && u.Outcome == "success" {
					u.Outcome = "error_during_execution"
				}
				terminalError = strings.Join(ev.Errors, "\n")
				if message := providerErrorText(ev.Error); message != "" {
					if terminalError != "" {
						terminalError += "\n"
					}
					terminalError += message
				}
				if ev.Result != "" {
					if terminalError != "" {
						terminalError += "\n"
					}
					terminalError += ev.Result
				}
				if terminalError == "" && u.Outcome == "error_during_execution" {
					terminalError = assistantError
				}
				if u.Summary == "" {
					u.Summary = terminalError
				}
			}
		case "error":
			assistantError = providerErrorText(ev.Error)
		}
	}
	if u.Outcome == "" {
		terminalError = assistantError
	}
	return u, terminalError, scanner.Err()
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
