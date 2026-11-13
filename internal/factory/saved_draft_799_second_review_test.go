package factory

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/project"
)

// These hold what a second independent review found after the first
// review's fixes. Each began as one of its probes, which reproduced the
// finding on those fixes.

// A plan carried in its comment is a proposal too, and the commonest. Its
// lock was written by joining each acceptance test file's path to the
// worktree, and checked only when read back. The probe's plan named a file
// beside the worktree: ratification wrote it, and overwrote the one planted
// there, before the check refused the plan.
func TestInlinePlan799PathsOutsideTheRepositoryAreRefused(t *testing.T) {
	name, outside, victim := review799Outside(t)
	plan := &plumbing.Plan{Name: "markers", Summary: "Read markers.", Files: []string{"internal/x/x.go"},
		Tests: []plumbing.Test{{Name: "TestX", File: "../" + name + "/evil_test.go", Says: "It reads."}},
		Sources: map[string]string{
			"../" + name + "/evil_test.go": "package x\n// written by a plan's ratification\n",
			"../" + name + "/Victim.tla":   "overwritten by a plan's ratification\n",
		}}
	p := &formalize.Proposal{Plan: plan, Hash: plan.Hash()}
	r := newRig(t)
	saved790Open(t, r)
	r.gh.say(790, r.gh.me, (Marker{Kind: KindProposal, Proposal: p, ReplyTo: []int64{0}}).encode())
	review799Refused(t, r, p.Hash, "isn't a path inside the repository")
	review799Untouched(t, outside, victim)
}

// An amendment keeps its project's manifest, and writes the one it carries.
// Nothing compared that with the project's: the probe's amendment of a Go
// project said TypeScript, with its code and its conformance driver beside
// the agent's workspace. Ratification committed that manifest, and the
// build's first steps wrote the driver's harness and made the directory
// there. A saved record was no better, since its manifest is compared only
// with the baseline saved beside it. Either way the amendment no longer
// amends the project as it is, and is answered as a stale one.
func TestAmendment799KeepsItsProjectsManifest(t *testing.T) {
	for _, saved := range []bool{false, true} {
		carried := "carried in its comment"
		if saved {
			carried = "read from a saved record"
		}
		t.Run(carried, func(t *testing.T) {
			name, outside, victim := review799Outside(t)
			r := newRig(t)
			saved790Open(t, r)
			result := review799Amendment(t, r, "examples/saved-reader")
			p := result.Proposal
			for _, m := range []*project.Manifest{&p.Target.Manifest, &p.Target.Baseline.Manifest} {
				m.Language, m.Conformance, m.Code = "typescript", "../"+name+"/conformance.ts", "../"+name+"/code"
			}
			if saved {
				if err := validateDraftResult(result); err != nil {
					t.Fatalf("the fixture is refused as a saved record, before ratification: %v", err)
				}
				saved790Post(t, r, saved790Save(t, r, result, "draft-issue"), p)
			} else {
				r.gh.say(790, r.gh.me, (Marker{Kind: KindProposal, Proposal: p, ReplyTo: []int64{0}}).encode())
			}
			saved790Fresh(t, r)
			r.gh.say(790, "gitdek", "/invariant ratify "+p.Hash)
			before := len(r.gh.comments[790])
			r.poll()
			note := r.gh.last(790)
			if note.Marker.Kind != KindNote || !strings.Contains(note.Comment.Body, "The project changed") {
				t.Fatalf("an amendment with a manifest that isn't its project's wasn't answered as stale:\n%s", note.Comment.Body)
			}
			r.poll()
			if len(r.gh.comments[790]) != before+1 || len(r.build.built) != 0 || len(r.gh.prs) != 0 {
				t.Fatal("an amendment with a manifest that isn't its project's was ratified, built or answered twice")
			}
			if branches := git(t, r.origin, "branch", "--list", "invariant/issue-790-*"); strings.TrimSpace(branches) != "" {
				t.Fatalf("a ratification was pushed: %s", branches)
			}
			review799Untouched(t, outside, victim)
		})
	}
}

// A plan's post shows its acceptance tests' source, and a test about
// markers holds one as a literal. That put a whole marker in the post's
// text, before its own, and the first one was read: the probe's plan post
// read as the marker in its text, or was refused as damaged when that one
// didn't decode. A post's own marker is its last. And the same post cut
// before its own marker is damaged: the one in its text never stands in.
func TestPlanPost799ReadsItsOwnMarker(t *testing.T) {
	f := &Factory{Self: "invariant-code-factory[bot]"}
	for _, c := range []struct{ name, literal string }{
		{"a literal that is no marker", `strings.Cut(body, "<!-- invariant:")`},
		{"a literal marker that doesn't decode", `r.gh.say(42, "stranger", "hi <!-- invariant:e30= -->")`},
		{"a literal marker that decodes", `golden := "` + (Marker{Kind: KindMerged, PR: 1}).encode() + `"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := "package x\n\nfunc TestX(t *testing.T) {\n\t" + c.literal + "\n}\n"
			p := &formalize.Proposal{Hash: "sha256:" + strings.Repeat("ab", 32), Plan: &plumbing.Plan{Name: "markers", Summary: "Read markers.",
				Files: []string{"internal/x/x.go"}, Tests: []plumbing.Test{{Name: "TestX", File: "internal/x/x_test.go", Says: "It reads."}},
				Sources: map[string]string{"internal/x/x_test.go": source}}}
			body := planComment(p, Marker{Kind: KindProposal, Proposal: p, ReplyTo: []int64{0}})
			own := strings.LastIndex(body, markerStart)
			if !strings.Contains(body[:own], c.literal) {
				t.Fatal("the fixture's post doesn't show the literal in its text")
			}
			comment := github.Comment{ID: 7, Body: body, User: github.User{Login: f.Self, Type: "Bot"}}
			post, ok, err := f.markPost(comment)
			if err != nil || !ok || post.Marker.Kind != KindProposal || post.Marker.Proposal == nil || post.Marker.Proposal.Hash != p.Hash {
				t.Fatalf("the factory's own plan post reads as %q (a post: %v): %v", post.Marker.Kind, ok, err)
			}
			// The dashboard, and a factory with no App, read the same post.
			if m, ok := DecodeMarker(body); !ok || m.Kind != KindProposal {
				t.Fatalf("read by its marker alone, the plan post is %q (a marker: %v)", m.Kind, ok)
			}
			comment.Body = body[:own]
			if post, ok, err := f.markPost(comment); err == nil || !strings.Contains(err.Error(), "restore the comment") {
				t.Fatalf("cut before its own marker, the post reads as %q (a post: %v), not as damaged: %v", post.Marker.Kind, ok, err)
			}
		})
	}
}

// A revision that fails posts the draft that failed. A post leaves out a
// draft it can't hold, and when the thread's state was a saved record, as a
// draft too large for a comment is, the record's name went with it: the
// probe's next revision started from nothing, with no draft, no record and
// no baseline. Such a post keeps the proposal before the failed draft, with
// its record. A failed draft that fits is carried, as before.
func TestFailedRevision799OfASavedDraftKeepsWhatTheNextNeeds(t *testing.T) {
	for _, large := range []bool{false, true} {
		name := "a draft that fits a comment"
		if large {
			name = "a draft too large for a comment"
		}
		t.Run(name, func(t *testing.T) {
			result := saved790Result(t, large)
			r := newRig(t)
			saved790Open(t, r)
			saved790Post(t, r, saved790Save(t, r, result, "draft-issue"), result.Proposal)
			saved790Fresh(t, r)
			saved790Read(t, r, result.Proposal)
			candidate := *result.Proposal
			candidate.ModuleText = strings.Replace(result.Proposal.ModuleText, "====", "\\* failed candidate\n====", 1)
			var previous []*formalize.Proposal
			r.f.Formalizer = reader603Formalizer{run: func(_ context.Context, q formalize.Request, _ string) (*formalize.Result, error) {
				previous = append(previous, q.Previous)
				c := candidate
				return &formalize.Result{Proposal: &c, Problem: "TLC found a deadlock."}, nil
			}}
			r.gh.say(790, "gitdek", "Fix it.\n/invariant revise")
			r.poll()
			stuck := r.expect(790, KindStuck, LabelHumanReview)
			if large != (stuck.Marker.ProposalRef != nil) || large == (stuck.Marker.Proposal != nil) {
				t.Fatalf("the failed revision's post names a saved record: %v, and carries a proposal: %v", stuck.Marker.ProposalRef != nil, stuck.Marker.Proposal != nil)
			}
			r.gh.say(790, "gitdek", "Try again.\n/invariant revise")
			r.poll()
			if len(previous) != 2 || previous[1] == nil {
				t.Fatalf("the next revision started from nothing (%d drafts)", len(previous))
			}
			switch failed := strings.Contains(previous[1].ModuleText, "failed candidate"); {
			case large && (failed || !bytes.Equal(reader603JSON(t, previous[1]), reader603JSON(t, result.Proposal))):
				t.Fatal("the next revision didn't start from the saved proposal before the draft that failed")
			case !large && !failed:
				t.Fatal("the next revision didn't start from the draft that failed")
			}
		})
	}
}

// An amendment's slug is its project's directory's name, and names its
// branch. Held to one plain name, amendments of directories an issue's
// Project line may name, and git can name a branch after, never ratified:
// the probe's three were refused at every poll, with nothing said.
func TestAmendment799OfADirectoryGitCanNameRatifies(t *testing.T) {
	for _, dir := range []string{"examples/_saved", "examples/@saved", "examples/saved+reader"} {
		t.Run(dir, func(t *testing.T) {
			if problem := badDir(dir); problem != "" {
				t.Fatalf("an issue can't name %s: %s", dir, problem)
			}
			r := newRig(t)
			saved790Open(t, r)
			p := review799Amendment(t, r, dir).Proposal
			r.gh.say(790, r.gh.me, (Marker{Kind: KindProposal, Proposal: p, ReplyTo: []int64{0}}).encode())
			saved790Fresh(t, r)
			r.gh.say(790, "gitdek", "/invariant ratify "+p.Hash)
			r.poll()
			if len(r.build.built) != 1 {
				t.Fatalf("an amendment of %s didn't ratify and build:\n%s", dir, r.gh.last(790).Comment.Body)
			}
		})
	}
}

// What a slug may be is what git takes at the end of a branch's name, and
// git is asked.
func TestBranchable799(t *testing.T) {
	for _, slug := range []string{
		"a", "0", "A.b", "a-", "-a", ".a", "@", "_saved", "@saved", "saved+reader", "saved_reader", "03-buffer", "HEAD", "a.lock.b", "é",
		"", "a..b", "a.", "a.lock", "a/b", "a b", "a\n", "a\tb", "a\x7fb", "a@{b", "a~1", "a^", "a:b", "a?", "a*", "a[b", `a\b`,
	} {
		err := exec.Command("git", "check-ref-format", "refs/heads/invariant/issue-1-"+slug).Run()
		// git takes nothing after the issue's number as a name too, and a
		// slash as the start of another name. A slug is one name.
		want := err == nil && slug != "" && !strings.Contains(slug, "/")
		if got := branchable(slug); got != want {
			t.Errorf("branchable(%q) = %v, and git says %v", slug, got, err)
		}
	}
}
