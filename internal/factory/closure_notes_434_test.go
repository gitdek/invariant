package factory

import (
	"context"
	"errors"
	"testing"
)

func TestClosureNoteRecoversCleanupAndRecordsLaterClosure434(t *testing.T) {
	r := newRig(t)
	r.form.forks = nil
	r.gh.open(1, "gitdek", "A bounded buffer", "/invariant solve", LabelTrigger, "bug")
	r.poll()
	proposal := r.expect(1, KindProposal, LabelProposal, LabelTrigger, "bug")
	g := closedPoll434Hub(r)

	// An ordinary note is not a closure record. Interrupt cleanup after
	// the actual closure note, then finish it with another watcher.
	r.gh.say(1, r.gh.me, post("note", "A note after the proposal.", Marker{Kind: KindNote}))
	r.gh.issues[1].State = "closed"
	g.failCleanup = true
	before := len(r.gh.posts(1))
	if err := r.f.Poll(context.Background()); !errors.Is(err, errClosedPoll434Cleanup) {
		t.Fatalf("the first cleanup must be interrupted: %v", err)
	}
	closed := r.expect(1, KindNote, LabelProposal, LabelTrigger, "bug")
	if !closed.Marker.IssueClosed || len(closed.Marker.ReplyTo) != 0 || len(r.gh.posts(1)) != before+1 {
		t.Fatalf("closure must record one observation without answering a command: %+v", closed.Marker)
	}
	publication434Restart(t, r)
	r.poll()
	closedPoll434NoActiveLabels(t, r, 1)
	state, ok := (Thread{Posts: r.gh.posts(1)}).State()
	if !ok || state.Comment.ID != proposal.Comment.ID || len(r.gh.posts(1)) != before+1 {
		t.Fatal("cleanup must preserve the proposal and the single closure observation")
	}

	// A new working post after reopening starts work the earlier closure
	// did not observe, even if another ordinary note follows that post.
	r.gh.issues[1].State = "open"
	r.gh.say(1, "gitdek", "/invariant revise")
	r.poll()
	revised := r.expect(1, KindProposal, LabelProposal, LabelTrigger, "bug")
	if revised.Comment.ID == proposal.Comment.ID {
		t.Fatal("revision must produce a new proposal")
	}
	r.gh.say(1, r.gh.me, post("note", "A note after revision.", Marker{Kind: KindNote}))
	r.gh.issues[1].State = "closed"
	before = len(r.gh.posts(1))
	r.poll()
	closed = r.expect(1, KindNote, LabelTrigger, "bug")
	if !closed.Marker.IssueClosed || len(r.gh.posts(1)) != before+1 {
		t.Fatal("the later work needs its own closure observation")
	}
	closedPoll434NoActiveLabels(t, r, 1)
	publication434Restart(t, r)
	r.poll()
	if len(r.gh.posts(1)) != before+1 || len(r.form.requests) != 2 || len(r.build.built) != 0 {
		t.Fatal("a closed issue must not repeat the observation or resume its ordinary work")
	}
}
