package factory

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/github"
)

type heldRefusal434GitHub struct {
	*draftGitHub
	reportFailures int
	labelFailures  int
}

func (g *heldRefusal434GitHub) PostComment(ctx context.Context, n int, body string) (github.Comment, error) {
	if m, ok := DecodeMarker(body); ok && m.Kind == KindFailed && m.Failure == FailMerge && g.reportFailures > 0 {
		g.reportFailures--
		return github.Comment{}, fmt.Errorf("POST comment: Service Unavailable (HTTP 503)")
	}
	return g.draftGitHub.PostComment(ctx, n, body)
}

func (g *heldRefusal434GitHub) AddLabels(ctx context.Context, n int, labels ...string) error {
	for _, label := range labels {
		if label == LabelHumanReview && g.labelFailures > 0 {
			g.labelFailures--
			return fmt.Errorf("POST labels: Forbidden (HTTP 403)")
		}
	}
	return g.draftGitHub.AddLabels(ctx, n, labels...)
}

// A failure post reports a refused merge; it does not release its hold.
// Older commands, a reader and a closed issue cannot give new consent.
func TestMergeRefusal434KeepsHoldUntilNewWriterRetry(t *testing.T) {
	for _, draft := range []bool{false, true} {
		for _, closed := range []bool{false, true} {
			for _, lostReport := range []bool{false, true} {
				t.Run(fmt.Sprintf("draft=%v/closed=%v/lost-report=%v", draft, closed, lostReport), func(t *testing.T) {
					var r *rig
					var g *draftGitHub
					var n int
					if draft {
						r, g, n = watchedDraft(t)
					} else {
						r, g = draftRig(t)
						n = ratified(t, r).Marker.PR
					}
					const message = "installation refuses this operation (HTTP 422)"
					operation := fmt.Sprintf("merge #%d", n)
					if draft {
						g.unready = message
						operation = fmt.Sprintf("ready #%d", n)
					} else {
						g.refuse = message
					}
					hub := &heldRefusal434GitHub{draftGitHub: g}
					if lostReport {
						hub.reportFailures, hub.labelFailures = 1, 1
					}
					r.f.GitHub = hub
					r.gh.ci(n, "success")
					r.gh.say(1, "gitdek", "/invariant retry")
					builds := len(r.build.built)
					effects434Poll(t, r)
					if g.asked(operation) != 1 {
						t.Fatalf("fixture must reach one refusal: %v", g.calls)
					}
					if closed {
						r.gh.issues[1].State = "closed"
					}
					publication434Restart(t, r)
					for i := 0; i < 3; i++ {
						r.poll()
					}
					r.gh.say(1, "mallory", "/invariant retry")
					r.poll()
					if g.asked(operation) != 1 || len(r.gh.merged) != 0 || len(r.build.built) != builds || len(effects434Reports(r, 1, message)) != 1 {
						t.Fatalf("the refusal lost its hold: calls=%v merges=%v builds=%d reports=%d", g.calls, r.gh.merged, len(r.build.built), len(effects434Reports(r, 1, message)))
					}
					r.expect(1, KindFailed, LabelHumanReview)
					g.refuse, g.unready = "", ""
					r.gh.at = "2026-09-25T12:00:00Z"
					retry := r.gh.say(1, "gitdek", "/invariant retry")
					r.poll()
					if closed {
						r.expect(1, KindClosed)
						if r.gh.prs[n].State != "open" || g.asked(operation) != 1 || len(r.gh.closed) != 0 {
							t.Fatalf("retry on a closed issue resumed its PR: calls=%v PR=%+v", g.calls, r.gh.prs[n])
						}
					} else {
						watched := r.expect(1, KindPR, LabelPR)
						if !reflect.DeepEqual(watched.Marker.ReplyTo, []int64{retry.ID}) || watched.Marker.RetriedAt != retry.CreatedAt {
							t.Fatalf("the protocol must answer the new retry: %+v", watched.Marker)
						}
						r.poll()
						r.expect(1, KindMerged, LabelMerged)
						if g.asked(operation) != 2 || len(r.gh.merged) != 1 {
							t.Fatalf("the new retry must finish once: calls=%v merges=%v", g.calls, r.gh.merged)
						}
					}
					posts := len(r.gh.posts(1))
					r.poll()
					if len(r.gh.posts(1)) != posts && closed {
						t.Fatal("the final closure was posted twice")
					}
					if len(effects434Reports(r, 1, message)) != 1 || len(r.build.built) != builds {
						t.Fatal("retry repeated the report or the completed build")
					}
				})
			}
		}
	}
}

type autoClosedMerge434GitHub struct {
	GitHub
	issue *github.Issue
	crash bool
	calls int
}

func (g *autoClosedMerge434GitHub) Merge(ctx context.Context, n int, sha, method string) (string, error) {
	g.calls++
	merged, err := g.GitHub.Merge(ctx, n, sha, method)
	if err != nil {
		return merged, err
	}
	g.issue.State = "closed"
	if g.crash {
		panic(errCrash)
	}
	return "", badGateway
}

// GitHub's auto-closure must not hide the merge record when the factory
// stopped after the merge, or only lost GitHub's answer to its request.
func TestAutoClosedMerge434RecoversFactoryAttribution(t *testing.T) {
	for _, plumbing := range []bool{false, true} {
		for _, crash := range []bool{false, true} {
			t.Run(fmt.Sprintf("plumbing=%v/crash=%v", plumbing, crash), func(t *testing.T) {
				r, _ := publication434Proposal(t, plumbing)
				r.poll()
				opened := r.expect(1, KindPR, LabelPR)
				n := opened.Marker.PR
				r.gh.ci(n, "success")
				g := &autoClosedMerge434GitHub{GitHub: r.f.GitHub, issue: r.gh.issues[1], crash: crash}
				r.f.GitHub = g
				before := len(r.gh.posts(1))
				if err := r.f.Poll(context.Background()); err == nil {
					t.Fatal("fixture must interrupt the step after the merge")
				}
				if g.calls != 1 || !r.gh.prs[n].Merged || g.issue.State != "closed" || len(r.gh.posts(1)) != before {
					t.Fatalf("fixture must merge and auto-close before posting: calls=%d PR=%+v issue=%s posts=%d", g.calls, r.gh.prs[n], g.issue.State, len(r.gh.posts(1))-before)
				}
				publication434Restart(t, r)
				r.poll()
				merged := r.expect(1, KindMerged, LabelMerged)
				if !strings.Contains(merged.Comment.Body, "I merged it as "+r.gh.prs[n].MergeCommitSHA) || merged.Marker.Numbers == nil || merged.Marker.Numbers.PeopleComments == 0 {
					t.Fatalf("recovery lost factory attribution, SHA or completion numbers:\n%s", merged.Comment.Body)
				}
				for i := 0; i < 3; i++ {
					r.poll()
				}
				if g.calls != 1 || len(r.gh.merged) != 1 || len(r.gh.deleted) != 1 || len(r.gh.posts(1)) != before+1 {
					t.Fatalf("recovery repeated completed work: calls=%d merges=%v deleted=%v posts=%d", g.calls, r.gh.merged, r.gh.deleted, len(r.gh.posts(1))-before)
				}
			})
		}
	}
}

// Even an externally merged PR cannot erase a closed issue's pending
// refusal. After consent, the refused attempt's record is not attribution.
func TestClosedMergeRefusal434KeepsHoldAfterAPersonMerges(t *testing.T) {
	r, g := draftRig(t)
	n := ratified(t, r).Marker.PR
	const message = "merge refused before a person's merge (HTTP 405)"
	g.refuse = message
	r.gh.ci(n, "success")
	r.poll()
	r.expect(1, KindFailed, LabelHumanReview)
	pr := r.gh.prs[n]
	pr.Merged, pr.State, pr.MergeCommitSHA = true, "closed", "human-merge-after-refusal"
	r.gh.issues[1].State = "closed"
	publication434Restart(t, r)
	for i := 0; i < 3; i++ {
		r.poll()
		r.expect(1, KindFailed, LabelHumanReview)
	}
	r.gh.say(1, "gitdek", "/invariant retry")
	r.poll()
	merged := r.expect(1, KindMerged, LabelMerged)
	if !strings.Contains(merged.Comment.Body, fmt.Sprintf("#%d was merged", n)) || strings.Contains(merged.Comment.Body, "I merged") || merged.Marker.Numbers != nil {
		t.Fatalf("the refused attempt must not claim the person's merge:\n%s", merged.Comment.Body)
	}
	posts := len(r.gh.posts(1))
	r.poll()
	if len(r.gh.posts(1)) != posts || len(effects434Reports(r, 1, message)) != 1 || g.asked(fmt.Sprintf("merge #%d", n)) != 1 || len(r.gh.deleted) != 0 {
		t.Fatalf("reconciliation repeated refused or completed work: calls=%v deleted=%v posts=%d", g.calls, r.gh.deleted, len(r.gh.posts(1))-posts)
	}
}
