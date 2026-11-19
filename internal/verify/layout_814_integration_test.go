//go:build integration

package verify

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/toolchain"
)

// The issue's two layouts on the pinned TLC (#814). Listed, Inv is
// (FALSE => FALSE) /\ FALSE, which TLC finds is FALSE. Joined on one line,
// it's FALSE => (FALSE /\ FALSE), and TLC finds no error, with the same pin.
// The same holds of an invariant TLC checks state by state, which it finds
// violated. Against a lock ratified with the listed layout, the gate fails
// the joined one on its layout alone, and names the line. A lock ratified
// before layout pins, not migrated, still verifies as it did, and says its
// layout isn't pinned yet.
func TestLayoutPinsTellTheIssuesLayoutsApartIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err) // Integration runs must not silently skip.
	}
	stateLevel := func(s string) string {
		return strings.NewReplacer("/\\ FALSE => FALSE", "/\\ x = 1 => FALSE", "/\\ FALSE\n====", "/\\ x = 1\n====").Replace(s)
	}
	for _, c := range []struct {
		name           string
		listed, joined string
		refused        func(Design) bool
	}{
		{"the issue's", layoutListed814, layoutJoined814, func(d Design) bool {
			// Inv names no variable, so TLC evaluates it once, before
			// exploring, and stops on its being FALSE.
			return !d.Passed && strings.Contains(d.Message, "The invariant of Inv is equal to FALSE")
		}},
		{"state by state", stateLevel(layoutListed814), stateLevel(layoutJoined814), func(d Design) bool {
			return d.Outcome == "violated" && d.Violated == "Inv"
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.listed == c.joined || !strings.Contains(c.joined, "Inv == /\\ ") {
				t.Fatalf("the layouts aren't the issue's:\n%s\n%s", c.listed, c.joined)
			}
			listed := layoutProject814(t, c.listed, false)
			r, err := RunModel(ctx, listed, tc)
			if err != nil {
				t.Fatal(err)
			}
			if !c.refused(r.Design) {
				t.Fatalf("the pinned TLC reads the listed layout as %s (%s): %s", r.Design.Outcome, r.Design.Violated, r.Design.Message)
			}
			for _, pin := range r.Pins {
				if !pin.Match || pin.Layout == nil || !pin.Layout.Match {
					t.Errorf("the listed layout against its own lock: %+v, layout %+v", pin, pin.Layout)
				}
			}

			// The joined layout, against the lock ratified with the listed
			// one, as a synthesis gate runs it, with the ratified project
			// beside it.
			ratified := layoutProject814(t, c.listed, false)
			rewrite814(t, listed, c.joined)
			r, err = RunModel(ctx, listed, tc, Options{Ratified: ratified})
			if err != nil {
				t.Fatal(err)
			}
			if !r.Design.Passed || r.Design.Outcome != "passed" {
				t.Fatalf("the pinned TLC reads the joined layout as %s: %s; want no error", r.Design.Outcome, r.Design.Message)
			}
			inv := r.Pins[1]
			if !inv.Match || inv.Layout == nil || inv.Layout.Match {
				t.Fatalf("the joined layout's pin is %+v, layout %+v; want its pin to match and its layout pin not to", inv, inv.Layout)
			}
			if r.Passed || strings.Join(Failed(r), ", ") != "layout Inv" {
				t.Fatalf("the gate passed %v, failing %v; want it to fail on Inv's layout alone", r.Passed, Failed(r))
			}
			if mv := inv.Layout.Moved; mv == nil || mv.Definition != "Inv" || mv.Line != 7 || mv.Ratified != "Inv ==" {
				t.Errorf("the gate names the move as %+v; want Inv's line 7, which was `Inv ==`", mv)
			}

			// A lock ratified before layout pins, not migrated, passes
			// the joined layout as it did before them.
			legacy := layoutProject814(t, c.listed, true)
			rewrite814(t, legacy, c.joined)
			r, err = RunModel(ctx, legacy, tc)
			if err != nil {
				t.Fatal(err)
			}
			if !r.Passed {
				t.Fatalf("a lock without layout pins fails: %v", Failed(r))
			}
			for _, pin := range r.Pins {
				if !pin.Match || pin.Layout == nil || pin.Layout.Want != "" || !pin.LayoutHolds() {
					t.Errorf("a lock without layout pins: %+v, layout %+v", pin, pin.Layout)
				}
			}
		})
	}
}
