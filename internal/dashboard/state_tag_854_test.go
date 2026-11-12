package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A page reads the snapshot every 15 seconds, and it changes once per
// refresh. The server tags what it serves, and a read that names the tag
// it has gets 304 and no body (#854), in the tag's weak form too, as a
// proxy that compresses the body again sends it back. A read that names
// another tag, or none, gets the snapshot.
func TestAnUnchangedSnapshotIsNotSentAgain854(t *testing.T) {
	s, _, _ := receipt723Load(t, receipt723Fixture(t), nil)
	read := func(named string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/state.json", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		if named != "" {
			req.Header.Set("If-None-Match", named)
		}
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec
	}
	first := read("")
	tag := first.Header().Get("ETag")
	if first.Code != http.StatusOK || first.Body.Len() == 0 || !strings.HasPrefix(tag, `"`) || first.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("a first read: %d, %d bytes, tag %q, cache %q", first.Code, first.Body.Len(), tag, first.Header().Get("Cache-Control"))
	}
	for _, named := range []string{tag, "W/" + tag, `"other", W/` + tag, `"other" , ` + tag} {
		if again := read(named); again.Code != http.StatusNotModified || again.Body.Len() != 0 || again.Header().Get("ETag") != tag {
			t.Errorf("a read naming %s got %d and %d bytes, with tag %q", named, again.Code, again.Body.Len(), again.Header().Get("ETag"))
		}
	}
	for _, named := range []string{`"other"`, `W/"other"`, strings.Trim(tag, `"`)} {
		if again := read(named); again.Code != http.StatusOK || again.Body.Len() != first.Body.Len() {
			t.Errorf("a read naming %s got %d and %d bytes, not the snapshot", named, again.Code, again.Body.Len())
		}
	}
	// What's published next has another tag, and the old one gets it whole.
	snap := s.assemble(time.Now().Add(time.Hour))
	if _, err := s.publish(snap); err != nil {
		t.Fatal(err)
	}
	next := read(tag)
	if next.Code != http.StatusOK || next.Body.Len() == 0 || next.Header().Get("ETag") == tag || next.Header().Get("ETag") == "" {
		t.Errorf("after a new snapshot, the old tag got %d with tag %q", next.Code, next.Header().Get("ETag"))
	}
	// The same bytes keep their tag across a restart.
	cold := &Server{Repos: []*Repo{{Name: receipt723RepoName}}, Cache: s.Cache}
	cold.loadSnapshot()
	req := httptest.NewRequest("GET", "/api/state.json", nil)
	req.Header.Set("If-None-Match", next.Header().Get("ETag"))
	rec := httptest.NewRecorder()
	cold.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Errorf("a restarted server answered the tag of what it restored with %d", rec.Code)
	}
}
