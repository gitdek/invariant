package dashboard

import (
	"bytes"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestProposalPublicationFailureKeepsReadAndDisk(t *testing.T) {
	s, c, _ := cache730Fixture(t)
	first := cache730Snapshot(t, s)
	before := cache730Bodies(t, s, cache730Refs(first))
	files := cache730Files(t, s, cache730Refs(first))
	disk, err := os.ReadFile(s.snapshotFile())
	if err != nil {
		t.Fatal(err)
	}
	previous := s.acted.read
	// This comment would be forgotten by a successful refresh. Failed
	// publication must leave the command overlay and underlying read intact.
	s.acted.kept = []kept{{repo: c.Repo, issue: c.Issue, Acted: Acted{URL: ""}}}
	for _, failure := range []string{"encoding", "saving"} {
		t.Run(failure, func(t *testing.T) {
			next := cache730Clone(t, first)
			cache730Issue(t, &next, c.Repo, c.Issue).Waiting = nil
			if failure == "encoding" {
				next.GeneratedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
			} else if err := os.Mkdir(s.snapshotFile()+".tmp", 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := s.publish(next); err == nil {
				t.Fatal("failed replacement was published")
			}
			if s.acted.read != previous || len(s.acted.kept) != 1 {
				t.Fatal("failed replacement changed the private page's read or command overlay")
			}
			got, err := os.ReadFile(s.snapshotFile())
			if err != nil || !bytes.Equal(got, disk) {
				t.Fatal("failed replacement changed the saved snapshot", err)
			}
			if !reflect.DeepEqual(cache730Refs(cache730State(t, s)), cache730Refs(first)) {
				t.Fatal("failed replacement changed the served snapshot")
			}
			cache730SameBodies(t, s, before)
			cache730SameFiles(t, files)
		})
	}
}

func TestProposalDelayedPublicationReinstallsRetiredCandidate(t *testing.T) {
	s, c, _ := cache730Fixture(t)
	first := cache730Snapshot(t, s)
	before := cache730Bodies(t, s, cache730Refs(first))
	old := *cache730Issue(t, &first, c.Repo, c.Issue).Waiting.Reader
	ready, proceed := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	delayed := cache730Clone(t, first)
	go func() {
		close(ready)
		<-proceed
		_, err := s.publish(delayed)
		done <- err
	}()
	<-ready
	next := cache730Clone(t, first)
	candidate := cache730Clone(t, c)
	candidate.Proposal.Name += " intervening revision"
	w := cache730Issue(t, &next, c.Repo, c.Issue).Waiting
	w.Proposal, w.Reader = candidate.Proposal, s.keepComparison(candidate)
	_, err := s.publish(next)
	if err == nil {
		cache730Gone(t, s, old)
	}
	close(proceed)
	delayedErr := <-done
	if err != nil || delayedErr != nil {
		t.Fatalf("interleaved publication: %v, %v", err, delayedErr)
	}
	cache730SameBodies(t, s, before)
	cache730DiskMatches(t, s, first)
	cache730WantWork(t, s, [3]uint64{3, 2, 5})
}

func TestProposalRestoredDiagnosticsSurviveImmediateRestart(t *testing.T) {
	s, c, _ := cache730Fixture(t)
	first := cache730Snapshot(t, s)
	old := *cache730Issue(t, &first, c.Repo, c.Issue).Waiting.Reader
	reader631Write(t, cache730FileName(s, old), []byte("{"))
	cold := &Server{Cache: s.Cache, Repos: []*Repo{{Name: c.Repo}}}
	cold.loadSnapshot()
	got := cache730State(t, cold)
	if cache730Issue(t, &got, c.Repo, c.Issue).Waiting.Reader.Status != "cached_evidence" {
		t.Fatal("damaged proposal has no diagnostic")
	}
	files := cache730Files(t, cold, cache730Refs(got))
	// No refresh or explicit snapshot save occurs before the next restart.
	again := &Server{Cache: s.Cache, Repos: []*Repo{{Name: c.Repo}}}
	again.loadSnapshot()
	if !reflect.DeepEqual(cache730Refs(got), cache730Refs(cache730State(t, again))) {
		t.Fatal("immediate restart changed the restored diagnostic")
	}
	cache730SameFiles(t, files)
	cache730WantWork(t, again, [3]uint64{1, 2, 0})
}
