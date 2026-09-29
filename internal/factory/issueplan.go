package factory

import (
	"context"
	"fmt"
	"strings"

	"github.com/gitdek/invariant/factory/recovery/recovery"
	"github.com/gitdek/invariant/internal/formalize"
)

// Plans of issues (D-0105, #112). A writer's /invariant plan on an issue
// that holds or links a PRD has the factory draft a plan of issues in place
// of statements: the issues that carry the PRD out, in order, each modeled
// or plumbing. People answer its forks and ask for revisions as they do for
// any draft, and ratify it by its hash, as the protocol has it. Its
// ratification is recorded on the issue, and the plan itself builds nothing.

// isPRD says whether a writer asked for a plan of issues on the issue. From
// then on, the issue is a PRD through its forks, answers and revisions,
// whatever its own Kind:, Project: and Code: lines say.
func isPRD(t Thread) bool { return hasVerb(t.Commands, Plan) }

// isIssuePlan says whether a post's proposal is a plan of issues.
func isIssuePlan(m Marker) bool { return m.Proposal != nil && m.Proposal.IssuePlan != nil }

// ratifyIssuePlan records a writer's ratification of a plan of issues, once
// the protocol allows it: one post, in answer to the ratifying comment. It
// pushes nothing and builds nothing.
func (f *Factory) ratifyIssuePlan(ctx context.Context, t Thread, state Post, c Command) error {
	n, p := t.Issue.Number, state.Marker.Proposal
	f.logf("#%d: ratified by @%s", n, c.By)
	// What's ratified must be exactly what was proposed.
	if h := p.IssuePlan.Hash(); h != p.Hash {
		return fmt.Errorf("the plan of issues hashes to %s, not the proposal's %s", h, p.Hash)
	}
	named := p.Hash
	if !matches(c.Args, p.Hash) {
		named = "the proposal " + strings.Join(c.Args, " ")
	}
	before, after := f.ratifyStep(t, state, c, named, "")
	if err := f.allowed(ctx, n, "ratify "+named, before, after); err != nil {
		return err
	}
	// There's nothing to push, so everything the ratification does before
	// it's posted is done.
	if err := f.recovers(ctx, n, "say it's ratified", f.canPost(ratifying(true), recovery.Ratify)); err != nil {
		return err
	}
	m := Marker{Kind: KindRatified, ReplyTo: []int64{c.Comment}, Answers: state.Marker.Answers, Proposal: p, Hash: p.Hash}
	return f.say(ctx, n, issuePlanRatifiedComment(c.By, p, m), "")
}

// issuePlanComment proposes a PRD's plan of issues for ratification: its
// name, and each issue's title and whole body, in order.
func issuePlanComment(p *formalize.Proposal, m Marker) string {
	plan := p.IssuePlan
	var b strings.Builder
	fmt.Fprintf(&b, "Here's my plan of issues for **%s**: %s, in order. Once you ratify it, the plan is pinned by hash: "+
		"these issues, in this order, each with exactly this title and body.\n\n", plan.Name, issueCount(len(plan.Issues)))
	fmt.Fprintf(&b, "**What they do.** %s\n", strings.TrimSpace(plan.Summary))
	for i, is := range plan.Issues {
		fmt.Fprintf(&b, "\n**%d. %s**\n\n%s\n", i+1, is.Title, blockquote(is.Body))
	}
	if len(m.Answers) > 0 {
		b.WriteString("\n**Decided on this issue:**\n\n")
		for _, a := range m.Answers {
			fmt.Fprintf(&b, "- %s. %s **%s.** %s (@%s)\n", a.Fork, a.Question, a.Option, a.Says, a.By)
		}
	}
	fmt.Fprintf(&b, "\nTo ratify exactly this, comment `/invariant ratify %s`. "+
		"To change anything, say what in a comment, then comment `/invariant revise`.", strings.TrimPrefix(p.Hash, "sha256:")[:hashChars])
	return post("plan of issues for ratification", b.String(), m)
}

func issuePlanRatifiedComment(by string, p *formalize.Proposal, m Marker) string {
	plan := p.IssuePlan
	return post("ratified", fmt.Sprintf("Ratified by @%s. I recorded the plan of issues for **%s** here, with its %s (plan `%s`). "+
		"The plan itself builds nothing, so this issue gets no branch, build or pull request of its own.", by, plan.Name, issueCount(len(plan.Issues)), short(p.Hash)), m)
}

// issueCount says how many issues there are: one issue, or 3 issues.
func issueCount(n int) string {
	if n == 1 {
		return "one issue"
	}
	return fmt.Sprintf("%d issues", n)
}

// blockquote quotes text, every line of it.
func blockquote(text string) string {
	return "> " + strings.ReplaceAll(strings.Trim(text, "\n"), "\n", "\n> ")
}
