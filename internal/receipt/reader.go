package receipt

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/verify"
)

const noReader = "\nNo reader chart is available. Supply .invariant/view.json and verify again.\n"

// Rendering a decoded report must not mutate its evidence or publish a
// partial chart. Keep a rendering failure in a detached explanatory record.
func renderReader(r *verify.Report) (*verify.Report, string) {
	if r.Reader == nil || r.Reader.Chart == nil {
		return r, ""
	}
	svg, err := r.Reader.Chart.SVG()
	if err == nil {
		return r, svg
	}
	copy := *r
	reader := *r.Reader
	reader.Chart, reader.Limitation, reader.Fingerprint = nil, nil, ""
	reader.Unavailable = &verify.ReaderDiagnostic{Code: "render_failed", Message: err.Error()}
	copy.Reader = &reader
	return &copy, ""
}

func readerMarkdown(r *verify.Reader) string {
	if r == nil {
		return noReader
	}
	var b strings.Builder
	switch {
	case r.Unavailable != nil:
		fmt.Fprintf(&b, "\n**Reader chart unavailable (%s).** %s\n", tlcCheckLiteral(r.Unavailable.Code), statechart.Literal(r.Unavailable.Message))
	case r.Limitation != nil:
		l := r.Limitation
		b.WriteString("\n**Reader chart**\n\n" + statechart.BoundsMarkdown(l.Source.Config.Constants))
		fmt.Fprintf(&b, "The chart is not drawn (%s): TLC reached %d states, more than the %d states charts are drawn for. The view is well-formed; its mappings and hiding were not evaluated. This adds no proof claim.\n", tlcCheckLiteral(l.Code), l.Actual, l.Limit)
	case r.Chart != nil:
		b.WriteString(r.Chart.Markdown())
	default:
		return noReader
	}
	var source *statechart.Source
	if r.Chart != nil {
		source = &r.Chart.Report.Source
	} else if r.Limitation != nil {
		source = &r.Limitation.Source
	}
	if source != nil {
		fmt.Fprintf(&b, "\nReader tools: %s; Java %s; TLC jar SHA-256 %s.\n",
			statechart.Literal(source.TLC), statechart.Literal(source.Tool.Image), statechart.Literal(source.Tool.JarSHA256))
	}
	if r.Fingerprint != "" {
		fmt.Fprintf(&b, "\nReader fingerprint: %s. Separate from verification evidence and assurance.\n", statechart.Literal(r.Fingerprint))
	}
	b.WriteString("\n| Reader input | SHA-256 |\n| :-- | :-- |\n")
	fmt.Fprintf(&b, "| %s | %s |\n| .invariant/view.json | %s |\n",
		statechart.Literal(r.Inputs.Module+".tla"), statechart.Literal(r.Inputs.ModelSHA256), statechart.Literal(r.Inputs.ViewSHA256))
	for _, name := range slices.Sorted(maps.Keys(r.Inputs.Includes)) {
		fmt.Fprintf(&b, "| %s | %s |\n", statechart.Literal(name), statechart.Literal(r.Inputs.Includes[name]))
	}
	return b.String()
}
