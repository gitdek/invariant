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
