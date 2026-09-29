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

**The files you may write.** Anything else you change doesn't count, and the factory throws it away:

`, n, strings.TrimSpace(plan.Summary))
	for _, f := range plan.Files {
		fmt.Fprintf(&b, "- `%s`\n", f)
	}
	b.WriteString("\n**The acceptance tests.** They're already in the repository, and you can't change them. Your change has to make every one pass:\n\n")
	for _, t := range plan.Tests {
		fmt.Fprintf(&b, "- `%s` in `%s`: %s\n", t.Name, t.File, strings.TrimSpace(t.Says))
	}
	fmt.Fprintf(&b, `
The plan's record is `+"`%s`"+`.

## How to work

1. Read AGENTS.md for the repository's rules, then the acceptance tests, then the code the plan changes.
2. Write the change in the files the plan names. Write it the way the code around it is written: its naming, its comment density, its idiom. Keep to what the plan says.
3. Run the `+"`test`"+` tool. It copies the repository with your changes to the plan's files and runs, in a sandbox with no network: gofmt on the files the change touches, `+"`go vet ./...`"+`, the tests of every package the change touches, and the acceptance tests. CI runs every test before anything merges. You have %d runs, so use them to check your work, not to explore.
4. When every check passes, stop, and say in a few sentences what you changed and any call you made that the plan didn't settle.

SPEC.md and the README describe Invariant as it is (D-0122). When the plan names either one, change it as the plan's summary says. A new or changed SPEC bullet cites the decisions it rests on, as `+"`decisions check`"+` requires, and only decisions already recorded, since you can't write decisions/. When the change needs a decision no one has recorded yet, say so when you stop, and whoever merges it records the decision.

%s

You have no shell and no network. Don't change the acceptance tests, the plan's record, or anything under decisions/, and don't write anywhere but the files the plan names.
`, LockPath(n), runs, synth.WriteAsYouGo("", "test runs"))
	return b.String()
}

// ReviewPrompt is the task for the second agent, which reviews a build
// against its plan with tools that only read.
func ReviewPrompt(plan *Plan, n int) string {
	return fmt.Sprintf(`You're reviewing a change a coding agent made for issue #%d of Invariant, a Go project, before it merges. You can only read.

In this directory:
- plan.json: the plan a person ratified. It says what changes, which files the build may write, and the acceptance tests that show it works.
- change.diff: the change to files that already existed.
- new-files.txt: the files it added, which you can read under repo/.
- tests.txt: the factory's own run, on the change, of gofmt, go vet, the tests of every package it touches, and the acceptance tests.
- repo/: the repository with the change in it. AGENTS.md there holds its rules.

Check, citing file:line for each finding:
1. The change does what the plan's summary says, and nothing more.
2. The acceptance tests test what they say, and the code passes them by doing what they test, not by special-casing them.
3. Nothing weakens a check, reaches the network or the host, or leaks a secret.
4. It reads like the code around it: its naming, its comment density, its idiom.

The plan's summary, for reference: %s

End your reply with exactly one line: `+"`VERDICT: approve`"+` if it can merge as it is, or `+"`VERDICT: changes needed`"+` followed by what has to change.
`, n, strings.TrimSpace(plan.Summary))
}
