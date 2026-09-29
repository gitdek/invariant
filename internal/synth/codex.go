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
	"syscall"
)

// Codex runs OpenAI's Codex CLI headlessly, with codex exec, its documented
// headless mode, on @gitdek's own Codex account (D-0028, D-0128). It takes
// every job Claude Code takes, under the same rules and limits (#153).
//
// Its home, CODEX_HOME, is one of its own, where its sign-in lives, and a
// run ignores any configuration there, so nothing of the user's own Codex
// settings, servers or history applies. Its only tools are its own and
// Invariant's MCP server, with only the job's tools on it. Codex's apps and
// plugins, which reach the services connected to the account, are off.
// Its sandbox writes only in the workspace, with no network (D-0014), and
// a reviewer's reads only, with no server (D-0087). Its commands get only
// the environment's core variables, not its keys or tokens.
//
// Codex's sandbox doesn't stop reads outside the workspace, and its agent
// has a shell, so the factory doesn't run it until it has a place where it
// can see nothing but its workspace (D-0037).
type Codex struct {
	Binary string // the codex CLI
	Home   string // CODEX_HOME: its sign-in, and nothing of the user's own configuration
	Model  string // -m; empty leaves Codex's own
	// Effort is Codex's reasoning effort, in the words D-0114 uses for
	// Claude Code's, from low to max, its highest. A job that names its
	// own effort, as a build's fallback run does, thinks at that instead
	// (D-0125).
	Effort string
	// MaxTurns caps the agent's steps, since codex exec keeps no cap of its
	// own: each message, command, file change or tool call is one.
	MaxTurns int
	// ToolTimeout is how long a call to Invariant's tool may take, in
	// seconds. A gate run takes minutes.
	ToolTimeout int
}

func (c Codex) Name() string { return "codex" }

// codexServer is Invariant's MCP server's name in Codex's configuration.
const codexServer = "invariant"

func (c Codex) args(job Job) ([]string, error) {
	args := []string{"exec", "--json",
		// Nothing in the Codex home's own configuration applies, and the
		// run keeps no session to resume.
		"--ignore-user-config", "--ephemeral",
		"--skip-git-repo-check",
		"--disable", "apps", "--disable", "plugins",
		"-C", job.Workspace,
		"-c", `shell_environment_policy.inherit="core"`,
	}
	if job.ReadOnly {
		args = append(args, "-s", "read-only")
	} else {
		if len(job.GateServer) == 0 {
			return nil, errors.New("codex needs Invariant's MCP server for this job")
		}
		tools := job.Tools
		if len(tools) == 0 {
			tools = []string{"gate"}
		}
		timeout := c.ToolTimeout
		if timeout <= 0 {
			timeout = 900
		}
		// Codex asks before an MCP call unless the server's tools are
		// approved, and codex exec never asks, so it would refuse every
		// call.
		server := fmt.Sprintf(`mcp_servers.%s={command=%s, args=%s, required=true, default_tools_approval_mode="approve", enabled_tools=%s, startup_timeout_sec=60, tool_timeout_sec=%d}`,
			codexServer, tomlString(job.GateServer[0]), tomlStrings(job.GateServer[1:]), tomlStrings(tools), timeout)
		args = append(args, "-s", "workspace-write", "-c", server)
	}
	if c.Model != "" {
		args = append(args, "-m", c.Model)
	}
	effort := job.Effort
	if effort == "" {
		effort = c.Effort
	}
	if effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+tomlString(effort))
	}
	// The prompt comes last, and exec reads nothing more from its input.
	return append(args, "--", job.Prompt), nil
}

// tomlString is s as a TOML basic string, which JSON's string is.
func tomlString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// tomlStrings is a TOML array of basic strings, which JSON's array of
// strings is.
func tomlStrings(ss []string) string {
	if ss == nil {
		ss = []string{}
	}
	b, _ := json.Marshal(ss)
	return string(b)
}

// codexEnv is the environment codex runs in: the home where its sign-in
// lives, and what it needs to find its tools, and no keys or tokens.
func (c Codex) codexEnv() []string {
	env := []string{"CODEX_HOME=" + c.Home}
	for _, k := range []string{"PATH", "HOME", "USER", "LOGNAME", "TMPDIR", "LANG", "LC_ALL", "TERM"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func (c Codex) Run(ctx context.Context, job Job) (Usage, error) {
	if c.Home == "" {
		return Usage{}, errors.New("codex needs a home of its own, where its sign-in lives")
	}
	args, err := c.args(job)
	if err != nil {
		return Usage{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Binary, args...)
	cmd.Dir = job.Workspace
	cmd.Env = c.codexEnv()
	// Codex's agent has a shell, so stopping the run stops everything it
	// started, not codex alone.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	// Given an input, exec reads it as more of the prompt.
	cmd.Stdin = nil
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Usage{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return Usage{}, err
	}
	usage, readErr := ReadCodexStream(stdout, job.Transcript, c.MaxTurns, !job.ReadOnly)
	if readErr != nil {
		cancel() // stop the agent, rather than wait for it
	}
	waitErr := cmd.Wait()
	usage.Backend, usage.Model = c.Name(), c.Model
	// A required server that doesn't start stops codex before the turn.
	if !job.ReadOnly && usage.GateTool == "" && strings.Contains(stderr.String(), "MCP servers failed") {
		usage.GateTool = "failed"
	}
	switch {
	case readErr != nil:
		return usage, readErr
	case usage.Outcome == "":
		return usage, fmt.Errorf("codex ended without finishing its turn: %v\n%s", waitErr, lastLines(stderr.String(), 20))
	case usage.Outcome != "success":
		return usage, fmt.Errorf("codex stopped: %s", usage.Summary)
	}
	return usage, nil
}

// ErrTooManyTurns is a Codex run the factory stopped at its cap on steps,
// which codex exec doesn't keep itself.
var ErrTooManyTurns = errors.New("the agent took more steps than its cap allows, so the factory stopped it")

// ReadCodexStream reads codex exec's JSON events, copying each to
// transcript, into the same Usage Claude Code's run gives: its steps, its
// tool calls by kind, whether Invariant's server connected, how the run
// ended, its last message and its tokens. It stops early, with
// ErrTooManyTurns, once the agent takes more than maxTurns steps. gated
// says the run has Invariant's server, which Codex must start before the
// agent's turn does.
func ReadCodexStream(r io.Reader, transcript io.Writer, maxTurns int, gated bool) (Usage, error) {
	u := Usage{ToolCalls: map[string]int{}}
	var lastError string
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if transcript != nil {
			transcript.Write(append(append([]byte{}, line...), '\n'))
		}
		var ev struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Error   struct {
				Message string `json:"message"`
			} `json:"error"`
			Usage *Tokens `json:"usage"`
			Item  struct {
				Type   string `json:"type"`
				Text   string `json:"text"`
				Server string `json:"server"`
				Tool   string `json:"tool"`
				Status string `json:"status"`
			} `json:"item"`
		}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		switch ev.Type {
		case "turn.started":
			// A required server that doesn't start stops the run before
			// the turn does.
			if gated {
				u.GateTool = "connected"
			}
		case "item.completed":
			switch it := ev.Item; it.Type {
			case "agent_message":
				u.Summary = it.Text
			case "reasoning", "todo_list", "error":
				continue // not a step
			case "mcp_tool_call":
				u.ToolCalls["mcp__"+it.Server+"__"+it.Tool]++
			case "command_execution":
				u.ToolCalls["shell"]++
			case "file_change":
				u.ToolCalls["edit"]++
			default:
				u.ToolCalls[it.Type]++
			}
			u.Turns++
			if maxTurns > 0 && u.Turns > maxTurns {
				u.Outcome = "error_max_turns"
				return u, ErrTooManyTurns
			}
		case "turn.completed":
			u.Outcome, u.Tokens = "success", ev.Usage
		case "turn.failed":
			u.Outcome, u.Summary = "failed", ev.Error.Message
		case "error":
			lastError = ev.Message
		}
	}
	if u.Outcome == "failed" && u.Summary == "" {
		u.Summary = lastError
	}
	if u.Outcome == "" && lastError != "" {
		u.Summary = lastError
	}
	return u, scanner.Err()
}

// Tokens is what an agent's run used, where its agent counts tokens rather
// than cost, as Codex does on an account. The cached input is part of the
// input, and the reasoning part of the output.
type Tokens struct {
	Input       int `json:"input_tokens"`
	CachedInput int `json:"cached_input_tokens"`
	Output      int `json:"output_tokens"`
	Reasoning   int `json:"reasoning_output_tokens"`
}

// String says the tokens in a few words.
func (t Tokens) String() string {
	return strconv.Itoa(t.Input) + " tokens in, " + strconv.Itoa(t.Output) + " out"
}
