package factory

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/verify"
)

// These hold what #799's independent review found past the two defects the
// factory's own review named. Each began as a probe that reproduced a
// finding: a saved or inline proposal that wrote, and removed files,
// outside its worktree.

func review799ReaderFingerprint(t *testing.T, p *formalize.Proposal) string {
	t.Helper()
	b, err := json.Marshal(struct {
		Version    int                         `json:"version"`
		Module     string                      `json:"module"`
		Model      string                      `json:"model"`
		Includes   map[string]string           `json:"includes,omitempty"`
		View       string                      `json:"view"`
		Bounds     map[string]string           `json:"bounds"`
		Statements []project.Statement         `json:"statements"`
		Chart      *statechart.Result          `json:"chart,omitempty"`
		Limitation *formalize.ReaderLimitation `json:"limitation,omitempty"`
		Comparison *statechart.Comparison      `json:"comparison,omitempty"`
	}{p.Reader.Version, p.Module, p.ModuleText, p.Includes, p.View, p.Bounds, p.Statements, p.Reader.Chart, p.Reader.Limitation, p.Reader.Comparison})
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(b))
}

// review799Rename gives a checked result another module name and recomputes
// every fingerprint the saved-draft validator recomputes, as anyone holding
// the source can. So a digest that matches says nothing about the name.
func review799Rename(t *testing.T, result *formalize.Result, module string) {
	t.Helper()
	p, r := result.Proposal, result.Report
	p.Module = module
	for _, c := range []*statechart.Result{p.Reader.Chart, r.Reader.Chart} {
		if c != nil {
			c.Report.Source.Module = module
		}
	}
	for _, l := range []*statechart.Limitation{p.Reader.Limitation, r.Reader.Limitation} {
		if l != nil {
			l.Source.Module = module
		}
	}
	r.Reader.Inputs.Module = module
	p.Reader.Fingerprint = review799ReaderFingerprint(t, p)
	rr := *r.Reader
	rr.Fingerprint = ""
	b, err := json.Marshal(struct {
		Reader verify.Reader `json:"reader"`
		Lock   project.Lock  `json:"lock"`
	}{rr, project.Lock{Bounds: p.Bounds, Statements: p.Statements}})
	if err != nil {
		t.Fatal(err)
	}
	r.Reader.Fingerprint = draftDigest(b)
	r.Fingerprint = verify.Fingerprint(*r)
}

// review799Outside is a directory beside the factory's worktrees, with a
// model file in it that belongs to someone else.
func review799Outside(t *testing.T) (name, outside, victim string) {
	t.Helper()
	name = fmt.Sprintf("review799-%d-%s", os.Getpid(), strings.NewReplacer("/", "-", " ", "-").Replace(t.Name()))
	outside = filepath.Join(os.TempDir(), name)
	os.RemoveAll(outside)
	t.Cleanup(func() { os.RemoveAll(outside) })
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	victim = filepath.Join(outside, "Victim.tla")
	if err := os.WriteFile(victim, []byte("someone else's file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return name, outside, victim
}

// review799Untouched says the directory outside the worktree holds what it
// held, and nothing more.
func review799Untouched(t *testing.T, outside, victim string) {
	t.Helper()
	if b, err := os.ReadFile(victim); err != nil || string(b) != "someone else's file\n" {
		t.Fatalf("the file outside the worktree was removed or changed: %v", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("ratification wrote outside its worktree: %v (%v)", names, err)
	}
}

// review799Pending says a ratification is still waiting: the poll says why,
// and nothing was answered, built or opened.
func review799Pending(t *testing.T, r *rig, hash, problem string) {
	t.Helper()
	r.gh.say(790, "gitdek", "/invariant ratify "+hash)
	before := len(r.gh.comments[790])
	if err := r.f.Poll(t.Context()); err == nil || !strings.Contains(err.Error(), problem) {
		t.Fatalf("the poll doesn't say %q: %v", problem, err)
	}
	if len(r.gh.comments[790]) != before || len(r.build.built) != 0 || len(r.gh.prs) != 0 {
		t.Fatal("an unsafe proposal consumed the ratification, started a build or opened a pull request")
	}
}

// A saved proposal's module name is joined to the project's directory when
// ratification writes the model, and for a model with includes every other
// .tla file beside it is removed. A name that climbs out, with every
// fingerprint recomputed to match, was accepted: the review's probe wrote
// an include five directories up and removed the file planted there.
func TestSavedDraft799EscapingModuleStaysPending(t *testing.T) {
	name, outside, victim := review799Outside(t)
	result := saved790Result(t, false)
	review799Rename(t, result, "../../../../../"+name+"/Evil")
	if err := validateDraftResult(result); err == nil || !strings.Contains(err.Error(), "module") {
		t.Fatalf("a saved module that climbs out of its project wasn't refused as one: %v", err)
	}
	r := newRig(t)
	saved790Open(t, r)
	ref := saved790Save(t, r, result, "draft-issue")
	saved790Post(t, r, ref, result.Proposal)
	saved790Fresh(t, r)
	if _, err := r.f.read(t.Context(), *r.gh.issues[790]); err == nil || !strings.Contains(err.Error(), "module") {
		t.Fatalf("the saved proposal was read as usable: %v", err)
	}
	review799Pending(t, r, result.Proposal.Hash, "module")
	review799Untouched(t, outside, victim)
}

func review799Amendment(t *testing.T, r *rig, dir string) *formalize.Result {
	t.Helper()
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	old, _ := reader603Checked(t, ws, tc)
	work := t.TempDir()
	git(t, work, "clone", "--quiet", r.origin, ".")
	if err := old.Write(filepath.Join(work, dir), "original", &project.Ratification{Issue: 600, By: "gitdek", Proposal: old.Hash}); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "-A")
	git(t, work, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "seed baseline")
	git(t, work, "push", "--quiet", "origin", "HEAD:main")
	cur, err := r.f.current(t.Context(), dir)
	if err != nil || cur == nil {
		t.Fatalf("capture amendment target: %v", err)
	}
	authority := formalize.Request{Current: cur}.CheckContext()
	reader603Write(t, filepath.Join(ws, "view.json"), strings.ReplaceAll(reader603View, "Working", "Revised working"))
	p, report, err := formalize.CheckWithContext(t.Context(), ws, tc, authority)
	if err != nil || p == nil || report == nil || !report.Passed {
		t.Fatalf("check amendment: %v", err)
	}
	if err := p.Amend(cur); err != nil {
		t.Fatal(err)
	}
	r.gh.issues[790].Body = "Project: " + dir + "\n/invariant solve"
	return &formalize.Result{Proposal: p, Report: report, CheckContext: &authority}
}

// An amendment keeps its project's manifest, and the manifest's module path
// is where the amended model is written. The saved capture carries both the
// manifest and the baseline it's compared with, so changing both alike
// passed: the probe wrote the model three directories up and removed the
// file planted there.
func TestSavedDraft799EscapingAmendmentModuleStaysPending(t *testing.T) {
	name, outside, victim := review799Outside(t)
	r := newRig(t)
	saved790Open(t, r)
	result := review799Amendment(t, r, "examples/saved-reader")
	if err := validateDraftResult(result); err != nil {
		t.Fatalf("the amendment as it was checked is refused: %v", err)
	}
	p := result.Proposal
	evil := "../../../" + name + "/Reader603.tla"
	p.Target.Manifest.Module = evil
	p.Target.Baseline.Manifest.Module = evil
	if err := validateDraftResult(result); err == nil || !strings.Contains(err.Error(), "module") {
		t.Fatalf("a saved amendment whose module climbs out of its project wasn't refused as one: %v", err)
	}
	ref := saved790Save(t, r, result, "draft-issue")
	saved790Post(t, r, ref, p)
	saved790Fresh(t, r)
	review799Pending(t, r, p.Hash, "module")
	review799Untouched(t, outside, victim)
}

// A saved amendment's slug comes from its project's directory, which an
// issue's Project line may name in any plain spelling. Held to kebab case,
// as a new project's slug is, a saved amendment of examples/saved_reader
// never ratified, though the same proposal carried in its comment did.
func TestSavedDraft799AmendmentKeepsItsDirectorysName(t *testing.T) {
	r := newRig(t)
	saved790Open(t, r)
	dir := "examples/saved_reader"
	result := review799Amendment(t, r, dir)
	if result.Proposal.Slug != "saved_reader" {
		t.Fatalf("the fixture's slug is %q, not its directory's name", result.Proposal.Slug)
	}
	if err := validateDraftResult(result); err != nil {
		t.Fatalf("a saved amendment of %s is refused: %v", dir, err)
	}
	ref := saved790Save(t, r, result, "draft-issue")
	saved790Post(t, r, ref, result.Proposal)
	saved790Fresh(t, r)
	saved790Read(t, r, result.Proposal)
	r.gh.say(790, "gitdek", "/invariant ratify "+result.Proposal.Hash)
	r.poll()
	if len(r.build.built) != 1 {
		t.Fatalf("the saved amendment of %s didn't build: %s", dir, r.gh.last(790).Comment.Body)
	}
}

// review799Refused says a ratification was answered with why nothing was
// ratified, once: nothing was built or opened, and the next poll has
// nothing more to say.
func review799Refused(t *testing.T, r *rig, hash, problem string) {
	t.Helper()
	r.gh.say(790, "gitdek", "/invariant ratify "+hash)
	before := len(r.gh.comments[790])
	r.poll()
	note := r.gh.last(790)
	if note.Marker.Kind != KindNote || !strings.Contains(note.Comment.Body, problem) || !strings.Contains(note.Comment.Body, "haven't written anything") {
		t.Fatalf("the ratification wasn't answered with %q:\n%s", problem, note.Comment.Body)
	}
	r.poll()
	if len(r.gh.comments[790]) != before+1 || len(r.build.built) != 0 || len(r.gh.prs) != 0 {
		t.Fatalf("an unsafe proposal was answered %d times, started a build or opened a pull request", len(r.gh.comments[790])-before)
	}
}

// A proposal carried in its comment met none of these checks: its directory,
// module, slug and package went to ratification as they were. The probes'
// inline proposal with a directory beside the worktree wrote a
// ratified.lock there, and one with a package beside the agent's workspace
// had the build make a directory there. Each is refused before anything is
// written, in a note the person who ratified it reads, not at every poll in
// the watcher's log.
func TestInlineProposal799UnsafeDestinationsAreRefused(t *testing.T) {
	for _, c := range []struct {
		name, problem string
		edit          func(t *testing.T, result *formalize.Result, outside string)
	}{
		{"directory", "project directory", func(t *testing.T, result *formalize.Result, outside string) { result.Proposal.Dir = "../" + outside }},
		{"module", "module", func(t *testing.T, result *formalize.Result, outside string) {
			review799Rename(t, result, "../../../../../"+outside+"/Evil")
		}},
		{"slug", "proposal slug", func(t *testing.T, result *formalize.Result, outside string) {
			result.Proposal.Slug = "a/../../" + outside
		}},
		{"package", "package", func(t *testing.T, result *formalize.Result, outside string) {
			result.Proposal.Package = "../" + outside + "/pkg"
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			name, outside, victim := review799Outside(t)
			result := saved790Result(t, false)
			c.edit(t, result, name)
			r := newRig(t)
			saved790Open(t, r)
			r.gh.say(790, r.gh.me, (Marker{Kind: KindProposal, Proposal: result.Proposal, ReplyTo: []int64{0}}).encode())
			review799Refused(t, r, result.Proposal.Hash, c.problem)
			review799Untouched(t, outside, victim)
		})
	}
}

// What a project's directory may be, for an issue's Project line and for
// any proposal: a clean path inside the repository, outside what the
// repository keeps for itself, that no tool reads as anything else.
func TestBadDir799(t *testing.T) {
	for dir, bad := range map[string]bool{
		"examples/03-buffer":         false,
		"invariant/05-statue":        false,
		"statue-service/verify":      false,
		"examples/saved_reader":      false,
		"app/[slug]":                 false,
		"..":                         true,
		"../x":                       true,
		"/tmp/x":                     true,
		".":                          true,
		"examples//x":                true,
		"examples/x/":                true,
		"examples/x/../../../y":      true,
		`examples\..\..\x`:           true,
		".github":                    true,
		".github/workflows":          true,
		".GitHub/workflows":          true,
		".git":                       true,
		".git/hooks":                 true,
		".invariant":                 true,
		".invariant/plans":           true,
		"decisions":                  true,
		"decisions/journal":          true,
		"examples/a b":               true,
		"examples/a\tb":              true,
		"examples/a\nb":              true,
		"examples/a\x00b":            true,
		"-rf":                        true,
		"examples/-rf":               true,
		"examples/.github":           false,
		"examples/decisions-journal": false,
	} {
		if problem := badDir(dir); (problem != "") != bad {
			t.Errorf("badDir(%q) = %q, want refused=%v", dir, problem, bad)
		}
	}
}

// A revision that comes back with a draft and a problem posts that draft
// with its failure, and the next revision starts from it. #790's build made
// such a post carry the proposal before it instead, which no ratified test
// asked for: a person revising then saw the old proposal under the new
// failure, and the agent started over from it.
func TestFailedRevision799CarriesTheDraftThatFailed(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	first, _ := reader603Checked(t, ws, tc)
	r := newRig(t)
	r.gh.open(790, "gitdek", "Revise a draft", "A lifecycle.\n/invariant solve")
	r.gh.say(790, r.gh.me, (Marker{Kind: KindProposal, Proposal: first, ReplyTo: []int64{0}}).encode())
	candidate := *first
	candidate.ModuleText = strings.Replace(first.ModuleText, "====", "\\* failed candidate\n====", 1)
	var previous []*formalize.Proposal
	r.f.Formalizer = reader603Formalizer{run: func(_ context.Context, q formalize.Request, _ string) (*formalize.Result, error) {
		previous = append(previous, q.Previous)
		c := candidate
		return &formalize.Result{Proposal: &c, Problem: "TLC found a deadlock."}, nil
	}}
	r.gh.say(790, "gitdek", "Fix it.\n/invariant revise")
	r.poll()
	stuck := r.expect(790, KindStuck, LabelHumanReview)
	if q := stuck.Marker.Proposal; q == nil || !strings.Contains(q.ModuleText, "failed candidate") {
		t.Fatalf("the failed revision's post doesn't carry the draft that failed: %+v", q)
	}
	r.gh.say(790, "gitdek", "Try again.\n/invariant revise")
	r.poll()
	if len(previous) != 2 || previous[0] == nil || strings.Contains(previous[0].ModuleText, "failed candidate") {
		t.Fatalf("the first revision didn't start from the proposal it revises: %d requests", len(previous))
	}
	if previous[1] == nil || !strings.Contains(previous[1].ModuleText, "failed candidate") {
		t.Fatal("the second revision didn't start from the draft that failed")
	}
}

func review799Gzip(t *testing.T, b []byte) string {
	t.Helper()
	var z bytes.Buffer
	w := gzip.NewWriter(&z)
	w.Write(b)
	w.Close()
	return base64.StdEncoding.EncodeToString(z.Bytes())
}

// A marker of the factory's own that no longer reads blocks, wherever the
// damage is. #790's hand fix caught damage inside the JSON after the
// reference's key. A comment cut before that key, base64 cut short, with or
// without its closing, and one changed character all still read as no
// marker, so the earlier proposal answered a pending ratification: the
// review's probe built it. The marker here is the factory's own encoding,
// gzipped, which puts the reference after everything a cut loses last.
func TestSavedDraft799DamagedMarkersStayPending(t *testing.T) {
	result := saved790Result(t, false)
	r := newRig(t)
	saved790Open(t, r)
	if r.f.Self == "" {
		t.Fatal("the fixture reads as no App, where any commenter's marker counts")
	}
	earlier := r.gh.say(790, r.gh.me, (Marker{Kind: KindProposal, Proposal: result.Proposal, ReplyTo: []int64{0}}).encode())
	ref := saved790Save(t, r, result, "draft-issue")
	posted := saved790Post(t, r, ref, result.Proposal)
	index := len(r.gh.comments[790]) - 1
	saved790Fresh(t, r)
	saved790Read(t, r, result.Proposal)
	r.gh.say(790, "gitdek", "/invariant ratify "+result.Proposal.Hash)
	envelope, err := json.Marshal(Marker{Kind: KindProposal, ReplyTo: []int64{0}, ProposalRef: reader603JSON(t, ref)})
	if err != nil {
		t.Fatal(err)
	}
	at := bytes.Index(envelope, []byte(`"proposal_ref"`))
	if at < 0 {
		t.Fatal("the fixture's marker has no reference")
	}
	encoded := review799Gzip(t, envelope)
	changed := byte('A')
	if encoded[len(encoded)/2] == 'A' {
		changed = 'B'
	}
	inline := (Marker{Kind: KindProposal, Proposal: result.Proposal, ReplyTo: []int64{0}}).encode()
	head := "◉ **Invariant** · saved draft\n\n"
	whole := markerStart + "z:" + encoded + " -->"
	for _, c := range []struct{ name, body string }{
		// What the second review found still fell back: a cut that leaves
		// nothing of the marker, which is where a cut at a random place in
		// a long post lands, and the edit a person makes, which removes the
		// hidden block whole. And a marker that no longer ends its comment.
		{"cut inside its visible text", head + "Here's what I propose must always be tr"},
		{"cut inside its marker's opening", head + "Here's what I propose.\n\n<!-- invar"},
		{"its marker removed", head + "Here's what I propose."},
		{"words after its marker", head + whole + "\n\nedited: see the other issue\n"},
		{"its opening words removed and its marker cut short", markerStart + "z:" + encoded[:len(encoded)/2]},
		{"JSON cut before the reference", head + markerStart + base64.StdEncoding.EncodeToString(envelope[:at]) + " -->"},
		{"gzipped JSON cut before the reference", head + markerStart + "z:" + review799Gzip(t, envelope[:at]) + " -->"},
		{"base64 cut short, closing kept", head + markerStart + "z:" + encoded[:len(encoded)-8] + " -->"},
		{"base64 cut short, closing lost", head + markerStart + "z:" + encoded[:len(encoded)/2]},
		{"one character changed", head + markerStart + "z:" + encoded[:len(encoded)/2] + string(changed) + encoded[len(encoded)/2+1:] + " -->"},
		{"an inline proposal cut short", head + inline[:len(inline)/2]},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, ok := endMarker(c.body); ok {
				t.Fatal("the fixture ends with a marker that reads")
			}
			r.gh.comments[790][index].Body = c.body
			before := len(r.gh.comments[790])
			if _, err := r.f.read(t.Context(), *r.gh.issues[790]); err == nil || !strings.Contains(err.Error(), "marker") {
				t.Fatalf("a damaged marker fell back to the earlier proposal: %v", err)
			}
			if _, err := r.f.postsOn(t.Context(), 790); err == nil {
				t.Fatal("the parent-step reader read past a damaged marker")
			}
			if err := r.f.Poll(t.Context()); err == nil || !strings.Contains(err.Error(), "restore the comment") {
				t.Fatalf("a damaged marker has no diagnostic a person can act on: %v", err)
			}
			if len(r.gh.comments[790]) != before || len(r.form.requests) != 0 || len(r.build.built) != 0 || len(r.gh.prs) != 0 {
				t.Fatal("a damaged marker consumed the pending ratification or started work")
			}
		})
	}
	// A comment with nothing left that says it was a post is one a person
	// emptied, which is how the diagnostic says to fall back on purpose.
	for _, body := range []string{"", "(removed)"} {
		r.gh.comments[790][index].Body = body
		thread, err := r.f.read(t.Context(), *r.gh.issues[790])
		if err != nil {
			t.Fatalf("an emptied comment (%q) stops the issue: %v", body, err)
		}
		if state, ok := thread.State(); !ok || state.Comment.ID != earlier.ID {
			t.Fatalf("an emptied comment (%q) didn't fall back to the post before it", body)
		}
	}
	// A stranger's comment with the same damage is nobody's post.
	stranger := r.gh.say(790, "stranger", head+markerStart+"z:"+encoded[:len(encoded)/2])
	r.gh.comments[790][index].Body = posted.Body
	if _, err := r.f.read(t.Context(), *r.gh.issues[790]); err != nil {
		t.Fatalf("a stranger's damaged marker blocks the issue: %v (comment %d)", err, stranger.ID)
	}
	// Restored, the same pending ratification runs, once.
	r.poll()
	if len(r.build.built) != 1 {
		t.Fatal("the restored marker didn't let the pending ratification run")
	}
	r.poll()
	if len(r.build.built) != 1 {
		t.Fatal("the restored ratification built twice")
	}
}

// Without an App, the factory posts as whoever gh is signed in as, and its
// posts are told apart by their markers alone. There a marker that doesn't
// read is no post, as before: nobody's comment can be known to be the
// factory's, so nobody's damage may stop an issue.
func TestSavedDraft799DamagedMarkerIsNoPostWithoutAnApp(t *testing.T) {
	r := newRig(t)
	if r.f.Self != "" {
		t.Fatal("the rig has a login")
	}
	r.gh.open(42, "gitdek", "Something", "A lifecycle.\n/invariant solve")
	r.poll()
	r.gh.say(42, "stranger", "hi "+markerStart+"z:"+base64.StdEncoding.EncodeToString([]byte("not gzip"))+" -->")
	if _, err := r.f.read(t.Context(), *r.gh.issues[42]); err != nil {
		t.Fatalf("a marker that doesn't read stopped an issue with no App: %v", err)
	}
}

// review799FlakyRepo fails a saved record's lookup when told to, as a
// moment's network trouble does.
type review799FlakyRepo struct {
	Repo
	fail func(issue int, step string) bool
}

func (r *review799FlakyRepo) Run(ctx context.Context, issue int, step string) (RunState, string, error) {
	if r.fail != nil && r.fail(issue, step) {
		return RunNone, "", errors.New("git ls-remote: transient network failure")
	}
	return r.Repo.Run(ctx, issue, step)
}

// A build that has finished isn't lost to one failed lookup of a saved
// record. Asking whether a later post superseded the build reads every
// saved record on the issue, between the agent's run and the store of its
// result, and #790's plan has that reader refuse a record it can't read.
// One lookup failing there left the run with no result: the review's probe
// had the next poll report the finished build as stopped partway, with no
// pull request, for a retry to build again. The build now asks again.
func TestSavedDraft799FinishedBuildSurvivesALostLookup(t *testing.T) {
	result := saved790Result(t, false)
	r := newRig(t)
	saved790Open(t, r)
	ref := saved790Save(t, r, result, "draft-issue")
	saved790Post(t, r, ref, result.Proposal)
	saved790Fresh(t, r)
	saved790Read(t, r, result.Proposal)
	failures := 0
	flaky := &review799FlakyRepo{Repo: r.f.Repo}
	flaky.fail = func(issue int, step string) bool {
		// The first lookup of the saved draft after the agent has built
		// fails, as a moment's network trouble does.
		if step == "draft-issue" && len(r.build.built) == 1 && failures == 0 {
			failures++
			return true
		}
		return false
	}
	r.f.Repo = flaky
	r.gh.say(790, "gitdek", "/invariant ratify "+result.Proposal.Hash)
	if err := r.f.Poll(t.Context()); err != nil {
		t.Logf("the poll that built: %v", err)
	}
	flaky.fail = nil
	r.poll()
	last := r.gh.last(790)
	if len(r.build.built) != 1 || failures != 1 {
		t.Fatalf("the agent built %d times, and %d lookups were lost", len(r.build.built), failures)
	}
	if last.Marker.Kind == KindFailed {
		t.Fatalf("a finished build was reported as failed, %s, after %d lost lookups: %s", last.Marker.Why(), failures, last.Comment.Body)
	}
	if len(r.gh.prs) != 1 {
		t.Fatalf("the finished build opened %d pull requests, after %d lost lookups", len(r.gh.prs), failures)
	}
}
