package receipt

import (
	"encoding/json"
	"html"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/verify"
)

func TestDeclaredRefusalReceipt(t *testing.T) {
	// Read through the public JSON contract to require round-trip retention,
	// not a particular Go struct layout. The integers must remain exact.
	var x verify.PythonApplicationExecutionResult
	raw := `{"success":true,"complete":true,"observation_mode":"explored","exploration":{"runs":2,"states":1,"depth":1,"attempts":2,"refusals":1,"declared_refusals":1,"omitted":0,"steps":[{"name":"Read","args":[[]],"refusal":{"type":"app.Refused","attributes":{"status_code":404,"token":9007199254740993}}}]},"observations":[{"kind":"transition","step":"Read","args":[],"called":false,"replay":false,"before":{"slot":0},"after":{"slot":0},"outcome":"refused","refusal":{"type":"app.Refused","attributes":{"status_code":404,"token":9007199254740993}}}]}`
	if err := json.Unmarshal([]byte(raw), &x); err != nil {
		t.Fatal(err)
	}
	r := &verify.Report{Project: "slot", Application: &verify.PythonApplicationReport{
		Executions:  []verify.PythonApplicationExecutionResult{x},
		Conformance: []verify.PythonApplicationConformance{{Mode: "bounded-exhaustive", Result: conformance.Result{Passed: true}}},
	}}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"declared_refusals":1`, `"outcome":"refused"`, `"type":"app.Refused"`, `"status_code":404`, `"token":9007199254740993`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("JSON receipt lost %s: %s", want, b)
		}
	}
	md := html.UnescapeString(Markdown(r))
	for _, want := range []string{"app.Refused", "9007199254740993", "404", "declared_refusals"} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown lost %s: %s", want, md)
		}
	}
	lower := strings.ToLower(md)
	for _, want := range []string{"named", "disabled", "normal"} {
		if !strings.Contains(lower, want) {
			t.Errorf("receipt must explain strict named-action refusal and normal-return checks (%s): %s", want, md)
		}
	}
	if strings.Contains(md, "every step against Next") {
		t.Fatal("receipt still describes strict steps as checked only against Next")
	}
	sampled := &verify.Report{Project: "sampled", Application: &verify.PythonApplicationReport{Conformance: []verify.PythonApplicationConformance{{Result: conformance.Result{Passed: true}}}}}
	sm := Markdown(sampled)
	if !strings.Contains(sm, "Python application · sampled execution") || strings.Contains(sm, "declared_refusals") || strings.Contains(strings.ToLower(sm), "disabled named") {
		t.Fatal("sampled receipt changed")
	}
}
