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

**The files the plan names:**

`, n, strings.TrimSpace(plan.Summary))
	for _, f := range plan.Files {
		fmt.Fprintf(&b, "- `%s`\n", f)
	}
	b.WriteString("\nYou may also add regular Go `*_test.go` regression files in packages the plan touches, including the nearest Go package owning a planned embedded asset and new packages named by the plan. New means absent from the original checkout before this build, so you may correct or remove your added regression files in a later repair. Existing unnamed tests, other production files and unrelated packages stay outside your writable scope. Anything else you change doesn't count, and the factory throws it away.\n")
	b.WriteString("\n**The acceptance tests.** They're already in the repository, and you can't change them. Your change has to make every one pass:\n\n")
	for _, t := range plan.Tests {
		fmt.Fprintf(&b, "- `%s` in `%s`: %s\n", t.Name, t.File, strings.TrimSpace(t.Says))
	}
	fmt.Fprintf(&b, `
The plan's record is `+"`%s`"+`.

## How to work

1. Read AGENTS.md for the repository's rules, then the acceptance tests, then the code the plan changes.
2. Write the change in the files the plan names, adding regression tests within the allowance above when needed. Write it the way the code around it is written: its naming, its comment density, its idiom. Keep to what the plan says.
3. Run the `+"`test`"+` tool. It copies the repository with your changes to the plan's files and allowed new regression tests and runs, in a sandbox with no network: gofmt on the files the change touches, `+"`go vet ./...`"+`, the tests of every package the change touches, and the acceptance tests. CI runs every test before anything merges. You have %d runs, so use them to check your work, not to explore.
4. When every check passes, stop, and say in a few sentences what you changed and any call you made that the plan didn't settle.

After the factory's own checks pass, a second agent reviews the whole candidate. If it asks for changes, you may receive its findings here for at most two repair attempts in this same workspace, under the unchanged plan and with only the build's remaining time. Fix what the plan permits and add regression coverage where useful. Say which findings require a different plan rather than expanding your scope. Every repair gets fresh factory checks and, only if they pass, a fresh read-only review against the original checkout.

SPEC.md and the README describe Invariant as it is (D-0122). When the plan names either one, change it as the plan's summary says. A new or changed SPEC bullet cites the decisions it rests on, as `+"`decisions check`"+` requires, and only decisions already recorded, since you can't write decisions/. When the change needs a decision no one has recorded yet, say so when you stop, and whoever merges it records the decision.

%s

You have no shell and no network. Don't change the acceptance tests, the plan's record, or anything under decisions/, and write only the files the plan names and the allowed new regression tests.
`, LockPath(n), runs, synth.WriteAsYouGo("", "test runs"))
	return b.String()
}

// ReviewPrompt is the task for the second agent, which reviews a build
// against its plan with tools that only read.
func ReviewPrompt(plan *Plan, n int) string {
	return fmt.Sprintf(`You're reviewing a change a coding agent made for issue #%d of Invariant, a Go project, before it merges. You can only read.

In this directory:
- plan.json: the plan a person ratified. It says what changes, which files the build may write, and the acceptance tests that show it works.
- change.diff: the whole candidate's changes against the original checkout, including additions, deletions and all repairs so far.
- new-files.txt: all files the candidate added since the original checkout, which you can read under repo/.
- tests.txt: the factory's own run, on the change, of gofmt, go vet, the tests of every package it touches, and the acceptance tests.
- repo/: the repository with the change in it. AGENTS.md there holds its rules.
- original/: the unchanged checkout before the build, for comparison.

Check, citing file:line for each finding:
1. The change does what the plan's summary says, and nothing more.
2. The acceptance tests test what they say, and the code passes them by doing what they test, not by special-casing them.
3. Nothing weakens a check, reaches the network or the host, or leaks a secret.
4. It reads like the code around it: its naming, its comment density, its idiom.

Review the whole candidate against the unchanged plan, not just the latest repair. The build may add regular Go *_test.go regression files in packages the plan touches, including the nearest Go package owning a planned embedded asset and new packages named by the plan. Those additions are allowed without changing the plan. Existing unnamed tests, the ratified acceptance tests and plan, other production files and unrelated packages remain protected.

Under a separate "Merger work" heading, list decision recording and checks that require Docker or network access unavailable in this sandbox. The build cannot write decisions/, and the merger must record any new policy and run those unavailable checks. These duties alone do not require a repair: if they are all that remains, end with VERDICT: approve. Report code defects, scope violations and failed available checks as findings that need changes.

The plan's summary, for reference: %s

Give any findings and Merger work before your verdict. End your reply with exactly one line: `+"`VERDICT: approve`"+` if the candidate is ready with any listed merger duties, or `+"`VERDICT: changes needed`"+` if the candidate needs changes.
`, n, strings.TrimSpace(plan.Summary))
}
