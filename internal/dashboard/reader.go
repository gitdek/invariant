package dashboard

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"maps"
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
	Comparison                []byte
	Report                    *verify.Report
	Artifacts                 readerArtifacts
	Traces                    map[string]tlc.TraceFile
	Problem                   string
}

type readerPage struct {
	repo         string
	presentation ReaderPresentation
	html, svg    []byte
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
	if rr.Comparison != nil {
		var captured statechart.Comparison
		if err := rr.Comparison.Validate(); err != nil {
			return bad("comparison_evidence", err.Error())
		}
		if decodeReaderJSON(e.Comparison, &captured) != nil || !sameReaderJSON(&captured, rr.Comparison) || rr.Comparison.CandidateIdentity.ProposalHash != project.ProposalHash(e.Lock.Bounds, e.Lock.Statements) {
			return bad("comparison_evidence", "The proposal comparison differs from the captured comparison at this receipt commit. Verify again using the original project evidence.")
		}
	}
	if len(e.Artifacts.JSON) == 0 || (rr.Chart != nil && len(e.Artifacts.SVG) == 0) {
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
		if rr.Comparison == nil {
			return bad(code, message)
		}
		svg, err := rr.SVG()
		if err != nil {
			return bad("reader_render", err.Error())
		}
		if !bytes.Equal(e.Artifacts.SVG, []byte(svg)) {
			return bad("artifact_mismatch", "The archived comparison SVG differs from the native renderer.")
		}
		return "chart", message + " " + strings.Join(rr.ComparisonNotes(), " ") + " " + strings.Join(comparisonPageNotes(rr.Comparison), " "), svg
	}
	if d := rr.Unavailable; d != nil {
		if len(e.Artifacts.SVG) != 0 && rr.Comparison == nil {
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
		if (len(e.Artifacts.SVG) != 0 && rr.Comparison == nil) || l.Code != "too_large" || l.Actual <= statechart.MaxStates || l.Limit != statechart.MaxStates ||
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
	svg, err := rr.SVG()
	if err != nil {
		return bad("reader_render", "Reader generation failed: "+err.Error())
	}
	if !bytes.Equal(e.Artifacts.SVG, []byte(svg)) {
		return bad("artifact_mismatch", "The archived SVG does not match the shared native renderer's complete checked result. Verify again with the current renderer.")
	}
	message = "Checked bounded model; the full report is included in the image. This adds no proof claim."
	if len(rr.ComparisonNotes()) > 0 {
		message += " " + strings.Join(rr.ComparisonNotes(), " ")
	}
	if rr.Comparison != nil {
		message += " " + strings.Join(comparisonPageNotes(rr.Comparison), " ")
	}
	return "chart", message, svg
}

const readerHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Evidence.Manifest.Name}} — reader evidence</title>
<style>body{font:16px system-ui,sans-serif;margin:2rem;line-height:1.5;color:#17202a;background:#fff}main{max-width:100rem;margin:auto}pre{white-space:pre-wrap;overflow-wrap:anywhere}img{display:block;width:100%;height:auto}code{overflow-wrap:anywhere}dt{font-weight:bold}dd{margin-bottom:1rem}</style></head>
<body><main><h1>{{.Evidence.Manifest.Name}}</h1><p>{{.Evidence.Repo}} · {{.Evidence.Dir}}</p>
<p>Receipt commit: <code>{{.Presentation.Commit}}</code></p>
<p>{{.Presentation.Message}}</p>
{{if .Presentation.SVG}}<p><a href="{{.Presentation.SVG}}">Open standalone SVG at full size</a></p><img src="{{.Presentation.SVG}}" alt="Checked reader chart and complete report for {{.Evidence.Manifest.Name}}">{{end}}
<h2>Checked bounds</h2><dl>{{range $name, $value := .Evidence.Lock.Bounds}}<dt>{{$name}}</dt><dd><pre>{{$value}}</pre></dd>{{end}}</dl>
<h2>Locked rules</h2>{{range .Evidence.Lock.Statements}}<h3>{{.Name}} ({{.Kind}})</h3><pre>{{.Says}}</pre><p>Pin: <code>{{.SHA256}}</code></p>{{end}}
{{if .Record}}<details><summary>Complete receipt reader record</summary><pre>{{.Record}}</pre></details>{{end}}
<p>Presentation: <code>{{.Presentation.Fingerprint}}</code><br>Format: {{.Presentation.Format}}</p>
<p>This explanation describes the model at the receipt's bounds. It does not change the receipt's verification verdict or code assurance.</p>
</main></body></html>`

var readerTemplate = template.Must(template.New("reader").Parse(readerHTML))
var readerPath = regexp.MustCompile(`^/reader/([0-9a-f]{64})\.(html|svg)$`)

func makeReader(e readerEvidence) readerPage {
	status, message, svg := e.checked()
	p := ReaderPresentation{Status: status, Message: message, Commit: e.Commit, Format: readerFormat}
	key := readerHash(readerJSON(struct {
		Evidence                       readerEvidence
		Status, Message, SVG, Template string
	}{e, status, message, svg, readerHTML}))
	p.Fingerprint = key
	base := "/reader/" + strings.TrimPrefix(key, "sha256:")
	p.Page = base + ".html"
	if svg != "" {
		p.SVG = base + ".svg"
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
		Record       string
	}{e, p, record})
	return readerPage{repo: e.Repo, presentation: p, html: b.Bytes(), svg: []byte(svg)}
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
	if parts[2] == "svg" {
		body, ct = p.svg, "image/svg+xml; charset=utf-8"
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
				}
			}
			if !ok {
				rejected = true
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
