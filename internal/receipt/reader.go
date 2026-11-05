package receipt

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/verify"
)

const noReader = "\nNo reader chart is available. Supply .invariant/view.json and verify again.\n"

// Rendering is read-only, including for reports decoded from older archives.
// An undrawable supplied chart produces a diagnostic copy, never a partial chart.
func renderReader(r *verify.Reader) (*verify.Reader, string) {
	if r == nil || r.Chart == nil {
		return r, ""
	}
	svg, err := r.Chart.SVG()
	if err == nil {
		return r, svg
	}
	copyReader := *r
	copyReader.Chart, copyReader.Limitation, copyReader.Fingerprint = nil, nil, ""
	copyReader.Unavailable = &verify.ReaderDiagnostic{Code: "reader_render", Message: err.Error(), Source: &r.Chart.Report.Source}
	data, marshalErr := json.Marshal(struct {
		Reader     *verify.Reader           `json:"reader"`
		Diagnostic *verify.ReaderDiagnostic `json:"diagnostic"`
	}{r, copyReader.Unavailable})
	if marshalErr == nil {
		copyReader.Fingerprint = fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	}
	return &copyReader, ""
}

func readerMarkdown(r *verify.Reader) string {
	r, _ = renderReader(r)
	if r == nil {
		return noReader
	}
	var b strings.Builder
	b.WriteString("\n**Reader chart**\n\n")
	if d := r.Unavailable; d != nil {
		fmt.Fprintf(&b, "No reader chart is available (%s): %s\n", tlcCheckLiteral(d.Code), statechart.TableLiteral(d.Message))
	} else if l := r.Limitation; l != nil {
		b.WriteString(statechart.BoundsMarkdown(l.Source.Config.Constants))
		fmt.Fprintf(&b, "The chart is not drawn (too_large): TLC reached %d states, more than the %d states charts are drawn for. The view is well-formed, but its mappings and hiding were not evaluated. This adds no proof claim.\n", l.Actual, l.Limit)
	} else if r.Chart != nil {
		b.WriteString(statechart.BoundsMarkdown(r.Chart.Report.Source.Config.Constants))
		b.WriteString(r.Chart.Markdown())
	} else {
		b.WriteString(noReader)
	}
	if r.Fingerprint != "" {
		fmt.Fprintf(&b, "\nReader fingerprint: %s. Separate from verification evidence.\n", statechart.TableLiteral(r.Fingerprint))
	}
	return b.String()
}
