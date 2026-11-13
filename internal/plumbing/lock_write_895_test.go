package plumbing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A lock is written by joining each acceptance test file's path to the
// checkout. A plan carried in a comment can name any path, and only a
// configured repository's plan was checked before the write: the second
// review of #799 had a plan's ratification write a file beside the checkout
// and overwrite the one planted there. The write refuses it itself.
func TestLockWritesNothingOutsideItsCheckout895(t *testing.T) {
	plan := func(file string) Plan {
		return Plan{Name: "markers", Summary: "Read markers.", Files: []string{"internal/x/x.go"},
			Tests:   []Test{{Name: "TestX", File: file, Says: "It reads."}},
			Sources: map[string]string{file: "package x\n\nimport \"testing\"\n\n// written by a plan's ratification\nfunc TestX(t *testing.T) {}\n"}}
	}
	fixture := func(t *testing.T) (checkout, outside, victim string) {
		t.Helper()
		base := t.TempDir()
		checkout, outside = filepath.Join(base, "checkout"), filepath.Join(base, "outside")
		for _, dir := range []string{filepath.Join(checkout, "internal"), outside} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		victim = filepath.Join(outside, "evil_test.go")
		if err := os.WriteFile(victim, []byte("someone else's file\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return checkout, outside, victim
	}
	untouched := func(t *testing.T, outside, victim string) {
		t.Helper()
		if b, err := os.ReadFile(victim); err != nil || string(b) != "someone else's file\n" {
			t.Fatalf("the file outside the checkout was removed or changed: %q, %v", b, err)
		}
		if entries, err := os.ReadDir(outside); err != nil || len(entries) != 1 {
			t.Fatalf("the write left %d files outside its checkout: %v", len(entries), err)
		}
	}
	t.Run("a path that climbs out", func(t *testing.T) {
		for _, file := range []string{"../outside/evil_test.go", "internal/../../outside/evil_test.go"} {
			checkout, outside, victim := fixture(t)
			p := plan(file)
			err := (Lock{Plan: p, Ratified: Ratification{Issue: 7, Proposal: p.Hash()}}).Write(checkout)
			if err == nil || !strings.Contains(err.Error(), "isn't a path inside the repository") {
				t.Fatalf("a lock whose test file is %s was written: %v", file, err)
			}
			untouched(t, outside, victim)
			if _, err := os.Stat(filepath.Join(checkout, LockDir)); !os.IsNotExist(err) {
				t.Fatalf("a refused lock wrote its record all the same: %v", err)
			}
		}
		checkout, outside, victim := fixture(t)
		p := plan(victim)
		if err := (Lock{Plan: p, Ratified: Ratification{Issue: 7, Proposal: p.Hash()}}).Write(checkout); err == nil {
			t.Fatal("a lock whose test file is an absolute path was written")
		}
		untouched(t, outside, victim)
	})
	t.Run("a link in the checkout that leads out", func(t *testing.T) {
		checkout, outside, victim := fixture(t)
		if err := os.Symlink(outside, filepath.Join(checkout, "internal", "link")); err != nil {
			t.Fatal(err)
		}
		p := plan("internal/link/evil_test.go")
		if err := (Lock{Plan: p, Ratified: Ratification{Issue: 7, Proposal: p.Hash()}}).Write(checkout); err == nil {
			t.Fatal("a lock was written through a link that leads out of its checkout")
		}
		untouched(t, outside, victim)
	})
	t.Run("a link where a test file goes", func(t *testing.T) {
		checkout, outside, victim := fixture(t)
		if err := os.MkdirAll(filepath.Join(checkout, "internal", "x"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, filepath.Join(checkout, "internal", "x", "x_test.go")); err != nil {
			t.Fatal(err)
		}
		p := plan("internal/x/x_test.go")
		if err := (Lock{Plan: p, Ratified: Ratification{Issue: 7, Proposal: p.Hash()}}).Write(checkout); err == nil {
			t.Fatal("a lock's test file was written through a link that leads out of its checkout")
		}
		untouched(t, outside, victim)
	})
	t.Run("a plan inside its checkout", func(t *testing.T) {
		checkout, outside, victim := fixture(t)
		p := plan("internal/x/x_test.go")
		if err := (Lock{Plan: p, Ratified: Ratification{Issue: 7, Proposal: p.Hash()}}).Write(checkout); err != nil {
			t.Fatal(err)
		}
		if b, err := os.ReadFile(filepath.Join(checkout, "internal", "x", "x_test.go")); err != nil || !strings.Contains(string(b), "written by a plan's ratification") {
			t.Fatalf("the acceptance test file wasn't written: %v", err)
		}
		if l, err := ReadLockFile(checkout, 7); err != nil || l.Plan.Hash() != p.Hash() {
			t.Fatalf("the record written doesn't read back as the plan: %v", err)
		}
		untouched(t, outside, victim)
	})
}
