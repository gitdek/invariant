package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/github"
)

func TestSavedDraftRecordSaveFailureRetries(t *testing.T) {
	result := saved897Proposal(t, false, 2, nil)
	f := saved897FixtureFor(t, result)
	f.s.prepare()
	file := filepath.Join(f.s.Cache, "saved-drafts", strings.TrimPrefix(readerHash([]byte(saved897Locator(f.ref))), "sha256:")+".json")
	if err := os.MkdirAll(file+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	// The snapshot can be saved, but the source cannot. No usable locator or
	// ratification may escape that failed source save.
	f.refresh(t)
	saved897Unavailable(t, f.s, f.ref.Issue)
	saved897NonSuccess(t, f.s, saved897Locator(f.ref))
	if err := os.RemoveAll(file + ".tmp"); err != nil {
		t.Fatal(err)
	}
	f.refresh(t)
	w := saved897Healthy(t, f.s, f.ref, result)
	saved897Evidence(t, f.s, w.SavedDraft, f.payload)
	before, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	cold := f.restart(t, saved897Author)
	saved897Healthy(t, cold, f.ref, result)
	after, err := os.Stat(file)
	if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("healthy restart rewrote retained source authority")
	}
}

func TestSavedDraftAnsweredAlternativesSurviveOfflineEviction(t *testing.T) {
	result := saved897Questions(t, nil)
	f := saved897FixtureFor(t, result)
	f.s.prepare()
	f.refresh(t)
	w := saved897Healthy(t, f.s, f.ref, result)
	var pages []string
	for _, q := range w.Forks {
		for _, o := range q.Options {
			pages = append(pages, o.Reader.Page, o.Reader.SVG, o.Posters.Animated, o.Posters.Still)
		}
	}
	comments := []github.Comment{
		{ID: 897, User: github.User{Login: saved897Author}, CreatedAt: "2026-11-12T12:00:01Z", Body: marker(t, f.marker)},
		{ID: 899, User: github.User{Login: "owner"}, AuthorAssociation: "OWNER", CreatedAt: "2026-11-12T12:00:40Z", Body: "/invariant choose F1 A\n/invariant choose F2 B"},
	}
	saved897Endpoint(t, f.r, "issues/897/comments?per_page=100", reader631JSON(t, comments))
	f.issues[0].UpdatedAt = "2026-11-12T12:00:45Z"
	f.writeIssues(t)
	f.refresh(t)
	_, after := saved897State(t, f.s, f.ref.Issue)
	if after != nil {
		t.Fatal("fully answered reference still has a card")
	}
	if err := os.RemoveAll(filepath.Join(f.s.Cache, "previews")); err != nil {
		t.Fatal(err)
	}
	file := saved897Endpoint(t, f.r, "contents/result.json?ref="+f.ref.Commit, f.payload)
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	cold := f.restart(t, saved897Author)
	saved897Evidence(t, cold, w.SavedDraft, f.payload)
	for _, page := range pages {
		ct := "image/svg+xml"
		if strings.HasSuffix(page, ".html") {
			ct = "text/html"
		}
		saved897Response(t, cold, page, ct)
	}
	// The unchanged factory still owns the evidence after all choices, but
	// an unknown factory account must not inherit its saved locator.
	wrong := f.restart(t, "")
	saved897NonSuccess(t, wrong, w.SavedDraft.Locator)
}
