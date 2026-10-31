package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/synth"
)

// A CLI stand-in starts the actual MCP command from the backend's config.
// Codex clears the server's environment, apart from its default whitelist
// and explicitly configured env/env_vars; the allowance must be forwarded.
// Every provider attempt starts a fresh server and calls check twice. The
// first attempt then loses its provider before reporting any tool events;
// accounting cannot depend on a completed agent transcript or result.
func TestProviderRetriesShareMCPAllowance(t *testing.T) {
	if providerMCPChild() {
		return
	}
	providerMCPAllowance(t, "missing", false)
}

func TestProviderRetriesReconcileMCPUsage(t *testing.T) {
	for _, events := range []string{"missing", "partial", "complete"} {
		t.Run(events, func(t *testing.T) {
			providerMCPAllowance(t, events, true)
		})
	}
}

func providerMCPAllowance(t *testing.T, events string, checkUsage bool) {
	t.Helper()
	for _, agent := range []string{"codex", "claude-code"} {
		for _, reports := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/reports=%v", agent, reports), func(t *testing.T) {
				dir, ws := t.TempDir(), t.TempDir()
				name, body := "issues.json", `{"name":"fixture","summary":"One change.","issues":[{"title":"A change","body":"One change.\n\nKind: plumbing"}]}`
				if !reports {
					// This accepted call currently returns before writing a log.
					name, body = "proposal.json", `{"unsupported":"Fixture: no work to build."}`
				}
				if err := os.WriteFile(filepath.Join(ws, name), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				exe, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				wrap := func(name string, args ...string) string {
					t.Helper()
					quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
					command := []string{exe, "-test.run=^TestProviderRetriesShareMCPAllowance$", "--", "provider-mcp-fixture"}
					command = append(command, args...)
					for i := range command {
						command[i] = quote(command[i])
					}
					path := filepath.Join(dir, name)
					if err := os.WriteFile(path, []byte("#!/bin/sh\nexec "+strings.Join(command, " ")+" \"$@\"\n"), 0o700); err != nil {
						t.Fatal(err)
					}
					return path
				}
				server := wrap("invariant", "server")
				cli := wrap("agent", "agent", agent, dir, events)
				var backend synth.Backend
				if agent == "codex" {
					backend = synth.Codex{Binary: cli, Home: t.TempDir(), MaxTurns: 20}
				} else {
					backend = synth.ClaudeCode{Binary: cli, BudgetUSD: 2, MaxTurns: 20}
				}
				if b, ok := backend.(interface {
					WithProviderWait(func(context.Context, time.Duration) error) synth.Backend
				}); ok {
					backend = b.WithProviderWait(func(ctx context.Context, _ time.Duration) error { return ctx.Err() })
				}
				job := synth.Job{Workspace: ws, Prompt: "check the proposal", Tools: []string{"check"}, Timeout: 10 * time.Second,
					GateServer: []string{server, "mcp", "-issues", "-max-runs", "3", "-log", filepath.Join(dir, "checks.jsonl"), ws}}
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				// A new Run gets a new allowance even with the same workspace,
				// backend and log; only provider attempts share the allowance.
				for run := 0; run < 2; run++ {
					u, err := backend.Run(ctx, job)
					if err != nil || u.Outcome != "success" {
						t.Fatalf("run %d did not recover: usage=%+v error=%v", run+1, u, err)
					}
					admitted, denied := 0, 0
					var allowance string
					for _, attempt := range []int{2*run + 1, 2*run + 2} {
						if agent == "codex" {
							forwarded, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("allowance.%d", attempt)))
							if err != nil || len(forwarded) == 0 {
								t.Fatalf("attempt %d did not forward its allowance through MCP config: %q, %v", attempt, forwarded, err)
							}
							if allowance != "" && allowance != string(forwarded) {
								t.Fatalf("run %d renewed its MCP allowance on retry: %q became %q", run+1, allowance, forwarded)
							}
							allowance = string(forwarded)
						}
						raw, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("responses.%d", attempt)))
						if err != nil {
							t.Fatal(err)
						}
						for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
							var reply struct {
								Result struct {
									Content []struct{ Text string } `json:"content"`
									IsError bool                    `json:"isError"`
								} `json:"result"`
							}
							if err := json.Unmarshal(line, &reply); err != nil || len(reply.Result.Content) != 1 {
								t.Fatalf("bad MCP response %q: %v", line, err)
							}
							text := reply.Result.Content[0].Text
							if reply.Result.IsError && strings.Contains(text, "No checks left") {
								denied++
							} else if !reply.Result.IsError && (strings.Contains(text, "holds together") || strings.Contains(text, "nothing to check")) {
								admitted++
							} else {
								t.Fatalf("unexpected tool result: %s", text)
							}
						}
					}
					if admitted != 3 || denied != 1 {
						t.Fatalf("run %d admitted %d calls and denied %d across two MCP servers; want the original allowance of three", run+1, admitted, denied)
					}
					if checkUsage {
						want := map[string]int{"mcp__invariant__check": 3}
						if events != "missing" {
							tool := "shell"
							if agent == "claude-code" {
								tool = "Read"
							}
							want[tool] = 2 // one ordinary tool in each attempt
						}
						if !reflect.DeepEqual(u.ToolCalls, want) {
							t.Errorf("run %d with %s agent events: tool usage=%v; want %v, reconciling three trusted admitted checks without counting reported calls twice", run+1, events, u.ToolCalls, want)
						}
					}
				}
			})
		}
	}
}

// Reexec the test binary as either a provider CLI or invariant mcp. No live
// service, Docker, nested Go build or credentials are needed.
func providerMCPChild() bool {
	var args []string
	for i, arg := range os.Args {
		if arg == "provider-mcp-fixture" {
			args = os.Args[i+1:]
			break
		}
	}
	if len(args) == 0 {
		return false
	}
	fail := func(err error) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if args[0] == "server" {
		if len(args) < 2 || args[1] != "mcp" {
			fail(fmt.Errorf("want mcp command, got %q", args))
		}
		os.Exit(mcpCmd(context.Background(), args[2:]))
	}
	if len(args) < 5 || args[0] != "agent" {
		fail(fmt.Errorf("bad provider fixture args %q", args))
	}
	agent, dir, events, cliArgs := args[1], args[2], args[3], args[4:]
	var command string
	var serverArgs []string
	serverEnv := make(map[string]string)
	var envVars []string
	applyCodexField := func(key, value string) {
		var err error
		switch key {
		case "command":
			err = json.Unmarshal([]byte(value), &command)
		case "args":
			err = json.Unmarshal([]byte(value), &serverArgs)
		case "env_vars":
			err = json.Unmarshal([]byte(value), &envVars)
		case "env":
			var fields map[string]string
			fields, err = providerMCPConfigFields(value)
			if err == nil {
				for name, literal := range fields {
					var v string
					if err = json.Unmarshal([]byte(literal), &v); err != nil {
						break
					}
					serverEnv[name] = v
				}
			}
		default:
			if strings.HasPrefix(key, "env.") {
				var v string
				err = json.Unmarshal([]byte(value), &v)
				serverEnv[strings.TrimPrefix(key, "env.")] = v
			}
		}
		if err != nil {
			fail(fmt.Errorf("cannot read MCP config %s=%s: %w", key, value, err))
		}
	}
	for i, arg := range cliArgs {
		if agent == "claude-code" && arg == "--mcp-config" && i+1 < len(cliArgs) {
			var config struct {
				Servers map[string]struct {
					Command string            `json:"command"`
					Args    []string          `json:"args"`
					Env     map[string]string `json:"env"`
				} `json:"mcpServers"`
			}
			if err := json.Unmarshal([]byte(cliArgs[i+1]), &config); err != nil {
				fail(err)
			}
			command, serverArgs = config.Servers["invariant"].Command, config.Servers["invariant"].Args
			for name, value := range config.Servers["invariant"].Env {
				serverEnv[name] = value
			}
		}
		if agent == "codex" {
			// Accept both the inline server table and separate dotted -c
			// overrides. env sets literal values; env_vars forwards named
			// values from Codex's own environment.
			if strings.HasPrefix(arg, "--config=") {
				arg = strings.TrimPrefix(arg, "--config=")
			} else if i == 0 || (cliArgs[i-1] != "-c" && cliArgs[i-1] != "--config") {
				continue
			}
			key, value, ok := strings.Cut(arg, "=")
			key, value = strings.TrimSpace(key), strings.TrimSpace(value)
			if !ok {
				continue
			}
			if key == "mcp_servers.invariant" {
				fields, err := providerMCPConfigFields(value)
				if err != nil {
					fail(err)
				}
				for field, literal := range fields {
					applyCodexField(field, literal)
				}
			} else if strings.HasPrefix(key, "mcp_servers.invariant.") {
				applyCodexField(strings.TrimPrefix(key, "mcp_servers.invariant."), value)
			}
		}
	}
	if command == "" {
		fail(fmt.Errorf("provider was not given its MCP server: %q", cliArgs))
	}
	count, _ := os.ReadFile(filepath.Join(dir, "attempts"))
	n, _ := strconv.Atoi(strings.TrimSpace(string(count)))
	n++
	if err := os.WriteFile(filepath.Join(dir, "attempts"), []byte(strconv.Itoa(n)), 0o600); err != nil {
		fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, serverArgs...)
	if agent == "codex" {
		// A nil Env would inherit everything and hide a production bug:
		// Codex does not forward INVARIANT_TOOL_ALLOWANCE by default.
		cmd.Env = []string{}
		for _, name := range append([]string{"HOME", "LOGNAME", "PATH", "SHELL", "TMPDIR", "USER"}, envVars...) {
			if value, ok := os.LookupEnv(name); ok {
				cmd.Env = append(cmd.Env, name+"="+value)
			}
		}
	} else {
		cmd.Env = os.Environ()
	}
	for name, value := range serverEnv {
		cmd.Env = append(cmd.Env, name+"="+value)
	}
	if agent == "codex" {
		var allowance string
		for _, entry := range cmd.Env {
			if value, ok := strings.CutPrefix(entry, "INVARIANT_TOOL_ALLOWANCE="); ok {
				allowance = value
			}
		}
		if allowance == "" {
			fail(fmt.Errorf("Codex MCP config did not explicitly forward INVARIANT_TOOL_ALLOWANCE to its filtered server environment"))
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("allowance.%d", n)), []byte(allowance), 0o600); err != nil {
			fail(err)
		}
	}
	cmd.Stdin = strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"check\",\"arguments\":{}}}\n" +
		"{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"check\",\"arguments\":{}}}\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		fail(fmt.Errorf("MCP server: %v: %s", err, stderr.String()))
	}
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("responses.%d", n)), output, 0o600); err != nil {
		fail(err)
	}
	// Report only admitted calls: two in the first attempt, one in the
	// second. Lost events may hide all calls, or one call in each attempt;
	// complete reporting must not add the trusted count to the same calls.
	if events != "missing" {
		reported := 2
		if n%2 == 0 {
			reported = 1
		}
		if events == "partial" {
			reported--
		} else if events != "complete" {
			fail(fmt.Errorf("unknown event fixture %q", events))
		}
		for call := 0; call < reported; call++ {
			if agent == "codex" {
				fmt.Printf("{\"type\":\"item.completed\",\"item\":{\"id\":\"check-%d-%d\",\"type\":\"mcp_tool_call\",\"server\":\"invariant\",\"tool\":\"check\",\"status\":\"completed\"}}\n", n, call)
			} else {
				fmt.Printf("{\"type\":\"assistant\",\"message\":{\"id\":\"check-%d-%d\",\"content\":[{\"type\":\"tool_use\",\"id\":\"call-%d-%d\",\"name\":\"mcp__invariant__check\",\"input\":{}}]}}\n", n, call, n, call)
			}
		}
		// Reconciliation must retain unrelated tools counted from events.
		if agent == "codex" {
			fmt.Println(`{"type":"item.completed","item":{"type":"command_execution","aggregated_output":"read source","exit_code":0,"status":"completed"}}`)
		} else {
			fmt.Printf("{\"type\":\"assistant\",\"message\":{\"id\":\"read-%d\",\"content\":[{\"type\":\"tool_use\",\"id\":\"read-call-%d\",\"name\":\"Read\",\"input\":{\"file_path\":\"source.go\"}}]}}\n", n, n)
		}
	}
	if n%2 == 1 {
		if agent == "codex" {
			fmt.Println(`{"type":"turn.failed","error":{"message":"Selected model is at capacity. Please try a different model."}}`)
		} else {
			fmt.Println(`{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["API Error: 529 overloaded_error"],"total_cost_usd":0.125,"num_turns":1}`)
		}
		os.Exit(1)
	}
	if agent == "codex" {
		fmt.Println(`{"type":"item.completed","item":{"type":"agent_message","text":"Done."}}`)
		fmt.Println(`{"type":"turn.completed"}`)
	} else {
		fmt.Println(`{"type":"result","subtype":"success","result":"Done.","total_cost_usd":0.125,"num_turns":1}`)
	}
	os.Exit(0)
	return true
}

// Read the code-generated TOML subset used by Codex: inline tables containing
// basic strings, arrays and nested tables. Splitting only at top-level commas
// preserves quoted paths and the env table without depending on field order.
func providerMCPConfigFields(value string) (map[string]string, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "{") || !strings.HasSuffix(value, "}") {
		return nil, fmt.Errorf("want MCP inline table, got %q", value)
	}
	value = value[1 : len(value)-1]
	fields := make(map[string]string)
	start, depth, quoted, escaped := 0, 0, false, false
	for i := 0; i <= len(value); i++ {
		if i == len(value) || (!quoted && depth == 0 && value[i] == ',') {
			field := strings.TrimSpace(value[start:i])
			start = i + 1
			if field == "" {
				continue
			}
			key, literal, ok := strings.Cut(field, "=")
			if !ok {
				return nil, fmt.Errorf("bad MCP config field %q", field)
			}
			key = strings.TrimSpace(key)
			if strings.HasPrefix(key, `"`) {
				if err := json.Unmarshal([]byte(key), &key); err != nil {
					return nil, err
				}
			}
			fields[key] = strings.TrimSpace(literal)
			continue
		}
		if quoted {
			if escaped {
				escaped = false
			} else if value[i] == '\\' {
				escaped = true
			} else if value[i] == '"' {
				quoted = false
			}
			continue
		}
		switch value[i] {
		case '"':
			quoted = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		}
	}
	if quoted || depth != 0 {
		return nil, fmt.Errorf("unbalanced MCP config table %q", value)
	}
	return fields, nil
}
