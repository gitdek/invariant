package factory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
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
	commit, err := a.Save(ctx, result, "the draft for comment 1001")
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
