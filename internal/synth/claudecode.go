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
)

// ClaudeCode runs the official Claude Code CLI headlessly (claude -p), on the
// user's own account and machine (D-0027). The agent can read and edit files
// in its workspace and call the gate. It has no shell, no web and no
// subagents, so any code it writes runs only inside the gate's sandbox.
type ClaudeCode struct {
	Binary    string  // the claude CLI
	Model     string  // "opus", "sonnet" or a full model name
	BudgetUSD float64 // --max-budget-usd: a cap on the run's estimated cost
	MaxTurns  int
}

func (c ClaudeCode) Name() string { return "claude-code" }

func (c ClaudeCode) Run(ctx context.Context, job Job) (Usage, error) {
	config, err := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"invariant": map[string]any{"command": job.GateServer[0], "args": job.GateServer[1:]},
	}})
	if err != nil {
		return Usage{}, err
	}
	allowed := "Read,Write,Edit,Glob,Grep"
	tools := job.Tools
	if len(tools) == 0 {
		tools = []string{"gate"}
	}
	for _, t := range tools {
		allowed += ",mcp__invariant__" + t
	}
	args := []string{
		"-p", job.Prompt,
		"--output-format", "stream-json", "--verbose",
		"--model", c.Model,
		"--max-budget-usd", strconv.FormatFloat(c.BudgetUSD, 'f', 2, 64),
		"--max-turns", strconv.Itoa(c.MaxTurns),
		"--mcp-config", string(config), "--strict-mcp-config",
		"--allowedTools", allowed,
		"--disallowedTools", "Bash,WebFetch,WebSearch,Task,NotebookEdit",
		"--permission-mode", "acceptEdits",
		"--setting-sources", "project",
		"--no-session-persistence",
	}
	cmd := exec.CommandContext(ctx, c.Binary, args...)
	cmd.Dir = job.Workspace
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
	usage, readErr := ReadStream(stdout, job.Transcript)
	waitErr := cmd.Wait()
	switch {
	case readErr != nil:
		return usage, readErr
	case usage.Outcome == "":
		return usage, fmt.Errorf("claude ended without a result: %v\n%s", waitErr, lastLines(stderr.String(), 20))
	case usage.Outcome != "success":
		return usage, fmt.Errorf("claude stopped: %s", usage.Outcome)
	}
	return usage, nil
}

// ReadStream reads Claude Code's stream-json events, copying each to
// transcript, and returns what the closing result event reports.
func ReadStream(r io.Reader, transcript io.Writer) (Usage, error) {
	u := Usage{ToolCalls: map[string]int{}}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if transcript != nil {
			transcript.Write(append(append([]byte{}, line...), '\n'))
		}
		var ev struct {
			Type    string  `json:"type"`
			Subtype string  `json:"subtype"`
			Model   string  `json:"model"`
			Result  string  `json:"result"`
			Cost    float64 `json:"total_cost_usd"`
			Turns   int     `json:"num_turns"`
			Servers []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"mcp_servers"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Name string `json:"name"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &ev) != nil {
			continue
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
			for _, c := range ev.Message.Content {
				if c.Type == "tool_use" {
					u.ToolCalls[c.Name]++
				}
			}
		case "result":
			u.Outcome, u.Summary, u.CostUSD, u.Turns = ev.Subtype, ev.Result, ev.Cost, ev.Turns
		}
	}
	return u, scanner.Err()
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
