package synth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

type modeled540Backend struct {
	run func(context.Context, Job) (Usage, error)
}

func (b modeled540Backend) Name() string { return "modeled-gate-fixture" }
func (b modeled540Backend) Run(ctx context.Context, job Job) (Usage, error) {
	return b.run(ctx, job)
}

func TestModeledGate540UsesBuildAllowance(t *testing.T) {
	t.Setenv("MCP_TOOL_TIMEOUT", "1")
	t.Setenv("CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT", "17")
	for _, codex := range []bool{true, false} {
		backendName := "claude_code"
		if codex {
			backendName = "codex"
		}
		for _, mode := range []string{"bounded", "uncut", "shared fallback", "ordinary fallback", "longer explicit backend timer"} {
			t.Run(backendName+"/"+mode, func(t *testing.T) {
				// Use Synthesize's real job construction and each real backend's
				// process launch, with a CLI that only records its configuration.
				bin := checkWaitTimeoutCLI(t, t.TempDir(), codex)
				script, err := os.ReadFile(bin)
				if err != nil {
					t.Fatal(err)
				}
				script = append(script, []byte("printf '%s\\n' \"${CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT-}\" > check-wait-idle-timeout\n")...)
				if err := os.WriteFile(bin, script, 0700); err != nil {
					t.Fatal(err)
				}
				var actual Backend = ClaudeCode{Binary: bin, Model: "fixture", MaxTurns: 10, BudgetUSD: 1}
				explicit := 0
				if mode == "longer explicit backend timer" {
					explicit = 7200
				}
				if codex {
					actual = Codex{Binary: bin, Home: t.TempDir(), MaxTurns: 10, ToolTimeout: explicit}
				}
				ctx, stop := context.WithTimeout(t.Context(), 15*time.Second)
				defer stop()
				allowance := 40*time.Minute + 500*time.Millisecond
				if mode == "uncut" {
					allowance = 0
				}
				if mode == "shared fallback" {
					var cancel context.CancelFunc
					ctx, cancel = WithExecutionBudget(ctx, 30*time.Minute)
					defer cancel()
				}
				fallback := ""
				if strings.Contains(mode, "fallback") {
					fallback = "xhigh"
				}
				var jobs []Job
				capture := modeled540Backend{run: func(ctx context.Context, job Job) (Usage, error) {
					jobs = append(jobs, job)
					if job.ReadOnly || len(job.GateServer) == 0 || len(job.Tools) != 0 && !contains(job.Tools, "gate") {
						t.Fatalf("not a modeled build's gate job: %+v", job)
					}
					if allowance == 0 {
						if job.ToolTimeout >= 0 {
							t.Errorf("uncut modeled build retained the ordinary tool timer: %s", job.ToolTimeout)
						}
					} else if job.ToolTimeout <= 0 || job.ToolTimeout != job.Timeout {
						t.Errorf("gate tool allowance = %s, remaining run allowance = %s", job.ToolTimeout, job.Timeout)
					}
					u, err := actual.Run(ctx, job)
					if err != nil || u.Outcome != "success" {
						t.Fatalf("fake CLI did not finish: %+v, %v", u, err)
					}
					read := func(name string) string {
						t.Helper()
						b, err := os.ReadFile(filepath.Join(job.Workspace, name))
						if err != nil {
							t.Fatal(err)
						}
						return strings.TrimSpace(string(b))
					}
					if codex {
						args := read("check-wait-args")
						match := regexp.MustCompile(`\btool_timeout_sec\s*=\s*([0-9]+)\b`).FindAllStringSubmatch(args, -1)
						want := int64((job.Timeout + time.Second - 1) / time.Second)
						if allowance == 0 {
							want = int64((1<<63 - 1) / time.Second)
						}
						want = max(want, int64(explicit))
						if len(match) != 1 || match[0][1] != strconv.FormatInt(want, 10) {
							t.Errorf("modeled gate CLI timeout: want %d seconds, got %s", want, args)
						}
					} else {
						want := int64((job.Timeout + time.Millisecond - 1) / time.Millisecond)
						if allowance == 0 {
							want = 1<<31 - 1
						}
						if got := read("check-wait-tool-timeout"); got != fmt.Sprint(want) {
							t.Errorf("modeled gate CLI timeout = %s milliseconds, want %d", got, want)
						}
						if got := read("check-wait-idle-timeout"); got != "0" {
							t.Errorf("a silent gate still has an independent idle cutoff: %q", got)
						}
					}
					if fallback != "" && len(jobs) == 1 {
						time.Sleep(150 * time.Millisecond)
						return u, ErrThinkingLoop
					}
					// End before Synthesize's final verifier phase; this is the
					// existing provider interruption path, not a real agent run.
					return u, ErrProviderUnavailable
				}}
				r, err := Synthesize(ctx, Options{Project: example, Out: filepath.Join(t.TempDir(), "out"), Backend: capture,
					Binary: "fixture-invariant", KeepModel: true, GateRuns: 4, Timeout: allowance, FallbackEffort: fallback})
				if r == nil || !errors.Is(err, ErrProviderUnavailable) {
					t.Fatalf("configuration fixture did not stop before the final gate: %+v, %v", r, err)
				}
				wantRuns := 1
				if fallback != "" {
					wantRuns = 2
				}
				if len(jobs) != wantRuns {
					t.Fatalf("got %d runs, want %d", len(jobs), wantRuns)
				}
				if mode == "shared fallback" {
					if jobs[0].Timeout > 30*time.Minute || jobs[0].ToolTimeout-jobs[1].ToolTimeout < 100*time.Millisecond {
						t.Fatalf("fallback renewed the shared allowance: first=%s second=%s", jobs[0].ToolTimeout, jobs[1].ToolTimeout)
					}
				}
				if mode == "ordinary fallback" && (jobs[0].Timeout != allowance || jobs[1].Timeout != allowance) {
					t.Fatal("ordinary modeled fallback changed its existing timeout restart policy")
				}
			})
		}
	}
}
