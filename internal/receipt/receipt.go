// Package receipt renders a verification report for people: the Markdown a
// pull request or CI run carries.
package receipt

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/verify"
)

// Markdown renders the report as a receipt.
func Markdown(r *verify.Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## ◉ Invariant receipt · %s\n\n", r.Project)
	if r.Passed {
		fmt.Fprintf(&b, "**✅ Pass.** Every check passed. The code is **%s**. Fingerprint `%s`\n\n", r.Assurance, short(r.Fingerprint))
	} else {
		fmt.Fprintf(&b, "**❌ Fail.** At least one check failed. Fingerprint `%s`\n\n", short(r.Fingerprint))
	}

	b.WriteString("| Check | Result | Evidence |\n| :-- | :-- | :-- |\n")
	row(&b, "Pinned statements", pinsOK(r) && r.RatificationMatches(), pinsResult(r), provenance(r))
	row(&b, "Design · TLC", r.Design.Passed, designResult(r.Design), designEvidence(r.Design))
	row(&b, "Reachability", witnessesOK(r), fmt.Sprintf("%d of %d witnesses reached", count(r.Witnesses, func(w verify.Witness) bool { return w.Reached }), len(r.Witnesses)), witnessEvidence(r))
	row(&b, "Known bugs", bugsOK(r), fmt.Sprintf("%d of %d caught", count(r.Bugs, func(g verify.Bug) bool { return g.Caught }), len(r.Bugs)), bugEvidence(r))
	if a := r.Agreement; a != nil {
		row(&b, "Agreement", a.Passed, agreementResult(*a), agreementEvidence(*a))
	}
	if c := r.Code; c != nil {
		row(&b, "Code · "+c.Verifier, c.Passed, codeResult(*c), codeEvidence(*c))
	}
	if c := r.Conformance; c != nil {
		row(&b, "Code · conformance", c.Passed, conformanceResult(*c), conformanceEvidence(*c))
	}
	if len(r.Existing) > 0 {
		row(&b, "Existing code", r.Build.Passed, "run as it is, by the factory's driver", existingEvidence(r.Existing))
	}
	if !r.ModelOnly {
		row(&b, "Build", r.Build.Passed, buildResult(r.Build), "sandboxed, no network")
	}

	fmt.Fprintf(&b, "\nChecked within %s. Within these bounds TLC's search is exhaustive. Nothing is claimed outside them.\n", bounds(r.Bounds))

	if problems := failures(r); len(problems) > 0 {
		b.WriteString("\n**What failed**\n\n")
		for _, p := range problems {
			fmt.Fprintf(&b, "- %s\n", p)
		}
	}

	b.WriteString("\n<details>\n<summary>Ratified statements</summary>\n\n")
	b.WriteString("| Statement | Kind | Says | Pin |\n| :-- | :-- | :-- | :-- |\n")
	for _, p := range r.Pins {
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s `%s` |\n", p.Name, p.Kind, p.Says, mark(p.Match), short(p.Want))
	}
	b.WriteString("\n</details>\n\n<details>\n<summary>Toolchain</summary>\n\n")
	b.WriteString("| Tool | Pinned at |\n| :-- | :-- |\n")
	t := r.Toolchain
	fmt.Fprintf(&b, "| TLC | %s · tla2tools.jar %s `%s` |\n", t.TLC, t.TLCRelease, short("sha256:"+t.TLCJarSHA256))
	fmt.Fprintf(&b, "| Java | `%s` |\n", shortImage(t.JavaImage))
	for _, img := range [][2]string{{"Gobra", t.GobraImage}, {"Go sandbox", t.GoImage}, {"Node sandbox", t.NodeImage}, {"Python sandbox", t.PythonImage}} {
		if img[1] != "" {
			fmt.Fprintf(&b, "| %s | `%s` |\n", img[0], shortImage(img[1]))
		}
	}
	if t.NaginiRecipe != "" {
		fmt.Fprintf(&b, "| Nagini sandbox | recipe `%s` |\n", short(t.NaginiRecipe))
	}
	if t.DepsRecipe != "" {
		fmt.Fprintf(&b, "| Dependencies sandbox | `%s`, with the package's locked dependencies: recipe `%s` |\n", shortImage(t.DepsBase), short(t.DepsRecipe))
	}
	if t.Go != "" {
		fmt.Fprintf(&b, "| Go | %s |\n", t.Go)
	}
	fmt.Fprintf(&b, "\n</details>\n\n<sub>Generated %s from tool output only. Fingerprint `%s`</sub>\n", r.GeneratedAt, r.Fingerprint)
	return b.String()
}

// existingEvidence names the existing code a project ran, and its hashes.
func existingEvidence(code []verify.ExistingCode) string {
	var parts []string
	for _, c := range code {
		files := "1 file"
		if c.Files != 1 {
			files = fmt.Sprintf("%d files", c.Files)
		}
		parts = append(parts, fmt.Sprintf("`%s`, %s, `%s`", c.Path, files, short(c.SHA256)))
	}
	return strings.Join(parts, "; ")
}

func row(b *strings.Builder, check string, ok bool, result, evidence string) {
	fmt.Fprintf(b, "| %s | %s %s | %s |\n", check, mark(ok), result, evidence)
}

// provenance says where the statements were ratified: an entry in
// decisions/log.md for a hand-built project, or a comment on an issue for a
// factory project.
func provenance(r *verify.Report) string {
	switch {
	case r.Ratified != nil && !r.RatificationMatches():
		return fmt.Sprintf("the lock no longer matches what @%s ratified on #%d", r.Ratified.By, r.Ratified.Issue)
	case r.Ratified != nil && r.Ratified.Previous != "":
		return fmt.Sprintf("ratified by @%s on [#%d](%s), amending %s", r.Ratified.By, r.Ratified.Issue, r.Ratified.Comment, r.Ratified.Previous)
	case r.Ratified != nil:
		return fmt.Sprintf("ratified by @%s on [#%d](%s)", r.Ratified.By, r.Ratified.Issue, r.Ratified.Comment)
	case r.Decision != "":
		return "recorded in " + r.Decision
	default:
		return "not ratified yet"
	}
}

func pinsOK(r *verify.Report) bool {
	return count(r.Pins, func(p verify.Pin) bool { return p.Match }) == len(r.Pins)
}

func pinsResult(r *verify.Report) string {
	return fmt.Sprintf("%d of %d match", count(r.Pins, func(p verify.Pin) bool { return p.Match }), len(r.Pins))
}

func designResult(d verify.Design) string {
	switch d.Outcome {
	case "passed":
		return "no violations, no deadlock"
	case "violated":
		return "`" + d.Violated + "` violated"
	case "deadlock":
		return "deadlock reachable"
	default:
		return "TLC failed"
	}
}

func designEvidence(d verify.Design) string {
	return fmt.Sprintf("%s distinct states (%s generated), depth %d", thousands(d.DistinctStates), thousands(d.StatesGenerated), d.Depth)
}

func witnessesOK(r *verify.Report) bool {
	return count(r.Witnesses, func(w verify.Witness) bool { return w.Reached }) == len(r.Witnesses)
}

func witnessEvidence(r *verify.Report) string {
	var parts []string
	for _, w := range r.Witnesses {
		if w.Reached {
			parts = append(parts, fmt.Sprintf("`%s` in %s", w.Name, steps(w.Steps)))
		} else {
			parts = append(parts, fmt.Sprintf("`%s` unreachable", w.Name))
		}
	}
	return strings.Join(parts, ", ")
}

func bugsOK(r *verify.Report) bool {
	return count(r.Bugs, func(g verify.Bug) bool { return g.Caught }) == len(r.Bugs)
}

func bugEvidence(r *verify.Report) string {
	var parts []string
	for _, g := range r.Bugs {
		if g.Caught {
			parts = append(parts, fmt.Sprintf("%s: `%s` violated after %s", g.Label, g.Violated, steps(g.Steps)))
		} else {
			parts = append(parts, g.Label+": not caught")
		}
	}
	return strings.Join(parts, ", ")
}

func agreementResult(a verify.Agreement) string {
	if a.Passed {
		return "code reaches the model's states"
	}
	return "code and model differ"
}

func agreementEvidence(a verify.Agreement) string {
	if a.States == 0 && !a.Passed {
		return "not explored"
	}
	return fmt.Sprintf("%s states, depth %d", thousands(a.States), a.Depth)
}

func codeResult(c verify.Code) string {
	if c.Passed {
		return fmt.Sprintf("proved: %d of %d functions verified", len(c.Functions), len(c.Functions))
	}
	if len(c.Errors) == 1 {
		return "1 verification error"
	}
	return fmt.Sprintf("%d verification errors", len(c.Errors))
}

func conformanceResult(c conformance.Result) string {
	switch {
	case c.Passed && c.Exhaustive:
		return "tested against the model: every reachable state, no step outside it"
	case c.Passed:
		return "tested against the model: no step outside it"
	case c.BadStart != "" || c.BadStep != nil:
		return "the code left the model"
	case c.Exhaustive && int64(c.States) < c.ModelStates:
		return "the code misses states the model reaches"
	}
	return "couldn't be checked"
}

func conformanceEvidence(c conformance.Result) string {
	if int64(c.States) > c.ModelStates {
		return fmt.Sprintf("%d runs, %s steps, %d states visited, more than the model's %s", c.Runs, thousands(int64(c.Steps)), c.States, thousands(c.ModelStates))
	}
	return fmt.Sprintf("%d runs, %s steps, %d of %s model states visited", c.Runs, thousands(int64(c.Steps)), c.States, thousands(c.ModelStates))
}

func codeEvidence(c verify.Code) string {
	s := fmt.Sprintf("%d with contracts", len(c.Contracts))
	if c.Overflow {
		s += ", overflow checked"
	}
	if len(c.Unverified) > 0 {
		s += fmt.Sprintf(", not verified: %s", strings.Join(c.Unverified, ", "))
	}
	return s
}

func buildResult(b verify.Build) string {
	parts := make([]string, len(b.Steps))
	for i, s := range b.Steps {
		parts[i] = s.Name
		if !b.Passed {
			parts[i] += " " + mark(s.Passed)
		}
	}
	return strings.Join(parts, ", ")
}

func failures(r *verify.Report) []string {
	var out []string
	if !r.RatificationMatches() {
		out = append(out, fmt.Sprintf("Ratification: the lock's statements or bounds no longer match the proposal @%s ratified (`%s`)", r.Ratified.By, short(r.Ratified.Proposal)))
	}
	for _, p := range r.Pins {
		switch {
		case p.Error != "":
			out = append(out, fmt.Sprintf("Pinned statement `%s`: %s", p.Name, p.Error))
		case !p.Match:
			out = append(out, fmt.Sprintf("Pinned statement `%s` changed: its text no longer matches what was ratified", p.Name))
		}
	}
	if !r.Design.Passed && r.Design.Message != "" {
		out = append(out, "TLC: "+r.Design.Message)
	}
	for _, w := range r.Witnesses {
		if !w.Reached {
			out = append(out, fmt.Sprintf("Witness `%s`: %s", w.Name, w.Message))
		}
	}
	for _, g := range r.Bugs {
		if !g.Caught {
			out = append(out, fmt.Sprintf("Known bug `%s`: %s", g.Name, g.Message))
		}
	}
	if a := r.Agreement; a != nil && !a.Passed {
		out = append(out, "Agreement: "+a.Message)
	}
	if c := r.Conformance; c != nil && !c.Passed {
		msg := "Conformance: " + c.Message
		if c.BadStep != nil {
			msg += fmt.Sprintf(". From `%s` to `%s`", c.BadStep.From, c.BadStep.To)
		}
		if c.BadStart != "" {
			msg += fmt.Sprintf(". The run started in `%s`", c.BadStart)
		}
		out = append(out, msg)
	}
	if c := r.Code; c != nil {
		for _, e := range c.Errors {
			out = append(out, c.Verifier+": "+e)
		}
	}
	if !r.Build.Passed && !r.ModelOnly {
		out = append(out, "Build:\n\n```\n"+r.Build.Output+"\n```")
	}
	return out
}

func count[T any](xs []T, ok func(T) bool) int {
	n := 0
	for _, x := range xs {
		if ok(x) {
			n++
		}
	}
	return n
}

func mark(ok bool) string {
	if ok {
		return "✅"
	}
	return "❌"
}

func steps(n int) string {
	if n == 1 {
		return "1 step"
	}
	return strconv.Itoa(n) + " steps"
}

func bounds(m map[string]string) string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, k := range names {
		parts[i] = "`" + k + " = " + m[k] + "`"
	}
	return strings.Join(parts, ", ")
}

func thousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// short trims a sha256 pin to 12 hex digits for display.
func short(pin string) string {
	if h, ok := strings.CutPrefix(pin, "sha256:"); ok && len(h) > 12 {
		return "sha256:" + h[:12]
	}
	return pin
}

func shortImage(ref string) string {
	if name, digest, ok := strings.Cut(ref, "@"); ok {
		return name + "@" + short(digest)
	}
	return ref
}
