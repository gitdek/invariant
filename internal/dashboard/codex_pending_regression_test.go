package dashboard

import (
	"os"
	"testing"
)

// A session that failed without reporting thinking can try again in the
// same transcript. Only a completed reasoning item or turn supplies the
// distinction that survives the start of another session.
func TestCodexThinkingPendingAcrossUnreportedSessions(t *testing.T) {
	f := newLiveStepFixture(t)
	out, err := os.Create(f.transcript)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	workspace := ev(t, map[string]any{"type": "invariant.workspace", "cwd": f.ws})
	for _, test := range []struct {
		name, line string
		pending    bool
		agent      string
	}{
		{"workspace", workspace, true, ""},
		{"turn starts", `{"type":"turn.started"}`, true, ""},
		{"reasoning starts", `{"type":"item.started","item":{"id":"reason","type":"reasoning","text":"not complete"}}`, true, ""},
		{"reasoning updates", `{"type":"item.updated","item":{"id":"reason","type":"reasoning","text":"still not complete"}}`, true, ""},
		{"malformed completion", `{"type":"turn.completed","usage":`, true, ""},
		{"failed turn", `{"type":"turn.failed"}`, false, "stopped"},
		{"retry session", workspace, true, ""},
		{"retry starts", `{"type":"turn.started"}`, true, ""},
		{"null usage", `{"type":"turn.completed","usage":{"reasoning_output_tokens":null}}`, false, "finished"},
		{"later session", workspace, false, ""},
		{"later turn", `{"type":"turn.started"}`, false, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := out.WriteString(test.line + "\n"); err != nil {
				t.Fatal(err)
			}
			v := f.one(t)
			if v.ThinkingPending != test.pending || v.Thinking != 0 || v.ThinkingExact || v.Agent != test.agent {
				t.Errorf("pending %v, thinking %d, exact %v, agent %q; want pending %v, zero estimated tokens, agent %q",
					v.ThinkingPending, v.Thinking, v.ThinkingExact, v.Agent, test.pending, test.agent)
			}
			if len(v.Marks) != 0 {
				t.Errorf("incomplete reasoning made timeline marks: %+v", v.Marks)
			}
		})
	}
}
