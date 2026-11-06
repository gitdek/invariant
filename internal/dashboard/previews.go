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

const previewFormat = "1: unratified option, isolated model-only receipt"

// Option evidence is independent of project receipts and their commit cache.
// The enclosing words belong in the identity as much as the source snapshot.
type previewEvidence struct {
	Format   string
	Repo     string
	Issue    int
	Fork     string
	Kind     string
	Question string
	Option   formalize.Option
	Problem  string
}

func (e previewEvidence) checked() (status, message, svg string) {
	if e.Problem != "" {
		return "cached_evidence", e.Problem, ""
	}
	f := formalize.Fork{ID: e.Fork, Kind: e.Kind, Question: e.Question}
	if err := f.ValidatePreview(e.Option); err != nil {
		return "invalid_preview", fmt.Sprintf("%s/%s: %v", e.Fork, e.Option.ID, err), ""
	}
	v := e.Option.Preview
	if l := v.Model.Reader.Limitation; l != nil {
		return "too_large", fmt.Sprintf("Unratified alternative %s/%s; model-only evidence. TLC reached %d states at the checked bounds, more than the %d states charts are drawn for. Mappings and hiding were not evaluated.", e.Fork, e.Option.ID, l.Actual, l.Limit), ""
	}
	svg, err := v.Model.Reader.Chart.SVG()
	if err != nil {
		return "reader_render", err.Error(), ""
	}
	return "chart", v.Receipt.Reader.Preview.Description(), svg
}

const previewHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Evidence.Fork}}/{{.Evidence.Option.ID}} — alternative evidence</title>
<style>body{font:16px system-ui,sans-serif;margin:2rem;line-height:1.5;color:#17202a;background:#fff}main{max-width:100rem;margin:auto}pre{white-space:pre-wrap;overflow-wrap:anywhere}img{display:block;width:100%;height:auto}code{overflow-wrap:anywhere}dt{font-weight:bold}dd{margin-bottom:1rem}</style></head>
<body><main><h1>Unratified alternative {{.Evidence.Fork}}/{{.Evidence.Option.ID}}</h1>
<p>{{.Evidence.Repo}} · issue #{{.Evidence.Issue}} · model-only evidence</p>
<h2>{{.Evidence.Question}}</h2><pre>{{.Evidence.Option.Says}}</pre>
<p>{{.Presentation.Message}}</p>
{{if .Presentation.SVG}}<p><a href="{{.Presentation.SVG}}">Open standalone SVG at full size</a></p><img src="{{.Presentation.SVG}}" alt="Checked alternative and complete report for {{.Evidence.Fork}}/{{.Evidence.Option.ID}}">{{end}}
{{with .Evidence.Option.Preview}}
<h2>Previously authorized choices</h2><dl>{{range $fork, $option := .Choices}}<dt>{{$fork}}</dt><dd>{{$option}}</dd>{{end}}</dl>
{{with .Model}}<h2>{{if or (eq $.Presentation.Status "chart") (eq $.Presentation.Status "too_large")}}Checked bounds{{else}}Candidate bounds (unvalidated){{end}}</h2><dl>{{range $name, $value := .Bounds}}<dt>{{$name}}</dt><dd><pre>{{$value}}</pre></dd>{{end}}</dl>
<h2>Candidate rules</h2>{{range .Statements}}<h3>{{.Name}} ({{.Kind}})</h3><pre>{{.Says}}</pre><p>Pin: <code>{{.SHA256}}</code></p>{{end}}{{end}}{{end}}
<details><summary>Complete option inputs, receipt and reader evidence</summary><pre>{{.Record}}</pre></details>
<p>Presentation: <code>{{.Presentation.Fingerprint}}</code><br>Format: {{.Presentation.Format}}</p>
<p>These candidate rules have not been ratified. This bounded model explanation adds no proof or code assurance and starts no build.</p>
</main></body></html>`

var previewTemplate = template.Must(template.New("preview").Parse(previewHTML))

func makePreview(e previewEvidence) readerPage {
	status, message, svg := e.checked()
	p := ReaderPresentation{Status: status, Message: message, Format: previewFormat}
	key := readerHash(readerJSON(struct {
		Evidence                       previewEvidence
		Status, Message, SVG, Template string
	}{e, status, message, svg, previewHTML}))
	p.Fingerprint = key
	base := "/reader/" + strings.TrimPrefix(key, "sha256:")
	p.Page = base + ".html"
	if svg != "" {
		p.SVG = base + ".svg"
	}
	record, _ := json.MarshalIndent(e.Option.Preview, "", "  ")
	var b bytes.Buffer
	_ = previewTemplate.Execute(&b, struct {
		Evidence     previewEvidence
		Presentation ReaderPresentation
		Record       string
	}{e, p, string(record)})
	return readerPage{repo: e.Repo, presentation: p, html: b.Bytes(), svg: []byte(svg)}
}

func (s *Server) previewFile(key string) string {
	return filepath.Join(s.Cache, "previews", key+".json")
}

func (s *Server) keepPreview(e previewEvidence) *ReaderPresentation {
	page := makePreview(e)
	key := strings.TrimPrefix(page.presentation.Fingerprint, "sha256:")
	s.mu.Lock()
	if s.readers == nil {
		s.readers = map[string]readerPage{}
	}
	s.readers[key] = page
	s.mu.Unlock()
	if s.Cache != "" {
		file := s.previewFile(key)
		err := os.MkdirAll(filepath.Dir(file), 0o755)
		if err == nil {
			err = os.WriteFile(file+".tmp", readerJSON(e), 0o600)
		}
		if err == nil {
			err = os.Rename(file+".tmp", file)
		}
		if err != nil {
			s.logf("keeping option evidence: %v", err)
		}
	}
	return &page.presentation
}

func optionEvidence(is *Issue, q Question, o Choice) previewEvidence {
	return previewEvidence{Format: previewFormat, Repo: is.Repo, Issue: is.Number,
		Fork: q.ID, Kind: q.Kind, Question: q.Question,
		Option: formalize.Option{ID: o.ID, Says: o.Says, Preview: o.Preview}}
}

func (s *Server) presentPreviews(is *Issue) {
	if is.Waiting == nil {
		return
	}
	for i := range is.Waiting.Forks {
		q := &is.Waiting.Forks[i]
		has := false
		for _, o := range q.Options {
			has = has || o.Preview != nil
		}
		if !has {
			continue
		}
		for j := range q.Options {
			o := &q.Options[j]
			o.Reader = s.keepPreview(optionEvidence(is, *q, *o))
		}
	}
}

// A bad option replaces only its own presentation. Project evidence still
// follows restoreReaders' discard-on-damage policy for the whole snapshot.
func (s *Server) restorePreviews(snap *Snapshot) {
	for i := range snap.Issues {
		is := &snap.Issues[i]
		identity := *is
		if identity.Repo == "" {
			identity.Repo = snap.Repo
		}
		if is.Waiting == nil || !s.readerRepo(identity.Repo) {
			continue
		}
		for j := range is.Waiting.Forks {
			q := &is.Waiting.Forks[j]
			for k := range q.Options {
				o := &q.Options[k]
				if o.Preview == nil && o.Reader == nil {
					continue
				}
				expected := optionEvidence(&identity, *q, *o)
				var saved previewEvidence
				ok := o.Reader != nil && o.Reader.Format == previewFormat
				if ok {
					parts := readerPath.FindStringSubmatch(o.Reader.Page)
					ok = len(parts) == 3
					if ok {
						b, err := os.ReadFile(s.previewFile(parts[1]))
						ok = err == nil && decodeReaderJSON(b, &saved) == nil
					}
				}
				if ok {
					comparable := saved
					comparable.Problem = ""
					ok = sameReaderJSON(comparable, expected) && makePreview(saved).presentation == *o.Reader
				}
				if !ok {
					saved = expected
					saved.Problem = fmt.Sprintf("%s/%s: cached option evidence is damaged or obsolete; check this option again.", q.ID, o.ID)
				}
				o.Reader = s.keepPreview(saved)
			}
		}
	}
}
