package receipt

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

// A component receipt says what the check one size larger did, in its table
// and in words (#880). Its headline can read Pass where that check claimed
// nothing: the exploration didn't finish, TLC found a problem at that size,
// every invocation was skipped, or no check ran. Before, the only trace was
// a JSON object in the application's section, and nothing at all where no
// check ran.
func TestComponentReceiptSaysWhatTheLargerCheckDid880(t *testing.T) {
	key := verify.PythonApplicationInvocationKey{Target: "existing", Check: "driver", Kind: "conformance", ID: "explore"}
	sibling := verify.PythonApplicationInvocationKey{Target: "existing", Check: "driver", Kind: "conformance", ID: "sibling"}
	bounds := map[string]string{"Limit": "3"}
	explored := []verify.PythonApplicationConformance{{Key: key, Mode: "bounded-exhaustive",
		Result: conformance.Result{Passed: true, Exhaustive: true, States: 3, ModelStates: 3, Tried: &conformance.Tried{Passed: true, Attempts: 3, States: 3}}}}
	sampled := []verify.PythonApplicationConformance{{Key: key, Result: conformance.Result{Passed: true, States: 3, ModelStates: 3}}}
	skip := func(k verify.PythonApplicationInvocationKey) verify.PythonApplicationLarger {
		return verify.PythonApplicationLarger{Key: k, Bounds: bounds, Skipped: []string{"base exploration transition conformance did not pass"}, Result: verify.Larger{Bounds: bounds}}
	}
	for _, c := range []struct {
		name        string
		conformance []verify.PythonApplicationConformance
		overall     *verify.Larger
		each        []verify.PythonApplicationLarger
		row, words  string // "" where nothing is shown
	}{
		{"agreement", explored, &verify.Larger{Passed: true, Required: true, Bounds: bounds, States: 4, Depth: 4, WantStates: 4, WantDepth: 4}, nil,
			"| One size larger | ✅ the code reaches as many states as the model, as deep | 4 states, depth 4, within <code>Limit = 3</code> |",
			"One size larger: ✅ the code reaches as many states as the model, as deep. 4 states, depth 4, within <code>Limit = 3</code>."},
		{"a bound the code carries", explored, &verify.Larger{Required: true, Bounds: bounds, States: 3, Depth: 3, WantStates: 4, WantDepth: 4}, nil,
			"| One size larger | ❌ code and model differ, so the code carries a bound | 3 states, depth 3, where the model has 4, depth 4, within <code>Limit = 3</code> |",
			"One size larger: ❌ code and model differ, so the code carries a bound."},
		{"an exploration that didn't finish", explored, &verify.Larger{Bounds: bounds, Message: "the driver didn't finish exploring one size larger within 6m0s"}, nil,
			"| One size larger | ➖ not claimed | the driver didn't finish exploring one size larger within 6m0s |",
			"One size larger: ➖ not claimed. the driver didn't finish exploring one size larger within 6m0s."},
		{"a problem TLC found there", explored, &verify.Larger{Bounds: bounds, Message: "TLC found a problem one size larger: Safe | Bounded is violated\nState 1: count = 4"}, nil,
			"| One size larger | ➖ not claimed | TLC found a problem one size larger: Safe ", ""},
		{"every invocation skipped", explored, nil, []verify.PythonApplicationLarger{skip(key)},
			"| One size larger | ➖ not claimed | skipped for <code>explore</code>: <code>base exploration transition conformance did not pass</code> |",
			"One size larger: ➖ not claimed. skipped for <code>explore</code>: <code>base exploration transition conformance did not pass</code>."},
		{"one invocation skipped beside one that agreed", explored, &verify.Larger{Passed: true, Required: true, Bounds: bounds, States: 4, Depth: 4, WantStates: 4, WantDepth: 4},
			[]verify.PythonApplicationLarger{{Key: key, Bounds: bounds, Result: verify.Larger{Passed: true, Required: true, Bounds: bounds, States: 4, Depth: 4}}, skip(sibling)},
			"within <code>Limit = 3</code>; skipped for <code>sibling</code>: <code>base exploration transition conformance did not pass</code> |", ""},
		{"no check at all", explored, nil, nil,
			"| One size larger | ➖ not checked | no check ran one size larger, which takes an exhaustive manifest and a driver that names a constant for each bound |",
			"One size larger: ➖ not checked. no check ran one size larger"},
		{"an application that samples its states", sampled, nil, nil, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			md := Markdown(&verify.Report{Project: "counter", Bounds: map[string]string{"Limit": "2"}, Larger: c.overall,
				Components:  &verify.ComponentEvidence{Inventory: project.ComponentInventory{Mode: "explicit"}},
				Application: &verify.PythonApplicationReport{Conformance: c.conformance, Larger: c.each}})
			if c.row == "" {
				if strings.Contains(md, "| One size larger |") || strings.Contains(md, "\nOne size larger: ➖") || strings.Contains(md, "\nOne size larger: ✅") {
					t.Fatalf("a receipt with no such check speaks of one:\n%s", md)
				}
				return
			}
			if !strings.Contains(md, c.row) || !strings.Contains(md, c.words) {
				t.Fatalf("want the row %q and the words %q:\n%s", c.row, c.words, md)
			}
			// The table's row stays one row of three cells whatever the message
			// holds: a | in it is escaped, and only its first line is kept.
			for _, line := range strings.Split(md, "\n") {
				if strings.HasPrefix(line, "| One size larger |") && (strings.Count(line, " | ") != 2 || strings.Contains(line, "State 1")) {
					t.Errorf("the row isn't one row of three cells: %q", line)
				}
			}
		})
	}

	// A receipt with no component evidence keeps its own row, and gains no
	// second account of the same check.
	legacy := Markdown(&verify.Report{Project: "counter", Bounds: map[string]string{"Limit": "2"},
		Larger:      &verify.Larger{Bounds: bounds, Message: "the driver didn't finish exploring one size larger within 6m0s"},
		Application: &verify.PythonApplicationReport{Conformance: explored}})
	if strings.Count(legacy, "| One size larger | ➖ not claimed |") != 1 || strings.Contains(legacy, "\nOne size larger: ➖") {
		t.Errorf("a legacy receipt's account of the check changed:\n%s", legacy)
	}
}

func TestPythonLargerConformanceReceipt881(t *testing.T) {
	key := verify.PythonApplicationInvocationKey{Target: "counter", Check: "driver", Kind: "conformance", ID: "explore"}
	bounds := map[string]string{"Limit": "3"}
	for _, explicit := range []bool{false, true} {
		for _, failure := range []string{"", "Inc from count = 2 to count = 4", "Init doesn't allow count = -1", "Done was not tried in count = 3"} {
			passed := failure == ""
			l := verify.Larger{Passed: passed, Required: true, Bounds: bounds, States: 4, Depth: 4, WantStates: 4, WantDepth: 4}
			if !passed {
				l.Message = "conformance one size larger: " + failure
			}
			c := &verify.PythonApplicationConformance{Key: key, Bounds: bounds, Mode: "bounded-exhaustive", Result: conformance.Result{Passed: passed, States: 4, ModelStates: 4}}
			r := &verify.Report{Project: "counter", Passed: passed, Larger: &l, Application: &verify.PythonApplicationReport{
				Larger: []verify.PythonApplicationLarger{{Key: key, Bounds: bounds, Result: l, Conformance: c, Execution: &verify.PythonApplicationExecutionResult{Key: key, Success: true, Complete: true}}}}}
			if explicit {
				r.Components = &verify.ComponentEvidence{Inventory: project.ComponentInventory{Mode: "explicit"}}
			}
			md := Markdown(r)
			if passed {
				if !strings.Contains(md, "the attempts conform and every step was tried, with matching state count and depth") {
					t.Fatalf("new evidence has only count wording: %s", md)
				}
				continue
			}
			_, failed, ok := strings.Cut(md, "**What failed**")
			failed = strings.SplitN(failed, "\n##", 2)[0]
			if !ok || !strings.Contains(failed, componentText(failure)) || !strings.Contains(failed, "explore") || !strings.Contains(failed, "larger") {
				t.Fatalf("failure left to JSON: %s", md)
			}
		}
	}
}
