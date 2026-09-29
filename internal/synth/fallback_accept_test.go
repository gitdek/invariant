package synth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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

// loopyRun is one run of the stand-in agent: the effort it thought at, its
// workspace, and the text of each file it watches, as the run found them.
type loopyRun struct {
	effort    string
	workspace string
	found     map[string]string
}

// loopyAgent is a stand-in coding agent. It thinks at the effort its job
// names, or at its own when the job names none. At each effort it writes
// the files writes holds for it, then either finishes, or, where loops says
// so, spends every reply thinking until the output limit cuts it off, which
// the loop guard stops.
type loopyAgent struct {
	effort string
	loops  map[string]bool
	writes map[string]map[string]string // by effort, each file's text by its slash path in the workspace
	watch  []string
	runs   []loopyRun
}

func (a *loopyAgent) Name() string { return "stand-in" }

func (a *loopyAgent) Run(_ context.Context, job Job) (Usage, error) {
	effort := job.Effort
	if effort == "" {
		effort = a.effort
	}
	run := loopyRun{effort: effort, workspace: job.Workspace, found: map[string]string{}}
	for _, f := range a.watch {
		if b, err := os.ReadFile(filepath.Join(job.Workspace, filepath.FromSlash(f))); err == nil {
			run.found[f] = string(b)
		}
	}
	a.runs = append(a.runs, run)
	for f, text := range a.writes[effort] {
		p := filepath.Join(job.Workspace, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return Usage{}, err
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			return Usage{}, err
		}
	}
	if a.loops[effort] {
		return ReadStream(strings.NewReader(loopyReplies), job.Transcript)
	}
	return Usage{Model: "claude-opus-5", Turns: 4, Outcome: "success", Summary: "The code is written."}, nil
}

// efforts is the effort of each of the agent's runs, in order.
func (a *loopyAgent) efforts() []string {
	var out []string
	for _, r := range a.runs {
		out = append(out, r.effort)
	}
	return out
}

// loopySynthesis synthesizes the two-phase commit with agent, whose own
// effort is max, falling back to xhigh. It returns the result, where the
// synthesis put its output, and its error. Without the verifiers here, the
// final gate may not run, so the error can be the gate's.
func loopySynthesis(t *testing.T, agent *loopyAgent) (*Result, string, error) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out")
	r, err := Synthesize(context.Background(), Options{Project: example, Out: out, Backend: agent, Binary: "invariant",
		KeepModel: true, GateRuns: 4, Timeout: time.Minute, FallbackEffort: "xhigh"})
	return r, out, err
}

// loopyFellBack fails the test unless fb says the build ran its agent again
// at xhigh, because the loop guard stopped its first run.
func loopyFellBack(t *testing.T, fb *Fallback) {
	t.Helper()
	switch {
	case fb == nil:
		t.Error("the result doesn't say the build fell back")
	case fb.Effort != "xhigh" || !strings.Contains(fb.Why, ErrThinkingLoop.Error()):
		t.Errorf("the result says the build fell back to %q because %q; want xhigh, because %q", fb.Effort, fb.Why, ErrThinkingLoop.Error())
	}
}

// At max, #91's build agent spent whole replies thinking, and the loop guard
// stopped it. A synthesis whose agent the guard stops at max runs it once
// more at xhigh, in the same workspace, so it keeps what it wrote before it
// looped, and the result says so.
func TestASynthesisThatLoopsAtMaxRunsAgainAtXhigh(t *testing.T) {
	first := "package twophase\n\n// Written at max, before the agent looped.\n"
	second := "package twophase\n\n// Written at xhigh.\n"
	agent := &loopyAgent{effort: "max", loops: map[string]bool{"max": true},
		writes: map[string]map[string]string{"max": {"twophase/first.go": first}, "xhigh": {"twophase/second.go": second}},
		watch:  []string{"twophase/first.go"}}
	r, out, err := loopySynthesis(t, agent)
	if got := agent.efforts(); !slices.Equal(got, []string{"max", "xhigh"}) {
		t.Fatalf("the agent ran at %q; want once at max, then once more at xhigh", got)
	}
	if agent.runs[1].workspace != agent.runs[0].workspace {
		t.Errorf("the second run was in %s, not in the first run's workspace, %s", agent.runs[1].workspace, agent.runs[0].workspace)
	}
	if got := agent.runs[1].found["twophase/first.go"]; got != first {
		t.Errorf("the second run found twophase/first.go as %q; want it as the first run left it", got)
	}
	if errors.Is(err, ErrThinkingLoop) {
		t.Errorf("the synthesis failed with the loop guard's error, though its second run finished: %v", err)
	}
	if r == nil {
		t.Fatalf("the synthesis gave no result: %v", err)
	}
	for f, want := range map[string]string{"twophase/first.go": first, "twophase/second.go": second} {
		if b, err := os.ReadFile(filepath.Join(out, "result", filepath.FromSlash(f))); err != nil || string(b) != want {
			t.Errorf("the result's %s is %q, %v; want what the agent wrote", f, b, err)
		}
	}
	if r.Usage.Outcome != "success" {
		t.Errorf("the run's outcome is %q; want the second run's success", r.Usage.Outcome)
	}
	loopyFellBack(t, r.Fallback)
}

// A synthesis whose agent loops at xhigh too, and writes nothing, as #91's
// did, runs it exactly twice. It fails with the guard's error, which says
// why, though the final gate then found nothing to check, and its result
// says it fell back.
func TestASynthesisThatLoopsAtXhighTooFailsWithTheGuardsReason(t *testing.T) {
	agent := &loopyAgent{effort: "max", loops: map[string]bool{"max": true, "xhigh": true}}
	r, _, err := loopySynthesis(t, agent)
	if got := agent.efforts(); !slices.Equal(got, []string{"max", "xhigh"}) {
		t.Fatalf("the agent ran at %q; want exactly twice, at max, then at xhigh", got)
	}
	if err == nil || !errors.Is(err, ErrThinkingLoop) || !strings.Contains(err.Error(), ErrThinkingLoop.Error()) {
		t.Errorf("the synthesis failed with %v; want the loop guard's error, which says why", err)
	}
	if r == nil {
		t.Fatal("the synthesis gave no result")
	}
	loopyFellBack(t, r.Fallback)
}

// The run a build falls back to thinks at the effort its job names. Claude
// Code is told that effort, once, in place of its own, for building and for
// reviewing alike, and its own when the job names none.
func TestAFallbackRunTellsClaudeCodeItsEffort(t *testing.T) {
	c := ClaudeCode{Model: "opus", BudgetUSD: 5, MaxTurns: 80, Effort: "max"}
	for _, job := range []Job{{Prompt: "p", GateServer: []string{"invariant", "mcp"}}, {Prompt: "p", ReadOnly: true}} {
		for _, effort := range []string{"xhigh", ""} {
			job.Effort = effort
			args, err := c.args(job)
			if err != nil {
				t.Fatal(err)
			}
			want := effort
			if want == "" {
				want = "max"
			}
			var told []string
			for i, a := range args {
				if a == "--effort" && i+1 < len(args) {
					told = append(told, args[i+1])
				}
			}
			if !slices.Equal(told, []string{want}) {
				t.Errorf("read-only %v, a job whose effort is %q: Claude Code was told --effort %q; want %s, once", job.ReadOnly, effort, told, want)
			}
		}
	}
}
