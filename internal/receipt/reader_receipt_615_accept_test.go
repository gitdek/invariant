package receipt_test

import (
	"bytes"
	"encoding/json"
	"html"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/statechart"
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
func TestReader615ReceiptRoundTrip(t *testing.T) {
	c := receipt615Chart(t)
	r := receipt615Report(t, c, false)
	before := receipt615JSON(t, r)
	var object map[string]json.RawMessage
	if err := json.Unmarshal(before, &object); err != nil {
		t.Fatal(err)
	}
	if len(object["reader"]) == 0 {
		t.Fatal("receipt JSON dropped structured chart data")
	}
	var decoded verify.Report
	if err := json.Unmarshal(before, &decoded); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, receipt615JSON(t, &decoded)) {
		t.Fatal("report JSON changed after decoding")
	}
	md := receipt.Markdown(&decoded)
	if !strings.Contains(md, "❌ Fail") || !strings.Contains(md, "stateDiagram-v2") || !strings.Contains(md, "adds no proof claim") {
		t.Fatalf("receipt lost verdict, Mermaid or assurance boundary: %s", md)
	}
	visible := html.UnescapeString(md)
	for _, word := range []string{c.Report.Rules[0].Says, "Unavailable", "Hidden steps", "TLC states", "TLC steps", "Generated states", "Limit", "1", "Poll", "Reset", "Begin"} {
		if !strings.Contains(visible, word) {
			t.Errorf("reader receipt omits %q", word)
		}
	}
	// Topology comes from the checked result. A label resembling an edge must
	// remain text inside one line, without inventing a Mermaid edge.
	start := strings.Index(md, "```mermaid\n")
	if start < 0 {
		t.Fatal("missing Mermaid fence")
	}
	diagram, _, ok := strings.Cut(md[start+len("```mermaid\n"):], "\n```")
	if !ok {
		t.Fatal("unterminated Mermaid")
	}
	decode := func(s string) string {
		return regexp.MustCompile(`#([0-9]+);`).ReplaceAllStringFunc(s, func(x string) string { n, _ := strconv.Atoi(x[1 : len(x)-1]); return string(rune(n)) })
	}
	aliases := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^\s*state "(.*)" as ([A-Za-z0-9_]+)$`).FindAllStringSubmatch(diagram, -1) {
		aliases[m[2]] = decode(m[1])
	}
	if len(aliases) != len(c.States) {
		t.Fatal("Mermaid changed states")
	}
	got := map[string]int{}
	for _, m := range regexp.MustCompile(`(?m)^\s*([A-Za-z0-9_]+) --> ([A-Za-z0-9_]+): (.*)$`).FindAllStringSubmatch(diagram, -1) {
		got[aliases[m[1]]+"\x00"+decode(m[3])+"\x00"+aliases[m[2]]]++
	}
	labels := map[string]string{}
	for _, s := range c.States {
		labels[s.ID] = s.Label
	}
	want := map[string]int{}
	for _, a := range c.Arrows {
		want[labels[a.From]+"\x00"+a.Label+"\x00"+labels[a.To]]++
	}
	if !reflect.DeepEqual(got, want) || strings.Count(diagram, "[*] -->") != len(c.Initial) {
		t.Fatalf("Mermaid changed topology: %v, want %v", got, want)
	}
	delete(object, "reader")
	var plain verify.Report
	if err := json.Unmarshal(receipt615JSON(t, object), &plain); err != nil {
		t.Fatal(err)
	}
	if verify.Fingerprint(plain) != verify.Fingerprint(*r) || r.Passed || r.Claim() != plain.Claim() {
		t.Fatal("reader upgraded assurance or changed evidence identity")
	}
	if !bytes.Equal(before, receipt615JSON(t, r)) || receipt.Markdown(r) != md {
		t.Fatal("rendering changed the report")
	}
}
func TestReader615LegacyReceipt(t *testing.T) {
	goldens := []string{"sha256:12362a3a9c164fea85ae6ea6ff4bf623d16ed3136adfbbdb211ea8161007453a", "sha256:8d0e0ac4ecf82433ffb6f0971b45a14bafc96fa10407766f3bcae0b76dc50fda", "sha256:295f04994adf44460b659ba6ded42792ceab506707fae3516ef90e7fab2d475e"}
	for i, old := range receiptAssuranceFixtures() {
		var r verify.Report
		if err := json.Unmarshal(receipt615JSON(t, old), &r); err != nil {
			t.Fatal(err)
		}
		if verify.Fingerprint(r) != goldens[i] {
			t.Fatal("legacy verification evidence fingerprint changed")
		}
		md := strings.ToLower(receipt.Markdown(&r))
		if !strings.Contains(md, "no reader chart") || !strings.Contains(md, "view") || strings.Contains(md, "statediagram-v2") {
			t.Fatalf("old receipt does not explicitly lack a reader chart: %s", md)
		}
	}
}
