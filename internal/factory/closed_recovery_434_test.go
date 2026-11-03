package factory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/github"
)

type closedPublicationHub struct {
	*fakeGitHub
	readError error
}

func (g *closedPublicationHub) PullRequestFrom(ctx context.Context, branch string) (github.PullRequest, bool, error) {
	if g.readError != nil {
		return github.PullRequest{}, false, g.readError
	}
	return g.fakeGitHub.PullRequestFrom(ctx, branch)
}

// A pull request can merge and close its issue before publication's post
// reaches GitHub. The next watcher must find this build's pull request even
// though the thread still says it is building, without running that build.
func TestClosedIssueRecoversUnpostedPullRequest434(t *testing.T) {
	for _, plumbing := range []bool{false, true} {
		for _, outcome := range []string{"merged", "read fails", "open", "earlier build"} {
			t.Run(fmt.Sprintf("plumbing=%v/%s", plumbing, outcome), func(t *testing.T) {
				r, _ := publication434Proposal(t, plumbing)
				r.f.GitHub = stopBeforePRPost{GitHub: r.f.GitHub}
				if err := r.f.Poll(context.Background()); !errors.Is(err, errStoppedBeforePost) {
					t.Fatalf("publication should stop before its post: %v", err)
				}
				building := r.expect(1, KindRatified, LabelBuilding)
				if building.Marker.PR != 0 || len(r.gh.prs) != 1 {
					t.Fatalf("publication must leave one unrecorded PR: %+v, PRs=%d", building.Marker, len(r.gh.prs))
				}
				pr := r.gh.prs[r.gh.nextPR]
				if outcome != "open" {
					pr.Merged, pr.State, pr.MergeCommitSHA = true, "closed", "human-merge"
				}
				if outcome == "earlier build" {
					pr.CreatedAt = "2026-09-24T12:00:00Z"
				}
				r.gh.issues[1].State = "closed"
				g := &closedPublicationHub{fakeGitHub: r.gh}
				r.f.GitHub = g
				publication434Restart(t, r)
				before := len(r.gh.posts(1))
				if outcome == "read fails" {
					g.readError = errors.New("GET pull requests: temporarily unavailable (HTTP 502)")
					if err := r.f.Poll(context.Background()); !errors.Is(err, g.readError) {
						t.Fatalf("a failed branch lookup must defer reconciliation: %v", err)
					}
					if len(r.gh.posts(1)) != before {
						t.Fatal("a failed lookup must not record a closure in place of the merge")
					}
					g.readError = nil
				}
				r.poll()
				if outcome == "merged" || outcome == "read fails" {
					last := r.expect(1, KindMerged, LabelMerged)
					if last.Marker.PR != pr.Number || !strings.Contains(last.Comment.Body, fmt.Sprintf("#%d was merged", pr.Number)) {
						t.Fatalf("the external merge must name the recovered PR: %+v\n%s", last.Marker, last.Comment.Body)
					}
				} else {
					last := r.expect(1, KindNote)
					if !last.Marker.IssueClosed {
						t.Fatal("the closure must be recorded as an observation note")
					}
					if outcome == "earlier build" && last.Marker.PR != 0 {
						t.Fatalf("an earlier build's PR must not be adopted: %+v", last.Marker)
					}
				}
				r.poll()
				builds := len(r.build.built)
				if plumbing {
					builds = len(r.f.Plumbing.(*fakePlanBuilder).built)
				}
				if len(r.gh.posts(1)) != before+1 || builds != 1 || len(r.gh.prs) != 1 || len(r.gh.merged) != 0 || len(r.gh.closed) != 0 || len(r.gh.deleted) != 0 {
					t.Fatalf("reconciliation must only record once: posts=%d, builds=%d, PRs=%d, merges=%v, closes=%v, deletes=%v", len(r.gh.posts(1))-before, builds, len(r.gh.prs), r.gh.merged, r.gh.closed, r.gh.deleted)
				}
			})
		}
	}
}

// Closure is an observation, not a replacement for the proposal, forks or
// failure a writer can act on after reopening. A fresh watcher must retain
// the original command rules and all recorded work.
func TestReopenedIssueAcceptsWriterCommands434(t *testing.T) {
	for _, action := range []string{Ratify, Revise, Choose, Solve, Plan, Retry, "retry pull request"} {
		t.Run(action, func(t *testing.T) {
			r := newRig(t)
			if action != Choose {
				r.form.forks = nil
			}
			if action == Solve {
				r.form.fail = "The first draft did not check out."
			}
			if action == Plan {
				r.f.Formalizer = pipelinePlanner{rest: r.form}
			}
			if action == Retry {
				r.build.stop = true
				ratified(t, r)
			} else if action == "retry pull request" {
				pr := ratified(t, r)
				r.gh.ci(pr.Marker.PR, "failure")
				r.poll()
			} else {
				r.gh.open(1, "gitdek", "A bounded buffer", "/invariant solve")
				r.poll()
			}
			previous := r.gh.last(1)
			drafts, builds := len(r.form.requests), len(r.build.built)
			r.gh.issues[1].State = "closed"
			r.poll()
			closed := r.expect(1, KindNote)
			if !closed.Marker.IssueClosed {
				t.Fatal("the closure must be a note so existing readers retain the working state")
			}
			closedPosts := len(r.gh.posts(1))
			r.poll()
			if len(r.gh.posts(1)) != closedPosts || len(r.form.requests) != drafts || len(r.build.built) != builds {
				t.Fatal("a closed issue must stay stopped with one observation")
			}
			r.f = freshWatcher(r, r.gh)
			r.gh.issues[1].State = "open"
			r.form.fail, r.build.stop = "", false
			command := "/invariant " + action
			want, label := KindProposal, LabelProposal
			switch action {
			case Ratify:
				command += " " + strings.TrimPrefix(previous.Marker.Proposal.Hash, "sha256:")
				want, label = KindPR, LabelPR
			case Choose:
				command += " F1 A"
			case Retry:
				want, label = KindRatified, LabelBuilding
			case "retry pull request":
				command, want, label = "/invariant retry", KindPR, LabelPR
			}
			r.gh.say(1, "mallory", command)
			r.poll()
			if len(r.gh.posts(1)) != closedPosts || len(r.form.requests) != drafts || len(r.build.built) != builds {
				t.Fatal("a reader's command must not resume closed work")
			}
			writer := r.gh.say(1, "gitdek", command)
			r.poll()
			last := r.expect(1, want, label)
			if answersTo(r, writer.ID) != 1 {
				t.Fatal("the writer's command must be answered once")
			}
			switch action {
			case Ratify:
				if last.Marker.Hash != previous.Marker.Proposal.Hash || len(r.build.built) != 1 {
					t.Fatal("ratification must build the original proposal once")
				}
			case Retry:
				if last.Marker.Hash != previous.Marker.Hash {
					t.Fatal("retry must retain the ratified proposal")
				}
				r.poll()
				r.expect(1, KindPR, LabelPR)
				if len(r.build.built) != builds+1 {
					t.Fatal("retry must start exactly one new build")
				}
			case "retry pull request":
				if last.Marker.PR != previous.Marker.PR || len(r.build.built) != builds || len(r.gh.prs) != 1 {
					t.Fatal("retry must watch the original pull request without building again")
				}
			case Plan:
				if last.Marker.Proposal.IssuePlan == nil {
					t.Fatal("the writer's plan command must draft a plan of issues")
				}
			default:
				if len(r.form.requests) != drafts+1 || len(r.build.built) != builds {
					t.Fatal("the command must draft once, without building")
				}
			}
		})
	}
}
