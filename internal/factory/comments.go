package factory

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/verify"
)

// Every factory comment starts with this, so people can tell it apart from
// their own, even though it posts from their account.
const header = "◉ **Invariant** · "

// hashChars is how much of a proposal's hash a ratifying comment must quote.
const hashChars = 12

func post(title, body string, m Marker) string {
	return header + title + "\n\n" + strings.TrimSpace(body) + "\n\n" + m.encode() + "\n"
}

func forksComment(forks []formalize.Fork, m Marker) string {
	var b strings.Builder
	b.WriteString("Before I write down what must be true for this issue, I need you to decide:\n")
	for _, f := range forks {
		fmt.Fprintf(&b, "\n**%s. %s**\n\n", f.ID, f.Question)
		for _, o := range f.Options {
			fmt.Fprintf(&b, "- **%s.** %s\n", o.ID, o.Says)
		}
	}
	example := forks[0].ID + " " + forks[0].Options[0].ID
	fmt.Fprintf(&b, "\nAnswer each one with a comment like `/invariant choose %s`, one line per question. "+
		"Or say what you want in your own words, then comment `/invariant revise`.", example)
	return post("a decision for you", b.String(), m)
}

func proposalComment(p *formalize.Proposal, r *verify.Report, m Marker) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Here's what I propose must always be true for **%s**. Once you ratify it, these statements are pinned by hash, and I can't change them. "+
		"Then I'll write the code %s.\n\n", p.Name, formalize.Languages[p.Manifest().Language])
	b.WriteString("| Statement | Kind | Says |\n| :-- | :-- | :-- |\n")
	for _, s := range p.Statements {
		says := s.Says
		if s.Expect != "" {
			says += fmt.Sprintf(" It must break `%s`.", s.Expect)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", s.Name, s.Kind, says)
	}
	if r != nil {
		fmt.Fprintf(&b, "\n**Already checked** against a draft model, within %s: %s\n", bounds(p.Bounds), checked(r))
	}
	if len(m.Answers) > 0 {
		b.WriteString("\n**Decided on this issue:**\n\n")
		for _, a := range m.Answers {
			fmt.Fprintf(&b, "- %s. %s **%s.** %s (@%s)\n", a.Fork, a.Question, a.Option, a.Says, a.By)
		}
	}
	if pinned, err := p.Pinned(); err == nil {
		fmt.Fprintf(&b, "\n<details>\n<summary>The statements in TLA+</summary>\n\n```tla\n%s```\n\n</details>\n", pinned)
	}
	fmt.Fprintf(&b, "\nTo ratify exactly this, comment `/invariant ratify %s`. "+
		"To change anything, say what in a comment, then comment `/invariant revise`.", strings.TrimPrefix(p.Hash, "sha256:")[:hashChars])
	return post("proposal for ratification", b.String(), m)
}

// checked says in a sentence what the model check of a draft found.
func checked(r *verify.Report) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("TLC explored %s states and found no violation and no deadlock", thousands(r.Design.DistinctStates)))
	switch n := len(r.Witnesses); n {
	case 0:
	case 1:
		parts = append(parts, "the witness was reached")
	default:
		parts = append(parts, fmt.Sprintf("all %d witnesses were reached", n))
	}
	var bugs []string
	for _, g := range r.Bugs {
		bugs = append(bugs, "`"+g.Name+"`")
	}
	switch len(bugs) {
	case 0:
	case 1:
		parts = append(parts, "the known bug "+bugs[0]+" was caught")
	default:
		parts = append(parts, "the known bugs "+strings.Join(bugs, ", ")+" were caught")
	}
	if len(parts) == 1 {
		return parts[0] + "."
	}
	return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1] + "."
}

func stuckComment(problem string, m Marker) string {
	return post("I couldn't draft this", "I couldn't draft statements for this issue that check out. What went wrong:\n\n"+
		quote(problem, 40)+"\n\nSay what you'd like in a comment, then comment `/invariant revise` to try again.", m)
}

func unsupportedComment(reason string, m Marker) string {
	return post("not one I can take", reason+"\n\nIf I've misread the issue, say how in a comment, then comment `/invariant revise`.", m)
}

func noteComment(text string, m Marker) string { return post("note", text, m) }

func ratifiedComment(by, dir, branch, hash string, statements int, m Marker) string {
	return post("ratified", fmt.Sprintf("Ratified by @%s. I pinned %d statements in `%s` on branch `%s` (proposal `%s`). Writing the code now.",
		by, statements, dir, branch, short(hash)), m)
}

func prComment(pr github.PullRequest, r *verify.Report, m Marker) string {
	return post("pull request", fmt.Sprintf("The code is written, and it passed the gate here: it's **%s**. "+
		"I opened #%d, and I'll merge it once CI's `invariant/gate` passes on it.", r.Assurance, pr.Number), m)
}

func buildFailedComment(pr *github.PullRequest, res *synth.Result, runErr error, m Marker) string {
	var b strings.Builder
	switch {
	case res == nil || res.Final == nil:
		fmt.Fprintf(&b, "I couldn't build the code: %v", runErr)
	default:
		fmt.Fprintf(&b, "I couldn't write code that passes the gate in %d runs.", max(len(res.GateRuns), 1))
		if pr != nil {
			fmt.Fprintf(&b, " My last attempt is in draft pull request #%d.", pr.Number)
		}
		b.WriteString(" What failed:\n\n" + quote(verify.Feedback(res.Final), 60))
	}
	b.WriteString("\n\nA person needs to look at this.")
	return post("needs a person", b.String(), m)
}

func ciFailedComment(pr github.PullRequest, run github.CheckRun, m Marker) string {
	return post("needs a person", fmt.Sprintf("CI's `invariant/gate` %s on #%d ([run](%s)), so I won't merge it. A person needs to look at this.",
		strings.ReplaceAll(run.Conclusion, "_", " "), pr.Number, run.URL), m)
}

func scopeFailedComment(pr github.PullRequest, problems []string, m Marker) string {
	return post("needs a person", fmt.Sprintf("#%d is out of scope, so I won't merge it:\n\n- %s\n\nA person needs to look at this.",
		pr.Number, strings.Join(problems, "\n- ")), m)
}

func mergedComment(pr github.PullRequest, run github.CheckRun, sha string, r *Marker) string {
	return post("merged", fmt.Sprintf("CI's `invariant/gate` passed on #%d ([run](%s)), and I merged it as %s.", pr.Number, run.URL, sha), *r)
}

func closedComment(pr github.PullRequest, m Marker) string {
	return post("closed", fmt.Sprintf("#%d was closed without merging, so I've stopped. Comment `/invariant revise` to start again from a new proposal.", pr.Number), m)
}

func pullRequestBody(t Thread, m Marker, res *synth.Result, proposal *formalize.Proposal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Closes #%d.\n\n", t.Issue.Number)
	fmt.Fprintf(&b, "◉ Written by Invariant for #%d, against the statements ratified there.\n\n", t.Issue.Number)
	fmt.Fprintf(&b, "- **Project:** `%s`\n- **Ratified proposal:** `%s`\n", m.Project, short(m.Hash))
	if res != nil {
		gate := fmt.Sprintf("the gate never passed in %d runs", len(res.GateRuns))
		for _, g := range res.GateRuns {
			if g.Passed {
				gate = fmt.Sprintf("the gate passed on run %d", g.Run)
				break
			}
		}
		fmt.Fprintf(&b, "- **Synthesis:** %s, %d turns, %s\n", res.Usage.Backend, res.Usage.Turns, gate)
		if len(res.Tampered) > 0 {
			fmt.Fprintf(&b, "- **Discarded:** the agent edited protected files (%s), and its edits were thrown away\n", strings.Join(res.Tampered, ", "))
		}
	}
	if res != nil && res.Final != nil {
		b.WriteString("\n" + receipt.Markdown(res.Final))
	}
	return b.String()
}

// projectReadme introduces a project the factory built.
func projectReadme(t Thread, dir string, p *formalize.Proposal, answers []formalize.Answer, ratifiedBy, commentURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", capitalize(p.Name))
	fmt.Fprintf(&b, "Written by Invariant for [#%d](%s): %s.\n\n", t.Issue.Number, t.Issue.URL, t.Issue.Title)
	fmt.Fprintf(&b, "@%s ratified these statements [on the issue](%s). They're pinned by hash in [`ratified.lock`](.invariant/ratified.lock), "+
		"and their text is in [`%s`](%s).\n\n", ratifiedBy, commentURL, p.Manifest().Module, p.Manifest().Module)
	b.WriteString("| Statement | Kind | Says |\n| :-- | :-- | :-- |\n")
	for _, s := range p.Statements {
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", s.Name, s.Kind, s.Says)
	}
	if len(answers) > 0 {
		b.WriteString("\nDecided on the issue:\n\n")
		for _, a := range answers {
			fmt.Fprintf(&b, "- %s **%s.** %s (@%s)\n", a.Question, a.Option, a.Says, a.By)
		}
	}
	fmt.Fprintf(&b, "\nChecked within %s. To run the gate yourself:\n\n```bash\ngo run ./cmd/invariant verify %s\n```\n", bounds(p.Bounds), dir)
	return b.String()
}

func quote(text string, lines int) string {
	ls := strings.Split(strings.TrimSpace(text), "\n")
	if len(ls) > lines {
		ls = append(ls[:lines], "…")
	}
	return "> " + strings.Join(ls, "\n> ")
}

func bounds(b map[string]string) string {
	var parts []string
	for name, value := range b {
		parts = append(parts, fmt.Sprintf("`%s = %s`", name, value))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func short(hash string) string {
	if len(hash) > len("sha256:")+hashChars {
		return hash[:len("sha256:")+hashChars]
	}
	return hash
}

func thousands(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
