package decisions

import (
	"testing"
	"time"
)

// A rebuild reads a checkout's journals and then replaces the project's
// nodes. A decision another process writes in between would be dropped, so
// a rebuild waits while another store on the same database holds the write
// lock, and finishes once it's released, with every decision in place
// (#758).
func TestRebuildWaitsForTheWriteLock(t *testing.T) {
	s, repo := fixture(t)
	if _, err := s.Decide(repo, NewDecision{Door: "two-way", Status: "decided", Who: "agent", Text: "Keep the store in one file."}, "agent"); err != nil {
		t.Fatal(err)
	}
	other, err := Open(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	unlock, err := other.lockWrites()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Rebuild([]Repo{repo}) }()
	select {
	case err := <-done:
		unlock()
		t.Fatalf("the rebuild ran while another store held the write lock: %v", err)
	case <-time.After(time.Second):
	}
	unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the rebuild didn't finish once the write lock was released")
	}
	nodes, err := s.Decisions(repo.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(nodes); len(got) != 1 || got[0] != repo.Name+"/D-0001" {
		t.Errorf("after the rebuild the store holds %v; want the one decision", got)
	}
}
