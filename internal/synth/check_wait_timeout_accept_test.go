package synth

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A queued check can spend more than the ordinary fifteen-minute MCP timeout
// waiting for another build. Run the actual backends against CLI stand-ins to
// check the configuration the processes receive, without running real agents
// or waiting for the long allowance to pass.
func TestQueuedCheckToolCallsHaveTimeToWait(t *testing.T) {
	// The job's setting must also override an inherited, shorter Claude Code
	// timeout. This test changes the environment, so it stays serial.
	t.Setenv("MCP_TOOL_TIMEOUT", "1")
	for _, backend := range []struct {
		name         string
		codex        bool
		codexTimeout int
	}{
		{name: "codex", codex: true},
		{name: "codex_with_shorter_backend_timeout", codex: true, codexTimeout: 37},
		{name: "codex_with_longer_backend_timeout", codex: true, codexTimeout: 3600},
		{name: "claude_code"},
	} {
		t.Run(backend.name, func(t *testing.T) {
			for _, tc := range []struct {
				name        string
				timeout     time.Duration
				wantSeconds int
				wantMillis  string
			}{
				{name: "ordinary_job", wantSeconds: 900, wantMillis: "900000"},
				{name: "queued_checks", timeout: 40*time.Minute + 500*time.Millisecond, wantSeconds: 2401, wantMillis: "2400500"},
				{name: "round_up_fractional_millisecond", timeout: 40*time.Minute + 500*time.Millisecond + time.Nanosecond, wantSeconds: 2401, wantMillis: "2400501"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					workspace := t.TempDir()
					bin := checkWaitTimeoutCLI(t, workspace, backend.codex)
					var b Backend = ClaudeCode{Binary: bin, Model: "fixture-model", MaxTurns: 10, BudgetUSD: 1}
					if backend.codex {
						b = Codex{Binary: bin, Home: t.TempDir(), MaxTurns: 10, ToolTimeout: backend.codexTimeout}
					}
					ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
					defer cancel()
					u, err := b.Run(ctx, Job{
						Workspace:   workspace,
						Prompt:      "Run the queued build checks.",
						GateServer:  []string{"fixture-invariant", "mcp"},
						Tools:       []string{"test"},
						Timeout:     5 * time.Second,
						ToolTimeout: tc.timeout,
					})
					if err != nil || u.Outcome != "success" {
						t.Fatalf("CLI stand-in did not finish: usage=%+v, error=%v", u, err)
					}
					read := func(name string) string {
						t.Helper()
						data, err := os.ReadFile(filepath.Join(workspace, name))
						if err != nil {
							t.Fatal(err)
						}
						return string(data)
					}
					if backend.codex {
						args := read("check-wait-args")
						matches := regexp.MustCompile(`\btool_timeout_sec\s*=\s*([0-9]+)\b`).FindAllStringSubmatch(args, -1)
						if len(matches) != 1 {
							t.Fatalf("want one MCP tool timeout in Codex arguments, got %d: %s", len(matches), args)
						}
						got, err := strconv.Atoi(matches[0][1])
						want := tc.wantSeconds
						if tc.timeout == 0 && backend.codexTimeout > 0 {
							want = backend.codexTimeout
						} else if backend.codexTimeout > want {
							want = backend.codexTimeout
						}
						if err != nil || got != want {
							t.Errorf("Codex MCP tool timeout = %q seconds; want %d for job allowance %s (run deadline is only 5s)", matches[0][1], want, tc.timeout)
						}
					} else if got := strings.TrimSpace(read("check-wait-tool-timeout")); got != tc.wantMillis {
						t.Errorf("Claude Code MCP_TOOL_TIMEOUT = %q milliseconds; want %s for job allowance %s (run deadline is only 5s)", got, tc.wantMillis, tc.timeout)
					}
				})
			}
		})
	}
}

func checkWaitTimeoutCLI(t *testing.T, workspace string, codex bool) string {
	t.Helper()
	result := `{"type":"result","subtype":"success","result":"Checks passed.","num_turns":1}`
	if codex {
		result = `{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":2}}`
	}
	// Both records are written by the launched CLI, so these assertions cover
	// Backend.Run's arguments and environment rather than a helper's result.
	script := `#!/bin/sh
printf '%s\n' "$@" > check-wait-args
printf '%s\n' "${MCP_TOOL_TIMEOUT-}" > check-wait-tool-timeout
printf '%s\n' '` + result + `'
`
	bin := filepath.Join(workspace, "check-wait-agent")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin
}
