package formalize

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/synth"
)

// loopyReplies is what Claude Code reports of a run whose replies are only
// thinking: each ends at the output limit, and Claude Code asks the agent to
// carry on. The loop guard stops it at the second, before the rest.
const loopyReplies = `{"type":"system","subtype":"init","model":"claude-opus-5","mcp_servers":[{"name":"invariant","status":"connected"}]}
{"type":"assistant","message":{"id":"m1","content":[{"type":"thinking","thinking":""}]}}
{"type":"user","message":{"content":[{"type":"text","text":"Output token limit hit. Resume directly."}]}}
{"type":"assistant","message":{"id":"m2","content":[{"type":"thinking","thinking":""}]}}
{"type":"user","message":{"content":[{"type":"text","text":"Output token limit hit. Resume directly."}]}}
{"type":"assistant","message":{"id":"m3","content":[{"type":"tool_use","name":"Write"}]}}
{"type":"result","subtype":"success","result":"Done.","num_turns":3}
`

// loopyAgent is a stand-in drafting agent. At max, its own effort, its
// replies are only thinking, each cut off at the output limit, which the
// loop guard stops. At any other effort it finishes, having written nothing.
// It notes the effort of each run: the job's, or max when the job names
// none.
type loopyAgent struct{ efforts []string }

func (a *loopyAgent) Name() string { return "stand-in" }

func (a *loopyAgent) Run(_ context.Context, job synth.Job) (synth.Usage, error) {
	effort := job.Effort
	if effort == "" {
		effort = "max"
	}
	a.efforts = append(a.efforts, effort)
	if effort == "max" {
		return synth.ReadStream(strings.NewReader(loopyReplies), job.Transcript)
	}
	return synth.Usage{Model: "claude-opus-5", Turns: 4, Outcome: "success"}, nil
}

// Only builds fall back (D-0125). A draft whose agent the loop guard stops,
// whether it drafts statements, a plumbing plan or a PRD's plan of issues,
// ran its agent once, at max, and isn't run again. Its problem says why.
func TestADraftWhoseAgentLoopsIsntRunAgain(t *testing.T) {
	for _, kind := range []string{"statements", "a plumbing plan", "a plan of issues"} {
		req := Request{Repo: "gitdek/invariant", Issue: 7, Title: "Add a bounded buffer", Body: "Producers put lines in a buffer, and a shipper takes them out.", Author: "gitdek"}
		switch kind {
		case "a plumbing plan":
			req.Plumbing = t.TempDir()
		case "a plan of issues":
			req.PRD = t.TempDir()
		}
		agent := &loopyAgent{}
		f := Formalizer{Backend: agent, Binary: "invariant", CheckRuns: 4, Timeout: time.Minute}
		r, err := f.Formalize(context.Background(), req, filepath.Join(t.TempDir(), "out"))
		if err != nil {
			t.Fatalf("drafting %s: %v", kind, err)
		}
		if len(agent.efforts) != 1 || agent.efforts[0] != "max" {
			t.Errorf("drafting %s ran the agent at %q; want once, at max", kind, agent.efforts)
		}
		if !strings.Contains(r.Problem, synth.ErrThinkingLoop.Error()) {
			t.Errorf("drafting %s, the problem is %q; want the loop guard's reason", kind, r.Problem)
		}
	}
}
