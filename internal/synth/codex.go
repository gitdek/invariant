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
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Codex runs OpenAI's Codex CLI headlessly, with codex exec, its documented
// headless mode, on @gitdek's own Codex account (D-0028, D-0128). It takes
// every job Claude Code takes, under the same rules and limits (#153).
//
// Its home, CODEX_HOME, is one of its own, where its sign-in lives, and a
// run ignores any configuration there, so nothing of the user's own Codex
// settings, servers or history applies. Its only tools are its own and
// Invariant's MCP server, with only the job's tools on it. Codex's apps and
// plugins, which reach the services connected to the account, are off, and
// so is its web search.
// Its sandbox writes only in the workspace, with no network (D-0014), and
// a reviewer's reads only, with no server (D-0087). Its commands get only
// the environment's core variables, not its keys or tokens.
//
// Its agent has a shell, so its commands run under a permission profile of
// Codex's own (D-0138): they read only the workspace, the system's own
// files and the toolchains it names, write only in the workspace and the
// system's temporary directories, and reach no network. Everything else is
// denied, the home directory included, and Codex's home with its sign-in
// is kept there. Codex's own connection to its service, and Invariant's
// server, aren't sandboxed, so the agent still thinks and checks its work.
// Codex runs go one at a time, since runs that share a sign-in can break
// each other's token refresh.
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
	// Reads are what its commands may read beyond the workspace and the
	// system's own files: the toolchains they build with (D-0138).
	Reads []string
	// GoModCache is Go's module cache, which its commands read to build Go
	// without the network.
	GoModCache string
}

// codexTurn lets one Codex run go at a time (D-0138).
var codexTurn = make(chan struct{}, 1)

// profile is the permission profile Codex's commands run under (D-0138):
// Codex's workspace profile, or its read-only one for a reviewer, with
// everything denied but the system's own files, the workspace and what the
// backend's Reads name. The user's temporary directory, where every
// agent's workspace is, is denied too, so a run reads no other's. Codex's
// minimal files include the shared temporary directories, /tmp among them,
// which its commands may also write.
func (c Codex) profile(readOnly bool) []string {
	base, fs := ":workspace", []string{`":root"="deny"`, `":minimal"="read"`, `":tmpdir"="deny"`}
	if readOnly {
		// The read-only profile reads everything, so once everything is
		// denied, the workspace is named again.
		base, fs = ":read-only", append(fs, `":workspace_roots"="read"`)
	}
	for _, p := range c.reads() {
		fs = append(fs, tomlString(p)+`="read"`)
	}
	return []string{
		"-c", `default_permissions="invariant"`,
		"-c", `permissions.invariant.extends="` + base + `"`,
		"-c", "permissions.invariant.filesystem={" + strings.Join(fs, ", ") + "}",
	}
}

func (c Codex) reads() []string {
	reads := append([]string{}, c.Reads...)
	if c.GoModCache != "" {
		reads = append(reads, c.GoModCache)
	}
	return append(reads, c.codexDirs()...)
}

// codexDirs are where Codex's binary is, as it's run and as it really is.
// Codex runs itself under the profile to read a workspace's instructions,
// so its commands may read it too. A copy run that way can't read Codex's
// sign-in or reach the network any more than they can.
func (c Codex) codexDirs() []string {
	p, err := exec.LookPath(c.Binary)
	if err != nil {
		return nil
	}
	dirs := []string{filepath.Dir(p)}
	if real, err := filepath.EvalSymlinks(p); err == nil && filepath.Dir(real) != dirs[0] {
		dirs = append(dirs, filepath.Dir(real))
	}
	return dirs
}

// commandEnv is what the agent's commands get beyond the environment's core
// variables: a temporary directory of the run's own, tmp, which they may
// write, Go set to build offline, from the module cache, into a build cache
// there, and git kept from the home directory it can't read.
func (c Codex) commandEnv(tmp string) string {
	set := []string{"TMPDIR=" + tomlString(tmp), "GOCACHE=" + tomlString(filepath.Join(tmp, "go-build")), `GOPROXY="off"`, `GOFLAGS="-mod=mod"`, `GOTOOLCHAIN="local"`, `GIT_CONFIG_GLOBAL="/dev/null"`}
	if c.GoModCache != "" {
		set = append(set, "GOMODCACHE="+tomlString(c.GoModCache))
	}
	return "shell_environment_policy.set={" + strings.Join(set, ", ") + "}"
}

func (c Codex) Name() string { return "codex" }

// codexServer is Invariant's MCP server's name in Codex's configuration.
const codexServer = "invariant"

func (c Codex) args(job Job, tmp string) ([]string, error) {
	args := []string{"exec", "--json",
		// Nothing in the Codex home's own configuration applies, and the
		// run keeps no session to resume.
		"--ignore-user-config", "--ephemeral",
		"--skip-git-repo-check",
		"--disable", "apps", "--disable", "plugins",
		// Codex's own web search runs on its service, outside the sandbox,
		// and Claude Code's agents get no web either (D-0037).
		"-c", `web_search="disabled"`,
		"-C", job.Workspace,
		"-c", `shell_environment_policy.inherit="core"`,
		"-c", c.commandEnv(tmp),
	}
	args = append(args, c.profile(job.ReadOnly)...)
	if !job.ReadOnly {
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
		args = append(args, "-c", server)
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
	select {
	case codexTurn <- struct{}{}:
		defer func() { <-codexTurn }()
	case <-ctx.Done():
		return Usage{}, ctx.Err()
	}
	// The agent's commands get a temporary directory of the run's own, which
	// holds their Go build cache, where the profile lets them write. In the
	// workspace, it would count as the agent's work.
	tmp, err := os.MkdirTemp("/tmp", "invariant-codex-")
	if err != nil {
		return Usage{}, err
	}
	defer os.RemoveAll(tmp)
	args, err := c.args(job, tmp)
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
	// Codex's events carry no time and no workspace, which the live view
	// needs, so the transcript starts with the workspace, and each event is
	// stamped with when it came (#153).
	if job.Transcript != nil {
		start, _ := json.Marshal(map[string]string{"type": "invariant.workspace", "cwd": job.Workspace})
		job.Transcript.Write(append(stamped(start, time.Now()), '\n'))
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

// Probe checks, before the factory runs Codex, that its sandbox holds as
// D-0138 needs: a command under a build's profile writes in its workspace,
// and can't read a file in Codex's home, where its sign-in lives, or one in
// the temporary directory where other runs' workspaces are. It runs no
// agent and spends nothing.
func (c Codex) Probe(ctx context.Context) error {
	ws, err := os.MkdirTemp("", "invariant-codex-probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(ws)
	if err := os.MkdirAll(c.Home, 0o700); err != nil {
		return err
	}
	probe := filepath.Join(c.Home, "sandbox-probe")
	if err := os.WriteFile(probe, []byte("invariant\n"), 0o600); err != nil {
		return err
	}
	defer os.Remove(probe)
	sandbox := func(command ...string) error { return c.sandboxed(ctx, ws, command...) }
	if err := sandbox("/usr/bin/touch", filepath.Join(ws, "written")); err != nil {
		return fmt.Errorf("codex's sandbox didn't run a command under the factory's profile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "written")); err != nil {
		return errors.New("a command in codex's sandbox couldn't write in its workspace")
	}
	// Codex runs itself under the profile to read a workspace's
	// instructions, so it must run there.
	if bin, err := exec.LookPath(c.Binary); err == nil {
		if err := sandbox(bin, "--version"); err != nil {
			return fmt.Errorf("codex can't run itself under the factory's profile, as it does to read a workspace's instructions: %v", err)
		}
	}
	if sandbox("/bin/cat", probe) == nil {
		return fmt.Errorf("a command in codex's sandbox read a file in its home, %s, where its sign-in lives. Keep Codex's home in your home directory, outside the temporary directories its commands may read", c.Home)
	}
	other, err := os.MkdirTemp("", "invariant-codex-probe-other-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(other)
	if err := os.WriteFile(filepath.Join(other, "probe"), []byte("invariant\n"), 0o600); err != nil {
		return err
	}
	if sandbox("/bin/cat", filepath.Join(other, "probe")) == nil {
		return errors.New("a command in codex's sandbox read a file in the temporary directory, where other runs' workspaces are")
	}
	return nil
}

// sandboxed runs a command as a build's agent would, under the factory's
// profile with ws as its workspace, through codex sandbox, which runs no
// agent.
func (c Codex) sandboxed(ctx context.Context, ws string, command ...string) error {
	args := append([]string{"sandbox", "-P", "invariant", "-C", ws}, c.profile(false)...)
	cmd := exec.CommandContext(ctx, c.Binary, append(append(args, "--"), command...)...)
	cmd.Env = c.codexEnv()
	return cmd.Run()
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
			transcript.Write(append(stamped(line, time.Now()), '\n'))
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

// stamped is a JSON event with when it came, as its first field. Anything
// else is left as it is.
func stamped(line []byte, at time.Time) []byte {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) < 2 || trimmed[0] != '{' {
		return append([]byte{}, line...)
	}
	out := []byte(`{"timestamp":"` + at.UTC().Format(time.RFC3339Nano) + `"`)
	if rest := bytes.TrimSpace(trimmed[1:]); len(rest) > 0 && rest[0] != '}' {
		out = append(out, ',')
	}
	return append(out, trimmed[1:]...)
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
