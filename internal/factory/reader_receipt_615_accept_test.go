package factory

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/verify"
)

func receipt615JSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func receipt615Chart(t *testing.T) *statechart.Result {
	t.Helper()
	in := statechart.Input{
		Graph: tlc.GraphResult{
			Graph: tlc.Graph{
				States:  []tlc.GraphState{{ID: "a", Label: "n = 0", Values: map[string]string{"n": "0"}}, {ID: "b", Label: "n = 1", Values: map[string]string{"n": "1"}}},
				Initial: []string{"a"}, Steps: []tlc.GraphStep{{From: "a", Action: "Begin", To: "b"}, {From: "b", Action: "Reset", To: "a"}, {From: "a", Action: "Poll", To: "a"}, {From: "a", Action: "Poll", To: "a"}},
			}, StateCount: 2, StepCount: 4, Module: "Receipt615", Config: tlc.Config{Specification: "Spec", Constants: map[string]string{"Limit": "1"}},
			Tool: tlc.GraphTool{Image: "java@sha256:" + strings.Repeat("a", 64), JarSHA256: strings.Repeat("b", 64)},
			Run:  tlc.Result{Outcome: tlc.Passed, Version: "TLC2 Version 2.19", DistinctStates: 2, StatesGenerated: 5, Depth: 2},
		},
		Lock:       project.Lock{Bounds: map[string]string{"Limit": "1"}, Statements: []project.Statement{{Name: "Spec", Kind: project.Spec, Says: "Keep <exact> | `words` & spaces.\nSecond line.", SHA256: strings.Repeat("c", 64)}}},
		Invariants: map[string]map[string]bool{"a": {}, "b": {}},
	}
	view := `{"version":1,"state":[{"id":"ready","when":{"eq":[{"var":"n"},"0"]},"label":"Final ready <&> \"quoted\""},{"id":"busy","when":true,"label":"Final working"}],"step":[{"id":"poll","when":{"eq":[{"action":true},"Poll"]},"label":"Hidden poll","hide":true},{"id":"other","when":true,"label":"Next\n[*] --> forged #amp;"}]}`
	c, err := statechart.Compute(t.Context(), in, []byte(view))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func receipt615Report(t *testing.T, c *statechart.Result, passed bool) *verify.Report {
	t.Helper()
	// The wire format is the intended public API; serializing the reader is
	// presentation, not an assertion that this supplied fixture ran a verifier.
	body := map[string]any{
		"project": "receipt reader", "passed": passed, "assurance": "unchecked", "bounds": map[string]string{"Limit": "1"},
		"design": map[string]any{"passed": passed, "outcome": "passed", "spec": "Spec", "distinct_states": 2},
		"reader": map[string]any{"version": 1, "fingerprint": "sha256:" + strings.Repeat("d", 64),
			"inputs": map[string]any{"module": "Receipt615", "model_sha256": "sha256:" + strings.Repeat("e", 64), "view_sha256": "sha256:" + strings.Repeat("f", 64), "includes": map[string]string{}}, "chart": c},
	}
	var r verify.Report
	if err := json.Unmarshal(receipt615JSON(t, body), &r); err != nil {
		t.Fatal(err)
	}
	r.Fingerprint = verify.Fingerprint(r)
	return &r
}

func TestReader615FactoryReceiptAndPlumbing(t *testing.T) {
	chart := receipt615Chart(t)
	report := receipt615Report(t, chart, false)
	proposal := &formalize.Proposal{Reader: &formalize.Reader{Version: 1, Chart: &statechart.Result{States: []statechart.State{{ID: "stale", Label: "Stale proposal chart"}}}}}
	result := &synth.Result{Final: report}
	// Factory publication must use the final report even with an unrelated
	// saved proposal, and serialization/recovery must retain that choice.
	var recovered synth.Result
	if err := json.Unmarshal(receipt615JSON(t, result), &recovered); err != nil {
		t.Fatal(err)
	}
	body := pullRequestBody(Thread{}, Marker{}, &recovered, proposal, nil)
	for _, word := range []string{"stateDiagram-v2", "Final working", "❌ Fail", "the final gate failed"} {
		if !strings.Contains(body, word) {
			t.Fatalf("factory final receipt lacks %q: %s", word, body)
		}
	}
	if strings.Contains(body, "Stale proposal chart") || !strings.Contains(body, receipt.Markdown(recovered.Final)) {
		t.Fatal("factory substituted proposal evidence")
	}
	plain := planPullRequestBody(Thread{}, Marker{}, &plumbing.BuildResult{Summary: "All tests passed."}, nil)
	if strings.Contains(strings.ToLower(plain), "reader chart") || strings.Contains(plain, "stateDiagram-v2") || strings.Contains(plain, "reader.svg") {
		t.Fatal("plumbing acquired a modeled chart")
	}
}
