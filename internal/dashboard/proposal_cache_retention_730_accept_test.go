package dashboard

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func cache730Issue(t *testing.T, snap *Snapshot, repo string, issue int) *Issue {
	t.Helper()
	for i := range snap.Issues {
		is := &snap.Issues[i]
		if is.Repo == repo && is.Number == issue {
			return is
		}
	}
	t.Fatalf("missing %s#%d", repo, issue)
	return nil
}

func cache730Gone(t *testing.T, s *Server, p ReaderPresentation) {
	t.Helper()
	for _, url := range []string{p.Page, p.SVG} {
		if url != "" {
			if r := reader631GET(s, "GET", url, ""); r.Code != 404 {
				t.Fatalf("superseded URL still served: %s (%d)", url, r.Code)
			}
		}
	}
	s.mu.RLock()
	_, exists := s.readers[strings.TrimPrefix(p.Fingerprint, "sha256:")]
	s.mu.RUnlock()
	if exists {
		t.Fatal("superseded page remains resident")
	}
	if _, err := os.Stat(cache730FileName(s, p)); !os.IsNotExist(err) {
		t.Fatalf("superseded evidence was not removed: %v", err)
	}
}

func cache730DiskMatches(t *testing.T, s *Server, snap Snapshot) {
	t.Helper()
	want := map[string]bool{}
	for _, p := range cache730Refs(snap) {
		if p.Format == comparisonFormat || p.Format == previewFormat {
			want[cache730FileName(s, p)] = true
		}
	}
	got := map[string]bool{}
	for _, dir := range []string{"comparisons", "previews"} {
		entries, err := os.ReadDir(filepath.Join(s.Cache, dir))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				t.Fatalf("unexpected incomplete evidence entry: %s/%s", dir, e.Name())
			}
			got[filepath.Join(s.Cache, dir, e.Name())] = true
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted proposal/preview files are not exactly the live references: got %v, want %v", got, want)
	}
}

func cache730AddReceipts(t *testing.T, s *Server) *Repo {
	t.Helper()
	first := reader631FixtureFor(t, 3, "Healthy receipt sibling")
	second := reader631FixtureFor(t, 4, "Second receipt")
	second.Dir = "examples/second"
	fixtures := []reader631Fixture{first, second}
	r := reader631Repo(t, "owner/receipts", strings.Repeat("a", 40), 631, fixtures, reader631Archive(t, fixtures, nil))
	s.Repos = append(s.Repos, r)
	if err := s.readReceipts(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	return r
}

// Exercise publication together with immutable HTTP access. A request for an
// old URL may finish with the original bytes or 404 after retirement. A page
// referenced by a snapshot that stays current throughout the read must exist.
func cache730Watch(s *Server, old map[string]cache730Body) func() []error {
	stop := make(chan struct{})
	errors := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for url, want := range old {
					r := reader631GET(s, "GET", url, "")
					if r.Code != 404 && (r.Code != 200 || r.Body.String() != want.body || r.Header().Get("ETag") != want.etag) {
						errors <- fmt.Errorf("immutable URL served different bytes during publication: %s", url)
						return
					}
				}
				before := reader631GET(s, "GET", "/api/state.json", "")
				var snap Snapshot
				if before.Code != 200 || decodeReaderJSON(before.Body.Bytes(), &snap) != nil {
					errors <- fmt.Errorf("publication exposed an incomplete snapshot")
					return
				}
				missing := false
				for _, p := range cache730Refs(snap) {
					for _, url := range []string{p.Page, p.SVG} {
						if url != "" && reader631GET(s, "GET", url, "").Code != 200 {
							missing = true
						}
					}
				}
				after := reader631GET(s, "GET", "/api/state.json", "")
				if missing && bytes.Equal(before.Body.Bytes(), after.Body.Bytes()) {
					errors <- fmt.Errorf("current snapshot points to an evicted page")
					return
				}
			}
		}()
	}
	return func() []error {
		close(stop)
		wg.Wait()
		close(errors)
		var out []error
		for err := range errors {
			out = append(out, err)
		}
		return out
	}
}

func TestProposalCache730PublicationReclaimsOnlySupersededPages(t *testing.T) {
	s, original, options := cache730Fixture(t)
	// Same issue and option IDs in another repository must remain independent.
	other := *s.Repos[0]
	other.Name = "owner/second"
	s.Repos = append(s.Repos, &other)
	cache730AddReceipts(t, s)
	first := cache730Snapshot(t, s)
	firstRefs := cache730Refs(first)
	if len(firstRefs) != 8 { // two comparisons, four previews, two receipts
		t.Fatalf("incomplete multiple-repository fixture: %d", len(firstRefs))
	}
	before := cache730Bodies(t, s, firstRefs)
	oldComparison := *cache730Issue(t, &first, original.Repo, 730).Waiting.Reader
	oldOption := *cache730Issue(t, &first, original.Repo, 731).Waiting.Forks[0].Options[0].Reader
	stable := map[string]cache730Body{}
	for url, b := range before {
		if url != oldComparison.Page && url != oldComparison.SVG && url != oldOption.Page && url != oldOption.SVG {
			stable[url] = b
		}
	}
	stop := cache730Watch(s, before)
	defer func() {
		for _, err := range stop() {
			t.Error(err)
		}
	}()
	// Building a candidate is not publication. Failed JSON encoding must not
	// retire any old page, persist a replacement snapshot or replace act's read.
	speculative := cache730Clone(t, original)
	speculative.Proposal.Name += " unpublished candidate"
	unpublished := *s.keepComparison(speculative)
	bad := cache730Clone(t, first)
	cache730Issue(t, &bad, original.Repo, 730).Waiting.Reader = &unpublished
	bad.GeneratedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.publish(bad); err == nil {
		t.Fatal("invalid refresh unexpectedly published")
	}
	if !reflect.DeepEqual(cache730Refs(first), cache730Refs(cache730State(t, s))) {
		t.Fatal("failed refresh replaced the served snapshot")
	}
	cache730SameBodies(t, s, before)
	for _, p := range []ReaderPresentation{oldComparison, oldOption} {
		if _, err := os.Stat(cache730FileName(s, p)); err != nil {
			t.Fatal("failed publication pruned durable current evidence", err)
		}
	}
	// Revisions preserve statement hashes. Each replaces just one repository's
	// comparison; removing one waiting option must retain its healthy sibling.
	current := cache730Clone(t, first)
	question := &cache730Issue(t, &current, original.Repo, 731).Waiting.Forks[0]
	question.Options = question.Options[1:]
	for i := 0; i < 12; i++ {
		candidate := cache730Clone(t, original)
		candidate.Proposal.Name = fmt.Sprintf("Revision %d", i)
		is := cache730Issue(t, &current, original.Repo, 730)
		is.Waiting.Proposal, is.Waiting.Reader = candidate.Proposal, s.keepComparison(candidate)
		current.GeneratedAt = first.GeneratedAt.Add(time.Duration(i+1) * time.Second)
		cache730Publish(t, s, current)
		cache730SameBodies(t, s, stable)
		cache730Gone(t, s, oldComparison)
		cache730Gone(t, s, oldOption)
		cache730Gone(t, s, unpublished)
		cache730DiskMatches(t, s, current)
		s.mu.RLock()
		n := len(s.readers)
		s.mu.RUnlock()
		if n != 7 {
			t.Fatalf("memory grew beyond the seven current pages: %d", n)
		}
	}
	// Removing the remaining waiting items retires them, but neither another
	// repository's equal-numbered items nor either receipt is collateral damage.
	last := *cache730Issue(t, &current, original.Repo, 730).Waiting.Reader
	remaining := *cache730Issue(t, &current, original.Repo, 731).Waiting.Forks[0].Options[0].Reader
	cache730Issue(t, &current, original.Repo, 730).Waiting = nil
	cache730Issue(t, &current, original.Repo, 731).Waiting = nil
	current.GeneratedAt = current.GeneratedAt.Add(time.Second)
	cache730Publish(t, s, current)
	cache730Gone(t, s, last)
	cache730Gone(t, s, remaining)
	cache730DiskMatches(t, s, current)
	for _, p := range cache730Refs(current) {
		cache730SameBodies(t, s, map[string]cache730Body{p.Page: before[p.Page], p.SVG: before[p.SVG]})
	}
	// Retiring a page also removes its input lookup. Reintroducing exactly that
	// input must build again and reproduce its original immutable identity.
	work := cache730Work(t, s)
	if got := *s.keepComparison(original); got != oldComparison {
		t.Fatal("reintroduced evidence changed its immutable identity")
	}
	if got := *s.keepPreview(options[0]); got != oldOption {
		t.Fatal("reintroduced option changed its immutable identity")
	}
	cache730WantWork(t, s, [3]uint64{work[0] + 1, work[1] + 1, work[2] + 2})
}

func TestProposalCache730RestartIsolatesDamageAndPrunes(t *testing.T) {
	base, c, options := cache730Fixture(t)
	for _, kind := range []string{"comparison", "preview"} {
		for _, damage := range []string{"missing", "corrupt", "record", "presentation", "format", "absent proposal"} {
			if kind == "preview" && damage == "absent proposal" {
				continue
			}
			t.Run(kind+"/"+damage, func(t *testing.T) {
				s := &Server{Cache: t.TempDir(), Repos: base.Repos}
				snap := cache730Snapshot(t, s)
				refs := cache730Refs(snap)
				before := cache730Bodies(t, s, refs)
				files := cache730Files(t, s, refs)
				var target *ReaderPresentation
				if kind == "comparison" {
					target = cache730Issue(t, &snap, c.Repo, 730).Waiting.Reader
				} else {
					target = cache730Issue(t, &snap, c.Repo, 731).Waiting.Forks[0].Options[0].Reader
				}
				old := *target
				file := cache730FileName(s, old)
				delete(before, old.Page)
				delete(before, old.SVG)
				delete(files, file)
				switch damage {
				case "missing":
					if err := os.Remove(file); err != nil {
						t.Fatal(err)
					}
				case "corrupt":
					reader631Write(t, file, []byte("{"))
				case "record":
					if kind == "comparison" {
						e := cache730Clone(t, c)
						e.Proposal.Name += " different saved record"
						reader631Write(t, file, readerJSON(e))
					} else {
						e := cache730Clone(t, options[0])
						e.Option.Says += " different saved words"
						reader631Write(t, file, readerJSON(e))
					}
				case "presentation":
					target.Message += " substituted expected presentation"
				case "format":
					target.Format = "obsolete validation and rendering format"
				case "absent proposal":
					cache730Issue(t, &snap, c.Repo, 730).Waiting.Proposal = nil
				}
				// Save the deliberately damaged snapshot without publishing it in
				// this server. These extra pages emulate a crash before publication.
				gz, err := gzipped(readerJSON(snap))
				if err != nil {
					t.Fatal(err)
				}
				s.saveSnapshot(gz)
				orphanC := cache730Clone(t, c)
				orphanC.Proposal.Name += " orphan comparison"
				orphanP := cache730Clone(t, options[0])
				orphanP.Problem = "orphan option diagnostic"
				deadC, deadP := *s.keepComparison(orphanC), *s.keepPreview(orphanP)
				cold := &Server{Repos: []*Repo{{Name: c.Repo}}}
				// Even a validated input lookup already in memory is not authority
				// for an unvalidated saved record or mismatched snapshot card.
				if kind == "comparison" {
					cold.keepComparison(cache730Clone(t, c))
				} else {
					cold.keepPreview(cache730Clone(t, options[0]))
				}
				cold.Cache = s.Cache
				cold.loadSnapshot()
				got := cache730State(t, cold)
				var diagnostic *ReaderPresentation
				if kind == "comparison" {
					diagnostic = cache730Issue(t, &got, c.Repo, 730).Waiting.Reader
				} else {
					diagnostic = cache730Issue(t, &got, c.Repo, 731).Waiting.Forks[0].Options[0].Reader
				}
				if damage == "absent proposal" {
					if diagnostic != nil {
						t.Fatal("absent proposal retained its dangling reader link")
					}
				} else {
					if diagnostic == nil || diagnostic.Status != "cached_evidence" || diagnostic.Page == "" || diagnostic.SVG != "" || diagnostic.Message == "" {
						t.Fatalf("damage did not become an isolated diagnostic: %+v", diagnostic)
					}
					cache730Bodies(t, cold, []ReaderPresentation{*diagnostic})
				}
				cache730SameBodies(t, cold, before)
				cache730SameFiles(t, files)
				for _, dead := range []ReaderPresentation{old, deadC, deadP} {
					cache730Gone(t, cold, dead)
				}
				cache730DiskMatches(t, cold, got)
				// Persist the now-served diagnostic snapshot, then restore again.
				// Its already valid diagnostics and siblings are one-pass/no-write.
				cache730Publish(t, cold, got)
				allFiles := cache730Files(t, cold, cache730Refs(got))
				again := &Server{Cache: cold.Cache, Repos: []*Repo{{Name: c.Repo}}}
				again.loadSnapshot()
				if !reflect.DeepEqual(cache730Refs(got), cache730Refs(cache730State(t, again))) {
					t.Fatal("second restart changed an isolated diagnostic or sibling")
				}
				cache730SameFiles(t, allFiles)
				want := [3]uint64{1, 2, 0}
				if damage == "absent proposal" {
					want[0] = 0
				}
				cache730WantWork(t, again, want)
			})
		}
	}
}

func TestProposalCache730RejectedReceiptSnapshotDoesNotPrune(t *testing.T) {
	s, c, options := cache730Fixture(t)
	receipts := cache730AddReceipts(t, s)
	snap := cache730Snapshot(t, s)
	bad := readerRecoveryProject(t, snap, receipts.Name, "examples/second")
	reader631Write(t, s.readerFile(strings.TrimPrefix(bad.Reader.Fingerprint, "sha256:")), []byte("corrupt receipt"))
	orphan := cache730Clone(t, c)
	orphan.Proposal.Name += " interrupted revision"
	deadC := *s.keepComparison(orphan)
	p := cache730Clone(t, options[0])
	p.Problem = "interrupted preview"
	deadP := *s.keepPreview(p)
	// Copy the last good source reads, as loadSources does on an actual restart.
	// Restoring receipts must continue to populate independent retryable sources.
	cold := &Server{Cache: s.Cache}
	for _, r := range s.Repos {
		rr := *r
		cold.Repos = append(cold.Repos, &rr)
	}
	cold.loadSnapshot()
	if r := reader631GET(cold, "GET", "/api/state.json", ""); r.Code != 503 {
		t.Fatal("receipt damage was broadened into whole-snapshot salvage")
	}
	for _, ref := range append(cache730Refs(snap), deadC, deadP) {
		if ref.Format == comparisonFormat || ref.Format == previewFormat {
			if _, err := os.Stat(cache730FileName(cold, ref)); err != nil {
				t.Fatal("rejected restoration pruned proposal evidence", err)
			}
		}
	}
	replacement := cold.assemble(snap.GeneratedAt.Add(time.Second))
	bad = readerRecoveryProject(t, replacement, receipts.Name, "examples/second")
	readerRecoveryNoResults(t, bad)
	if bad.Reader.Status != "cached_evidence" || !cold.Repos[len(cold.Repos)-1].src.receipts.retry {
		t.Fatal("receipt diagnostic lost authoritative archive retry")
	}
	good := readerRecoveryProject(t, replacement, receipts.Name, "examples/reader")
	if good.ReceiptMissing || !good.Passed || good.Reader.Status != "chart" {
		t.Fatal("rejected snapshot lost its independently healthy receipt")
	}
	cache730Publish(t, cold, replacement)
	cache730Bodies(t, cold, cache730Refs(cache730State(t, cold)))
	cache730Gone(t, cold, deadC)
	cache730Gone(t, cold, deadP)
	cache730DiskMatches(t, cold, replacement)
	// No archive was fetched to turn this failed read into fresh authority.
	if !cold.Repos[len(cold.Repos)-1].src.receipts.retry {
		t.Fatal("proposal cache cleanup cleared receipt retry")
	}
}
