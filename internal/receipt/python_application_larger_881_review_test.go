package receipt

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

// larger881Report is a report as the gate writes one for a run one size
// larger that failed: the message names the invocation, and conformance
// evidence is there only when the run's attempts were checked (#881).
func larger881Report(explicit, checked bool, states int64, depth int, said string) *verify.Report {
	key := verify.PythonApplicationInvocationKey{Target: "existing", Check: "driver", Kind: "conformance", ID: "explore"}
	bounds := map[string]string{"Limit": "3"}
	l := verify.Larger{Required: true, Bounds: bounds, States: states, Depth: depth, WantStates: 4, WantDepth: 4,
		Message: "target existing, check driver, invocation explore: " + said}
	record := verify.PythonApplicationLarger{Key: key, Bounds: bounds, Result: l,
		Execution: &verify.PythonApplicationExecutionResult{Key: key, Success: true, Complete: true}}
	if checked {
		record.Conformance = &verify.PythonApplicationConformance{Key: key, Bounds: bounds, Mode: "bounded-exhaustive", Result: conformance.Result{States: 4, ModelStates: 4}}
	} else {
		// A receipt from before the attempts were checked names no invocation.
		l.Message = said
		record.Result = l
	}
	r := &verify.Report{Project: "counter", Bounds: map[string]string{"Limit": "2"}, Larger: &l, Build: verify.Build{Passed: true},
		Application: &verify.PythonApplicationReport{Larger: []verify.PythonApplicationLarger{record}}}
	if explicit {
		r.Components = &verify.ComponentEvidence{Inventory: project.ComponentInventory{Mode: "explicit"}}
	}
	return r
}

func larger881Row(t *testing.T, md string) string {
	t.Helper()
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "| One size larger |") {
			return line
		}
	}
	t.Fatalf("no row for the check one size larger:\n%s", md)
	return ""
}

// A legacy receipt's row set the code's counts beside the model's as what
// differed. Where the counts agree and the attempts are what failed, it says
// what the gate said. Receipts without that evidence keep their row.
func TestLegacyReceiptSaysWhatFailedOneSizeLarger881(t *testing.T) {
	const step = "conformance one size larger: the code took a step the model doesn't allow; Inc from count = 2 to count = 4"
	row := larger881Row(t, Markdown(larger881Report(false, true, 4, 4, step)))
	if strings.Contains(row, "differ") || strings.Contains(row, "where the model has") {
		t.Errorf("equal counts are shown as what differed: %s", row)
	}
	if want := "| One size larger | ❌ code and model disagree one size larger | target existing, check driver, invocation explore: " + step + " |"; row != want {
		t.Errorf("row = %s\nwant  %s", row, want)
	}
	// What the gate said stays in its cell, whatever a recorded value holds.
	row = larger881Row(t, Markdown(larger881Report(false, true, 4, 4, "conformance one size larger: a run starts in a state Init doesn't allow: name = \"a|b\"\nand more")))
	if !strings.HasSuffix(row, "Init doesn't allow: name = \"a&#124;b\" |") {
		t.Errorf("row = %s", row)
	}
	const fewer = "conformance one size larger: the driver explores every state the code can reach, and it reached 3 of the model's 4"
	if row, want := larger881Row(t, Markdown(larger881Report(false, true, 3, 3, fewer))), "| One size larger | ❌ code and model differ, so the code carries a bound | 3 states, depth 3, where the model has 4, depth 4, within <code>Limit = 3</code> |"; row != want {
		t.Errorf("counts that differ lost their row: %s", row)
	}
	// Old receipts: counts alone, whatever they are.
	for _, states := range []int64{3, 4} {
		before := larger881Report(false, false, states, int(states), "one size larger, the code reaches 3 states in 3 levels, and the model reaches 4 in 4")
		want := fmt.Sprintf("| One size larger | ❌ code and model differ, so the code carries a bound | %d states, depth %d, where the model has 4, depth 4, within <code>Limit = 3</code> |", states, states)
		if row := larger881Row(t, Markdown(before)); row != want {
			t.Errorf("a receipt without attempt evidence changed its row: %s", row)
		}
	}
}

// larger881Failed is the list under What failed.
func larger881Failed(t *testing.T, md string) []string {
	t.Helper()
	_, after, ok := strings.Cut(md, "**What failed**\n\n")
	if !ok {
		t.Fatalf("the receipt says nothing failed:\n%s", md)
	}
	var out []string
	for _, line := range strings.Split(after, "\n") {
		if !strings.HasPrefix(line, "- ") {
			break
		}
		out = append(out, line)
	}
	return out
}

// What failed says a larger failure once, and names its invocation once.
func TestReceiptStatesALargerFailureOnce881(t *testing.T) {
	const step = "the code took a step the model doesn't allow; Inc from count = 2 to count = 4"
	for _, explicit := range []bool{true, false} {
		failed := larger881Failed(t, Markdown(larger881Report(explicit, true, 4, 4, "conformance one size larger: "+step)))
		want := "- Python conformance one size larger, target <code>existing</code>, check <code>driver</code>, invocation <code>explore</code>: <code>conformance one size larger: " + step + "</code>"
		if len(failed) != 1 || failed[0] != want || strings.Count(failed[0], step) != 1 || strings.Count(failed[0], "invocation") != 1 {
			t.Errorf("explicit=%v: want the failure and its invocation once:\n%s\nwant\n%s", explicit, strings.Join(failed, "\n"), want)
		}
		// A receipt from before the attempts were checked keeps its one line.
		failed = larger881Failed(t, Markdown(larger881Report(explicit, false, 3, 3, "one size larger, the code reaches 3 states in 3 levels, and the model reaches 4 in 4")))
		want = "- One size larger: one size larger, the code reaches 3 states in 3 levels, and the model reaches 4 in 4"
		if !explicit {
			want += ". The explorer names its bounds, so the code must hold at every size, and something in it stops at the bounds."
		}
		if len(failed) != 1 || failed[0] != want {
			t.Errorf("explicit=%v: a receipt without attempt evidence changed what failed:\n%s", explicit, strings.Join(failed, "\n"))
		}
	}
}
