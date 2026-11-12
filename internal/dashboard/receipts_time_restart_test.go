package dashboard

import (
	"os"
	"strings"
	"testing"
	"time"
)

// After a restart the dashboard serves its last snapshot, and restores the
// receipts it holds from it. The restored set named its run without the
// run's times, so the page's Receipts line read "739931 days ago": an age
// counted from year one. The times and the run's title come back with the
// set, a snapshot already saved without them takes them from the runs read
// since, and the page prints no age for a time it wasn't given.
func TestARestartKeepsWhenTheReceiptsWereMade(t *testing.T) {
	sha := strings.Repeat("6", 40)
	f := reader631FixtureFor(t, 3, "A repository")
	r := reader631Repo(t, "owner/one", sha, 631, []reader631Fixture{f}, reader631Archive(t, []reader631Fixture{f}, nil))
	r.src.runs[0].StartedAt, r.src.runs[0].UpdatedAt, r.src.runs[0].DisplayTitle = "2026-11-05T11:00:00Z", "2026-11-05T11:20:00Z", "Merge a change"
	made := time.Date(2026, 11, 5, 11, 20, 0, 0, time.UTC)
	s := &Server{Repos: []*Repo{r}, Cache: t.TempDir()}
	reader631Load(t, s, r)
	if got := querySnapshot(t, s).Receipts; got == nil || !got.Updated.Equal(made) {
		t.Fatalf("before any restart, the receipts' run is %+v", got)
	}
	// A restart, with no read of GitHub yet.
	cold := &Server{Cache: s.Cache, Repos: []*Repo{{Name: r.Name}}}
	cold.loadSnapshot()
	again := cold.assemble(time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC))
	for name, got := range map[string]*RunRef{"the page's": again.Receipts, "the repository's": again.Repos[0].Receipts} {
		if got == nil || !got.Updated.Equal(made) || got.Started.IsZero() || got.Title != "Merge a change" || got.ID != 631 || got.SHA != sha {
			t.Errorf("after a restart, %s receipts' run is %+v; want run 631, made at %s", name, got, made)
		}
	}
	// A set restored from a snapshot that was saved without the times takes
	// them from the run once GitHub has been read.
	stale := &Server{Cache: s.Cache, Repos: []*Repo{{Name: r.Name}}}
	stale.loadSnapshot()
	stale.Repos[0].src.receipts.run.StartedAt, stale.Repos[0].src.receipts.run.UpdatedAt = "", ""
	if got := stale.assemble(time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)).Receipts; got == nil || !got.Updated.IsZero() {
		t.Fatalf("a set with no times and no run read since: %+v", got)
	}
	stale.Repos[0].src.runs = r.src.runs
	if got := stale.assemble(time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)).Receipts; got == nil || !got.Updated.Equal(made) {
		t.Errorf("a set with no times, once its run is read: %+v", got)
	}
	script, err := os.ReadFile("web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "T(s.receipts?.updated) > 0 ?") {
		t.Error("the page prints an age for the receipts whether or not it was given their time")
	}
}
