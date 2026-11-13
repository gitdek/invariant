package project

import (
	"os"
	"path/filepath"
	"testing"
)

// The reader that takes a pin's text reads a model the way TLC does and
// refuses what it can't read exactly (#812). Making it stricter must leave
// every ratified pin where it was: each project the repository holds still
// reads, and each of its statements still has the pin its lock records.
func TestEveryRatifiedLockStillMatchesItsModel812(t *testing.T) {
	var dirs []string
	for _, pattern := range []string{"../../examples/*", "../../factory/*"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, dir := range matches {
			if _, err := os.Stat(filepath.Join(dir, lockFile)); err == nil {
				dirs = append(dirs, dir)
			}
		}
	}
	if len(dirs) < 15 {
		t.Fatalf("found %d projects with a lock under examples/ and factory/; the repository holds more", len(dirs))
	}
	statements := 0
	for _, dir := range dirs {
		p, err := Load(dir)
		if err != nil {
			t.Errorf("%s: %v", dir, err)
			continue
		}
		src, err := os.ReadFile(p.ModulePath())
		if err != nil {
			t.Errorf("%s: %v", dir, err)
			continue
		}
		model, err := p.ReadModel(string(src))
		if err != nil {
			t.Errorf("%s: the reader refuses its model: %v", dir, err)
			continue
		}
		for _, s := range p.Lock.Statements {
			statements++
			if pin, err := model.PinHash(s.Name, Model); err != nil || pin != s.SHA256 {
				t.Errorf("%s: %s reads as %s, %v; its lock pins %s", dir, s.Name, pin, err, s.SHA256)
			}
		}
		if _, err := model.Steps(); err != nil {
			t.Errorf("%s: the steps of Next: %v", dir, err)
		}
	}
	t.Logf("%d projects, %d pinned statements", len(dirs), statements)
}
