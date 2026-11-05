package dashboard

import (
	"errors"
	"path"
	"strings"
	"testing"
	"time"
)

// An archive whose only project lost its receipt.json is missing evidence, as
// an absent archive is: a fresh dashboard still shows the project's card and
// says why it has no chart, rather than nothing at all.
func TestReaderArchiveWithoutReceiptShowsItsProject(t *testing.T) {
	f := reader631FixtureFor(t, 3, "Only project")
	archive := reader631Archive(t, []reader631Fixture{f}, func(m map[string][]byte) {
		delete(m, path.Base(f.Dir)+"/receipt.json")
	})
	sha := strings.Repeat("b", 40)
	r := reader631Repo(t, "owner/no-receipt", sha, 631, []reader631Fixture{f}, archive)
	s := &Server{Repos: []*Repo{r}, Cache: t.TempDir()}
	if err := s.readReceipts(t.Context(), r); !errors.Is(err, errNoReceiptJSON) {
		t.Fatalf("an archive without receipt.json must read as missing evidence: %v", err)
	}
	snap := s.assemble(time.Now())
	if len(snap.Projects) != 1 || snap.Totals.Projects != 1 {
		t.Fatalf("the project's card is missing: %+v", snap.Projects)
	}
	p := readerRecoveryProject(t, snap, r.Name, f.Dir)
	readerRecoveryNoResults(t, p)
	reader631Diagnostic(t, s, reader631Presentation(*p.Reader), "missing_artifacts")
	if p.Reader.Commit != sha || !strings.Contains(p.Reader.Message, "holds no receipt.json") {
		t.Fatalf("the diagnostic doesn't say why: %+v", p.Reader)
	}
	if !r.src.receipts.retry {
		t.Fatal("missing evidence must be read again on the next refresh")
	}
}

// A restart that rejects cached reader evidence must not publish the saved
// snapshot, whose totals and tiles still count the rejected evidence.
func TestReaderRestartWithRejectedEvidencePublishesNoStaleTotals(t *testing.T) {
	f := reader631FixtureFor(t, 3, "Ready")
	r := reader631Repo(t, "owner/restart", strings.Repeat("c", 40), 631, []reader631Fixture{f}, reader631Archive(t, []reader631Fixture{f}, nil))
	s := &Server{Repos: []*Repo{r}, Cache: t.TempDir()}
	p := reader631Find(t, reader631Load(t, s, r), r.Name, f.Dir)
	if p.Status != "chart" {
		t.Fatalf("fixture has no chart: %+v", p)
	}
	before := s.assemble(time.Now()).Totals
	if before.Models == 0 || before.States == 0 {
		t.Fatalf("fixture counts no verification results: %+v", before)
	}

	valid := &Server{Cache: s.Cache, Repos: []*Repo{{Name: r.Name}}}
	valid.loadSnapshot()
	if len(valid.state) == 0 {
		t.Fatal("a restart with valid cached evidence must serve the last snapshot")
	}

	parts := readerPath.FindStringSubmatch(p.Page)
	if len(parts) != 3 {
		t.Fatalf("unexpected reader page %q", p.Page)
	}
	reader631Write(t, s.readerFile(parts[1]), []byte("corrupt reader cache entry"))
	damaged := &Server{Cache: s.Cache, Repos: []*Repo{{Name: r.Name}}}
	damaged.loadSnapshot()
	if len(damaged.state) != 0 {
		plain, err := gunzip(damaged.state)
		if err != nil {
			t.Fatal(err)
		}
		t.Fatalf("a restart that rejected cached evidence served the saved totals: %s", plain)
	}
}
