package factory

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
)

const pageTest = `package page

import "testing"

func TestPageSaysHello(t *testing.T) {
	if Hello() != "hello" {
		t.Fatal("Hello() isn't hello")
	}
}
`

// fakePlanBuilder writes the files a ratified plan names, the way the build
// agent would, and reports what it's told to.
type fakePlanBuilder struct {
	pass, approve bool
	built         []int
}

func (b *fakePlanBuilder) Build(_ context.Context, root string, n int, out string) (*plumbing.BuildResult, error) {
	b.built = append(b.built, n)
	lock, err := plumbing.ReadLockFile(root, n)
	if err != nil {
		return nil, err
	}
	for _, f := range lock.Plan.Files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, []byte("package page\n\n// Hello says hello.\nfunc Hello() string { return \"hello\" }\n"), 0o644); err != nil {
			return nil, err
		}
	}
	verdict := "It does what the plan says.\n\nVERDICT: approve"
	if !b.approve {
		verdict = "Hello is special-cased for the test.\n\nVERDICT: changes needed"
	}
	return &plumbing.BuildResult{Changes: plumbing.Changes{Written: lock.Plan.Files}, Passed: b.pass, Tests: len(lock.Plan.Tests),
		Summary: "Everything passed.", Review: verdict, Approved: b.pass && b.approve,
		Usage: synth.Usage{Backend: "fake", Turns: 2, CostUSD: 0.30}, Spend: 0.40, TestRuns: []synth.GateRun{{Run: 1, Passed: b.pass}}}, nil
}

// plumbingRig is a rig whose formalizer plans a plumbing change to files. It
// runs on Invariant's own repository, the only one that plans plumbing for
// now (#103).
func plumbingRig(t *testing.T, files ...string) (*rig, *fakePlanBuilder) {
	r := newRig(t)
	r.f.Repository = "gitdek/invariant"
	r.form.forks = nil
	r.form.plan = &plumbing.Plan{Name: "a hello page", Summary: "Add a page that says hello.", Files: files,
		Tests:   []plumbing.Test{{Name: "TestPageSaysHello", File: "page/page_accept_test.go", Says: "The page says hello."}},
		Sources: map[string]string{"page/page_accept_test.go": pageTest}}
	b := &fakePlanBuilder{pass: true, approve: true}
	r.f.Plumbing = b
	return r, b
}

// A plumbing issue goes from a plan to a merge: the plan is proposed with
// its files and acceptance tests, the ratification records it and adds the
// tests, the build writes only what the plan names, and the factory merges
// once CI's gate passes, since nothing is in the trusted base.
func TestAPlumbingIssueMergesThroughAPlan(t *testing.T) {
	r, b := plumbingRig(t, "page/page.go")
	r.gh.open(1, "gitdek", "Add a hello page", "A page that says hello.\n\nKind: plumbing\n\n/invariant solve")
	r.poll()
	plan := r.expect(1, KindProposal, LabelProposal)
	p := plan.Marker.Proposal
	if p.Plan == nil || p.Hash != p.Plan.Hash() || r.form.requests[0].Plumbing == "" {
		t.Fatalf("the proposal should be a plan: %+v", p)
	}
	short := strings.TrimPrefix(p.Hash, "sha256:")[:hashChars]
	for _, want := range []string{"Here's my plan for **a hello page**", "tested, not proved", "`page/page.go`", "`TestPageSaysHello` in `page/page_accept_test.go`",
		"Once CI's gate and a second agent's review pass, I merge it.", "`/invariant ratify " + short + "`", "func TestPageSaysHello"} {
		if !strings.Contains(plan.Comment.Body, want) {
			t.Errorf("the plan comment lacks %q:\n%s", want, plan.Comment.Body)
		}
	}

	ratify := r.gh.say(1, "gitdek", "/invariant ratify "+short)
	r.poll()
	pr := r.expect(1, KindPR, LabelPR)
	branch := "invariant/issue-1-a-hello-page"
	if !reflect.DeepEqual(b.built, []int{1}) || pr.Marker.Branch != branch || pr.Marker.Project != plumbing.LockPath(1) || pr.Marker.Hash != p.Hash {
		t.Fatalf("built %v, marker %+v", b.built, pr.Marker)
	}
	log := git(t, r.origin, "log", "--format=%s", "main.."+branch)
	if log != "Implement #1: Add a hello page\nRatify the plan for #1" {
		t.Errorf("log = %q", log)
	}
	lock, err := plumbing.ReadLock([]byte(git(t, r.origin, "show", branch+":"+plumbing.LockPath(1))))
	if err != nil {
		t.Fatal(err)
	}
	want := plumbing.Ratification{By: "gitdek", At: ratify.CreatedAt, Issue: 1, Comment: ratify.URL, Proposal: p.Hash}
	if lock.Ratified != want {
		t.Errorf("ratified = %+v; want %+v", lock.Ratified, want)
	}
	if got := git(t, r.origin, "show", branch+":page/page_accept_test.go"); got+"\n" != pageTest {
		t.Errorf("the acceptance test on the branch isn't the ratified one:\n%s", got)
	}
	if body := r.gh.prBody(pr.Marker.PR); !strings.Contains(body, "Closes #1.") || !strings.Contains(body, "VERDICT: approve") || r.gh.prs[pr.Marker.PR].Draft {
		t.Errorf("pull request body:\n%s", body)
	}

	r.gh.ci(pr.Marker.PR, "success")
	r.poll()
	r.expect(1, KindMerged, LabelMerged)
	if !reflect.DeepEqual(r.gh.merged, []int{pr.Marker.PR}) {
		t.Errorf("merged = %v", r.gh.merged)
	}
}

// A plan that changes the trusted base passes CI's gate and the review, and
// then waits for a person to merge it. When one does, the factory says so.
func TestAPlanInTheTrustedBaseWaitsForAPerson(t *testing.T) {
	r, _ := plumbingRig(t, "internal/factory/page.go")
	r.gh.open(1, "gitdek", "Add a hello page", "Kind: plumbing\n\n/invariant solve")
	r.poll()
	plan := r.expect(1, KindProposal, LabelProposal)
	if !strings.Contains(plan.Comment.Body, "It changes the trusted base (`internal/factory/page.go`), so once CI's gate and a second agent's review pass, a person merges it.") {
		t.Errorf("the plan should say a person merges it:\n%s", plan.Comment.Body)
	}
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(plan.Marker.Proposal.Hash, "sha256:")[:hashChars])
	r.poll()
	pr := r.expect(1, KindPR, LabelPR)
	r.gh.ci(pr.Marker.PR, "success")
	r.poll()
	waiting := r.expect(1, KindFailed, LabelHumanReview)
	if waiting.Marker.Failure != FailTrusted || len(r.gh.merged) != 0 || !strings.Contains(waiting.Comment.Body, "Only a person merges that") {
		t.Fatalf("failure %q, merged %v:\n%s", waiting.Marker.Failure, r.gh.merged, waiting.Comment.Body)
	}
	// It doesn't say it again while it waits.
	before := len(r.gh.comments[1])
	r.poll()
	if len(r.gh.comments[1]) != before {
		t.Error("the factory kept posting while it waited for a person")
	}
	merged := r.gh.prs[pr.Marker.PR]
	merged.Merged, merged.State = true, "closed"
	r.poll()
	if done := r.expect(1, KindMerged, LabelMerged); !strings.Contains(done.Comment.Body, "was merged") {
		t.Errorf("merged post:\n%s", done.Comment.Body)
	}
}

// A build whose review asks for changes opens a draft pull request and asks
// a person to look, instead of merging.
func TestAPlanTheReviewRejectsNeedsAPerson(t *testing.T) {
	r, b := plumbingRig(t, "page/page.go")
	b.approve = false
	r.gh.open(1, "gitdek", "Add a hello page", "Kind: plumbing\n\n/invariant solve")
	r.poll()
	plan := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(plan.Marker.Proposal.Hash, "sha256:")[:hashChars])
	r.poll()
	failed := r.expect(1, KindFailed, LabelHumanReview)
	if failed.Marker.Failure != FailGate || !r.gh.prs[failed.Marker.PR].Draft || !strings.Contains(failed.Comment.Body, "the second agent's review asks for changes") {
		t.Fatalf("failure %q, draft %v:\n%s", failed.Marker.Failure, r.gh.prs[failed.Marker.PR].Draft, failed.Comment.Body)
	}
}

// A plumbing issue can't also name a project.
func TestAPlumbingIssueNamesNoProject(t *testing.T) {
	r, _ := plumbingRig(t, "page/page.go")
	r.gh.open(1, "gitdek", "Add a hello page", "Kind: plumbing\nProject: examples/09-page\n\n/invariant solve")
	r.poll()
	stuck := r.expect(1, KindStuck, LabelHumanReview)
	if !strings.Contains(stuck.Comment.Body, "names no project or code to check") {
		t.Errorf("stuck:\n%s", stuck.Comment.Body)
	}
}
