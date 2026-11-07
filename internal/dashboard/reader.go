package dashboard

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html/template"
	"io"
	"maps"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/verify"
)

// Change this when the presentation or its validation contract changes. The
// identity also includes the actual SVG and page template, so renderer changes
// cannot reuse an earlier explanation's URL.
const readerFormat = "1: checked receipt, native SVG, complete report"

type ReaderPresentation struct {
	Status      string `json:"status"`
	Message     string `json:"message"`
	Commit      string `json:"commit"`
	Fingerprint string `json:"fingerprint"`
	Format      string `json:"format"`
	Page        string `json:"page"`
	SVG         string `json:"svg,omitempty"`
}

// PosterLinks draw a checked chart for people to read: an animated poster,
// and the same poster without motion for readers who prefer reduced motion.
// The presentation's SVG stays the complete evidence image.
type PosterLinks struct {
	Animated string `json:"animated"`
	Still    string `json:"still"`
}

type readerArtifacts struct {
	JSON []byte
	SVG  []byte
}

// Keep the inputs, not just a rendered image, for validation after a restart.
// No command, action token or live activity data belongs in this record.
type readerEvidence struct {
	Format, Repo, Dir, Commit string
	Manifest                  project.Manifest
	Lock                      project.Lock
	ManifestBytes, LockBytes  []byte
	Files                     map[string][]byte
	View                      []byte
	// Omitted when absent, so evidence without a comparison keeps the
	// identities and immutable URLs it had before comparisons existed.
	Comparison []byte `json:",omitzero"`
	Report     *verify.Report
	Artifacts  readerArtifacts
	Traces     map[string]tlc.TraceFile
	Problem    string
}

type readerPage struct {
	repo                     string
	presentation             ReaderPresentation
	posters                  *PosterLinks
	html, svg, poster, still []byte
}

func readerHash(b []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(b)) }

func readerJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func decodeReaderJSON(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("extra JSON data")
	}
	return nil
}

func sameReaderJSON(a, b any) bool { return bytes.Equal(readerJSON(a), readerJSON(b)) }

func (e readerEvidence) checked() (status, message, svg string) {
	bad := func(code, why string) (string, string, string) { return code, why, "" }
	if e.Problem == "missing_archive" {
		return bad("missing_artifacts", "The gate's receipt archive is missing or expired, or holds no receipt.json. Verify again to publish the complete evidence.")
	}
	if e.Problem != "" {
		return bad(e.Problem, "Reader evidence is unavailable: the saved artifacts cannot identify this project's receipt. Verify again to replace them.")
	}
	r := e.Report
	if r == nil {
		return bad("missing_artifacts", "The receipt archive has no report for this modeled project.")
	}
	if r.Reader == nil {
		return bad("legacy_receipt", "This legacy receipt has no checked reader artifacts. Save .invariant/view.json and verify again.")
	}
	rr := r.Reader
	// Recompute even when the receipt omits the comparison or its diagnostic.
	// The original reader remains intact for every fingerprint and final check.
	display := *rr
	var comparisonErr error
	if e.Comparison == nil {
		comparisonErr = os.ErrNotExist
	}
	captured, diagnostic := verify.CapturedComparison(e.Comparison, comparisonErr, e.Lock)
	if !sameReaderJSON(captured, rr.Comparison) || !sameReaderJSON(diagnostic, rr.ComparisonDiagnostic) {
		message := "The receipt's proposal comparison or diagnostic differs from the captured comparison at this receipt commit. Verify again using the original project evidence."
		if diagnostic != nil {
			message = diagnostic.Message + " " + message
		}
		diagnostic = &verify.ReaderDiagnostic{Code: "comparison_evidence", Message: message}
	}
	if diagnostic != nil {
		display.Comparison, display.ComparisonDiagnostic = nil, diagnostic
	}
	if len(e.Artifacts.JSON) == 0 || ((rr.Chart != nil || rr.Comparison != nil) && len(e.Artifacts.SVG) == 0) {
		return bad("missing_artifacts", "The receipt archive is missing reader.json or reader.svg. Verify again to publish the complete evidence.")
	}
	var companion verify.Reader
	if decodeReaderJSON(e.Artifacts.JSON, &companion) != nil || !sameReaderJSON(&companion, rr) {
		return bad("artifact_mismatch", "The archived reader.json differs from the receipt's checked reader record.")
	}
	if rr.Version != 1 {
		return bad("unsupported_reader", "This receipt uses an unsupported reader format. Verify again with the current reader.")
	}
	module := strings.TrimSuffix(path.Base(e.Manifest.Module), ".tla")
	model, exists := e.Files[module+".tla"]
	inputs := verify.ReaderInputs{Module: module, Model: readerHash(model), Includes: map[string]string{}}
	if e.View != nil {
		inputs.View = readerHash(e.View)
	}
	for name, data := range e.Files {
		if name != module+".tla" {
			inputs.Includes[name] = readerHash(data)
		}
	}
	if !exists || !sameReaderJSON(inputs, rr.Inputs) || !maps.Equal(r.Bounds, e.Lock.Bounds) ||
		r.Proposal != project.ProposalHash(e.Lock.Bounds, e.Lock.Statements) || r.Project != e.Manifest.Name {
		return bad("snapshot_mismatch", "The receipt's model, sibling includes, saved view, lock or bounds differ from this receipt commit.")
	}
	if r.Fingerprint != verify.Fingerprint(*r) {
		return bad("snapshot_mismatch", "The verification report does not match its recorded fingerprint.")
	}
	if len(r.Pins) != len(e.Lock.Statements) {
		return bad("snapshot_mismatch", "The receipt's statements differ from the lock at this commit.")
	}
	for i, st := range e.Lock.Statements {
		pin := r.Pins[i]
		if pin.Name != st.Name || pin.Kind != st.Kind || pin.Says != st.Says || pin.Want != st.SHA256 {
			return bad("snapshot_mismatch", "The receipt's pinned rules differ from the lock at this commit.")
		}
		if rr.Chart != nil || rr.Limitation != nil {
			got, err := tla.PinHash(string(model), st.Name, project.Model)
			if err != nil || got != st.SHA256 || !pin.Match || pin.Got != st.SHA256 {
				return bad("snapshot_mismatch", "The checked reader's rule pins do not match the model at this commit.")
			}
		}
	}
	count := 0
	if rr.Chart != nil {
		count++
	}
	if rr.Limitation != nil {
		count++
	}
	if rr.Unavailable != nil {
		count++
	}
	if count != 1 {
		return bad("invalid_reader", "The receipt must contain exactly one reader outcome.")
	}
	copyReader := *rr
	copyReader.Fingerprint = ""
	want := readerHash(readerJSON(struct {
		Reader verify.Reader `json:"reader"`
		Lock   project.Lock  `json:"lock"`
	}{copyReader, e.Lock}))
	// A render failure replaces its chart after fingerprinting. It cannot
	// supply a chart, but its original failure explanation remains useful.
	if rr.Fingerprint != want && (rr.Unavailable == nil || rr.Unavailable.Code != "reader_render") {
		return bad("snapshot_mismatch", "The checked reader fingerprint does not match its inputs, lock and complete contents.")
	}
	limitedComparison := func(code, message string) (string, string, string) {
		if display.Comparison == nil {
			// A damaged comparison's diagnostic stays visible beside the
			// final model's own outcome.
			if len(e.Artifacts.SVG) != 0 && !readerCompanionMatches(rr, e.Artifacts.SVG, diagnostic != nil) {
				return bad("artifact_mismatch", "The archived SVG differs from the native renderer's final outcome.")
			}
			return bad(code, strings.TrimSpace(message+" "+strings.Join(display.ComparisonNotes(), " ")))
		}
		svg, err := rr.SVG()
		if err != nil {
			return bad("reader_render", err.Error())
		}
		if !bytes.Equal(e.Artifacts.SVG, []byte(svg)) {
			return bad("artifact_mismatch", "The archived comparison SVG differs from the native renderer.")
		}
		return "chart", message + " " + strings.Join(rr.ComparisonNotes(), " ") + " " + comparisonPageSummary(rr.Comparison), svg
	}
	if d := rr.Unavailable; d != nil {
		if len(e.Artifacts.SVG) != 0 && rr.Comparison == nil && diagnostic == nil {
			return bad("artifact_mismatch", "The archive has an SVG beside an unavailable reader outcome.")
		}
		if d.Code == "" || d.Message == "" {
			return bad("invalid_reader", "The reader diagnostic is incomplete.")
		}
		if d.Code == "chart" {
			return bad("invalid_reader", "An unavailable reader cannot claim to be a chart.")
		}
		if d.Code == "missing_view" {
			if e.View != nil {
				return bad("snapshot_mismatch", "The receipt reports a missing view, but this commit has a saved view.")
			}
			return limitedComparison("missing_view", "No saved reader view exists at this receipt commit. Supply .invariant/view.json and verify again.")
		}
		return limitedComparison(d.Code, d.Message)
	}
	if e.View == nil {
		return bad("snapshot_mismatch", "The chart's saved reader view is absent from the receipt commit.")
	}
	cfg := tlc.Config{Constants: e.Lock.Bounds}
	for _, st := range e.Lock.Statements {
		if st.Kind == project.Spec {
			cfg.Specification = st.Name
		}
		if st.Kind == project.Invariant {
			cfg.Invariants = append(cfg.Invariants, st.Name)
		}
	}
	source := statechart.Source{Module: module, Config: cfg,
		Tool: tlc.GraphTool{Image: r.Toolchain.JavaImage, JarSHA256: r.Toolchain.TLCJarSHA256}, TLC: r.Toolchain.TLC}
	if !r.Design.Passed || r.Design.Outcome != string(tlc.Passed) || r.Design.Spec != cfg.Specification || !slices.Equal(r.Design.Invariants, cfg.Invariants) {
		return bad("snapshot_mismatch", "The reader has no matching completed model check in the receipt.")
	}
	if l := rr.Limitation; l != nil {
		if (len(e.Artifacts.SVG) != 0 && rr.Comparison == nil && diagnostic == nil) || l.Code != "too_large" || l.Actual <= statechart.MaxStates || l.Limit != statechart.MaxStates ||
			int64(l.Actual) != r.Design.DistinctStates || !sameReaderJSON(l.Source, source) {
			return bad("snapshot_mismatch", "The reader limitation disagrees with the receipt's completed exploration or provenance.")
		}
		return limitedComparison("too_large", fmt.Sprintf("TLC reached %d states at the checked bounds, more than the %d states charts are drawn for. The view's mappings and hiding were not evaluated; no partial chart is shown.", l.Actual, l.Limit))
	}
	c := rr.Chart
	if !sameReaderJSON(c.Report.Rules, e.Lock.Statements) || !sameReaderJSON(c.Report.Source, source) ||
		c.Report.Counts["tlc_states"] != r.Design.DistinctStates || c.Report.Counts["tlc_generated_states"] != r.Design.StatesGenerated {
		return bad("snapshot_mismatch", "The chart's rules, counts or tool provenance disagree with the receipt and lock.")
	}
	svg, err := display.SVG()
	if err != nil {
		return bad("reader_render", "Reader generation failed: "+err.Error())
	}
	if !bytes.Equal(e.Artifacts.SVG, []byte(svg)) && (diagnostic == nil || !readerCompanionMatches(rr, e.Artifacts.SVG, true)) {
		return bad("artifact_mismatch", "The archived SVG does not match the shared native renderer's complete checked result. Verify again with the current renderer.")
	}
	message = "Checked bounded model; the full report is included in the image. This adds no proof claim."
	if len(display.ComparisonNotes()) > 0 {
		message += " " + strings.Join(display.ComparisonNotes(), " ")
	}
	if display.Comparison != nil {
		message += " " + comparisonPageSummary(display.Comparison)
	}
	return "chart", message, svg
}

// A comparison-only discrepancy may change the leading native panels. Check
// the final drawing and report independently, never just their embedded JSON.
// Archived bytes are only evidence to compare; all displayed SVG is regenerated.
func readerCompanionMatches(r *verify.Reader, archived []byte, fallback bool) bool {
	if svg, err := r.SVG(); err == nil && bytes.Equal(archived, []byte(svg)) {
		return true
	}
	if !fallback {
		return false
	}
	final := *r
	final.Comparison, final.ComparisonDiagnostic = nil, nil
	if svg, err := final.SVG(); err == nil && svg != "" && bytes.Equal(archived, []byte(svg)) {
		return true
	}
	// Use the shared renderer to obtain the title and complete final panel,
	// including specific unavailable and too_large outcomes. The first panel
	// is a placeholder, ignored along with the archived comparison panels.
	final.ComparisonDiagnostic = &verify.ReaderDiagnostic{Code: "comparison_evidence", Message: "Unavailable"}
	svg, err := final.SVG()
	if err != nil {
		return false
	}
	want, ok := readerStack([]byte(svg))
	if !ok {
		return false
	}
	got, ok := readerStack(archived)
	if !ok || !sameReaderJSON(got.root, want.root) || got.width < want.width ||
		(len(got.panels) != len(want.panels) && len(got.panels) != len(want.panels)+1) {
		return false
	}
	for i := 1; i < len(want.panels); i++ {
		j := len(got.panels) - len(want.panels) + i
		if !sameReaderJSON(got.panels[j], want.panels[i]) || got.height-got.y[j] != want.height-want.y[i] {
			return false
		}
	}
	return true
}

type readerSVGStack struct {
	root          xml.StartElement
	width, height float64
	y             []float64
	panels        [][]xml.Token
}

// Accept only the native stack envelope. Normalize its generated panel IDs,
// but preserve every drawing attribute, text node and report in each panel.
func readerStack(data []byte) (readerSVGStack, bool) {
	var s readerSVGStack
	d := xml.NewDecoder(bytes.NewReader(data))
	t, err := d.Token()
	root, ok := t.(xml.StartElement)
	if err != nil || !ok || root.Name != (xml.Name{Space: "http://www.w3.org/2000/svg", Local: "svg"}) {
		return s, false
	}
	s.root = root.Copy()
	for _, a := range root.Attr {
		if a.Name.Local == "viewBox" {
			if _, err := fmt.Sscanf(a.Value, "0 0 %f %f", &s.width, &s.height); err != nil || s.width <= 0 || s.height <= 0 ||
				a.Value != fmt.Sprintf("0 0 %g %g", s.width, s.height) {
				return s, false
			}
		}
	}
	if s.width <= 0 || s.height <= 0 || math.IsNaN(s.width) || math.IsNaN(s.height) || math.IsInf(s.width, 0) || math.IsInf(s.height, 0) {
		return s, false
	}
	for i, a := range s.root.Attr {
		switch a.Name.Local {
		case "width", "height":
			v := s.width
			if a.Name.Local == "height" {
				v = s.height
			}
			if a.Value != fmt.Sprint(v) {
				return s, false
			}
			s.root.Attr[i].Value = "dimension"
		case "viewBox":
			s.root.Attr[i].Value = "dimensions"
		}
	}
	depth := 0
	closed := false
	for {
		t, err := d.Token()
		if err == io.EOF {
			return s, closed && len(s.panels) >= 2
		}
		if err != nil || closed {
			return s, false
		}
		switch el := t.(type) {
		case xml.StartElement:
			if el.Name.Space != root.Name.Space {
				return s, false
			}
			if depth == 0 {
				var y float64
				if el.Name.Local != "g" || len(el.Attr) != 1 || el.Attr[0].Name != (xml.Name{Local: "transform"}) {
					return s, false
				}
				v := el.Attr[0].Value
				if _, err := fmt.Sscanf(v, "translate(0 %f)", &y); err != nil || v != fmt.Sprintf("translate(0 %g)", y) ||
					y < 0 || y >= s.height || (len(s.y) == 0 && y != 0) || (len(s.y) > 0 && y <= s.y[len(s.y)-1]) {
					return s, false
				}
				s.y = append(s.y, y)
				s.panels = append(s.panels, nil)
				depth++
				continue
			}
			switch el.Name.Local {
			case "g", "title", "desc", "metadata", "defs", "marker", "path", "rect", "text", "tspan", "circle":
			default:
				return s, false
			}
			el = el.Copy()
			prefix := fmt.Sprintf("panel%d-", len(s.panels)-1)
			for i, a := range el.Attr {
				if strings.HasPrefix(strings.ToLower(a.Name.Local), "on") || a.Name.Local == "href" || a.Name.Local == "style" {
					return s, false
				}
				switch a.Name.Local {
				case "id", "aria-labelledby", "aria-describedby":
					if !strings.HasPrefix(a.Value, prefix) {
						return s, false
					}
					el.Attr[i].Value = strings.TrimPrefix(a.Value, prefix)
				case "marker-end":
					if !strings.HasPrefix(a.Value, "url(#"+prefix) {
						return s, false
					}
					el.Attr[i].Value = strings.ReplaceAll(a.Value, "url(#"+prefix, "url(#")
				}
			}
			t = el
			depth++
		case xml.EndElement:
			if depth == 0 {
				closed = true
				continue
			}
			depth--
			if depth == 0 {
				continue
			}
		case xml.CharData:
			if depth == 0 {
				return s, false
			}
		default:
			return s, false
		}
		i := len(s.panels) - 1
		s.panels[i] = append(s.panels[i], xml.CopyToken(t))
	}
}

const readerHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Evidence.Manifest.Name}} — reader evidence</title>
<style>body{font:16px system-ui,sans-serif;margin:2rem;line-height:1.5;color:#17202a;background:#fff}main{max-width:100rem;margin:auto}pre{white-space:pre-wrap;overflow-wrap:anywhere}img{display:block;width:100%;height:auto}img.poster{width:auto;max-width:100%;margin:1rem auto;border-radius:28px;box-shadow:0 24px 60px rgba(9,13,28,.35)}code{overflow-wrap:anywhere}dt{font-weight:bold}dd{margin-bottom:1rem}</style></head>
<body><main><h1>{{.Evidence.Manifest.Name}}</h1><p>{{.Evidence.Repo}} · {{.Evidence.Dir}}</p>
<p>Receipt commit: <code>{{.Presentation.Commit}}</code></p>
<p>{{.Presentation.Message}}</p>
{{if .Posters}}<picture><source media="(prefers-reduced-motion: reduce)" srcset="{{.Posters.Still}}"><img class="poster" src="{{.Posters.Animated}}" alt="Checked reader chart for {{.Evidence.Manifest.Name}}"></picture>
<p><a href="{{.Presentation.SVG}}">Open the complete evidence image, with the full report</a></p>{{else if .Presentation.SVG}}<p><a href="{{.Presentation.SVG}}">Open standalone SVG at full size</a></p><img src="{{.Presentation.SVG}}" alt="Checked reader chart and complete report for {{.Evidence.Manifest.Name}}">{{end}}
<h2>Checked bounds</h2><dl>{{range $name, $value := .Evidence.Lock.Bounds}}<dt>{{$name}}</dt><dd><pre>{{$value}}</pre></dd>{{end}}</dl>
<h2>Locked rules</h2>{{range .Evidence.Lock.Statements}}<h3>{{.Name}} ({{.Kind}})</h3><pre>{{.Says}}</pre><p>Pin: <code>{{.SHA256}}</code></p>{{end}}
{{if .Record}}<details><summary>Complete receipt reader record</summary><pre>{{.Record}}</pre></details>{{end}}
<p>Presentation: <code>{{.Presentation.Fingerprint}}</code><br>Format: {{.Presentation.Format}}</p>
<p>This explanation describes the model at the receipt's bounds. It does not change the receipt's verification verdict or code assurance.</p>
</main></body></html>`

var readerTemplate = template.Must(template.New("reader").Parse(readerHTML))
var readerPath = regexp.MustCompile(`^/reader/([0-9a-f]{64})\.(html|svg|poster\.svg|still\.svg)$`)

// posters draws a checked chart's animated and still posters, or neither.
func (e readerEvidence) posters(status string) (string, string) {
	if status != "chart" || e.Report == nil || e.Report.Reader == nil || e.Report.Reader.Chart == nil {
		return "", ""
	}
	c := e.Report.Reader.Chart
	poster, err := c.Poster(statechart.PosterOptions{Title: e.Manifest.Name, Animated: true})
	if err != nil {
		return "", ""
	}
	still, err := c.Poster(statechart.PosterOptions{Title: e.Manifest.Name})
	if err != nil {
		return "", ""
	}
	return poster, still
}

func makeReader(e readerEvidence) readerPage {
	status, message, svg := e.checked()
	poster, still := e.posters(status)
	if poster != "" {
		message = strings.Replace(message, "the full report is included in the image", "the poster's light replays one real behavior from TLC's graph, and the evidence page holds the full report", 1)
	}
	p := ReaderPresentation{Status: status, Message: message, Commit: e.Commit, Format: readerFormat}
	key := readerHash(readerJSON(struct {
		Evidence                       readerEvidence
		Status, Message, SVG, Template string
		Poster, Still                  string `json:",omitempty"`
	}{e, status, message, svg, readerHTML, poster, still}))
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
	record := ""
	if e.Report != nil && e.Report.Reader != nil {
		b, _ := json.MarshalIndent(e.Report.Reader, "", "  ")
		record = string(b)
	}
	var b bytes.Buffer
	_ = readerTemplate.Execute(&b, struct {
		Evidence     readerEvidence
		Presentation ReaderPresentation
		Posters      *PosterLinks
		Record       string
	}{e, p, posters, record})
	return readerPage{repo: e.Repo, presentation: p, posters: posters, html: b.Bytes(), svg: []byte(svg), poster: []byte(poster), still: []byte(still)}
}

func (s *Server) keepReader(e readerEvidence) *ReaderPresentation {
	page := makeReader(e)
	key := strings.TrimPrefix(page.presentation.Fingerprint, "sha256:")
	s.mu.Lock()
	if s.readers == nil {
		s.readers = map[string]readerPage{}
	}
	s.readers[key] = page
	s.mu.Unlock()
	if s.Cache != "" {
		name := s.readerFile(key)
		err := os.MkdirAll(filepath.Dir(name), 0o755)
		if err == nil {
			err = os.WriteFile(name+".tmp", readerJSON(e), 0o600)
		}
		if err == nil {
			err = os.Rename(name+".tmp", name)
		}
		if err != nil {
			s.logf("keeping reader evidence: %v", err)
		}
	}
	return &page.presentation
}

// postersFor returns the poster links of a kept reader page, if it has any.
func (s *Server) postersFor(r *ReaderPresentation) *PosterLinks {
	if r == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readers[strings.TrimPrefix(r.Fingerprint, "sha256:")].posters
}

func (s *Server) readerFile(key string) string { return filepath.Join(s.Cache, "readers", key+".json") }

func (s *Server) readerRepo(name string) bool {
	for _, r := range s.Repos {
		if r.Name == name {
			return true
		}
	}
	return false
}

func (s *Server) serveReader(w http.ResponseWriter, r *http.Request) {
	parts := readerPath.FindStringSubmatch(r.URL.Path)
	s.mu.RLock()
	p, ok := s.readers[parts[1]]
	s.mu.RUnlock()
	if !ok || !s.readerRepo(p.repo) {
		http.NotFound(w, r)
		return
	}
	body, ct := p.html, "text/html; charset=utf-8"
	switch parts[2] {
	case "svg":
		body, ct = p.svg, "image/svg+xml; charset=utf-8"
	case "poster.svg":
		body, ct = p.poster, "image/svg+xml; charset=utf-8"
	case "still.svg":
		body, ct = p.still, "image/svg+xml; charset=utf-8"
	}
	if len(body) == 0 {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'; sandbox")
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	etag := `"` + readerHash(append([]byte(p.presentation.Fingerprint), body...)) + `"`
	h.Set("ETag", etag)
	for _, tag := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		tag = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(tag), "W/"))
		if tag == etag || tag == "*" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	h.Set("Content-Length", fmt.Sprint(len(body)))
	if r.Method != http.MethodHead {
		w.Write(body)
	}
}

// Validate each persisted entry against both its immutable URL and the snapshot
// card. Restore receipt sources too, so a failed refresh retains that evidence.
// restoreReaders reports whether it rejected any cached reader evidence.
func (s *Server) restoreReaders(snap *Snapshot) (rejected bool) {
	evidence := map[string][]readerEvidence{}
	runs := map[string]*RunRef{}
	for _, rs := range snap.Repos {
		runs[rs.Name] = rs.Receipts
	}
	if runs[snap.Repo] == nil {
		runs[snap.Repo] = snap.Receipts
	}
	projects := snap.Projects[:0]
	for _, p := range snap.Projects {
		if p.Repo == "" {
			p.Repo = snap.Repo
		}
		if !s.readerRepo(p.Repo) {
			continue
		}
		if p.Reader != nil {
			old := p.Reader
			run := runs[p.Repo]
			parts := readerPath.FindStringSubmatch(old.Page)
			var e readerEvidence
			ok := len(parts) == 3 && old.Format == readerFormat
			if ok {
				b, err := os.ReadFile(s.readerFile(parts[1]))
				ok = err == nil && decodeReaderJSON(b, &e) == nil && e.Format == readerFormat &&
					e.Repo == p.Repo && e.Dir == p.Dir && e.Commit == old.Commit && (run == nil || e.Commit == run.SHA)
			}
			if ok {
				page := makeReader(e)
				ok = page.presentation == *old
				if ok {
					s.mu.Lock()
					if s.readers == nil {
						s.readers = map[string]readerPage{}
					}
					s.readers[parts[1]] = page
					s.mu.Unlock()
					p.Poster = page.posters
				}
			}
			if !ok {
				rejected = true
				p.Poster = nil
				e = readerEvidence{Format: readerFormat, Repo: p.Repo, Dir: p.Dir, Commit: old.Commit,
					Manifest: project.Manifest{Name: p.Name, Language: p.Language}, Lock: project.Lock{Bounds: p.Bounds}, Problem: "cached_evidence"}
				if run != nil {
					e.Commit = run.SHA
				}
				// The snapshot identifies the project, but cannot replace its
				// damaged receipt or supply verification results on its own.
				p = projectOf(projectSource{dir: e.Dir, manifest: e.Manifest, lock: e.Lock, key: p.Model}, nil)
				p.Repo, p.Reader = e.Repo, s.keepReader(e)
			}
			evidence[p.Repo] = append(evidence[p.Repo], e)
		}
		projects = append(projects, p)
	}
	snap.Projects = projects
	for _, r := range s.Repos {
		if len(evidence[r.Name]) == 0 {
			continue
		}
		run := runs[r.Name]
		if run == nil {
			continue
		}
		set := &receiptSet{run: github.Run{ID: run.ID, HeadSHA: run.SHA, Name: "gate", Status: run.Status, Conclusion: run.Conclusion},
			reports: map[string]*verify.Report{}, readers: map[string]readerArtifacts{}, traces: map[string]map[string]tlc.TraceFile{}}
		var sources []projectSource
		for _, e := range evidence[r.Name] {
			if e.Problem == "cached_evidence" || e.Problem == "missing_archive" {
				set.retry = true
			}
			name := path.Base(e.Dir)
			set.reports[name], set.readers[name] = e.Report, e.Artifacts
			set.traces[name] = e.Traces
			for _, p := range snap.Projects {
				if p.Repo == r.Name && p.Dir == e.Dir {
					sources = append(sources, projectSource{dir: e.Dir, manifest: e.Manifest, lock: e.Lock, key: p.Model,
						files: e.Files, module: strings.TrimSuffix(path.Base(e.Manifest.Module), ".tla"), view: e.View, comparison: e.Comparison,
						manifestBytes: e.ManifestBytes, lockBytes: e.LockBytes, reader: p.Reader})
				}
			}
		}
		r.src.receipts, r.src.projects = set, sources
	}
	return rejected
}
