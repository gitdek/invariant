package factory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

// #376: GitHub failing for a moment, as the factory marks a draft ready or
// merges, is asked again on the next poll, but only for half an hour from
// the first failure. Then the factory says once that GitHub kept failing,
// what it was trying to do and GitHub's last answer, in the refusal's
// place, labels the issue for a person and asks nothing more, until a
// writer's retry has it watch the pull request again. The count is recorded
// beside the issue's runs, so it holds across polls and watchers, and a new
// post or head starts it afresh.

// failingGitHub is GitHub failing, for a moment, each time the factory marks
// a draft ready or merges: ready and merge are what it answers each with,
// when set. With done, GitHub carries the request out first, and only its
// answer is lost on the way.
type failingGitHub struct {
	*draftGitHub
	ready, merge error
	done         bool
}

func (g *failingGitHub) MarkReady(ctx context.Context, n int) error {
	if g.ready == nil {
		return g.draftGitHub.MarkReady(ctx, n)
	}
	g.calls = append(g.calls, fmt.Sprintf("ready #%d", n))
	if pr, ok := g.prs[n]; ok && g.done {
		pr.Draft = false
	}
	return fmt.Errorf("POST graphql: %w", g.ready)
}

func (g *failingGitHub) Merge(ctx context.Context, n int, sha, method string) (string, error) {
	if g.merge == nil {
		return g.draftGitHub.Merge(ctx, n, sha, method)
	}
	g.calls = append(g.calls, fmt.Sprintf("merge #%d", n))
	if g.done {
		if _, err := g.fakeGitHub.Merge(ctx, n, sha, method); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("PUT repos/o/r/pulls/%d/merge: %w", n, g.merge)
}

// ghSaid is what gh's exit leaves of what it said.
func ghSaid(said string) error { return fmt.Errorf("exit status 1: %s", said) }

// badGateway is GitHub failing for a moment, as gh says it.
var badGateway = ghSaid("gh: HTTP 502: Bad Gateway (https://api.github.com/graphql)")

// failingRig is a rig whose GitHub fails as failingGitHub does, on a clock
// the test moves, which starts at clockStart.
func failingRig(t *testing.T) (*rig, *failingGitHub, *time.Time) {
	t.Helper()
	r, g := draftRig(t)
	return r, failingOn(r, g), clockOn(r.f)
}

// clockStart is when the clock these tests move starts, and when GitHub
// first fails in most of them.
var clockStart = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// clockOn gives f a clock the test moves, from clockStart.
func clockOn(f *Factory) *time.Time {
	now := clockStart
	f.Now = func() time.Time { return now }
	return &now
}

// failingOn has r's GitHub fail as failingGitHub does.
func failingOn(r *rig, g *draftGitHub) *failingGitHub {
	fg := &failingGitHub{draftGitHub: g}
	r.f.GitHub = fg
	return fg
}

// failsAgain polls at each time, after clockStart, and checks that each poll
// fails its step, so the next tries again, and posts nothing.
func failsAgain(t *testing.T, r *rig, now *time.Time, at ...time.Duration) {
	t.Helper()
	posts := len(r.gh.posts(1))
	for _, d := range at {
		*now = clockStart.Add(d)
		if err := r.f.Poll(context.Background()); err == nil || !github.Transient(err) {
			t.Fatalf("%s after GitHub first failed, the poll = %v; want GitHub's failure, so the next poll tries again", d, err)
		}
		if got := len(r.gh.posts(1)); got != posts {
			t.Fatalf("%s after GitHub first failed, the factory posted %d times:\n%s", d, got-posts, r.gh.last(1).Comment.Body)
		}
	}
}

// keptFailingSaid checks the post that says GitHub kept failing: a failure
// of why, naming pull request pr, that says what the factory was doing,
// for how long, quotes GitHub's last answer and says how a retry goes on.
func keptFailingSaid(t *testing.T, r *rig, why string, pr int, doing, lasted, answer string) {
	t.Helper()
	failed := r.expect(1, KindFailed, LabelHumanReview)
	said := retry263Visible(failed.Comment.Body)
	if failed.Marker.Why() != why || failed.Marker.PR != pr {
		t.Errorf("the post records #%d, failed with %q; want #%d, failed with %q", failed.Marker.PR, failed.Marker.Why(), pr, why)
	}
	for _, want := range []string{doing, "it failed each time i asked, for " + lasted, "so i've stopped asking", strings.ToLower(answer), fmt.Sprintf("`/invariant retry` to have me watch #%d again", pr)} {
		if !strings.Contains(said, want) {
			t.Errorf("the post doesn't say %q:\n%s", want, failed.Comment.Body)
		}
	}
}

// stillQuiet polls three more times, later and later, and checks that the
// factory posts nothing more and asks GitHub for nothing more.
func stillQuiet(t *testing.T, r *rig, fg *failingGitHub, now *time.Time) {
	t.Helper()
	posts, calls := len(r.gh.posts(1)), len(fg.calls)
	for i := 1; i <= 3; i++ {
		*now = now.Add(time.Hour)
		pollOK(t, r, fmt.Sprintf("%d hours after it said GitHub kept failing", i))
	}
	if got := len(r.gh.posts(1)); got != posts || len(fg.calls) != calls {
		t.Errorf("after it said GitHub kept failing, the factory posted %d more times and asked GitHub for %v", got-posts, fg.calls[calls:])
	}
}

// A merge GitHub fails for a moment, poll after poll, is asked again for
// half an hour, and then said once: GitHub kept failing to merge it. A
// writer's retry has the factory watch it again, and it merges once GitHub
// does.
func TestGitHubFailingToMergeIsSaidOnceAfterHalfAnHour(t *testing.T) {
	r, fg, now := failingRig(t)
	n := ratified(t, r).Marker.PR
	r.expect(1, KindPR, LabelPR)
	r.gh.ci(n, "success")
	fg.merge = badGateway
	failsAgain(t, r, now, 0, 10*time.Minute, 20*time.Minute, failingFor)
	*now = clockStart.Add(failingFor + time.Minute)
	pollOK(t, r, "once GitHub had failed the merge for longer than half an hour")
	keptFailingSaid(t, r, FailMerge, n, "but github kept failing to merge it", "31 minutes", "Bad Gateway")
	if got := fg.asked(fmt.Sprintf("merge #%d", n)); got != 5 || len(r.gh.merged) != 0 {
		t.Errorf("the factory asked GitHub to merge #%d %d times, and merged %v; want 5 times, and nothing merged", n, got, r.gh.merged)
	}
	stillQuiet(t, r, fg, now)

	fg.merge = nil
	r.gh.say(1, "gitdek", "/invariant retry")
	pollOK(t, r, "after a writer said retry")
	r.expect(1, KindPR, LabelPR)
	pollOK(t, r, "in the poll after the retry's answer")
	r.expect(1, KindMerged, LabelMerged)
	if !reflect.DeepEqual(r.gh.merged, []int{n}) {
		t.Errorf("merged %v; want #%d", r.gh.merged, n)
	}
}

// Marking a watched draft ready, which GitHub fails for a moment poll after
// poll, is said once too, after half an hour, without the merge.
func TestGitHubFailingToMarkAWatchedDraftReadyIsSaidOnce(t *testing.T) {
	r, g, n := watchedDraft(t)
	fg, now := failingOn(r, g), clockOn(r.f)
	r.gh.ci(n, "success")
	fg.ready = ghSaid("gh: Server Error (HTTP 503)")
	failsAgain(t, r, now, 0, 15*time.Minute, failingFor)
	*now = clockStart.Add(failingFor + 5*time.Minute)
	pollOK(t, r, "once GitHub had failed to mark the draft ready for longer than half an hour")
	keptFailingSaid(t, r, FailMerge, n, "it's a draft, and github kept failing to mark it ready for review", "35 minutes", "Server Error (HTTP 503)")
	if !r.gh.prs[n].Draft || len(r.gh.merged) != 0 || fg.asked(fmt.Sprintf("merge #%d", n)) != 0 {
		t.Errorf("#%d must stay an unmerged draft: draft %v, merged %v, asked GitHub for %v", n, r.gh.prs[n].Draft, r.gh.merged, fg.calls)
	}
	stillQuiet(t, r, fg, now)
}

// A build that passed and finds a draft open from its branch, which GitHub
// fails for a moment to mark ready, poll after poll, says once, after half
// an hour, that GitHub kept failing, as the failure that names the draft
// for a writer's retry to watch.
func TestGitHubFailingToMarkABuildsDraftReadyIsSaidOnce(t *testing.T) {
	r, g, left := publishedDraft(t)
	fg, now := failingOn(r, g), clockOn(r.f)
	fg.ready = ghSaid("error connecting to api.github.com\ncheck your internet connection or https://githubstatus.com")
	failsAgain(t, r, now, 0, 29*time.Minute)
	r.expect(1, KindRatified, LabelBuilding)
	*now = clockStart.Add(2 * time.Hour)
	pollOK(t, r, "two hours after GitHub first failed to mark the draft ready")
	keptFailingSaid(t, r, FailUnready, left, "is still a draft: github kept failing to mark it ready for review", "2 hours", "check your internet connection")
	if !r.gh.prs[left].Draft || len(r.build.built) != 2 {
		t.Errorf("#%d must stay a draft, and the build run once: draft %v, built %d times", left, r.gh.prs[left].Draft, len(r.build.built))
	}
	stillQuiet(t, r, fg, now)
}

// The count goes on across watchers: one on another machine, with its own
// clone, finds when GitHub first failed where the first recorded it, and
// says GitHub kept failing once half an hour has gone since.
func TestTheCountGoesOnInAnotherWatcher(t *testing.T) {
	r, fg, now := failingRig(t)
	n := ratified(t, r).Marker.PR
	r.gh.ci(n, "success")
	fg.merge = ghSaid(`Put "https://api.github.com/repos/o/r/pulls/101/merge": net/http: request canceled (Client.Timeout exceeded while awaiting headers)`)
	failsAgain(t, r, now, 0)

	clone := r.f.Repo.(Clone)
	clone.Dir = filepath.Join(t.TempDir(), "clone")
	if err := clone.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.f = &Factory{Repository: r.f.Repository, GitHub: fg, Repo: clone, Formalizer: r.form, Builder: r.build,
		Base: r.f.Base, Projects: r.f.Projects, Work: t.TempDir(), Check: r.f.Check, Log: t.Logf}
	r.f.Now = func() time.Time { return *now }
	failsAgain(t, r, now, 20*time.Minute)
	*now = clockStart.Add(failingFor + time.Second)
	pollOK(t, r, "in the other watcher, once half an hour had gone since GitHub first failed")
	keptFailingSaid(t, r, FailMerge, n, "but github kept failing to merge it", "30 minutes", "Client.Timeout exceeded")
}

// A writer's retry, which the factory answers with a post, starts the count
// afresh, and so does a push, which moves the head.
func TestARetryOrAPushStartsTheCountAfresh(t *testing.T) {
	r, fg, now := failingRig(t)
	n := ratified(t, r).Marker.PR
	r.gh.ci(n, "success")
	fg.merge = badGateway
	failsAgain(t, r, now, 0)
	*now = clockStart.Add(failingFor + time.Minute)
	pollOK(t, r, "once GitHub had failed the merge for longer than half an hour")
	r.expect(1, KindFailed, LabelHumanReview)

	r.gh.say(1, "gitdek", "/invariant retry")
	pollOK(t, r, "after a writer said retry")
	r.expect(1, KindPR, LabelPR)
	failsAgain(t, r, now, failingFor+2*time.Minute, 2*failingFor)

	// Someone pushes, and CI's gate passes on the new head.
	branch := r.gh.prs[n].Head.Ref
	head := git(t, r.origin, "rev-parse", "refs/heads/"+branch)
	pushed := git(t, r.origin, "-c", "user.name=Someone", "-c", "user.email=someone@example.com", "commit-tree", head+"^{tree}", "-p", head, "-m", "a push")
	git(t, r.origin, "update-ref", "refs/heads/"+branch, pushed)
	r.gh.ci(n, "success")
	failsAgain(t, r, now, 3*failingFor, 4*failingFor)
	*now = clockStart.Add(4*failingFor + time.Minute)
	pollOK(t, r, "once GitHub had failed the merge at the new head for longer than half an hour")
	keptFailingSaid(t, r, FailMerge, n, "but github kept failing to merge it", "31 minutes", "Bad Gateway")
}

// A merge GitHub made, whose answer was lost on the way, is the factory's,
// within the half hour and past it: the factory reads the pull request
// again before it says GitHub kept failing, and the next poll says it
// merged it.
func TestALostMergeIsStillTheFactorys(t *testing.T) {
	for _, after := range []time.Duration{0, failingFor + time.Minute} {
		t.Run(after.String(), func(t *testing.T) {
			r, fg, now := failingRig(t)
			n := ratified(t, r).Marker.PR
			r.gh.ci(n, "success")
			fg.merge = badGateway
			if after > 0 {
				failsAgain(t, r, now, 0)
			}
			*now = clockStart.Add(after)
			fg.done = true
			if err := r.f.Poll(context.Background()); err == nil {
				t.Fatal("the merge's answer was lost, so the step must end without one")
			}
			if last := r.gh.last(1); last.Marker.Kind != KindPR {
				t.Fatalf("the factory said %q of a merge GitHub made:\n%s", last.Marker.Kind, last.Comment.Body)
			}
			fg.merge = nil
			pollOK(t, r, "in the poll after the merge whose answer was lost")
			merged := r.expect(1, KindMerged, LabelMerged)
			if !strings.Contains(merged.Comment.Body, "I merged it as abc123merge") || !reflect.DeepEqual(r.gh.merged, []int{n}) {
				t.Errorf("the merge is the factory's own, once: merged %v\n%s", r.gh.merged, merged.Comment.Body)
			}
		})
	}
}

// A draft GitHub marked ready, whose answer was lost on the way past the
// half hour, goes on as one GitHub marked ready: a watched draft merges, and
// a build's draft is its pull request, opened ready.
func TestALostMarkReadyGoesOn(t *testing.T) {
	t.Run("a watched draft", func(t *testing.T) {
		r, g, n := watchedDraft(t)
		fg, now := failingOn(r, g), clockOn(r.f)
		r.gh.ci(n, "success")
		fg.ready = badGateway
		failsAgain(t, r, now, 0)
		*now = clockStart.Add(failingFor + time.Minute)
		fg.done = true
		if err := r.f.Poll(context.Background()); err == nil {
			t.Fatal("marking the draft ready lost its answer, so the step must end without one")
		}
		pollOK(t, r, "in the poll after GitHub marked the draft ready")
		r.expect(1, KindMerged, LabelMerged)
		if !reflect.DeepEqual(r.gh.merged, []int{n}) {
			t.Errorf("merged %v; want #%d", r.gh.merged, n)
		}
	})
	t.Run("a build's draft", func(t *testing.T) {
		r, g, left := publishedDraft(t)
		fg, now := failingOn(r, g), clockOn(r.f)
		fg.ready = badGateway
		failsAgain(t, r, now, 0)
		*now = clockStart.Add(failingFor + time.Minute)
		fg.done = true
		if err := r.f.Poll(context.Background()); err == nil {
			t.Fatal("marking the draft ready lost its answer, so the step must end without one")
		}
		pollOK(t, r, "in the poll after GitHub marked the draft ready")
		if built := r.expect(1, KindPR, LabelPR); built.Marker.PR != left || r.gh.prs[left].Draft {
			t.Fatalf("the build must take #%d, ready: %+v", left, built.Marker)
		}
	})
}

// GitHub's primary rate limit resets within the hour, so a request it keeps
// back gets an hour more: the factory says GitHub kept failing only past an
// hour and a half.
func TestAPrimaryRateLimitGetsAnHourMore(t *testing.T) {
	r, fg, now := failingRig(t)
	n := ratified(t, r).Marker.PR
	r.gh.ci(n, "success")
	limited := "gh: API rate limit exceeded for installation ID 1. (HTTP 403)"
	fg.merge = ghSaid(limited)
	failsAgain(t, r, now, 0, failingFor+time.Minute, time.Hour, failingFor+rateLimitWait)
	*now = clockStart.Add(failingFor + rateLimitWait + time.Minute)
	pollOK(t, r, "past an hour and a half of GitHub's rate limit")
	keptFailingSaid(t, r, FailMerge, n, "but github kept failing to merge it", "91 minutes", limited)
}

// What GitHub says when it fails for a moment, beside a 5xx: a name that
// didn't resolve, a client's timeout, a connection closed before any
// answer, an HTTP/2 stream's internal error, a TLS alert, a secondary rate
// limit and gh killed by a signal, is no refusal, and the next poll asks
// again. A 405, a 409, not found and no permission are refusals, said at
// once, and a 405 stays one whatever else its message says.
func TestGitHubsMomentsAreNoRefusals(t *testing.T) {
	for _, c := range []struct {
		kind    string
		answer  error
		refusal bool
	}{
		{"a name that didn't resolve", ghSaid("error connecting to api.github.com\ncheck your internet connection or https://githubstatus.com"), false},
		{"a client's timeout", ghSaid(`Put "https://api.github.com/repos/o/r/pulls/101/merge": dial tcp 140.82.112.6:443: i/o timeout`), false},
		{"a client's timeout", ghSaid(`Put "https://api.github.com/repos/o/r/pulls/101/merge": net/http: timeout awaiting response headers`), false},
		{"a dropped connection", ghSaid(`Put "https://api.github.com/repos/o/r/pulls/101/merge": EOF`), false},
		{"a secondary rate limit", ghSaid("gh: You have exceeded a secondary rate limit. Please wait a few minutes before you try again. (HTTP 403)"), false},
		{"an HTTP/2 stream's internal error", ghSaid(`Put "https://api.github.com/repos/o/r/pulls/101/merge": stream error: stream ID 5; INTERNAL_ERROR; received from peer`), false},
		{"a TLS alert", ghSaid(`Put "https://api.github.com/repos/o/r/pulls/101/merge": remote error: tls: internal error`), false},
		{"gh killed by a signal", errors.New("signal: killed: "), false},
		{"a refusal", ghSaid("gh: Pull Request is not mergeable (HTTP 405)"), true},
		{"a refusal", ghSaid("gh: Pull Request is not mergeable: a required check ended with an i/o timeout (HTTP 405)"), true},
		{"a refusal", ghSaid("gh: Head branch was modified. Review and try the merge again. (HTTP 409)"), true},
		{"a refusal", github.ErrNotFound, true},
		{"a refusal", fmt.Errorf("%w: gh: Resource not accessible by integration (HTTP 403)", github.ErrNoPermission), true},
	} {
		t.Run(c.kind, func(t *testing.T) {
			r, fg, now := failingRig(t)
			n := ratified(t, r).Marker.PR
			r.gh.ci(n, "success")
			fg.merge = c.answer
			if c.refusal {
				pollOK(t, r, "when GitHub refused the merge")
				failed := r.expect(1, KindFailed, LabelHumanReview)
				if said := retry263Visible(failed.Comment.Body); failed.Marker.Why() != FailMerge || !strings.Contains(said, "github refused to merge it") {
					t.Errorf("a refusal must be said at once: %+v\n%s", failed.Marker, failed.Comment.Body)
				}
				return
			}
			failsAgain(t, r, now, 0)
			fg.merge = nil
			*now = clockStart.Add(time.Minute)
			pollOK(t, r, "once GitHub answered again")
			r.expect(1, KindMerged, LabelMerged)
		})
	}
}

// The posts that say GitHub kept failing keep their closing commands whole
// when they're too long for GitHub, as the refusals they stand in for do.
func TestKeptFailingPostsKeepTheirClosingCommands(t *testing.T) {
	long := keptFailing{err: errors.New(strings.Repeat("Evidence with café and 界. ", 4000)), lasted: 40 * time.Minute}
	pr := github.PullRequest{Number: 251}
	const watching = "`/invariant retry` to have me watch #251 again. I'll merge it once CI's `invariant/gate` passes on its current head. " +
		"I won't review #251 again, so if it needs fixes, including any changes its review asked for, push them first: otherwise it merges as it is."
	for _, c := range []struct {
		make func(Marker) string
		want string
	}{
		{func(m Marker) string { return mergeRefusedComment(pr, false, long, m) }, "\n\nA person needs to look at this. Once the cause is fixed, comment " + watching},
		{func(m Marker) string { return unreadyComment(pr, long, m) }, "\n\nA person needs to look at this. Comment " + watching},
	} {
		m := Marker{Kind: KindFailed, PR: 251}
		got := c.make(m)
		closingCommandsCheckPost(t, got, m, true)
		closingCommandsCheckInstruction(t, got, c.want)
		if !strings.Contains(got, "kept failing") || !strings.Contains(got, "for 40 minutes") {
			t.Errorf("a long post must still say GitHub kept failing, and for how long:\n%.400s", got)
		}
	}
}

// Watchers that hold the lease, one after the other, taking steps two at a
// time, as the watch command runs them: the first records when GitHub
// first failed under its lease, it stops, and the next, on another machine,
// takes the lease once it runs out and says GitHub kept failing once half
// an hour has gone since that first failure.
func TestLeasedWatchersCountTogether(t *testing.T) {
	ctx := context.Background()
	r, fg, now := failingRig(t)
	n := ratified(t, r).Marker.PR
	r.gh.ci(n, "success")
	fg.merge = badGateway
	logs := &logLines{t: t}
	leased := func(f *Factory, holder string) *Factory {
		f.Holder, f.LeaseFor, f.Parallel, f.Log = holder, 5*time.Minute, 2, logs.add
		f.GitHub, f.Repo = leasedGitHub{fg, f}, leasedRepo{f.Repo, f}
		return f
	}
	// poll has w hold the lease, renewing it, and poll at d after clockStart, and
	// waits for its steps.
	poll := func(w *Factory, d time.Duration) {
		t.Helper()
		*now = clockStart.Add(d)
		if held, err := w.hold(ctx); err != nil || !held {
			t.Fatalf("%s holds the lease %s after clockStart: %v, %v", w.Holder, d, held, err)
		}
		if err := w.Poll(ctx); err != nil {
			t.Fatalf("%s's poll %s after clockStart: %v", w.Holder, d, err)
		}
		w.Wait()
	}
	failed := func() int {
		count := 0
		for _, line := range logs.all() {
			if strings.Contains(line, "GitHub failed for a moment, first at "+clockStart.Format(time.RFC3339)) {
				count++
			}
		}
		return count
	}
	a := leased(r.f, "watcher-a")
	posts := len(r.gh.posts(1))
	poll(a, 0)
	if failed() != 1 || len(r.gh.posts(1)) != posts {
		t.Fatalf("watcher-a must log GitHub's first failure and post nothing: %d logged, %d posted", failed(), len(r.gh.posts(1))-posts)
	}

	clone := r.f.Repo.(leasedRepo).Repo.(Clone)
	clone.Dir = filepath.Join(t.TempDir(), "clone")
	if err := clone.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	b := leased(&Factory{Repository: r.f.Repository, Repo: clone, Formalizer: r.form, Builder: r.build,
		Base: r.f.Base, Projects: r.f.Projects, Work: t.TempDir(), Check: r.f.Check, Now: r.f.Now}, "watcher-b")
	r.f = b
	poll(b, 10*time.Minute)
	poll(b, 25*time.Minute)
	if failed() != 3 || len(r.gh.posts(1)) != posts {
		t.Fatalf("watcher-b must count from watcher-a's first failure, and post nothing within half an hour: %d logged, %d posted", failed(), len(r.gh.posts(1))-posts)
	}
	poll(b, failingFor+time.Minute)
	keptFailingSaid(t, r, FailMerge, n, "but github kept failing to merge it", "31 minutes", "Bad Gateway")
}
