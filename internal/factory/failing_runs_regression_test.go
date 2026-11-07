package factory

import (
	"context"
	"testing"
	"time"
)

// GitHub's run of failures at a request ends once the request succeeds, and
// a failure after that starts the count afresh. A watched draft's events
// read that failed once, then succeeded while GitHub worked out whether it
// could merge, isn't said to have kept failing when it fails again more than
// half an hour after the first failure (#760).
func TestASuccessEndsGitHubsRunOfFailures(t *testing.T) {
	r := newRig(t)
	now := clockOn(r.f)
	ctx := context.Background()
	const base = "failing-1-events-7-abcdef123456"
	since := func(at time.Duration, want time.Duration) {
		t.Helper()
		*now = clockStart.Add(at)
		got, err := r.f.failingSince(ctx, 1, base)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Equal(clockStart.Add(want)) {
			t.Fatalf("a failure %s after the clock started counts from %s; want %s", at, got.Sub(clockStart), want)
		}
	}
	ended := func(at time.Duration) {
		t.Helper()
		*now = clockStart.Add(at)
		if err := r.f.failingEnded(ctx, 1, base); err != nil {
			t.Fatal(err)
		}
	}
	ended(0) // a success with nothing failing changes nothing
	since(0, 0)
	since(20*time.Minute, 0)
	ended(25 * time.Minute)
	since(45*time.Minute, 45*time.Minute)
	since(70*time.Minute, 45*time.Minute)
	ended(80 * time.Minute)
	ended(81 * time.Minute)
	since(3*time.Hour, 3*time.Hour)
}
