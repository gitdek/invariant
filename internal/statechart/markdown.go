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

// TableLiteral keeps text literal, including newlines and Markdown punctuation.
// Numeric entities preserve the exact wording when decoded once.
func TableLiteral(text string) string {
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

func tableLiteral(text string) string { return TableLiteral(text) }

// BoundsMarkdown renders checked bounds without interpreting source text.
func BoundsMarkdown(bounds map[string]string) string {
	var values []string
	for _, name := range slices.Sorted(maps.Keys(bounds)) {
		values = append(values, tableLiteral(name+" = "+bounds[name]))
	}
	return "Checked bounds: " + strings.Join(values, ", ") + ".\n\n"
}

// Markdown renders only the supplied chart and report, without changing them.
func (c *Result) Markdown() string {
	var b strings.Builder
	if !c.Preview.IsZero() {
		b.WriteString(TableLiteral(c.Preview.Description()) + "\n\n")
	}
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
	if !c.Preview.IsZero() {
		for _, fork := range slices.Sorted(maps.Keys(c.Preview.Choices)) {
			fmt.Fprintf(&b, "\nPreviously authorized choice: %s = %s.\n", tableLiteral(fork), tableLiteral(c.Preview.Choices[fork]))
		}
		fmt.Fprintf(&b, "\nJava image: %s. TLC jar SHA-256: %s.\n", tableLiteral(c.Report.Source.Tool.Image), tableLiteral(c.Report.Source.Tool.JarSHA256))
	}
	return b.String()
}

// Markdown retains the complete candidate chart before describing differences.
// All source text uses the same literal encoders as ordinary charts.
func (c *Comparison) Markdown() string {
	if err := c.Validate(); err != nil {
		return "Comparison unavailable: " + TableLiteral(err.Error()) + "\n"
	}
	var b strings.Builder
	for _, line := range c.description() {
		// Description values follow fixed prose prefixes, so an issue's #
		// is literal here. Keep names copyable without relaxing the Mermaid
		// or table encoders, or allowing source newlines to become headings.
		b.WriteString(strings.ReplaceAll(TableLiteral(line), "&#35;", "#") + "\n\n")
	}
	if c.Candidate.Chart != nil {
		b.WriteString(c.Candidate.Chart.Markdown())
	}
	if c.Unavailable == nil {
		b.WriteString("\nComparison annotations (complete candidate and removed baseline context):\n\n```mermaid\nflowchart LR\n")
		b.WriteString("    classDef added fill:#e4f5e9,stroke:#16733c\n    classDef removed fill:#fff0ee,stroke:#b42318,stroke-dasharray:5 4\n")
		arrowIndex := 0
		for side, snapshot := range []ComparisonSnapshot{c.Candidate, c.Baseline} {
			prefix, change, states, arrows := "new", "added", c.Visible.AddedStates, c.Visible.AddedArrows
			if side == 1 {
				prefix, change, states, arrows = "old", "removed", c.Visible.RemovedStates, c.Visible.RemovedArrows
			}
			ids := map[string]string{}
			fmt.Fprintf(&b, "    subgraph %s[\"%s\"]\n", prefix, mermaidLiteral(snapshot.Name))
			for i, s := range snapshot.Chart.States {
				ids[s.ID] = fmt.Sprintf("%ss%d", prefix, i)
				label := s.Label
				changed := slices.ContainsFunc(states, func(a State) bool { return a.ID == s.ID })
				if changed {
					label = change + ": " + label
				}
				fmt.Fprintf(&b, "    %s[\"%s\"]\n", ids[s.ID], mermaidLiteral(label))
				if changed {
					fmt.Fprintf(&b, "    class %s %s\n", ids[s.ID], change)
				}
			}
			for _, a := range snapshot.Chart.Arrows {
				changed := slices.ContainsFunc(arrows, func(b Arrow) bool { return arrowKey(a) == arrowKey(b) })
				label, connector := a.Label, "-->"
				if changed {
					label = change + ": " + label
					if side == 1 {
						connector = "-.->"
					}
				}
				fmt.Fprintf(&b, "    %s %s|\"%s\"| %s\n", ids[a.From], connector, mermaidLiteral(label), ids[a.To])
				if changed {
					style := "stroke:#16733c,stroke-width:3px"
					if side == 1 {
						style = "stroke:#b42318,stroke-width:3px,stroke-dasharray:5 4"
					}
					fmt.Fprintf(&b, "    linkStyle %d %s\n", arrowIndex, style)
				}
				arrowIndex++
			}
			b.WriteString("    end\n")
		}
		b.WriteString("```\n\n")
	}
	for _, line := range c.deltaDescription() {
		b.WriteString(TableLiteral(line) + "\n\n")
	}
	return b.String()
}
