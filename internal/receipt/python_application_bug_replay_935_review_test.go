package receipt

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

// A bug replay's execution is listed with the ordinary run of the
// invocation it replays, under the same key. Its line must say it's a
// replay, and of which bug: a replay that reproduced the bug completes,
// and its failure isn't the ordinary run failing (#935's review).
func TestPythonApplicationBugReplayExecutionsAreNamed935(t *testing.T) {
	key := verify.PythonApplicationInvocationKey{Target: "python:app#advance@app.py", Check: "python:driver#drive@driver.py", Kind: "conformance", ID: "first-935"}
	replay := func(bug string) *verify.PythonApplicationBugReplay {
		return &verify.PythonApplicationBugReplay{Request: verify.BugReplayRequest{Version: 1, Bug: project.Statement{Name: bug, Kind: project.Bug, CodeBehavior: "current"}}}
	}
	r := &verify.Report{Project: "delivery", Application: &verify.PythonApplicationReport{Executions: []verify.PythonApplicationExecutionResult{
		{Key: key, ObservationMode: "sampled", Complete: true, Success: true},
		{Key: key, ObservationMode: "bug-replay", Complete: true, Success: true, BugReplay: replay("ReplayCompletedTask")},
		{Key: key, ObservationMode: "bug-replay", HarnessError: "replay-missing-dispatch: ReuseName", BugReplay: replay("ReuseRecoveredName")},
	}}}
	md := Markdown(r)
	for _, want := range []string{
		"\nInvocation <code>first-935</code> · <code>conformance</code>; complete: true; successful: true; exit: 0.\n",
		"\nInvocation <code>first-935</code> · <code>conformance</code> · bug replay of <code>ReplayCompletedTask</code>; complete: true; successful: true; exit: 0.\n",
		"\nInvocation <code>first-935</code> · <code>conformance</code> · bug replay of <code>ReuseRecoveredName</code>; complete: false; successful: false; exit: 0.\n",
		"Python bug replay <code>ReuseRecoveredName</code>, target " + componentLiteral(key.Target) + ", check " + componentLiteral(key.Check) + ", invocation <code>first-935</code>: <code>replay-missing-dispatch: ReuseName</code>",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("receipt doesn't say %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "Python execution, target") {
		t.Errorf("a failed replay reads as the ordinary run failing:\n%s", md)
	}
}
