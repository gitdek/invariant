package factory

import (
	"fmt"
	"strings"
	"testing"
)

// A plan's build left in a draft pull request says to close it before
// drafting again, since the factory drafts again only once it's closed
// (#218). One that stopped before it made a pull request has nothing to
// close.
func TestAPlanInDraftSaysToCloseItBeforeDraftingAgain(t *testing.T) {
	r, b := plumbingRig(t, "page/page.go")
	b.approve = false
	r.gh.open(1, "gitdek", "Add a hello page", "Kind: plumbing\n\n/invariant solve")
	r.poll()
	plan := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(plan.Marker.Proposal.Hash, "sha256:")[:hashChars])
	r.poll()
	failed := r.expect(1, KindFailed, LabelHumanReview)
	body := failed.Comment.Body
	closeAt := strings.Index(body, fmt.Sprintf("close #%d", failed.Marker.PR))
	if reviseAt := strings.LastIndex(body, "`/invariant revise`"); closeAt < 0 || reviseAt < closeAt {
		t.Errorf("the comment doesn't say to close #%d before drafting again:\n%s", failed.Marker.PR, body)
	}
	if stopped := planFailedComment(nil, nil, fmt.Errorf("the agent stopped"), failed.Marker); strings.Contains(stopped, "close #") {
		t.Errorf("a build with no pull request is told to close one:\n%s", stopped)
	}
}
