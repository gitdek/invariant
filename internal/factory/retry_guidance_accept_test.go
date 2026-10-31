package factory

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/plumbing"
)

// These scenarios exercise the factory's real poll, publication and command
// paths. The controlled builder supplies outcomes; retry must keep the meaning
// the protocol already gives it, regardless of why the build failed.
type retry263Builder struct {
	base  *fakePlanBuilder
	first string
	calls int
}

func (b *retry263Builder) Build(ctx context.Context, root string, n int, out string) (*plumbing.BuildResult, error) {
	b.calls++
	if b.calls == 1 && b.first == "stopped" {
		return nil, errors.New("agent stopped before returning a build")
	}
	res, err := b.base.Build(ctx, root, n, out)
	if err != nil || b.calls != 1 {
		return res, err
	}
	switch b.first {
	case "agent error":
		res.Passed, res.Approved = false, false
		res.TestRuns[0].Passed = false
		res.Summary = "The agent stopped with an error and left partial work."
		res.Review = ""
		return res, errors.New("the agent stopped with an error")
	case "failed tests":
		res.Passed, res.Approved = false, false
		res.TestRuns[0].Passed = false
		res.Summary = "The tests failed on the saved work."
		res.Review = ""
	case "review changes":
		res.Approved = false
		res.Review = "The change needs repair.\n\nVERDICT: changes needed"
	}
	return res, nil
}

func retry263Start(t *testing.T, outcome, file string) (*rig, *retry263Builder, Post) {
	t.Helper()
	r, base := plumbingRig(t, file)
	b := &retry263Builder{base: base, first: outcome}
	r.f.Plumbing = b
	r.gh.open(1, "gitdek", "Explain retries", "Kind: plumbing\n\n/invariant solve")
	r.poll()
	proposal := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(proposal.Marker.Proposal.Hash, "sha256:")[:hashChars])
	r.poll()
	return r, b, r.gh.last(1)
}

func retry263Visible(body string) string {
	visible, _, _ := strings.Cut(body, "<!-- invariant:")
	return strings.ToLower(visible)
}

func retry263WatchWords(t *testing.T, body string, pr int, command bool) {
	t.Helper()
	s := retry263Visible(body)
	watch := false
	for _, verb := range []string{"watch", "watching", "watches"} {
		watch = watch || strings.Contains(s, fmt.Sprintf("%s #%d again", verb, pr))
	}
	if !watch || (command && !strings.Contains(s, "/invariant retry")) {
		t.Errorf("retry advice must say it watches #%d again:\n%s", pr, body)
	}
	for _, wrong := range []string{"`/invariant retry` to build", "to build the same statements again", "retry reruns ci"} {
		if strings.Contains(s, wrong) {
			t.Errorf("retry advice promises an action it does not take (%q):\n%s", wrong, body)
		}
	}
}

// The watch is only the first step. Every PR retry post must tell a person
// what will happen after CI passes, including that it does not buy another
// agent review. Match the meaning without pinning a particular sentence.
func retry263MergeWords(t *testing.T, body string, pr int, trusted, draft bool) {
	t.Helper()
	s := strings.NewReplacer("`", "", "’", "'").Replace(retry263Visible(body))
	for _, word := range []string{"ci", "gate", "pass", "head"} {
		if !strings.Contains(s, word) {
			t.Errorf("retry advice must describe CI's gate passing on #%d's current head, missing %q:\n%s", pr, word, body)
		}
	}
	if !regexp.MustCompile(`(?:won't|will not|doesn't|does not|never|no)[^.!?\n]*review[^.!?\n]*(?:again|rerun)|(?:without|no) (?:a new|another|new) review|review[^.!?\n]*(?:won't|will not|isn't|is not|never) (?:be )?(?:run|rerun|repeated)`).MatchString(s) {
		t.Errorf("retry advice must say the review does not run again:\n%s", body)
	}
	if trusted {
		if !strings.Contains(s, "trusted base") || !regexp.MustCompile(`(?:a person|a human|you)[^.!?\n]*merge`).MatchString(s) || mergePromise.MatchString(s) {
			t.Errorf("retry advice must reserve merging #%d's trusted-base change to a person:\n%s", pr, body)
		}
		return
	}
	if !regexp.MustCompile(`(?:i'll|i will|i|the factory)[^.!?\n]*merge`).MatchString(s) {
		t.Errorf("retry advice must say the factory merges #%d once CI's gate passes:\n%s", pr, body)
	}
	if draft && !strings.Contains(s, "ready") {
		t.Errorf("retry advice must say it marks draft #%d ready before merging:\n%s", pr, body)
	}
}

func retry263RejectedReviewWords(t *testing.T, body string) {
	t.Helper()
	s := strings.NewReplacer("`", "", "’", "'").Replace(retry263Visible(body))
	if !regexp.MustCompile(`(?:as[- ]is|as it is|unchanged|without (?:the |those |these )?(?:requested )?changes)`).MatchString(s) || !strings.Contains(s, "push") || !regexp.MustCompile(`unless|until|if|except`).MatchString(s) {
		t.Errorf("retry advice for a rejected review must say it can merge as-is unless someone pushes the requested changes:\n%s", body)
	}
}

func retry263RedraftWords(t *testing.T, body string, pr int) {
	t.Helper()
	s := retry263Visible(body)
	closeAt, sameAt, reviseAt := strings.Index(s, fmt.Sprintf("close #%d", pr)), strings.Index(s, "draft the same plan again"), strings.LastIndex(s, "/invariant revise")
	if closeAt < 0 || sameAt < closeAt || reviseAt < sameAt || !strings.Contains(s[reviseAt:], "ratif") {
		t.Errorf("building again must require closing #%d, asking for the same plan, revising and ratifying again:\n%s", pr, body)
	}
}

func retry263RetryWatches(t *testing.T, r *rig, b *retry263Builder, failed Post) {
	t.Helper()
	pr, builds, drafts := failed.Marker.PR, b.calls, len(r.form.requests)
	before := *r.gh.prs[pr]
	checks := len(r.gh.checks[before.Head.SHA])
	// A command from someone without write access still cannot retry.
	r.gh.say(1, "mallory", "/invariant retry")
	r.poll()
	if b.calls != builds || len(r.gh.posts(1)) == 0 {
		t.Fatal("a nonwriter changed the failed build")
	}
	command := r.gh.say(1, "gitdek", "/invariant retry")
	r.poll()
	answer := r.expect(1, KindPR, LabelPR)
	if answer.Marker.PR != pr || answer.Marker.Hash != failed.Marker.Hash || answer.Marker.Branch != failed.Marker.Branch || !slices.Contains(answer.Marker.ReplyTo, command.ID) {
		t.Fatalf("retry must watch the same ratified pull request and answer the command: %+v", answer.Marker)
	}
	retry263WatchWords(t, answer.Comment.Body, pr, false)
	trusted := slices.Contains(failed.Marker.Proposal.Plan.Files, "internal/factory/page.go")
	retry263MergeWords(t, answer.Comment.Body, pr, trusted, before.Draft)
	if b.calls != builds || len(r.form.requests) != drafts || len(r.gh.prs) != 1 || !reflect.DeepEqual(*r.gh.prs[pr], before) || len(r.gh.checks[before.Head.SHA]) != checks {
		t.Fatal("retry rebuilt, drafted, replaced/changed the PR, or reran CI instead of watching")
	}
	if len(r.gh.merged) != 0 {
		t.Fatal("retry merged without a passing gate")
	}
	// Watching actually resumes: the same PR's failed CI is observed on the
	// following poll, with no additional agent run or replacement PR.
	r.gh.ci(pr, "failure")
	r.poll()
	observed := r.expect(1, KindFailed, LabelHumanReview)
	if observed.Marker.PR != pr || observed.Marker.Failure != FailCI || b.calls != builds || len(r.gh.prs) != 1 {
		t.Fatalf("retry did not resume watching the existing PR: %+v", observed.Marker)
	}
}

func TestPlumbingDraftRetryPostsMatchWatching(t *testing.T) {
	for _, outcome := range []string{"agent error", "failed tests", "review changes"} {
		t.Run(outcome, func(t *testing.T) {
			r, b, failed := retry263Start(t, outcome, "page/page.go")
			if failed.Marker.Kind != KindFailed || failed.Marker.Failure != FailGate || failed.Marker.PR == 0 || !r.gh.prs[failed.Marker.PR].Draft {
				t.Fatalf("fixture must publish a failed draft: %+v", failed.Marker)
			}
			retry263WatchWords(t, failed.Comment.Body, failed.Marker.PR, true)
			retry263MergeWords(t, failed.Comment.Body, failed.Marker.PR, false, true)
			retry263RedraftWords(t, failed.Comment.Body, failed.Marker.PR)
			if outcome == "review changes" {
				retry263RejectedReviewWords(t, failed.Comment.Body)
			}
			// Revise is still refused while the draft is open, but its advice
			// must describe the same watch action and the manual redraft path.
			r.gh.say(1, "gitdek", "/invariant revise")
			r.poll()
			note := r.expect(1, KindNote, LabelHumanReview)
			retry263WatchWords(t, note.Comment.Body, failed.Marker.PR, true)
			retry263MergeWords(t, note.Comment.Body, failed.Marker.PR, false, true)
			retry263RedraftWords(t, note.Comment.Body, failed.Marker.PR)
			if outcome == "review changes" {
				retry263RejectedReviewWords(t, note.Comment.Body)
			}
			if b.calls != 1 || len(r.form.requests) != 1 {
				t.Fatal("revise started work while the failed draft was open")
			}
			retry263RetryWatches(t, r, b, failed)
			// Follow the advertised alternative. Closing and revising drafts a
			// proposal; it does not bypass ratification or start another build.
			r.gh.prs[failed.Marker.PR].State = "closed"
			r.poll()
			r.expect(1, KindClosed)
			r.gh.say(1, "gitdek", "draft the same plan again\n\n/invariant revise")
			r.poll()
			r.expect(1, KindProposal, LabelProposal)
			if b.calls != 1 || len(r.form.requests) != 2 || len(r.gh.prs) != 1 {
				t.Fatal("closing and revising must wait for a new ratification before building")
			}
		})
	}
}

// The newly explicit CI advice is paired with the unchanged stopped-build
// path: absence of a PR, rather than the failure's name, determines rebuilding.
func TestRetryPostsDistinguishCIFailureAndStoppedBuild(t *testing.T) {
	for _, file := range []string{"page/page.go", "internal/factory/page.go"} {
		t.Run("CI/"+file, func(t *testing.T) {
			r, b, built := retry263Start(t, "passed", file)
			if built.Marker.Kind != KindPR {
				t.Fatalf("fixture did not publish the successful build: %+v", built.Marker)
			}
			r.gh.ci(built.Marker.PR, "failure")
			r.poll()
			failed := r.expect(1, KindFailed, LabelHumanReview)
			retry263WatchWords(t, failed.Comment.Body, failed.Marker.PR, true)
			retry263MergeWords(t, failed.Comment.Body, failed.Marker.PR, file == "internal/factory/page.go", false)
			retry263RetryWatches(t, r, b, failed)
			// The retry's existing merge safeguards still apply on green.
			r.gh.say(1, "gitdek", "/invariant retry")
			r.poll()
			answer := r.expect(1, KindPR, LabelPR)
			retry263MergeWords(t, answer.Comment.Body, failed.Marker.PR, file == "internal/factory/page.go", false)
			if file == "internal/factory/page.go" && (!strings.Contains(answer.Comment.Body, "a person merges") || mergePromise.MatchString(answer.Comment.Body)) {
				t.Errorf("retry must preserve the human-only trusted-base merge promise:\n%s", answer.Comment.Body)
			}
			r.gh.ci(failed.Marker.PR, "success")
			r.poll()
			if file == "internal/factory/page.go" {
				if p := r.expect(1, KindFailed, LabelHumanReview); p.Marker.Failure != FailTrusted || len(r.gh.merged) != 0 {
					t.Fatal("retry changed trusted-base merge behavior")
				}
			} else {
				r.expect(1, KindMerged, LabelMerged)
			}
			if b.calls != 1 || len(r.gh.prs) != 1 {
				t.Fatal("CI retries must never start another build or replace the PR")
			}
		})
	}
	// Keep the unchanged no-PR assertions in the parent: the plan checker's
	// output parser treats a passing subtest as a passing acceptance test.
	{
		r, b, failed := retry263Start(t, "stopped", "page/page.go")
		if failed.Marker.Kind != KindFailed || failed.Marker.Failure != FailStopped || failed.Marker.PR != 0 || len(r.gh.prs) != 0 {
			t.Fatalf("fixture must stop without a PR: %+v", failed.Marker)
		}
		body := retry263Visible(failed.Comment.Body)
		if !strings.Contains(body, "`/invariant retry` to build again") || strings.Contains(body, "close #") {
			t.Errorf("the existing no-PR build-again advice must stay accurate:\n%s", failed.Comment.Body)
		}
		retry := r.gh.say(1, "gitdek", "/invariant retry")
		r.poll()
		restarted := r.expect(1, KindRatified, LabelBuilding)
		if restarted.Marker.Hash != failed.Marker.Hash || restarted.Marker.Branch != failed.Marker.Branch || !slices.Contains(restarted.Marker.ReplyTo, retry.ID) || b.calls != 1 || !strings.Contains(retry263Visible(restarted.Comment.Body), "building") {
			t.Fatalf("no-PR retry must resume the same ratified proposal: %+v", restarted.Marker)
		}
		r.poll()
		built := r.expect(1, KindPR, LabelPR)
		if b.calls != 2 || len(r.form.requests) != 1 || built.Marker.Hash != failed.Marker.Hash || len(r.gh.prs) != 1 {
			t.Fatal("no-PR retry must build again without drafting or ratifying again")
		}
	}
}

// A rejected agent review is not a merge gate in the retry protocol. A
// writer can retry the untouched draft, and CI passing at that same head
// makes it mergeable without a new build or agent review. Only a person
// merges the trusted-base variant; the factory does not mark that draft
// ready either. The posts must make these consequences visible beforehand.
func TestRejectedReviewRetrySaysAndDoesMergeTheUnchangedDraft(t *testing.T) {
	for _, file := range []string{"page/page.go", "internal/factory/page.go"} {
		t.Run(file, func(t *testing.T) {
			trusted := file == "internal/factory/page.go"
			r, b, failed := retry263Start(t, "review changes", file)
			pr := failed.Marker.PR
			if pr == 0 || failed.Marker.Kind != KindFailed || failed.Marker.Failure != FailGate || !r.gh.prs[pr].Draft || !strings.Contains(failed.Comment.Body, "review asks for changes") {
				t.Fatalf("fixture must leave a draft whose tests passed and review asked for changes: %+v\n%s", failed.Marker, failed.Comment.Body)
			}
			retry263WatchWords(t, failed.Comment.Body, pr, true)
			retry263MergeWords(t, failed.Comment.Body, pr, trusted, true)
			retry263RejectedReviewWords(t, failed.Comment.Body)
			retry263RedraftWords(t, failed.Comment.Body, pr)

			// Record both the exact rejected head and the review published on
			// its PR. The agent builder owns review too, so another build call
			// would expose any attempt to obtain a new approval before merging.
			before := *r.gh.prs[pr]
			body := r.gh.prBody(pr)
			if !strings.Contains(body, "VERDICT: changes needed") {
				t.Fatal("fixture does not retain the rejected review on the PR")
			}
			g := &draftGitHub{fakeGitHub: r.gh}
			r.f.GitHub = g
			command := r.gh.say(1, "gitdek", "/invariant retry")
			r.poll()
			answer := r.expect(1, KindPR, LabelPR)
			if answer.Marker.PR != pr || answer.Marker.Hash != failed.Marker.Hash || answer.Marker.Branch != failed.Marker.Branch || !slices.Contains(answer.Marker.ReplyTo, command.ID) {
				t.Fatalf("retry must watch and answer for the same rejected draft: %+v", answer.Marker)
			}
			retry263WatchWords(t, answer.Comment.Body, pr, false)
			retry263MergeWords(t, answer.Comment.Body, pr, trusted, true)
			retry263RejectedReviewWords(t, answer.Comment.Body)
			if !reflect.DeepEqual(*r.gh.prs[pr], before) || len(g.calls) != 0 {
				t.Fatal("answering retry changed the rejected draft or tried to make it ready/merge it")
			}
			r.poll()
			r.expect(1, KindPR, LabelPR)
			if len(g.calls) != 0 || len(r.gh.merged) != 0 {
				t.Fatal("the rejected draft was marked ready or merged before CI's gate passed")
			}

			// No commit is pushed and no review is changed. CI is the only
			// new evidence; the gate passing on this head drives the outcome.
			r.gh.ci(pr, "success")
			r.poll()
			if trusted {
				waiting := r.expect(1, KindFailed, LabelHumanReview)
				if waiting.Marker.Failure != FailTrusted || waiting.Marker.PR != pr || len(g.calls) != 0 || len(r.gh.merged) != 0 || !r.gh.prs[pr].Draft {
					t.Fatalf("trusted draft must still wait for a person without ready/merge calls: %+v, calls %v", waiting.Marker, g.calls)
				}
				retry263MergeWords(t, waiting.Comment.Body, pr, true, true)
				visible := retry263Visible(waiting.Comment.Body)
				if strings.Contains(visible, "the review passed") || strings.Contains(visible, "review approved") || strings.Contains(visible, "review has passed") {
					t.Errorf("CI passing must not be described as approval of the rejected review:\n%s", waiting.Comment.Body)
				}
			} else {
				r.expect(1, KindMerged, LabelMerged)
				want := []string{fmt.Sprintf("ready #%d", pr), fmt.Sprintf("merge #%d", pr)}
				if !reflect.DeepEqual(g.calls, want) || !reflect.DeepEqual(r.gh.merged, []int{pr}) || r.gh.prs[pr].Draft {
					t.Fatalf("green CI must mark the unchanged draft ready and merge it, in order: calls %v, merged %v", g.calls, r.gh.merged)
				}
			}
			if b.calls != 1 || len(b.base.built) != 1 || len(r.form.requests) != 1 || len(r.gh.prs) != 1 || r.gh.prs[pr].Head.SHA != before.Head.SHA || r.gh.prBody(pr) != body {
				t.Fatal("retry must preserve the rejected draft's head and review, with no build, review, redraft or replacement PR")
			}
		})
	}
}
