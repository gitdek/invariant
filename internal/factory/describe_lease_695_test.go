package factory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

// A watcher without the lease can't rewrite a pull request's description,
// the handoff that names a held draft's trusted paths. With the lease, for
// a step in its tenure, it can.
func TestDescribingAPullRequestNeedsTheLease(t *testing.T) {
	ctx := context.Background()
	origin, _ := gitRepos(t)
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	f := leaseFactory(t, origin, "watcher-a", &now)
	gh := f.GitHub.(*fakeGitHub)
	pr, err := gh.CreatePullRequest(ctx, github.NewPullRequest{Head: "main", Base: "main", Draft: true, Body: "before"})
	if err != nil {
		t.Fatal(err)
	}
	g := leasedGitHub{f.GitHub, f}
	if _, err := g.DescribePullRequest(ctx, pr.Number, "after"); !errors.Is(err, errLeaseLost) {
		t.Fatalf("DescribePullRequest without the lease = %v; want errLeaseLost", err)
	}
	if body := gh.prBody(pr.Number); body != "before" {
		t.Fatalf("a watcher without the lease rewrote the description: %q", body)
	}
	if held, err := f.hold(ctx); err != nil || !held {
		t.Fatal(held, err)
	}
	if _, err := g.DescribePullRequest(f.inTenure(ctx), pr.Number, "after"); err != nil {
		t.Fatalf("DescribePullRequest with the lease = %v", err)
	}
	if body := gh.prBody(pr.Number); body != "after" {
		t.Fatalf("the description is %q after a leased rewrite", body)
	}
}
