package factory

import (
	"errors"
	"math"
	"strings"
	"testing"
)

// A build its review's provider stopped is, in the protocol's terms, a build
// that stopped before it made a pull request (#261). Its post records what
// the build spent and the agent that ran it, its run stays recorded with no
// result beside the record of why, and a writer can draft again, as after
// any stopped build.
func TestAProviderStoppedBuildIsAStoppedBuild(t *testing.T) {
	r := newRig(t)
	r.build.review = "No problem."
	const last = "claude stopped: error_during_execution: API Error: 529 overloaded_error (attempt 4)"
	r.f.Builder = providerFailedBuilder{fakeBuilder: r.build,
		err: errors.New("the review's run failed: the review: claude-code provider unavailable after 4 attempts: " + last)}
	ratified(t, r)
	p := r.expect(1, KindFailed, LabelHumanReview)
	if p.Marker.Failure != FailStopped || p.Marker.PR != 0 || p.Marker.Agent != "claude-code" || math.Abs(p.Marker.Spend-0.30) > 1e-9 {
		t.Errorf("marker = %+v; want a stopped build, with no pull request, that the build's agent ran and that spent $0.30", p.Marker)
	}
	if !strings.Contains(p.Comment.Body, last) || !strings.Contains(p.Comment.Body, "review included") {
		t.Errorf("the post:\n%s", p.Comment.Body)
	}
	for _, post := range r.gh.posts(1) {
		if post.Marker.Kind != KindRatified {
			continue
		}
		step := buildStepName(post)
		if state, _, err := r.f.Repo.Run(t.Context(), 1, step); err != nil || state != RunRecorded {
			t.Errorf("the build's run is %v, %v; want it recorded with no result", state, err)
		}
		if what, err := r.f.Repo.Recorded(t.Context(), 1, unavailableStep(step)); err != nil || !strings.Contains(what, "overloaded_error") {
			t.Errorf("the record of why the build stopped is %q, %v", what, err)
		}
	}
	r.gh.say(1, "gitdek", "/invariant revise")
	r.poll()
	r.expect(1, KindProposal, LabelProposal)
	if len(r.gh.prs) != 0 || len(r.gh.merged) != 0 {
		t.Errorf("pull requests = %v, merged = %v; want none", r.gh.prs, r.gh.merged)
	}
}
