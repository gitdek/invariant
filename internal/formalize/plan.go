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
	if f.Sandbox.Repository != nil {
		var cancel context.CancelFunc
		ctx, cancel = synth.WithExecutionBudget(ctx, f.Timeout)
		defer cancel()
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(filepath.Join(out, "previews")); err != nil {
		return nil, err
	}
	// The check tool's build cache serves only this draft's agent, whose
	// run has ended when Plan returns; the factory's own check of the plan
	// runs without it. The run's records stay (#366).
	cache := filepath.Join(out, "gocache")
	defer plumbing.RemoveCache(cache)
	ws, err := os.MkdirTemp("", "invariant-plan-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(ws)
	authority := req.CheckContext()
	authority.Planning = "plumbing"
	_, release, err := stageCheckContext(ws, authority)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := stagePreviousPreviews(ws, req.Previous); err != nil {
		return nil, err
	}
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
	gateServer := []string{f.Binary, "mcp", "-plan", "-max-runs", fmt.Sprint(f.CheckRuns), "-log", checkLog, "-cache", cache, "-parent-plan", fmt.Sprint(req.ParentPlan), ws}
	if f.Sandbox.Repository != nil {
		queue, err := plumbing.NewCheckQueue(1)
		if err != nil {
			return nil, err
		}
		defer queue.Close()
		f.Sandbox.Original = repo
		client, finish, err := queue.RegisterPlan(ctx, f.Sandbox, repo)
		if err != nil {
			return nil, err
		}
		defer finish()
		gateServer = append(gateServer[:len(gateServer)-1], "-check-queue", client.Address, "-check-build", client.Build, ws)
		f.Sandbox.Checks = client
	}
	job := synth.Job{
		Workspace:  ws,
		Prompt:     PlanPrompt(req, f.CheckRuns),
		GateServer: gateServer,
		Tools:      []string{"check"},
		Transcript: transcript,
		Timeout:    f.Timeout,
	}
	if f.Sandbox.Repository != nil {
		job.ToolTimeout = f.Timeout
		if f.Timeout <= 0 {
			job.ToolTimeout = -1
		}
	}
	usage, _, runErr := synth.RunAgent(ctx, f.Backend, job, f.Timeout, "")
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
	if synth.ProviderUnavailable(runErr) {
		r.Problem = unfinished(runErr).Error()
		return r, nil
	}
	fail := func(problem string) (*Result, error) {
		if runErr != nil {
			problem = fmt.Sprintf("the agent's run failed (%v), and it left no usable plan: %s", runErr, problem)
		}
		r.Problem = problem
		return r, nil
	}
	if err := CheckStepDraft(ws, req.ParentPlan); err != nil {
		return fail(err.Error())
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
		if len(p.Forks) > 0 {
			if err := p.checkPreviews(ctx, f.Toolchain, authority); err != nil {
				return fail(err.Error())
			}
		}
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
	scope := "Cover the whole issue. If all the work fits in one reviewable pull request, write plan.json and its acceptance tests. Otherwise write issues.json, a plan of issues that together cover all the work, as described below. Write only one kind of plan."
	if req.ParentPlan != 0 {
		scope = "Cover the whole step in one reviewable pull request. Write plan.json and its acceptance tests, or proposal.json with forks to ask." + stepPrompt(req.ParentPlan)
	}
	fmt.Fprintf(&b, `You're planning a change to this repository for issue #%d. It isn't a state machine to model and prove. It's plumbing: code that's tested, not proved (D-0105). A person ratifies your plan by its hash, and then another agent builds exactly what it says, so the plan is the contract.

request.md holds the issue, what's been decided, and the discussion. repo/ is the repository as it is on the base branch. Read what the change touches, and AGENTS.md for the repository's rules. Don't change anything in repo/: it's there to read.

# Write the plan

%s

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

- Before naming files, look for the existing tests and fakes that check the behavior the change alters, including command lines, flags and output a fake validates. Name every existing test file the change needs in the plan's files.
- **files** names the files the build may change or add. Name every production file and existing test the change needs, and nothing it doesn't. Paths are from the repository's root. Invariant's built-in profile refuses .github/. Only the configured native profile admits explicitly named CI files, which always require a person to merge.
- The build may also add new regular Go `+"`*_test.go`"+` regression files in packages the plan touches, including the nearest Go package owning a planned embedded asset and new packages the plan names. In captured Python and TypeScript parts, new regular pytest, Vitest and Jest regression files may also live anywhere in the touched part, including separate and sibling test areas, when conventional filenames intersect the captured suite targets and default or supported literal discovery settings. Unsupported or executable discovery policy grants no implicit allowance. Test configuration is protected; candidate policy edits cannot grant permission or hide admitted tests. See docs/factory.md for supported forms. These need not be listed in **files**. The original checkout stays the baseline across repairs, so the builder may correct or remove its own new tests. This allowance never permits changes to existing unnamed tests, the ratified acceptance tests or plan, other production files or unrelated packages.
- **tests** are the acceptance tests: Go test functions that show the change works, each in a new file that isn't in repo/ yet, named like `+"`*_accept_test.go`"+`. Write each file under tests/, at its path in the repository: tests/internal/dashboard/graph_accept_test.go. The build can't change them, so write them against the change's intended API, and test what it does, not how.
- An acceptance test's expectations about behavior the plan keeps must agree with the existing tests that pin it. When the plan intentionally changes pinned behavior, say so in its summary and name those existing tests in its files.
- Each test must fail on the code as it is. A test that already passes can't show anything.

Native acceptance records explicitly add part, framework and selector together. Framework is go, pytest, vitest or jest; name is then only a display label. The full part/framework/file/selector identity and verbatim new source are pinned. Go selectors name a declared Test function and optional exact subtest path; pytest selectors are part-relative file::function or file::Class::method node IDs with concrete parameter cases; Vitest and Jest selectors are complete nested titles including a concrete case. Never supply execution, sources or trusted in plan.json: the caller captures authority. Configured Go, Python and TypeScript use the default Go, pytest, Vitest and Jest adapters and check every selector against the independent captured base. Pytest supports direct pytest or python3 -m pytest with one standalone {test} argument and supported captured flags; wrappers, -k, groups and ambiguous collection are refused. Exactly one collected item, its actual call, teardown, session and process must complete on separate evidence channels. Missing local production API imports qualify only with structured exceptions and conservative source analysis proving the selected test uses the API unconditionally from its body's start, never after return or raise or within uncertain control flow. Every corrected candidate needs a normal runtime pass. Each Go and Python process gets fresh authoritative transport source at a new private path; Go compiles it there. No check fetches pytest. The real Python 3.13/pytest 8.3.5 offline pinned-image opt-in is documented in docs/factory.md. Each needs exactly one completed test failure or attributable proposed production API compiler failure; passing, skipped, absent, unfinished or mismatched execution cannot qualify. Infrastructure, deadline, cancellation and cleanup failures veto the draft. The check tool and fresh final validation use the same retained authority. Vitest, Jest and mixed Go/pytest/Vitest/Jest plans use the same captured lifecycle. Unsupported frameworks and configured legacy records are refused before preparation. Unconfigured Invariant Go plans omit all native fields and keep the built-in runner, hashes and locks. External plumbing intake remains disabled. See docs/factory.md for the schema. Node acceptance uses direct vitest run or jest, one -t/--testNamePattern {test}, an optional confined --config and positive --testTimeout; Jest also accepts --runInBand. Both narrow to an exact part-relative file and escaped anchored full title, with one concrete test.each/it.each case. npm/npx/shell wrappers, watch/list modes, extra targets, unknown/conflicting flags and reporter/output overrides are refused before preparation. Factory-owned observers bind the actual call, hooks, suite/session completion and normal drained process exit to fresh identities and pinned sources. Only prepared locked dependencies supply frameworks, transformers and parsers; no runtime installs occur. A Node-only transport is staged from fresh host bytes per invocation. Node configuration is literal JSON or a literal module.exports/default-export object, never executable imports or calls. The precheck honors Vitest globals and captured Jest inline compiler settings, with ambient types resolved from locked dependencies. Framework tests execute in separate worker processes, including a single-file Jest run. Proposed local API attribution requires structured diagnostics, registrar and expect bindings resolved to named imports from the locked vitest or @jest/globals package, and conservative analysis of an unconditional direct use by that exact test in an otherwise working production module containing only named function declarations and simple literal-initialized identifier bindings; destructuring and side-effecting declarations are refused without evaluation, and every correction needs a runtime pass. The Node and four-framework offline opt-ins and required pinned images are documented in docs/factory.md.

Configured trust comes from the captured base contract: CI and the gate workflow (.github/, .gitlab-ci.yml and .circleci/), the repository contract, root and nested AGENTS.md and CLAUDE.md, root and nested .codex/, .claude/ and .mcp.json, declared agent rules, dependency inputs, lockfiles and additional trusted paths. The proposal displays derived trusted paths, but that list is not merge authority. Scope uses every actual changed path, including deletions and allowed regressions, under the original base policy and revalidates execution against the current base. Removing declarations in the candidate cannot weaken that boundary. Unrelated compatible base movement is allowed. Successful configured trusted builds remain drafts for a person through recovery and retries. Workflow edits also need publishing credentials with workflow write permission; a refused push preserves the work and waits for a writer's retry after those credentials are fixed. Ordinary work needs no workflow permission, Invariant's App permissions stay unchanged, and external issue intake remains disabled.

After the build passes factory checks, a second agent reviews the whole candidate against the original checkout. Findings return to the same builder in its existing workspace under this unchanged plan for at most two repair attempts, each followed by fresh checks and, if they pass, a fresh review. Final passing checks and approval are required for readiness; configured trusted builds still stay drafts for a person. Unresolved findings, failed checks or an exhausted time allowance leave a draft for a person. Decision recording and checks requiring unavailable Docker or network access belong under Merger work in the review, and do not by themselves require a repair. CI runs the opt-in real Python and TLAPS suites on a pull request whose change can affect them, so they aren't Merger work.

SPEC.md and the README describe Invariant as it is, and a change that makes either one wrong fixes it in the same change (D-0122). When yours does, name that file among the plan's files, and say in the summary what changes there. The README is the front page, at most 400 lines, so detail belongs in the page under docs/ it links, which describes Invariant as it is too: name that page instead when the detail is what changes (D-0294). A new or changed SPEC bullet cites the decisions it rests on, as `+"`decisions check`"+` requires. A plumbing build can't write decisions/, so when the change needs a decision no one has recorded yet, say so in the summary, and whoever merges it records the decision.
`, req.Issue, scope)
	issueCheck := ""
	if req.ParentPlan == 0 {
		issueCheck = "For issues.json, it checks the existing plan-of-issues format and rules, without running acceptance tests. "
		fmt.Fprintf(&b, `

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

- **issues** are 1 to %d, in the order they're taken. Each is opened once the one before it has merged, so it can build on the ones before it, and never on one after. Together they cover the whole issue; don't leave work for an unplanned follow-up. Each step must fit one complete plumbing or modeled change in one reviewable pull request and cannot become another plan of issues.
- Each **title** is one line. Each **body** is the whole issue: what changes and why, complete enough that someone who hasn't read this issue can take it on, with the decisions it rests on.
- Each body has either a `+"`Kind: plumbing`"+` line for tested code, with no `+"`Code:`"+` line, or a `+"`Project: <directory>`"+` line for a state machine whose rules are worth proving, never both. A project directory is a clean path inside the repository, outside .git/ and .github/.
- No body has an `+"`/invariant`"+` command on a line of its own. The factory takes each issue itself.
- The name, summary, titles and bodies together come to at most %d bytes, so the proposal fits in one comment. None has an HTML comment, which the proposal wouldn't show.

A person ratifies this plan's hash before any issue opens.
`, MaxIssues, MaxPlanText)
	}
	fmt.Fprintf(&b, `

# Ask when you must

If the issue allows materially different changes, don't guess. Write proposal.json with forks instead of a plan, and people will answer:

`+"```json"+`
{"forks": [{"id": "F1", "question": "…?", "options": [{"id": "A", "says": "…"}, {"id": "B", "says": "…"}]}]}
`+"```"+`

Don't ask about names or code structure: choose those yourself. If the issue isn't something the factory can do, write proposal.json with `+"`unsupported`"+` set to one sentence saying why.

# Check your plan

The `+"`check`"+` tool reads plan.json and tests/, checks that the plan holds together, and runs the acceptance tests on the code in repo/, where they must fail. %sYou have %d checks in total, so reread your files before each. When the check passes, you're done. Then reply with a two-sentence summary.

%s
`, issueCheck, checks, synth.WriteAsYouGo("", "checks"))
	if req.Previous != nil && req.Previous.Plan != nil {
		b.WriteString("\n# The plan people asked to revise\n\nprevious/plan.json is the plan before. Keep what the discussion didn't ask to change.\n")
	} else if req.Previous != nil && req.Previous.IssuePlan != nil {
		b.WriteString("\n# The plan people asked to revise\n\nprevious/issues.json is the plan before. Keep what the discussion didn't ask to change.\n")
	}
	return b.String()
}
