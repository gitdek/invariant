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
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These tests run Codex's backend against a stand-in for codex, since the
// plumbing sandbox has neither Codex nor a network (#153). The stand-in
// records its arguments and environment, and replays the events it's given.

const standIn = `#!/bin/sh
here=$(cd "$(dirname "$0")" && pwd)
printf '%s\n' "$@" > "$here/args"
env > "$here/env"
if [ -f "$here/linger" ]; then sleep 30 & echo $! > "$here/child"; fi
cat "$here/events.jsonl"
[ -f "$here/stderr" ] && cat "$here/stderr" >&2
[ -f "$here/linger" ] && wait
exit "$(cat "$here/exit" 2>/dev/null || echo 0)"
`

// codexRig is a stand-in for codex that replays events, with a home of its
// own and a workspace.
func codexRig(t *testing.T, events string) (Codex, string, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	for name, text := range map[string]string{"codex": standIn, "events.jsonl": events} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	home, ws := filepath.Join(t.TempDir(), "home"), t.TempDir()
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	return Codex{Binary: bin, Home: home, MaxTurns: 20}, dir, ws
}

func standInFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const codexRun = `{"type":"thread.started","thread_id":"t1"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"reasoning","text":"Reading the model."}}
{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"ls","aggregated_output":"model.tla\n","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"id":"item_2","type":"file_change","changes":[{"path":"src/queue.go","kind":"add"}],"status":"completed"}}
{"type":"item.started","item":{"id":"item_3","type":"mcp_tool_call","server":"invariant","tool":"gate","arguments":{},"status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_3","type":"mcp_tool_call","server":"invariant","tool":"gate","arguments":{},"result":{"content":[]},"status":"completed"}}
{"type":"item.completed","item":{"id":"item_4","type":"mcp_tool_call","server":"invariant","tool":"gate","arguments":{},"result":{"content":[]},"status":"completed"}}
{"type":"error","message":"Reconnecting... 1/5"}
{"type":"item.completed","item":{"id":"item_5","type":"agent_message","text":"The gate passes."}}
{"type":"turn.completed","usage":{"input_tokens":1200,"cached_input_tokens":800,"cache_write_input_tokens":0,"output_tokens":300,"reasoning_output_tokens":120}}
`

// A build's job runs codex exec headless in its workspace, with Invariant's
// server as its only MCP server and only the job's tools on it, approved,
// since exec never asks. Nothing of the user's own Codex configuration
// applies, the account's apps and plugins are off, the sandbox writes only
// in the workspace, and neither codex nor its commands get the host's keys
// or tokens. The run reads into the same Usage Claude Code's gives, with
// its tokens, and every event goes to the transcript.
func TestCodexRunsABuildsJobHeadless(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-stand-in")
	t.Setenv("GH_TOKEN", "ghp_stand-in")
	c, dir, ws := codexRig(t, codexRun)
	c.Model, c.Effort = "gpt-stand-in", "high"
	c.Reads, c.GoModCache = []string{"/opt/toolchain"}, "/cache/mod"
	var transcript bytes.Buffer
	u, err := c.Run(context.Background(), Job{Workspace: ws, Prompt: "-build the code", GateServer: []string{"/bin/invariant", "mcp", "-gate"},
		Tools: []string{"gate", "check"}, Transcript: &transcript, Effort: "max"})
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(standInFile(t, dir, "args")), "\n")
	for _, want := range [][]string{
		{"exec", "--json"}, {"--ignore-user-config"}, {"--ephemeral"}, {"--skip-git-repo-check"},
		{"--disable", "apps"}, {"--disable", "plugins"}, {"-c", `web_search="disabled"`}, {"-C", ws}, {"-m", "gpt-stand-in"},
		{"-c", `shell_environment_policy.inherit="core"`},
		// Its commands run under Codex's own permission profile: the
		// workspace, the system's own files and the toolchains, and nothing
		// else (D-0138).
		{"-c", `default_permissions="invariant"`},
		{"-c", `permissions.invariant.extends=":workspace"`},

		// The job's effort wins over the backend's own (D-0125).
		{"-c", `model_reasoning_effort="max"`},
		{"-c", `mcp_servers.invariant={command="/bin/invariant", args=["mcp","-gate"], required=true, default_tools_approval_mode="approve", enabled_tools=["gate","check"], startup_timeout_sec=60, tool_timeout_sec=900}`},
		// The prompt comes last, after the options end, so a prompt that
		// starts with a dash is still the prompt.
		{"--", "-build the code"},
	} {
		if !containsRun(args, want) {
			t.Errorf("codex's arguments %q lack %q", args, want)
		}
	}
	if args[len(args)-1] != "-build the code" {
		t.Errorf("the prompt isn't last: %q", args)
	}
	if slices.Contains(args, "-s") || slices.Contains(args, "--sandbox") {
		t.Errorf("a sandbox mode turns Codex's permission profile off: %q", args)
	}
	// Its commands may also read Codex's own binary, which Codex runs under
	// the profile to read the workspace's instructions.
	fs := `permissions.invariant.filesystem={":root"="deny", ":minimal"="read", ":tmpdir"="deny", "/opt/toolchain"="read", "/cache/mod"="read", "` + filepath.Dir(c.Binary) + `"="read"`
	if !slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, fs) }) {
		t.Errorf("codex's arguments %q lack the profile's files, %s…}", args, fs)
	}
	// Its commands get a temporary directory of the run's own, outside the
	// workspace, and build Go offline into a cache there, which is gone once
	// the run is.
	var commands string
	for _, a := range args {
		if strings.HasPrefix(a, "shell_environment_policy.set=") {
			commands = a
		}
	}
	for _, want := range []string{`GOPROXY="off"`, `GOFLAGS="-mod=mod"`, `GOTOOLCHAIN="local"`, `GIT_CONFIG_GLOBAL="/dev/null"`, `GOMODCACHE="/cache/mod"`, `TMPDIR="/tmp/invariant-codex-`, `GOCACHE="/tmp/invariant-codex-`} {
		if !strings.Contains(commands, want) {
			t.Errorf("the commands' environment %s lacks %s", commands, want)
		}
	}
	if i := strings.Index(commands, `GOCACHE="`); i >= 0 {
		cache := strings.SplitN(commands[i+len(`GOCACHE="`):], `"`, 2)[0]
		if _, err := os.Stat(cache); !os.IsNotExist(err) {
			t.Errorf("the run's build cache %s outlived it", cache)
		}
	}
	env := standInFile(t, dir, "env")
	if !strings.Contains(env, "CODEX_HOME="+c.Home+"\n") {
		t.Errorf("codex ran without its own home:\n%s", env)
	}
	for _, secret := range []string{"sk-stand-in", "ghp_stand-in"} {
		if strings.Contains(env, secret) {
			t.Errorf("codex got the host's %s", secret)
		}
	}
	want := map[string]int{"shell": 1, "edit": 1, "mcp__invariant__gate": 2}
	if u.Backend != "codex" || u.Model != "gpt-stand-in" || u.Outcome != "success" || u.Summary != "The gate passes." || u.Turns != 5 || u.GateTool != "connected" {
		t.Errorf("usage = %+v", u)
	}
	for k, n := range want {
		if u.ToolCalls[k] != n {
			t.Errorf("tool calls = %v; want %v", u.ToolCalls, want)
		}
	}
	if u.Tokens == nil || *u.Tokens != (Tokens{Input: 1200, CachedInput: 800, Output: 300, Reasoning: 120}) || u.CostUSD != 0 {
		t.Errorf("tokens = %+v, cost %v", u.Tokens, u.CostUSD)
	}
	// The transcript is every event, each stamped with when it came, after
	// a first line naming the workspace, for the live view.
	got := strings.Split(strings.TrimSpace(transcript.String()), "\n")
	events := strings.Split(strings.TrimSpace(codexRun), "\n")
	if len(got) != len(events)+1 {
		t.Fatalf("the transcript has %d lines; want the workspace and %d events", len(got), len(events))
	}
	var first map[string]any
	if json.Unmarshal([]byte(got[0]), &first) != nil || first["type"] != "invariant.workspace" || first["cwd"] != ws {
		t.Errorf("the transcript starts %s; want the workspace", got[0])
	}
	for i, line := range got {
		var stampedEv, event map[string]any
		if err := json.Unmarshal([]byte(line), &stampedEv); err != nil {
			t.Fatalf("line %d isn't an event: %s", i, line)
		}
		if _, err := time.Parse(time.RFC3339Nano, fmt.Sprint(stampedEv["timestamp"])); err != nil {
			t.Errorf("line %d has no time: %s", i, line)
		}
		if i == 0 {
			continue
		}
		delete(stampedEv, "timestamp")
		json.Unmarshal([]byte(events[i-1]), &event)
		if !reflect.DeepEqual(stampedEv, event) {
			t.Errorf("line %d is %s; want the event %s", i, line, events[i-1])
		}
	}
}

// Stamping adds a time to an event, and leaves anything else as it is.
func TestStampingAddsATimeToAnEvent(t *testing.T) {
	at := time.Date(2026, 9, 29, 12, 0, 0, 5, time.UTC)
	for in, want := range map[string]string{
		`{"type":"turn.started"}`: `{"timestamp":"2026-09-29T12:00:00.000000005Z","type":"turn.started"}`,
		`{}`:                      `{"timestamp":"2026-09-29T12:00:00.000000005Z"}`,
		`not an event`:            `not an event`,
	} {
		if got := string(stamped([]byte(in), at)); got != want {
			t.Errorf("stamped(%s) = %s; want %s", in, got, want)
		}
	}
}

// A reviewer's job reads only: Codex's read-only sandbox, and no server
// (D-0087).
func TestCodexReviewsReadOnly(t *testing.T) {
	c, dir, ws := codexRig(t, codexRun)
	if _, err := c.Run(context.Background(), Job{Workspace: ws, Prompt: "review", ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(standInFile(t, dir, "args")), "\n")
	if !containsRun(args, []string{"-c", `permissions.invariant.extends=":read-only"`}) || !strings.Contains(strings.Join(args, " "), `":workspace_roots"="read"`) ||
		strings.Contains(strings.Join(args, " "), ":workspace\"") || strings.Contains(strings.Join(args, " "), "mcp_servers") {
		t.Errorf("a reviewer's arguments: %q", args)
	}
}

// Codex runs go one at a time, since runs that share a sign-in can break
// each other's token refresh (D-0138).
func TestCodexRunsOneAtATime(t *testing.T) {
	c, dir, ws := codexRig(t, codexRun)
	script := strings.Replace(standIn, `cat "$here/events.jsonl"`, `echo "start $(date +%s%N)" >> "$here/turns"; sleep 0.4; echo "end $(date +%s%N)" >> "$here/turns"; cat "$here/events.jsonl"`, 1)
	if err := os.WriteFile(c.Binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := c.Run(context.Background(), Job{Workspace: ws, Prompt: "build", GateServer: []string{"/bin/invariant"}})
			done <- err
		}()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	turns := strings.Fields(standInFile(t, dir, "turns"))
	var kinds []string
	for i := 0; i < len(turns); i += 2 {
		kinds = append(kinds, turns[i])
	}
	if !slices.Equal(kinds, []string{"start", "end", "start", "end"}) {
		t.Errorf("two runs overlapped: %v", turns)
	}
}

// codex exec keeps no cap on steps, so the backend stops the run at its
// own, and doesn't wait for codex to finish.
func TestCodexStopsAtItsCapOnSteps(t *testing.T) {
	var events strings.Builder
	events.WriteString(`{"type":"turn.started"}` + "\n")
	for range 6 {
		events.WriteString(`{"type":"item.completed","item":{"type":"command_execution","command":"true","status":"completed"}}` + "\n")
	}
	c, dir, ws := codexRig(t, events.String())
	c.MaxTurns = 3
	if err := os.WriteFile(filepath.Join(dir, "linger"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	u, err := c.Run(context.Background(), Job{Workspace: ws, Prompt: "build", GateServer: []string{"/bin/invariant"}})
	if !errors.Is(err, ErrTooManyTurns) || u.Outcome != "error_max_turns" || u.Turns != 4 {
		t.Fatalf("got %+v, %v; want the run stopped at its cap", u, err)
	}
	if time.Since(start) > 10*time.Second {
		t.Error("the backend waited for codex instead of stopping it")
	}
	// What codex started is stopped with it.
	pid, err := strconv.Atoi(strings.TrimSpace(standInFile(t, dir, "child")))
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatal("a command codex started outlived the stopped run")
		}
	}
}

// A turn that fails says why, and a required server that doesn't start
// shows as a gate tool that didn't connect.
func TestCodexSaysWhyARunFailed(t *testing.T) {
	c, _, ws := codexRig(t, `{"type":"turn.started"}`+"\n"+`{"type":"turn.failed","error":{"message":"usage limit reached"}}`+"\n")
	u, err := c.Run(context.Background(), Job{Workspace: ws, Prompt: "build", GateServer: []string{"/bin/invariant"}})
	if err == nil || !strings.Contains(err.Error(), "usage limit reached") || u.Outcome != "failed" {
		t.Errorf("a failed turn: %+v, %v", u, err)
	}

	c, dir, ws := codexRig(t, "")
	for name, text := range map[string]string{"stderr": "Error: required MCP servers failed to initialize: invariant\n", "exit": "1"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	u, err = c.Run(context.Background(), Job{Workspace: ws, Prompt: "build", GateServer: []string{"/bin/invariant"}})
	if err == nil || u.GateTool != "failed" || !strings.Contains(err.Error(), "MCP servers failed") {
		t.Errorf("a server that didn't start: %+v, %v", u, err)
	}
}

// containsRun says whether want appears in args, in order and together.
func containsRun(args, want []string) bool {
	for i := 0; i+len(want) <= len(args); i++ {
		if slices.Equal(args[i:i+len(want)], want) {
			return true
		}
	}
	return false
}
