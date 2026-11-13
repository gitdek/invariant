//go:build integration

package verify

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

// Both counters pass at the ratified bounds. Only TLC's larger transition
// check distinguishes the faulty counter: its four states have four levels too.
func TestPythonApplicationLargerCounterTLC881(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []bool{false, true} {
		name := "correct counter"
		if bad {
			name = "equal count wrong step"
		}
		t.Run(name, func(t *testing.T) {
			f := pa342New(t, true, false)
			if bad {
				pa342Write(t, f.root, pa342Source, "def increment(count):\n    return count + (1 if count < 2 else 2)\n")
			}
			r, err := Run(ctx, f.p.Dir, "", tc)
			if err != nil || r == nil {
				t.Fatalf("gate: %v", err)
			}
			_, base := pa342Explored(t, r)
			if !base.passed() || len(r.Application.Larger) != 1 || r.Larger == nil {
				t.Fatalf("missing passing base or larger result: %s", Feedback(r))
			}
			l := r.Application.Larger[0]
			if r.Passed == bad || l.Result.Passed == bad || !l.Result.Required || l.Result.States != 4 || l.Result.Depth != 4 || l.Result.WantStates != 4 || l.Result.WantDepth != 4 {
				t.Fatalf("equal counts must not hide a bad transition: %+v", l.Result)
			}
			c := l.Conformance
			if c == nil || c.OperationalError || c.passed() == bad || c.Bounds["Limit"] != "3" {
				t.Fatalf("larger conformance: %+v", c)
			}
			if bad {
				if c.Result.BadStep == nil || c.Result.BadStep.From != "count = 2" || c.Result.BadStep.To != "count = 4" || !strings.Contains(l.Result.Message, "Inc") {
					t.Fatalf("larger counterexample: %+v", c.Result)
				}
				found := false
				for _, check := range c.Checks {
					found = found || check.Module == "Invariant_Conformance" && check.Result.Outcome == tlc.Deadlock && check.Error == "" && check.Config.Constants["Limit"] == "3"
				}
				if !found {
					t.Fatal("missing pinned TLC transition counterexample")
				}
			}
		})
	}
}
