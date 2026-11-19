package dashboard

import (
	"bytes"
	"testing"

	"github.com/gitdek/invariant/internal/statechart"
)

func TestReader963RemainingVolumeSidesAndRecovery(t *testing.T) {
	for _, kind := range []string{"replay_bytes", "steps", "invariant_cells"} {
		for _, side := range []string{"baseline", "candidate", "both"} {
			t.Run(kind+"/"+side, func(t *testing.T) {
				a, b := reader631FixtureFor(t, 2, "Before"), reader631FixtureFor(t, 3, "After")
				if side != "candidate" {
					a = reader963Limited(t, kind)
				}
				if side != "baseline" {
					b = reader963Limited(t, kind)
				}
				p := comparison717Proposal(t, reader963Comparison(t, a, b))
				e := comparisonEvidence{Format: comparisonFormat, Repo: "owner/limits", Issue: 963, Proposal: p}
				s := &Server{Repos: []*Repo{{Name: e.Repo}}, Cache: t.TempDir()}
				first := s.keepComparison(e)
				_, fold := reader963Unavailable(t, s, first)
				reader963BothSources(t, fold, p)
				snap := &Snapshot{Repo: e.Repo, Issues: []Issue{{Repo: e.Repo, Number: 963, Waiting: &Waiting{Proposal: p, Reader: first}}}}
				cold := &Server{Repos: s.Repos, Cache: s.Cache}
				cold.restoreComparisons(snap)
				got := snap.Issues[0].Waiting.Reader
				if *got != *first || *cold.keepComparison(e) != *first {
					t.Fatal("restart or refresh changed validated limitation")
				}
				reader963Unavailable(t, cold, got)
			})
		}
	}
}

func TestReader963LargeDrawableProjections(t *testing.T) {
	a := dashboard944Fixture(t, 24760, reader963Hostile)
	b := dashboard944Fixture(t, 24761, "Revised "+reader963Hostile)
	if len(a.Report.Reader.Chart.States) > 500 || a.Report.Reader.Chart.Report.Counts["tlc_states"] != 24760 {
		t.Fatal("fixture must retain every concrete state in a fitting projection")
	}
	p := reader963Proposal(t, a)
	before := reader631JSON(t, p)
	page := makeComparison(comparisonEvidence{Format: comparisonFormat, Repo: "owner/large", Issue: 963, Proposal: p})
	want, err := p.Reader.Chart.SVG()
	if err != nil || page.presentation.Status != "chart" || string(page.svg) != want || page.posters == nil {
		t.Fatal("24,760 concrete states with a fitting projection lost its native chart", err)
	}
	if !bytes.Equal(before, reader631JSON(t, p)) {
		t.Fatal("presentation changed the checked projection")
	}
	c := reader963Comparison(t, a, b)
	if c.Unavailable != nil {
		t.Fatal("two independently fitting large projections must fit this comparison")
	}
	p = comparison717Proposal(t, c)
	before = reader631JSON(t, p)
	page = makeComparison(comparisonEvidence{Format: comparisonFormat, Repo: "owner/large", Issue: 963, Proposal: p})
	want, err = c.SVG()
	if err != nil || page.presentation.Status != "chart" || string(page.svg) != want || page.posters == nil {
		t.Fatal("fitting large comparison lost its native chart", err)
	}
	readerFoldedEvidence(t, page.html, page.presentation.Message)
	if !bytes.Equal(before, reader631JSON(t, p)) {
		t.Fatal("presentation changed the comparison or its fingerprints")
	}
}

func TestReader963DrawableCompatibility(t *testing.T) {
	f := reader631FixtureFor(t, 3, "Legacy ready")
	page := makeReader(receipt723Evidence(t, f))
	// This identity was pinned on the original checkout by #723. It binds
	// the original complete canonical record, SVG and both poster bytes.
	if page.presentation.Fingerprint != "sha256:883064660d238f3da66b73b6430aa35c1a36f822046c7d6817cfe00c2aa7fcb6" {
		t.Fatal("pre-change drawable evidence identity changed")
	}
	for _, fixture := range []reader631Fixture{f, receipt723Fixture(t)} {
		e := receipt723Evidence(t, fixture)
		original := reader631JSON(t, fixture.Report)
		markdown := fixture.Report.Reader.Chart.Markdown()
		c := fixture.Report.Reader.Comparison
		comparisonMarkdown, comparisonFingerprint := "", ""
		if c != nil {
			comparisonMarkdown, comparisonFingerprint = c.Markdown(), c.Fingerprint
		}
		page := makeReader(e)
		want, err := fixture.Report.Reader.SVG()
		if err != nil || string(page.svg) != want {
			t.Fatal("drawable receipt bytes differ from the unchanged shared renderer", err)
		}
		for _, animated := range []bool{false, true} {
			want, err := fixture.Report.Reader.Chart.Poster(statechart.PosterOptions{Title: fixture.Manifest.Name, Animated: animated})
			got := page.still
			if animated {
				got = page.poster
			}
			if err != nil || string(got) != want {
				t.Fatal("drawable poster bytes changed", err)
			}
		}
		if !bytes.Equal(original, reader631JSON(t, fixture.Report)) || markdown != fixture.Report.Reader.Chart.Markdown() ||
			c != nil && (comparisonMarkdown != c.Markdown() || comparisonFingerprint != c.Fingerprint) {
			t.Fatal("presentation changed canonical evidence, Markdown or fingerprints")
		}
	}
}

func TestReader963SharedGoldens(t *testing.T) {
	f := receipt723Fixture(t)
	r := f.Report.Reader
	c := r.Comparison
	svg, err := r.SVG()
	if err != nil {
		t.Fatal(err)
	}
	comparisonSVG, err := c.SVG()
	if err != nil {
		t.Fatal(err)
	}
	animated, err := r.Chart.Poster(statechart.PosterOptions{Title: f.Manifest.Name, Animated: true})
	if err != nil {
		t.Fatal(err)
	}
	still, err := r.Chart.Poster(statechart.PosterOptions{Title: f.Manifest.Name})
	if err != nil {
		t.Fatal(err)
	}
	// The shared statechart and verification renderers are the unchanged
	// pre-963 implementations. Pin their complete bytes as well as identities.
	want := map[string]string{
		"reader":                 "sha256:1f7c9e9a42c46bc5b2124b73f759684ebea51d0f00cac6dc78e450284fd902bd",
		"reader fingerprint":     "sha256:3257789c8bee6b7298c9d51f412ea3da4b3082d0b6f20f4b3f2062429b87ccc5",
		"comparison":             "sha256:61f33f63d462de74e80d05826a7ccc5516a2534a890e0da9aedae421f5891f93",
		"comparison fingerprint": "sha256:bc0e8cac4be9f8695eced6fa62fac3a36197159a411bfbd2e39d442ea07e393e",
		"chart Markdown":         "sha256:5a7dfe4e5a905ec3f71d2d97c9ac813ee4d4c6f45abfd0603c65d5359abf5c71",
		"comparison Markdown":    "sha256:178e4fd10ede5d5a3aed60e7f1583ae238d2c5cd2c35d4f4c51cbd2c58741eb8",
		"receipt SVG":            "sha256:5daad625f145c9d607212d86c5f6dd6feccd0f597f1e9b7811d41f22cd1f73bc",
		"comparison SVG":         "sha256:4f245d3439696869f40b0e8944b328331fd5bc30fe5f3aa35f72433ec24660de",
		"animated":               "sha256:92ba111993c427903bf53eba7863e5b26c6f074c0c224e500747840d99a11f1f",
		"still":                  "sha256:17d5c4444f48f74ddcf6dbbde79ca9c43906b42fc1a513c67904ebe89fa347e9",
	}
	for name, value := range map[string]string{
		"reader": readerHash(reader631JSON(t, r)), "reader fingerprint": r.Fingerprint,
		"comparison": readerHash(reader631JSON(t, c)), "comparison fingerprint": c.Fingerprint,
		"chart Markdown": readerHash([]byte(r.Chart.Markdown())), "comparison Markdown": readerHash([]byte(c.Markdown())),
		"receipt SVG": readerHash([]byte(svg)), "comparison SVG": readerHash([]byte(comparisonSVG)),
		"animated": readerHash([]byte(animated)), "still": readerHash([]byte(still)),
	} {
		if value != want[name] {
			t.Errorf("%s changed: %s, want %s", name, value, want[name])
		}
	}
}
