package factory

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/project"
)

type rig struct {
	t      *testing.T
	gh     *fakeGitHub
	form   *scriptedFormalizer
	build  *fakeBuilder
	f      *Factory
	origin string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	origin, clone := gitRepos(t)
	gh := newFakeGitHub(t, origin)
	form := &scriptedFormalizer{t: t, forks: []formalize.Fork{{ID: "F1", Question: "When the buffer is full, what should a producer do?",
		Options: []formalize.Option{{ID: "A", Says: "Wait until there's room."}, {ID: "B", Says: "Get an error right away."}}}}}
	build := &fakeBuilder{pass: true}
	f := &Factory{Repository: "o/r", GitHub: gh, Repo: clone, Formalizer: form, Builder: build,
		Base: "main", Projects: "examples", Work: t.TempDir(), Check: "invariant/gate", Log: t.Logf}
	return &rig{t: t, gh: gh, form: form, build: build, f: f, origin: origin}
}

func (r *rig) poll() {
	r.t.Helper()
	if err := r.f.Poll(context.Background()); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) expect(issue int, kind string, labels ...string) Post {
	r.t.Helper()
	p := r.gh.last(issue)
	if p.Marker.Kind != kind {
		r.t.Fatalf("the factory's last post is %q; want %q:\n%s", p.Marker.Kind, kind, p.Comment.Body)
	}
	if got := r.gh.labelsOf(issue); !sameSet(got, labels) {
		r.t.Fatalf("labels = %v; want %v", got, labels)
	}
	return p
}

func sameSet(a, b []string) bool {
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}

// The whole flow: an issue becomes a decision request, then a
// ratification, then a pull request, then a merge.
func TestIssueToMergedPullRequest(t *testing.T) {
	r := newRig(t)
	r.gh.open(1, "gitdek", "Add a bounded buffer", "Producers put log lines in a buffer; a shipper takes them out in order.\n\n/invariant solve")

	r.poll()
	forks := r.expect(1, KindForks, LabelAsking)
	if !strings.Contains(forks.Comment.Body, "**F1. When the buffer is full, what should a producer do?**") ||
		!strings.Contains(forks.Comment.Body, "`/invariant choose F1 A`") {
		t.Errorf("the forks comment should ask the question and say how to answer:\n%s", forks.Comment.Body)
	}
	if req := r.form.requests[0]; strings.Contains(req.Body, "/invariant") || req.Issue != 1 {
		t.Errorf("the formalizer's request should be the issue without commands: %+v", req)
	}

	// Someone without write access can't decide anything.
	r.gh.say(1, "mallory", "/invariant choose F1 A")
	r.poll()
	if n := len(r.gh.posts(1)); n != 1 || len(r.form.requests) != 1 {
		t.Fatalf("a read-only user's command must be ignored: %d posts, %d formalizations", n, len(r.form.requests))
	}

	// Ratifying before the fork is decided isn't possible.
	r.gh.say(1, "gitdek", "/invariant ratify 0123456789ab")
	r.poll()
	if note := r.expect(1, KindNote, LabelAsking); !strings.Contains(note.Comment.Body, "no proposal waiting") {
		t.Errorf("note = %s", note.Comment.Body)
	}

	r.gh.say(1, "gitdek", "Wait, not an error: producers should block.\n\n`/invariant choose F1 A`")
	r.poll()
	proposal := r.expect(1, KindProposal, LabelProposal)
	if got := r.form.requests[1].Answers; len(got) != 1 || got[0].Option != "A" || got[0].By != "gitdek" {
		t.Errorf("the formalizer should get the decided fork: %+v", got)
	}
	if !strings.Contains(r.form.requests[1].Markdown(), "producers should block") {
		t.Error("the formalizer should see people's comments")
	}
	hash := proposal.Marker.Proposal.Hash
	short := strings.TrimPrefix(hash, "sha256:")[:hashChars]
	for _, want := range []string{"`WithinCap`", "The buffer never holds more than its capacity.", "`/invariant ratify " + short + "`",
		"TLC explored 7 states", "the known bug `PutWhenFull` was caught", "```tla", "Wait until there's room."} {
		if !strings.Contains(proposal.Comment.Body, want) {
			t.Errorf("the proposal lacks %q:\n%s", want, proposal.Comment.Body)
		}
	}
	if strings.Contains(proposal.Comment.Body, "Take ==") {
		t.Error("the proposal's TLA+ should hold only what's pinned, not the draft model")
	}

	// A hash that isn't the proposal's ratifies nothing.
	r.gh.say(1, "gitdek", "/invariant ratify ffffffffffff")
	r.poll()
	r.expect(1, KindNote, LabelProposal)

	ratify := r.gh.say(1, "gitdek", "/invariant ratify "+short)
	r.poll()
	pr := r.expect(1, KindPR, LabelPR)
	if len(r.build.built) != 1 {
		t.Fatalf("builds = %v", r.build.built)
	}
	branch := "invariant/issue-1-bounded-buffer"
	dir := "examples/03-bounded-buffer"
	if pr.Marker.Branch != branch || pr.Marker.Project != dir || pr.Marker.Hash != hash {
		t.Fatalf("marker = %+v", pr.Marker)
	}

	// The branch holds two commits: the ratification, then the code.
	log := git(t, r.origin, "log", "--format=%an <%ae>|%s", "main.."+branch)
	if want := "Joseph <7275925+gitdek@users.noreply.github.com>|Implement #1: Add a bounded buffer\n" +
		"Joseph <7275925+gitdek@users.noreply.github.com>|Ratify the statements for #1"; log != want {
		t.Errorf("log = %q", log)
	}
	var lock project.Lock
	if err := json.Unmarshal([]byte(git(t, r.origin, "show", branch+":"+dir+"/.invariant/ratified.lock")), &lock); err != nil {
		t.Fatal(err)
	}
	want := project.Ratification{By: "gitdek", At: ratify.CreatedAt, Issue: 1, Comment: ratify.URL, Proposal: hash}
	if lock.Ratified == nil || *lock.Ratified != want || project.ProposalHash(lock.Bounds, lock.Statements) != hash {
		t.Errorf("lock.Ratified = %+v; want %+v", lock.Ratified, want)
	}
	for _, file := range []string{"go.mod", "README.md", ".invariant/request.md", ".invariant/specs/BoundedBuffer.tla", "buffer/buffer.go"} {
		git(t, r.origin, "cat-file", "-e", branch+":"+dir+"/"+file)
	}
	if gomod := git(t, r.origin, "show", branch+":"+dir+"/go.mod"); gomod != "module github.com/o/r/examples/03-bounded-buffer\n\ngo 1.27.1" {
		t.Errorf("go.mod = %q", gomod)
	}
	request := git(t, r.origin, "show", branch+":"+dir+"/.invariant/request.md")
	if !strings.Contains(request, "Wait until there's room. (decided by @gitdek") || strings.Contains(request, "/invariant") {
		t.Errorf("request.md should record the decision and no commands:\n%s", request)
	}
	opened := r.gh.prs[pr.Marker.PR]
	if opened.Draft || opened.Head.Ref != branch || !strings.Contains(r.gh.prBody(pr.Marker.PR), "Closes #1.") {
		t.Errorf("pull request = %+v", opened)
	}

	// Nothing merges before CI's gate has passed.
	r.poll()
	r.expect(1, KindPR, LabelPR)
	r.gh.ci(pr.Marker.PR, "success")
	r.poll()
	merged := r.expect(1, KindMerged, LabelMerged)
	if !reflect.DeepEqual(r.gh.merged, []int{pr.Marker.PR}) || !reflect.DeepEqual(r.gh.deleted, []string{branch}) {
		t.Errorf("merged = %v, deleted = %v", r.gh.merged, r.gh.deleted)
	}
	if !strings.Contains(merged.Comment.Body, "I merged it as abc123merge") {
		t.Errorf("merged comment = %s", merged.Comment.Body)
	}

	// Once it's done, the factory leaves the issue alone.
	before := len(r.gh.comments[1])
	r.poll()
	if len(r.gh.comments[1]) != before {
		t.Error("the factory kept posting after the merge")
	}
}

// ratified runs an issue up to an open pull request.
func ratified(t *testing.T, r *rig) Post {
	t.Helper()
	r.form.forks = nil
	r.gh.open(1, "gitdek", "Add a bounded buffer", "A buffer.\n\n/invariant solve")
	r.poll()
	p := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Marker.Proposal.Hash, "sha256:"))
	r.poll()
	return r.gh.last(1)
}

func TestARedGateIsNeverMerged(t *testing.T) {
	r := newRig(t)
	pr := ratified(t, r)
	r.gh.ci(pr.Marker.PR, "failure")
	r.poll()
	failed := r.expect(1, KindFailed, LabelHumanReview)
	if len(r.gh.merged) != 0 || !strings.Contains(failed.Comment.Body, "failure on #") {
		t.Errorf("merged = %v\n%s", r.gh.merged, failed.Comment.Body)
	}
	r.poll()
	if len(r.gh.merged) != 0 {
		t.Error("a failed pull request must stay unmerged")
	}
}

// A commit pushed to the factory's branch that reaches outside its project
// keeps the pull request from merging, even with a green gate.
func TestOutOfScopeIsNeverMerged(t *testing.T) {
	r := newRig(t)
	pr := ratified(t, r)
	tamper := t.TempDir()
	git(t, tamper, "clone", "--quiet", "--branch", pr.Marker.Branch, r.origin, ".")
	os.WriteFile(tamper+"/README.md", []byte("# changed\n"), 0o644)
	git(t, tamper, "-c", "user.name=X", "-c", "user.email=x@example.com", "commit", "--quiet", "-am", "tamper")
	git(t, tamper, "push", "--quiet", "origin", pr.Marker.Branch)
	r.gh.ci(pr.Marker.PR, "success")
	r.poll()
	failed := r.expect(1, KindFailed, LabelHumanReview)
	if len(r.gh.merged) != 0 || !strings.Contains(failed.Comment.Body, "it changes README.md, which isn't in a project") {
		t.Errorf("merged = %v\n%s", r.gh.merged, failed.Comment.Body)
	}
}

func TestAFailedBuildOpensADraftForPeople(t *testing.T) {
	r := newRig(t)
	r.build.pass = false
	failed := ratified(t, r)
	if failed.Marker.Kind != KindFailed || !sameSet(r.gh.labelsOf(1), []string{LabelHumanReview}) {
		t.Fatalf("post = %+v, labels = %v", failed.Marker, r.gh.labelsOf(1))
	}
	pr := r.gh.prs[failed.Marker.PR]
	if !pr.Draft || !strings.Contains(failed.Comment.Body, "draft pull request #") {
		t.Errorf("pr = %+v\n%s", pr, failed.Comment.Body)
	}
	r.gh.ci(pr.Number, "success")
	r.poll()
	if len(r.gh.merged) != 0 {
		t.Error("a draft for people must never be merged")
	}
}

// A build that stops before it makes a pull request asks a person to look.
func TestABuildThatStopsAsksForHelp(t *testing.T) {
	r := newRig(t)
	r.build.stop = true
	failed := ratified(t, r)
	if failed.Marker.Kind != KindFailed || failed.Marker.PR != 0 || !sameSet(r.gh.labelsOf(1), []string{LabelHumanReview}) {
		t.Fatalf("post = %+v, labels = %v", failed.Marker, r.gh.labelsOf(1))
	}
	if !strings.Contains(failed.Comment.Body, "the agent stopped before it finished") {
		t.Errorf("the post doesn't say why:\n%s", failed.Comment.Body)
	}
}

// A writer's retry builds a stopped build's proposal again (#13).
func TestRetryRebuildsAStoppedBuild(t *testing.T) {
	r := newRig(t)
	r.build.stop = true
	stopped := ratified(t, r)
	if stopped.Marker.Failure != FailStopped {
		t.Fatalf("post = %+v", stopped.Marker)
	}
	r.build.stop = false
	r.gh.say(1, "gitdek", "/invariant retry")
	r.poll()
	again := r.expect(1, KindRatified, LabelBuilding)
	if !strings.Contains(again.Comment.Body, "again") {
		t.Errorf("the retry's post doesn't say it builds again:\n%s", again.Comment.Body)
	}
	r.poll()
	pr := r.expect(1, KindPR, LabelPR)
	if pr.Marker.PR == 0 || len(r.build.built) != 2 {
		t.Errorf("pr = %+v, builds = %d", pr.Marker, len(r.build.built))
	}
}

// A build the factory itself stopped partway through is a stop the factory
// says out loud, without another agent run (#13).
func TestAnUnfinishedBuildIsAStop(t *testing.T) {
	r := newRig(t)
	r.build.crash = true
	r.form.forks = nil
	r.gh.open(1, "gitdek", "Add a bounded buffer", "A buffer.\n\n/invariant solve")
	r.poll()
	p := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Marker.Proposal.Hash, "sha256:"))
	if err := r.f.Poll(context.Background()); err == nil || !strings.Contains(err.Error(), "partway") {
		t.Fatalf("the crash: %v", err)
	}
	r.expect(1, KindRatified, LabelBuilding)
	r.build.crash = false
	r.poll()
	stopped := r.expect(1, KindFailed, LabelHumanReview)
	if stopped.Marker.Failure != FailStopped || len(r.build.built) != 1 {
		t.Errorf("post = %+v, builds = %d", stopped.Marker, len(r.build.built))
	}
	r.poll()
	if len(r.build.built) != 1 {
		t.Error("the factory built again without a retry")
	}
}

// A build whose pull request GitHub refused never becomes another agent
// run: the next poll says the build stopped (#13).
func TestARefusedPullRequestIsAStop(t *testing.T) {
	r := newRig(t)
	r.gh.failPRs = 1
	r.form.forks = nil
	r.gh.open(1, "gitdek", "Add a bounded buffer", "A buffer.\n\n/invariant solve")
	r.poll()
	p := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Marker.Proposal.Hash, "sha256:"))
	if err := r.f.Poll(context.Background()); err == nil {
		t.Fatal("the refused pull request should be an error")
	}
	r.poll()
	stopped := r.expect(1, KindFailed, LabelHumanReview)
	if stopped.Marker.Failure != FailStopped || len(r.build.built) != 1 {
		t.Errorf("post = %+v, builds = %d", stopped.Marker, len(r.build.built))
	}
}

// Two stops, and the factory waits for a writer's retry before it starts
// another build; the retry counts stops afresh (#13).
func TestTwoStopsWaitForARetry(t *testing.T) {
	r := newRig(t)
	r.build.stop = true
	ratified(t, r)
	for i := 0; i < 2; i++ {
		// A revise drafts again, and the stops still count.
		r.gh.say(1, "gitdek", "/invariant revise")
		r.poll()
		p := r.expect(1, KindProposal, LabelProposal)
		r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Marker.Proposal.Hash, "sha256:"))
		r.poll()
	}
	limit := r.expect(1, KindFailed, LabelHumanReview)
	if limit.Marker.Failure != FailLimit || len(r.build.built) != 2 {
		t.Fatalf("post = %+v, builds = %d", limit.Marker, len(r.build.built))
	}
	r.build.stop = false
	r.gh.say(1, "gitdek", "/invariant retry")
	r.poll()
	r.expect(1, KindRatified, LabelBuilding)
	r.poll()
	r.expect(1, KindPR, LabelPR)
	if len(r.build.built) != 3 {
		t.Errorf("builds = %d", len(r.build.built))
	}
}

func TestTheLabelStartsTheFactory(t *testing.T) {
	r := newRig(t)
	r.gh.open(2, "gitdek", "Add a bounded buffer", "No command here.", LabelTrigger)
	r.gh.open(3, "gitdek", "Unrelated", "Just a note to self.")
	r.gh.open(4, "mallory", "Sneaky", "/invariant solve")
	r.poll()
	r.expect(2, KindForks, LabelTrigger, LabelAsking)
	if len(r.gh.posts(3)) != 0 || len(r.gh.posts(4)) != 0 {
		t.Error("only issues a writer gave to the factory are its business")
	}
}

func TestStuckFormalizationAsksForHelp(t *testing.T) {
	r := newRig(t)
	r.form.fail = "Design: TLC: invariant WithinCap is violated"
	r.gh.open(1, "gitdek", "Add a bounded buffer", "/invariant solve")
	r.poll()
	stuck := r.expect(1, KindStuck, LabelHumanReview)
	if !strings.Contains(stuck.Comment.Body, "> Design: TLC: invariant WithinCap is violated") {
		t.Errorf("stuck = %s", stuck.Comment.Body)
	}
	r.form.fail = ""
	r.form.forks = nil
	r.gh.say(1, "gitdek", "Cap it at two.\n/invariant revise")
	r.poll()
	r.expect(1, KindProposal, LabelProposal)
}

func TestBadChoicesGetANote(t *testing.T) {
	r := newRig(t)
	r.gh.open(1, "gitdek", "Add a bounded buffer", "/invariant solve")
	r.poll()
	r.gh.say(1, "gitdek", "/invariant choose F9 A")
	r.poll()
	if note := r.expect(1, KindNote, LabelAsking); !strings.Contains(note.Comment.Body, "There's no open question F9.") {
		t.Errorf("note = %s", note.Comment.Body)
	}
	r.gh.say(1, "gitdek", "/invariant choose F1 Z")
	r.poll()
	if note := r.expect(1, KindNote, LabelAsking); !strings.Contains(note.Comment.Body, "F1 has no option Z.") {
		t.Errorf("note = %s", note.Comment.Body)
	}
	r.gh.say(1, "gitdek", "/invariant solve")
	r.poll()
	if note := r.expect(1, KindNote, LabelAsking); !strings.Contains(note.Comment.Body, "already working on this issue") {
		t.Errorf("note = %s", note.Comment.Body)
	}
}

func TestParseCommands(t *testing.T) {
	body := "Sounds right.\n\n`/invariant ratify 3f2a9c1b7d4e`\n> /invariant solve\n```\n/invariant revise\n```\n  /invariant CHOOSE F1 a\n/invariant bogus\n/invariantsolve"
	got := ParseCommands(body)
	if len(got) != 2 || got[0].Verb != Ratify || got[0].Args[0] != "3f2a9c1b7d4e" || got[1].Verb != Choose || got[1].Args[1] != "a" {
		t.Errorf("commands = %+v", got)
	}
}

func TestMarkerRoundTrip(t *testing.T) {
	m := Marker{Kind: KindProposal, ReplyTo: []int64{0, 7}, Proposal: &formalize.Proposal{ModuleText: "a --> b -- c"}}
	body := post("x", "text", m)
	got, ok := DecodeMarker(body)
	if !ok || !reflect.DeepEqual(got, m) {
		t.Errorf("round trip = %+v, %v", got, ok)
	}
	if _, ok := DecodeMarker("<!-- invariant:not base64! -->"); ok {
		t.Error("a garbled marker isn't a marker")
	}
}

func TestMatches(t *testing.T) {
	hash := "sha256:3f2a9c1b7d4e5f60718293a4b5c6d7e8f9"
	for args, want := range map[string]bool{"3f2a9c1b7d4e": true, "sha256:3f2a9c1b7d4e5f": true, "3F2A9C1B7D4E": true, "3f2a9c1b7d4": false, "3f2a9c1b7d4f": false, "": false} {
		if got := matches(strings.Fields(args), hash); got != want {
			t.Errorf("matches(%q) = %v", args, got)
		}
	}
}

type fakeComments struct {
	comments map[int64]github.Comment
	perms    map[string]string
}

func (f fakeComments) Comment(_ context.Context, id int64) (github.Comment, error) {
	c, ok := f.comments[id]
	if !ok {
		return c, github.ErrNotFound
	}
	return c, nil
}

func (f fakeComments) Permission(_ context.Context, login string) (string, error) {
	return f.perms[login], nil
}

func TestVerifyRatification(t *testing.T) {
	p := bufferProposal(t)
	hex := strings.TrimPrefix(p.Hash, "sha256:")
	ratifying := func(id int64, issue int, by, body string) github.Comment {
		return github.Comment{ID: id, User: github.User{Login: by}, Body: body,
			IssueURL: "https://api.github.com/repos/o/r/issues/" + strconv.Itoa(issue)}
	}
	gh := fakeComments{perms: map[string]string{"gitdek": "admin", "reader": "read", "invariant-factory[bot]": "write"}, comments: map[int64]github.Comment{
		1: ratifying(1, 12, "gitdek", "Looks right.\n/invariant ratify "+hex[:12]),
		2: ratifying(2, 12, "reader", "/invariant ratify "+hex[:12]),
		3: ratifying(3, 12, "gitdek", "/invariant ratify 000000000000"),
		4: ratifying(4, 12, "gitdek", post("proposal", "`/invariant ratify "+hex[:12]+"`", Marker{Kind: KindProposal})),
		5: ratifying(5, 13, "gitdek", "/invariant ratify "+hex[:12]),
		6: func() github.Comment {
			c := ratifying(6, 12, "invariant-factory[bot]", "/invariant ratify "+hex[:12])
			c.User.Type = "Bot"
			return c
		}(),
	}}
	lock := func(id int64, by string, issue int) project.Lock {
		return project.Lock{Bounds: p.Bounds, Statements: p.Statements, Ratified: &project.Ratification{By: by, Issue: issue, Proposal: p.Hash,
			Comment: "https://github.com/o/r/issues/" + strconv.Itoa(issue) + "#issuecomment-" + strconv.FormatInt(id, 10)}}
	}
	ctx := context.Background()
	if err := VerifyRatification(ctx, gh, "o/r", lock(1, "gitdek", 12)); err != nil {
		t.Fatalf("a writer's ratifying comment should verify: %v", err)
	}
	edited := lock(1, "gitdek", 12)
	edited.Statements = append([]project.Statement(nil), edited.Statements...)
	edited.Statements[1].Says = "The buffer is roughly bounded."
	for name, tc := range map[string]struct {
		lock project.Lock
		repo string
		want string
	}{
		"edited lock":   {edited, "o/r", "not the ratified"},
		"other repo":    {lock(1, "gitdek", 12), "o/other", "not o/other"},
		"reader":        {lock(2, "reader", 12), "o/r", "only people with write access can ratify"},
		"wrong hash":    {lock(3, "gitdek", 12), "o/r", "doesn't ratify proposal"},
		"factory's own": {lock(4, "gitdek", 12), "o/r", "the factory's own"},
		"wrong author":  {func() project.Lock { l := lock(1, "gitdek", 12); l.Ratified.By = "someone"; return l }(), "o/r", "not @someone"},
		"wrong issue":   {lock(5, "gitdek", 12), "o/r", "is on #13, not #12"},
		"bot":           {lock(6, "invariant-factory[bot]", 12), "o/r", "only people ratify"},
		"missing":       {lock(99, "gitdek", 12), "o/r", "can't be read"},
	} {
		if err := VerifyRatification(ctx, gh, tc.repo, tc.lock); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v; want %q", name, err, tc.want)
		}
	}
}

// A language label decides the code's language, the project's layout, and
// how the code will be checked (D-0038 to D-0040).
func TestLanguageLabels(t *testing.T) {
	for _, tc := range []struct {
		label, lang, says, code, driver string
		files, absent                   []string
	}{
		{LabelTypeScript, "typescript", "in TypeScript, tested against the model in every state it can reach", "src", "conformance.ts",
			[]string{"package.json"}, []string{"go.mod"}},
		{LabelPython, "python", "in Python, proved with Nagini", "buffer", "conformance.py", nil, []string{"go.mod", "package.json"}},
		{"", "go", "in Go, proved with Gobra", "buffer", "", []string{"go.mod"}, []string{"package.json"}},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			r := newRig(t)
			r.form.forks = nil
			labels := []string{LabelTrigger}
			if tc.label != "" {
				labels = append(labels, tc.label)
			}
			r.gh.open(1, "gitdek", "Add a bounded buffer", "A buffer.", labels...)
			r.poll()
			if got := r.form.requests[0].Language; got != tc.lang {
				t.Fatalf("the formalizer was asked for %q; want %q", got, tc.lang)
			}
			p := r.gh.last(1)
			if !strings.Contains(p.Comment.Body, "Then I'll write the code "+tc.says+".") {
				t.Errorf("the proposal should say how the code will be checked:\n%s", p.Comment.Body)
			}
			r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Marker.Proposal.Hash, "sha256:"))
			r.poll()
			pr := r.expect(1, KindPR, append(labels, LabelPR)...)
			ref, dir := pr.Marker.Branch, pr.Marker.Project
			var m project.Manifest
			if err := json.Unmarshal([]byte(git(t, r.origin, "show", ref+":"+dir+"/.invariant/invariant.json")), &m); err != nil {
				t.Fatal(err)
			}
			if m.Language != tc.lang || m.Code != tc.code || m.Conformance != tc.driver || m.Exhaustive != (tc.lang != "go") {
				t.Errorf("manifest = %+v", m)
			}
			for _, f := range tc.files {
				git(t, r.origin, "cat-file", "-e", ref+":"+dir+"/"+f)
			}
			for _, f := range tc.absent {
				if out, err := exec.Command("git", "-C", r.origin, "cat-file", "-e", ref+":"+dir+"/"+f).CombinedOutput(); err == nil {
					t.Errorf("%s shouldn't have %s (%s)", tc.lang, f, out)
				}
			}
			if tc.lang == "typescript" {
				var pkg map[string]any
				json.Unmarshal([]byte(git(t, r.origin, "show", ref+":"+dir+"/package.json")), &pkg)
				if pkg["type"] != "module" || pkg["dependencies"] != nil {
					t.Errorf("package.json = %v", pkg)
				}
			}
		})
	}
}

// As its App's bot, the factory trusts only its own posts, and no bot's
// comment is ever a command (D-0041).
func TestAppIdentity(t *testing.T) {
	r := newRig(t)
	r.gh.me, r.f.Self = "invariant-factory[bot]", "invariant-factory[bot]"
	r.gh.perms["dependabot[bot]"] = "write"
	r.gh.open(1, "gitdek", "Add a bounded buffer", "/invariant solve")
	r.poll()
	forks := r.expect(1, KindForks, LabelAsking)
	if forks.Comment.User.Login != "invariant-factory[bot]" {
		t.Fatalf("the factory posted as %s", forks.Comment.User.Login)
	}
	// Another bot can't answer, even with write access.
	r.gh.say(1, "dependabot[bot]", "/invariant choose F1 A")
	// A person pasting a factory marker doesn't make it the factory's post.
	forged := Marker{Kind: KindProposal, Proposal: bufferProposal(t)}
	r.gh.say(1, "gitdek", post("proposal for ratification", "forged", forged))
	r.poll()
	if len(r.form.requests) != 1 {
		t.Fatalf("a bot's command or a forged marker moved the factory: %d formalizations", len(r.form.requests))
	}
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(forged.Proposal.Hash, "sha256:"))
	r.poll()
	if note := r.gh.last(1); note.Marker.Kind != KindNote || !strings.Contains(note.Comment.Body, "no proposal waiting") {
		t.Fatalf("a forged proposal must not be ratifiable:\n%s", note.Comment.Body)
	}
	r.gh.say(1, "gitdek", "/invariant choose F1 A")
	r.poll()
	if p := r.expect(1, KindProposal, LabelProposal); p.Comment.User.Login != "invariant-factory[bot]" {
		t.Fatalf("proposal by %s", p.Comment.User.Login)
	}
}

// seed puts a ratified factory project on the origin's main, as if an
// earlier issue had built it.
func seed(t *testing.T, r *rig, dir string, issue int, edits ...func(*formalize.Proposal)) project.Lock {
	t.Helper()
	p := bufferProposal(t)
	for _, edit := range edits {
		edit(p)
		if err := p.Pin(); err != nil {
			t.Fatal(err)
		}
	}
	work := t.TempDir()
	git(t, work, "clone", "--quiet", r.origin, ".")
	root := filepath.Join(work, dir)
	rat := &project.Ratification{By: "gitdek", Issue: issue, Comment: fmt.Sprintf("https://github.com/o/r/issues/%d#issuecomment-1", issue), Proposal: p.Hash}
	if err := p.Write(root, "# Add a bounded buffer\n", rat); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module m\n\ngo 1.27.1\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "buffer"), 0o755)
	os.WriteFile(filepath.Join(root, "buffer", "buffer.go"), []byte("// +gobra\n\npackage buffer\n"), 0o644)
	os.WriteFile(filepath.Join(root, "buffer", "old.go"), []byte("// +gobra\n\npackage buffer\n\n// Removed by the amendment.\n"), 0o644)
	git(t, work, "add", "-A")
	git(t, work, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "seed "+dir)
	git(t, work, "push", "--quiet", "origin", "HEAD:main")
	return project.Lock{Ratified: rat, Bounds: p.Bounds, Statements: p.Statements}
}

// The amendment drops the CanFill witness and rewords WithinCap.
func dropCanFill(c *formalize.Current) *formalize.Proposal {
	p := &formalize.Proposal{ModuleText: strings.Replace(c.ModuleText, "WithinCap == Len(buf) <= Cap", "WithinCap == Len(buf) < Cap + 1", 1)}
	p.Name, p.Slug, p.Module, p.Package = c.Manifest.Name, "bounded-buffer", "BoundedBuffer", "buffer"
	p.Bounds = c.Lock.Bounds
	for _, s := range c.Lock.Statements {
		if s.Name == "CanFill" {
			continue
		}
		if s.Name == "WithinCap" {
			s.Says = "The buffer never holds more than its capacity, counted strictly."
		}
		s.SHA256 = ""
		p.Statements = append(p.Statements, s)
	}
	if err := p.Pin(); err != nil {
		panic(err)
	}
	return p
}

// An issue that names a project amends it (D-0045): the proposal shows the
// diff, the ratification records what it amends, the code is changed rather
// than rewritten, and the amendment merges once CI's gate passes.
func TestAmendment(t *testing.T) {
	r := newRig(t)
	r.form.forks, r.form.amend = nil, dropCanFill
	dir := "examples/03-bounded-buffer"
	old := seed(t, r, dir, 1)
	r.gh.open(5, "gitdek", "Stop promising the buffer can fill", "It shouldn't have to fill.\n\nProject: examples/03-bounded-buffer\n\n/invariant solve")
	r.poll()
	proposal := r.expect(5, KindProposal, LabelProposal)
	if req := r.form.requests[0]; req.Current == nil || req.Current.Dir != dir || len(req.Current.Lock.Statements) != 5 || !strings.Contains(req.Current.ModuleText, "CanFill ==") {
		t.Fatalf("the formalizer should get the project as it stands: %+v", req.Current)
	}
	for _, want := range []string{"amendment for ratification", "It amends the statements ratified in #1", "**This removes `CanFill`.**",
		"**This changes the invariant `WithinCap`.**", "| `CanFill` | **removed** witness |", "*(was: The buffer never holds more than its capacity.)*",
		"Unchanged: `Spec`, `TypeOK` and `PutWhenFull`.", "WithinCap == Len(buf) < Cap + 1"} {
		if !strings.Contains(proposal.Comment.Body, want) {
			t.Errorf("the amendment's proposal lacks %q:\n%s", want, proposal.Comment.Body)
		}
	}
	p := proposal.Marker.Proposal
	r.gh.say(5, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Hash, "sha256:"))
	r.poll()
	pr := r.expect(5, KindPR, LabelPR)
	if !reflect.DeepEqual(r.build.amended, []bool{true}) {
		t.Errorf("the build should start from the existing code: %v", r.build.amended)
	}
	branch := pr.Marker.Branch
	if branch != "invariant/issue-5-bounded-buffer" || pr.Marker.Project != dir {
		t.Fatalf("marker = %+v", pr.Marker)
	}
	var lock project.Lock
	json.Unmarshal([]byte(git(t, r.origin, "show", branch+":"+dir+"/.invariant/ratified.lock")), &lock)
	if r := lock.Ratified; r == nil || r.Issue != 5 || r.Amends != project.ProposalHash(old.Bounds, old.Statements) || r.Previous != "#1" || len(lock.Statements) != 4 {
		t.Errorf("the amended lock = %+v", lock.Ratified)
	}
	if code := git(t, r.origin, "show", branch+":"+dir+"/buffer/buffer.go"); !strings.Contains(code, "// Amended.") {
		t.Errorf("the code should be changed, not rewritten: %q", code)
	}
	if out, err := exec.Command("git", "-C", r.origin, "cat-file", "-e", branch+":"+dir+"/buffer/old.go").CombinedOutput(); err == nil {
		t.Errorf("a file the agent dropped should be gone (%s)", out)
	}
	if log := git(t, r.origin, "log", "--format=%s", "main.."+branch); !strings.Contains(log, "Ratify the amendment for #5") {
		t.Errorf("log = %q", log)
	}
	r.gh.ci(pr.Marker.PR, "success")
	r.poll()
	r.expect(5, KindMerged, LabelMerged)
}

// An amendment drafted against a lock that has since changed ratifies
// nothing.
func TestStaleAmendment(t *testing.T) {
	r := newRig(t)
	r.form.forks, r.form.amend = nil, dropCanFill
	dir := "examples/03-bounded-buffer"
	seed(t, r, dir, 1)
	r.gh.open(5, "gitdek", "Stop promising the buffer can fill", "Project: examples/03-bounded-buffer\n\n/invariant solve")
	r.poll()
	p := r.expect(5, KindProposal, LabelProposal).Marker.Proposal
	seed(t, r, dir, 4, func(p *formalize.Proposal) { p.Statements[1].Says = "Someone else's amendment landed first." })
	r.gh.say(5, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Hash, "sha256:"))
	r.poll()
	if note := r.expect(5, KindNote, LabelProposal); !strings.Contains(note.Comment.Body, "The project changed after I drafted this amendment") {
		t.Errorf("note = %s", note.Comment.Body)
	}
	if len(r.build.built) != 0 {
		t.Error("a stale amendment must not be built")
	}
}

// A Project: line that names no existing project says where a new one goes.
func TestNewProjectAtANamedPath(t *testing.T) {
	r := newRig(t)
	r.form.forks = nil
	r.gh.open(6, "gitdek", "Formalize the factory's protocol", "Project: factory/protocol\n\n/invariant solve")
	r.poll()
	p := r.expect(6, KindProposal, LabelProposal)
	r.gh.say(6, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Marker.Proposal.Hash, "sha256:"))
	r.poll()
	if pr := r.expect(6, KindPR, LabelPR); pr.Marker.Project != "factory/protocol" {
		t.Errorf("project = %s", pr.Marker.Project)
	}
}

func TestProjectLine(t *testing.T) {
	for body, want := range map[string]string{
		"Fix it.\n\nProject: examples/04-api-rate-limiter\n":   "examples/04-api-rate-limiter",
		"project: `factory/protocol`":                          "factory/protocol",
		"```\nProject: in/a/code/block\n```\nNo project here.": "",
		"The project: is not a Project line.":                  "",
		"Nothing named.":                                       "",
	} {
		if got := projectLine(body); got != want {
			t.Errorf("projectLine(%q) = %q; want %q", body, got, want)
		}
	}
	for dir, bad := range map[string]bool{"examples/04-x": false, "factory/protocol": false, "../escape": true, "/abs": true,
		".github/workflows": true, "a/./b": true, "has space": true} {
		if got := badDir(dir) != ""; got != bad {
			t.Errorf("badDir(%q) = %v", dir, got)
		}
	}
}

func TestList(t *testing.T) {
	for names, want := range map[string]string{"": "", "a": "`a`", "a b": "`a` and `b`", "a b c": "`a`, `b` and `c`"} {
		if got := list(strings.Fields(names)); got != want {
			t.Errorf("list(%q) = %q; want %q", names, got, want)
		}
	}
}

// After a person fixes what made CI fail, /invariant retry has the factory
// look again, and it merges once the gate passes on the new head.
func TestRetryAfterAFailedGate(t *testing.T) {
	r := newRig(t)
	pr := ratified(t, r)
	r.gh.ci(pr.Marker.PR, "failure")
	r.poll()
	r.expect(1, KindFailed, LabelHumanReview)

	// Someone fixes the problem and pushes to the branch, and CI passes.
	fix := t.TempDir()
	git(t, fix, "clone", "--quiet", "--branch", pr.Marker.Branch, r.origin, ".")
	os.WriteFile(filepath.Join(fix, pr.Marker.Project, "buffer", "buffer.go"), []byte("// +gobra\n\npackage buffer\n\n// Fixed.\n"), 0o644)
	git(t, fix, "-c", "user.name=X", "-c", "user.email=x@example.com", "commit", "--quiet", "-am", "fix")
	git(t, fix, "push", "--quiet", "origin", pr.Marker.Branch)
	r.gh.ci(pr.Marker.PR, "success")
	r.poll()
	if len(r.gh.merged) != 0 {
		t.Fatal("a failed pull request waits for a person to say retry")
	}
	r.gh.say(1, "mallory", "/invariant retry")
	r.poll()
	r.expect(1, KindFailed, LabelHumanReview)

	r.gh.say(1, "gitdek", "/invariant retry")
	r.poll()
	r.expect(1, KindPR, LabelPR)
	r.poll()
	merged := r.expect(1, KindMerged, LabelMerged)
	if !reflect.DeepEqual(r.gh.merged, []int{pr.Marker.PR}) {
		t.Errorf("merged = %v", r.gh.merged)
	}

	// The merge records the issue's numbers. The failed post, the retry and
	// the merge carry the build's marker forward, and its spend and gate
	// runs still count once (D-0048, 5.4).
	drafts := 0
	for _, c := range r.gh.comments[1] {
		if m, ok := DecodeMarker(c.Body); ok && (m.Kind == KindForks || m.Kind == KindProposal) {
			drafts++
		}
	}
	n := merged.Marker.Numbers
	if n == nil || n.GateRuns != 1 || n.PeopleComments < 2 || math.Abs(n.SpendUSD-(0.10*float64(drafts)+0.25)) > 1e-9 {
		t.Fatalf("numbers %+v, with %d drafts", n, drafts)
	}
	if !strings.Contains(merged.Comment.Body, "of factory time") || !strings.Contains(merged.Comment.Body, "estimated spend was $") {
		t.Errorf("the merge comment doesn't report the numbers:\n%s", merged.Comment.Body)
	}
}

func TestNumbersOf(t *testing.T) {
	at := func(min int) string {
		return time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC).Add(time.Duration(min) * time.Minute).Format(time.RFC3339)
	}
	post := func(min int, m Marker) Post { return Post{Comment: github.Comment{CreatedAt: at(min)}, Marker: m} }
	th := Thread{
		Issue: github.Issue{CreatedAt: at(0)},
		Posts: []Post{
			post(2, Marker{Kind: KindProposal, Spend: 0.40}),
			post(12, Marker{Kind: KindRatified}),
			post(14, Marker{Kind: KindPR, Spend: 0.30, GateRuns: 2}),
			post(20, Marker{Kind: KindFailed}), // carried forward: no spend of its own
		},
		Commands: []Command{{Verb: Ratify, Comment: 7, At: at(10)}, {Verb: Retry, Comment: 8, At: at(30)}},
	}
	n := NumbersOf(th, time.Date(2026, 9, 26, 12, 34, 0, 0, time.UTC))
	// People: 2 to 10 and 20 to 30. The factory: 0 to 2, 10 to 20 and 30 to 34.
	if n.PeopleSeconds != 18*60 || n.FactorySeconds != 16*60 || n.PeopleComments != 2 || n.GateRuns != 2 || math.Abs(n.SpendUSD-0.70) > 1e-9 {
		t.Errorf("numbers %+v", n)
	}
	if s := n.Sentence(); !strings.Contains(s, "16 minutes of factory time") || !strings.Contains(s, "2 comments from people") || !strings.Contains(s, "$0.70") || !strings.Contains(s, "2 gate runs") {
		t.Errorf("sentence %q", s)
	}
}

func TestRetryWithNothingToRetry(t *testing.T) {
	r := newRig(t)
	r.gh.open(1, "gitdek", "Add a bounded buffer", "/invariant solve")
	r.poll()
	r.gh.say(1, "gitdek", "/invariant retry")
	r.poll()
	if note := r.expect(1, KindNote, LabelAsking); !strings.Contains(note.Comment.Body, "nothing that failed") {
		t.Errorf("note = %s", note.Comment.Body)
	}
}

func TestCodeLines(t *testing.T) {
	body := "Check the lease protocol.\n\nCode: src/lib\ncode: `migrations/admin/`\n\n```\nCode: not/this\n```\nProject: invariant/analysis-leases\n"
	if got := strings.Join(codeLines(body), ","); got != "src/lib,migrations/admin" {
		t.Errorf("codeLines = %s", got)
	}
	if len(codeLines("Nothing named here.")) != 0 {
		t.Error("an issue without Code: lines names no code")
	}
}
