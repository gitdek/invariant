package replaytest

import (
	"context"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/verify"
)

func TestCommandReplayConcurrentInvocations913(t *testing.T) {
	tc, _, tmp := fake913(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	type result struct {
		report *verify.Report
		err    error
	}
	done := make(chan result, 3)
	for _, route := range []string{"typescript", "existing", "python"} {
		dir := fixture913(t, route, "fixed")
		go func() {
			r, err := verify.Run(ctx, dir, "", tc, verify.Options{TLCParallel: 2})
			done <- result{r, err}
		}()
	}
	seen := map[string]bool{}
	var results []result
	for range 3 {
		results = append(results, <-done)
	}
	for _, got := range results {
		if got.err != nil || got.report == nil {
			t.Errorf("concurrent replay: %v", got.err)
			continue
		}
		assertReport913(t, got.report, "concurrent", "fixed")
		for _, bug := range got.report.Bugs {
			id := bug.Replay.Request.InvocationID
			if seen[id] {
				t.Errorf("concurrent gates reused invocation %s", id)
			}
			seen[id] = true
		}
	}
	if len(seen) != 6 {
		t.Errorf("got %d independent invocations, want six", len(seen))
	}
	clean913(t, tmp)
}
