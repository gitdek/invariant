package plumbing

import (
	"fmt"
	"strings"

	"github.com/gitdek/invariant/internal/synth"
)

// BuildPrompt is the task for the agent that builds a ratified plan.
func BuildPrompt(plan *Plan, n, runs int) string {
	var b strings.Builder
	fmt.Fprintf(&b, `You're building issue #%d's ratified plan in this repository: Invariant, a Go project. A person ratified the plan by its hash, so the plan is the job, and nothing beyond it.

## The plan

%s

**The files you may write,** along with the new regression tests described below. Other changes don't count, and the factory throws them away:

`, n, strings.TrimSpace(plan.Summary))
	for _, f := range plan.Files {
		fmt.Fprintf(&b, "- `%s`\n", f)
	}
	b.WriteString("\nYou may also add new regular Go `*_test.go` regression files in packages the plan touches, including the nearest Go package owning a planned embedded asset and new packages the plan names. The original checkout stays the baseline for every repair, so you may correct or remove your own new tests later. This does not permit changes to existing unnamed tests, other production files or unrelated packages.\n")
	b.WriteString("\n**The acceptance tests.** They're already in the repository, and you can't change them. Your change has to make every one pass:\n\n")
	for _, t := range plan.Tests {
		if t.Native() {
			fmt.Fprintf(&b, "- %q (%s): %s\n", t.Name, t.Identity(), strings.TrimSpace(t.Says))
		} else {
			fmt.Fprintf(&b, "- `%s` in `%s`: %s\n", t.Name, t.File, strings.TrimSpace(t.Says))
		}
	}
	if plan.Execution != nil {
		fmt.Fprintf(&b, "\nCaptured execution: %s (base provenance %s). Native selectors and verbatim sources stay pinned. Configured Go and Python use default Go and pytest adapters under captured policy. Every applicable command and exactly one runtime pass per selector must complete; compilation success, diagnostics and suite success cannot supply acceptance evidence. Tool and fresh final checks stage only planned files, pinned acceptance and allowed new Go regressions against the original checkout. Candidate configuration and replacement locks cannot supply authority. Pytest uses direct pytest or python3 -m pytest with supported captured flags and one literal exact node, never a shell or -k. It needs one collected item, its actual call, matching reports, completed teardown/session/process and fresh invocation/identity/source/configuration bindings. Skips, xfail/xpass, groups, absent calls and forged or incomplete evidence are refused. Only structured missing local production API imports directly used by the selected pinned function or method may qualify base; unrelated imports, missing distributions, fixtures and broken configuration are setup failures. Every correction still needs a runtime pass. Checks reuse captured dependencies without fetching pytest. The Python 3.13/pytest 8.3.5 offline pinned-image matrix uses INVARIANT_PYTEST_ACCEPTANCE_REAL=1 with explicit image/platform and fails on absent prerequisites. Vitest, Jest, mixed unsupported plans and configured legacy acceptance are refused before preparation. External plumbing intake remains disabled.\n", plan.Execution.Digest, plan.Execution.Base)
	}
	fmt.Fprintf(&b, `
The plan's record is `+"`%s`"+`.

## How to work

1. Read AGENTS.md for the repository's rules, then the acceptance tests, then the code the plan changes.
2. Write the change in the files the plan names and add regression tests within the allowance above. Write it the way the code around it is written: its naming, its comment density, its idiom. Keep to what the plan says.
3. Run the `+"`test`"+` tool. It copies the repository with your changes to the plan's files and allowed new regression tests and runs, in a sandbox with no network: gofmt on the files the change touches, `+"`go vet ./...`"+`, the tests of every package the change touches, and the acceptance tests. CI runs every test before anything merges. You have %d runs, so use them to check your work, not to explore.
4. When every check passes, stop, and say in a few sentences what you changed and any call you made that the plan didn't settle.

The factory runs fresh checks and a read-only review of the whole candidate against the original checkout. If review asks for changes, you get its findings in this same workspace under the unchanged plan, for at most two repair attempts. Each repair needs fresh factory checks and, if they pass, fresh review. The initial build, fallback, checks, repairs and reviews share one execution time allowance, excluding waits for an agent account's turn; no round renews it. Only final passing checks and approval produce a ready pull request. Otherwise the factory leaves a draft for a person with the unresolved findings, including when a repair fails tests or runs out of time. If a finding needs a different plan, explain that without expanding your scope.

SPEC.md and the README describe Invariant as it is (D-0122). When the plan names either one, change it as the plan's summary says. The README is the front page, at most 400 lines, so detail goes in the page under docs/ it links, which describes Invariant as it is too (D-0294). A new or changed SPEC bullet cites the decisions it rests on, as `+"`decisions check`"+` requires, and only decisions already recorded, since you can't write decisions/. When the change needs a decision no one has recorded yet, say so when you stop, and whoever merges it records the decision.

%s

You have no shell and no network. Don't change the acceptance tests, the plan's record, or anything under decisions/. Write only the files the plan names and the allowed new regression tests.
`, LockPath(n), runs, synth.WriteAsYouGo("", "test runs"))
	return b.String()
}

// ReviewPrompt is the task for the second agent, which reviews a build
// against its plan with tools that only read.
func ReviewPrompt(plan *Plan, n int) string {
	return fmt.Sprintf(`You're reviewing a change a coding agent made for issue #%d of Invariant, a Go project, before it merges. You can only read.

This directory is readable, although it sits in the temporary directory your sandbox otherwise denies, and its real path may begin with /private on macOS. Read its files by the relative paths below. Report an access problem only if reading one of them actually fails.

In this directory:
- plan.json: the plan a person ratified. It says what changes, which files the build may write, and the acceptance tests that show it works.
- change.diff: the whole candidate's changes and additions against the original checkout, including after a repair.
- new-files.txt: the files it added, which you can read under repo/.
- tests.txt: the factory's own run, on the change, of gofmt, go vet, the tests of every package it touches, and the acceptance tests.
- repo/: the repository with the change in it. AGENTS.md there holds its rules.
- base/: the original checkout, unchanged across every repair and review.

Check, citing file:line for each finding:
1. The change does what the plan's summary says, and nothing more.
2. The acceptance tests test what they say, and the code passes them by doing what they test, not by special-casing them.
3. Nothing weakens a check, reaches the network or the host, or leaks a secret.
4. It reads like the code around it: its naming, its comment density, its idiom.

The build may add new regular Go *_test.go regression files in packages the plan touches, including the nearest Go package owning a planned embedded asset and new packages the plan names. The original checkout remains the baseline across repairs: newly added tests may be corrected or removed, but existing unnamed tests, the ratified acceptance tests and plan, other production files and unrelated packages remain protected. Review the whole candidate, not just the latest repair.

List decision recording and checks that require unavailable Docker or network access under **Merger work**. The plumbing build cannot write decisions/, and those unavailable checks are the merger's responsibility. CI runs the opt-in real Python and TLAPS suites on a pull request whose change can affect them, and the gate requires them, so don't list those; the plumbing sandbox's real-image budget check is still the merger's. If these are all that remains, and the candidate otherwise satisfies the plan and factory checks, use VERDICT: approve. They do not require a repair. Concrete defects within the candidate still require changes; cite each finding so the same builder can repair it within the unchanged plan.

The plan's summary, for reference: %s

Put findings and any Merger work before your final line. End your reply with exactly one line: `+"`VERDICT: approve`"+` if the candidate is ready, or `+"`VERDICT: changes needed`"+` if it needs repair.
`, n, strings.TrimSpace(plan.Summary))
}
