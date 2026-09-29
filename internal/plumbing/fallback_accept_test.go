package plumbing

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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

func (a *loopyAgent) Run(_ context.Context, job synth.Job) (synth.Usage, error) {
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
			return synth.Usage{}, err
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			return synth.Usage{}, err
		}
	}
	if a.loops[effort] {
		return synth.ReadStream(strings.NewReader(loopyReplies), job.Transcript)
	}
	return synth.Usage{Model: "claude-opus-5", Turns: 4, Outcome: "success", Summary: "The change is written."}, nil
}

// efforts is the effort of each of the agent's runs, in order.
func (a *loopyAgent) efforts() []string {
	var out []string
	for _, r := range a.runs {
		out = append(out, r.effort)
	}
	return out
}

// loopyReviewer approves whatever it's shown, and notes the effort it
// thought at each time: the job's, or its own, max, when the job names none.
type loopyReviewer struct{ efforts []string }

func (r *loopyReviewer) Name() string { return "stand-in reviewer" }

func (r *loopyReviewer) Run(_ context.Context, job synth.Job) (synth.Usage, error) {
	effort := job.Effort
	if effort == "" {
		effort = "max"
	}
	r.efforts = append(r.efforts, effort)
	return synth.Usage{Outcome: "success", Summary: "It does what the plan says.\n\nVERDICT: approve"}, nil
}

// loopyPageTest is the acceptance test of the plan the stand-in agent builds.
const loopyPageTest = `package page

import "testing"

func TestPageSaysHello(t *testing.T) {
	if Hello() != "hello" {
		t.Fatal("Hello() isn't hello")
	}
}
`

// loopyDocker stands in for docker. A run of the sandbox passes gofmt, go
// vet and the tests, and passes the acceptance test only when the checkout
// it's given has a page/page.go that says hello.
const loopyDocker = `#!/bin/sh
[ "$1" = run ] || exit 0
src=
for a in "$@"; do
	case "$a" in
	*:/src) src=${a%:/src} ;;
	esac
done
echo "@@invariant vet=0"
echo "@@invariant test=0"
if grep -q 'func Hello' "$src/page/page.go" 2>/dev/null; then
	echo "--- PASS: TestPageSaysHello (0.00s)"
	echo "@@invariant accept=0"
else
	echo "--- FAIL: TestPageSaysHello (0.00s)"
	echo "@@invariant accept=1"
fi
`

// loopyCheckout is a git checkout holding issue 7's ratified plan, a page
// that says hello, with loopyDocker on the PATH for the sandbox to run.
func loopyCheckout(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in docker is a shell script")
	}
	bin, root := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(loopyDocker), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	p := &Plan{Name: "a hello page", Summary: "Add a page that says hello.", Files: []string{"page/page.go"},
		Tests:   []Test{{Name: "TestPageSaysHello", File: "page/page_accept_test.go", Says: "The page says hello."}},
		Sources: map[string]string{"page/page_accept_test.go": loopyPageTest}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	l := Lock{Ratified: Ratification{By: "gitdek", At: "2026-09-29T15:00:00Z", Issue: 7, Comment: "https://github.com/o/r/issues/7#issuecomment-1", Proposal: p.Hash()}, Plan: *p}
	if err := l.Write(root); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"add", "-A"},
		{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "--quiet", "--no-verify", "-m", "Ratify the plan for #7"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	return root
}

// loopyBuild builds issue 7's plan in root with agent, whose own effort is
// max, falling back to xhigh, and reviews it with reviewer.
func loopyBuild(t *testing.T, root string, agent *loopyAgent, reviewer *loopyReviewer) *BuildResult {
	t.Helper()
	b := Builder{Backend: agent, Reviewer: reviewer, Binary: "invariant", TestRuns: 6, Timeout: time.Minute,
		Sandbox: Sandbox{Image: "golang", ModCache: t.TempDir()}, FallbackEffort: "xhigh"}
	r, err := b.Build(context.Background(), root, 7, filepath.Join(t.TempDir(), "build"))
	if err != nil {
		t.Fatalf("the build: %v", err)
	}
	return r
}

// loopyFellBack fails the test unless fb says the build ran its agent again
// at xhigh, because the loop guard stopped its first run.
func loopyFellBack(t *testing.T, fb *synth.Fallback) {
	t.Helper()
	switch {
	case fb == nil:
		t.Error("the result doesn't say the build fell back")
	case fb.Effort != "xhigh" || !strings.Contains(fb.Why, synth.ErrThinkingLoop.Error()):
		t.Errorf("the result says the build fell back to %q because %q; want xhigh, because %q", fb.Effort, fb.Why, synth.ErrThinkingLoop.Error())
	}
}

// A plan's build whose agent the loop guard stops at max, after it began the
// plan's file, runs it once more at xhigh, in the same workspace with the
// file as it was, and passes on that run. The result says it fell back, and
// the review still thinks at max.
func TestAPlanBuildThatLoopsAtMaxPassesAtXhigh(t *testing.T) {
	root := loopyCheckout(t)
	begun := "package page\n"
	done := begun + "\n// Hello says hello.\nfunc Hello() string { return \"hello\" }\n"
	agent := &loopyAgent{effort: "max", loops: map[string]bool{"max": true},
		writes: map[string]map[string]string{"max": {"page/page.go": begun}, "xhigh": {"page/page.go": done}},
		watch:  []string{"page/page.go"}}
	reviewer := &loopyReviewer{}
	r := loopyBuild(t, root, agent, reviewer)
	if got := agent.efforts(); !slices.Equal(got, []string{"max", "xhigh"}) {
		t.Fatalf("the agent ran at %q; want once at max, then once more at xhigh", got)
	}
	if agent.runs[1].workspace != agent.runs[0].workspace {
		t.Errorf("the second run was in %s, not in the first run's workspace, %s", agent.runs[1].workspace, agent.runs[0].workspace)
	}
	if got := agent.runs[1].found["page/page.go"]; got != begun {
		t.Errorf("the second run found page/page.go as %q; want it as the first run left it, %q", got, begun)
	}
	if !r.Passed {
		t.Errorf("the build didn't pass on its second run: %s", r.Summary)
	}
	if b, err := os.ReadFile(filepath.Join(root, "page", "page.go")); err != nil || string(b) != done {
		t.Errorf("the checkout's page/page.go is %q, %v; want what the second run wrote", b, err)
	}
	loopyFellBack(t, r.Fallback)
	if !slices.Equal(reviewer.efforts, []string{"max"}) {
		t.Errorf("the review ran at %q; want once, at max", reviewer.efforts)
	}
}

// A plan's build whose agent loops at xhigh too runs it exactly twice, and
// fails, with the guard's reason in what the factory's own run found.
func TestAPlanBuildThatLoopsAtXhighTooFailsWithTheGuardsReason(t *testing.T) {
	root := loopyCheckout(t)
	agent := &loopyAgent{effort: "max", loops: map[string]bool{"max": true, "xhigh": true},
		writes: map[string]map[string]string{"max": {"page/page.go": "package page\n"}}}
	r := loopyBuild(t, root, agent, &loopyReviewer{})
	if got := agent.efforts(); !slices.Equal(got, []string{"max", "xhigh"}) {
		t.Fatalf("the agent ran at %q; want exactly twice, at max, then at xhigh", got)
	}
	if r.Passed || !strings.Contains(r.Summary, synth.ErrThinkingLoop.Error()) {
		t.Errorf("passed %v, with the summary %q; want it failed, with the loop guard's reason", r.Passed, r.Summary)
	}
	loopyFellBack(t, r.Fallback)
}
