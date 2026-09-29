package factory

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
)

// loopyFallback is how a build's result records that the loop guard stopped
// its agent's first run, and the build ran it once more at xhigh.
func loopyFallback() *synth.Fallback {
	return &synth.Fallback{Effort: "xhigh", Why: synth.ErrThinkingLoop.Error()}
}

// loopyBuilder is a project's build that fell back to xhigh, and then went
// as the fake builder's does.
type loopyBuilder struct{ *fakeBuilder }

func (b loopyBuilder) Build(ctx context.Context, dir, out string, amend bool) (*synth.Result, error) {
	res, err := b.fakeBuilder.Build(ctx, dir, out, amend)
	if res != nil {
		res.Fallback = loopyFallback()
	}
	return res, err
}

// loopyStuckBuilder is a project's build whose agent looped at max, then at
// xhigh too, and wrote nothing, so its final gate couldn't run, as #91's
// couldn't. Synthesis gives what result it has, and an error that leads
// with the agent's.
type loopyStuckBuilder struct{}

func (loopyStuckBuilder) Build(_ context.Context, _, out string, _ bool) (*synth.Result, error) {
	res := &synth.Result{Project: "bounded buffer", Usage: synth.Usage{Backend: "stand-in", Turns: 5}, Fallback: loopyFallback()}
	return res, fmt.Errorf("the agent's run failed: %w; then the final gate couldn't run: no Go files in %s", synth.ErrThinkingLoop, filepath.Join(out, "result", "buffer"))
}

// loopyPlanBuilder is a plan's build that fell back to xhigh, and then went
// as the fake plan builder's does.
type loopyPlanBuilder struct{ *fakePlanBuilder }

func (b loopyPlanBuilder) Build(ctx context.Context, root string, n int, out string) (*plumbing.BuildResult, error) {
	res, err := b.fakePlanBuilder.Build(ctx, root, n, out)
	if res != nil {
		res.Fallback = loopyFallback()
	}
	return res, err
}

// loopyPlan takes a plumbing issue from its plan through its build.
func loopyPlan(t *testing.T, r *rig) {
	t.Helper()
	r.gh.open(1, "gitdek", "Add a hello page", "A page that says hello.\n\nKind: plumbing\n\n/invariant solve")
	r.poll()
	plan := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(plan.Marker.Proposal.Hash, "sha256:")[:hashChars])
	r.poll()
}

// loopySays fails the test unless text says the build fell back to xhigh,
// and why: the loop guard's reason.
func loopySays(t *testing.T, what, text string) {
	t.Helper()
	for _, want := range []string{"xhigh", synth.ErrThinkingLoop.Error()} {
		if !strings.Contains(text, want) {
			t.Errorf("%s doesn't say %q:\n%s", what, want, text)
		}
	}
}

// A build that fell back to xhigh, and passed, says so where people read
// it: in the factory's post and in the pull request's body, for a project
// and for a plan alike. Both are written from the build's saved record.
func TestAPullRequestSaysItsBuildFellBack(t *testing.T) {
	proj := newRig(t)
	proj.f.Builder = loopyBuilder{proj.build}
	ratified(t, proj)
	pr := proj.expect(1, KindPR, LabelPR)
	loopySays(t, "a project's pull request post", pr.Comment.Body)
	loopySays(t, "a project's pull request", proj.gh.prBody(pr.Marker.PR))

	pl, pb := plumbingRig(t, "page/page.go")
	pl.f.Plumbing = loopyPlanBuilder{pb}
	loopyPlan(t, pl)
	pr = pl.expect(1, KindPR, LabelPR)
	loopySays(t, "a plan's pull request post", pr.Comment.Body)
	loopySays(t, "a plan's pull request", pl.gh.prBody(pr.Marker.PR))
}

// #91's post said only that the final gate found no Go files. A build whose
// agent looped, at max and then at xhigh, says it looped, and that it ran
// again at xhigh, and so does a plan's build that fell back and then failed
// its tests.
func TestAFailedBuildSaysItsAgentLooped(t *testing.T) {
	proj := newRig(t)
	proj.f.Builder = loopyStuckBuilder{}
	ratified(t, proj)
	failed := proj.expect(1, KindFailed, LabelHumanReview)
	loopySays(t, "a project's failure post", failed.Comment.Body)

	pl, pb := plumbingRig(t, "page/page.go")
	pb.pass = false
	pl.f.Plumbing = loopyPlanBuilder{pb}
	loopyPlan(t, pl)
	failed = pl.expect(1, KindFailed, LabelHumanReview)
	loopySays(t, "a plan's failure post", failed.Comment.Body)
}
