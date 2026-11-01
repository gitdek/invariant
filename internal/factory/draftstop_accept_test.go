package factory

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/factory/recovery/recovery"
	"github.com/gitdek/invariant/internal/github"
)

// #371: a person stops the factory merging a pull request a writer's retry
// has it watch by turning it into a draft. The factory's answer to a retry
// records whether the pull request was a draft or ready, and when the
// writer retried, and the factory marks ready only a draft a retry watched
// as that draft: one the answer saw as a draft, which GitHub shows no one
// has turned into a draft since the retry (#389's review). Any other draft
// waits for a person, and the factory says so once.

// The times GitHub gives the comments and events in these tests, in order.
const (
	earlierAt   = "2026-09-25T10:50:00Z"
	retriedAt   = "2026-09-25T10:55:00Z"
	stoppedAt   = "2026-09-25T10:58:00Z"
	answeredAt  = "2026-09-25T11:00:00Z"
	madeReadyAt = "2026-09-25T11:04:00Z"
	draftedAt   = "2026-09-25T11:05:00Z"
	laterAt     = "2026-09-25T11:10:00Z"
)

// madeADraft is what the factory says of a draft someone made after the
// retry its answer watches, before or after the answer.
const madeADraft = "someone turned it into a draft after the retry that has me watch it"

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

// mergesOnRetry has a writer retry draft n at laterAt, after it was made a
// draft, and checks that the factory watches it as a draft, then marks it
// ready and merges it.
func mergesOnRetry(t *testing.T, r *rig, g *draftGitHub, n int) {
	t.Helper()
	r.gh.at = laterAt
	calls := len(g.calls)
	r.gh.say(1, "gitdek", "/invariant retry")
	pollOK(t, r, "after a writer said retry")
	again := r.expect(1, KindPR, LabelPR)
	if again.Marker.Watched != WatchedDraft || again.Marker.RetriedAt != laterAt ||
		!strings.Contains(retry263Visible(again.Comment.Body), fmt.Sprintf("to stop me, close #%d or this issue", n)) {
		t.Fatalf("the retry's answer must record #%d as a draft, retried at %s, that it will mark ready: %+v\n%s", n, laterAt, again.Marker, again.Comment.Body)
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
	waitsForAPerson(t, r, g, n, madeADraft)
	mergesOnRetry(t, r, g, n)
}

// #389's review: a person who turns a ready pull request into a draft after
// a writer's retry, and before the factory answers it, as when the watcher
// is busy or away, stops the factory too. The answer records the draft and
// when the writer retried, and says the factory won't mark the draft ready.
// Once CI's gate passes, the factory says once that someone turned it into
// a draft after the retry, and a writer's retry after that, which comes
// after the draft was made, has it watch the draft and merge it.
func TestADraftMadeBetweenARetryAndItsAnswerStopsTheFactory(t *testing.T) {
	r, g := draftRig(t)
	r.gh.at = earlierAt
	n := ratified(t, r).Marker.PR
	r.expect(1, KindPR, LabelPR)
	r.gh.ci(n, "failure")
	pollOK(t, r, "after CI's gate failed")
	r.expect(1, KindFailed, LabelHumanReview)
	r.gh.at = retriedAt
	r.gh.say(1, "gitdek", "/invariant retry")
	r.gh.toDraft(n, "gitdek", stoppedAt)
	r.gh.at = answeredAt
	pollOK(t, r, "when the factory answers the retry, after a person turned the pull request into a draft")
	answer := r.expect(1, KindPR, LabelPR)
	if answer.Marker.Watched != WatchedDraft || answer.Marker.RetriedAt != retriedAt || answer.Comment.CreatedAt != answeredAt ||
		!strings.Contains(retry263Visible(answer.Comment.Body), "someone turned it into a draft after the retry i'm answering, so i won't mark it ready or merge it") {
		t.Fatalf("the answer must record the draft and the retry's time, and say the factory won't mark it ready: %+v\n%s", answer.Marker, answer.Comment.Body)
	}
	r.gh.ci(n, "success")
	pollOK(t, r, "after CI's gate passed on the draft")
	if len(g.calls) != 0 || len(r.gh.merged) != 0 {
		t.Fatalf("the factory asked GitHub for %v, and merged %v; it must mark nothing ready", g.calls, r.gh.merged)
	}
	waitsForAPerson(t, r, g, n, madeADraft)
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
			r.gh.toDraft(n, "gitdek", earlierAt)
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
		waitsForAPerson(t, r, g, n, madeADraft)
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
		waitsForAPerson(t, r, g, n, madeADraft)
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
// request, and GitHub's record of who turned it into a draft since the
// retry that answer answers, and the recovery core's step is built from
// that: the core marks ready, or reports on, only a draft the latest answer
// watched as that draft (#371, #389's review).
func TestOnlyADraftARetryWatchedAsThatDraftIsTheCoresWatchedDraft(t *testing.T) {
	const n = 101
	// answer is the factory's answer, made at laterAt, to a retry made at
	// retried, which saw the pull request as watched.
	answer := func(watched, retried string) Post {
		return Post{Comment: github.Comment{ID: 1, CreatedAt: laterAt}, Marker: Marker{Kind: KindPR, PR: n, ReplyTo: []int64{7}, Watched: watched, RetriedAt: retried}}
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
		{"an answer for another pull request", []Post{{Comment: github.Comment{CreatedAt: answeredAt}, Marker: Marker{Kind: KindPR, PR: n + 1, ReplyTo: []int64{7}, Watched: WatchedDraft, RetriedAt: retriedAt}}}, nil, watchNone},
		{"an answer from before answers recorded what they watched", []Post{built, answer("", "")}, nil, watchUnknown},
		{"an answer that watched it ready", []Post{built, answer(WatchedReady, retriedAt)}, drafted(draftedAt), watchReady},
		{"a later answer that watched it ready", []Post{answer(WatchedDraft, earlierAt), failed, answer(WatchedReady, retriedAt)}, nil, watchReady},
		{"a later answer that watched it as a draft", []Post{answer(WatchedReady, earlierAt), failed, answer(WatchedDraft, laterAt)}, drafted(draftedAt), watchDraft},
		{"a draft no one turned into one since", []Post{answer(WatchedDraft, retriedAt)}, nil, watchDraft},
		{"a draft made ready, but not drafted since", []Post{answer(WatchedDraft, retriedAt)}, []github.Event{{Event: "ready_for_review", CreatedAt: draftedAt}}, watchDraft},
		{"a draft someone made before the retry", []Post{answer(WatchedDraft, retriedAt)}, drafted(earlierAt), watchDraft},
		{"a draft someone made after the retry, before its answer", []Post{answer(WatchedDraft, retriedAt)}, drafted(stoppedAt), watchRedrafted},
		{"a draft someone made again after the answer", []Post{answer(WatchedDraft, retriedAt)}, drafted(draftedAt), watchRedrafted},
		{"a draft someone made in the second the writer retried", []Post{answer(WatchedDraft, retriedAt)}, drafted(retriedAt), watchRedrafted},
		{"a draft made at a time that doesn't read", []Post{answer(WatchedDraft, retriedAt)}, drafted("yesterday"), watchRedrafted},
		{"an answer whose retry's time doesn't read", []Post{answer(WatchedDraft, "")}, drafted(earlierAt), watchRedrafted},
		{"an answer whose retry's time doesn't read, of a draft no one made", []Post{answer(WatchedDraft, "")}, nil, watchDraft},
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

// A retry of a draft is a second watch only when the latest answer watched
// it as that draft, and its answer says the factory won't mark it ready when
// someone made it a draft at or after the retry. When GitHub doesn't show
// the draft's events, the retry is still answered, as a watch of the draft:
// the factory reads them again before it marks the draft ready (#389's
// review).
func TestARetryOfADraftGoesByTheRetrysTime(t *testing.T) {
	const n = 101
	watchedAt := Post{Comment: github.Comment{ID: 1, CreatedAt: answeredAt}, Marker: Marker{Kind: KindPR, PR: n, ReplyTo: []int64{7}, Watched: WatchedDraft, RetriedAt: retriedAt}}
	drafted := []github.Event{{Event: "convert_to_draft", CreatedAt: stoppedAt}}
	draft := github.PullRequest{Number: n, State: "open", Draft: true}
	for _, c := range []struct {
		name             string
		posts            []Post
		events           []github.Event
		fail             bool
		at               string
		watched, stopped bool
	}{
		{"a first watch of a draft made before the retry", nil, drafted, false, laterAt, false, false},
		{"a first watch of a draft made after the retry", nil, drafted, false, retriedAt, false, true},
		{"a second watch of the same draft", []Post{watchedAt}, nil, false, laterAt, true, false},
		{"a watch of a draft made again since the last answer's retry", []Post{watchedAt}, drafted, false, laterAt, false, false},
		{"a watch while GitHub doesn't show the events", []Post{watchedAt}, nil, true, laterAt, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			gh := newFakeGitHub(t, "")
			gh.events[n] = c.events
			var lines logLines
			lines.t = t
			f := &Factory{GitHub: gh, Log: lines.add}
			if c.fail {
				f.GitHub = &eventsGitHub{draftGitHub: &draftGitHub{fakeGitHub: gh}, n: n, err: badGateway}
			}
			watched, stopped := f.retriedDraft(context.Background(), Thread{Issue: github.Issue{Number: 1}, Posts: c.posts}, draft, Command{Verb: Retry, At: c.at})
			if watched != c.watched || stopped != c.stopped {
				t.Fatalf("retriedDraft = %v, %v; want %v, %v", watched, stopped, c.watched, c.stopped)
			}
			if logged := strings.Join(lines.all(), "\n"); c.fail != strings.Contains(logged, "didn't show #101's events") {
				t.Errorf("the factory must say in its log when GitHub doesn't show the events, and only then:\n%s", logged)
			}
		})
	}
}

// eventsGitHub is GitHub answering err each time the factory reads pull
// request n's events.
type eventsGitHub struct {
	*draftGitHub
	n   int
	err error
}

func (g *eventsGitHub) Events(ctx context.Context, n int) ([]github.Event, error) {
	if n == g.n && g.err != nil {
		return nil, fmt.Errorf("GET repos/o/r/issues/%d/events?per_page=100: %w", n, g.err)
	}
	return g.draftGitHub.Events(ctx, n)
}

// GitHub failing for a moment to show a watched draft's events is asked
// again on each poll, under #376's bound, and past it the factory says once
// that it can't tell whether someone made the draft after the retry, quoting
// GitHub, and marks nothing ready. GitHub refusing to show them is said at
// once. Either way, a writer's retry has the factory watch the draft again,
// and answers it even while GitHub still doesn't show the events (#389's
// review).
func TestADraftsEventsGitHubDoesntShowAreSaidOnce(t *testing.T) {
	cant := "i can't tell whether someone turned it into one after the retry that has me watch it"
	t.Run("GitHub failing for a moment", func(t *testing.T) {
		r, g, n := watchedDraftAt(t)
		now := clockOn(r.f)
		down := ghSaid("gh: HTTP 502: Bad Gateway (https://api.github.com/repos/o/r/issues/101/events?per_page=100)")
		eg := &eventsGitHub{draftGitHub: g, n: n, err: down}
		r.f.GitHub = eg
		r.gh.ci(n, "success")
		failsAgain(t, r, now, 0, 10*time.Minute, 20*time.Minute, 30*time.Minute)
		*now = clockStart.Add(31 * time.Minute)
		pollOK(t, r, "31 minutes after GitHub first failed to show the draft's events")
		keptFailingSaid(t, r, FailDraft, n, cant, "31 minutes", "HTTP 502: Bad Gateway")
		if len(g.calls) != 0 || len(r.gh.merged) != 0 || !r.gh.prs[n].Draft {
			t.Fatalf("the factory must mark nothing ready: asked GitHub for %v, merged %v", g.calls, r.gh.merged)
		}
		waitsForAPerson(t, r, g, n, cant)
		r.gh.at = laterAt
		r.gh.say(1, "gitdek", "/invariant retry")
		pollOK(t, r, "after a writer said retry, while GitHub still doesn't show the events")
		if again := r.expect(1, KindPR, LabelPR); again.Marker.Watched != WatchedDraft || again.Marker.RetriedAt != laterAt {
			t.Fatalf("the retry must be answered as a watch of the draft: %+v", again.Marker)
		}
		eg.err = nil
		pollOK(t, r, "once GitHub shows the events again")
		r.expect(1, KindMerged, LabelMerged)
		if want := []string{fmt.Sprintf("ready #%d", n), fmt.Sprintf("merge #%d", n)}; !reflect.DeepEqual(g.calls, want) {
			t.Errorf("the factory asked GitHub for %v; want %v", g.calls, want)
		}
	})
	t.Run("GitHub refusing", func(t *testing.T) {
		r, g, n := watchedDraftAt(t)
		refused := ghSaid("gh: Resource not accessible by integration (HTTP 403)")
		r.f.GitHub = &eventsGitHub{draftGitHub: g, n: n, err: refused}
		r.gh.ci(n, "success")
		pollOK(t, r, "when GitHub refused to show the draft's events")
		said := retry263Visible(r.expect(1, KindFailed, LabelHumanReview).Comment.Body)
		for _, want := range []string{cant, "github refused to show me its events", "resource not accessible by integration (http 403)"} {
			if !strings.Contains(said, want) {
				t.Errorf("the post doesn't say %q:\n%s", want, said)
			}
		}
		waitsForAPerson(t, r, g, n, cant)
	})
}
