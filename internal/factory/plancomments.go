package factory

import (
	"fmt"
	"strings"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
)

// planComment proposes a plumbing plan for ratification (D-0105): what
// changes, the files the build may write, and the acceptance tests that
// show it works.
func planComment(p *formalize.Proposal, m Marker) string {
	plan := p.Plan
	var b strings.Builder
	fmt.Fprintf(&b, "Here's my plan for **%s**. It's plumbing, so it's tested, not proved. Once you ratify it, the plan and its acceptance tests are pinned by hash: "+
		"the build writes only the files it names, and it has to make every acceptance test pass without changing them.\n\n", plan.Name)
	fmt.Fprintf(&b, "**What changes.** %s\n\n", strings.TrimSpace(plan.Summary))
	b.WriteString("**The files the build may write:** " + codeList(plan.Files) + "\n\n")
	b.WriteString("**The acceptance tests,** which fail on the code as it is:\n\n| Test | Shows |\n| :-- | :-- |\n")
	for _, t := range plan.Tests {
		fmt.Fprintf(&b, "| `%s` in `%s` | %s |\n", t.Name, t.File, t.Says)
	}
	if len(plan.Trusted) > 0 {
		fmt.Fprintf(&b, "\nIt changes the trusted base (%s), so once CI's gate and a second agent's review pass, a person merges it.\n", codeList(plan.Trusted))
	} else {
		b.WriteString("\nOnce CI's gate and a second agent's review pass, I merge it.\n")
	}
	if len(m.Answers) > 0 {
		b.WriteString("\n**Decided on this issue:**\n\n")
		for _, a := range m.Answers {
			fmt.Fprintf(&b, "- %s. %s **%s.** %s (@%s)\n", a.Fork, a.Question, a.Option, a.Says, a.By)
		}
	}
	for _, f := range plan.TestFiles() {
		fmt.Fprintf(&b, "\n<details>\n<summary><code>%s</code></summary>\n\n```go\n%s```\n\n</details>\n", f, plan.Sources[f])
	}
	fmt.Fprintf(&b, "\nTo ratify exactly this, comment `/invariant ratify %s`. "+
		"To change anything, say what in a comment, then comment `/invariant revise`.", strings.TrimPrefix(p.Hash, "sha256:")[:hashChars])
	return post("plan for ratification", b.String(), m)
}

func planRatifiedComment(by, branch string, n int, p *formalize.Proposal, m Marker) string {
	return post("ratified", fmt.Sprintf("Ratified by @%s. I recorded the plan in `%s` on branch `%s`, with its %d acceptance tests (plan `%s`). Building it now.",
		by, plumbing.LockPath(n), branch, len(p.Plan.Tests), short(p.Hash)), m)
}

func planPRComment(pr github.PullRequest, res *plumbing.BuildResult, trusted []string, m Marker) string {
	merge := "I'll merge it once CI's `invariant/gate` passes on it."
	if len(trusted) > 0 {
		merge = fmt.Sprintf("It changes the trusted base (%s), so once CI's `invariant/gate` passes on it, a person merges it.", codeList(trusted))
	}
	return post("pull request", fmt.Sprintf("The change is built: gofmt, go vet, the tests of every package it touches and all %d acceptance tests pass, and a second agent's review approves it. I opened #%d. %s",
		res.Tests, pr.Number, merge), m)
}

func planFailedComment(pr *github.PullRequest, res *plumbing.BuildResult, runErr error, m Marker) string {
	var b strings.Builder
	switch {
	case res == nil:
		fmt.Fprintf(&b, "I couldn't build the plan: %v", runErr)
	case res.Passed && !res.Approved:
		b.WriteString("The change passes its checks, but the second agent's review asks for changes:\n\n" + quote(clip(res.Review, accountLimit), 120))
	default:
		fmt.Fprintf(&b, "I couldn't build a change that passes every check in %d test runs. What the factory's own run found:\n\n%s", max(len(res.TestRuns), 1), quote(res.Summary, 60))
		if account := strings.TrimSpace(res.Usage.Summary); account != "" {
			b.WriteString("\n\nThe agent's own account, which nothing checks:\n\n" + quote(clip(account, accountLimit), 120))
		}
	}
	if pr != nil {
		fmt.Fprintf(&b, "\n\nThe build is in draft pull request #%d.", pr.Number)
	}
	b.WriteString("\n\nA person needs to look at this. Comment `/invariant retry` to build again, or say what to change and comment `/invariant revise`.")
	return post("needs a person", b.String(), m)
}

func trustedComment(pr github.PullRequest, trusted []string, m Marker) string {
	return post("needs a person", fmt.Sprintf("CI's `invariant/gate` and the review passed on #%d, but it changes the trusted base: %s. Only a person merges that (D-0105), "+
		"so #%d is waiting for you to review and merge it.", pr.Number, codeList(trusted), pr.Number), m)
}

// planPullRequestBody is a plan's pull request: the plan, how the build
// went, and the second agent's review.
func planPullRequestBody(t Thread, m Marker, res *plumbing.BuildResult, trusted []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Closes #%d.\n\n", t.Issue.Number)
	fmt.Fprintf(&b, "◉ Built by Invariant for #%d, to the plan ratified there. It's plumbing: tested, not proved (D-0105).\n\n", t.Issue.Number)
	fmt.Fprintf(&b, "- **Plan:** `%s`, `%s`\n", m.Project, short(m.Hash))
	fmt.Fprintf(&b, "- **Build:** %s, %d turns, %d test runs\n", res.Usage.Backend, res.Usage.Turns, len(res.TestRuns))
	if len(res.Changes.Written) > 0 {
		fmt.Fprintf(&b, "- **Written:** %s\n", codeList(res.Changes.Written))
	}
	if len(res.Changes.Outside) > 0 {
		fmt.Fprintf(&b, "- **Thrown away:** the agent also changed %s, which the plan doesn't name\n", codeList(res.Changes.Outside))
	}
	if len(trusted) > 0 {
		fmt.Fprintf(&b, "- **Trusted base:** it changes %s, so a person merges it\n", codeList(trusted))
	}
	fmt.Fprintf(&b, "\n**The factory's own run:** %s", res.Summary)
	if res.Review != "" {
		b.WriteString("\n**The review,** by a second agent that can only read. It's an opinion, not evidence:\n\n" + quote(clip(res.Review, accountLimit), 200) + "\n")
	}
	return b.String()
}

// codeList is paths as code, joined.
func codeList(paths []string) string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = "`" + p + "`"
	}
	return strings.Join(out, ", ")
}
