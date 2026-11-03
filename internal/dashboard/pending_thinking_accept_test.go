package dashboard_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/dashboard"
)

// This is the public contract consumed by the live cards. Decode the new
// field locally so the tests compile on the baseline and fail on behavior.
type pending438Card struct {
	dashboard.Live
	ThinkingPending bool `json:"thinkingPending"`
}

type pending438Fixture struct {
	server *dashboard.Server
	work   string
	status string
	since  time.Time
	now    time.Time
}

func newPending438Fixture(t *testing.T, steps ...dashboard.Step) *pending438Fixture {
	t.Helper()
	f := &pending438Fixture{work: t.TempDir(), since: time.Now().UTC().Add(-time.Minute), now: time.Now()}
	f.status = dashboard.StatusPath(f.work, "owner/app")
	f.setSteps(t, steps...)
	f.restart()
	return f
}

func (f *pending438Fixture) restart() {
	f.server = &dashboard.Server{Work: f.work, Repos: []*dashboard.Repo{{Name: "owner/app", Status: f.status}}}
}

func (f *pending438Fixture) setSteps(t *testing.T, steps ...dashboard.Step) {
	t.Helper()
	for i := range steps {
		steps[i].Since = f.since
		steps[i].Run = "build-438"
	}
	st := dashboard.Status{PID: os.Getpid(), Repo: "owner/app", Started: f.since, Heartbeat: f.since, Every: 30, Limit: 9000}
	st.Update(steps, f.since)
	if err := dashboard.WriteStatus(f.status, st); err != nil {
		t.Fatal(err)
	}
}

func (f *pending438Fixture) path(issue int) string {
	return filepath.Join(f.work, "owner", "app", "issues", fmt.Sprintf("issue-%d", issue), "build-438", "transcript.jsonl")
}

func (f *pending438Fixture) append(t *testing.T, issue int, text string) {
	t.Helper()
	p := f.path(issue)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := out.WriteString(text)
	closeErr := out.Close()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}

func (f *pending438Fixture) event(t *testing.T, issue int, event any) {
	t.Helper()
	b, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	f.append(t, issue, string(b)+"\n")
}

func (f *pending438Fixture) start(t *testing.T, issue int) {
	t.Helper()
	f.event(t, issue, map[string]any{"type": "invariant.workspace", "cwd": f.work})
	f.append(t, issue, `{"type":"turn.started"}`+"\n")
}

func (f *pending438Fixture) card(t *testing.T, issue int) pending438Card {
	t.Helper()
	// Cross the live endpoint's cache interval without sleeping.
	f.now = f.now.Add(3 * time.Second)
	b, err := f.server.LiveJSON(f.now)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"PRIVATE438", f.work} {
		if strings.Contains(string(b), secret) {
			t.Errorf("public live JSON exposed %q", secret)
		}
	}
	var body struct {
		Steps []pending438Card `json:"steps"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	for _, c := range body.Steps {
		if c.Issue == issue {
			return c
		}
	}
	t.Fatalf("live JSON has no card for issue %d: %s", issue, b)
	return pending438Card{}
}

func pending438Want(t *testing.T, c pending438Card, pending bool, count int, exact bool) {
	t.Helper()
	if c.ThinkingPending != pending || c.Thinking != count || c.ThinkingExact != exact {
		t.Errorf("issue %d thinking: pending=%v, count=%d, exact=%v; want pending=%v, count=%d, exact=%v",
			c.Issue, c.ThinkingPending, c.Thinking, c.ThinkingExact, pending, count, exact)
	}
}

func TestCodexThinkingIsPendingWhileStepsContinue(t *testing.T) {
	for _, agent := range []string{"codex", ""} {
		t.Run("watcher_agent_"+agent, func(t *testing.T) {
			f := newPending438Fixture(t, dashboard.Step{Issue: 438, Doing: "building", Agent: agent})
			if agent == "" {
				// The workspace line alone can identify a Codex transcript.
				f.start(t, 438)
			} else {
				// The watcher's agent also suffices without a workspace line.
				f.append(t, 438, `{"type":"turn.started"}`+"\n")
			}
			f.append(t, 438, `{"type":"item.completed","item":{"id":"step1","type":"command_execution","command":"go test ./... PRIVATE438","aggregated_output":"PRIVATE438 result"}}`+"\n")
			f.append(t, 438, `{"type":"item.started","item":{"id":"step2","type":"command_execution","command":"go test ./... PRIVATE438"}}`+"\n")
			c := f.card(t, 438)
			pending438Want(t, c, true, 0, false)
			if c.Turns != 1 || len(c.Marks) != 2 || c.Now == "" || c.Agent != "" || c.Ended != nil {
				t.Errorf("pending thinking obscured the run's activity: %+v", c)
			}
			pending438Want(t, f.card(t, 438), true, 0, false)
			f.restart()
			pending438Want(t, f.card(t, 438), true, 0, false)

			// A failed agent, or a watcher-recorded end without a final event,
			// must not promise that its report is still on the way.
			f.append(t, 438, `{"type":"turn.failed","error":{"message":"PRIVATE438"}}`+"\n")
			pending438Want(t, f.card(t, 438), false, 0, false)
		})
	}
	f := newPending438Fixture(t, dashboard.Step{Issue: 438, Doing: "building", Agent: "codex"})
	f.start(t, 438)
	pending438Want(t, f.card(t, 438), true, 0, false)
	ended := f.since.Add(time.Minute)
	f.setSteps(t, dashboard.Step{Issue: 438, Doing: "building", Agent: "codex", Ended: &ended})
	c := f.card(t, 438)
	pending438Want(t, c, false, 0, false)
	if c.Ended == nil {
		t.Error("watcher-recorded end was lost")
	}
}

func TestCodexPendingThinkingBecomesReportedUsage(t *testing.T) {
	for _, reported := range []int{2625, 0, -1} {
		t.Run(fmt.Sprintf("reported_%d", reported), func(t *testing.T) {
			f := newPending438Fixture(t, dashboard.Step{Issue: 438, Doing: "building", Agent: "codex"})
			f.start(t, 438)
			f.append(t, 438, `{"type":"item.completed","item":{"id":"answer","type":"agent_message","text":"PRIVATE438"}}`+"\n")
			pending438Want(t, f.card(t, 438), true, 0, false)
			usage := map[string]int{"output_tokens": 7000}
			if reported >= 0 {
				usage["reasoning_output_tokens"] = reported
			}
			completion, err := json.Marshal(map[string]any{"type": "turn.completed", "usage": usage})
			if err != nil {
				t.Fatal(err)
			}
			cut := len(completion) / 2
			f.append(t, 438, string(completion[:cut]))
			pending438Want(t, f.card(t, 438), true, 0, false)
			f.append(t, 438, string(completion[cut:])+"\n")
			want, exact := max(reported, 0), reported >= 0
			c := f.card(t, 438)
			pending438Want(t, c, false, want, exact)
			if c.Agent != "finished" || c.Output != 7000 || len(c.Marks) != 0 {
				t.Errorf("completed usage should finish the agent without inventing timeline marks: %+v", c)
			}
			pending438Want(t, f.card(t, 438), false, want, exact)
			f.restart()
			pending438Want(t, f.card(t, 438), false, want, exact)
			f.append(t, 438, `{"type":"turn.started"}`+"\n")
			pending438Want(t, f.card(t, 438), false, want, exact)
			f.append(t, 438, `{"type":"turn.completed","usage":{"reasoning_output_tokens":19}}`+"\n")
			pending438Want(t, f.card(t, 438), false, want+19, true)
			ended := f.since.Add(time.Minute)
			f.setSteps(t, dashboard.Step{Issue: 438, Doing: "building", Agent: "codex", Ended: &ended})
			pending438Want(t, f.card(t, 438), false, want+19, true)
		})
	}
}

func TestCodexPendingThinkingBecomesAnEstimate(t *testing.T) {
	for _, reasoning := range []string{"", "PRIVATE438 " + strings.Repeat("x", 390)} {
		t.Run(fmt.Sprintf("text_length_%d", len(reasoning)), func(t *testing.T) {
			f := newPending438Fixture(t, dashboard.Step{Issue: 438, Doing: "building", Agent: "codex"})
			f.start(t, 438)
			pending438Want(t, f.card(t, 438), true, 0, false)
			b, err := json.Marshal(map[string]any{"type": "item.completed", "item": map[string]any{
				"id": "reason", "type": "reasoning", "text": reasoning,
			}})
			if err != nil {
				t.Fatal(err)
			}
			f.append(t, 438, string(b))
			pending438Want(t, f.card(t, 438), true, 0, false)
			f.append(t, 438, "\n")
			// Preserve the existing estimate from the serialized text length.
			raw, err := json.Marshal(reasoning)
			if err != nil {
				t.Fatal(err)
			}
			want := len(raw) / 4
			c := f.card(t, 438)
			pending438Want(t, c, false, want, false)
			if len(c.Marks) != 1 || c.Marks[0].Kind != "think" || c.Marks[0].Tokens != want {
				t.Errorf("reasoning did not retain its estimated thinking stretch: %+v", c.Marks)
			}
			f.restart()
			pending438Want(t, f.card(t, 438), false, want, false)

			// A bounded timeline is not evidence that no reasoning ever arrived.
			var tools strings.Builder
			for i := 0; i < 2100; i++ {
				fmt.Fprintf(&tools, "{\"type\":\"item.completed\",\"item\":{\"id\":\"tool%d\",\"type\":\"mcp_tool_call\",\"server\":\"invariant\",\"tool\":\"check\"}}\n", i)
			}
			f.append(t, 438, tools.String())
			pending438Want(t, f.card(t, 438), false, want, false)
			// Fallback/repair sessions share the transcript's accumulated counts.
			f.start(t, 438)
			pending438Want(t, f.card(t, 438), false, want, false)
			f.append(t, 438, `{"type":"turn.completed","usage":{"output_tokens":12}}`+"\n")
			pending438Want(t, f.card(t, 438), false, want, false)
			before := f.card(t, 438)
			f.append(t, 438, `{"type":"turn.started"}`+"\n"+`{"type":"turn.completed","usage":{"reasoning_output_tokens":7}}`+"\n")
			after := f.card(t, 438)
			pending438Want(t, after, false, 7, true)
			if !reflect.DeepEqual(before.Marks, after.Marks) {
				t.Error("exact usage changed the existing timeline")
			}
		})
	}
}

func TestThinkingPendingBelongsToItsCodexTranscript(t *testing.T) {
	f := newPending438Fixture(t,
		dashboard.Step{Issue: 438, Doing: "building", Agent: "codex"},
		dashboard.Step{Issue: 439, Doing: "building", Agent: "codex"},
		dashboard.Step{Issue: 440, Doing: "building", Agent: "claude-code"},
	)
	f.start(t, 438)
	f.start(t, 439)
	f.event(t, 440, map[string]any{"type": "system", "subtype": "init", "cwd": f.work})
	pending438Want(t, f.card(t, 438), true, 0, false)
	pending438Want(t, f.card(t, 439), true, 0, false)
	pending438Want(t, f.card(t, 440), false, 0, false)
	f.append(t, 440, `{"type":"system","subtype":"thinking_tokens","estimated_tokens_delta":150}`+"\n")
	f.append(t, 438, `{"type":"turn.completed","usage":{"reasoning_output_tokens":2625}}`+"\n")
	pending438Want(t, f.card(t, 438), false, 2625, true)
	pending438Want(t, f.card(t, 439), true, 0, false)
	pending438Want(t, f.card(t, 440), false, 150, false)
	f.append(t, 440, `{"type":"system","subtype":"thinking_tokens","estimated_tokens_delta":400}`+"\n")
	f.append(t, 440, `{"type":"result","subtype":"success","usage":{"output_tokens":9000}}`+"\n")
	pending438Want(t, f.card(t, 440), false, 550, false)

	// A shorter replacement is a new transcript, not the last run's total.
	if err := os.WriteFile(f.path(438), []byte(`{"type":"turn.started"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pending438Want(t, f.card(t, 438), true, 0, false)
	pending438Want(t, f.card(t, 439), true, 0, false)
	pending438Want(t, f.card(t, 440), false, 550, false)
	f.restart()
	pending438Want(t, f.card(t, 438), true, 0, false)
	pending438Want(t, f.card(t, 440), false, 550, false)
}
