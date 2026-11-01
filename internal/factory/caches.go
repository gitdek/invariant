package factory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gitdek/invariant/internal/plumbing"
)

// clearRunCaches removes the Go build caches runs left in the work
// directory, and says how many it removed (#366). Each plan's draft and
// build removes its own as it ends, so one is left only by a watcher that
// stopped mid-run. Watch clears them the first time it may act, before it
// starts a step, as it clears the clone's worktrees: the work directory is
// the watcher's own, so no run there is live then. A run one of this
// watcher's steps is in keeps its cache all the same, and a link named
// gocache is never followed.
func (f *Factory) clearRunCaches() (int, error) {
	if f.Work == "" {
		return 0, nil
	}
	live := map[string]bool{}
	f.running.mu.Lock()
	for issue, s := range f.running.steps {
		if s.Run != "" {
			live[filepath.Join(f.Work, fmt.Sprintf("issue-%d", issue), s.Run)] = true
		}
	}
	f.running.mu.Unlock()
	caches, err := filepath.Glob(filepath.Join(f.Work, "issue-*", "*", "gocache"))
	if err != nil {
		return 0, err
	}
	removed := 0
	var errs []error
	for _, cache := range caches {
		if live[filepath.Dir(cache)] {
			continue
		}
		if info, err := os.Lstat(cache); err != nil || !info.IsDir() {
			continue
		}
		if err := plumbing.RemoveCache(cache); err != nil {
			errs = append(errs, err)
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}
