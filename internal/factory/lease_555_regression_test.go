package factory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// slowGit555 puts a git first on PATH that waits before each command on
// the factory's own refs, the lease's and the run records' reads and
// pushes, as they do when GitHub is slow (#555).
func slowGit555(t *testing.T, delay time.Duration) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	quoted := "'" + strings.ReplaceAll(real, "'", `'"'"'`) + "'"
	script := fmt.Sprintf("#!/bin/sh\ncase \" $* \" in *refs/invariant/*) sleep %.3f ;; esac\nexec %s \"$@\"\n", delay.Seconds(), quoted)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// With the lease's and run records' git commands 50 ms slower, a renewal
// spends longer in git than a 20 ms poll lasts. Quick steps still go on
// while the watcher drains.
func TestDrainKeepsQuickStepsWithSlowGit555(t *testing.T) {
	slowGit555(t, 50*time.Millisecond)
	TestDrainFinishesBuildsAndKeepsQuickSteps463(t)
}

// gatedLease555 holds each renewal's read of the lease until the test lets
// it go, as a slow git call would.
type gatedLease555 struct {
	Repo
	entered, release chan struct{}
}

func (r gatedLease555) Lease(ctx context.Context) (string, LeaseRecord, error) {
	r.entered <- struct{}{}
	<-r.release
	return r.Repo.Lease(ctx)
}

// While a renewal waits on git, a run push whose clone is free checks its
// permission against the lease last confirmed, and lands. Renewals still
// run one at a time.
func TestARunPushGoesOnWhileARenewalWaitsOnGit555(t *testing.T) {
	_, clone := gitRepos(t)
	f := &Factory{Repo: clone, Holder: "watcher-a", LeaseFor: 5 * time.Minute, Now: time.Now, Log: t.Logf}
	if held, err := f.hold(context.Background()); err != nil || !held {
		t.Fatal(held, err)
	}
	ctx, cancel := context.WithTimeout(f.inTenure(context.Background()), 20*time.Second)
	defer cancel()
	gate := gatedLease555{Repo: clone, entered: make(chan struct{}), release: make(chan struct{})}
	f.Repo = gate
	renewed := make(chan error, 2)
	renew := func() {
		held, err := f.hold(context.Background())
		if err == nil && !held {
			err = errors.New("a renewal lost the lease")
		}
		renewed <- err
	}
	go renew()
	<-gate.entered
	go renew()
	pushed := make(chan error, 1)
	go func() { pushed <- leasedRepo{Repo: clone, f: f}.Record(ctx, 555, "build", "a build") }()
	select {
	case err := <-pushed:
		if err != nil {
			t.Errorf("the run push failed while a renewal read the lease: %v", err)
		}
		pushed <- nil
	case <-time.After(10 * time.Second):
		t.Error("the run push waited for a renewal's git call")
	}
	select {
	case <-gate.entered:
		t.Fatal("a second renewal read the lease while the first one did")
	default:
	}
	close(gate.release)
	<-gate.entered
	for range 2 {
		if err := <-renewed; err != nil {
			t.Error(err)
		}
	}
	if err := <-pushed; err != nil {
		t.Errorf("the run push failed after the renewals: %v", err)
	}
	if state, _, err := clone.Run(context.Background(), 555, "build"); err != nil || state != RunRecorded {
		t.Errorf("the record didn't land: %v, %v", state, err)
	}
}
