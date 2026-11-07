package dashboard

import (
	"bytes"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestProposalCachePreparedSnapshotSurvivesInterveningPublication(t *testing.T) {
	s, original, _ := cache730Fixture(t)
	first := cache730Snapshot(t, s)
	candidate := cache730Clone(t, first)
	e := cache730Clone(t, original)
	e.Proposal.Name += " prepared revision"
	w := cache730Issue(t, &candidate, e.Repo, e.Issue).Waiting
	w.Proposal, w.Reader = e.Proposal, s.keepComparison(e)
	p := *w.Reader
	body := cache730Bodies(t, s, []ReaderPresentation{p})
	// Another refresh retires the prepared page before its snapshot publishes.
	cache730Publish(t, s, first)
	cache730Gone(t, s, p)
	candidate.GeneratedAt = first.GeneratedAt.Add(time.Second)
	cache730Publish(t, s, candidate)
	cache730SameBodies(t, s, body)
	cache730DiskMatches(t, s, candidate)
	// The retained refresh is detached from its caller as well as its cache.
	w.Reader.Message = "caller changed its snapshot"
	s.acted.mu.Lock()
	got := *cache730Issue(t, s.acted.read, e.Repo, e.Issue).Waiting.Reader
	s.acted.mu.Unlock()
	if got != p {
		t.Fatal("publication retained caller-owned presentation")
	}
}

func TestProposalCacheFailedPublicationPreservesActedRead(t *testing.T) {
	s, e, _ := cache730Fixture(t)
	first := cache730Snapshot(t, s)
	before := reader631GET(s, "GET", "/api/state.json", "").Body.Bytes()
	s.acted.mu.Lock()
	s.acted.kept = []kept{{repo: e.Repo, issue: e.Issue, Acted: Acted{URL: "already-read"}}}
	read := s.acted.read
	keptBefore := append([]kept(nil), s.acted.kept...)
	s.acted.mu.Unlock()
	r := s.Repos[0]
	c := r.src.comments[e.Issue]
	c.comments[0].URL = "already-read"
	r.src.comments[e.Issue] = c
	bad := cache730Clone(t, first)
	cache730Issue(t, &bad, e.Repo, e.Issue).Waiting.Reader.Message += " substituted card"
	if _, err := s.publish(bad); err == nil {
		t.Fatal("mismatched presentation published")
	}
	s.acted.mu.Lock()
	unchanged := s.acted.read == read && reflect.DeepEqual(s.acted.kept, keptBefore)
	s.acted.mu.Unlock()
	if !unchanged || !bytes.Equal(before, reader631GET(s, "GET", "/api/state.json", "").Body.Bytes()) {
		t.Fatal("failed publication replaced the refresh or forgot a command")
	}
	cache730Bodies(t, s, cache730Refs(first))
	cache730DiskMatches(t, s, first)
}

func TestProposalCacheFailedSnapshotSaveRetainsDurableEvidence(t *testing.T) {
	s, e, _ := cache730Fixture(t)
	first := cache730Snapshot(t, s)
	p := *cache730Issue(t, &first, e.Repo, e.Issue).Waiting.Reader
	next := cache730Clone(t, first)
	cache730Issue(t, &next, e.Repo, e.Issue).Waiting = nil
	// Fail the snapshot write after the replacement becomes visible in memory.
	blocked := s.snapshotFile() + ".tmp"
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	gz, err := s.publish(next)
	if err != nil {
		t.Fatal(err)
	}
	s.saveSnapshot(gz)
	if _, err := os.Stat(cache730FileName(s, p)); err != nil {
		t.Fatal("failed snapshot save removed the durable snapshot's evidence", err)
	}
	cold := &Server{Cache: s.Cache, Repos: []*Repo{{Name: e.Repo}}}
	cold.loadSnapshot()
	if !reflect.DeepEqual(cache730Refs(first), cache730Refs(cache730State(t, cold))) {
		t.Fatal("failed snapshot save broke restart recovery")
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	s.saveSnapshot(gz)
	cache730Gone(t, s, p)
	cache730DiskMatches(t, s, next)
}

func TestProposalCacheBuildBetweenPublicationAndSave(t *testing.T) {
	s, original, _ := cache730Fixture(t)
	first := cache730Snapshot(t, s)
	gz, err := s.publish(first)
	if err != nil {
		t.Fatal(err)
	}
	e := cache730Clone(t, original)
	e.Proposal.Name += " built during snapshot save"
	p := *s.keepComparison(e)
	s.saveSnapshot(gz)
	// A sweep must retire both the saved bit and its lookup, so the next keep
	// cannot reuse an entry whose evidence file that sweep removed.
	if got := *s.keepComparison(e); got != p {
		t.Fatal("retired candidate changed identity")
	}
	if _, err := os.Stat(cache730FileName(s, p)); err != nil {
		t.Fatal("lookup claimed persistence after its file was retired", err)
	}
}
