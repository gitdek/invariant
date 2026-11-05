package statechart

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Mermaid decodes its own entities once. Encode the input's # too, so an
// entity in a literal label cannot become syntax on that pass.
func mermaidLiteral(text string) string {
	var b strings.Builder
	for _, r := range text {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == ' ' {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "#%d;", r)
		}
	}
	return b.String()
}

// Table text stays literal, including newlines and Markdown punctuation.
// Numeric entities preserve the exact wording when decoded once.
func tableLiteral(text string) string {
	var b strings.Builder
	for _, r := range text {
		if strings.ContainsRune("&<>|`*_~[]()#\\\n\r\t", r) {
			fmt.Fprintf(&b, "&#%d;", r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Markdown renders the computed chart and report with literal-safe labels.
func (c *Result) Markdown() string {
	var b strings.Builder
	b.WriteString("\n**Reader chart**\n\n")
	b.WriteString(BoundsMarkdown(c.Report.Source.Config.Constants))
	aliases, labels := map[string]string{}, map[string]string{}
	b.WriteString("```mermaid\nstateDiagram-v2\n")
	for i, state := range c.States {
		aliases[state.ID], labels[state.ID] = fmt.Sprintf("s%d", i), state.Label
		fmt.Fprintf(&b, "    state \"%s\" as %s\n", mermaidLiteral(state.Label), aliases[state.ID])
	}
	for _, id := range c.Initial {
		fmt.Fprintf(&b, "    [*] --> %s\n", aliases[id])
	}
	for _, arrow := range c.Arrows {
		fmt.Fprintf(&b, "    %s --> %s: %s\n", aliases[arrow.From], aliases[arrow.To], mermaidLiteral(arrow.Label))
	}
	b.WriteString("```\n\n| State | Unavailable model actions | Violated invariants |\n| :-- | :-- | :-- |\n")
	for _, state := range c.States {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", tableLiteral(state.Label), tableLiteral(strings.Join(state.UnavailableActions, ", ")), tableLiteral(strings.Join(state.ViolatedRules, ", ")))
	}
	b.WriteString("\nUnavailable means the action occurs elsewhere in the reachable graph but at no member of this chart state, before hiding.\n\n| Checked rule | Kind | Exact wording |\n| :-- | :-- | :-- |\n")
	for _, rule := range c.Report.Rules {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", tableLiteral(rule.Name), tableLiteral(rule.Kind), tableLiteral(rule.Says))
	}
	b.WriteString("\n| Count | Value | Meaning |\n| :-- | --: | :-- |\n")
	for _, count := range []struct{ key, title string }{
		{"tlc_states", "TLC states"}, {"tlc_steps", "TLC steps"}, {"tlc_generated_states", "Generated states"},
		{"tlc_initial_states", "Initial TLC states"}, {"chart_states", "Chart states"}, {"chart_arrows", "Visible arrows"},
		{"chart_arrows_before_hiding", "Arrows before hiding"}, {"shown_steps", "Shown steps"}, {"hidden_steps", "Hidden steps"},
	} {
		fmt.Fprintf(&b, "| %s | %d | %s |\n", count.title, c.Report.Counts[count.key], tableLiteral(c.Report.CountMeanings[count.key]))
	}
	if c.Report.OverArrowTarget {
		fmt.Fprintf(&b, "\nThe chart exceeds the advisory target of %d visible arrows; every arrow is retained.\n", c.Report.ArrowTarget)
	}
	b.WriteString("\n**Hidden steps**\n")
	if len(c.Report.Hidden) == 0 {
		b.WriteString("\nNone.\n")
	} else {
		b.WriteString("\n| From | Reader label | To | Model step occurrences | Hiding rules |\n| :-- | :-- | :-- | :-- | :-- |\n")
		for _, arrow := range c.Report.Hidden {
			var steps []string
			for _, step := range arrow.Members {
				steps = append(steps, step.From+" → "+step.Action+" → "+step.To)
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", tableLiteral(labels[arrow.From]), tableLiteral(arrow.Label), tableLiteral(labels[arrow.To]), tableLiteral(strings.Join(steps, "; ")), tableLiteral(strings.Join(arrow.HidingRules, ", ")))
		}
	}
	fmt.Fprintf(&b, "\nComputed from %s by %s. This explains the bounded model; it adds no proof claim.\n", tableLiteral(c.Report.Source.Module), tableLiteral(c.Report.Source.TLC))
	return b.String()
}

// BoundsMarkdown keeps bound names and values literal in reader reports.
func BoundsMarkdown(bounds map[string]string) string {
	var checked []string
	for _, name := range slices.Sorted(maps.Keys(bounds)) {
		checked = append(checked, tableLiteral(name+" = "+bounds[name]))
	}
	return "Checked bounds: " + strings.Join(checked, ", ") + ".\n\n"
}

// Literal keeps reader diagnostics and provenance inside their Markdown text.
func Literal(text string) string { return tableLiteral(text) }
