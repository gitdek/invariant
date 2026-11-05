package dashboard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readerRecoveryProject(t *testing.T, snap Snapshot, repo, dir string) Project {
	t.Helper()
	for _, p := range snap.Projects {
		if p.Repo == repo && p.Dir == dir {
			if p.Reader == nil {
				t.Fatalf("%s:%s lost its reader diagnostic", repo, dir)
			}
			return p
		}
	}
	t.Fatalf("missing project %s:%s in %+v", repo, dir, snap.Projects)
	return Project{}
}

func readerRecoveryNoResults(t *testing.T, p Project) {
	t.Helper()
	if !p.ReceiptMissing || p.Passed || p.Fingerprint != "" || p.Assurance != "" || p.Graph ||
		p.States != 0 || p.Depth != 0 || p.Visited != 0 || p.Verified != 0 || p.Verifier != "" ||
		len(p.Statements) != 0 || len(p.Witnesses) != 0 || len(p.Bugs) != 0 || len(p.Unverified) != 0 ||
		p.Ratified != nil || p.Decision != "" || p.Factory {
		t.Fatalf("project without a receipt claims verification evidence: %+v", p)
	}
}

func TestReaderPartialArchiveKeepsMissingReceiptCard(t *testing.T) {
	healthy := reader631FixtureFor(t, 3, "Checked sibling")
	missing := reader631FixtureFor(t, 4, "Absent receipt")
	missing.Dir = "examples/omitted"
	fixtures := []reader631Fixture{healthy, missing}
	archive := reader631Archive(t, fixtures, func(entries map[string][]byte) {
		delete(entries, "omitted/receipt.json")
	})
	r := reader631Repo(t, "owner/partial", strings.Repeat("a", 40), 631, fixtures, archive)
	s := &Server{Repos: []*Repo{r}, Cache: t.TempDir()}
	ps := reader631Load(t, s, r)
	first := reader631Find(t, ps, r.Name, healthy.Dir)
	diagnostic := reader631Find(t, ps, r.Name, missing.Dir)
	reader631Chart(t, s, first, healthy.Report.Reader.Chart)
	reader631Diagnostic(t, s, diagnostic, "missing_artifacts")
	if diagnostic.Commit != r.src.receipts.run.HeadSHA || !strings.Contains(diagnostic.Message, "no report") {
		t.Fatalf("missing receipt diagnostic lost its cause or commit: %+v", diagnostic)
	}

	// The independently saved companions cannot stand in for receipt.json,
	// including after restart. Only the healthy report contributes evidence.
	cold := &Server{Repos: []*Repo{{Name: r.Name}}, Cache: s.Cache}
	cold.loadSnapshot()
	for _, server := range []*Server{s, cold} {
		snap := server.assemble(time.Now())
		if len(snap.Projects) != 2 || snap.Repos[0].Projects != 2 || snap.Totals.Projects != 2 || snap.Repos[0].Totals.Projects != 2 {
			t.Fatalf("partial archive dropped a modeled project: %+v", snap)
		}
		if snap.Totals.Models != 1 || snap.Totals.States != 3 || snap.Totals.Statements != len(healthy.Report.Pins) {
			t.Fatalf("missing receipt inflated evidence totals: %+v", snap.Totals)
		}
		p := readerRecoveryProject(t, snap, r.Name, missing.Dir)
		readerRecoveryNoResults(t, p)
		if p.Name != missing.Manifest.Name || p.Language != missing.Manifest.Language || p.Reader.Page != diagnostic.Page {
			t.Fatalf("missing receipt card lost its identity: %+v", p)
		}
		reader631Diagnostic(t, server, reader631Presentation(*p.Reader), "missing_artifacts")
		p = readerRecoveryProject(t, snap, r.Name, healthy.Dir)
		if p.ReceiptMissing || !p.Passed || p.Fingerprint != healthy.Report.Fingerprint || p.Reader.Page != first.Page {
			t.Fatalf("partial archive damaged the healthy sibling: %+v", p)
		}
		reader631Chart(t, server, reader631Presentation(*p.Reader), healthy.Report.Reader.Chart)
	}
}

func TestReaderCorruptSiblingSurvivesRestartAndFailedRefresh(t *testing.T) {
	for _, nextRun := range []int64{631, 632} {
		t.Run(fmt.Sprint(nextRun), func(t *testing.T) {
			healthy := reader631FixtureFor(t, 3, "Healthy sibling")
			damaged := reader631FixtureFor(t, 4, "Damaged sibling")
			damaged.Dir = "examples/damaged"
			fixtures := []reader631Fixture{healthy, damaged}
			archive := reader631Archive(t, fixtures, nil)
			sha := strings.Repeat("a", 40)
			r := reader631Repo(t, "owner/recovery", sha, 631, fixtures, archive)
			s := &Server{Repos: []*Repo{r}, Cache: t.TempDir()}
			ps := reader631Load(t, s, r)
			good := reader631Find(t, ps, r.Name, healthy.Dir)
			bad := reader631Find(t, ps, r.Name, damaged.Dir)
			if err := os.WriteFile(s.readerFile(strings.TrimPrefix(bad.Fingerprint, "sha256:")), []byte("corrupt entry"), 0600); err != nil {
				t.Fatal(err)
			}
			if nextRun != 631 {
				sha = strings.Repeat("b", 40)
			}
			unavailable := reader631Repo(t, r.Name, sha, nextRun, fixtures, archive)
			endpoint := "repos/" + r.Name + "/actions/artifacts/631/zip"
			name := strings.TrimPrefix(reader631Hash([]byte(endpoint)), "sha256:")
			if err := os.Remove(filepath.Join(filepath.Dir(unavailable.GitHub.GH), name)); err != nil {
				t.Fatal(err)
			}
			cold := &Server{Repos: []*Repo{unavailable}, Cache: s.Cache}
			cold.loadSnapshot()
			for restart := 0; restart < 2; restart++ {
				if err := cold.readReceipts(t.Context(), unavailable); err == nil {
					t.Fatal("damaged cache prevented retrying the unavailable archive")
				}
				snap := cold.assemble(time.Now())
				if len(snap.Projects) != 2 || snap.Totals.Projects != 2 || snap.Totals.States != 3 {
					t.Fatalf("corrupt entry lost healthy evidence after failed refresh: %+v", snap)
				}
				if snap.Repos[0].Receipts == nil || snap.Repos[0].Receipts.ID != 631 {
					t.Fatal("failed refresh changed the retained receipt run")
				}
				p := readerRecoveryProject(t, snap, r.Name, healthy.Dir)
				if p.ReceiptMissing || !p.Passed || p.Fingerprint != healthy.Report.Fingerprint || reader631Presentation(*p.Reader) != good {
					t.Fatalf("corrupt sibling changed validated evidence: %+v", p)
				}
				reader631Chart(t, cold, good, healthy.Report.Reader.Chart)
				p = readerRecoveryProject(t, snap, r.Name, damaged.Dir)
				readerRecoveryNoResults(t, p)
				reader631Diagnostic(t, cold, reader631Presentation(*p.Reader), "cached_evidence")
				if w := reader631GET(cold, "GET", bad.SVG, ""); w.Code != 404 {
					t.Fatalf("corrupt sibling's SVG is still served: %d", w.Code)
				}
				gz, err := cold.publish(snap)
				if err != nil {
					t.Fatal(err)
				}
				cold.saveSnapshot(gz)
				unavailable.src.receipts, unavailable.src.projects = nil, nil
				cold = &Server{Repos: []*Repo{unavailable}, Cache: s.Cache}
				cold.loadSnapshot()
			}
			// Once the archive is readable again, either run can replace the
			// diagnostic with independently validated evidence.
			recovered := reader631Repo(t, r.Name, sha, nextRun, fixtures, archive)
			unavailable.GitHub = recovered.GitHub
			ps = reader631Load(t, cold, unavailable)
			for _, f := range fixtures {
				p := reader631Find(t, ps, r.Name, f.Dir)
				reader631Chart(t, cold, p, f.Report.Reader.Chart)
				if p.Commit != sha {
					t.Fatalf("recovered evidence has the wrong commit: %+v", p)
				}
			}
			if unavailable.src.receipts.retry {
				t.Fatal("successful archive validation left the cache needing repair")
			}
		})
	}
}
