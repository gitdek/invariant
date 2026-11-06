package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"

	"github.com/gitdek/invariant/internal/formalize"
)

const proposalReaderFormat = "1: checked proposal comparison and native SVG"

type proposalEvidence struct {
	Format   string
	Repo     string
	Issue    int
	Proposal *formalize.Proposal
	Problem  string
}

func (e proposalEvidence) checked() (status, message, svg string) {
	if e.Problem != "" {
		return "cached_evidence", e.Problem, ""
	}
	if e.Proposal == nil {
		return "missing_comparison", "The checked proposal reader is missing.", ""
	}
	p := e.Proposal
	if err := p.ValidateReader(); err != nil {
		return "invalid_comparison", err.Error(), ""
	}
	if p.Target != nil && p.Target.Baseline != nil {
		ctx := (formalize.Request{Current: p.Target.Baseline}).CheckContext()
		if err := p.ValidateComparisonContext(ctx); err != nil {
			return "invalid_comparison", err.Error(), ""
		}
	}
	var err error
	if c := p.Reader.Comparison; c != nil {
		if c.Candidate.Chart == nil && c.Baseline.Chart == nil {
			return "too_large", c.Unavailable.Message, ""
		}
		svg, err = c.SVG()
		message = "Checked proposal comparison: complete candidate, named baseline, exact source identities, view changes and both checking scopes. Equal charts do not establish behavioral equivalence."
		if c.Unavailable != nil {
			message = c.Unavailable.Message + " Available complete charts and both source identities are retained."
		}
	} else if l := p.Reader.Limitation; l != nil {
		return "too_large", fmt.Sprintf("TLC reached %d states, more than the %d states charts are drawn for. Mappings and hiding were not evaluated.", l.Actual, l.Limit), ""
	} else {
		svg, err = p.Reader.Chart.SVG()
		message = "Checked bounded proposal chart. This adds no proof or code assurance."
	}
	if err != nil {
		return "reader_render", err.Error(), ""
	}
	return "chart", message, svg
}

const proposalReaderHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Proposal reader evidence</title>
<style>body{font:16px system-ui,sans-serif;margin:2rem;line-height:1.5;color:#17202a;background:#fff}main{max-width:100rem;margin:auto}pre{white-space:pre-wrap;overflow-wrap:anywhere}img{display:block;width:100%;height:auto}code{overflow-wrap:anywhere}</style></head>
<body><main><h1>Proposal reader evidence</h1><p>{{.Evidence.Repo}} · issue #{{.Evidence.Issue}}</p>
{{with .Evidence.Proposal}}<h2>{{.Name}}</h2><p>Proposal hash: <code>{{.Hash}}</code></p>{{end}}
<p>{{.Presentation.Message}}</p>
{{if .Presentation.SVG}}<p><a href="{{.Presentation.SVG}}">Open standalone SVG at full size</a></p><img src="{{.Presentation.SVG}}" alt="Complete checked proposal comparison and report">{{end}}
{{if .Comparison}}<h2>Proposal comparison</h2><pre>{{.Comparison}}</pre>{{end}}
<details><summary>Complete checked sources and reader evidence</summary><pre>{{.Record}}</pre></details>
<p>Presentation: <code>{{.Presentation.Fingerprint}}</code><br>Format: {{.Presentation.Format}}</p>
<p>This is a bounded, view-qualified explanation, not behavioral equivalence or code assurance.</p></main></body></html>`

var proposalReaderTemplate = template.Must(template.New("proposal-reader").Parse(proposalReaderHTML))

func makeProposalReader(e proposalEvidence) readerPage {
	status, message, svg := e.checked()
	p := ReaderPresentation{Status: status, Message: message, Format: proposalReaderFormat}
	key := readerHash(readerJSON(struct {
		Evidence                       proposalEvidence
		Status, Message, SVG, Template string
	}{e, status, message, svg, proposalReaderHTML}))
	p.Fingerprint = key
	base := "/reader/" + strings.TrimPrefix(key, "sha256:")
	p.Page = base + ".html"
	if svg != "" {
		p.SVG = base + ".svg"
	}
	comparison := ""
	if e.Proposal != nil && e.Proposal.Reader != nil && e.Proposal.Reader.Comparison != nil {
		comparison = e.Proposal.Reader.Comparison.Text()
	}
	record, _ := json.MarshalIndent(e.Proposal, "", "  ")
	var b bytes.Buffer
	_ = proposalReaderTemplate.Execute(&b, struct {
		Evidence           proposalEvidence
		Presentation       ReaderPresentation
		Record, Comparison string
	}{e, p, string(record), comparison})
	return readerPage{repo: e.Repo, presentation: p, html: b.Bytes(), svg: []byte(svg)}
}

func (s *Server) comparisonFile(key string) string {
	return filepath.Join(s.Cache, "comparisons", key+".json")
}

func (s *Server) keepProposalReader(e proposalEvidence) *ReaderPresentation {
	page := makeProposalReader(e)
	key := strings.TrimPrefix(page.presentation.Fingerprint, "sha256:")
	s.mu.Lock()
	if s.readers == nil {
		s.readers = map[string]readerPage{}
	}
	s.readers[key] = page
	s.mu.Unlock()
	if s.Cache != "" {
		file := s.comparisonFile(key)
		err := os.MkdirAll(filepath.Dir(file), 0o755)
		if err == nil {
			err = os.WriteFile(file+".tmp", readerJSON(e), 0o600)
		}
		if err == nil {
			err = os.Rename(file+".tmp", file)
		}
		if err != nil {
			s.logf("keeping proposal comparison: %v", err)
		}
	}
	return &page.presentation
}

func (s *Server) presentProposalReader(is *Issue) {
	if is.Waiting == nil || is.Waiting.Proposal == nil {
		return
	}
	is.Waiting.Reader = s.keepProposalReader(proposalEvidence{Format: proposalReaderFormat, Repo: is.Repo, Issue: is.Number, Proposal: is.Waiting.Proposal})
}

func (s *Server) restoreComparisons(snap *Snapshot) {
	for i := range snap.Issues {
		is := &snap.Issues[i]
		repo := is.Repo
		if repo == "" {
			repo = snap.Repo
		}
		w := is.Waiting
		if w == nil || w.Proposal == nil || !s.readerRepo(repo) {
			continue
		}
		expected := proposalEvidence{Format: proposalReaderFormat, Repo: repo, Issue: is.Number, Proposal: w.Proposal}
		var saved proposalEvidence
		ok := w.Reader != nil && w.Reader.Format == proposalReaderFormat
		if ok {
			parts := readerPath.FindStringSubmatch(w.Reader.Page)
			ok = len(parts) == 3
			if ok {
				b, err := os.ReadFile(s.comparisonFile(parts[1]))
				ok = err == nil && decodeReaderJSON(b, &saved) == nil
			}
		}
		if ok {
			comparable := saved
			comparable.Problem = ""
			ok = sameReaderJSON(comparable, expected) && makeProposalReader(saved).presentation == *w.Reader
		}
		if !ok {
			saved = expected
			saved.Problem = "Cached proposal comparison is damaged or obsolete. Restore the checked proposal sources or check the draft again."
		}
		w.Reader = s.keepProposalReader(saved)
	}
}
