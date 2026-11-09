package dashboard

import (
	"crypto/sha256"
	"fmt"
	"html"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
)

// A proposal's page draws its checked chart as the poster people read, with
// its still twin, lists the rules a person would ratify and the bounds, and
// keeps the complete evidence image one link away. Its card gets the poster
// in place of the evidence image (#784).
func TestAProposalPageShowsItsPoster784(t *testing.T) {
	f := reader631FixtureFor(t, 2, "Ready")
	model, view := string(f.Files[f.Manifest.Module]), string(f.Files[".invariant/view.json"])
	includes := map[string]string{"Helper631.tla": string(f.Files[".invariant/specs/Helper631.tla"])}
	p := &formalize.Proposal{Draft: formalize.Draft{Name: "Compile ownership", Module: "Dashboard631", Bounds: f.Lock.Bounds, Statements: f.Lock.Statements},
		Hash: project.ProposalHash(f.Lock.Bounds, f.Lock.Statements), ModuleText: model, Includes: includes, View: view,
		Reader: &formalize.Reader{Version: 1, Chart: f.Report.Reader.Chart, Limitation: f.Report.Reader.Limitation}}
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
	}{1, p.Module, p.ModuleText, p.Includes, p.View, p.Bounds, p.Statements, p.Reader.Chart, p.Reader.Limitation})
	p.Reader.Fingerprint = fmt.Sprintf("sha256:%x", sha256.Sum256(b))
	if err := p.ValidateReader(); err != nil {
		t.Fatal(err)
	}

	page := makeComparison(comparisonEvidence{Repo: "gitdek/ask3d", Issue: 16, Proposal: p})
	pr, posters := page.presentation, page.posters
	if pr.Status != "chart" || posters == nil || len(page.poster) == 0 || len(page.still) == 0 {
		t.Fatalf("a charted proposal's page has status %q, poster links %+v, %d poster bytes and %d still bytes; want a chart with both posters", pr.Status, posters, len(page.poster), len(page.still))
	}
	base := strings.TrimSuffix(pr.Page, ".html")
	if posters.Animated != base+".poster.svg" || posters.Still != base+".still.svg" {
		t.Errorf("poster links %+v; want %s.poster.svg and %s.still.svg", posters, base, base)
	}
	for _, poster := range [][]byte{page.poster, page.still} {
		if !strings.HasPrefix(strings.TrimSpace(string(poster)), "<svg") || strings.Contains(string(poster), "<script") {
			t.Errorf("a poster isn't a script-free SVG: %.80s", poster)
		}
	}
	h := html.UnescapeString(string(page.html))
	for _, want := range []string{
		`<img class="poster" src="` + posters.Animated + `"`, `srcset="` + posters.Still + `"`,
		`<a href="` + pr.SVG + `">Open the complete evidence image`, "The rules you would ratify", "Checked bounds",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("the proposal page doesn't have %q", want)
		}
	}
	for _, s := range p.Statements {
		if !strings.Contains(h, s.Says) {
			t.Errorf("the proposal page doesn't say rule %s: %q", s.Name, s.Says)
		}
	}
	if strings.Contains(h, `<img src="`+pr.SVG+`"`) {
		t.Error("the proposal page still shows the complete evidence image as its picture")
	}
	if !strings.Contains(string(assets["/app.js"]), `p.posters ? `) || !strings.Contains(string(assets["/app.js"]), `posters: w.posters`) {
		t.Error("a card in Needs you doesn't prefer the poster to the evidence image")
	}
}
