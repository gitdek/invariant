package receipt

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/verify"
)

func TestPythonApplicationBugReplayReceipt935(t *testing.T) {
	// Replay checks live beside actual execution, separately from conformance.
	// Both keys must be visible even when the top-level bug summarizes failure.
	const raw = `{"project":"delivery","passed":false,"bugs":[{"name":"ReplayCompletedTask","label":"replay-completed-task","caught":true,"violated":"Safe","code_behavior":"current","replay":{"required":true,"status":"failed","passed":false,"message":"second invocation failed"}}],"application":{"executions":[],"conformance":[],"bug_replays":[{"key":{"target":"python:app#advance@app.py","check":"python:driver#drive@driver.py","kind":"conformance","id":"first-935"},"evidence":{"required":true,"status":"refused","passed":true,"request":{"version":1,"id":"request-935","invocation_id":"fresh-935-a","bug":{"name":"ReplayCompletedTask","kind":"bug","code_behavior":"current"},"path":{"model_sha256":"model-935","bounds":{"Token":"9007199254740993"},"tool":{"image":"java-935","jar_sha256":"jar-935"}}},"result":{"version":1,"outcome":"refused"},"observation":{"started":true,"completed":true,"exit_code":0}}},{"key":{"target":"python:app#advance@app.py","check":"python:driver#drive@driver.py","kind":"conformance","id":"second-935"},"evidence":{"required":true,"status":"failed","passed":false,"message":"<script>missing dispatch</script> | [click](https://evil.invalid)","request":{"version":1,"id":"request-935","invocation_id":"fresh-935-b","bug":{"name":"ReplayCompletedTask","kind":"bug","code_behavior":"current"}}}}]}}`
	var r verify.Report
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bug_replays", "first-935", "second-935", "9007199254740993", "model-935", "java-935", "jar-935"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("application replay JSON discarded %q: %s", want, b)
		}
	}
	md := Markdown(&r)
	for _, want := range []string{"ReplayCompletedTask", "first-935", "second-935", "refused", "failed", "missing dispatch", "model-935", "9007199254740993"} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown lost replay evidence %q: %s", want, md)
		}
	}
	for _, bad := range []string{"<script>", "[click](https://evil.invalid)"} {
		if strings.Contains(md, bad) {
			t.Errorf("untrusted replay diagnostic became active Markdown: %s", md)
		}
	}
	if r.Passed || r.Bugs[0].ReplayStatus().Replayed || len(r.Coverage()) != 0 {
		t.Fatal("display or deterministic replay granted gate success or sampled coverage")
	}
}
