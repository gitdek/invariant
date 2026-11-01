package plumbing

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/synth"
)

// fillCache writes what a run's sandbox leaves in its Go build cache,
// part of it read-only as Go can leave it, and makes it writable again
// when the test ends, so a failed test still cleans up. A cache that's
// already there, from an earlier round, is left as it is.
func fillCache(t *testing.T, dir string) error {
	if _, err := os.Lstat(dir); err == nil {
		return nil
	}
	for _, d := range []string{filepath.Join(dir, "ab"), filepath.Join(dir, "ro", "sub")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(d, "entry-d"), []byte("compiled"), 0o444); err != nil {
			return err
		}
	}
	t.Cleanup(func() {
		filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if d != nil && d.IsDir() {
				os.Chmod(p, 0o755)
			}
			return nil
		})
	})
	for _, d := range []string{filepath.Join(dir, "ro", "sub"), filepath.Join(dir, "ro")} {
		if err := os.Chmod(d, 0o555); err != nil {
			return err
		}
	}
	return nil
}

// cacheAgent stands in for a build's agent. Its run fills the build's
// cache, as the test tool's sandbox runs do, through the -cache its job's
// tool server names. With hello, it writes the page the plan asks for. It
// cancels the build when cancel is set, and its run fails with err.
type cacheAgent struct {
	t      *testing.T
	hello  bool
	err    error
	cancel context.CancelFunc
	filled string
}

func (*cacheAgent) Name() string { return "fixture" }

func (a *cacheAgent) Run(_ context.Context, job synth.Job) (synth.Usage, error) {
	for i, arg := range job.GateServer {
		if arg == "-cache" && i+1 < len(job.GateServer) {
			a.filled = job.GateServer[i+1]
			if err := fillCache(a.t, a.filled); err != nil {
				return synth.Usage{}, err
			}
		}
	}
	if a.hello {
		if err := os.MkdirAll(filepath.Join(job.Workspace, "page"), 0o755); err != nil {
			return synth.Usage{}, err
		}
		if err := os.WriteFile(filepath.Join(job.Workspace, "page", "page.go"), []byte("package page\n\n// Hello says hello.\nfunc Hello() string { return \"hello\" }\n"), 0o644); err != nil {
			return synth.Usage{}, err
		}
	}
	if a.cancel != nil {
		a.cancel()
	}
	return synth.Usage{Outcome: "success"}, a.err
}

// A plan's build removes its Go build cache when it ends, whether it
// passed, failed its checks, its agent's run failed, it stopped with its
// provider unavailable or it was cancelled, and keeps the run's records
// (#366).
func TestABuildRemovesItsCacheWhateverItsOutcome(t *testing.T) {
	failed, passed := false, true
	for _, tc := range []struct {
		name   string
		hello  bool
		err    error
		cancel bool
		passed *bool
	}{
		{name: "passed", hello: true, passed: &passed},
		{name: "failed its checks", passed: &failed},
		{name: "errored", hello: true, err: errors.New("the agent's run failed")},
		{name: "stopped", hello: true, err: fmt.Errorf("Selected model is at capacity: %w", synth.ErrProviderUnavailable), passed: &failed},
		{name: "cancelled", hello: true, cancel: true, passed: &failed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := loopyCheckout(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			agent := &cacheAgent{t: t, hello: tc.hello, err: tc.err}
			if tc.cancel {
				agent.cancel, agent.err = cancel, context.Canceled
			}
			b := Builder{Backend: agent, Reviewer: &providerReviewProbe{}, Binary: "invariant", TestRuns: 4, Timeout: time.Minute,
				Sandbox: Sandbox{Image: "golang", ModCache: t.TempDir()}}
			out := filepath.Join(t.TempDir(), "build")
			r, err := b.Build(ctx, root, 7, out)
			if r == nil {
				t.Fatalf("the build returned no result: %v", err)
			}
			if tc.passed != nil && r.Passed != *tc.passed {
				t.Errorf("passed = %v, want %v: %s", r.Passed, *tc.passed, r.Summary)
			}
			if agent.filled != filepath.Join(out, "gocache") {
				t.Fatalf("the agent's test tool was given the cache %q, not the build's", agent.filled)
			}
			if _, err := os.Lstat(agent.filled); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the build left its cache behind: %v", err)
			}
			for _, name := range []string{"build.json", "transcript.jsonl"} {
				if _, err := os.Stat(filepath.Join(out, name)); err != nil {
					t.Errorf("the build lost its record %s: %v", name, err)
				}
			}
		})
	}
}

// RemoveCache removes a cache Go left partly read-only, and removes a link
// inside it as a link: what the link points to keeps its contents and
// permissions.
func TestRemoveCacheTakesReadOnlyEntriesAndFollowsNoLink(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "gocache")
	if err := fillCache(t, cache); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(dir, "outside")
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(outside, "keep")
	if err := os.WriteFile(keep, []byte("not the cache's"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cache, "ab", "dir-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(keep, filepath.Join(cache, "ab", "file-link")); err != nil {
		t.Fatal(err)
	}
	if err := RemoveCache(cache); err != nil {
		t.Fatalf("RemoveCache = %v", err)
	}
	if _, err := os.Lstat(cache); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the cache is still there: %v", err)
	}
	if b, err := os.ReadFile(keep); err != nil || string(b) != "not the cache's" {
		t.Errorf("a link in the cache changed what it points to: %q, %v", b, err)
	}
	for path, want := range map[string]fs.FileMode{outside: 0o750, keep: 0o400} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != want {
			t.Errorf("%s's permissions changed through a link in the cache: %v, %v", path, info.Mode().Perm(), err)
		}
	}
	for _, dir := range []string{"", filepath.Join(dir, "never")} {
		if err := RemoveCache(dir); err != nil {
			t.Errorf("RemoveCache(%q) = %v; want nothing to remove", dir, err)
		}
	}
}
