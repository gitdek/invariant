package factory

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// A writer's retry of a pull request that failed has the factory watch it
// again (#122). Its answer promises a merge only when the factory will make
// one: a plan's pull request that changes the trusted base waits for a
// person once CI's gate passes, so its answer says a person merges it
// (D-0105).

// mergePromise is the factory saying it will merge a pull request itself.
var mergePromise = regexp.MustCompile(`(?i)\bI(?:'ll|’ll| will)? merge`)

// planPR takes a plumbing issue, whose plan writes file, to its open pull
// request, and returns the rig and the pull request's number.
func planPR(t *testing.T, file string) (*rig, int) {
	t.Helper()
	r, _ := plumbingRig(t, file)
	r.gh.open(1, "gitdek", "Add a hello page", "A page that says hello.\n\nKind: plumbing\n\n/invariant solve")
	r.poll()
	plan := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(plan.Marker.Proposal.Hash, "sha256:")[:hashChars])
	r.poll()
	return r, r.expect(1, KindPR, LabelPR).Marker.PR
}

// retried has CI's gate fail on pull request pr, then a writer say retry,
// and returns what the factory's answer to the retry says, without its
// marker.
func retried(t *testing.T, r *rig, pr int) string {
	t.Helper()
	r.gh.ci(pr, "failure")
	r.poll()
	if failed := r.expect(1, KindFailed, LabelHumanReview); failed.Marker.Failure != FailCI || failed.Marker.PR != pr {
		t.Fatalf("failure %q on #%d; want CI's gate on #%d:\n%s", failed.Marker.Failure, failed.Marker.PR, pr, failed.Comment.Body)
	}
	retry := r.gh.say(1, "gitdek", "/invariant retry")
	r.poll()
	answer := r.expect(1, KindPR, LabelPR)
	if !slices.Contains(answer.Marker.ReplyTo, retry.ID) || answer.Marker.PR != pr {
		t.Fatalf("the last post doesn't answer the retry of #%d: it replies to %v, about #%d:\n%s", pr, answer.Marker.ReplyTo, answer.Marker.PR, answer.Comment.Body)
	}
	said, _, _ := strings.Cut(answer.Comment.Body, "<!-- invariant:")
	return said
}

// A retry of a plan's pull request that changes the trusted base is
// answered without saying the factory will merge it. It names the pull
// request and the file it changes in the trusted base, as the build's own
// comment does, and says a person merges it once CI's gate passes.
func TestARetriedPlanInTheTrustedBaseSaysAPersonMergesIt(t *testing.T) {
	r, pr := planPR(t, "internal/factory/page.go")
	said := retried(t, r, pr)
	if promise := mergePromise.FindString(said); promise != "" {
		t.Errorf("the answer says %q, but the factory never merges a change to the trusted base:\n%s", promise, said)
	}
	lower := strings.ToLower(said)
	for _, want := range []string{fmt.Sprintf("#%d", pr), "trusted base", "internal/factory/page.go", "a person merges", "gate"} {
		if !strings.Contains(lower, want) {
			t.Errorf("the answer doesn't say %q:\n%s", want, said)
		}
	}
}

// The retry's answer promises a merge exactly when the factory will make
// one. A project's pull request, and a plan's that stays out of the trusted
// base, are answered as before, and merge once CI's gate passes. A plan's
// in the trusted base is promised nothing, and waits for a person.
func TestARetryPromisesAMergeOnlyWhenTheFactoryWillMakeIt(t *testing.T) {
	for _, c := range []struct {
		name    string
		trusted bool
		open    func(t *testing.T) (*rig, int)
	}{
		{"a project", false, func(t *testing.T) (*rig, int) {
			r := newRig(t)
			return r, ratified(t, r).Marker.PR
		}},
		{"a plan outside the trusted base", false, func(t *testing.T) (*rig, int) { return planPR(t, "page/page.go") }},
		{"a plan in the trusted base", true, func(t *testing.T) (*rig, int) { return planPR(t, "internal/factory/page.go") }},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, pr := c.open(t)
			said := retried(t, r, pr)
			if c.trusted {
				if promise := mergePromise.FindString(said); promise != "" || !strings.Contains(strings.ToLower(said), "a person merges") {
					t.Errorf("the answer should say a person merges #%d, and promise no merge of the factory's own:\n%s", pr, said)
				}
			} else if want := fmt.Sprintf("Watching #%d again. I'll merge it once CI's `invariant/gate` passes on its current head.", pr); !strings.Contains(said, want) {
				t.Errorf("the answer should still say %q:\n%s", want, said)
			}
			r.gh.ci(pr, "success")
			r.poll()
			if c.trusted {
				waiting := r.expect(1, KindFailed, LabelHumanReview)
				if waiting.Marker.Failure != FailTrusted || len(r.gh.merged) != 0 {
					t.Errorf("failure %q, merged %v; want #%d waiting for a person:\n%s", waiting.Marker.Failure, r.gh.merged, pr, waiting.Comment.Body)
				}
				return
			}
			r.expect(1, KindMerged, LabelMerged)
			if !reflect.DeepEqual(r.gh.merged, []int{pr}) {
				t.Errorf("merged = %v; want #%d", r.gh.merged, pr)
			}
		})
	}
}
