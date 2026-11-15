package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

// Each dashboard Bug adds application_replays, the matching application's
// per-invocation {key,evidence} records. Cards and the evidence page present
// each key/status/diagnostic as escaped text; a model's shared graph remains
// independent of project execution evidence. Keep the existing browser
// escaping tests and add a browser regression for multiple Python invocations.
func TestDashboardPythonApplicationReplays935(t *testing.T) {
	const raw = `{"passed":false,"bugs":[{"name":"ReplayCompletedTask","label":"replay-completed-task","caught":true,"violated":"Safe","code_behavior":"current","replay":{"required":true,"status":"failed","passed":false,"message":"one invocation did not dispatch"}}],"application":{"executions":[],"conformance":[],"bug_replays":[{"key":{"target":"python:app#advance@app.py","check":"python:driver#drive@driver.py","kind":"conformance","id":"first-935"},"evidence":{"required":true,"status":"refused","passed":true,"request":{"id":"request-935","bug":{"name":"ReplayCompletedTask"}},"result":{},"observation":{"completed":true}}},{"key":{"target":"python:app#advance@app.py","check":"python:driver#drive@driver.py","kind":"conformance","id":"second-935"},"evidence":{"required":true,"status":"failed","passed":false,"request":{"id":"request-935","bug":{"name":"ReplayCompletedTask"}},"message":"<dispatch-935> missing call"}}]}}`
	var r verify.Report
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	p := projectOf(projectSource{key: "delivery", manifest: project.Manifest{Language: "python", Existing: []string{"app.py"}}}, &r)
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Bugs []struct {
			Name    string            `json:"name"`
			Replays []json.RawMessage `json:"application_replays"`
		} `json:"bugs"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Bugs) != 1 || len(wire.Bugs[0].Replays) != 2 {
		t.Fatalf("dashboard collapsed actual invocation outcomes: %s", b)
	}
	for _, want := range []string{"first-935", "second-935", "refused", "failed", "dispatch-935"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("dashboard lost %q: %s", want, b)
		}
	}
	if p.Passed || p.Bugs[0].ReplayStatus.Replayed || len(p.Coverage) != 0 {
		t.Fatal("dashboard granted gate success or sampled coverage from replay")
	}
	statement := project.Statement{Name: "ReplayCompletedTask", Kind: project.Bug, Expect: "Safe", CodeBehavior: "current"}
	proposal := &formalize.Proposal{Draft: formalize.Draft{Name: "delivery", Language: "python", Statements: []project.Statement{{Name: "Safe", Kind: project.Invariant}, statement}}, Target: &formalize.Target{Manifest: project.Manifest{Language: "python", Existing: []string{"app.py"}}}}
	w := waitingFor(factory.Marker{Kind: factory.KindProposal, Proposal: proposal}, time.Now())
	if w == nil || len(w.Unreplayed) != 0 {
		t.Fatalf("supported Python replay is advertised as unavailable: %+v", w)
	}
}
