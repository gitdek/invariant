package dashboard

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestReaderRetainsReceiptAfterRestartAndFailedRefresh(t *testing.T) {
	f := reader631FixtureFor(t, 3, "Last checked view")
	r := reader631Repo(t, "owner/retained", strings.Repeat("a", 40), 631, []reader631Fixture{f}, reader631Archive(t, []reader631Fixture{f}, nil))
	s := &Server{Repos: []*Repo{r}, Cache: t.TempDir()}
	first := reader631Find(t, reader631Load(t, s, r), r.Name, f.Dir)
	broken := reader631Repo(t, r.Name, strings.Repeat("b", 40), 632, []reader631Fixture{f}, []byte("incomplete archive"))
	cold := &Server{Repos: []*Repo{broken}, Cache: s.Cache}
	cold.loadSnapshot()
	if err := cold.readReceipts(t.Context(), broken); err == nil {
		t.Fatal("incomplete archive accepted")
	}
	snap := cold.assemble(time.Now())
	if len(snap.Projects) != 1 || snap.Projects[0].Reader == nil || *snap.Projects[0].Reader != (ReaderPresentation{
		Status: first.Status, Message: first.Message, Commit: first.Commit, Fingerprint: first.Fingerprint, Format: first.Format, Page: first.Page, SVG: first.SVG,
	}) {
		t.Fatalf("failed first refresh lost last receipt: %+v", snap.Projects)
	}
	reader631Chart(t, cold, first, f.Report.Reader.Chart)
}

func TestReaderPageHTTPAndCompanionValidation(t *testing.T) {
	f := reader631FixtureFor(t, 3, "Literal <heading>")
	r := reader631Repo(t, "owner/http", strings.Repeat("a", 40), 631, []reader631Fixture{f}, reader631Archive(t, []reader631Fixture{f}, nil))
	s := &Server{Repos: []*Repo{r}, Cache: t.TempDir()}
	p := reader631Find(t, reader631Load(t, s, r), r.Name, f.Dir)
	page := reader631GET(s, "GET", p.Page, "")
	if !strings.Contains(page.Body.String(), "script-free") && !strings.Contains(page.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatal("reader page has no restrictive resource policy")
	}
	head := reader631GET(s, "HEAD", p.Page, "")
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("ETag") != page.Header().Get("ETag") {
		t.Fatalf("page HEAD: %d %v", head.Code, head.Header())
	}
	conditional := reader631GET(s, "GET", p.Page, `"other", W/`+page.Header().Get("ETag"))
	if conditional.Code != 304 || conditional.Body.Len() != 0 {
		t.Fatal("page conditional read did not match")
	}
	if page.Header().Get("ETag") == reader631GET(s, "GET", p.SVG, "").Header().Get("ETag") {
		t.Fatal("different representations share an ETag")
	}
	for _, suffix := range []string{" {}", " trailing data"} {
		archive := reader631Archive(t, []reader631Fixture{f}, func(entries map[string][]byte) {
			entries["reader/reader.json"] = append(entries["reader/reader.json"], []byte(suffix)...)
		})
		r := reader631Repo(t, "owner/damaged", strings.Repeat("c", 40), 631, []reader631Fixture{f}, archive)
		s := &Server{Repos: []*Repo{r}}
		p := reader631Find(t, reader631Load(t, s, r), r.Name, f.Dir)
		reader631Diagnostic(t, s, p, "artifact_mismatch")
	}
}

func TestReaderDoesNotAssignBasenameArchiveToTwoProjects(t *testing.T) {
	a := reader631FixtureFor(t, 3, "First")
	b := reader631FixtureFor(t, 3, "Second")
	b.Dir = "other/reader"
	r := reader631Repo(t, "owner/ambiguous", strings.Repeat("a", 40), 631, []reader631Fixture{a, b}, reader631Archive(t, []reader631Fixture{a}, nil))
	s := &Server{Repos: []*Repo{r}}
	ps := reader631Load(t, s, r)
	for _, dir := range []string{a.Dir, b.Dir} {
		reader631Diagnostic(t, s, reader631Find(t, ps, r.Name, dir), "ambiguous_artifacts")
	}
}

func TestReaderCardsUseLinkedImagesAndSeparateRawDetail(t *testing.T) {
	js := assets["/app.js"]
	for _, want := range []string{`<img class="reader-image"`, `src="${esc(r.svg)}"`, `href="${esc(r.page)}"`, "Read model evidence", "Raw state graph detail"} {
		if !bytes.Contains(js, []byte(want)) {
			t.Errorf("reader card omits %s", want)
		}
	}
	if bytes.Contains(js, []byte(`<canvas class="thumb"`)) {
		t.Fatal("reader card still uses a raw graph thumbnail")
	}
}
