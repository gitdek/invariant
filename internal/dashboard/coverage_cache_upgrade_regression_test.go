package dashboard

import (
	"bytes"
	"encoding/json"
	"html/template"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/verify"
)

func TestCoverageCacheUpgradePreservesEvidence802(t *testing.T) {
	f := reader631FixtureFor(t, 5, "Cached sample")
	f.Report.ModelOnly = false
	f.Report.Conformance = &conformance.Result{Passed: true, States: 2, ModelStates: 5}
	f.bind(t)
	e := receipt723Evidence(t, f)
	current := makeReader(e)
	if current.presentation.Status != "chart" || current.posters == nil {
		t.Fatal("fixture must have checked reader and poster evidence")
	}

	// Render the frozen pre-coverage template to model a saved page from an
	// earlier binary. Its HTML hash changes; the checked inputs do not.
	old := current.presentation
	old.Page, _, _ = strings.Cut(old.Page, "?")
	var legacy bytes.Buffer
	err := template.Must(template.New("legacy").Parse(readerIdentityTemplate)).Execute(&legacy, struct {
		Evidence     readerEvidence
		Presentation ReaderPresentation
		Posters      *PosterLinks
		Record       string
	}{e, old, current.posters, string(reader631JSON(t, e.Report.Reader))})
	if err != nil {
		t.Fatal(err)
	}
	old.Page += "?h=" + strings.TrimPrefix(readerHash(legacy.Bytes()), "sha256:")[:16]
	if old.Page == current.presentation.Page || bytes.Contains(legacy.Bytes(), []byte("Conformance coverage")) {
		t.Fatal("upgrade fixture must use different, pre-coverage markup")
	}

	for _, tc := range []struct {
		name   string
		edit   func(*Project, *readerEvidence)
		accept bool
	}{
		{"old markup", func(*Project, *readerEvidence) {}, true},
		{"unversioned page", func(p *Project, _ *readerEvidence) { p.Reader.Page, _, _ = strings.Cut(p.Reader.Page, "?") }, true},
		{"wrong fingerprint", func(p *Project, _ *readerEvidence) { p.Reader.Fingerprint = "sha256:" + strings.Repeat("0", 64) }, false},
		{"wrong evidence path", func(p *Project, _ *readerEvidence) {
			p.Reader.Page = "/reader/" + strings.Repeat("0", 64) + ".html?h=0123456789abcdef"
		}, false},
		{"wrong page kind", func(p *Project, _ *readerEvidence) {
			p.Reader.Page = strings.Replace(p.Reader.Page, ".html", ".svg", 1)
		}, false},
		{"wrong status", func(p *Project, _ *readerEvidence) { p.Reader.Status = "legacy_receipt" }, false},
		{"wrong message", func(p *Project, _ *readerEvidence) { p.Reader.Message = "unverified cached claim" }, false},
		{"wrong image", func(p *Project, _ *readerEvidence) { p.Reader.SVG = "" }, false},
		{"wrong commit", func(p *Project, _ *readerEvidence) { p.Reader.Commit = strings.Repeat("0", 40) }, false},
		{"wrong format", func(p *Project, _ *readerEvidence) { p.Reader.Format = "obsolete" }, false},
		{"changed visited count", func(_ *Project, e *readerEvidence) {
			e.Report.Conformance.States++
			e.Report.Fingerprint = verify.Fingerprint(*e.Report)
		}, false},
		{"changed model count", func(_ *Project, e *readerEvidence) {
			e.Report.Conformance.ModelStates++
			e.Report.Fingerprint = verify.Fingerprint(*e.Report)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var saved readerEvidence
			if err := decodeReaderJSON(reader631JSON(t, e), &saved); err != nil {
				t.Fatal(err)
			}
			s := &Server{Cache: t.TempDir(), Repos: []*Repo{{Name: e.Repo}}}
			p := projectOf(projectSource{dir: e.Dir, manifest: e.Manifest, lock: e.Lock}, e.Report)
			presentation := old
			p.Repo, p.Reader, p.Poster = e.Repo, &presentation, current.posters
			p.Assurance = "tested in every state"
			p.Coverage = []verify.Coverage{{Source: "conformance", Checked: true, Status: "full", Fraction: "5/5", Percent: "100%"}}
			tc.edit(&p, &saved)
			// Even an entry copied under another key must fail identity checks.
			parts := readerPath.FindStringSubmatch(p.Reader.Page)
			if len(parts) != 3 {
				t.Fatal("fixture needs a well-formed saved URL")
			}
			reader631Write(t, s.readerFile(parts[1]), reader631JSON(t, saved))
			snap := Snapshot{Repo: e.Repo, Projects: []Project{p}, Receipts: &RunRef{ID: 802, SHA: e.Commit}}
			gz, err := gzipped(reader631JSON(t, snap))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.writeSnapshot(gz); err != nil {
				t.Fatal(err)
			}
			cold := &Server{Cache: s.Cache, Repos: []*Repo{{Name: e.Repo}}}
			cold.loadSnapshot()
			if !tc.accept {
				if len(cold.state) != 0 {
					t.Fatal("restart served a snapshot with mismatched evidence")
				}
				return
			}
			if len(cold.state) == 0 {
				t.Fatal("renderer upgrade discarded unchanged cached evidence")
			}
			plain, err := gunzip(cold.state)
			if err != nil {
				t.Fatal(err)
			}
			var restored Snapshot
			if err := json.Unmarshal(plain, &restored); err != nil {
				t.Fatal(err)
			}
			if len(restored.Projects) != 1 {
				t.Fatal("restart lost the cached project")
			}
			got := restored.Projects[0]
			if got.Reader == nil || *got.Reader != current.presentation || !reflect.DeepEqual(got.Poster, current.posters) || got.Fingerprint != e.Report.Fingerprint {
				t.Fatalf("upgrade changed evidence or failed to refresh the page URL: %+v", got)
			}
			if !got.Passed || got.Assurance != "tested (sampled)" || len(got.Coverage) != 1 || got.Coverage[0].Fraction != "2/5" || got.Coverage[0].Percent != "40%" {
				t.Fatalf("upgrade did not derive coverage from saved evidence: %+v", got)
			}
			page := reader631GET(cold, "GET", got.Reader.Page, "")
			if page.Code != 200 || !bytes.Equal(page.Body.Bytes(), current.html) || !strings.Contains(page.Body.String(), "2/5") {
				t.Fatal("restored page did not serve current coverage markup")
			}
			for url, want := range map[string][]byte{got.Reader.SVG: current.svg, got.Poster.Animated: current.poster, got.Poster.Still: current.still} {
				image := reader631GET(cold, "GET", url, "")
				if image.Code != 200 || !bytes.Equal(image.Body.Bytes(), want) {
					t.Fatalf("restored image unavailable at %s", url)
				}
			}
		})
	}
}
