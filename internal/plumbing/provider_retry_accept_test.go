package plumbing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/synth"
)

type providerReviewProbe struct{ calls int }

func (*providerReviewProbe) Name() string { return "fixture" }
func (p *providerReviewProbe) Run(context.Context, synth.Job) (synth.Usage, error) {
	p.calls++
	return synth.Usage{Outcome: "success", Summary: "VERDICT: approve"}, nil
}

func TestPlumbingBuildFailsWhenItsProviderNeverFinishes(t *testing.T) {
	// The existing checkout fixture supplies a fake sandbox whose checks pass
	// for Hello. This isolates the run failure from an unrelated test failure.
	root := loopyCheckout(t)
	bin := filepath.Join(t.TempDir(), "codex")
	script := `#!/bin/sh
mkdir -p page
printf '%s\n' 'package page' 'func Hello() string { return "hello" }' > page/page.go
printf '%s\n' '{"type":"turn.failed","error":{"message":"Selected model is at capacity. Please try a different model."}}'
exit 1
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	reviewer := &providerReviewProbe{}
	b := Builder{Backend: synth.Codex{Binary: bin, Home: t.TempDir()}, Reviewer: reviewer,
		Binary: "invariant", TestRuns: 4, Timeout: 300 * time.Millisecond, FallbackEffort: "xhigh",
		Sandbox: Sandbox{Image: "golang", ModCache: t.TempDir()}}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	r, err := b.Build(ctx, root, 7, filepath.Join(t.TempDir(), "build"))
	if err != nil || r == nil {
		t.Fatalf("build result=%+v, error=%v; want a recorded failed build", r, err)
	}
	if r.Passed || r.Approved || reviewer.calls != 0 || r.Fallback != nil {
		t.Errorf("an exhausted provider was treated as a finished build or thinking-loop fallback: passed=%v approved=%v reviews=%d fallback=%+v", r.Passed, r.Approved, reviewer.calls, r.Fallback)
	}
	for _, want := range []string{"provider", "unavailable", "Selected model is at capacity."} {
		if !strings.Contains(r.Summary, want) {
			t.Errorf("build summary lost %q: %s", want, r.Summary)
		}
	}
}

func TestPlumbingPaidRetryDeadlineCannotPublishPassingLeftovers(t *testing.T) {
	root := loopyCheckout(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := `#!/bin/sh
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
n=0
[ ! -f "$here/count" ] || n=$(cat "$here/count")
n=$((n + 1))
printf '%s\n' "$n" > "$here/count"
if [ "$n" -eq 1 ]; then
  printf '%s\n' '{"type":"result","subtype":"error_during_execution","is_error":true,"result":"API Error: 529 overloaded_error","errors":["API Error: 529 overloaded_error"],"total_cost_usd":0.125,"num_turns":1}'
  exit 1
fi
if [ "$n" -eq 2 ]; then
  mkdir -p page
  printf '%s\n' 'package page' 'func Hello() string { return "hello" }' > page/page.go
  printf '%s\n' '{"type":"system","subtype":"init","model":"fixture-model","mcp_servers":[{"name":"invariant","status":"connected"}]}'
  printf '%s\n' '{"type":"assistant","message":{"id":"paid-write-before-deadline","role":"assistant","model":"fixture-model","content":[{"type":"tool_use","id":"write-page","name":"Write","input":{"file_path":"page/page.go"}}],"stop_reason":"tool_use","usage":{"input_tokens":120,"output_tokens":40}}}'
  touch "$here/paid-started"
  exec sleep 30
fi
printf '%s\n' '{"type":"result","subtype":"success","result":"Done.","num_turns":1,"total_cost_usd":0.125}'
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	// These leftovers pass the existing sandbox fixture. Observe any
	// invocation so a cancelled run cannot even begin checking or reviewing
	// them, then copy them into the checkout a factory could publish.
	checkLog := filepath.Join(dir, "sandbox-runs")
	t.Setenv("PROVIDER_DEADLINE_SANDBOX_LOG", checkLog)
	docker := strings.Replace(loopyDocker, "#!/bin/sh\n", "#!/bin/sh\n[ \"$1\" != run ] || printf 'run\\n' >> \"$PROVIDER_DEADLINE_SANDBOX_LOG\"\n", 1)
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(docker), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	waits := 0
	var backend synth.Backend = synth.ClaudeCode{Binary: bin, Model: "fixture-model", BudgetUSD: 1, MaxTurns: 9}
	if c, ok := backend.(interface {
		WithProviderWait(func(context.Context, time.Duration) error) synth.Backend
	}); ok {
		backend = c.WithProviderWait(func(ctx context.Context, _ time.Duration) error {
			waits++
			return ctx.Err()
		})
	}
	reviewer := &providerReviewProbe{}
	b := Builder{Backend: backend, Reviewer: reviewer, Binary: "invariant", TestRuns: 4,
		Timeout: time.Second, FallbackEffort: "xhigh", Sandbox: Sandbox{Image: "golang", ModCache: t.TempDir()}}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	r, err := b.Build(ctx, root, 7, filepath.Join(t.TempDir(), "build"))
	if err != nil || r == nil {
		t.Fatalf("build result=%+v, error=%v; want a recorded failed build", r, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "paid-started")); err != nil {
		t.Fatalf("the paid retry never wrote the passing leftover before its deadline: %v", err)
	}
	count, readErr := os.ReadFile(filepath.Join(dir, "count"))
	if readErr != nil || strings.TrimSpace(string(count)) != "2" || waits != 1 {
		t.Errorf("attempt count=%q (%v), waits=%d; want two attempts and one wait", count, readErr, waits)
	}
	if r.Passed || r.Approved || reviewer.calls != 0 || r.Fallback != nil {
		t.Errorf("a deadline-interrupted paid retry passed, reviewed, or fell back: passed=%v approved=%v reviews=%d fallback=%+v", r.Passed, r.Approved, reviewer.calls, r.Fallback)
	}
	if _, err := os.Stat(checkLog); !os.IsNotExist(err) {
		t.Errorf("the factory checked files from a deadline-interrupted paid retry: sandbox log error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "page", "page.go")); !os.IsNotExist(err) {
		t.Errorf("the unfinished retry's leftover was copied into the publishable checkout: %v", err)
	}
	for _, want := range []string{context.DeadlineExceeded.Error(), "provider", "unavailable", "API Error: 529 overloaded_error"} {
		if !strings.Contains(r.Summary, want) {
			t.Errorf("the recorded build failure lost %q: %s", want, r.Summary)
		}
	}
	if r.Usage.CostUSD != 1 || r.Spend != 1 || r.Usage.Turns != 2 || r.Usage.ToolCalls["Write"] != 1 {
		t.Errorf("the failed build lost its reserved usage: usage=%+v spend=%g; want $1, two turns and one write", r.Usage, r.Spend)
	}
}

type providerHelloBuilder struct{}

func (providerHelloBuilder) Name() string { return "fixture" }
func (providerHelloBuilder) Run(_ context.Context, job synth.Job) (synth.Usage, error) {
	if err := os.MkdirAll(filepath.Join(job.Workspace, "page"), 0o755); err != nil {
		return synth.Usage{}, err
	}
	err := os.WriteFile(filepath.Join(job.Workspace, "page", "page.go"), []byte("package page\nfunc Hello() string { return \"hello\" }\n"), 0o644)
	return synth.Usage{Outcome: "success"}, err
}

func TestPlumbingReviewsRetryAndReportProviderExhaustion(t *testing.T) {
	for _, succeeds := range []bool{true, false} {
		t.Run(fmt.Sprintf("eventually succeeds=%v", succeeds), func(t *testing.T) {
			root := loopyCheckout(t)
			dir := t.TempDir()
			bin := filepath.Join(dir, "codex")
			script := `#!/bin/sh
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ -f "$here/attempted" ] && [ SUCCEEDS = true ]; then
  printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"VERDICT: approve"}}' '{"type":"turn.completed"}'
  exit 0
fi
touch "$here/attempted"
printf '%s\n' '{"type":"turn.failed","error":{"message":"Selected model is at capacity. Please try a different model."}}'
exit 1
`
			script = strings.ReplaceAll(script, "SUCCEEDS", fmt.Sprint(succeeds))
			if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			timeout := 30 * time.Second
			if !succeeds {
				timeout = 300 * time.Millisecond
			}
			var reviewer synth.Backend = synth.Codex{Binary: bin, Home: t.TempDir()}
			// Keep the one-minute production wait short here. The type
			// assertion lets the base branch run and fail behaviorally too.
			if c, ok := reviewer.(interface {
				WithProviderWait(func(context.Context, time.Duration) error) synth.Backend
			}); ok {
				reviewer = c.WithProviderWait(func(ctx context.Context, delay time.Duration) error {
					timer := time.NewTimer(delay / 12)
					defer timer.Stop()
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-timer.C:
						return nil
					}
				})
			}
			b := Builder{Backend: providerHelloBuilder{}, Reviewer: reviewer,
				Binary: "invariant", TestRuns: 4, Timeout: timeout, Sandbox: Sandbox{Image: "golang", ModCache: t.TempDir()}}
			ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
			defer cancel()
			r, err := b.Build(ctx, root, 7, filepath.Join(t.TempDir(), "build"))
			if err != nil || r == nil || !r.Passed {
				t.Fatalf("fixture build failed: %+v, %v", r, err)
			}
			if r.Approved != succeeds {
				t.Errorf("review approved=%v, want %v: %s", r.Approved, succeeds, r.Review)
			}
			if !succeeds {
				for _, want := range []string{"provider", "unavailable", "Selected model is at capacity."} {
					if !strings.Contains(r.Review, want) {
						t.Errorf("review failure lost %q: %s", want, r.Review)
					}
				}
			}
		})
	}
}
