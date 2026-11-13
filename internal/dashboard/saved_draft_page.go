package dashboard

import (
	"bytes"
	"html/template"
	"strings"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
)

const savedDraftHTML = `<!doctype html>
<html lang="en"><head>` + evidenceHead + `<title>{{.Result.Proposal.Name}} — saved draft evidence</title></head>
<body><main>{{template "top" .View}}
<div class="hero">{{template "hero" .View}}<p class="lede">Complete saved draft. Unratified, model-only evidence: this record grants no code assurance and offers no commands.</p>{{template "links" .View}}</div>
<section><h2>The saved reference</h2><p>{{.Reference.Repository}} · issue #{{.Reference.Issue}} · {{.Reference.Step}}</p>
<p>Result commit: <code>{{.Reference.Commit}}</code><br>Exact payload: <code>{{.Reference.SHA256}}</code><br>Proposal: <code>{{.Reference.ProposalHash}}</code></p>
<p><a href="{{.Locator}}">Permanent saved draft locator</a> · <a href="{{.Download}}" download="result.json">Download the exact complete result.json</a></p></section>
{{if .Comparison}}<p class="lede">{{.Comparison.Message}}</p><p><a href="{{.Comparison.Page}}">Proposal chart and comparison evidence</a></p>{{end}}
<div class="chart">{{if .Posters}}<picture><source media="(prefers-reduced-motion: reduce)" srcset="{{.Posters.Still}}"><img class="poster" src="{{.Posters.Animated}}" alt="Checked draft state chart"></picture>{{end}}
{{if .Presentation.SVG}}<p><a href="{{.Presentation.SVG}}">Complete native SVG and report</a></p>{{end}}</div>
{{if .Result.Proposal.Statements}}<section><h2>Candidate rules</h2>{{template "rules" .View}}</section><section><h2>Checked bounds</h2>{{template "bounds" .View}}</section>{{end}}
{{if .Result.Report}}<section><h2>TLC outcomes and tool identities</h2><p>Model check: {{.Result.Report.Design.Outcome}}. {{.Result.Report.Design.DistinctStates}} distinct states; {{.Result.Report.Design.StatesGenerated}} generated states.</p><p>TLC: {{.Result.Report.Toolchain.TLC}}<br>Java image: <code>{{.Result.Report.Toolchain.JavaImage}}</code></p></section>{{end}}
{{range .Questions}}<section><h2>{{.ID}}: {{.Question}}</h2><p>All original alternatives, unratified and model-only. Later answers do not edit these snapshots.</p>
{{range .Options}}<h3>{{.ID}}: {{.Says}}</h3>{{if .Reader}}<p>{{.Reader.Message}}</p><p><a href="{{.Reader.Page}}">Read this option's complete evidence</a></p>{{end}}
{{if .Posters}}<picture><source media="(prefers-reduced-motion: reduce)" srcset="{{.Posters.Still}}"><img class="poster" src="{{.Posters.Animated}}" alt="Checked alternative state chart"></picture>{{end}}
{{with .Preview}}{{if .Choices}}<h4>Previously authorized choices at this check</h4><ul class="bounds">{{range $fork, $option := .Choices}}<li class="bound"><b>{{$fork}}</b> <span>= {{$option}}</span></li>{{end}}</ul>{{end}}{{end}}{{end}}</section>{{end}}
<section><h2>Complete saved result</h2><p>Original questions, sources and includes, candidate rules and bounds, TLC outcomes, pinned tool and source identities, captured baselines, comparisons and qualifications, and prior-choice context are retained below. Backend: {{.Result.Usage.Backend}}.</p><pre>{{.Record}}</pre></section>
<p class="meta">Presentation: <code>{{.Presentation.Fingerprint}}</code><br>Format: {{.Presentation.Format}}</p>
<p class="foot">This is a bounded model explanation. Equal charts do not establish behavioral equivalence. The issue's current answers and ratification remain on its dashboard card.</p>
</main></body></html>` + evidenceParts

var savedDraftTemplate = template.Must(template.New("saved-draft").Funcs(evidenceFuncs).Parse(savedDraftHTML))

type savedDraftView struct {
	Reference    factory.DraftReference
	Locator      string
	Download     string
	Result       *formalize.Result
	Comparison   *ReaderPresentation
	Presentation ReaderPresentation
	Posters      *PosterLinks
	Questions    []Question
	Record       string
	View         evidenceView
}

func makeSavedDraft(ref factory.DraftReference, payload []byte, result *formalize.Result, comparison *ReaderPresentation, native readerPage, questions []Question) (readerPage, error) {
	p := ReaderPresentation{Format: savedDraftFormat, Commit: ref.Commit, Status: "questions", Message: "Complete unratified model-only alternatives."}
	if comparison != nil {
		p.Status, p.Message = comparison.Status, comparison.Message
	}
	view := proposalView(comparisonEvidence{Repo: ref.Repository, Issue: ref.Issue, Proposal: result.Proposal})
	view.Kind, view.Chips = "saved draft", []evidenceChip{{Text: "Unratified", Tone: "people"}, {Text: "Model only"}}
	view.Groups = rulesOf(result.Proposal.Statements, result.Report)
	v := savedDraftView{Reference: ref, Locator: draftLocator(ref), Result: result, Comparison: comparison,
		Questions: questions, Record: string(payload), View: view}
	links := func(base string) {
		p.Page = base + ".html"
		v.Download = base + ".json"
		if len(native.svg) > 0 {
			p.SVG = base + ".svg"
		}
		if len(native.poster) > 0 {
			v.Posters = &PosterLinks{Animated: base + ".poster.svg", Still: base + ".still.svg"}
		}
		v.Presentation = p
	}
	// Normalize only the self-referential links for the first render. The
	// identity covers actual current HTML, exact bytes (including whitespace),
	// reference and native image contents, rather than a template version alone.
	links("/reader/presentation")
	var b bytes.Buffer
	if err := savedDraftTemplate.Execute(&b, v); err != nil {
		return readerPage{}, err
	}
	p.Fingerprint = readerHash(readerJSON(struct {
		Reference                         factory.DraftReference
		Payload, HTML, SVG, Poster, Still []byte
	}{ref, payload, b.Bytes(), native.svg, native.poster, native.still}))
	links("/reader/" + strings.TrimPrefix(p.Fingerprint, "sha256:"))
	b.Reset()
	if err := savedDraftTemplate.Execute(&b, v); err != nil {
		return readerPage{}, err
	}
	return readerPage{repo: ref.Repository, presentation: p, posters: v.Posters, html: b.Bytes(),
		svg: native.svg, poster: native.poster, still: native.still, download: bytes.Clone(payload)}, nil
}
