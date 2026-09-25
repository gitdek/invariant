package factory

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"

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
