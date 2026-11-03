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

func TestUncutQueuedJobsUseBackendMaximumToolTimers(t *testing.T) {
	t.Setenv("MCP_TOOL_TIMEOUT", "1")
	t.Setenv("CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT", "17")
	for _, codex := range []bool{false, true} {
		name := "claude_code"
		if codex {
			name = "codex"
		}
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name    string
				timeout time.Duration
				seconds int64
				millis  int64
				idle    string
			}{
				{name: "ordinary", seconds: 900, millis: 900000, idle: "17"},
				{name: "uncut", timeout: -1, seconds: 9223372036, millis: 2147483647, idle: "0"},
				{name: "queued", timeout: 40 * time.Minute, seconds: 2400, millis: 2400000, idle: "0"},
				{name: "largest_duration", timeout: 1<<63 - 1, seconds: 9223372037, millis: 2147483647, idle: "0"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					ws := t.TempDir()
					bin := checkWaitTimeoutCLI(t, ws, codex)
					// The launched process records the idle setting too, so an
					// inherited short timer cannot silently truncate a queued call.
					script, err := os.ReadFile(bin)
					if err != nil {
						t.Fatal(err)
					}
					script = append(script, []byte("printf '%s\\n' \"${CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT-}\" > check-wait-idle-timeout\n")...)
					if err := os.WriteFile(bin, script, 0o700); err != nil {
						t.Fatal(err)
					}
					var b Backend = ClaudeCode{Binary: bin, Model: "fixture", MaxTurns: 10, BudgetUSD: 1}
					if codex {
						b = Codex{Binary: bin, Home: t.TempDir(), MaxTurns: 10}
					}
					ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
					defer cancel()
					u, err := b.Run(ctx, Job{Workspace: ws, Prompt: "Run checks.", GateServer: []string{"fixture-invariant", "mcp"}, Tools: []string{"test"}, ToolTimeout: tc.timeout})
					if err != nil || u.Outcome != "success" {
						t.Fatalf("backend run: usage=%+v error=%v", u, err)
					}
					read := func(name string) string {
						t.Helper()
						data, err := os.ReadFile(filepath.Join(ws, name))
						if err != nil {
							t.Fatal(err)
						}
						return strings.TrimSpace(string(data))
					}
					if codex {
						args := read("check-wait-args")
						matches := regexp.MustCompile(`\btool_timeout_sec=([0-9]+)\b`).FindAllStringSubmatch(args, -1)
						if len(matches) != 1 || matches[0][1] != strconv.FormatInt(tc.seconds, 10) {
							t.Fatalf("Codex tool timer: want %d seconds, got %s", tc.seconds, args)
						}
					} else {
						if got := read("check-wait-tool-timeout"); got != strconv.FormatInt(tc.millis, 10) {
							t.Errorf("Claude tool timer = %s, want %d milliseconds", got, tc.millis)
						}
						if got := read("check-wait-idle-timeout"); got != tc.idle {
							t.Errorf("Claude idle timer = %q, want %q", got, tc.idle)
						}
					}
				})
			}
		})
	}
}
