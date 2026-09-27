package factory

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/verify"
)

// Every factory comment starts with this, so people can tell it apart from
// their own, even though it posts from their account.
const header = "◉ **Invariant** · "

// hashChars is how much of a proposal's hash a ratifying comment must quote.
const hashChars = 12

// maxComment keeps a post under GitHub's limit of 65,536 characters, with
// room to spare. A post GitHub refuses would be drafted again, by an agent,
// on every poll.
const maxComment = 60000

// details are a post's collapsed sections, such as the TLA+ a proposal
// changes. The marker and the proposal's hash already pin what they show.
var details = regexp.MustCompile(`(?s)\n*<details>.*?</details>\n*`)

func post(title, body string, m Marker) string {
	marker := m.encode()
	compose := func(body string) string {
		return header + title + "\n\n" + strings.TrimSpace(body) + "\n\n" + marker + "\n"
	}
	out := compose(body)
	if len(out) <= maxComment {
		return out
	}
	const short = "\n\n_Some detail is left out, because GitHub limits how long a comment can be._"
	body = details.ReplaceAllString(body, "\n\n")
	if room := maxComment - len(compose("")) - len(short); len(body) > room {
		body = strings.ToValidUTF8(body[:max(room, 0)], "")
	}
	return compose(strings.TrimSpace(body) + short)
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

// thenWhat says what the factory does once people ratify: write or change
// code in the project's language, or, for existing code, run it as it is
// against the model (D-0054).
func thenWhat(p *formalize.Proposal, verb string) string {
	if e := p.Manifest().Existing; len(e) > 0 {
		return "I'll write a driver that runs " + list(e) + " as it is, and CI will check every step the code takes against the model. I never change that code"
	}
	return "I'll " + verb + " " + formalize.Languages[p.Manifest().Language]
}

func proposalComment(p *formalize.Proposal, r *verify.Report, m Marker) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Here's what I propose must always be true for **%s**. Once you ratify it, these statements are pinned by hash, and I can't change them. "+
		"Then %s.\n\n", p.Name, thenWhat(p, "write the code"))
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

// amendmentComment proposes changes to an existing project: what's
// ratified now, what changes, and a callout for anything removed or any
// invariant changed, since that can loosen what was promised (D-0045).
func amendmentComment(p *formalize.Proposal, r *verify.Report, ch *formalize.Changes, m Marker) string {
	var b strings.Builder
	t := p.Target
	fmt.Fprintf(&b, "Here's how I propose to change **%s**, in `%s`. It amends the statements ratified in %s (proposal `%s`). "+
		"Once you ratify it, the whole new set is pinned by hash. Then %s.\n\n",
		p.Name, t.Dir, t.Previous, short(t.Amends), thenWhat(p, "change the code"))
	if ch == nil {
		ch = &formalize.Changes{}
	}
	names := func(cs []formalize.Change) string {
		var out []string
		for _, c := range cs {
			out = append(out, c.Name)
		}
		return list(out)
	}
	if removed := ch.Of(formalize.Removed); len(removed) > 0 {
		fmt.Fprintf(&b, "**This removes %s.** Nothing will hold the code to it any more.\n\n", names(removed))
	}
	var loosened, properties, assumed []formalize.Change
	for _, c := range ch.Of(formalize.Changed) {
		switch c.Old.Kind {
		case project.Invariant:
			loosened = append(loosened, c)
		case project.Property:
			properties = append(properties, c)
		case project.Fairness:
			assumed = append(assumed, c)
		}
	}
	for _, c := range ch.Of(formalize.Added) {
		if c.New.Kind == project.Fairness {
			assumed = append(assumed, c)
		}
	}
	switch len(loosened) {
	case 0:
	case 1:
		fmt.Fprintf(&b, "**This changes the invariant %s.** A changed invariant can promise less than before, so compare its old and new text below.\n\n", names(loosened))
	default:
		fmt.Fprintf(&b, "**This changes the invariants %s.** A changed invariant can promise less than before, so compare their old and new text below.\n\n", names(loosened))
	}
	switch len(properties) {
	case 0:
	case 1:
		fmt.Fprintf(&b, "**This changes the property %s.** A changed property can promise less than before, so compare its old and new text below.\n\n", names(properties))
	default:
		fmt.Fprintf(&b, "**This changes the properties %s.** A changed property can promise less than before, so compare their old and new text below.\n\n", names(properties))
	}
	if len(assumed) > 0 {
		fmt.Fprintf(&b, "**This changes what the properties assume: %s.** Fairness is an assumption, and a property can hold only because it assumes more, so read what each one says below.\n\n", names(assumed))
	}
	b.WriteString("| Statement | Change | Says |\n| :-- | :-- | :-- |\n")
	for _, c := range ch.Statements {
		switch c.How {
		case formalize.Added:
			fmt.Fprintf(&b, "| `%s` | added %s | %s |\n", c.Name, c.New.Kind, c.New.Says)
		case formalize.Changed:
			says := c.New.Says
			if c.Old.Says != c.New.Says {
				says += " *(was: " + c.Old.Says + ")*"
			}
			how := "changed " + c.New.Kind
			if c.OwnTextSame() && len(c.Through) > 0 {
				how += ", only through " + list(c.Through)
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", c.Name, how, says)
		case formalize.Removed:
			fmt.Fprintf(&b, "| `%s` | **removed** %s | %s |\n", c.Name, c.Old.Kind, c.Old.Says)
		}
	}
	if same := ch.Of(formalize.Unchanged); len(same) > 0 {
		fmt.Fprintf(&b, "\nUnchanged: %s.\n", names(same))
	}
	for _, bc := range ch.Bounds {
		switch {
		case bc.From == "":
			fmt.Fprintf(&b, "\nNew bound: `%s = %s`.", bc.Name, bc.To)
		case bc.To == "":
			fmt.Fprintf(&b, "\nBound removed: `%s = %s`.", bc.Name, bc.From)
		default:
			fmt.Fprintf(&b, "\nBound changed: `%s`, from `%s` to `%s`.", bc.Name, bc.From, bc.To)
		}
	}
	if len(ch.Bounds) > 0 {
		b.WriteString("\n")
	}
	if r != nil {
		fmt.Fprintf(&b, "\n**Already checked** against the amended model, within %s: %s\n", bounds(p.Bounds), checked(r))
	}
	if len(m.Answers) > 0 {
		b.WriteString("\n**Decided on this issue:**\n\n")
		for _, a := range m.Answers {
			fmt.Fprintf(&b, "- %s. %s **%s.** %s (@%s)\n", a.Fork, a.Question, a.Option, a.Says, a.By)
		}
	}
	var tla strings.Builder
	for _, c := range ch.Statements {
		switch c.How {
		case formalize.Added:
			fmt.Fprintf(&tla, "**`%s`**, added:\n\n```tla\n%s\n```\n\n", c.Name, c.NewText)
		case formalize.Changed:
			if c.OwnTextSame() {
				continue // shown through the definitions it depends on, below
			}
			fmt.Fprintf(&tla, "**`%s`**, before:\n\n```tla\n%s\n```\n\nafter:\n\n```tla\n%s\n```\n\n", c.Name, c.OldText, c.NewText)
		case formalize.Removed:
			fmt.Fprintf(&tla, "**`%s`**, removed:\n\n```tla\n%s\n```\n\n", c.Name, c.OldText)
		}
	}
	for _, d := range ch.Definitions {
		fmt.Fprintf(&tla, "**`%s`**, which statements above depend on, before:\n\n```tla\n%s\n```\n\nafter:\n\n```tla\n%s\n```\n\n", d.Name, d.OldText, d.NewText)
	}
	if tla.Len() > 0 {
		fmt.Fprintf(&b, "\n<details>\n<summary>What changes, in TLA+</summary>\n\n%s</details>\n", tla.String())
	}
	fmt.Fprintf(&b, "\nTo ratify exactly this, comment `/invariant ratify %s`. "+
		"To change anything, say what in a comment, then comment `/invariant revise`.", strings.TrimPrefix(p.Hash, "sha256:")[:hashChars])
	return post("amendment for ratification", b.String(), m)
}

// list writes names in code and joins them: `a`, `a` and `b`, or
// `a`, `b` and `c`.
func list(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "`" + n + "`"
	}
	if len(quoted) < 2 {
		return strings.Join(quoted, "")
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
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
		"I opened #%d, and I'll merge it once CI's `invariant/gate` passes on it.", r.Claim(), pr.Number), m)
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
	body := fmt.Sprintf("CI's `invariant/gate` passed on #%d ([run](%s)), and I merged it as %s.", pr.Number, run.URL, sha)
	if r.Numbers != nil {
		body += "\n\n" + r.Numbers.Sentence()
	}
	return post("merged", body, *r)
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
