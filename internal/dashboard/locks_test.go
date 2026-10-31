package dashboard

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

// The numbers row counts every lock the gate checked against what a person
// ratified, by a comment or by a ratify line in the journal, and calls out
// one that isn't what was ratified (D-0208). A lock ratified by hand in a
// repository with no journal has nothing to be checked against, so it
// isn't counted.
func TestEveryCheckedLockCounts(t *testing.T) {
	const a, b = "sha256:aa", "sha256:bb"
	comment := func(ratified, holds string) *verify.Report {
		return &verify.Report{Ratified: &project.Ratification{By: "gitdek", Issue: 1, Proposal: ratified}, Proposal: holds}
	}
	journal := func(ratified, holds, problem string) *verify.Report {
		return &verify.Report{Decision: "D-0001", Journal: &verify.JournalRatification{Decision: "D-0001", By: "@gitdek", Proposal: ratified, Problem: problem}, Proposal: holds}
	}
	for _, c := range []struct {
		name    string
		reports []*verify.Report
		checked int
		bad     int
	}{
		{"by comment and by journal", []*verify.Report{comment(a, a), journal(b, b, "")}, 2, 0},
		{"a lock edited after its comment", []*verify.Report{comment(a, b), journal(b, b, "")}, 2, 1},
		{"a lock edited after its ratify line", []*verify.Report{comment(a, a), journal(a, b, "decision D-0001 ... isn't what @gitdek ratified")}, 2, 1},
		{"a decision no person ratified for it", []*verify.Report{journal("", a, "decision D-0001, which the lock names, is proposed")}, 1, 1},
		{"no journal to check against", []*verify.Report{comment(a, a), {Decision: "D-0001"}}, 1, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			var totals Totals
			for _, r := range c.reports {
				countLock(&totals, r)
			}
			if totals.LocksChecked != c.checked || totals.BadLocks != c.bad {
				t.Fatalf("counted %d locks, %d bad; want %d, %d bad", totals.LocksChecked, totals.BadLocks, c.checked, c.bad)
			}
			small := Tiles(totals, Who{})[0].Small
			want := "All 2 factory locks are exactly what a person ratified."
			switch {
			case c.bad > 0:
				want = "1 locks differ from what was ratified."
			case c.checked == 1:
				want = "All 1 factory locks are exactly what a person ratified."
			}
			if !strings.Contains(small, want) {
				t.Errorf("the tile says %q, want it to say %q", small, want)
			}
		})
	}
}
