// Package receipt renders a verification report for people: the Markdown a
// pull request or CI run carries.
package receipt

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/gitdek/invariant/internal/verify"
)

// Markdown renders the report as a receipt.
func Markdown(r *verify.Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## ◉ Invariant receipt · %s\n\n", r.Project)
	if r.Passed {
		fmt.Fprintf(&b, "**✅ Pass.** Every check passed. Fingerprint `%s`\n\n", short(r.Fingerprint))
	} else {
		fmt.Fprintf(&b, "**❌ Fail.** At least one check failed. Fingerprint `%s`\n\n", short(r.Fingerprint))
	}

	b.WriteString("| Check | Result | Evidence |\n| :-- | :-- | :-- |\n")
	row(&b, "Pinned statements", pinsOK(r), pinsResult(r), "recorded in "+r.Decision)
	row(&b, "Design · TLC", r.Design.Passed, designResult(r.Design), designEvidence(r.Design))
	row(&b, "Reachability", witnessesOK(r), fmt.Sprintf("%d of %d witnesses reached", count(r.Witnesses, func(w verify.Witness) bool { return w.Reached }), len(r.Witnesses)), witnessEvidence(r))
	row(&b, "Known bugs", mutantsOK(r), fmt.Sprintf("%d of %d caught", count(r.Mutants, func(m verify.Mutant) bool { return m.Caught }), len(r.Mutants)), mutantEvidence(r))
	row(&b, "Code · "+r.Code.Verifier, r.Code.Passed, codeResult(r.Code), codeEvidence(r.Code))
	row(&b, "Build", r.Build.Passed, buildResult(r.Build), "")

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
	fmt.Fprintf(&b, "| TLC | %s · tla2tools.jar %s `sha256:%s` |\n", t.TLC, t.TLCRelease, t.TLCJarSHA256[:12])
	fmt.Fprintf(&b, "| Java | `%s` |\n", shortImage(t.JavaImage))
	fmt.Fprintf(&b, "| Gobra | `%s` |\n", shortImage(t.GobraImage))
	fmt.Fprintf(&b, "| Go | %s |\n", t.Go)
	fmt.Fprintf(&b, "\n</details>\n\n<sub>Generated %s from tool output only. Fingerprint `%s`</sub>\n", r.GeneratedAt, r.Fingerprint)
	return b.String()
}

func row(b *strings.Builder, check string, ok bool, result, evidence string) {
	fmt.Fprintf(b, "| %s | %s %s | %s |\n", check, mark(ok), result, evidence)
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

func mutantsOK(r *verify.Report) bool {
	return count(r.Mutants, func(m verify.Mutant) bool { return m.Caught }) == len(r.Mutants)
}

func mutantEvidence(r *verify.Report) string {
	var parts []string
	for _, m := range r.Mutants {
		if m.Caught {
			parts = append(parts, fmt.Sprintf("%s: `%s` violated after %s", m.Name, m.Violated, steps(m.Steps)))
		} else {
			parts = append(parts, m.Name+": not caught")
		}
	}
	return strings.Join(parts, ", ")
}

func codeResult(c verify.Code) string {
	if c.Passed {
		return fmt.Sprintf("%d of %d functions verified", len(c.Functions), len(c.Functions))
	}
	return fmt.Sprintf("%d verification errors", len(c.Errors))
}

func codeEvidence(c verify.Code) string {
	s := fmt.Sprintf("%d with contracts", len(c.Contracts))
	if c.Overflow {
		s += ", overflow checked"
	}
	return s
}

func buildResult(b verify.Build) string {
	if b.Passed {
		return "go vet, go test"
	}
	return fmt.Sprintf("go vet %s, go test %s", mark(b.Vet), mark(b.Test))
}

func failures(r *verify.Report) []string {
	var out []string
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
	for _, m := range r.Mutants {
		if !m.Caught {
			out = append(out, fmt.Sprintf("Mutant %s: %s", m.Name, m.Message))
		}
	}
	for _, e := range r.Code.Errors {
		out = append(out, r.Code.Verifier+": "+e)
	}
	if !r.Build.Passed {
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
