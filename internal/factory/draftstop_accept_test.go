package factory

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/factory/recovery/recovery"
	"github.com/gitdek/invariant/internal/github"
)

// #371: a person stops the factory merging a pull request a writer's retry
// has it watch by turning it into a draft. The factory's answer to a retry
// records whether the pull request was a draft or ready, and the factory
// marks ready only a draft a retry watched as that draft: one the answer saw
// as a draft, which GitHub shows no one has turned into a draft since. Any
// other draft waits for a person, and the factory says so once.

// The times GitHub gives the comments and events in these tests, in order.
const (
	beforeAnswer = "2026-09-25T10:55:00Z"
	answeredAt   = "2026-09-25T11:00:00Z"
	madeReadyAt  = "2026-09-25T11:04:00Z"
	draftedAt    = "2026-09-25T11:05:00Z"
	laterAt      = "2026-09-25T11:10:00Z"
)

// toDraft turns pull request n into a draft, as a person does on GitHub,
// which records the event at at.
func (g *fakeGitHub) toDraft(n int, by, at string) {
	g.prs[n].Draft = true
	g.events[n] = append(g.events[n], github.Event{Event: "convert_to_draft", Actor: github.User{Login: by}, CreatedAt: at})
}

// toReady marks pull request n ready for review, as a person does on
// GitHub, which records the event at at.
func (g *fakeGitHub) toReady(n int, by, at string) {
	g.prs[n].Draft = false
	g.events[n] = append(g.events[n], github.Event{Event: "ready_for_review", Actor: github.User{Login: by}, CreatedAt: at})
}

// waitsForAPerson checks that the factory said once, as a draft no retry
// watches, that it won't mark draft n ready, in the words why gives, and
// that it asks GitHub for nothing and posts nothing more on the polls
// after.
func waitsForAPerson(t *testing.T, r *rig, g *draftGitHub, n int, why string) {
	t.Helper()
	said := r.expect(1, KindFailed, LabelHumanReview)
	body := retry263Visible(said.Comment.Body)
	if said.Marker.Why() != FailDraft || said.Marker.PR != n || !strings.Contains(body, why) || !strings.Contains(body, "it stays stopped") ||
		!strings.Contains(body, fmt.Sprintf("to have me watch #%d again", n)) {
		t.Fatalf("the factory must say once that it won't mark #%d ready, since %s: %+v\n%s", n, why, said.Marker, said.Comment.Body)
	}
	calls := len(g.calls)
	if len(r.gh.merged) != 0 || !r.gh.prs[n].Draft {
		t.Fatalf("#%d must stay an unmerged draft: merged %v, draft %v", n, r.gh.merged, r.gh.prs[n].Draft)
	}
	posts := len(r.gh.posts(1))
	for i := 1; i <= 3; i++ {
		pollOK(t, r, fmt.Sprintf("%d polls after it said so", i))
	}
	if len(r.gh.posts(1)) != posts || len(g.calls) != calls || len(r.gh.merged) != 0 || !r.gh.prs[n].Draft {
		t.Fatalf("the draft must stay a person's: %d more posts, asked GitHub for %v, merged %v", len(r.gh.posts(1))-posts, g.calls[calls:], r.gh.merged)
	}
}

// mergesOnRetry has a writer retry draft n at laterAt, and checks that the
// factory watches it as a draft, then marks it ready and merges it.
func mergesOnRetry(t *testing.T, r *rig, g *draftGitHub, n int) {
	t.Helper()
	r.gh.at = laterAt
	calls := len(g.calls)
	r.gh.say(1, "gitdek", "/invariant retry")
	pollOK(t, r, "after a writer said retry")
	if again := r.expect(1, KindPR, LabelPR); again.Marker.Watched != WatchedDraft {
		t.Fatalf("the retry's answer must record #%d as a draft: %+v", n, again.Marker)
	}
	pollOK(t, r, "in the poll after the retry's answer")
	r.expect(1, KindMerged, LabelMerged)
	if want := []string{fmt.Sprintf("ready #%d", n), fmt.Sprintf("merge #%d", n)}; !reflect.DeepEqual(g.calls[calls:], want) {
		t.Errorf("the factory asked GitHub for %v; want %v", g.calls[calls:], want)
	}
}

// The issue's sequence: the factory opens a ready pull request, CI's gate
// fails on it, a writer's retry has the factory watch it again, and a
// person turns it into a draft to stop it. Once CI's gate passes on its
// head, the factory says once that it won't mark the draft ready, and the
// issue leaves the pull request's state, so a writer's retry has it watch
// the draft and merge it after all.
func TestAPersonStopsAWatchedPullRequestByTurningItIntoADraft(t *testing.T) {
	r, g := draftRig(t)
	r.gh.at = answeredAt
	n := ratified(t, r).Marker.PR
	r.expect(1, KindPR, LabelPR)
	if r.gh.prs[n].Draft {
		t.Fatalf("a build that passed should open #%d ready", n)
	}
	r.gh.ci(n, "failure")
	pollOK(t, r, "after CI's gate failed")
	if failed := r.expect(1, KindFailed, LabelHumanReview); failed.Marker.Why() != FailCI {
		t.Fatalf("CI's gate failed on #%d: %+v", n, failed.Marker)
	}
	r.gh.say(1, "gitdek", "/invariant retry")
	pollOK(t, r, "after a writer said retry")
	answer := r.expect(1, KindPR, LabelPR)
	if answer.Marker.Watched != WatchedReady || !strings.Contains(retry263Visible(answer.Comment.Body), fmt.Sprintf("to stop me, turn #%d into a draft", n)) {
		t.Fatalf("the retry's answer must record #%d ready, and say a draft stops the factory: %+v\n%s", n, answer.Marker, answer.Comment.Body)
	}

	r.gh.toDraft(n, "gitdek", draftedAt)
	r.gh.ci(n, "success")
	pollOK(t, r, "after CI's gate passed on the draft a person made")
	if len(g.calls) != 0 {
		t.Fatalf("the factory asked GitHub for %v; it must mark nothing ready", g.calls)
	}
	waitsForAPerson(t, r, g, n, "someone turned it into a draft after i said i'd watch it")
	mergesOnRetry(t, r, g, n)
}

// A draft a writer's retry watched as a draft still merges once CI's gate
// passes: a failed build's draft, and a ready pull request a person turned
// into a draft before the retry's answer, which watched it as that draft.
func TestADraftARetryWatchedAsADraftStillMerges(t *testing.T) {
	for _, c := range []struct {
		name string
		open func(t *testing.T, r *rig) int
	}{
		{"a failed build's draft", func(t *testing.T, r *rig) int {
			r.build.pass = false
			return ratified(t, r).Marker.PR
		}},
		{"a draft a person made before the retry", func(t *testing.T, r *rig) int {
			n := ratified(t, r).Marker.PR
			r.gh.ci(n, "failure")
			pollOK(t, r, "after CI's gate failed")
			r.expect(1, KindFailed, LabelHumanReview)
			r.gh.toDraft(n, "gitdek", beforeAnswer)
			return n
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, g := draftRig(t)
			r.gh.at = answeredAt
			n := c.open(t, r)
			r.gh.say(1, "gitdek", "/invariant retry")
			pollOK(t, r, "after a writer said retry")
			answer := r.expect(1, KindPR, LabelPR)
			if answer.Marker.Watched != WatchedDraft || !strings.Contains(retry263Visible(answer.Comment.Body), fmt.Sprintf("to stop me, close #%d or this issue", n)) {
				t.Fatalf("the retry's answer must record #%d as a draft, and say how to stop the factory: %+v\n%s", n, answer.Marker, answer.Comment.Body)
			}
			r.gh.ci(n, "success")
			pollOK(t, r, "after CI's gate passed on the watched draft")
			r.expect(1, KindMerged, LabelMerged)
			if want := []string{fmt.Sprintf("ready #%d", n), fmt.Sprintf("merge #%d", n)}; !reflect.DeepEqual(g.calls, want) {
				t.Errorf("the factory asked GitHub for %v; want %v", g.calls, want)
			}
		})
	}
}

// An answer from before answers recorded what they watched counts as
// watching no draft: the factory can't tell the draft is the one the retry
// watched, so a person decides, and a writer's retry has it watch the draft
// after all.
func TestAnAnswerThatRecordsNothingWatchesNoDraft(t *testing.T) {
	r, g := draftRig(t)
	r.gh.at = answeredAt
	r.build.pass = false
	n := ratified(t, r).Marker.PR
	r.gh.say(1, "gitdek", "/invariant retry")
	pollOK(t, r, "after a writer said retry")
	answer := r.expect(1, KindPR, LabelPR)
	old := answer.Marker
	old.Watched = ""
	for i, c := range r.gh.comments[1] {
		if c.ID == answer.Comment.ID {
			r.gh.comments[1][i].Body = markerRE.ReplaceAllLiteralString(c.Body, old.encode())
		}
	}
	if m := r.gh.last(1).Marker; m.Kind != KindPR || m.Watched != "" || len(m.ReplyTo) == 0 {
		t.Fatalf("the answer must read as one from before answers recorded what they watched: %+v", m)
	}
	r.gh.ci(n, "success")
	pollOK(t, r, "after CI's gate passed on the draft")
	if len(g.calls) != 0 {
		t.Fatalf("the factory asked GitHub for %v; it must mark nothing ready", g.calls)
	}
	waitsForAPerson(t, r, g, n, "i answered the retry that has me watch it before i recorded whether i watched a draft")
	mergesOnRetry(t, r, g, n)
}

// A watched draft someone turned into a draft again since the factory's
// answer, after it was made ready, by a person or by the factory as it
// began a merge that a push held up, is a person's draft too: the factory
// says once that it won't mark it ready.
func TestAWatchedDraftSomeoneDraftedAgainWaitsForAPerson(t *testing.T) {
	t.Run("after a person made it ready", func(t *testing.T) {
		r, g, n := watchedDraftAt(t)
		r.gh.toReady(n, "gitdek", madeReadyAt)
		r.gh.toDraft(n, "gitdek", draftedAt)
		r.gh.ci(n, "success")
		pollOK(t, r, "after CI's gate passed on the draft a person made again")
		if len(g.calls) != 0 {
			t.Fatalf("the factory asked GitHub for %v; it must mark nothing ready", g.calls)
		}
		waitsForAPerson(t, r, g, n, "someone turned it into a draft after i said i'd watch it")
		mergesOnRetry(t, r, g, n)
	})
	t.Run("after the factory made it ready", func(t *testing.T) {
		r, g, n := watchedDraftAt(t)
		p := &readyPush{draftGitHub: g, t: t, origin: r.origin, branch: r.gh.prs[n].Head.Ref}
		r.f.GitHub = p
		r.gh.ci(n, "success")
		pollOK(t, r, "when someone pushes to the draft as the factory marks it ready")
		r.f.GitHub = g
		if p.pushed == "" || r.gh.prs[n].Draft || len(r.gh.merged) != 0 {
			t.Fatalf("the push must hold up the merge, with #%d ready: pushed %q, draft %v, merged %v", n, p.pushed, r.gh.prs[n].Draft, r.gh.merged)
		}
		r.expect(1, KindPR, LabelPR)
		r.gh.toDraft(n, "gitdek", draftedAt)
		r.gh.ci(n, "success")
		pollOK(t, r, "after CI's gate passed on the new head of the draft a person made")
		if want := []string{fmt.Sprintf("ready #%d", n)}; !reflect.DeepEqual(g.calls, want) {
			t.Fatalf("the factory asked GitHub for %v; want only %v", g.calls, want)
		}
		waitsForAPerson(t, r, g, n, "someone turned it into a draft after i said i'd watch it")
	})
}

// watchedDraftAt has a writer retry a failed build's draft at answeredAt,
// which the factory watches again as a draft, and returns the draft's
// number.
func watchedDraftAt(t *testing.T) (*rig, *draftGitHub, int) {
	t.Helper()
	r, g := draftRig(t)
	r.gh.at = answeredAt
	r.build.pass = false
	n := ratified(t, r).Marker.PR
	r.gh.say(1, "gitdek", "/invariant retry")
	pollOK(t, r, "after a writer said retry")
	if answer := r.expect(1, KindPR, LabelPR); answer.Marker.Watched != WatchedDraft {
		t.Fatalf("the retry's answer must record #%d as a draft: %+v", n, answer.Marker)
	}
	return r, g, n
}

// The factory goes by its latest answer to a retry that watches the pull
// request, and GitHub's record of who turned it into a draft since, and the
// recovery core's step is built from that: the core marks ready, or reports
// on, only a draft the latest answer watched as that draft (#371).
func TestOnlyADraftARetryWatchedAsThatDraftIsTheCoresWatchedDraft(t *testing.T) {
	const n = 101
	answer := func(watched string, at string) Post {
		return Post{Comment: github.Comment{ID: 1, CreatedAt: at}, Marker: Marker{Kind: KindPR, PR: n, ReplyTo: []int64{7}, Watched: watched}}
	}
	failed := Post{Comment: github.Comment{ID: 2, CreatedAt: answeredAt}, Marker: Marker{Kind: KindFailed, PR: n, Failure: FailCI}}
	built := Post{Comment: github.Comment{ID: 3, CreatedAt: answeredAt}, Marker: Marker{Kind: KindPR, PR: n}}
	drafted := func(at string) []github.Event {
		return []github.Event{{Event: "convert_to_draft", Actor: github.User{Login: "gitdek"}, CreatedAt: at}}
	}
	for _, c := range []struct {
		name   string
		posts  []Post
		events []github.Event
		want   watchState
	}{
		{"no answer", []Post{built}, nil, watchNone},
		{"an answer for another pull request", []Post{{Comment: github.Comment{CreatedAt: answeredAt}, Marker: Marker{Kind: KindPR, PR: n + 1, ReplyTo: []int64{7}, Watched: WatchedDraft}}}, nil, watchNone},
		{"an answer from before answers recorded what they watched", []Post{built, answer("", answeredAt)}, nil, watchUnknown},
		{"an answer that watched it ready", []Post{built, answer(WatchedReady, answeredAt)}, drafted(draftedAt), watchReady},
		{"a later answer that watched it ready", []Post{answer(WatchedDraft, beforeAnswer), failed, answer(WatchedReady, answeredAt)}, nil, watchReady},
		{"a later answer that watched it as a draft", []Post{answer(WatchedReady, beforeAnswer), failed, answer(WatchedDraft, laterAt)}, drafted(draftedAt), watchDraft},
		{"a draft no one turned into one since", []Post{answer(WatchedDraft, answeredAt)}, nil, watchDraft},
		{"a draft made ready, but not drafted since", []Post{answer(WatchedDraft, answeredAt)}, []github.Event{{Event: "ready_for_review", CreatedAt: draftedAt}}, watchDraft},
		{"a draft someone made before the answer", []Post{answer(WatchedDraft, answeredAt)}, drafted(beforeAnswer), watchDraft},
		{"a draft someone made again since", []Post{answer(WatchedDraft, answeredAt)}, drafted(draftedAt), watchRedrafted},
		{"a draft someone made in the second the factory answered", []Post{answer(WatchedDraft, answeredAt)}, drafted(answeredAt), watchRedrafted},
		{"a draft made at a time that doesn't read", []Post{answer(WatchedDraft, answeredAt)}, drafted("yesterday"), watchRedrafted},
		{"an answer at a time that doesn't read", []Post{answer(WatchedDraft, "")}, drafted(beforeAnswer), watchRedrafted},
	} {
		t.Run(c.name, func(t *testing.T) {
			gh := newFakeGitHub(t, "")
			gh.events[n] = c.events
			f := &Factory{GitHub: gh}
			th := Thread{Posts: c.posts}
			draft := github.PullRequest{Number: n, State: "open", Draft: true}
			got, err := f.watchOf(context.Background(), th, draft)
			if err != nil || got != c.want {
				t.Fatalf("watchOf = %d, %v; want %d", got, err, c.want)
			}
			watched, err := f.alreadyWatched(context.Background(), th, draft)
			if err != nil || watched != (c.want == watchDraft) {
				t.Fatalf("alreadyWatched = %v, %v; want %v", watched, err, c.want == watchDraft)
			}
			if ok := f.canMarkReady(mergingPR(watched, draft, true, false)); ok != watched {
				t.Errorf("the core let the factory mark the draft ready: %v; want %v", ok, watched)
			}
			if ok := f.canPost(mergingPR(watched, draft, false, false), recovery.Merge); ok != watched {
				t.Errorf("the core let the factory report the draft it can't merge: %v; want %v", ok, watched)
			}
			ready := draft
			ready.Draft = false
			if watched, err := f.alreadyWatched(context.Background(), th, ready); err != nil || watched {
				t.Errorf("a ready pull request is no watched draft: %v, %v", watched, err)
			}
		})
	}
}
