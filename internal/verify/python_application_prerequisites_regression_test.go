package verify

import (
	"strings"
	"testing"
)

// A complete exploration can still have a bad transition. Its successful
// process must not schedule a larger run, even beside a passing unit test.
func TestPythonApplicationGateSkipsLargerAfterBadTransition(t *testing.T) {
	tc, log := pa342Fake(t)
	f := pa342New(t, true, false)
	pa342Write(t, f.root, pa342Source, "from .step import next_count\n\ndef increment(count):\n    return next_count(count) if count != 1 else 3\n")
	r := pa342Unchanged(t, f, tc)
	x, c := pa342Explored(t, r)
	if !x.Success || !x.Complete || c.Result.Passed || r.Passed {
		t.Fatalf("expected a complete execution with a bad transition: execution=%+v conformance=%+v", x, c)
	}
	counts := counts849(t, log)
	if counts.LargerTLC != 0 || len(counts.Larger) != 0 || counts.Base["unit"] != 1 {
		t.Fatalf("wrong dependent or independent work: %+v", counts)
	}
	if len(r.Application.Larger) != 1 {
		t.Fatalf("missing skipped invocation: %+v", r.Application.Larger)
	}
	l := r.Application.Larger[0]
	if l.Execution != nil || l.Result.Required || l.Result.Passed || !strings.Contains(strings.Join(l.Skipped, "; "), "transition conformance") {
		t.Fatalf("bad transition did not skip the larger run: %+v", l)
	}
}
