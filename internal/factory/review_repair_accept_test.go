package factory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
)

const repair281Initial = "package page\n\nfunc Hello() string { return \"hello\" }\n\nfunc Greeting(name string) string { return \"hello \" + name }\n"
const repair281Fixed = "package page\n\nimport \"strings\"\n\nfunc Hello() string { return \"hello\" }\n\nfunc Greeting(name string) string { return \"hello \" + strings.TrimSpace(name) }\n"
const repair281Regression = "package page\n\nimport \"testing\"\n\nfunc TestGreetingTrimsSpaces(t *testing.T) {\n\tif Greeting(\" Ada \") != \"hello Ada\" {\n\t\tt.Fatal(\"spaces survived\")\n\t}\n}\n"

// Only the external agent and Docker process are stand-ins: the real builder,
// staging, checks, review, commits, scope check and publication run together.
type repair281Agent struct {
	jobs []synth.Job
	run  func(context.Context, synth.Job, int) (synth.Usage, error)
}

func (*repair281Agent) Name() string { return "fake" }
func (a *repair281Agent) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	a.jobs = append(a.jobs, job)
	role := "builder"
	if job.ReadOnly {
		role = "reviewer"
	}
	if job.Transcript != nil {
		fmt.Fprintf(job.Transcript, "{\"review281\":\"%s invocation %d\"}\n", role, len(a.jobs))
	}
	if !job.ReadOnly {
		for i := 0; i+1 < len(job.GateServer); i++ {
			if job.GateServer[i] == "-log" {
				log, err := os.OpenFile(job.GateServer[i+1], os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
				if err != nil {
					return synth.Usage{}, err
				}
				fmt.Fprintf(log, "{\"run\":%d,\"passed\":true}\n", len(a.jobs))
				log.Close()
				break
			}
		}
	}
	return a.run(ctx, job, len(a.jobs))
}

type repair281Build struct {
	plumbing.Builder
	result *plumbing.BuildResult
	out    string
}

func (b *repair281Build) Build(ctx context.Context, root string, n int, out string) (*plumbing.BuildResult, error) {
	r, err := b.Builder.Build(ctx, root, n, out)
	b.result = r
	b.out = out
	return r, err
}

func repair281Write(t *testing.T, root, name, text string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func repair281Read(t *testing.T, root, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Feedback may be in the job's prompt or in a file the agent is told to read.
func repair281Sees(job synth.Job, text string) bool {
	if strings.Contains(job.Prompt, text) {
		return true
	}
	found := false
	_ = filepath.WalkDir(job.Workspace, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			b, _ := os.ReadFile(p)
			found = found || strings.Contains(string(b), text)
		}
		return nil
	})
	return found
}

func repair281Rig(t *testing.T) (*rig, *repair281Agent, *repair281Agent, *repair281Build, string) {
	t.Helper()
	r, _ := plumbingRig(t, "page/page.go")
	r.form.plan.Summary = "Add a page whose Hello returns hello and whose Greeting trims surrounding spaces from a name before greeting it."
	bin, checked := t.TempDir(), filepath.Join(t.TempDir(), "checked.txt")
	// The process records each tested candidate; BROKEN_CHECKS simulates a
	// regression caught by the package suite while the acceptance test passes.
	const docker = `#!/bin/sh
[ "$1" = run ] || exit 0
src=
for a in "$@"; do
  case "$a" in *:/src) src=${a%:/src} ;; esac
done
cat "$src/page/page.go" >> "$REPAIR281_CHECKED"
echo CHECK-END >> "$REPAIR281_CHECKED"
echo '@@invariant vet=0'
if grep -q BROKEN_CHECKS "$src/page/page.go"; then
  echo '@@invariant test=1'
else
  echo '@@invariant test=0'
fi
echo '--- PASS: TestPageSaysHello (0.00s)'
echo '@@invariant accept=0'
`
	repair281Write(t, bin, "docker", docker)
	if err := os.Chmod(filepath.Join(bin, "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("REPAIR281_CHECKED", checked)
	a, review := &repair281Agent{}, &repair281Agent{}
	b := &repair281Build{Builder: plumbing.Builder{Backend: a, Reviewer: review, Binary: "invariant", TestRuns: 6,
		Timeout: time.Minute, Sandbox: plumbing.Sandbox{Image: "stand-in", ModCache: t.TempDir()}}}
	r.f.Plumbing = b
	return r, a, review, b, checked
}

func repair281Run(t *testing.T, r *rig) Post {
	t.Helper()
	r.gh.open(1, "gitdek", "Add a hello page", "Kind: plumbing\n\n/invariant solve")
	r.poll()
	p := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Marker.Proposal.Hash, "sha256:")[:hashChars])
	r.poll()
	if len(r.form.requests) != 1 || len(r.gh.prs) != 1 {
		t.Fatalf("repair redrafted or published intermediate candidates: drafts=%d PRs=%d", len(r.form.requests), len(r.gh.prs))
	}
	return r.gh.last(1)
}

func repair281Review(t *testing.T, job synth.Job) {
	t.Helper()
	if !job.ReadOnly {
		t.Error("the second agent can write")
	}
	if got := repair281Read(t, filepath.Join(job.Workspace, "repo"), "page/page_accept_test.go"); got != pageTest {
		t.Error("review did not receive the ratified acceptance test")
	}
}

func TestReviewFindingIsRepairedBeforeAReadyPullRequest(t *testing.T) {
	r, a, review, b, checked := repair281Rig(t)
	const finding = "page/page.go:5: Greeting keeps surrounding spaces; trim the name and add a regression test."
	a.run = func(_ context.Context, job synth.Job, round int) (synth.Usage, error) {
		if round == 1 {
			repair281Write(t, job.Workspace, "page/page.go", repair281Initial)
		} else {
			if round != 2 || job.Workspace != a.jobs[0].Workspace || !repair281Sees(job, finding) {
				t.Error("repair did not get the findings in the original workspace")
			}
			if got := repair281Read(t, job.Workspace, "page/page.go"); got != repair281Initial {
				t.Errorf("repair lost the original build: %q", got)
			}
			repair281Write(t, job.Workspace, "page/page.go", repair281Fixed)
			repair281Write(t, job.Workspace, "page/greeting_review_test.go", repair281Regression)
		}
		return synth.Usage{Outcome: "success", Summary: "Built the planned page."}, nil
	}
	review.run = func(_ context.Context, job synth.Job, round int) (synth.Usage, error) {
		repair281Review(t, job)
		checks, _ := os.ReadFile(checked)
		if strings.Count(string(checks), "CHECK-END") != round {
			t.Error("review ran without fresh factory checks")
		}
		if round == 1 {
			return synth.Usage{Summary: finding + "\nVERDICT: changes needed"}, nil
		}
		if got := repair281Read(t, filepath.Join(job.Workspace, "repo"), "page/page.go"); got != repair281Fixed {
			t.Error("fresh review saw the old implementation")
		}
		if got := repair281Read(t, filepath.Join(job.Workspace, "repo"), "page/greeting_review_test.go"); got != repair281Regression {
			t.Error("fresh review did not receive the added regression test")
		}
		return synth.Usage{Summary: "The finding is fixed.\nVERDICT: approve"}, nil
	}
	p := repair281Run(t, r)
	if p.Marker.Kind != KindPR || r.gh.prs[p.Marker.PR].Draft || len(a.jobs) != 2 || len(review.jobs) != 2 {
		t.Fatalf("want a ready PR after one repair, got builds=%d reviews=%d:\n%s", len(a.jobs), len(review.jobs), p.Comment.Body)
	}
	if b.result == nil || !b.result.Passed || !b.result.Approved {
		t.Fatal("final build did not pass and receive approval")
	}
	if len(b.result.TestRuns) != 2 {
		t.Errorf("test history lost a round: %+v", b.result.TestRuns)
	}
	for _, event := range []string{"builder invocation 1", "builder invocation 2", "reviewer invocation 1", "reviewer invocation 2"} {
		if !repair281Sees(synth.Job{Workspace: b.out}, event) {
			t.Errorf("build artifacts lost transcript event %q", event)
		}
	}
	branch := p.Marker.Branch
	if got := git(t, r.origin, "show", branch+":page/greeting_review_test.go"); got+"\n" != repair281Regression {
		t.Error("the regression test was lost before publication")
	}
	l, err := plumbing.ReadLock([]byte(git(t, r.origin, "show", branch+":"+plumbing.LockPath(1))))
	if err != nil || l.Plan.Hash() != r.form.plan.Hash() {
		t.Fatalf("repair changed the ratified plan: %v", err)
	}
	if got := git(t, r.origin, "show", branch+":page/page_accept_test.go"); got+"\n" != pageTest {
		t.Error("repair changed the pinned acceptance test")
	}
	sc, err := r.f.Repo.Scope(context.Background(), "origin/main", "origin/"+branch, 1)
	if err != nil || !sc.OK() {
		t.Fatalf("the published repair is out of scope: %+v, %v", sc, err)
	}
}

func TestReviewFindingOutsideThePlanGoesToAPerson(t *testing.T) {
	r, a, review, _, _ := repair281Rig(t)
	const finding = "README.md:1: The requested behavior needs a different plan that also changes README.md."
	a.run = func(_ context.Context, job synth.Job, round int) (synth.Usage, error) {
		repair281Write(t, job.Workspace, "page/page.go", repair281Initial)
		if round > 1 {
			if !repair281Sees(job, finding) {
				t.Error("builder was not given the out-of-plan finding")
			}
			// Even a mistaken attempt to address it cannot expand the plan.
			repair281Write(t, job.Workspace, "README.md", "unauthorized expansion\n")
		}
		return synth.Usage{Summary: "I cannot fix this within the ratified plan; a person must revise it."}, nil
	}
	review.run = func(_ context.Context, job synth.Job, _ int) (synth.Usage, error) {
		repair281Review(t, job)
		return synth.Usage{Summary: finding + "\nVERDICT: changes needed"}, nil
	}
	p := repair281Run(t, r)
	if len(a.jobs) < 2 || len(a.jobs) > 3 {
		t.Errorf("builder got %d attempts; want an opportunity to repair, capped at three", len(a.jobs))
	}
	if p.Marker.Kind != KindFailed || !r.gh.prs[p.Marker.PR].Draft || !strings.Contains(p.Comment.Body, finding) {
		t.Errorf("out-of-plan finding did not reach a person in a draft failure post:\n%s", p.Comment.Body)
	}
	if got := git(t, r.origin, "show", p.Marker.Branch+":README.md"); got != "# repo" {
		t.Error("an out-of-plan edit reached the PR")
	}
	if !sameSet(r.gh.labelsOf(1), []string{LabelHumanReview}) {
		t.Error("issue was not left for a person")
	}
}

func TestReviewRepairsStopAfterTwoAttempts(t *testing.T) {
	r, a, review, _, checked := repair281Rig(t)
	a.run = func(_ context.Context, job synth.Job, round int) (synth.Usage, error) {
		if round > 1 && !repair281Sees(job, fmt.Sprintf("finding from review %d", round-1)) {
			t.Error("repair was not given the latest findings")
		}
		repair281Write(t, job.Workspace, "page/page.go", repair281Initial+fmt.Sprintf("\n// candidate %d\n", round))
		if round == 2 {
			repair281Write(t, job.Workspace, "page/discarded_review_test.go", "package page\n\nimport \"testing\"\n\nfunc TestReviewGreeting(t *testing.T) {\n\tif Hello() != \"hello\" {\n\t\tt.Fatal(\"greeting changed\")\n\t}\n}\n")
		}
		if round == 3 {
			if err := os.Remove(filepath.Join(job.Workspace, "page/discarded_review_test.go")); err != nil {
				t.Fatal(err)
			}
		}
		return synth.Usage{Summary: "Attempted the repair."}, nil
	}
	review.run = func(_ context.Context, job synth.Job, round int) (synth.Usage, error) {
		repair281Review(t, job)
		_, err := os.Stat(filepath.Join(job.Workspace, "repo/page/discarded_review_test.go"))
		if (round == 2 && err != nil) || (round == 3 && !os.IsNotExist(err)) {
			t.Errorf("review %d did not see the current set of added regression tests: %v", round, err)
		}
		return synth.Usage{Summary: fmt.Sprintf("page/page.go:5: finding from review %d\nVERDICT: changes needed", round)}, nil
	}
	p := repair281Run(t, r)
	checks, _ := os.ReadFile(checked)
	if len(a.jobs) != 3 || len(review.jobs) != 3 || strings.Count(string(checks), "CHECK-END") != 3 {
		t.Errorf("want initial build plus two checked repairs, got %d builds, %d reviews, checks=%q", len(a.jobs), len(review.jobs), checks)
	}
	if p.Marker.Kind != KindFailed || !r.gh.prs[p.Marker.PR].Draft || !strings.Contains(p.Comment.Body, "finding from review 3") {
		t.Errorf("last findings did not reach a person at the repair limit:\n%s", p.Comment.Body)
	}
	if files := git(t, r.origin, "ls-tree", "-r", "--name-only", p.Marker.Branch); strings.Contains(files, "discarded_review_test.go") {
		t.Error("a regression test removed by the last repair survived publication")
	}
}

func TestMergerWorkAloneApprovesWithoutARepair(t *testing.T) {
	r, a, review, _, _ := repair281Rig(t)
	a.run = func(_ context.Context, job synth.Job, _ int) (synth.Usage, error) {
		repair281Write(t, job.Workspace, "page/page.go", repair281Fixed)
		return synth.Usage{Summary: "The planned change is complete."}, nil
	}
	const tasks = "Merger work: record the decision and run the Docker integration suite and network checks unavailable in this sandbox."
	review.run = func(_ context.Context, job synth.Job, _ int) (synth.Usage, error) {
		repair281Review(t, job)
		// The agent interface is prose. Check that the reviewer is actually
		// instructed about all three merger duties before following that policy.
		prompt := strings.ToLower(job.Prompt)
		for _, instruction := range []string{"merger work", "decision", "docker", "network", "verdict: approve"} {
			if !strings.Contains(prompt, instruction) {
				t.Errorf("review instructions omit %q", instruction)
				return synth.Usage{Summary: tasks + "\nVERDICT: changes needed"}, nil
			}
		}
		return synth.Usage{Summary: tasks + "\nVERDICT: approve"}, nil
	}
	p := repair281Run(t, r)
	if p.Marker.Kind != KindPR || r.gh.prs[p.Marker.PR].Draft || len(a.jobs) != 1 || len(review.jobs) != 1 {
		t.Errorf("merger-only work caused a repair or draft:\n%s", p.Comment.Body)
	}
	if !strings.Contains(r.gh.prBody(p.Marker.PR), tasks) {
		t.Error("the merger's checklist is absent from the PR")
	}
}

func TestFailedRepairKeepsTheReviewFindingsInTheFailurePost(t *testing.T) {
	r, a, review, _, _ := repair281Rig(t)
	const finding = "page/page.go:5: Greeting must trim the user's name."
	a.run = func(_ context.Context, job synth.Job, round int) (synth.Usage, error) {
		code := repair281Initial
		if round > 1 {
			code += "\n// BROKEN_CHECKS\n"
		}
		repair281Write(t, job.Workspace, "page/page.go", code)
		return synth.Usage{Summary: "Attempted the fix."}, nil
	}
	review.run = func(_ context.Context, job synth.Job, _ int) (synth.Usage, error) {
		repair281Review(t, job)
		return synth.Usage{Summary: finding + "\nVERDICT: changes needed"}, nil
	}
	p := repair281Run(t, r)
	if len(a.jobs) < 2 || len(a.jobs) > 3 || len(review.jobs) != 1 {
		t.Errorf("failed repair must be checked and cannot be approved: builds=%d reviews=%d", len(a.jobs), len(review.jobs))
	}
	if p.Marker.Kind != KindFailed || !r.gh.prs[p.Marker.PR].Draft || !strings.Contains(p.Comment.Body, finding) || !strings.Contains(p.Comment.Body, "tests failed") {
		t.Errorf("failure post lost the checks or review findings:\n%s", p.Comment.Body)
	}
}
