package factory

import (
	"fmt"
	"strings"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/statechart"
)

func tableLiteral(text string) string { return statechart.TableLiteral(text) }

func readerComment(p *formalize.Proposal) string {
	if p.Reader == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n**Reader chart**\n\n")
	b.WriteString(statechart.BoundsMarkdown(p.Bounds))
	if l := p.Reader.Limitation; l != nil {
		fmt.Fprintf(&b, "The chart is not drawn: TLC reached %d states at these bounds, more than the %d states charts are drawn for. Model checks passed; the view is well-formed, but its mappings and hiding were not evaluated (too_large).\n", l.Actual, l.Limit)
		return b.String()
	}
	b.WriteString(p.Reader.Chart.Markdown())
	return b.String()
}
