package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

// GitHub failing for a moment isn't GitHub refusing (#369's review). Where
// the factory says GitHub's refusal on the issue, as it marks a draft ready
// or merges, it asks again on the next poll instead, but only for so long
// (#376): once GitHub has failed the request for longer than failingFor,
// counting from the first failure, the factory says so once, in place of
// the refusal, as the same step of the protocol and the recovery core, and
// asks nothing more until a writer's retry has it watch the pull request
// again.
//
// The bound needs to know when GitHub first failed. No post can say, since
// the factory posts nothing while it asks again, and nothing it records
// already does: a merge's record is made just before the first attempt at a
// head, but one from an attempt before a writer's retry stays, and a
// build's draft has no record at all. So the factory records that time
// beside the issue's runs and merges (D-0069), where every watcher can read
// it, and the count holds across polls, restarts and watchers. The record
// is named after the issue's latest post, the request, the pull request and
// its head, so a writer's retry, which the factory answers with a post, or
// a push, which moves the head, starts the count afresh.

// failingFor is how long GitHub may keep failing a request before the
// factory stops asking and says so: half an hour, about 60 polls at 30
// seconds and 30 at the watcher's default minute. It outlasts a blip, a
// watcher's restart and another watcher taking the lease over, which waits a
// minute past the lease's five, and a person hears within it.
const failingFor = 30 * time.Minute

// rateLimitWait is how much longer a request GitHub's primary rate limit
// keeps back may go unanswered: GitHub resets the limit within the hour, so
// waiting out one reset never counts as GitHub failing.
const rateLimitWait = time.Hour

// keptFailing is GitHub failing a request, for a moment each time the
// factory asked, for longer than it asks for: the factory says so on the
// issue, as it would a refusal. Its error is GitHub's last answer.
type keptFailing struct {
	err    error
	lasted time.Duration
}

func (k keptFailing) Error() string { return k.err.Error() }
func (k keptFailing) Unwrap() error { return k.err }

// span says how long GitHub kept failing, as a post says it.
func (k keptFailing) span() string {
	switch m := int(k.lasted / time.Minute); {
	case m < 2*60:
		return fmt.Sprintf("%d minutes", m)
	case m < 2*24*60:
		return fmt.Sprintf("%d hours", m/60)
	default:
		return fmt.Sprintf("%d days", m/(24*60))
	}
}

// failingRecordJSON is what the record of GitHub's first failure at a
// request says: when it was, by the clock of the watcher that recorded it.
type failingRecordJSON struct {
	Since time.Time `json:"since"`
}

// failingStep names the record of when GitHub first failed a request the
// factory made in the issue's state as state, its latest post, left it:
// marking pr ready, or merging it, at head.
func failingStep(state Post, ready bool, pr int, head string) string {
	what := "merge"
	if ready {
		what = "ready"
	}
	if len(head) > 12 {
		head = head[:12]
	}
	return fmt.Sprintf("failing-%d-%s-%d-%s", state.Comment.ID, what, pr, head)
}

// fromGitHub says whether err is GitHub's answer to a request, rather than
// this watcher losing its lease, or its context ending, before GitHub
// answered.
func fromGitHub(ctx context.Context, err error) bool {
	return !errors.Is(err, errLeaseLost) && !errors.Is(err, errStaleTenure) && !errors.Is(err, errNoStep) && ctx.Err() == nil
}

// gitHubsAnswer is what the factory makes of err, from a request whose
// refusal it says on the issue: marking pr ready, or merging it, which the
// record named step counts. It returns the answer to say: GitHub's refusal,
// or, once GitHub has kept failing the request for longer than the factory
// asks for, that it did. Otherwise it returns the error to end the step
// with, so the next poll asks again: the watcher lost its lease or its
// context ended before GitHub answered, or GitHub failed for a moment and
// hasn't kept failing for long enough. Before it says GitHub kept failing,
// it reads pr again: if GitHub did what it was asked after all, as when
// only its answer was lost, or anything else changed pr, the next poll goes
// on from what GitHub says then, as it always has.
func (f *Factory) gitHubsAnswer(ctx context.Context, n int, step string, pr github.PullRequest, err error) (answer, retry error) {
	switch {
	case !fromGitHub(ctx, err):
		return nil, err
	case !github.Transient(err):
		return err, nil
	}
	since, recErr := f.failingSince(ctx, n, step)
	if recErr != nil {
		return nil, errors.Join(err, recErr)
	}
	limit := failingFor
	if github.RateLimited(err) {
		limit += rateLimitWait
	}
	lasted := f.now().Sub(since)
	if lasted <= limit {
		return nil, fmt.Errorf("GitHub failed for a moment, first at %s, so the next poll asks again: %w", since.UTC().Format(time.RFC3339), err)
	}
	now, readErr := f.GitHub.PullRequest(ctx, pr.Number)
	switch {
	case readErr != nil:
		return nil, errors.Join(err, readErr)
	case now.Merged || now.State != "open" || now.Draft != pr.Draft || now.Head.SHA != pr.Head.SHA:
		return nil, fmt.Errorf("GitHub failed, but #%d has changed since, so the next poll goes on from it: %w", pr.Number, err)
	}
	return keptFailing{err: err, lasted: lasted}, nil
}

// failingSince is when GitHub first failed the request the record named
// step counts. When nothing has recorded that yet, it records now.
func (f *Factory) failingSince(ctx context.Context, n int, step string) (time.Time, error) {
	if since, ok, err := f.failingRecord(ctx, n, step); err != nil || ok {
		return since, err
	}
	now := f.now()
	b, err := json.Marshal(failingRecordJSON{Since: now.UTC()})
	if err != nil {
		return time.Time{}, err
	}
	switch err := f.Repo.Record(ctx, n, step, string(b)); {
	case err == nil:
		return now, nil
	case !errors.Is(err, ErrRecorded):
		return time.Time{}, err
	}
	// Another watcher recorded it first.
	since, ok, err := f.failingRecord(ctx, n, step)
	if err == nil && !ok {
		err = fmt.Errorf("the record of GitHub failing #%d's request, %s, isn't there", n, step)
	}
	return since, err
}

// failingRecord reads when the record named step says GitHub first failed,
// if it's recorded.
func (f *Factory) failingRecord(ctx context.Context, n int, step string) (time.Time, bool, error) {
	what, err := f.Repo.Recorded(ctx, n, step)
	if err != nil || what == "" {
		return time.Time{}, false, err
	}
	var r failingRecordJSON
	if err := json.Unmarshal([]byte(what), &r); err != nil {
		return time.Time{}, false, fmt.Errorf("the record of GitHub failing #%d's request, %s, can't be read: %w", n, step, err)
	}
	return r.Since, true, nil
}
