package dashboard

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func readerMissingArchive(t *testing.T, r *Repo, run int64, expired bool) {
	t.Helper()
	artifacts := `{"artifacts":[]}`
	if expired {
		artifacts = `{"artifacts":[{"id":631,"name":"receipts","expired":true}]}`
	}
	endpoint := fmt.Sprintf("repos/%s/actions/runs/%d/artifacts?per_page=100", r.Name, run)
	name := strings.TrimPrefix(reader631Hash([]byte(endpoint)), "sha256:")
	reader631Write(t, filepath.Join(filepath.Dir(r.GitHub.GH), name), []byte(artifacts))
}

func TestReaderMissingArchiveShowsProjectsAndRetriesAfterRestart(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired=%t", expired), func(t *testing.T) {
			first := reader631FixtureFor(t, 3, "First project")
			second := reader631FixtureFor(t, 4, "Second project")
			second.Dir = "examples/second"
			fixtures := []reader631Fixture{first, second}
			archive := reader631Archive(t, fixtures, nil)
			sha := strings.Repeat("a", 40)
			r := reader631Repo(t, "owner/missing", sha, 631, fixtures, archive)
			readerMissingArchive(t, r, 631, expired)
			s := &Server{Repos: []*Repo{r}, Cache: t.TempDir()}
			pages := map[string]string{}
			for restart := 0; restart < 2; restart++ {
				if err := s.readReceipts(t.Context(), r); err == nil || !strings.Contains(err.Error(), "missing or expired") {
					t.Fatalf("missing archive did not report a failed read: %v", err)
				}
				snap := s.assemble(time.Now())
				if len(snap.Projects) != 2 || snap.Repos[0].Projects != 2 || snap.Totals.Projects != 2 || snap.Repos[0].Totals.Projects != 2 {
					t.Fatalf("missing archive dropped discovered projects: %+v", snap)
				}
				if snap.Totals.Models != 0 || snap.Totals.States != 0 || snap.Totals.Statements != 0 || snap.Totals.Proved != 0 {
					t.Fatalf("missing archive claimed verification results: %+v", snap.Totals)
				}
				for _, f := range fixtures {
					p := readerRecoveryProject(t, snap, r.Name, f.Dir)
					readerRecoveryNoResults(t, p)
					reader631Diagnostic(t, s, reader631Presentation(*p.Reader), "missing_artifacts")
					if p.Reader.Commit != sha || !strings.Contains(p.Reader.Message, "receipt archive is missing or expired") ||
						p.Name != f.Manifest.Name || p.Language != f.Manifest.Language || !reflect.DeepEqual(p.Bounds, f.Lock.Bounds) {
						t.Fatalf("missing archive diagnostic lost its cause or project: %+v", p)
					}
					if old := pages[f.Dir]; old != "" && old != p.Reader.Page {
						t.Fatal("restart replaced unchanged missing archive evidence")
					}
					pages[f.Dir] = p.Reader.Page
				}
				gz, err := s.publish(snap)
				if err != nil {
					t.Fatal(err)
				}
				s.saveSnapshot(gz)
				r.src.receipts, r.src.projects = nil, nil
				s = &Server{Repos: []*Repo{r}, Cache: s.Cache}
				s.loadSnapshot()
			}
			// Publishing the archive for the same run replaces diagnostics,
			// even when they were restored from a saved snapshot.
			recovered := reader631Repo(t, r.Name, sha, 631, fixtures, archive)
			r.GitHub = recovered.GitHub
			ps := reader631Load(t, s, r)
			for _, f := range fixtures {
				reader631Chart(t, s, reader631Find(t, ps, r.Name, f.Dir), f.Report.Reader.Chart)
			}
			if r.src.receipts.retry {
				t.Fatal("validated archive still needs retrying")
			}
		})
	}
}

func TestReaderMissingArchiveRetainsLastEvidence(t *testing.T) {
	for _, expired := range []bool{false, true} {
		for _, restart := range []bool{false, true} {
			t.Run(fmt.Sprintf("expired=%t/restart=%t", expired, restart), func(t *testing.T) {
				f := reader631FixtureFor(t, 3, "Last checked view")
				fixtures := []reader631Fixture{f}
				archive := reader631Archive(t, fixtures, nil)
				r := reader631Repo(t, "owner/retained", strings.Repeat("a", 40), 631, fixtures, archive)
				s := &Server{Repos: []*Repo{r}, Cache: t.TempDir()}
				first := reader631Find(t, reader631Load(t, s, r), r.Name, f.Dir)
				before := s.assemble(time.Now())
				next := reader631Repo(t, r.Name, strings.Repeat("b", 40), 632, fixtures, archive)
				readerMissingArchive(t, next, 632, expired)
				if restart {
					s = &Server{Repos: []*Repo{next}, Cache: s.Cache}
					r = next
					s.loadSnapshot()
				} else {
					r.GitHub, r.src.runs, r.src.gates = next.GitHub, next.src.runs, next.src.gates
				}
				if err := s.readReceipts(t.Context(), r); err == nil || !strings.Contains(err.Error(), "missing or expired") {
					t.Fatalf("missing archive did not report a failed read: %v", err)
				}
				after := s.assemble(time.Now())
				if !reflect.DeepEqual(before.Projects, after.Projects) || !reflect.DeepEqual(before.Totals, after.Totals) ||
					after.Receipts == nil || after.Receipts.ID != 631 || after.Repos[0].Receipts.ID != 631 {
					t.Fatalf("missing archive changed retained evidence: %+v", after)
				}
				reader631Chart(t, s, first, f.Report.Reader.Chart)
			})
		}
	}
}
