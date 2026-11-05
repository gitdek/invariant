package factory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

// A run is recorded once, by whichever watcher records it first, and every
// watcher sees its state and, once it finishes, its result.
func TestRunsAreRecordedOnceAndSeenEverywhere(t *testing.T) {
	ctx := context.Background()
	origin, a := gitRepos(t)
	b := Clone{Dir: filepath.Join(t.TempDir(), "clone"), Remote: origin, Name: a.Name, Email: a.Email}
	if err := b.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if state, _, err := b.Run(ctx, 7, "draft-1001"); err != nil || state != RunNone {
		t.Fatalf("Run = %v, %v; want nothing recorded", state, err)
	}
	if err := a.Record(ctx, 7, "draft-1001", "a draft for comment 1001"); err != nil {
		t.Fatal(err)
	}
	if err := b.Record(ctx, 7, "draft-1001", "a draft for comment 1001"); !errors.Is(err, ErrRecorded) {
		t.Fatalf("a second Record = %v; want ErrRecorded", err)
	}
	if state, _, err := b.Run(ctx, 7, "draft-1001"); err != nil || state != RunRecorded {
		t.Fatalf("Run on the other clone = %v, %v; want recorded", state, err)
	}

	// The run finishes: its result is saved and recorded, and the other
	// watcher reads it back.
	result := t.TempDir()
	os.WriteFile(filepath.Join(result, "result.json"), []byte(`{"proposal": "p"}`), 0o644)
	os.MkdirAll(filepath.Join(result, "draft"), 0o755)
	os.WriteFile(filepath.Join(result, "draft", "Mutex.tla"), []byte("---- MODULE Mutex ----\n===="), 0o644)
	commit, err := a.Save(ctx, result, "the draft for comment 1001", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Finish(ctx, 7, "draft-1001", commit); err != nil {
		t.Fatal(err)
	}
	state, sha, err := b.Run(ctx, 7, "draft-1001")
	if err != nil || state != RunDone || sha != commit {
		t.Fatalf("Run after Finish = %v %s, %v; want done at %s", state, sha, err, commit)
	}
	back := t.TempDir()
	if err := b.Load(ctx, sha, back); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"result.json": `{"proposal": "p"}`, "draft/Mutex.tla": "---- MODULE Mutex ----\n===="} {
		if got, err := os.ReadFile(filepath.Join(back, name)); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
	// A finished run can't be finished again, or recorded again.
	if err := b.Finish(ctx, 7, "draft-1001", commit); err == nil {
		t.Error("finished a run twice")
	}
	if err := b.Record(ctx, 7, "draft-1001", "again"); !errors.Is(err, ErrRecorded) {
		t.Errorf("recorded a finished run again: %v", err)
	}
}

// Two calls with the same description still have different nonces. Git's
// compare-and-swap lets only one record the run, even on separate clones.
func TestConcurrentRecordsHaveOneWinner(t *testing.T) {
	origin, a := gitRepos(t)
	b := Clone{Dir: filepath.Join(t.TempDir(), "clone"), Remote: origin, Name: a.Name, Email: a.Email}
	ctx := context.Background()
	if err := b.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	done := make(chan error, 2)
	for _, c := range []Clone{a, b} {
		go func() {
			<-start
			done <- c.Record(ctx, 7, "draft-1001", "the same draft")
		}()
	}
	close(start)
	won, lost := 0, 0
	for range 2 {
		switch err := <-done; {
		case err == nil:
			won++
		case errors.Is(err, ErrRecorded):
			lost++
		default:
			t.Fatalf("Record = %v", err)
		}
	}
	if won != 1 || lost != 1 {
		t.Fatalf("%d winners and %d losers; want one of each", won, lost)
	}
	if what, err := b.Recorded(ctx, 7, "draft-1001"); err != nil || what != "the same draft" {
		t.Fatalf("Recorded = %q, %v", what, err)
	}
}

// Reconciliation also wins over a refusal on the very last attempt. It
// must happen before the retry budget is declared exhausted.
func TestTheLastRunPushCanLandWithoutItsAnswer(t *testing.T) {
	for _, operation := range []string{"Record", "Finish"} {
		t.Run(operation, func(t *testing.T) {
			replies := make([]run545Reply, 4)
			for i := range replies {
				replies[i] = run545Reply{"fail", run545ServerError}
			}
			f := newRun545Fixture(t, operation, append(replies, run545Reply{"land", "fatal: The requested URL returned error: 403"})...)
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				if err := f.call(context.Background(), f.clone, operation); err != nil {
					t.Fatal(err)
				}
				if elapsed := time.Since(start); elapsed != 225*time.Second {
					t.Errorf("waited %v; want 225s", elapsed)
				}
				f.checkAttempts(t, 5, 5)
				f.checkPublished(t, operation)
			})
		})
	}
}

// A different writer can move the ref while this call waits. The next
// push must keep its original expectation, so it cannot overwrite that
// writer, even when the new commit is another unfinished record.
func TestARunPushCannotReplaceARecordMadeDuringBackoff(t *testing.T) {
	for _, operation := range []string{"Record", "Finish"} {
		t.Run(operation, func(t *testing.T) {
			f := newRun545Fixture(t, operation, run545Reply{"fail", run545ServerError}, run545Reply{"ok", ""})
			synctest.Test(t, func(t *testing.T) {
				timer := time.AfterFunc(5*time.Second, func() {
					git(t, f.origin, "update-ref", f.ref, f.otherRecord)
				})
				defer timer.Stop()
				start := time.Now()
				err := f.call(context.Background(), f.clone, operation)
				if err == nil || operation == "Record" && !errors.Is(err, ErrRecorded) {
					t.Fatalf("%s over another record = %v", operation, err)
				}
				if elapsed := time.Since(start); elapsed != 15*time.Second {
					t.Errorf("waited %v; want 15s", elapsed)
				}
				f.checkAttempts(t, 2, 2)
				if got := f.remote(t); got != f.otherRecord {
					t.Errorf("ref = %s; want the other writer's %s", got, f.otherRecord)
				}
			})
		})
	}
}
