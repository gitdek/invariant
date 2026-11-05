package factory

import (
	"fmt"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/statechart"
)

func tableLiteral(text string) string { return statechart.Literal(text) }

func readerComment(p *formalize.Proposal) string {
	if p.Reader == nil {
		return ""
	}
	if l := p.Reader.Limitation; l != nil {
		return "\n**Reader chart**\n\n" + statechart.BoundsMarkdown(p.Bounds) +
			fmt.Sprintf("The chart is not drawn: TLC reached %d states at these bounds, more than the %d states charts are drawn for. Model checks passed; the view is well-formed, but its mappings and hiding were not evaluated (too_large).\n", l.Actual, l.Limit)
	}
	return p.Reader.Chart.Markdown()
}
