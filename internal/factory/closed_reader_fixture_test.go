package factory

import (
	"testing"

	"github.com/gitdek/invariant/internal/github"
)

// ClosedReaderFixture434 gives external readers the actual comments a poll
// writes, before closure and after it, with the issue closed and reopened.
func ClosedReaderFixture434(t *testing.T, kind string) (before, closed, reopened github.Issue, beforeComments, afterComments []github.Comment) {
	t.Helper()
	r := newRig(t)
	switch kind {
	case KindForks, KindProposal:
		if kind == KindProposal {
			r.form.forks = nil
		}
		r.gh.open(1, "gitdek", "A bounded buffer", "/invariant solve")
		r.poll()
	case KindFailed:
		pr := ratified(t, r)
		r.gh.ci(pr.Marker.PR, "failure")
		r.poll()
	default:
		t.Fatalf("unsupported reader fixture kind %q", kind)
	}
	if last := r.gh.last(1); last.Marker.Kind != kind {
		t.Fatalf("fixture reached %q; want %q", last.Marker.Kind, kind)
	}
	before = *r.gh.issues[1]
	before.Labels = append([]github.Label(nil), before.Labels...)
	beforeComments = append([]github.Comment(nil), r.gh.comments[1]...)
	r.gh.issues[1].State = "closed"
	r.gh.issues[1].ClosedAt = "2026-09-25T12:00:00Z"
	r.gh.at = "2026-09-25T12:01:00Z"
	r.poll()
	closed = *r.gh.issues[1]
	afterComments = append([]github.Comment(nil), r.gh.comments[1]...)
	if len(afterComments) != len(beforeComments)+1 {
		t.Fatal("closure must add one observation comment")
	}
	reopened = closed
	reopened.State, reopened.ClosedAt = "open", ""
	return
}
