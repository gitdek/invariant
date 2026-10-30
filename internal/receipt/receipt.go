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
	explicit := hasComponentEvidence(r)
	display := func(s string) string {
		if explicit {
			return componentText(s)
		}
		return s
	}
	writeRow := func(check string, ok bool, result, evidence string) {
		row(&b, display(check), ok, display(result), display(evidence))
	}
	fmt.Fprintf(&b, "## ◉ Invariant receipt · %s\n\n", display(r.Project))
	if r.Passed && explicit {
		fmt.Fprintf(&b, "**✅ Pass.** Gate passed. Assurance: **component evidence**. Fingerprint `%s`\n\n", display(short(r.Fingerprint)))
	} else if r.Passed {
		fmt.Fprintf(&b, "**✅ Pass.** Every check passed. The code is **%s**. Fingerprint `%s`\n\n", r.Claim(), short(r.Fingerprint))
	} else {
		fmt.Fprintf(&b, "**❌ Fail.** At least one check failed. Fingerprint `%s`\n\n", display(short(r.Fingerprint)))
	}
	// The gate checks the code whoever wrote it, so the receipt names the
	// agent but the fingerprint leaves it out (#179).
	if r.Agent != "" {
		if explicit {
			fmt.Fprintf(&b, "Coding agent: %s. The fingerprint leaves it out.\n\n", componentLiteral(r.Agent))
		} else {
			fmt.Fprintf(&b, "Coding agent: `%s`. The fingerprint leaves it out.\n\n", r.Agent)
		}
	}

	b.WriteString("| Check | Result | Evidence |\n| :-- | :-- | :-- |\n")
	writeRow("Pinned statements", pinsOK(r) && r.RatificationMatches(), pinsResult(r), provenance(r))
	writeRow("Design · TLC", r.Design.Passed, designResult(r.Design), designEvidence(r.Design))
	writeRow("Reachability", witnessesOK(r), fmt.Sprintf("%d of %d witnesses reached", count(r.Witnesses, func(w verify.Witness) bool { return w.Reached }), len(r.Witnesses)), witnessEvidence(r))
	if len(r.Properties) > 0 {
		writeRow("Liveness · TLC", propertiesOK(r), fmt.Sprintf("%d of %d properties hold", count(r.Properties, func(p verify.Property) bool { return p.Holds }), len(r.Properties)), propertyEvidence(r))
	}
	if len(r.Fairness) > 0 {
		writeRow("Fairness", fairnessOK(r), fmt.Sprintf("%d of %d on steps the model takes", count(r.Fairness, func(f verify.Fair) bool { return f.InNext }), len(r.Fairness)), fairnessEvidence(r))
	}
	writeRow("Known bugs", bugsOK(r), fmt.Sprintf("%d of %d caught", count(r.Bugs, func(g verify.Bug) bool { return g.Caught }), len(r.Bugs)), bugEvidence(r))
	if a := r.Agreement; a != nil && !explicit {
		writeRow("Agreement", a.Passed, agreementResult(*a), agreementEvidence(*a))
		// A Go explorer with Successors and no Try says where it went, not
		// what it tried (D-0090).
		if r.Conformance == nil {
			b.WriteString("| Every step tried | ➖ not checked | the explorer reports the states it reaches, not its attempts (D-0082) |\n")
		}
	}
	if l := r.Larger; l != nil && !explicit {
		// It adds to a claim and never fails a pass, so it isn't marked as a
		// failure when it doesn't hold.
		if l.Passed {
			writeRow("One size larger", true, "code reaches the model's states", fmt.Sprintf("%s states, depth %d, within %s", thousands(l.States), l.Depth, bounds(l.Bounds)))
		} else if l.Required {
			writeRow("One size larger", false, "code and model differ, so the code carries a bound", fmt.Sprintf("%s states, depth %d, where the model has %s, depth %d, within %s", thousands(l.States), l.Depth, thousands(l.WantStates), l.WantDepth, bounds(l.Bounds)))
		} else {
			fmt.Fprintf(&b, "| One size larger | ➖ not claimed | %s |\n", strings.ReplaceAll(firstLine(l.Message), "|", "\\|"))
		}
	}
	if c := r.Code; c != nil && !explicit {
		writeRow("Code · "+c.Verifier, c.Passed, codeResult(*c), codeEvidence(*c))
	}
	if c := r.Conformance; c != nil && !explicit {
		writeRow("Code · conformance", c.Passed, conformanceResult(*c), conformanceEvidence(*c))
		switch t := c.Tried; {
		case t != nil && t.Passed:
			writeRow("Every step tried", true, "in every state reached, but where only the environment's bounds rule a step out",
				fmt.Sprintf("%s attempts in %s states, refusals included", thousands(int64(t.Attempts)), thousands(int64(t.States))))
		case t != nil:
			writeRow("Every step tried", false, "a step never tried", triedEvidence(*t))
		case c.Exhaustive:
			b.WriteString("| Every step tried | ➖ not checked | the driver records its runs, not its attempts (D-0082) |\n")
		}
	}
	if len(r.Existing) > 0 {
		writeRow("Existing code", r.Build.Passed, "run as it is, by the factory's driver", existingEvidence(r.Existing))
	}
	if !r.ModelOnly {
		writeRow("Build", r.Build.Passed, buildResult(r.Build), "sandboxed, no network")
	}
	if explicit {
		componentEvidence(&b, r.Components)
	}

	if explicit {
		fmt.Fprintf(&b, "\nDesign checked within %s. Within these bounds TLC's search is exhaustive. Component proof coverage is listed above.\n", display(bounds(r.Bounds)))
	} else if r.EverySize() {
		fmt.Fprintf(&b, "\nChecked within %s, and again one size larger, within %s. Within each, TLC's search is exhaustive. %s proves the code against its contracts at every size, and the design's rules are claimed only within the sizes checked.\n", bounds(r.Bounds), bounds(r.Larger.Bounds), r.Code.Verifier)
	} else {
		fmt.Fprintf(&b, "\nChecked within %s. Within these bounds TLC's search is exhaustive. Nothing is claimed outside them.\n", bounds(r.Bounds))
	}

	if problems := failures(r); len(problems) > 0 {
		b.WriteString("\n**What failed**\n\n")
		for _, p := range problems {
			if explicit {
				p = componentText(p)
			}
			fmt.Fprintf(&b, "- %s\n", p)
		}
	}

	b.WriteString("\n<details>\n<summary>Ratified statements</summary>\n\n")
	b.WriteString("| Statement | Kind | Says | Pin |\n| :-- | :-- | :-- | :-- |\n")
	for _, p := range r.Pins {
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s `%s` |\n", display(p.Name), display(p.Kind), display(p.Says), mark(p.Match), display(short(p.Want)))
	}
	b.WriteString("\n</details>\n\n<details>\n<summary>Toolchain</summary>\n\n")
	b.WriteString("| Tool | Pinned at |\n| :-- | :-- |\n")
	t := r.Toolchain
	fmt.Fprintf(&b, "| TLC | %s · tla2tools.jar %s `%s` |\n", display(t.TLC), display(t.TLCRelease), display(short("sha256:"+t.TLCJarSHA256)))
	fmt.Fprintf(&b, "| Java | `%s` |\n", display(shortImage(t.JavaImage)))
	for _, img := range [][2]string{{"Gobra", t.GobraImage}, {"Go sandbox", t.GoImage}, {"Node sandbox", t.NodeImage}, {"Python sandbox", t.PythonImage}} {
		if img[1] != "" {
			fmt.Fprintf(&b, "| %s | `%s` |\n", img[0], display(shortImage(img[1])))
		}
	}
	if t.NaginiRecipe != "" {
		fmt.Fprintf(&b, "| Nagini sandbox | recipe `%s` |\n", display(short(t.NaginiRecipe)))
	}
	if t.DepsRecipe != "" {
		fmt.Fprintf(&b, "| Dependencies sandbox | `%s`, with the package's locked dependencies: recipe `%s` |\n", display(shortImage(t.DepsBase)), display(short(t.DepsRecipe)))
	}
	if deps := t.PythonDependencies; deps != nil {
		fmt.Fprintf(&b, "| Python application dependencies | base %s, recipe %s, platform %s |\n",
			componentLiteral(shortImage(deps.Base)), componentLiteral(short(deps.Recipe)), componentLiteral(deps.Platform))
	}
	if t.Go != "" {
		fmt.Fprintf(&b, "| Go | %s |\n", display(t.Go))
	}
	if deps := t.PythonDependencies; deps != nil {
		b.WriteString("\nPython application dependency inputs:\n\n| Input | SHA-256 |\n| :-- | :-- |\n")
		for _, input := range deps.Inputs {
			fmt.Fprintf(&b, "| %s | %s |\n", componentLiteral(input.Path), componentLiteral(short(input.SHA256)))
		}
		b.WriteString("\nResolved Python application wheels:\n")
		if len(deps.Artifacts) == 0 {
			b.WriteString("\nNo application wheels were installed.\n")
		} else {
			b.WriteString("\n| Package | Version | Wheel | SHA-256 |\n| :-- | :-- | :-- | :-- |\n")
			for _, artifact := range deps.Artifacts {
				fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", componentLiteral(artifact.Name), componentLiteral(artifact.Version),
					componentLiteral(artifact.Filename), componentLiteral(short(artifact.SHA256)))
			}
		}
	}
	fmt.Fprintf(&b, "\n</details>\n\n<sub>Generated %s from tool output only. Fingerprint `%s`</sub>\n", display(r.GeneratedAt), display(r.Fingerprint))
	return b.String()
}

func hasComponentEvidence(r *verify.Report) bool {
	return r.Components != nil && r.Components.Inventory.Mode == "explicit"
}

// componentEvidence reports every scoped declaration, even when it was only
// a contract input or had no selected check. Package-wide results cannot
// supply a component's proof status.
func componentEvidence(b *strings.Builder, evidence *verify.ComponentEvidence) {
	proofs := make(map[string]verify.ComponentProof, len(evidence.Proofs))
	for _, p := range evidence.Proofs {
		proofs[p.Target] = p
	}
	contracts := make(map[string][]string)
	for _, check := range evidence.Inventory.Checks {
		if check.Proof != nil {
			contracts[check.Target] = check.Proof.Contracts
		}
	}
	proved := 0
	for _, c := range evidence.Inventory.Components {
		if proofs[c.ID].Status == "proved" {
			proved++
		}
	}
	fmt.Fprintf(b, "\nComponent proofs: **%d proved; %d unchecked**.\n\n", proved, len(evidence.Inventory.Components)-proved)
	b.WriteString("| Component | Proof | Source | Contract | Evidence |\n| :-- | :-- | :-- | :-- | :-- |\n")
	for _, c := range evidence.Inventory.Components {
		proof := proofs[c.ID]
		status := "unchecked"
		if proof.Status == "proved" {
			status = "proved"
		}
		contract := "none"
		if c.ContractSHA256 != "" {
			contract = componentLiteral(short(c.ContractSHA256))
		}
		var details []string
		if selected, ok := contracts[c.ID]; ok {
			ids := make([]string, len(selected))
			for i, id := range selected {
				ids[i] = componentLiteral(id)
			}
			details = append(details, "selected contracts: "+strings.Join(ids, ", "))
		} else {
			details = append(details, "no proof selected")
		}
		if result := proof.Result; result != nil {
			if result.Verifier != "" {
				details = append(details, "verifier: "+componentText(result.Verifier))
			}
			for _, diagnostic := range result.Errors {
				details = append(details, componentText(diagnostic))
			}
		} else if _, ok := contracts[c.ID]; ok {
			details = append(details, "no verifier result")
		}
		fmt.Fprintf(b, "| %s | %s | %s · %s | %s | %s |\n", componentLiteral(c.ID), status,
			componentLiteral(c.Source), componentLiteral(short(c.SourceSHA256)), contract, strings.Join(details, "; "))
	}
	b.WriteString("\nScope: ")
	for i, scope := range evidence.Inventory.Scope {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(componentLiteral(scope))
	}
	b.WriteString(".\n\n| Source file | Identity |\n| :-- | :-- |\n")
	for _, file := range evidence.Inventory.Files {
		fmt.Fprintf(b, "| %s | %s |\n", componentLiteral(file.Path), componentLiteral(short(file.SHA256)))
	}
}

func componentLiteral(s string) string {
	return "<code>" + componentText(s) + "</code>"
}

// componentText keeps source-derived text literal in both prose and GFM
// tables. Entities cannot delimit table cells, Markdown spans or HTML tags.
// Encode line endings too: even a filename or diagnostic containing a newline
// must stay within its own row. JSON retains the original, unescaped values.
func componentText(s string) string {
	return strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;", "\\", "&#92;",
		"`", "&#96;", "*", "&#42;", "_", "&#95;", "[", "&#91;", "]", "&#93;",
		"~", "&#126;", "|", "&#124;", "\r", "&#13;", "\n", "&#10;",
	).Replace(s)
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

func propertiesOK(r *verify.Report) bool {
	return count(r.Properties, func(p verify.Property) bool { return p.Holds }) == len(r.Properties)
}

// propertyEvidence names each property and, for a broken one, how the
// behavior that breaks it goes on forever.
func propertyEvidence(r *verify.Report) string {
	fair := "with no fairness"
	if len(r.Fairness) > 0 {
		names := make([]string, len(r.Fairness))
		for i, f := range r.Fairness {
			names[i] = "`" + f.Name + "`"
		}
		fair = "under " + strings.Join(names, ", ")
	}
	var parts []string
	for _, p := range r.Properties {
		switch {
		case p.Holds:
			parts = append(parts, fmt.Sprintf("`%s` holds", p.Name))
		case p.Stutters:
			parts = append(parts, fmt.Sprintf("`%s` broken: stops after %s", p.Name, steps(p.Steps)))
		case p.Loop > 0:
			parts = append(parts, fmt.Sprintf("`%s` broken: loops back to state %d after %s", p.Name, p.Loop, steps(p.Steps)))
		default:
			parts = append(parts, fmt.Sprintf("`%s` not checked", p.Name))
		}
	}
	return strings.Join(parts, ", ") + ", " + fair
}

func fairnessOK(r *verify.Report) bool {
	return count(r.Fairness, func(f verify.Fair) bool { return f.InNext }) == len(r.Fairness)
}

func fairnessEvidence(r *verify.Report) string {
	var parts []string
	for _, f := range r.Fairness {
		kind := "weak"
		if f.Strong {
			kind = "strong"
		}
		switch {
		case f.InNext:
			parts = append(parts, fmt.Sprintf("`%s`: %s, on `%s`", f.Name, kind, f.Action))
		case f.Action != "":
			parts = append(parts, fmt.Sprintf("`%s`: `%s` isn't a Next step", f.Name, f.Action))
		default:
			parts = append(parts, fmt.Sprintf("`%s`: not a fairness condition", f.Name))
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
		if len(c.Functions) == 0 {
			return "no functions verified"
		}
		return fmt.Sprintf("proved: %d of %d functions verified", len(c.Functions), len(c.Functions))
	}
	if len(c.Errors) == 1 {
		return "1 verification error"
	}
	return fmt.Sprintf("%d verification errors", len(c.Errors))
}

func conformanceResult(c conformance.Result) string {
	switch {
	case c.Passed && c.Exhaustive && c.States > 0 && int64(c.States) == c.ModelStates && (c.Tried == nil || c.Tried.Passed):
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
	if c.Tried != nil {
		return fmt.Sprintf("%s steps recorded, %s of %s model states visited", thousands(int64(c.Steps)), thousands(int64(c.States)), thousands(c.ModelStates))
	}
	if int64(c.States) > c.ModelStates {
		return fmt.Sprintf("%d runs, %s steps, %d states visited, more than the model's %s", c.Runs, thousands(int64(c.Steps)), c.States, thousands(c.ModelStates))
	}
	return fmt.Sprintf("%d runs, %s steps, %d of %s model states visited", c.Runs, thousands(int64(c.Steps)), c.States, thousands(c.ModelStates))
}

// triedEvidence names what a driver never tried, and where.
func triedEvidence(t conformance.Tried) string {
	switch {
	case t.Untried != "" && t.In != "":
		return strings.ReplaceAll(fmt.Sprintf("`%s` in `%s`", t.Untried, t.In), "|", "\\|")
	case t.Untried != "":
		return strings.ReplaceAll("`"+t.Untried+"`", "|", "\\|")
	}
	return strings.ReplaceAll(firstLine(t.Message), "|", "\\|")
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
	for _, p := range r.Properties {
		if !p.Holds {
			out = append(out, fmt.Sprintf("Property `%s`: %s", p.Name, p.Message))
		}
	}
	for _, f := range r.Fairness {
		if !f.InNext {
			out = append(out, fmt.Sprintf("Fairness `%s`: %s", f.Name, f.Message))
		}
	}
	for _, g := range r.Bugs {
		if !g.Caught {
			out = append(out, fmt.Sprintf("Known bug `%s`: %s", g.Name, g.Message))
		}
	}
	if l := r.Larger; l != nil && l.Required && !l.Passed {
		message := "One size larger: " + l.Message
		if !hasComponentEvidence(r) {
			message += ". The explorer names its bounds, so the code must hold at every size, and something in it stops at the bounds."
		}
		out = append(out, message)
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
	if c := r.Conformance; c != nil && c.Tried != nil && !c.Tried.Passed {
		out = append(out, "Every step tried: "+c.Tried.Message+". "+triedEvidence(*c.Tried))
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

// firstLine is a message's first line, for a table cell.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
