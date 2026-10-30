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

// Exercise the JSON consumed by the cards, using transcript files and watcher
// status just as the running dashboard does. No private parser state is read.
type thinking239Card struct {
	dashboard.Live
	ThinkingExact bool `json:"thinkingExact"`
}

type thinking239Fixture struct {
	server *dashboard.Server
	work   string
	status string
	since  time.Time
	now    time.Time
}

func newThinking239Fixture(t *testing.T, steps ...dashboard.Step) *thinking239Fixture {
	t.Helper()
	f := &thinking239Fixture{
		work: t.TempDir(), since: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC), now: time.Now(),
	}
	f.status = dashboard.StatusPath(f.work, "gitdek/app")
	f.setSteps(t, steps...)
	f.restart()
	return f
}

func (f *thinking239Fixture) restart() {
	f.server = &dashboard.Server{Work: f.work, Repos: []*dashboard.Repo{{Name: "gitdek/app", Status: f.status}}}
}

func (f *thinking239Fixture) setSteps(t *testing.T, steps ...dashboard.Step) {
	t.Helper()
	for i := range steps {
		steps[i].Since = f.since
		steps[i].Run = filepath.Base(filepath.Dir(f.path(steps[i].Issue)))
	}
	if err := dashboard.WriteStatus(f.status, dashboard.Status{
		PID: os.Getpid(), Repo: "gitdek/app", Started: f.since, Heartbeat: f.since,
		Every: 30, Limit: 9000, Steps: steps,
	}); err != nil {
		t.Fatal(err)
	}
}

func (f *thinking239Fixture) path(issue int) string {
	return filepath.Join(f.work, "gitdek", "app", "issues", fmt.Sprintf("issue-%d", issue), "build-20261029-120000", "transcript.jsonl")
}

func (f *thinking239Fixture) append(t *testing.T, issue int, text string) {
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

func (f *thinking239Fixture) line(t *testing.T, issue, second int, event map[string]any) {
	t.Helper()
	event["timestamp"] = f.since.Add(time.Duration(second) * time.Second).Format(time.RFC3339Nano)
	b, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	f.append(t, issue, string(b)+"\n")
}

func (f *thinking239Fixture) cards(t *testing.T) map[int]thinking239Card {
	t.Helper()
	// Advance beyond the public live endpoint's cache between polls.
	f.now = f.now.Add(10 * time.Second)
	b, err := f.server.LiveJSON(f.now)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"PRIVATE239", f.work} {
		if strings.Contains(string(b), private) {
			t.Errorf("the public live JSON contains private transcript data %q", private)
		}
	}
	var body struct {
		Steps []thinking239Card `json:"steps"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	got := make(map[int]thinking239Card)
	for _, card := range body.Steps {
		if _, exists := got[card.Issue]; exists {
			t.Fatalf("duplicate card for issue %d", card.Issue)
		}
		got[card.Issue] = card
	}
	return got
}

func (f *thinking239Fixture) card(t *testing.T, issue int) thinking239Card {
	t.Helper()
	c, ok := f.cards(t)[issue]
	if !ok {
		t.Fatalf("no live card for issue %d", issue)
	}
	return c
}

func thinking239Count(t *testing.T, c thinking239Card, want int, exact bool) {
	t.Helper()
	if c.Thinking != want || c.ThinkingExact != exact {
		t.Errorf("issue %d thinking = %d (exact %v), want %d (exact %v)", c.Issue, c.Thinking, c.ThinkingExact, want, exact)
	}
}

func thinking239Running(t *testing.T, c thinking239Card) {
	t.Helper()
	if c.Now != "thinking" || c.Agent != "" || c.Ended != nil {
		t.Errorf("running card: now %q, agent %q, ended %v; want thinking with no finished state", c.Now, c.Agent, c.Ended)
	}
}

func TestLiveCodexReportsCompletedTurnThinking(t *testing.T) {
	f := newThinking239Fixture(t, dashboard.Step{Issue: 7, Doing: "building", Agent: "codex"})
	f.line(t, 7, 0, map[string]any{"type": "invariant.workspace", "cwd": f.work})
	f.line(t, 7, 1, map[string]any{"type": "turn.started"})
	c := f.card(t, 7)
	thinking239Running(t, c)
	thinking239Count(t, c, 0, false)
	f.line(t, 7, 2, map[string]any{"type": "item.completed", "item": map[string]any{
		"id": "answer1", "type": "agent_message", "text": "PRIVATE239 answer",
	}})

	// A completion may arrive across reads. Its reported count is unavailable
	// until the whole newline-terminated event has been written.
	completion := `{"type":"turn.completed","usage":{"input_tokens":9000,"output_tokens":6000,"reasoning_output_tokens":4157}}` + "\n"
	cut := len(completion) / 2
	f.append(t, 7, completion[:cut])
	c = f.card(t, 7)
	thinking239Running(t, c)
	thinking239Count(t, c, 0, false)
	f.append(t, 7, completion[cut:])
	c = f.card(t, 7)
	thinking239Count(t, c, 4157, true)
	if c.Now != "" || c.Agent != "finished" || c.Output != 6000 {
		t.Errorf("completed card: now %q, agent %q, output %d; want finished with 6000 output tokens", c.Now, c.Agent, c.Output)
	}
	if len(c.Marks) != 0 {
		t.Errorf("usage alone invented timeline marks: %+v", c.Marks)
	}
	thinking239Count(t, f.card(t, 7), 4157, true)

	// Until the next turn reports usage, only the preceding completed turns
	// have an exact total. The card must still show the new turn's activity.
	f.line(t, 7, 4, map[string]any{"type": "turn.started"})
	c = f.card(t, 7)
	thinking239Running(t, c)
	thinking239Count(t, c, 4157, true)
	f.line(t, 7, 5, map[string]any{"type": "turn.completed", "usage": map[string]any{
		"input_tokens": 12000, "output_tokens": 91, "reasoning_output_tokens": 23,
	}})
	thinking239Count(t, f.card(t, 7), 4180, true)
	thinking239Count(t, f.card(t, 7), 4180, true)

	// Reading from the beginning and retaining an ended card give the same sum.
	f.restart()
	thinking239Count(t, f.card(t, 7), 4180, true)
	f.setSteps(t)
	c = f.card(t, 7)
	thinking239Count(t, c, 4180, true)
	if c.Ended == nil || c.Now != "" {
		t.Errorf("ended step not retained as ended: %+v", c.Live)
	}
}

func TestLiveCodexUsageReplacesEstimatesAndKeepsThinkingStretches(t *testing.T) {
	for _, reported := range []int{7, 0} {
		t.Run(fmt.Sprintf("reported_%d", reported), func(t *testing.T) {
			// No agent name in status: the workspace line identifies Codex,
			// including when the watcher has not named the agent yet.
			f := newThinking239Fixture(t, dashboard.Step{Issue: 7, Doing: "building"})
			f.line(t, 7, 0, map[string]any{"type": "invariant.workspace", "cwd": f.work})
			f.line(t, 7, 1, map[string]any{"type": "turn.started"})
			f.line(t, 7, 3, map[string]any{"type": "item.completed", "item": map[string]any{
				"id": "reason1", "type": "reasoning", "text": "PRIVATE239 " + strings.Repeat("thought ", 100),
			}})
			before := f.card(t, 7)
			thinking239Running(t, before)
			if before.Thinking <= reported || before.ThinkingExact {
				t.Errorf("before usage, want the existing text estimate: %+v", before)
			}
			if len(before.Marks) != 1 {
				t.Fatalf("want one thinking stretch, got %+v", before.Marks)
			}
			m := before.Marks[0]
			if m.Kind != "think" || !m.At.Equal(f.since.Add(time.Second)) || m.Until == nil || !m.Until.Equal(f.since.Add(3*time.Second)) || m.Tokens <= 0 {
				t.Errorf("reasoning should remain a timed, estimated thinking stretch: %+v", m)
			}

			// Legacy transcripts can omit reasoning usage. Output usage is not
			// itself evidence of an exact thinking count.
			f.line(t, 7, 4, map[string]any{"type": "turn.completed", "usage": map[string]any{"output_tokens": 1000}})
			thinking239Count(t, f.card(t, 7), before.Thinking, false)
			f.line(t, 7, 5, map[string]any{"type": "turn.started"})
			f.line(t, 7, 6, map[string]any{"type": "turn.completed", "usage": map[string]any{
				"output_tokens": 1000, "reasoning_output_tokens": reported,
			}})
			after := f.card(t, 7)
			thinking239Count(t, after, reported, true)
			if !reflect.DeepEqual(after.Marks, before.Marks) {
				t.Errorf("reported usage changed the timeline: before %+v, after %+v", before.Marks, after.Marks)
			}

			f.line(t, 7, 7, map[string]any{"type": "turn.started"})
			f.line(t, 7, 9, map[string]any{"type": "item.completed", "item": map[string]any{
				"id": "reason2", "type": "reasoning", "text": "PRIVATE239 more reasoning " + strings.Repeat("x", 400),
			}})
			pending := f.card(t, 7)
			thinking239Running(t, pending)
			thinking239Count(t, pending, reported, true)
			if len(pending.Marks) != 2 {
				t.Fatalf("later reasoning lost its timeline stretch: %+v", pending.Marks)
			}
			m = pending.Marks[1]
			if m.Kind != "think" || !m.At.Equal(f.since.Add(7*time.Second)) || m.Until == nil || !m.Until.Equal(f.since.Add(9*time.Second)) {
				t.Errorf("later reasoning has wrong times: %+v", m)
			}
			f.line(t, 7, 10, map[string]any{"type": "turn.completed", "usage": map[string]any{"reasoning_output_tokens": 5}})
			after = f.card(t, 7)
			thinking239Count(t, after, reported+5, true)
			if !reflect.DeepEqual(after.Marks, pending.Marks) {
				t.Errorf("completing a later turn changed its thinking stretches")
			}
		})
	}
}

func TestLiveThinkingCountsStayWithTheirAgentRun(t *testing.T) {
	f := newThinking239Fixture(t,
		dashboard.Step{Issue: 7, Doing: "building", Agent: "codex"},
		dashboard.Step{Issue: 9, Doing: "building", Agent: "claude-code"},
	)
	f.line(t, 7, 0, map[string]any{"type": "invariant.workspace", "cwd": f.work})
	f.line(t, 7, 1, map[string]any{"type": "turn.started"})
	f.line(t, 9, 0, map[string]any{"type": "system", "subtype": "init", "cwd": f.work})
	for _, delta := range []int{50, 100} {
		f.line(t, 9, 1, map[string]any{"type": "system", "subtype": "thinking_tokens", "estimated_tokens_delta": delta})
	}
	cards := f.cards(t)
	if len(cards) != 2 {
		t.Fatalf("want both running cards, got %d", len(cards))
	}
	thinking239Running(t, cards[7])
	thinking239Running(t, cards[9])
	thinking239Count(t, cards[9], 150, false)
	f.line(t, 7, 2, map[string]any{"type": "turn.completed", "usage": map[string]any{"reasoning_output_tokens": 4157}})
	f.line(t, 9, 2, map[string]any{"type": "system", "subtype": "thinking_tokens", "estimated_tokens_delta": 400})
	cards = f.cards(t)
	thinking239Count(t, cards[7], 4157, true)
	thinking239Count(t, cards[9], 550, false)
	thinking239Running(t, cards[9])
	f.line(t, 9, 3, map[string]any{"type": "assistant", "message": map[string]any{
		"id": "claude1", "content": []map[string]any{
			{"type": "thinking", "thinking": "PRIVATE239 Claude thinking"},
			{"type": "text", "text": "PRIVATE239 Claude answer"},
		},
	}})
	f.line(t, 9, 4, map[string]any{"type": "result", "subtype": "success", "result": "PRIVATE239 result", "usage": map[string]any{"output_tokens": 9000}})
	cards = f.cards(t)
	thinking239Count(t, cards[7], 4157, true)
	thinking239Count(t, cards[9], 550, false)
	claude := cards[9]
	if claude.Agent != "finished" || claude.Now != "" || len(claude.Marks) != 1 || claude.Marks[0].Tokens != 550 || claude.Marks[0].Until == nil {
		t.Errorf("Claude's existing count and completed thinking stretch changed: %+v", claude)
	}

	// Replacement of a transcript must discard the preceding run's totals
	// and exactness; the other agent's retained card is unaffected.
	if err := os.WriteFile(f.path(7), []byte(`{"type":"turn.started"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cards = f.cards(t)
	thinking239Running(t, cards[7])
	thinking239Count(t, cards[7], 0, false)
	thinking239Count(t, cards[9], 550, false)
	f.line(t, 7, 6, map[string]any{"type": "turn.completed", "usage": map[string]any{"reasoning_output_tokens": 31}})
	cards = f.cards(t)
	thinking239Count(t, cards[7], 31, true)
	thinking239Count(t, cards[9], 550, false)
}
