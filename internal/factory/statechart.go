package factory

import (
	"fmt"
	"strings"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/statechart"
)

func tableLiteral(text string) string { return statechart.TableLiteral(text) }

func previewForksComment(forks []formalize.Fork, m Marker) string {
	var b strings.Builder
	b.WriteString("These are unratified alternatives with model-only evidence and candidate rules. Decide which behavior you want:\n")
	for _, f := range forks {
		fmt.Fprintf(&b, "\n**%s. %s**\n", tableLiteral(f.ID), tableLiteral(f.Question))
		for _, o := range f.Options {
			fmt.Fprintf(&b, "\n**%s.** %s\n", tableLiteral(o.ID), tableLiteral(o.Says))
			if v := o.Preview; v != nil && v.Model != nil {
				b.WriteString(readerComment(v.Model))
			}
		}
	}
	example := forks[0].ID + " " + forks[0].Options[0].ID
	closing := fmt.Sprintf("\n\nAnswer each one with a comment like `/invariant choose %s`, one line per question. "+
		"Or say what you want in your own words, then comment `/invariant revise`.", example)
	return post("a decision for you", strings.TrimSuffix(b.String(), "\n"), m, closing)
}

func readerComment(p *formalize.Proposal) string {
	return readerCommentWithEvidence(p, "")
}

func readerCommentWithEvidence(p *formalize.Proposal, evidence string) string {
	if p.Reader == nil {
		return ""
	}
	if p.Reader.Comparison != nil {
		if evidence != "" {
			return "\n**Reader proposal comparison**\n\n" + p.Reader.Comparison.CompactMarkdown()
		}
		return "\n**Reader proposal comparison**\n\n" + p.Reader.Comparison.Markdown()
	}
	var b strings.Builder
	b.WriteString("\n**Reader chart**\n\n")
	b.WriteString(statechart.BoundsMarkdown(p.Bounds))
	if l := p.Reader.Limitation; l != nil {
		fmt.Fprintf(&b, "The chart is not drawn: TLC reached %d states at these bounds, more than the %d states charts are drawn for. Model checks passed; the view is well-formed, but its mappings and hiding were not evaluated (too_large).\n", l.Actual, l.Limit)
		return b.String()
	}
	if evidence != "" {
		b.WriteString(p.Reader.Chart.CompactMarkdown())
	} else {
		b.WriteString(p.Reader.Chart.Markdown())
	}
	return b.String()
}
