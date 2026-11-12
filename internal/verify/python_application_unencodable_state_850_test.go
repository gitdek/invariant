package verify

import (
	"context"
	"strings"
	"testing"
	"time"
)

// An application whose state can't be written as a TLA+ value fails its
// conformance check, and the gate still returns its report (#850). With
// explicit components the component row is failed, names the invocation,
// run, step, side and field that couldn't be written, and holds no
// observations. #837 made that row refuse such a state outright, which
// left the person with an error and no receipt.
func TestUnencodableStateStillGivesAReceipt850(t *testing.T) {
	const why = "module observation cannot encode as TLA+: 0.5 isn't an integer"
	for _, explicit := range []bool{true, false} {
		r := UnencodableStateReport850(t, explicit)
		if r.Passed || r.Application == nil || len(r.Application.Conformance) != 1 || r.Application.Conformance[0].Result.Passed ||
			!strings.Contains(r.Application.Conformance[0].Result.Message, why) {
			t.Fatalf("explicit=%v: the report doesn't fail on the state it couldn't write: %+v", explicit, r.Application)
		}
		if !explicit {
			continue
		}
		if r.Components == nil || len(r.Components.Conformance) != 1 {
			t.Fatalf("the report holds no component conformance row: %+v", r.Components)
		}
		row := r.Components.Conformance[0]
		diagnostics := strings.Join(row.Result.Diagnostics, "\n")
		if row.Status == "tested" || row.TransitionsChecked || row.Result.Outcome != "failed" || len(row.Result.Observations) != 0 {
			t.Errorf("a state that couldn't be written left the row %s with %d observations", row.Status, len(row.Result.Observations))
		}
		for _, want := range []string{row.Target, row.Check, "invocation explore", "run 1 step ", "before", `field "count"`, why} {
			if !strings.Contains(diagnostics, want) {
				t.Errorf("the row's diagnostics don't say %q:\n%s", want, diagnostics)
			}
		}
	}
}

// UnencodableStateReport850 is the gate's report on the hold fixture with a
// float in its state, shared with the receipt's test.
func UnencodableStateReport850(t *testing.T, explicit bool) *Report {
	t.Helper()
	source := strings.Replace(hold393Source, "counts = {'value': 0, 'active': 0, 'prepared': False}", "counts = {'value': 0.5, 'active': 0, 'prepared': False}", 1)
	source = strings.Replace(source, "counts.update(value=0, active=0, prepared=False)", "counts.update(value=0.5, active=0, prepared=False)", 1)
	// The driver states no expectations of its own, so the gate is what
	// meets the state.
	driver := strings.NewReplacer(", expected_before=state, expected_after=expected", "", ", expected_before=state, expected_after=state", "",
		"expected_before=state, expected_after=expected", "").Replace(hold393Driver)
	if !strings.Contains(source, "0.5") || strings.Contains(driver, "expected_") {
		t.Fatal("the fixture moved")
	}
	tc, _ := hold393GateFake(t)
	f := hold393Fixture(t, explicit, source, driver)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	r, err := Run(ctx, f.p.Dir, "", tc)
	if err != nil || r == nil {
		t.Fatalf("explicit=%v: the gate returned no report: %v", explicit, err)
	}
	return r
}
