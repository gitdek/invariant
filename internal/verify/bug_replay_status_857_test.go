package verify

import (
	"errors"
	"strings"
	"testing"
)

// What a receipt and the dashboard say of a bug's replay is derived from
// its evidence, and says no more than the evidence shows (#857). A current
// bug nothing replayed reads the same whether it has no replay evidence or
// evidence that says so, and a replay isn't called complete, or refused,
// without the request, the result and the observation that show it.
func TestABugsReplayLabelSaysWhatItsEvidenceShows857(t *testing.T) {
	whole := func(status string, passed bool) *BugReplayEvidence {
		return &BugReplayEvidence{Required: true, Status: status, Passed: passed, Request: &BugReplayRequest{}, Result: &BugReplayResult{}, Observation: &BugReplayObservation{}}
	}
	for _, tc := range []struct {
		name     string
		bug      Bug
		status   string
		label    string
		required bool
		replayed bool
	}{
		{"a bug an older lock left unclassified", Bug{}, "not-classified", "not replayed: not classified", false, false},
		{"a hypothetical bug", Bug{CodeBehavior: "hypothetical"}, "hypothetical", "not replayed: hypothetical", false, false},
		{"a current bug with no evidence", Bug{CodeBehavior: "current"}, "not-replayed", "current; not replayed", false, false},
		{"a current bug Go's own checks cover", Bug{CodeBehavior: "current", Replay: &BugReplayEvidence{Status: "not-replayed"}}, "not-replayed", "current; not replayed", false, false},
		{"a path that's prepared", Bug{CodeBehavior: "current", Replay: &BugReplayEvidence{Required: true, Status: "pending", Request: &BugReplayRequest{}}}, "pending", "current; pending", true, false},
		{"a replay nothing can run", Bug{CodeBehavior: "current", Replay: &BugReplayEvidence{Required: true, Status: "replay-unavailable"}}, "replay-unavailable", "current; replay-unavailable", true, false},
		{"a replay that ran", Bug{CodeBehavior: "current", Replay: whole("complete", true)}, "complete", "current; complete", true, true},
		{"a refusal that was checked", Bug{CodeBehavior: "current", Replay: whole("refused", true)}, "refused", "current; refused", true, true},
		{"a replay that reproduced the bug", Bug{CodeBehavior: "current", Replay: whole("reproduced", false)}, "reproduced", "current; reproduced", true, false},
		{"a status of complete with nothing behind it", Bug{CodeBehavior: "current", Replay: &BugReplayEvidence{Required: true, Status: "complete", Passed: true}}, "incomplete", "current; incomplete", true, false},
		{"a status of refused that didn't pass", Bug{CodeBehavior: "current", Replay: whole("refused", false)}, "incomplete", "current; incomplete", true, false},
		{"a status of complete with no observation", Bug{CodeBehavior: "current", Replay: &BugReplayEvidence{Required: true, Status: "complete", Passed: true, Request: &BugReplayRequest{}, Result: &BugReplayResult{}}}, "incomplete", "current; incomplete", true, false},
	} {
		got := tc.bug.ReplayStatus()
		if got.Status != tc.status || got.Label != tc.label || got.Required != tc.required || got.Replayed != tc.replayed {
			t.Errorf("%s reads %+v; want status %q, label %q, required %v, replayed %v", tc.name, got, tc.status, tc.label, tc.required, tc.replayed)
		}
	}
}

// Why a path couldn't be prepared goes into a receipt, which is published.
// It's the refusal's first line, with no directory of the machine that ran
// it, and short enough for a table.
func TestAReplayRefusalsMessageIsFitToPublish857(t *testing.T) {
	const dir = "/private/var/folders/zz/T/invariant-model-1234"
	for _, tc := range []struct{ name, in, want string }{
		{"a refusal as it's written", "replay-unsupported-action: Pick binds x, which is also a constant or a model value; rename one", "replay-unsupported-action: Pick binds x, which is also a constant or a model value; rename one"},
		{"the model's own directory", "replay-check-failed: " + dir + "/Replay_Bug.tla: error", "replay-check-failed: <model>/Replay_Bug.tla: error"},
		{"another directory", "replay-check-failed: cannot read /Users/someone/work/tla2tools.jar", "replay-check-failed: cannot read <path>"},
		{"TLC's output after the first line", "replay-check-failed: Replay_Bug: error\nTLC2 Version 2.19\nStarting... (/tmp/x/y)", "replay-check-failed: Replay_Bug: error"},
		{"a message that starts on its second line", "\n  replay-check-failed: Replay_Bug: error", "replay-check-failed: Replay_Bug: error"},
		{"a URL", "replay-check-failed: see https://example.com/tlaplus/issues for /opt/tlc/lib/x.jar", "replay-check-failed: see https://example.com/tlaplus/issues for <path>"},
		{"a path in parentheses", "replay-check-failed: Starting... (/tmp/x/y)", "replay-check-failed: Starting... (<path>)"},
		{"TLA+ in the message", `replay-invalid-counterexample: expected a complete Safe violation of x' = x + 1 /\ y \in S`, `replay-invalid-counterexample: expected a complete Safe violation of x' = x + 1 /\ y \in S`},
	} {
		if got := replayMessage(errors.New(tc.in), dir); got != tc.want {
			t.Errorf("%s: kept %q; want %q", tc.name, got, tc.want)
		}
	}
	long := replayMessage(errors.New("replay-check-failed: "+strings.Repeat("é", 600)), dir)
	if len(long) > 400+len("…") || !strings.HasSuffix(long, "…") || strings.ContainsRune(long, '�') {
		t.Errorf("a long message is kept at %d bytes, ending %q", len(long), long[max(0, len(long)-8):])
	}
}
