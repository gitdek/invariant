package factory

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// leftRun makes a run directory under work, as a watcher that stopped
// mid-run leaves it: its records, and, with a cache, a Go build cache part
// of which is read-only, as Go can leave it.
func leftRun(t *testing.T, work, run string, cache bool) string {
	t.Helper()
	dir := filepath.Join(work, run)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "transcript.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !cache {
		return dir
	}
	ro := filepath.Join(dir, "gocache", "ro")
	if err := os.MkdirAll(ro, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ro, "entry-d"), []byte("compiled"), 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(ro, 0o755) })
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	return dir
}

func cacheGone(t *testing.T, run string) bool {
	t.Helper()
	_, err := os.Lstat(filepath.Join(run, "gocache"))
	return errors.Is(err, fs.ErrNotExist)
}

// The first time a watcher may act, before it starts a step, it removes
// the Go build caches that runs in its work directory left, and keeps
// every run's records (#366).
func TestAWatchersStartClearsTheCachesRunsLeft(t *testing.T) {
	r := newRig(t)
	build := leftRun(t, r.f.Work, "issue-1/build-1", true)
	draft := leftRun(t, r.f.Work, "issue-2/formalize-9", true)
	plain := leftRun(t, r.f.Work, "issue-2/formalize-10", false)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	r.f.Watch(ctx, 20*time.Millisecond)
	for _, run := range []string{build, draft} {
		if !cacheGone(t, run) {
			t.Errorf("%s still has its cache", run)
		}
	}
	for _, run := range []string{build, draft, plain} {
		if _, err := os.Stat(filepath.Join(run, "transcript.jsonl")); err != nil {
			t.Errorf("%s lost its record: %v", run, err)
		}
	}
}

// The sweep spares a run one of this watcher's steps is in, and doesn't
// follow a link named gocache out of the work directory.
func TestClearingRunCachesSparesALiveRun(t *testing.T) {
	r := newRig(t)
	ended := leftRun(t, r.f.Work, "issue-1/build-1", true)
	live := leftRun(t, r.f.Work, "issue-3/build-2", true)
	r.f.running.mu.Lock()
	r.f.running.steps = map[int]Step{3: {Issue: 3, Doing: "building", Run: "build-2"}}
	r.f.running.mu.Unlock()
	outside := t.TempDir()
	keep := filepath.Join(outside, "keep")
	if err := os.WriteFile(keep, []byte("not a cache"), 0o644); err != nil {
		t.Fatal(err)
	}
	linked := leftRun(t, r.f.Work, "issue-4/build-3", false)
	if err := os.Symlink(outside, filepath.Join(linked, "gocache")); err != nil {
		t.Fatal(err)
	}
	removed, err := r.f.clearRunCaches()
	if err != nil || removed != 1 {
		t.Fatalf("clearRunCaches = %d, %v; want the ended run's one cache", removed, err)
	}
	if !cacheGone(t, ended) {
		t.Error("the ended run still has its cache")
	}
	if cacheGone(t, live) {
		t.Error("the sweep removed a live run's cache")
	}
	if b, err := os.ReadFile(keep); err != nil || string(b) != "not a cache" {
		t.Errorf("the sweep followed a link out of the work directory: %q, %v", b, err)
	}
}
