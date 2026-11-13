package replaytest

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/verify"
)

// The contract takes a result whose values are the path's, however an
// adapter's JSON spells them (#857): here every integer of the result and
// of the observation is padded, each differently. Comparing the records as
// they were decoded would refuse this replay, which is the path exactly.
// A padded number that's another number is still another state.
func TestAReplayResultIsTakenByItsValues857(t *testing.T) {
	dir, tc, _ := gateFixture857(t, "typescript", []string{"current"}, false)
	r := run857(t, dir, tc, true)
	if r.Bugs[0].Replay == nil || r.Bugs[0].Replay.Request == nil {
		t.Fatal("no prepared request")
	}
	q := *r.Bugs[0].Replay.Request
	runner := tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pad := func(state tlc.ReplayState, zeros string) int {
		padded := 0
		for name, v := range state {
			if v.Kind == "integer" {
				v.Atom = zeros + v.Atom
				state[name] = v
				padded++
			}
		}
		return padded
	}
	result, observed := successfulResult857(t, q)
	padded := pad(result.Initial, "00") + pad(observed.Initial, "0")
	for i := range result.Calls {
		padded += pad(result.Calls[i].Before, "0") + pad(result.Calls[i].After, "000")
		padded += pad(observed.Calls[i].Before, "00") + pad(observed.Calls[i].After, "0")
	}
	if padded < 6 {
		t.Fatalf("the fixture's path has too few integers to respell: %d", padded)
	}
	got, err := verify.CheckBugReplay(ctx, runner, filepath.Join(dir, ".invariant/specs"), q, result, observed)
	if err != nil || !got.Passed || got.Status != "refused" {
		t.Fatalf("a result that spells the path's numbers another way is refused: %+v, %v", got, err)
	}
	// Another number is another state, padded or not.
	wrong, wrongObserved := successfulResult857(t, q)
	for name, v := range wrong.Initial {
		if v.Kind == "integer" {
			v.Atom = "007"
			wrong.Initial[name], wrongObserved.Initial[name] = v, v
		}
	}
	if got, err := verify.CheckBugReplay(ctx, runner, filepath.Join(dir, ".invariant/specs"), q, wrong, wrongObserved); err == nil || got.Passed {
		t.Fatalf("a result that starts in another state is taken: %+v", got)
	}
}
