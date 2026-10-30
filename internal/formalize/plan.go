package formalize

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/regular"
	"github.com/gitdek/invariant/internal/synth"
)

// Plan drafts a plumbing issue's plan with the agent (D-0105): what
// changes, the files the build may write, and the acceptance tests that
// show it works, a plan of issues, or the forks it can't settle. repo is a
// checkout of the base branch, which the agent reads and the factory checks
// the plan against. Its transcript and draft land in out.
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
	} else if p != nil && p.IssuePlan != nil {
		b, err := json.MarshalIndent(p.IssuePlan, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := writeFile(filepath.Join(ws, "previous", "issues.json"), string(b)+"\n"); err != nil {
			return nil, err
		}
	}
	transcript, err := os.Create(filepath.Join(out, "transcript.jsonl"))
	if err != nil {
		return nil, err
	}
	defer transcript.Close()
	checkLog := filepath.Join(out, "check-runs.jsonl")
	usage, runErr := f.Backend.Run(ctx, synth.Job{
		Workspace:  ws,
		Prompt:     PlanPrompt(req, f.CheckRuns),
		GateServer: []string{f.Binary, "mcp", "-plan", "-max-runs", fmt.Sprint(f.CheckRuns), "-log", checkLog, "-cache", filepath.Join(out, "gocache"), ws},
		Tools:      []string{"check"},
		Transcript: transcript,
		Timeout:    f.Timeout,
	})
	usage.Backend = f.Backend.Name()
	r := &Result{Usage: usage}
	r.CheckRuns, _ = readRuns(checkLog)
	defer func() {
		for _, name := range []string{"plan.json", "issues.json", "proposal.json"} {
			if b, err := regular.ReadFile(ws, name); err == nil {
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
		if _, err := os.Stat(filepath.Join(ws, "issues.json")); err == nil {
			plan, err := ReadIssues(ws)
			if err != nil {
				return fail(err.Error())
			}
			r.Proposal = &Proposal{Draft: Draft{Name: plan.Name}, IssuePlan: plan, Hash: plan.Hash()}
			return r, nil
		}
		// No plan: forks to ask, or why the factory can't take the issue.
		p, err := Read(ws)
		switch {
		case err != nil:
			return fail(err.Error())
		case p.Unsupported == "" && len(p.Forks) == 0:
			return fail("a plumbing draft is plan.json, issues.json, or proposal.json with forks to ask")
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

Cover the whole issue. If all the work fits in one reviewable pull request, write plan.json and its acceptance tests. Otherwise write issues.json, a plan of issues that together cover all the work, as described below. Write only one kind of plan.

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

- **files** names the files the build may change or add. The build may also add regular Go `+"`*_test.go`"+` regression files in packages the plan touches: the nearest Go package owning a planned embedded asset counts, as does a new package named by the plan. This allowance doesn't permit changes to existing unnamed tests, the acceptance tests, the plan's record, other production files or unrelated packages. Name everything else the change needs, and nothing it doesn't. Paths are from the repository's root. Nothing under .github/.
- **tests** are the acceptance tests: Go test functions that show the change works, each in a new file that isn't in repo/ yet, named like `+"`*_accept_test.go`"+`. Write each file under tests/, at its path in the repository: tests/internal/dashboard/graph_accept_test.go. The build can't change them, so write them against the change's intended API, and test what it does, not how.
- Each test must fail on the code as it is. A test that already passes can't show anything.

SPEC.md and the README describe Invariant as it is, and a change that makes either one wrong fixes it in the same change (D-0122). When yours does, name that file among the plan's files, and say in the summary what changes there. A new or changed SPEC bullet cites the decisions it rests on, as `+"`decisions check`"+` requires. A plumbing build can't write decisions/, so when the change needs a decision no one has recorded yet, say so in the summary, and whoever merges it records the decision.

# When the work needs several pull requests

Write issues.json instead of plan.json and tests/:

`+"```json"+`
{
  "name": "the whole change, in a few plain words",
  "summary": "What the issues do together, and why.",
  "issues": [
    {"title": "Serve the decision graph as JSON", "body": "The dashboard serves every decision and its edges at /api/graph.json, for the page to draw.\n\nKind: plumbing"},
    {"title": "Draw the decision graph", "body": "The page draws every decision and edge served at /api/graph.json.\n\nKind: plumbing"}
  ]
}
`+"```"+`

- **issues** are 1 to %d, in the order they're taken. Each is opened once the one before it has merged, so it can build on the ones before it, and never on one after. Together they cover the whole issue; don't leave work for an unplanned follow-up. Each plumbing issue will get its own plan, and can itself become a plan of issues if needed.
- Each **title** is one line. Each **body** is the whole issue: what changes and why, complete enough that someone who hasn't read this issue can take it on, with the decisions it rests on.
- Each body has either a `+"`Kind: plumbing`"+` line for tested code, with no `+"`Code:`"+` line, or a `+"`Project: <directory>`"+` line for a state machine whose rules are worth proving, never both. A project directory is a clean path inside the repository, outside .git/ and .github/.
- No body has an `+"`/invariant`"+` command on a line of its own. The factory takes each issue itself.
- The name, summary, titles and bodies together come to at most %d bytes, so the proposal fits in one comment. None has an HTML comment, which the proposal wouldn't show.

A person ratifies this plan's hash before any issue opens. A nested plan counts as complete for its parent only when the factory posts completion for its ratified hash; an unfinished or stopped plan holds its parent.

# Ask when you must

If the issue allows materially different changes, don't guess. Write proposal.json with forks instead of a plan, and people will answer:

`+"```json"+`
{"forks": [{"id": "F1", "question": "…?", "options": [{"id": "A", "says": "…"}, {"id": "B", "says": "…"}]}]}
`+"```"+`

Don't ask about names or code structure: choose those yourself. If the issue isn't something the factory can do, write proposal.json with `+"`unsupported`"+` set to one sentence saying why.

# Check your plan

The `+"`check`"+` tool reads plan.json and tests/, checks that the plan holds together, and runs the acceptance tests on the code in repo/, where they must fail. For issues.json, it checks the existing plan-of-issues format and rules, without running acceptance tests. You have %d checks in total across both kinds of plan, so reread your files before each. When the check passes, you're done. Then reply with a two-sentence summary.

%s
`, req.Issue, MaxIssues, MaxPlanText, checks, synth.WriteAsYouGo("", "checks"))
	if req.Previous != nil && req.Previous.Plan != nil {
		b.WriteString("\n# The plan people asked to revise\n\nprevious/plan.json is the plan before. Keep what the discussion didn't ask to change.\n")
	} else if req.Previous != nil && req.Previous.IssuePlan != nil {
		b.WriteString("\n# The plan people asked to revise\n\nprevious/issues.json is the plan before. Keep what the discussion didn't ask to change.\n")
	}
	return b.String()
}
