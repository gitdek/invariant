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
<html lang="en"><head>` + evidenceHead + `<title>{{.Evidence.Proposal.Name}} — proposal evidence</title></head>
<body><main>{{template "top" .View}}
<div class="hero">{{template "hero" .View}}<p class="lede">{{.Presentation.Message}}</p>{{template "links" .View}}</div>
<div class="chart">{{if .Posters}}<picture><source media="(prefers-reduced-motion: reduce)" srcset="{{.Posters.Still}}"><img class="poster" src="{{.Posters.Animated}}" alt="Checked state chart for {{.Evidence.Proposal.Name}}"></picture>
{{if .Presentation.SVG}}<p><a href="{{.Presentation.SVG}}">Open the complete evidence image, with the full report</a></p>{{end}}{{else if .Presentation.SVG}}<p><a href="{{.Presentation.SVG}}">Open standalone SVG at full size</a></p><img src="{{.Presentation.SVG}}" alt="Checked proposal comparison and complete bounded evidence">{{end}}</div>
{{if .Evidence.Proposal}}<section><h2>The rules you would ratify</h2><p class="note">Ratifying pins these by hash. The factory then writes the code or driver, and CI checks every step against them.</p>{{template "rules" .View}}</section>
<section><h2>Checked bounds</h2><p class="note">TLC explored every state the draft model reaches within these.</p>{{template "bounds" .View}}</section>{{end}}
<details><summary>Complete proposal sources and checked reader record</summary><pre>{{.Record}}</pre></details>
<p class="meta">Presentation: <code>{{.Presentation.Fingerprint}}</code><br>Format: {{.Presentation.Format}}</p>
<p class="foot">These proposed rules await ratification. The comparison describes bounded, view-qualified charts and does not establish behavioral equivalence.</p>
</main></body></html>` + evidenceParts

var comparisonTemplate = template.Must(template.New("comparison").Funcs(evidenceFuncs).Parse(comparisonHTML))

// posters draws a checked proposal's chart for people to read, as a merged
// project's page does: an animated poster and its still twin, or neither
// (#784). The complete evidence image stays the presentation's SVG.
func (e comparisonEvidence) posters(status string) (string, string) {
	p := e.Proposal
	if status != "chart" || p == nil || p.Reader == nil || p.Reader.Chart == nil {
		return "", ""
	}
	poster, err := p.Reader.Chart.Poster(statechart.PosterOptions{Title: p.Name, Animated: true})
	if err != nil {
		return "", ""
	}
	still, err := p.Reader.Chart.Poster(statechart.PosterOptions{Title: p.Name})
	if err != nil {
		return "", ""
	}
	return poster, still
}

func makeComparison(e comparisonEvidence) readerPage {
	status, message, svg := e.checked()
	poster, still := e.posters(status)
	p := ReaderPresentation{Status: status, Message: message, Format: comparisonFormat}
	view := proposalView(e)
	key := readerHash(readerJSON(struct {
		Evidence                       comparisonEvidence
		Status, Message, SVG, Template string
		Poster, Still                  string `json:",omitempty"`
		View                           evidenceView
	}{e, status, message, svg, comparisonHTML, poster, still, view}))
	p.Fingerprint = key
	base := "/reader/" + strings.TrimPrefix(key, "sha256:")
	p.Page = base + ".html"
	if svg != "" {
		p.SVG = base + ".svg"
	}
	var posters *PosterLinks
	if poster != "" {
		posters = &PosterLinks{Animated: base + ".poster.svg", Still: base + ".still.svg"}
	}
	record, _ := json.MarshalIndent(e.Proposal, "", "  ")
	var b bytes.Buffer
	_ = comparisonTemplate.Execute(&b, struct {
		Evidence     comparisonEvidence
		Presentation ReaderPresentation
		Posters      *PosterLinks
		Record       string
		View         evidenceView
	}{e, p, posters, string(record), view})
	return readerPage{repo: e.Repo, presentation: p, posters: posters, html: b.Bytes(), svg: []byte(svg), poster: []byte(poster), still: []byte(still)}
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
	is.Waiting.Posters = s.postersFor(is.Waiting.Reader)
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
		w.Posters = s.postersFor(w.Reader)
	}
}
