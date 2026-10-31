package receipt

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/verify"
)

// An explored application's receipt says what exploration establishes,
// names its failed claims and records the run one size larger (#342); a
// sampled one keeps its own words.
func TestPythonApplicationExplorationReceipt(t *testing.T) {
	key := verify.PythonApplicationInvocationKey{Target: "existing", Check: "driver", Kind: "conformance", ID: "explore"}
	explored := &verify.Report{Project: "explored counter", Bounds: map[string]string{"Limit": "2"}, Application: &verify.PythonApplicationReport{
		Conformance: []verify.PythonApplicationConformance{{Key: key, Mode: "bounded-exhaustive", Result: conformance.Result{Passed: true, Exhaustive: true, States: 2, ModelStates: 3,
			Message: "the driver explores every state the code can reach, and it reached 2 of the model's 3",
			Tried:   &conformance.Tried{Attempts: 3, States: 2, Untried: `{<<"Done">>}`, In: "count = 0", Message: "the driver never tried a step that more than a bound rules out"}}}},
		Larger: []verify.PythonApplicationLarger{{Key: key, Bounds: map[string]string{"Limit": "3"}, Result: verify.Larger{Required: true, Bounds: map[string]string{"Limit": "3"}, States: 3, WantStates: 4}}},
	}}
	md := Markdown(explored)
	for _, want := range []string{"Python application · explored execution", "TLC exploration check", "One size larger: ", "Every state, <code>explore</code>", "Every step tried, <code>explore</code>", "&lt;&lt;\"Done\"&gt;&gt;"} {
		if !strings.Contains(md, want) {
			t.Errorf("explored receipt lacks %q:\n%s", want, md)
		}
	}
	sampled := &verify.Report{Project: "sampled counter", Bounds: map[string]string{"Limit": "2"}, Application: &verify.PythonApplicationReport{
		Conformance: []verify.PythonApplicationConformance{{Key: key, Result: conformance.Result{Passed: true, States: 3, ModelStates: 3}}}}}
	md = Markdown(sampled)
	if !strings.Contains(md, "Python application · sampled execution") || !strings.Contains(md, "TLC sampled check") || strings.Contains(md, "explored execution") || strings.Contains(md, "Every state,") {
		t.Errorf("sampled receipt changed:\n%s", md)
	}
}
