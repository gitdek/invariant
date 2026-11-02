package factory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/scope"
)

// scopeRace434Hub lets the initial read see an open pull request and the
// read after the scope check see what happened while the step was running.
type scopeRace434Hub struct {
	GitHub
	afterScope bool
	readsAfter int
	readError  error
}

func (g *scopeRace434Hub) PullRequest(ctx context.Context, n int) (github.PullRequest, error) {
	if g.afterScope {
		g.readsAfter++
		if g.readError != nil {
			return github.PullRequest{}, g.readError
		}
	}
	return g.GitHub.PullRequest(ctx, n)
}

// scopeRace434Repo advances the real origin's main to the pull request's
// head after watch has read the open pull request. The real three-dot diff
// therefore becomes empty, just as it does when a person merges mid-step.
type scopeRace434Repo struct {
	Repo
	r      *rig
	hub    *scopeRace434Hub
	pr     int
	merged bool
	fired  bool
}

func (r *scopeRace434Repo) Scope(ctx context.Context, base, head string, issue int) (scope.Result, error) {
	if !r.fired {
		r.fired = true
		before, err := r.Repo.Scope(ctx, base, head, issue)
		if err != nil {
			return before, err
		}
		if len(before.Files) == 0 {
			r.r.t.Fatal("the pull request must change files before main advances")
		}
		pr := r.r.gh.prs[r.pr]
		git(r.r.t, r.r.origin, "update-ref", "refs/heads/main", pr.Head.SHA)
		if err := r.Repo.Fetch(ctx); err != nil {
			return scope.Result{}, err
		}
		if r.merged {
			pr.Merged, pr.State, pr.MergeCommitSHA = true, "closed", pr.Head.SHA
		}
		r.hub.afterScope = true
	}
	result, err := r.Repo.Scope(ctx, base, head, issue)
	if err == nil && len(result.Files) != 0 {
		r.r.t.Fatalf("the real diff after main advances must be empty: %v", result.Files)
	}
	return result, err
}

func scopeRace434Rig(t *testing.T, plumbing bool) (*rig, Post) {
	t.Helper()
	if !plumbing {
		r := newRig(t)
		return r, ratified(t, r)
	}
	r, _ := plumbingRig(t, "internal/factory/page.go")
	r.gh.open(1, "gitdek", "Add a hello page", "Kind: plumbing\n\n/invariant solve")
	r.poll()
	proposal := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(proposal.Marker.Proposal.Hash, "sha256:"))
	r.poll()
	return r, r.expect(1, KindPR, LabelPR)
}

// An empty scope diff is not evidence that a merged pull request changed
// nothing. The watcher must read GitHub again before making that claim.
// The same behavior covers modeled projects and plumbing plans; a failed
// fresh read defers the verdict, while a truly unmerged empty PR still
// cannot merge. None of these cases replaces the real scope checker.
func TestAnEmptyScopeRechecksThePullRequest434(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		plumbing, merged, fail bool
	}{
		{name: "a modeled project merges during the step", merged: true},
		{name: "a plumbing plan merges during the step", plumbing: true, merged: true},
		{name: "the fresh pull request read fails", merged: true, fail: true},
		{name: "an empty unmerged pull request stays out of scope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, built := scopeRace434Rig(t, tc.plumbing)
			n := built.Marker.PR
			r.gh.ci(n, "success")
			before := len(r.gh.posts(1))
			hub := &scopeRace434Hub{GitHub: r.f.GitHub}
			readFailure := errors.New("GET pull request: temporarily unavailable (HTTP 502)")
			if tc.fail {
				hub.readError = readFailure
			}
			repo := &scopeRace434Repo{Repo: r.f.Repo, r: r, hub: hub, pr: n, merged: tc.merged}
			r.f.GitHub, r.f.Repo = hub, repo

			err := r.f.Poll(context.Background())
			if !repo.fired {
				t.Fatal("the step did not reach its scope check")
			}
			if hub.readsAfter == 0 {
				t.Error("the empty diff did not trigger a fresh pull request read")
			}
			if tc.fail {
				if !errors.Is(err, readFailure) {
					t.Errorf("fresh-read error = %v; want %v", err, readFailure)
				}
				if got := len(r.gh.posts(1)); got != before {
					t.Fatalf("a failed fresh read posted %d verdicts; want none:\n%s", got-before, r.gh.last(1).Comment.Body)
				}
				r.expect(1, KindPR, LabelPR)
				// Once GitHub answers again, the next poll records the
				// person's merge without requiring a writer to retry.
				hub.readError = nil
				r.poll()
			} else if err != nil {
				t.Fatal(err)
			}

			if tc.merged {
				merged := r.expect(1, KindMerged, LabelMerged)
				visible, _, _ := strings.Cut(merged.Comment.Body, "<!-- invariant:")
				if !strings.Contains(visible, fmt.Sprintf("#%d was merged", n)) || strings.Contains(visible, "I merged") {
					t.Errorf("the post must attribute the merge to a person:\n%s", visible)
				}
				for _, post := range r.gh.posts(1)[before:] {
					if post.Marker.Kind == KindFailed || strings.Contains(post.Comment.Body, "it changes nothing") {
						t.Errorf("a merge was falsely reported out of scope:\n%s", post.Comment.Body)
					}
				}
			} else {
				failed := r.expect(1, KindFailed, LabelHumanReview)
				if !strings.Contains(failed.Comment.Body, "it changes nothing") {
					t.Errorf("an unmerged empty pull request must still be refused:\n%s", failed.Comment.Body)
				}
			}
			if len(r.gh.merged) != 0 {
				t.Errorf("the factory attempted its own merge: %v", r.gh.merged)
			}
			posts := len(r.gh.posts(1))
			r.poll()
			if len(r.gh.posts(1)) != posts {
				t.Error("the final verdict was posted again on the next poll")
			}
		})
	}
}
