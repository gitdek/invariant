package synth

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Each attempt reports real work before its terminal provider error. This
// makes replacing an earlier attempt's usage observable, even when the last
// attempt succeeds, fails normally, or exhausts the provider retry policy.
func providerClaudeUsageAttempt(outcome, summary string, cost float64, turns int, calls map[string]int) providerAttempt {
	init := map[string]any{"type": "system", "subtype": "init", "model": "fixture-model"}
	for name := range calls {
		if strings.HasPrefix(name, "mcp__invariant__") {
			init["mcp_servers"] = []map[string]string{{"name": "invariant", "status": "connected"}}
		}
	}
	out := providerJSON(init)
	var names []string
	for name := range calls {
		names = append(names, name)
	}
	sort.Strings(names)
	var content []map[string]any
	for _, name := range names {
		for i := 0; i < calls[name]; i++ {
			content = append(content, map[string]any{"type": "tool_use", "name": name,
				"id": fmt.Sprintf("%s-%d", name, i), "input": map[string]any{}})
		}
	}
	if len(content) > 0 {
		out += providerJSON(map[string]any{"type": "assistant", "message": map[string]any{
			"id": "attempt-work", "role": "assistant", "model": "fixture-model", "content": content,
			"usage": map[string]int{"input_tokens": 100, "output_tokens": 30,
				"cache_creation_input_tokens": 1000, "cache_read_input_tokens": 2000},
		}})
	}
	result := map[string]any{
		"type": "result", "subtype": outcome, "result": summary,
		"total_cost_usd": cost, "num_turns": turns, "is_error": outcome != "success",
	}
	exit := 0
	if outcome != "success" {
		result["errors"], exit = []string{summary}, 1
	}
	return providerAttempt{out: out + providerJSON(result), exit: exit}
}

func providerCodexUsageAttempt(success bool, summary string, tools ...string) providerAttempt {
	out := "{\"type\":\"turn.started\"}\n"
	for _, tool := range tools {
		item := map[string]any{"type": tool, "status": "completed"}
		if tool == "mcp_tool_call" {
			item["server"], item["tool"] = "invariant", "gate"
		}
		out += providerJSON(map[string]any{"type": "item.completed", "item": item})
	}
	if !success {
		out += providerJSON(map[string]any{"type": "turn.failed", "error": map[string]string{"message": summary}})
		return providerAttempt{out: out, exit: 1}
	}
	out += providerJSON(map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": summary}})
	out += "{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}\n"
	return providerAttempt{out: out}
}

func providerNoWait(ctx context.Context, _ time.Duration) error { return ctx.Err() }

func providerFlag(t *testing.T, dir string, attempt int, flag string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("args.%d", attempt)))
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(string(b), "\n")
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	t.Fatalf("attempt %d omitted %s: %q", attempt, flag, args)
	return ""
}

func TestProviderRetriesSumUsageAndKeepTheLastOutcome(t *testing.T) {
	for _, agent := range []string{"codex", "claude-code"} {
		for _, ending := range []string{"success", "ordinary failure", "provider exhaustion"} {
			t.Run(agent+"/"+ending, func(t *testing.T) {
				last := "Last attempt completed the job."
				wantOutcome := "success"
				n := 3
				if ending != "success" {
					last, wantOutcome = "Invalid API key on the last attempt", "failed"
					if ending == "provider exhaustion" {
						last, n = providerCapacity+" (last attempt)", 4
					}
					if agent == "claude-code" {
						wantOutcome = "error_during_execution"
						if ending == "provider exhaustion" {
							last = "API Error: 529 overloaded_error (last attempt)"
						}
					}
				}
				var attempts []providerAttempt
				want := Usage{Outcome: wantOutcome, Summary: last, ToolCalls: map[string]int{}}
				for i := 0; i < n; i++ {
					final := i == n-1
					if agent == "claude-code" {
						outcome, summary := "error_during_execution", "API Error: 529 overloaded_error"
						if final {
							outcome, summary = wantOutcome, last
						}
						calls := map[string]int{"Read": i + 1, "mcp__invariant__gate": 1}
						cost, turns := .0625*float64(i+1), i+2
						attempts = append(attempts, providerClaudeUsageAttempt(outcome, summary, cost, turns, calls))
						want.CostUSD += cost
						want.Turns += turns
						for tool, count := range calls {
							want.ToolCalls[tool] += count
						}
					} else {
						summary := providerCapacity
						if final {
							summary = last
						}
						attempts = append(attempts, providerCodexUsageAttempt(final && ending == "success", summary,
							"command_execution", "file_change", "mcp_tool_call"))
						want.Turns += 3
						if final && ending == "success" {
							want.Turns++ // the closing agent message is also a Codex step
						}
						for _, tool := range []string{"shell", "edit", "mcp__invariant__gate"} {
							want.ToolCalls[tool]++
						}
					}
				}
				b, dir := providerBackend(t, agent, attempts...)
				b = providerWithWait(b, providerNoWait)
				u, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), GateServer: []string{"invariant", "mcp"}, Timeout: 5 * time.Second})
				if ending == "success" && err != nil || ending != "success" && err == nil {
					t.Errorf("last outcome %q returned error %v", ending, err)
				}
				if ending == "provider exhaustion" {
					providerUnavailable(t, err, last)
				}
				if got := providerCount(t, dir); got != n {
					t.Errorf("attempts=%d; want %d", got, n)
				}
				if u.Turns != want.Turns || math.Abs(u.CostUSD-want.CostUSD) > 1e-9 || !reflect.DeepEqual(u.ToolCalls, want.ToolCalls) {
					t.Errorf("aggregate usage=%+v; want turns=%d cost=%g tools=%v", u, want.Turns, want.CostUSD, want.ToolCalls)
				}
				if u.Outcome != want.Outcome || u.Summary != last {
					t.Errorf("final outcome=%q summary=%q; want %q and %q", u.Outcome, u.Summary, want.Outcome, last)
				}
			})
		}
	}
}

func TestClaudeRetriesReceiveOnlyTheRemainingBudgetAndTurns(t *testing.T) {
	first := providerClaudeUsageAttempt("error_during_execution", "API Error: 529 overloaded_error", .125, 2, map[string]int{"Read": 1})
	second := providerClaudeUsageAttempt("error_during_execution", "API Error: 429 rate_limit_error", .25, 1, map[string]int{"Read": 1})
	third := providerClaudeUsageAttempt("success", "Done.", .25, 2, map[string]int{"Read": 1})
	bin, dir := providerCLI(t, first, second, third)
	b := providerWithWait(ClaudeCode{Binary: bin, Model: "fixture-model", BudgetUSD: 1, MaxTurns: 7}, providerNoWait)
	u, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 5 * time.Second})
	if err != nil || u.Outcome != "success" || providerCount(t, dir) != 3 {
		t.Fatalf("retry sequence: usage=%+v, error=%v, attempts=%d", u, err, providerCount(t, dir))
	}
	for i, wantBudget := range []float64{1, .875, .625} {
		budget, err := strconv.ParseFloat(providerFlag(t, dir, i+1, "--max-budget-usd"), 64)
		if err != nil || math.Abs(budget-wantBudget) > 1e-9 {
			t.Errorf("attempt %d budget=%g (%v); want exactly %g, without rounding the remaining allowance upward", i+1, budget, err, wantBudget)
		}
		turns, err := strconv.Atoi(providerFlag(t, dir, i+1, "--max-turns"))
		wantTurns := []int{7, 5, 4}[i]
		if err != nil || turns != wantTurns {
			t.Errorf("attempt %d turns=%d (%v); want remaining %d", i+1, turns, err, wantTurns)
		}
	}
	if u.Turns != 5 || math.Abs(u.CostUSD-.625) > 1e-9 {
		t.Errorf("usage=%+v; want five turns and $0.625 for the whole run", u)
	}
}

func TestClaudeProviderRetriesCannotRenewAnExhaustedLimit(t *testing.T) {
	for _, limit := range []string{"budget", "turns"} {
		t.Run(limit, func(t *testing.T) {
			cost, turns := .25, 2
			if limit == "budget" {
				cost = 1
			} else {
				turns = 7
			}
			failure := providerClaudeUsageAttempt("error_during_execution", "API Error: 529 overloaded_error", cost, turns, map[string]int{"Read": 1})
			bin, dir := providerCLI(t, failure, providerSuccess("claude-code"))
			waits := 0
			b := providerWithWait(ClaudeCode{Binary: bin, Model: "fixture-model", BudgetUSD: 1, MaxTurns: 7}, func(ctx context.Context, _ time.Duration) error {
				waits++
				return ctx.Err()
			})
			u, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 5 * time.Second})
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.TrimSuffix(limit, "s")) {
				t.Errorf("exhausted %s returned %v; want a diagnostic identifying the exhausted limit", limit, err)
			}
			if count := providerCount(t, dir); count != 1 || waits != 0 {
				t.Errorf("exhausted %s launched %d attempts and waited %d times; want one attempt and no wait", limit, count, waits)
			}
			if u.Outcome != "error_during_execution" || u.Turns != turns || math.Abs(u.CostUSD-cost) > 1e-9 {
				t.Errorf("limit exhaustion lost the last attempt's outcome or usage: %+v", u)
			}
		})
	}
}

func TestClaudeProviderRetryStopsAtTheRemainingLimit(t *testing.T) {
	// The stand-in CLI needs two more turns and $0.50 on attempt two. It
	// honors the actual limits supplied by the backend, as Claude Code does.
	// A renewed allowance lets it succeed, which is the bug under test.
	for _, limit := range []string{"budget", "turns"} {
		t.Run(limit, func(t *testing.T) {
			cost, turns, flag := .75, 1, "--max-budget-usd"
			wantOutcome, wantCost, wantTurns := "error_max_budget_usd", 1.0, 2
			before := `budget=$(awk '$0 == "--max-budget-usd" {getline; print; exit}' "$here/args.$n")
awk -v remaining="$budget" 'BEGIN {
  if (remaining < 0.5) {
    printf "{\"type\":\"result\",\"subtype\":\"error_max_budget_usd\",\"is_error\":true,\"result\":\"Budget exhausted\",\"num_turns\":1,\"total_cost_usd\":%.9f}\n", remaining
  } else {
    print "{\"type\":\"result\",\"subtype\":\"success\",\"result\":\"Done.\",\"num_turns\":1,\"total_cost_usd\":0.5}"
  }
}' > "$here/out.$i"
`
			wantRemaining := .25
			if limit == "turns" {
				cost, turns, flag = .125, 3, "--max-turns"
				wantOutcome, wantCost, wantTurns, wantRemaining = "error_max_turns", .25, 4, 1
				before = `turns=$(awk '$0 == "--max-turns" {getline; print; exit}' "$here/args.$n")
awk -v remaining="$turns" 'BEGIN {
  if (remaining < 2) {
    printf "{\"type\":\"result\",\"subtype\":\"error_max_turns\",\"is_error\":true,\"result\":\"Turn limit reached\",\"num_turns\":%d,\"total_cost_usd\":0.125}\n", remaining
  } else {
    print "{\"type\":\"result\",\"subtype\":\"success\",\"result\":\"Done.\",\"num_turns\":2,\"total_cost_usd\":0.125}"
  }
}' > "$here/out.$i"
`
			}
			first := providerClaudeUsageAttempt("error_during_execution", "API Error: 529 overloaded_error", cost, turns, nil)
			second := providerAttempt{before: before}
			bin, dir := providerCLI(t, first, second, providerSuccess("claude-code"))
			b := providerWithWait(ClaudeCode{Binary: bin, Model: "fixture-model", BudgetUSD: 1, MaxTurns: 4}, providerNoWait)
			u, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 5 * time.Second})
			if err == nil || u.Outcome != wantOutcome || providerCount(t, dir) != 2 {
				t.Fatalf("retry renewed %s: usage=%+v error=%v attempts=%d", limit, u, err, providerCount(t, dir))
			}
			remaining, parseErr := strconv.ParseFloat(providerFlag(t, dir, 2, flag), 64)
			if parseErr != nil || math.Abs(remaining-wantRemaining) > 1e-9 {
				t.Errorf("second attempt %s=%g (%v); want %g", flag, remaining, parseErr, wantRemaining)
			}
			if u.Turns != wantTurns || math.Abs(u.CostUSD-wantCost) > 1e-9 {
				t.Errorf("usage=%+v; want %d turns, cost %g at the shared limit", u, wantTurns, wantCost)
			}
		})
	}
}

// Claude Code reports token usage on assistant messages, but dollars only
// on the closing result. These are paid replies followed by a provider
// disconnect, with no result from which to learn the final cost. The model
// deliberately has no known price: a guessed conversion from tokens must
// not authorize another paid attempt.
func providerClaudeIncompletePaidAttempt() providerAttempt {
	return providerAttempt{out: `{"type":"system","subtype":"init","model":"fixture-model"}
{"type":"assistant","message":{"id":"paid-reply-1","type":"message","role":"assistant","model":"fixture-model","content":[{"type":"tool_use","id":"read-before-disconnect","name":"Read","input":{"file_path":"source.go"}}],"stop_reason":"tool_use","usage":{"input_tokens":120,"cache_creation_input_tokens":1000,"cache_read_input_tokens":2000,"output_tokens":40}}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"read-before-disconnect","content":"package example"}]}}
{"type":"assistant","message":{"id":"paid-reply-2","type":"message","role":"assistant","model":"fixture-model","content":[{"type":"text","text":"I found the problem and am checking the fix."}],"stop_reason":"end_turn","usage":{"input_tokens":140,"cache_creation_input_tokens":0,"cache_read_input_tokens":3000,"output_tokens":60}}}
`, stderr: "API request failed: connection reset by peer\n", exit: 1}
}

func TestClaudeIncompletePaidAttemptCannotRenewTheDollarBudget(t *testing.T) {
	for _, afterRetry := range []bool{false, true} {
		t.Run(fmt.Sprintf("after a reported paid failure=%v", afterRetry), func(t *testing.T) {
			var attempts []providerAttempt
			wantTurns, wantReads := 2, 1
			if afterRetry {
				attempts = append(attempts, providerClaudeUsageAttempt("error_during_execution", "API Error: 529 overloaded_error", .125, 3, map[string]int{"Read": 2}))
				wantTurns, wantReads = 5, 3
			}
			attempts = append(attempts, providerClaudeIncompletePaidAttempt(), providerSuccess("claude-code"))
			bin, dir := providerCLI(t, attempts...)
			waits := 0
			b := providerWithWait(ClaudeCode{Binary: bin, Model: "fixture-model", BudgetUSD: 1, MaxTurns: 9}, func(ctx context.Context, _ time.Duration) error {
				waits++
				return ctx.Err()
			})
			u, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 5 * time.Second})
			if err == nil {
				t.Fatal("paid work with no closing cost result renewed the dollar budget and succeeded")
			}
			diagnostic := strings.ToLower(err.Error())
			if !strings.Contains(diagnostic, "connection reset by peer") || !strings.Contains(diagnostic, "budget") {
				t.Errorf("failure=%v; want the provider disconnect and the reason its remaining budget cannot be reused", err)
			}
			if !strings.Contains(diagnostic, "incomplete") && !strings.Contains(diagnostic, "unknown") && !strings.Contains(diagnostic, "uncertain") && !strings.Contains(diagnostic, "conservative") {
				t.Errorf("failure=%v; want explicit notice that final cost is unavailable and accounting is conservative", err)
			}
			wantAttempts := len(attempts) - 1
			if count := providerCount(t, dir); count != wantAttempts || waits != wantAttempts-1 {
				t.Errorf("attempts=%d waits=%d; want %d attempts and %d waits, with none after the incomplete paid attempt", count, waits, wantAttempts, wantAttempts-1)
			}
			// Reserve the whole remaining allowance when the final cost is
			// unknown, keeping this conservative estimate visible in Usage.
			// Earlier reported costs count once; their budget is not reserved
			// again. This is an estimate for the limit, never a known charge.
			if math.Abs(u.CostUSD-1) > 1e-9 || u.Turns != wantTurns || u.ToolCalls["Read"] != wantReads || u.Outcome == "success" {
				t.Errorf("incomplete usage=%+v; want the $1 conservative run estimate, %d observed turns, %d reads and no success", u, wantTurns, wantReads)
			}
			if afterRetry {
				remaining, parseErr := strconv.ParseFloat(providerFlag(t, dir, 2, "--max-budget-usd"), 64)
				if parseErr != nil || math.Abs(remaining-.875) > 1e-9 {
					t.Errorf("incomplete attempt budget=%g (%v); want the remaining $0.875", remaining, parseErr)
				}
			}
		})
	}
}

func TestClaudePaidRetryPreservesItsDeadlineWhileReservingCost(t *testing.T) {
	first := providerClaudeUsageAttempt("error_during_execution", "API Error: 529 overloaded_error", .125, 3, map[string]int{"Read": 2})
	second := providerClaudeIncompletePaidAttempt()
	// Emit paid replies, then replace the fixture shell with one process
	// that the run's deadline kills. There is deliberately no provider or
	// connection diagnostic on this attempt: cancellation is the cause.
	second.stderr = ""
	second.before = "cat \"$here/out.$i\"\ntouch \"$here/paid-started\"\nexec sleep 30\n"
	bin, dir := providerCLI(t, first, second, providerSuccess("claude-code"))
	waits := 0
	b := providerWithWait(ClaudeCode{Binary: bin, Model: "fixture-model", BudgetUSD: 1, MaxTurns: 9}, func(ctx context.Context, _ time.Duration) error {
		waits++
		return ctx.Err()
	})
	start := time.Now()
	u, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: time.Second})
	if _, startErr := os.Stat(filepath.Join(dir, "paid-started")); startErr != nil {
		t.Fatalf("the paid retry never reached its hanging stream: %v; run error=%v", startErr, err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("incomplete cost reservation lost the run's deadline cause: %v", err)
	}
	providerUnavailable(t, err, "API Error: 529 overloaded_error")
	if attempts := providerCount(t, dir); attempts != 2 || waits != 1 {
		t.Errorf("attempts=%d waits=%d; want one provider retry and no attempt or wait after its deadline", attempts, waits)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond || elapsed > 4*time.Second {
		t.Errorf("paid retry returned after %s; want the original one-second deadline", elapsed)
	}
	if math.Abs(u.CostUSD-1) > 1e-9 || u.Turns != 5 || u.ToolCalls["Read"] != 3 || u.Outcome == "success" {
		t.Errorf("deadline usage=%+v; want the $1 conservative run estimate, five observed turns, three reads and no success", u)
	}
	remaining, parseErr := strconv.ParseFloat(providerFlag(t, dir, 2, "--max-budget-usd"), 64)
	if parseErr != nil || math.Abs(remaining-.875) > 1e-9 {
		t.Errorf("paid retry budget=%g (%v); want the remaining $0.875 before its deadline", remaining, parseErr)
	}
	if err != nil {
		diagnostic := strings.ToLower(err.Error())
		if !strings.Contains(diagnostic, "budget") ||
			(!strings.Contains(diagnostic, "incomplete") && !strings.Contains(diagnostic, "unknown") && !strings.Contains(diagnostic, "uncertain") && !strings.Contains(diagnostic, "conservative")) {
			t.Errorf("deadline failure=%v; want the conservative incomplete-cost reservation to remain visible", err)
		}
	}
}

func TestClaudeClosingResultsReconcilePaidAssistantUsage(t *testing.T) {
	// The same paid events are followed by a real result here. Its cost and
	// turns are authoritative; observing tokens must neither reserve the
	// remaining budget nor double-count usage already included in that result.
	first := providerClaudeIncompletePaidAttempt()
	first.out += providerJSON(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true,
		"result": "API Error: 529 overloaded_error", "errors": []string{"API Error: 529 overloaded_error"},
		"total_cost_usd": .125, "num_turns": 2})
	first.stderr = ""
	second := providerClaudeIncompletePaidAttempt()
	second.out += providerJSON(map[string]any{"type": "result", "subtype": "success", "is_error": false,
		"result": "The provider recovered.", "total_cost_usd": .25, "num_turns": 2})
	second.stderr, second.exit = "", 0
	bin, dir := providerCLI(t, first, second)
	b := providerWithWait(ClaudeCode{Binary: bin, Model: "fixture-model", BudgetUSD: 1, MaxTurns: 9}, providerNoWait)
	u, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 5 * time.Second})
	if err != nil || providerCount(t, dir) != 2 || u.Outcome != "success" || u.Summary != "The provider recovered." {
		t.Fatalf("reported costs should permit recovery: usage=%+v error=%v attempts=%d", u, err, providerCount(t, dir))
	}
	if math.Abs(u.CostUSD-.375) > 1e-9 || u.Turns != 4 || u.ToolCalls["Read"] != 2 {
		t.Errorf("reported results must replace each attempt's provisional accounting: %+v; want $0.375, four turns and two reads", u)
	}
	remaining, parseErr := strconv.ParseFloat(providerFlag(t, dir, 2, "--max-budget-usd"), 64)
	if parseErr != nil || math.Abs(remaining-.875) > 1e-9 {
		t.Errorf("second attempt budget=%g (%v); want $0.875 from the first result", remaining, parseErr)
	}
}

func TestCodexProviderRetriesShareTheStepLimit(t *testing.T) {
	// The first attempt consumes two of three steps; the next consumes the
	// last. Both stop at capacity, but there is no allowance for a third CLI.
	first := providerCodexUsageAttempt(false, providerCapacity, "command_execution", "file_change")
	second := providerCodexUsageAttempt(false, providerCapacity, "command_execution")
	bin, dir := providerCLI(t, first, second, providerSuccess("codex"))
	waits := 0
	b := providerWithWait(Codex{Binary: bin, Home: t.TempDir(), MaxTurns: 3}, func(ctx context.Context, _ time.Duration) error {
		waits++
		return ctx.Err()
	})
	u, err := b.Run(providerContext(t), Job{Workspace: t.TempDir(), ReadOnly: true, Timeout: 5 * time.Second})
	if !errors.Is(err, ErrTooManyTurns) {
		t.Errorf("spent the run's steps: error=%v; want the existing step-limit error", err)
	}
	if attempts := providerCount(t, dir); attempts != 2 || waits != 1 {
		t.Errorf("attempts=%d waits=%d; want two attempts and one wait before exhausting the shared step limit", attempts, waits)
	}
	if u.Turns != 3 || u.ToolCalls["shell"] != 2 || u.ToolCalls["edit"] != 1 || u.Outcome == "success" {
		t.Errorf("step-limit usage=%+v; want the work from both attempts and a failed outcome", u)
	}
}
