package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"path/filepath"
	"strings"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/statechart"
)

const comparisonFormat = "1: checked modeled proposal and captured comparison"

type comparisonEvidence struct {
	Format   string
	Repo     string
	Issue    int
	Proposal *formalize.Proposal
	Problem  string
}

var comparisonCache = proposalKind[comparisonEvidence]{
	directory: "comparisons", format: comparisonFormat, template: comparisonHTML,
	render: makeComparison,
	withProblem: func(e comparisonEvidence, problem string) comparisonEvidence {
		e.Problem = problem
		return e
	},
}

func comparisonPageSummary(c *statechart.Comparison) string {
	return "Proposal comparison: " + c.Baseline.Name + " to " + c.Candidate.Name + ". Added and removed annotations retain the complete candidate."
}

func (e comparisonEvidence) checked() (string, string, string) {
	if e.Problem != "" {
		return "cached_evidence", e.Problem, ""
	}
	p := e.Proposal
	if p == nil {
		return "comparison_evidence", "The checked proposal is missing.", ""
	}
	if err := p.ValidateReader(); err != nil {
		return "comparison_evidence", err.Error(), ""
	}
	message := "Checked bounded proposal; candidate rules await ratification. This adds no proof claim."
	var svg string
	var err error
	if p.Reader.Comparison != nil {
		message += " " + comparisonPageSummary(p.Reader.Comparison) + " " + strings.Join(p.Reader.Comparison.Qualifications(), " ")
		svg, err = p.Reader.Comparison.SVG()
	} else if l := p.Reader.Limitation; l != nil {
		return "too_large", fmt.Sprintf("TLC reached %d states at these bounds; chart capacity is %d. Mappings and hiding were not evaluated.", l.Actual, l.Limit), ""
	} else {
		svg, err = p.Reader.Chart.SVG()
	}
	if err != nil {
		return "reader_render", err.Error(), ""
	}
	return "chart", message, svg
}

const comparisonHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Proposal reader evidence</title>
<style>body{font:16px system-ui,sans-serif;margin:2rem;line-height:1.5;color:#17202a;background:#fff}pre{white-space:pre-wrap;overflow-wrap:anywhere}img{display:block;width:100%;height:auto}code{overflow-wrap:anywhere}</style></head>
<body><main><h1>{{.Evidence.Proposal.Name}}</h1><p>{{.Evidence.Repo}} · issue #{{.Evidence.Issue}}</p><p>{{.Presentation.Message}}</p>
{{if .Presentation.SVG}}<p><a href="{{.Presentation.SVG}}">Open standalone SVG at full size</a></p><img src="{{.Presentation.SVG}}" alt="Checked proposal comparison and complete bounded evidence">{{end}}
<details><summary>Complete proposal sources and checked reader record</summary><pre>{{.Record}}</pre></details>
<p>Presentation: <code>{{.Presentation.Fingerprint}}</code><br>Format: {{.Presentation.Format}}</p>
<p>These proposed rules await ratification. The comparison describes bounded, view-qualified charts and does not establish behavioral equivalence.</p></main></body></html>`

var comparisonTemplate = template.Must(template.New("comparison").Parse(comparisonHTML))

func makeComparison(e comparisonEvidence) readerPage {
	status, message, svg := e.checked()
	p := ReaderPresentation{Status: status, Message: message, Format: comparisonFormat}
	key := readerHash(readerJSON(struct {
		Evidence                       comparisonEvidence
		Status, Message, SVG, Template string
	}{e, status, message, svg, comparisonHTML}))
	p.Fingerprint = key
	base := "/reader/" + strings.TrimPrefix(key, "sha256:")
	p.Page = base + ".html"
	if svg != "" {
		p.SVG = base + ".svg"
	}
	record, _ := json.MarshalIndent(e.Proposal, "", "  ")
	var b bytes.Buffer
	_ = comparisonTemplate.Execute(&b, struct {
		Evidence     comparisonEvidence
		Presentation ReaderPresentation
		Record       string
	}{e, p, string(record)})
	return readerPage{repo: e.Repo, presentation: p, html: b.Bytes(), svg: []byte(svg)}
}

func (s *Server) comparisonFile(key string) string {
	return filepath.Join(s.Cache, "comparisons", key+".json")
}

func (s *Server) keepComparison(e comparisonEvidence) *ReaderPresentation {
	return comparisonCache.keep(s, e)
}

func (s *Server) presentComparison(is *Issue) {
	// A proposal without reader evidence predates charts and stays
	// ratifiable, so its card shows no chart rather than an error.
	if is.Waiting == nil || is.Waiting.Proposal == nil || is.Waiting.Proposal.Reader == nil {
		return
	}
	is.Waiting.Reader = s.keepComparison(comparisonEvidence{Format: comparisonFormat, Repo: is.Repo, Issue: is.Number, Proposal: is.Waiting.Proposal})
}

func (s *Server) restoreComparisons(snap *Snapshot) {
	for i := range snap.Issues {
		is := &snap.Issues[i]
		repo := is.Repo
		if repo == "" {
			repo = snap.Repo
		}
		w := is.Waiting
		if w == nil || !s.readerRepo(repo) {
			continue
		}
		// A saved link without the proposal it shows could never be served
		// again, so a damaged snapshot loses the link rather than serving a
		// page that doesn't exist.
		if w.Proposal == nil || w.Proposal.Reader == nil {
			w.Reader = nil
			continue
		}
		expected := comparisonEvidence{Format: comparisonFormat, Repo: repo, Issue: is.Number, Proposal: w.Proposal}
		w.Reader = comparisonCache.restore(s, expected, w.Reader, "Cached proposal comparison is damaged or obsolete; check the captured baseline and candidate again.")
	}
}
