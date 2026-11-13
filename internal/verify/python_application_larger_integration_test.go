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

// Evidence the pinned TLC can't read one size larger fails, as it does at the
// ratified bounds. A double can only repeat what TLC is taken to say, so
// these run TLC itself. The counter's modules extend Naturals, so SANY
// rejects the plan's own start of -1; a boolean stops Next's evaluation; a
// number past TLC's integers ends it with its general status. Each ends with
// the status and words the gate reads, so a pinned release that says
// otherwise shows here.
func TestPythonApplicationLargerUnreadableEvidenceTLC881(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, source, module string
		exit                 int
		reason               string
	}{
		{"wrong start", "", "Invariant_ConformanceInit", 150, "Could not find declaration or definition of symbol '-.'"},
		{"step to True", "def increment(count):\n    return count + 1 if count < 2 else True\n", "Invariant_Conformance", 75, "Attempted to check equality of integer 3 with non-integer: TRUE"},
		{"step to 2**40", "def increment(count):\n    return count + 1 if count < 2 else 2**40\n", "Invariant_Conformance", 255, "TLC can't handle a number this big. 1099511627776"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := pa342New(t, true, false)
			if c.source == "" {
				f = largerFixture881(t, "wrong start", true)
			} else {
				pa342Write(t, f.root, pa342Source, c.source)
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
			if l.Result.States != 4 || l.Result.Depth != 4 || l.Result.WantStates != 4 || l.Result.WantDepth != 4 {
				t.Fatalf("the fixture must match TLC's count and depth: %+v", l.Result)
			}
			if r.Passed || !l.Result.Required || l.Result.Passed || !r.Larger.Required || r.Larger.Passed {
				t.Fatalf("evidence TLC can't read claimed nothing instead of failing: passed=%v %+v", r.Passed, l.Result)
			}
			conformance := l.Conformance
			if conformance == nil || conformance.OperationalError || conformance.passed() {
				t.Fatalf("larger conformance: %+v", conformance)
			}
			found := false
			for _, check := range conformance.Checks {
				found = found || check.Module == c.module && check.Result.Outcome == tlc.Failed && check.Error == "" && check.Result.ExitCode == c.exit && check.Result.Version != ""
			}
			if !found {
				t.Fatalf("pinned TLC didn't end %s with status %d: %+v", c.module, c.exit, conformance.Checks)
			}
			want := "invocation " + l.Key.ID + ": conformance one size larger: TLC can't check what the run recorded: "
			for _, message := range []string{l.Result.Message, r.Larger.Message} {
				if !strings.Contains(message, want) || !strings.Contains(message, c.reason) || strings.ContainsAny(message, "\r\n") {
					t.Fatalf("want the invocation and TLC's reason on one line, got %q", message)
				}
			}
		})
	}
}
