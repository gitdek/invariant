package factory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

type failedMergeList struct {
	GitHub
	fail bool
}

func (g *failedMergeList) MergedPullRequests(ctx context.Context) ([]github.PullRequest, error) {
	if g.fail {
		g.fail = false
		return nil, errors.New("merge list unavailable")
	}
	return g.GitHub.MergedPullRequests(ctx)
}

func TestPollDiscoveryFailureKeepsWatermarkAndHeartbeat(t *testing.T) {
	r := newRig(t)
	g := &failedMergeList{GitHub: r.f.GitHub}
	r.f.GitHub = g
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	r.f.Now = func() time.Time { return now }
	heartbeats := 0
	r.f.Activity = func([]Step) { heartbeats++ }
	r.poll()
	before := r.f.closedSince
	now = now.Add(5 * time.Minute)
	g.fail = true
	if err := r.f.Poll(context.Background()); err == nil {
		t.Fatal("the merge list failure was lost")
	}
	if r.f.closedSince != before || heartbeats != 2 {
		t.Fatalf("failed discovery: watermark %s, heartbeat count %d", r.f.closedSince, heartbeats)
	}
	r.poll()
	if !r.f.closedSince.After(before) || heartbeats != 3 {
		t.Fatal("successful retry did not advance discovery and its heartbeat")
	}
}

func TestPollChangedTriggerPreservesLabels(t *testing.T) {
	r := newRig(t)
	r.gh.open(1, "gitdek", "A bounded buffer", "No command.", "bug")
	r.gh.issues[1].UpdatedAt = "2026-11-03T12:00:00Z"
	r.poll()
	if err := r.gh.AddLabels(context.Background(), 1, LabelTrigger); err != nil {
		t.Fatal(err)
	}
	r.gh.events[1] = append(r.gh.events[1], github.Event{Event: "labeled", Actor: github.User{Login: "gitdek"}, Label: &github.Label{Name: LabelTrigger}})
	r.gh.issues[1].UpdatedAt = "2026-11-03T12:00:01Z"
	r.poll()
	r.expect(1, KindForks, LabelTrigger, LabelAsking, "bug")
	r.poll()
	r.poll()
	if len(r.form.requests) != 1 || len(r.gh.posts(1)) != 1 {
		t.Fatal("the retained trigger repeated the draft")
	}
}
