package formalize

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
)

// Plan drafts a plumbing issue's plan with the agent (D-0105): what
// changes, the files the build may write, and the acceptance tests that
// show it works, or the forks it can't settle. repo is a checkout of the
// base branch, which the agent reads and the factory checks the plan
// against. Its transcript and draft land in out.
func (f Formalizer) Plan(ctx context.Context, req Request, repo, out string) (*Result, error) {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	ws, err := os.MkdirTemp("", "invariant-plan-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(ws)
	if err := writeFile(filepath.Join(ws, "request.md"), req.Markdown()); err != nil {
		return nil, err
	}
	if err := plumbing.CopyCheckout(ctx, repo, filepath.Join(ws, "repo")); err != nil {
		return nil, err
	}
	if p := req.Previous; p != nil && p.Plan != nil {
		b, err := json.MarshalIndent(p.Plan, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := writeFile(filepath.Join(ws, "previous", "plan.json"), string(b)+"\n"); err != nil {
			return nil, err
		}
	}
	transcript, err := os.Create(filepath.Join(out, "transcript.jsonl"))
	if err != nil {
		return nil, err
	}
	defer transcript.Close()
	checkLog := filepath.Join(out, "check-runs.jsonl")
	runCtx, cancel := context.WithTimeout(ctx, f.Timeout)
	usage, runErr := f.Backend.Run(runCtx, synth.Job{
		Workspace:  ws,
		Prompt:     PlanPrompt(req, f.CheckRuns),
		GateServer: []string{f.Binary, "mcp", "-plan", "-max-runs", fmt.Sprint(f.CheckRuns), "-log", checkLog, "-cache", filepath.Join(out, "gocache"), ws},
		Tools:      []string{"check"},
		Transcript: transcript,
	})
	cancel()
	usage.Backend = f.Backend.Name()
	r := &Result{Usage: usage}
	r.CheckRuns, _ = readRuns(checkLog)
	defer func() {
		for _, name := range []string{"plan.json", "proposal.json"} {
			if b, err := os.ReadFile(filepath.Join(ws, name)); err == nil {
				os.WriteFile(filepath.Join(out, name), b, 0o644)
			}
		}
		b, _ := json.MarshalIndent(r, "", "  ")
		os.WriteFile(filepath.Join(out, "formalization.json"), append(b, '\n'), 0o644)
	}()
	fail := func(problem string) (*Result, error) {
		if runErr != nil {
			problem = fmt.Sprintf("the agent's run failed (%v), and it left no usable plan: %s", runErr, problem)
		}
		r.Problem = problem
		return r, nil
	}
	if _, err := os.Stat(filepath.Join(ws, "plan.json")); err != nil {
		// No plan: forks to ask, or why the factory can't take the issue.
		p, err := Read(ws)
		switch {
		case err != nil:
			return fail(err.Error())
		case p.Unsupported == "" && len(p.Forks) == 0:
			return fail("a plumbing draft is plan.json, or proposal.json with forks to ask")
		}
		r.Proposal = p
		return r, nil
	}
	// The factory checks the plan itself, against the base branch as it is,
	// whatever the agent says.
	c, err := plumbing.Check(ctx, ws, repo, f.Sandbox)
	if err != nil {
		return nil, err
	}
	if c.Problem != "" {
		return fail(c.Problem)
	}
	r.Proposal = &Proposal{Draft: Draft{Name: c.Plan.Name, Language: "go"}, Plan: c.Plan, Hash: c.Plan.Hash()}
	return r, nil
}

// PlanPrompt is the task for the agent that plans a plumbing change.
func PlanPrompt(req Request, checks int) string {
	var b strings.Builder
	fmt.Fprintf(&b, `You're planning a change to this repository for issue #%d. It isn't a state machine to model and prove. It's plumbing: code that's tested, not proved (D-0105). A person ratifies your plan by its hash, and then another agent builds exactly what it says, so the plan is the contract.

request.md holds the issue, what's been decided, and the discussion. repo/ is the repository as it is on the base branch. Read what the change touches, and AGENTS.md for the repository's rules. Don't change anything in repo/: it's there to read.

# Write the plan

plan.json:

`+"```json"+`
{
  "name": "the change, in a few plain words",
  "summary": "What changes, and why, in two to five plain sentences. It's what the person ratifies, and what the build does.",
  "files": ["internal/dashboard/graph.go", "internal/dashboard/web/app.js"],
  "tests": [
    {"name": "TestGraphShowsEveryDecision", "file": "internal/dashboard/graph_accept_test.go", "says": "Every decision in the journal is a node in the graph the page draws."}
  ]
}
`+"```"+`

- **files** is every file the build may write: the ones it changes and the ones it adds. The build can't write anything else, so name everything the change needs, and nothing it doesn't. Paths are from the repository's root. Nothing under .github/.
- **tests** are the acceptance tests: Go test functions that show the change works, each in a new file that isn't in repo/ yet, named like `+"`*_accept_test.go`"+`. Write each file under tests/, at its path in the repository: tests/internal/dashboard/graph_accept_test.go. The build can't change them, so write them against the change's intended API, and test what it does, not how.
- Each test must fail on the code as it is. A test that already passes can't show anything.

Keep the change small enough to review. If the issue needs several independent changes, plan the first, and say in the summary what's left.

# Ask when you must

If the issue allows materially different changes, don't guess. Write proposal.json with forks instead of a plan, and people will answer:

`+"```json"+`
{"forks": [{"id": "F1", "question": "…?", "options": [{"id": "A", "says": "…"}, {"id": "B", "says": "…"}]}]}
`+"```"+`

Don't ask about names or code structure: choose those yourself. If the issue isn't something the factory can do, write proposal.json with `+"`unsupported`"+` set to one sentence saying why.

# Check your plan

The `+"`check`"+` tool reads plan.json and tests/, checks that the plan holds together, and runs the acceptance tests on the code in repo/, where they must fail. You have %d checks, so reread your files before each. When the check passes, you're done. Then reply with a two-sentence summary.

%s
`, req.Issue, checks, synth.WriteAsYouGo("", "checks"))
	if req.Previous != nil && req.Previous.Plan != nil {
		b.WriteString("\n# The plan people asked to revise\n\nprevious/plan.json is the plan before. Keep what the discussion didn't ask to change.\n")
	}
	return b.String()
}
