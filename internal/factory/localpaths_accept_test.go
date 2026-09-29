package factory

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
)

// localUser stands for the machine's user. It's in the watcher's work
// directory and in paths elsewhere on the machine, as it was in the path
// #91's failure comment quoted.
const localUser = "someone-5e1f"

// In error text, a path under the watcher's work directory is written
// relative to it, and any other absolute path as its last part. The rest
// stays as it is: URLs, branch names, the repository's own paths, and the
// factory's commands.
func TestLocalPathsInErrorText(t *testing.T) {
	work := filepath.Join(t.TempDir(), localUser, "factory-work")
	build := filepath.Join(work, "issue-91", "build-20260929-101010")
	rel := filepath.Join("issue-91", "build-20260929-101010")
	worktree := "/private/var/folders/xy/abc123def456/T/invariant-worktree-123"
	for _, c := range []struct{ in, want string }{
		{"the final gate couldn't run: open " + filepath.Join(build, "result", "go.mod") + ": no such file or directory",
			"the final gate couldn't run: open " + filepath.Join(rel, "result", "go.mod") + ": no such file or directory"},
		{`stat "` + filepath.Join(build, "gate") + `": permission denied`,
			`stat "` + filepath.Join(rel, "gate") + `": permission denied`},
		{"fork/exec /Users/" + localUser + "/.local/bin/claude: no such file or directory",
			"fork/exec claude: no such file or directory"},
		{"git worktree add --quiet -B invariant/issue-91-buffer " + worktree + " origin/main: exit status 128",
			"git worktree add --quiet -B invariant/issue-91-buffer invariant-worktree-123 origin/main: exit status 128"},
		{"fatal: '" + worktree + "' is a missing but locked worktree",
			"fatal: 'invariant-worktree-123' is a missing but locked worktree"},
		{"/Users/" + localUser + "/go/pkg/mod/cache: read-only file system (in " + build + ")",
			"cache: read-only file system (in " + rel + ")"},
	} {
		if got := localPaths(c.in, work); got != c.want {
			t.Errorf("localPaths(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
	for _, text := range []string{
		"the agent stopped before it finished",
		"POST https://api.github.com/repos/o/r/issues/91/comments: 502 Bad Gateway",
		"internal/factory/factory.go:1165:3: undefined: localPaths",
		"2 builds stopped before they made a pull request, so I haven't started another. Comment `/invariant retry` to try again",
	} {
		if got := localPaths(text, work); got != text {
			t.Errorf("localPaths(%q) = %q; want it as it is", text, got)
		}
	}
}

// noLocalPath fails the test if text names the watcher's work directory or
// any part of its path: the directory it's in, the machine's user, or its
// own name.
func noLocalPath(t *testing.T, what, text, root string) {
	t.Helper()
	for _, part := range []string{root, localUser, "factory-work"} {
		if i := strings.Index(text, part); i >= 0 {
			t.Errorf("%s names %q: …%s…", what, part, text[max(i-80, 0):min(i+len(part)+80, len(text))])
			return
		}
	}
}

// mustSay fails the test unless text says each of wants.
func mustSay(t *testing.T, what, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("%s doesn't say %q", what, want)
		}
	}
}

// savedRecords is what the factory saved under refs/invariant/runs/ for
// issue 1: the result.json of each run that left one.
func savedRecords(t *testing.T, origin string) string {
	t.Helper()
	var records []string
	for _, ref := range strings.Fields(git(t, origin, "for-each-ref", "--format=%(refname)", "refs/invariant/runs/1")) {
		if b, err := exec.Command("git", "-C", origin, "show", ref+":result.json").Output(); err == nil {
			records = append(records, string(b))
		}
	}
	return strings.Join(records, "\n")
}

// nothingLocal fails the test if the factory's posts on issue 1, its pull
// requests' bodies, or the records it saved for the issue name the
// watcher's work directory or any part of its path.
func nothingLocal(t *testing.T, r *rig, root string) {
	t.Helper()
	for _, p := range r.gh.posts(1) {
		noLocalPath(t, "the "+p.Marker.Kind+" post", p.Comment.Body, root)
	}
	for n, body := range r.gh.bodies {
		noLocalPath(t, fmt.Sprintf("the body of #%d", n), body, root)
	}
	noLocalPath(t, "the saved records", savedRecords(t, r.origin), root)
}

// failingBuild is a build whose agent run fails with an error that names a
// path under the watcher's work directory, and one elsewhere on the
// machine. Without a result, it fails as #91's did, when the final gate
// couldn't run. With one, the run fails after the final gate ran, as
// synthesis reports it.
type failingBuild struct {
	*fakeBuilder
	result bool
	out    string
}

func (b *failingBuild) Build(ctx context.Context, dir, out string, amend bool) (*synth.Result, error) {
	b.out = out
	if !b.result {
		return nil, fmt.Errorf("the final gate couldn't run: open %s: no such file or directory (TLC is /Users/%s/.invariant/tla2tools.jar)",
			filepath.Join(out, "result", "go.mod"), localUser)
	}
	res, err := b.fakeBuilder.Build(ctx, dir, out, amend)
	if err != nil {
		return nil, err
	}
	return res, fmt.Errorf("the agent's run failed: claude ended without a result: exit status 1: open %s: permission denied (the gate tool is /Users/%s/go/bin/invariant)",
		filepath.Join(out, "gate-runs.jsonl"), localUser)
}

// failingPlanBuild is a plan's build whose runs fail with errors that name
// a path under the watcher's work directory, and one elsewhere on the
// machine. Without a result, the build itself fails. With one, its agent
// run ended with an error that its summary quotes, as plumbing.Builder
// reports it, and its review didn't run.
type failingPlanBuild struct {
	*fakePlanBuilder
	result bool
	out    string
}

func (b *failingPlanBuild) Build(ctx context.Context, root string, n int, out string) (*plumbing.BuildResult, error) {
	b.out = out
	if !b.result {
		return nil, fmt.Errorf("running the sandbox: fork/exec /Users/%s/.docker/bin/docker: no such file or directory, with its build cache in %s",
			localUser, filepath.Join(out, "gocache"))
	}
	res, err := b.fakePlanBuilder.Build(ctx, root, n, out)
	if err != nil {
		return nil, err
	}
	res.Summary = fmt.Sprintf("The agent's run ended with an error: claude ended without a result: exit status 1: open %s: permission denied (the test tool is /Users/%s/go/bin/invariant)\n%s",
		filepath.Join(out, "test-runs.jsonl"), localUser, res.Summary)
	res.Review, res.Approved = fmt.Sprintf("The review didn't run: open %s: permission denied", filepath.Join(out, "review-transcript.jsonl")), false
	return res, nil
}

// A build whose agent run fails with no result, as #91's did when its final
// gate couldn't run, gets a failure comment that says what failed and
// where, with the build's directory relative to the work directory, and
// names no part of the work directory's own path.
func TestABuildThatCantRunNamesNoLocalPath(t *testing.T) {
	r := newRig(t)
	root := t.TempDir()
	r.f.Work = filepath.Join(root, localUser, "factory-work")
	b := &failingBuild{fakeBuilder: r.build}
	r.f.Builder = b
	ratified(t, r)
	failed := r.expect(1, KindFailed, LabelHumanReview)
	rel, err := filepath.Rel(r.f.Work, b.out)
	if err != nil {
		t.Fatal(err)
	}
	mustSay(t, "the failure comment", failed.Comment.Body, "the final gate couldn't run", filepath.Join(rel, "result", "go.mod"), "TLC is tla2tools.jar")
	nothingLocal(t, r, root)
}

// A build whose agent run fails after its final gate ran gets a failure
// comment, and a saved record whose error says what failed and where, and
// neither names any part of the work directory's own path.
func TestABuildsSavedErrorNamesNoLocalPath(t *testing.T) {
	r := newRig(t)
	root := t.TempDir()
	r.f.Work = filepath.Join(root, localUser, "factory-work")
	r.build.pass = false
	b := &failingBuild{fakeBuilder: r.build, result: true}
	r.f.Builder = b
	ratified(t, r)
	failed := r.expect(1, KindFailed, LabelHumanReview)
	rel, err := filepath.Rel(r.f.Work, b.out)
	if err != nil {
		t.Fatal(err)
	}
	mustSay(t, "the failure comment", failed.Comment.Body, "What failed")
	mustSay(t, "the build's record", savedRecords(t, r.origin), "the agent's run failed", filepath.Join(rel, "gate-runs.jsonl"), "the gate tool is invariant")
	nothingLocal(t, r, root)
}

// ratifyPlan takes a plumbing issue from its plan through its build.
func ratifyPlan(t *testing.T, r *rig) {
	t.Helper()
	r.gh.open(1, "gitdek", "Add a hello page", "A page that says hello.\n\nKind: plumbing\n\n/invariant solve")
	r.poll()
	plan := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(plan.Marker.Proposal.Hash, "sha256:")[:hashChars])
	r.poll()
}

// A plan's build that fails with no result gets a failure comment that says
// what failed and where, and names no part of the work directory's own
// path.
func TestAPlanBuildThatCantRunNamesNoLocalPath(t *testing.T) {
	r, pb := plumbingRig(t, "page/page.go")
	root := t.TempDir()
	r.f.Work = filepath.Join(root, localUser, "factory-work")
	b := &failingPlanBuild{fakePlanBuilder: pb}
	r.f.Plumbing = b
	ratifyPlan(t, r)
	failed := r.expect(1, KindFailed, LabelHumanReview)
	rel, err := filepath.Rel(r.f.Work, b.out)
	if err != nil {
		t.Fatal(err)
	}
	mustSay(t, "the failure comment", failed.Comment.Body, "running the sandbox", "fork/exec docker", filepath.Join(rel, "gocache"))
	nothingLocal(t, r, root)
}

// A plan's build whose agent run ended with an error, and whose review
// didn't run, gets a failure comment, a pull request and a saved record
// that say what failed and where, and name no part of the work directory's
// own path.
func TestAPlanBuildsErrorsNameNoLocalPath(t *testing.T) {
	r, pb := plumbingRig(t, "page/page.go")
	root := t.TempDir()
	r.f.Work = filepath.Join(root, localUser, "factory-work")
	b := &failingPlanBuild{fakePlanBuilder: pb, result: true}
	r.f.Plumbing = b
	ratifyPlan(t, r)
	failed := r.expect(1, KindFailed, LabelHumanReview)
	rel, err := filepath.Rel(r.f.Work, b.out)
	if err != nil {
		t.Fatal(err)
	}
	runs, review := filepath.Join(rel, "test-runs.jsonl"), filepath.Join(rel, "review-transcript.jsonl")
	mustSay(t, "the failure comment", failed.Comment.Body, "The review didn't run", review)
	for what, text := range map[string]string{"the pull request": r.gh.prBody(failed.Marker.PR), "the build's record": savedRecords(t, r.origin)} {
		mustSay(t, what, text, "The agent's run ended with an error", runs, "the test tool is invariant", "The review didn't run", review)
	}
	nothingLocal(t, r, root)
}

// A draft whose problem names a path under the work directory gets a
// comment, and a saved record, that say what went wrong and where, and name
// no part of the work directory's own path.
func TestADraftsProblemNamesNoLocalPath(t *testing.T) {
	r := newRig(t)
	root := t.TempDir()
	r.f.Work = filepath.Join(root, localUser, "factory-work")
	runs := filepath.Join("issue-1", "formalize-20260929-101010", "check-runs.jsonl")
	r.form.fail = fmt.Sprintf("the agent's run failed (claude ended without a result: exit status 1: can't write %s; its settings are in /Users/%s/.claude/settings.json), and it left no usable draft: the draft has no proposal.json",
		filepath.Join(r.f.Work, runs), localUser)
	r.gh.open(1, "gitdek", "Add a bounded buffer", "A buffer.\n\n/invariant solve")
	r.poll()
	stuck := r.expect(1, KindStuck, LabelHumanReview)
	for what, text := range map[string]string{"the comment": stuck.Comment.Body, "the draft's record": savedRecords(t, r.origin)} {
		mustSay(t, what, text, "no usable draft", runs, "its settings are in settings.json")
	}
	nothingLocal(t, r, root)
}
