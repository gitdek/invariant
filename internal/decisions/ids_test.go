package decisions

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

// Two checkouts of one project, each on a branch of its own, share the
// machine's store.
func twoCheckouts(t *testing.T) (*Store, Repo, Repo) {
	t.Helper()
	s, a := fixture(t)
	b := Repo{Name: a.Name, Dir: filepath.Join(filepath.Dir(a.Dir), "other")}
	for _, r := range []Repo{a, b} {
		if err := os.MkdirAll(r.JournalDir(), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return s, a, b
}

func decision(text string) NewDecision {
	return NewDecision{Door: "two-way", Status: "decided", Who: "agent", Text: text}
}

// A decide that stops after the store records its ID, before its journal
// file is written, leaves a gap: the other checkout takes a new ID, never
// that one, and so does the checkout itself once it decides again (#192,
// factory/ids). Decide had it the other way round: it wrote the journal file
// first, so a stop between the two left an ID the store didn't know.
func TestADecideThatStopsLeavesAGapNotASharedID(t *testing.T) {
	s, a, b := twoCheckouts(t)
	// The journal file can't be written, so a's decide stops there.
	if err := os.Chmod(a.JournalDir(), 0o500); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(a, decision("Stops before its journal file is written."), "agent"); err == nil {
		t.Fatal("the decide should stop when its journal file can't be written")
	}
	if err := os.Chmod(a.JournalDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	other, err := s.Decide(b, decision("The other checkout's."), "agent")
	if err != nil || other != "D-0002" {
		t.Fatalf("the other checkout took %q, %v; want D-0002, past the ID the stopped decide took", other, err)
	}
	again, err := s.Decide(a, decision("The first checkout's, again."), "agent")
	if err != nil || again != "D-0003" {
		t.Fatalf("the first checkout took %q, %v; want D-0003", again, err)
	}
	if _, err := os.Stat(filepath.Join(a.JournalDir(), "D-0001.jsonl")); !os.IsNotExist(err) {
		t.Errorf("the stopped decide's journal file is there: %v", err)
	}
}

// Decides in two checkouts at once, many of them, never take the same ID.
func TestDecidesAtOnceNeverShareAnID(t *testing.T) {
	s, a, b := twoCheckouts(t)
	var mu sync.Mutex
	var got []string
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := a
			if i%2 == 1 {
				r = b
			}
			id, err := s.Decide(r, decision("One of many at once."), "agent")
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			got = append(got, id)
			mu.Unlock()
		}()
	}
	wg.Wait()
	slices.Sort(got)
	if len(slices.Compact(slices.Clone(got))) != len(got) || len(got) != 20 {
		t.Errorf("the IDs taken at once: %v", got)
	}
}
