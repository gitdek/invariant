package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/synth"
)

// A CLI stand-in starts the actual MCP command from the backend's config.
// Every provider attempt starts a fresh server and calls check twice. The
// first attempt then loses its provider before reporting any tool events;
// accounting cannot depend on a completed agent transcript or result.
func TestProviderRetriesShareMCPAllowance(t *testing.T) {
	if providerMCPChild() {
		return
	}
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
				cli := wrap("agent", "agent", agent, dir)
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
					for _, attempt := range []int{2*run + 1, 2*run + 2} {
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
	if len(args) < 4 || args[0] != "agent" {
		fail(fmt.Errorf("bad provider fixture args %q", args))
	}
	agent, dir, cliArgs := args[1], args[2], args[3:]
	var command string
	var serverArgs []string
	for i, arg := range cliArgs {
		if agent == "claude-code" && arg == "--mcp-config" && i+1 < len(cliArgs) {
			var config struct {
				Servers map[string]struct {
					Command string   `json:"command"`
					Args    []string `json:"args"`
				} `json:"mcpServers"`
			}
			if err := json.Unmarshal([]byte(cliArgs[i+1]), &config); err != nil {
				fail(err)
			}
			command, serverArgs = config.Servers["invariant"].Command, config.Servers["invariant"].Args
		}
		if agent == "codex" && strings.HasPrefix(arg, "mcp_servers.invariant=") {
			// Both literals are JSON strings/arrays in the backend's TOML.
			cmd := regexp.MustCompile(`command=("(?:[^"\\]|\\.)*")`).FindStringSubmatch(arg)
			argv := regexp.MustCompile(`args=(\[(?:[^\]"\\]|"(?:[^"\\]|\\.)*")*\])`).FindStringSubmatch(arg)
			if len(cmd) != 2 || len(argv) != 2 {
				fail(fmt.Errorf("cannot read server command from %s", arg))
			}
			if err := json.Unmarshal([]byte(cmd[1]), &command); err != nil {
				fail(err)
			}
			if err := json.Unmarshal([]byte(argv[1]), &serverArgs); err != nil {
				fail(err)
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
