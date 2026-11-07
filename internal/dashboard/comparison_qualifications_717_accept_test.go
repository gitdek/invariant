package dashboard

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/verify"
)

// Read rendered SVG text as a user would: attributes and hidden metadata
// cannot satisfy the qualification assertions. Preserve wrapped text lines.
func comparison717SVGText(t *testing.T, svg string) string {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(svg))
	var out strings.Builder
	depth := 0
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch v := token.(type) {
		case xml.StartElement:
			if v.Name.Local == "script" || v.Name.Local == "foreignObject" {
				t.Fatal("literal source text became active SVG")
			}
			for _, attr := range v.Attr {
				if strings.HasPrefix(strings.ToLower(attr.Name.Local), "on") {
					t.Fatal("active SVG attribute")
				}
			}
			if v.Name.Local == "text" {
				depth++
			}
		case xml.EndElement:
			if v.Name.Local == "text" {
				depth--
				out.WriteByte(' ')
			}
		case xml.CharData:
			if depth > 0 {
				out.Write(v)
			}
		}
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

func comparison717Proposal(t *testing.T, c *statechart.Comparison) *formalize.Proposal {
	t.Helper()
	s := c.Candidate
	p := &formalize.Proposal{Draft: formalize.Draft{Name: "Comparison qualifications", Module: s.Module, Bounds: s.Lock.Bounds, Statements: s.Lock.Statements},
		Hash: project.ProposalHash(s.Lock.Bounds, s.Lock.Statements), ModuleText: s.Model, Includes: s.Includes, View: s.View,
		Reader: &formalize.Reader{Version: 1, Chart: s.Chart, Limitation: s.Limitation, Comparison: c}}
	// The existing reader wire contract is sealed independently, so the
	// dashboard's real publication boundary is exercised, not bypassed.
	b := reader631JSON(t, struct {
		Version    int                    `json:"version"`
		Module     string                 `json:"module"`
		Model      string                 `json:"model"`
		Includes   map[string]string      `json:"includes,omitempty"`
		View       string                 `json:"view"`
		Bounds     map[string]string      `json:"bounds"`
		Statements []project.Statement    `json:"statements"`
		Chart      *statechart.Result     `json:"chart,omitempty"`
		Limitation *statechart.Limitation `json:"limitation,omitempty"`
		Comparison *statechart.Comparison `json:"comparison,omitempty"`
	}{1, p.Module, p.ModuleText, p.Includes, p.View, p.Bounds, p.Statements, p.Reader.Chart, p.Reader.Limitation, c})
	p.Reader.Fingerprint = fmt.Sprintf("sha256:%x", sha256.Sum256(b))
	if err := p.ValidateReader(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestComparison717QualificationsAcrossPublishers(t *testing.T) {
	const hostile = "Ready λ <script>alert('Ω')</script> & | `literal`"
	const baselineName = "Baseline λ <script>literal</script> & | `source`"
	snapshot := func(f reader631Fixture, name string) statechart.ComparisonSnapshot {
		return statechart.ComparisonSnapshot{Kind: "proposal", Name: name, Module: "Dashboard631", Model: string(f.Files[f.Manifest.Module]),
			Includes: map[string]string{"Helper631.tla": string(f.Files[".invariant/specs/Helper631.tla"])}, View: string(f.Files[".invariant/view.json"]),
			Lock: f.Lock, Chart: f.Report.Reader.Chart, Limitation: f.Report.Reader.Limitation}
	}
	for _, mode := range []string{"unchanged", "view", "model", "include", "bounds", "baseline limited", "candidate limited"} {
		t.Run(mode, func(t *testing.T) {
			a := reader631FixtureFor(t, 2, hostile)
			b := reader631FixtureFor(t, 2, hostile)
			switch mode {
			case "view":
				b = reader631FixtureFor(t, 2, hostile+" revised")
			case "model":
				b.Files[b.Manifest.Module] = append(b.Files[b.Manifest.Module], []byte("\\* model changed\n")...)
			case "include":
				b.Files[".invariant/specs/Helper631.tla"] = append(b.Files[".invariant/specs/Helper631.tla"], []byte("\\* include changed\n")...)
			case "bounds":
				b = reader631FixtureFor(t, 3, hostile)
			case "baseline limited":
				a = reader631FixtureFor(t, 501, hostile)
			case "candidate limited":
				b = reader631FixtureFor(t, 501, hostile)
			}
			candidateName := "Proposal " + project.ProposalHash(b.Lock.Bounds, b.Lock.Statements) + " with view " + reader631Hash(b.Files[".invariant/view.json"])[:19]
			c, err := statechart.Compare(snapshot(a, baselineName), snapshot(b, candidateName))
			if err != nil {
				t.Fatal(err)
			}
			before := reader631JSON(t, c)
			p := comparison717Proposal(t, c)
			proposalPage := makeComparison(comparisonEvidence{Format: comparisonFormat, Repo: "o/r", Issue: 717, Proposal: p})
			if proposalPage.presentation.Status != "chart" {
				t.Fatalf("proposal page refused: %+v", proposalPage.presentation)
			}
			// Receipts must keep the original proposal identities while naming
			// the separately checked final sources, including include changes.
			b.Report.Reader.Comparison = c
			b.Files[".invariant/comparison.json"] = reader631JSON(t, c)
			b.Files[b.Manifest.Module] = append(b.Files[b.Manifest.Module], []byte("\\* independent final model\n")...)
			b.bind(t)
			r := b.Report
			fingerprint := verify.Fingerprint(*r)
			archive := t.TempDir()
			if err := receipt.Write(archive, r); err != nil {
				t.Fatal(err)
			}
			read := func(name string) []byte {
				data, err := os.ReadFile(filepath.Join(archive, name))
				if err != nil {
					t.Fatal(err)
				}
				return data
			}
			companion, receiptSVG, receiptMD := read("reader.json"), read("reader.svg"), read("receipt.md")
			e := readerEvidence{Format: readerFormat, Repo: "o/r", Dir: b.Dir, Commit: strings.Repeat("7", 40), Manifest: b.Manifest, Lock: b.Lock,
				ManifestBytes: b.Files[".invariant/invariant.json"], LockBytes: b.Files[".invariant/ratified.lock"], Files: map[string][]byte{"Dashboard631.tla": b.Files[b.Manifest.Module], "Helper631.tla": b.Files[".invariant/specs/Helper631.tla"]},
				View: b.Files[".invariant/view.json"], Comparison: b.Files[".invariant/comparison.json"], Report: r,
				Artifacts: readerArtifacts{JSON: companion, SVG: receiptSVG}}
			receiptPage := makeReader(e)
			if receiptPage.presentation.Status != "chart" && receiptPage.presentation.Status != "too_large" {
				t.Fatalf("receipt page refused: %+v", receiptPage.presentation)
			}
			svg, err := c.SVG()
			if err != nil {
				t.Fatal(err)
			}
			outputs := map[string]string{
				"check feedback":             p.ReaderFeedback(),
				"comparison Markdown":        html.UnescapeString(c.Markdown()),
				"comparison SVG":             comparison717SVGText(t, svg),
				"receipt notes":              strings.Join(r.Reader.ComparisonNotes(), " "),
				"receipt Markdown":           html.UnescapeString(string(receiptMD)),
				"receipt SVG":                comparison717SVGText(t, string(receiptSVG)),
				"dashboard proposal message": proposalPage.presentation.Message,
				"dashboard receipt message":  receiptPage.presentation.Message,
			}
			qualifications := []string{"Equal charts do not establish behavioral equivalence. This bounded, view-qualified comparison adds no proof claim."}
			if c.ViewChanged {
				qualifications = append(qualifications, "View change: labels or hiding definitions differ; chart differences may reflect the view.")
			}
			if c.ModelChanged {
				qualifications = append(qualifications, "Model source change: exact model or sibling include bytes differ.")
			}
			if c.BoundsChanged {
				qualifications = append(qualifications, c.Scope)
			}
			if c.Unavailable != nil {
				qualifications = append(qualifications, c.Unavailable.Message)
			}
			for surface, text := range outputs {
				text = strings.Join(strings.Fields(text), " ")
				for _, want := range qualifications {
					if !strings.Contains(text, want) {
						t.Errorf("%s omits shared qualification %q", surface, want)
					}
				}
			}
			for _, text := range []string{html.UnescapeString(c.Markdown()), comparison717SVGText(t, svg), html.UnescapeString(string(proposalPage.html)), html.UnescapeString(string(receiptPage.html))} {
				for _, want := range []string{baselineName, candidateName, c.BaselineIdentity.ModelSHA256, c.CandidateIdentity.ViewSHA256, "Limit"} {
					if !strings.Contains(text, want) {
						t.Errorf("literal name or identity %q lost", want)
					}
				}
			}
			if !strings.Contains(comparison717SVGText(t, svg), hostile) {
				t.Error("hostile/Unicode chart label changed")
			}
			for _, page := range [][]byte{proposalPage.html, receiptPage.html} {
				if bytes.Contains(page, []byte("<script>")) {
					t.Error("literal source became HTML")
				}
			}
			for _, text := range []string{strings.Join(r.Reader.ComparisonNotes(), " "), receiptPage.presentation.Message} {
				if !strings.Contains(text, "Final source identities differ") || !strings.Contains(text, r.Reader.Inputs.Model) {
					t.Error("final source distinction lost")
				}
			}
			var restored verify.Reader
			if err := json.Unmarshal(companion, &restored); err != nil {
				t.Fatal(err)
			}
			if restored.Comparison == nil || !bytes.Equal(before, reader631JSON(t, restored.Comparison)) || !bytes.Equal(before, reader631JSON(t, c)) || fingerprint != verify.Fingerprint(*r) {
				t.Fatal("publication changed retained evidence or verification identity")
			}
		})
	}
}
