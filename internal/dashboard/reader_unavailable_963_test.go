package dashboard

import (
	"bytes"
	"path"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
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

// A limited side keeps its own reason when the combined evidence overflows
// too. Naming only the combined measure, which checked the fitting side's
// view alone, would hide a side whose view was never evaluated.
func TestReader963LimitedSideBesideCombinedVolume(t *testing.T) {
	a, b := reader963Limited(t, "historical"), reader631FixtureFor(t, 3, "After")
	candidateName := "Proposal " + project.ProposalHash(b.Lock.Bounds, b.Lock.Statements) + " with view " + reader631Hash(b.Files[".invariant/view.json"])[:19]
	as, bs := receipt723Snapshot(a, "Baseline "+reader963Hostile), receipt723Snapshot(b, candidateName)
	as.Model += "\n\\* " + strings.Repeat("x", 31<<20) + "\n"
	bs.Model += "\n\\* " + strings.Repeat("y", 31<<20) + "\n"
	c, err := statechart.Compare(as, bs)
	if err != nil || c.Volume() == nil || c.Baseline.Limitation == nil || c.Candidate.Chart == nil {
		t.Fatalf("fixture must limit the baseline and overflow the combined evidence: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	// TestReader963CombinedComparisonVolume already reads this page's shape
	// over HTTP; parsing its 62 MiB fold again would only repeat that cost.
	page := makeComparison(comparisonEvidence{Format: comparisonFormat, Repo: "owner/volume", Issue: 963, Proposal: comparison717Proposal(t, c)})
	r := page.presentation
	if r.Status != "too_large" || len(page.svg) != 0 || page.posters != nil {
		t.Fatalf("unavailable comparison advertised a chart or image: %+v", r)
	}
	if len([]rune(r.Message)) > 420 || strings.Contains(r.Message, "sha256:") || strings.Contains(r.Message, "too_large") {
		t.Errorf("reason is not a concise human-readable explanation: %q", r.Message)
	}
	l := c.Volume()
	reader963Words(t, r.Message, "baseline", "501", "earlier", "not evaluated", "fresh", "combined", "json",
		strings.TrimSuffix(counted(int64(l.Actual), "unit"), " units"), "62,914,560", "candidate view checked")
	if side, combined := strings.Index(r.Message, "baseline"), strings.Index(r.Message, "combined"); side < 0 || combined < side {
		t.Errorf("the limited side must be named before the combined overflow: %q", r.Message)
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

// When a receipt's final model draws but its retained comparison can't, the
// page still leads with the final model's own outcome. The comparison's
// reason is the card's message, and on the page it sits in the comparison's
// own section, with complete closed evidence naming the final model too.
func TestReader963ReceiptLeadsWithItsFinalModel(t *testing.T) {
	for _, mode := range []string{"limited", "damaged"} {
		t.Run(mode, func(t *testing.T) {
			f := receipt723Fixture(t)
			var c *statechart.Comparison
			if mode == "limited" {
				f = reader631FixtureFor(t, 3, "Independent final")
				c = reader963Comparison(t, reader963Limited(t, "historical"), reader631FixtureFor(t, 3, "Candidate"))
				f.Files[f.Manifest.Module] = append(f.Files[f.Manifest.Module], []byte("\\* independent final source\n")...)
				f.Report.Reader.Comparison, f.Files[".invariant/comparison.json"] = c, reader631JSON(t, c)
				f.bind(t)
			} else {
				f.Files[".invariant/comparison.json"] = []byte(`{"version":`)
			}
			s, _, p := receipt723Load(t, f, nil)
			if p.Status != "chart" || p.SVG == "" || !strings.HasPrefix(p.Message, "Comparison unavailable:") {
				t.Fatalf("fixture must draw its final chart beside an unavailable comparison: %+v", p)
			}
			n := reader963GET(t, s, p.Page)
			var lede string
			for _, div := range n.all("div") {
				if div.attrs["class"] != "hero" {
					continue
				}
				for _, para := range div.all("p") {
					if _, ok := para.attrs["data-reader-reason"]; ok {
						lede = reader963Space(para.words(true))
					}
				}
			}
			if !strings.HasPrefix(lede, "Checked bounded model;") || !strings.Contains(lede, "This adds no proof claim.") {
				t.Errorf("the page's lead sentence must describe the final model: %q", lede)
			}
			fold := reader963Fold(t, n, p.Message)
			inSection := false
			for at := fold.parent; at != nil; at = at.parent {
				if at.tag == "section" && len(at.all("h2")) > 0 && reader963Space(at.all("h2")[0].words(true)) == "Retained proposal comparison" {
					inSection = true
				}
			}
			if !inSection {
				t.Error("the comparison's reason and evidence must sit in its own section")
			}
			reader963Words(t, fold.words(true), "Independently checked final model", f.Report.Reader.Inputs.Model)
			folds := 0
			for _, d := range n.all("details") {
				if _, ok := d.attrs["data-reader-details"]; ok {
					folds++
				}
			}
			if folds != 1 {
				t.Errorf("the page folds its complete evidence %d times, want once", folds)
			}
			if c != nil {
				reader963Words(t, fold.words(true), c.Baseline.Name, c.Candidate.Name, c.BaselineIdentity.ModelSHA256, c.CandidateIdentity.ViewSHA256, c.Fingerprint)
			} else {
				reader963Words(t, fold.words(true), "comparison_evidence", "unexpected EOF")
			}
			receipt723Topology(t, receipt723HTTP(t, s, p.SVG), f.Report.Reader.Chart)
		})
	}
}

// A receipt too large to hold its comparison inline keeps it by reference.
// That storage isn't why the comparison can't be drawn when one of its own
// sides is limited: the sentence names that side, then the comparison's own
// combined overflow, as a proposal page does.
func TestReader963ReferencedComparisonNamesItsLimitedSide(t *testing.T) {
	baseline := receipt723Snapshot(reader631FixtureFor(t, 1, "Captured baseline"), "Baseline")
	candidate := receipt723Snapshot(reader631FixtureFor(t, 501, "Captured candidate"), "Candidate")
	comment := "\n\\* " + strings.Repeat("x", 31<<20) + "\n"
	baseline.Model += comment
	candidate.Model += comment
	c, err := statechart.Compare(baseline, candidate)
	if err != nil || c.Volume() == nil || c.Candidate.Limitation == nil || c.Baseline.Chart == nil {
		t.Fatal("comparison must limit its candidate and overflow as a whole", err)
	}
	captured, companions, err := statechart.ComparisonFiles(c)
	if err != nil {
		t.Fatal(err)
	}
	f := dashboard944Fixture(t, 501, "Independent final")
	f.Report.Reader.Comparison = c
	f.bind(t)
	dir := t.TempDir()
	if err := receipt.Write(dir, f.Report); err != nil {
		t.Fatal(err)
	}
	set, err := readReceiptZip(dashboard944Zip(t, dir))
	if err != nil || set.reports["reader"] == nil || set.reports["reader"].Reader == nil {
		t.Fatal("archive lost its reader", err)
	}
	files := map[string][]byte{}
	for name, data := range f.Files {
		if path.Dir(name) == path.Dir(f.Manifest.Module) && strings.HasSuffix(name, ".tla") {
			files[path.Base(name)] = data
		}
	}
	e := readerEvidence{Format: readerFormat, Repo: receipt723RepoName, Dir: f.Dir, Commit: receipt723Commit,
		Manifest: f.Manifest, Lock: f.Lock, ManifestBytes: f.Files[".invariant/invariant.json"], LockBytes: f.Files[".invariant/ratified.lock"],
		Files: files, View: f.Files[".invariant/view.json"], Comparison: captured, ComparisonCompanions: companions,
		Report: set.reports["reader"], Artifacts: set.readers["reader"]}
	if _, ok := e.Artifacts.Companions["reader-comparison.json"]; !ok {
		t.Fatal("receipt must keep its comparison by reference")
	}
	p := makeReader(e).presentation
	if p.Status != "chart" || p.SVG == "" {
		t.Fatalf("final chart must draw beside its referenced comparison: %+v", p)
	}
	if len([]rune(p.Message)) > 420 || strings.Contains(p.Message, "sha256:") || strings.Contains(p.Message, "too_large") {
		t.Errorf("reason is not a concise human-readable explanation: %q", p.Message)
	}
	reader963Words(t, p.Message, "Comparison unavailable", "candidate", "501", "earlier", "fresh", "combined", "baseline view checked")
	if side, combined := strings.Index(p.Message, "candidate"), strings.Index(p.Message, "combined"); side < 0 || combined < side {
		t.Errorf("the limited side must be named before the combined overflow: %q", p.Message)
	}
}
