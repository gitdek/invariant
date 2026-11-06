package factory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/synth"
)

// A revision of an amendment whose project another ratification changed on
// the base branch amends the project as it is now, as the stale note
// promises, so it can be ratified (#648).
func TestStaleAmendmentRevisionAmendsTheProjectAsItIsNow(t *testing.T) {
	r := newRig(t)
	r.form.forks, r.form.amend = nil, dropCanFill
	dir := "examples/03-bounded-buffer"
	seed(t, r, dir, 1)
	r.gh.open(5, "gitdek", "Stop promising the buffer can fill", "Project: "+dir+"\n\n/invariant solve")
	r.poll()
	p := r.expect(5, KindProposal, LabelProposal).Marker.Proposal
	seed(t, r, dir, 4, func(p *formalize.Proposal) { p.Statements[1].Says = "Someone else's amendment landed first." })
	r.gh.say(5, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Hash, "sha256:"))
	r.poll()
	r.expect(5, KindNote, LabelProposal)
	r.gh.say(5, "gitdek", "/invariant revise")
	r.poll()
	q := r.expect(5, KindProposal, LabelProposal).Marker.Proposal
	if q.Target == nil || q.Target.Amends == p.Target.Amends {
		t.Fatal("the revision amended the stale captured lock again")
	}
	before := len(r.gh.comments[5])
	r.gh.say(5, "gitdek", "/invariant ratify "+strings.TrimPrefix(q.Hash, "sha256:"))
	r.poll()
	ratified := false
	for _, c := range r.gh.comments[5][before:] {
		if strings.Contains(c.Body, "The project changed after I drafted this amendment") {
			t.Fatal("the revised amendment was stale again")
		}
		if m, ok := DecodeMarker(c.Body); ok && m.Kind == KindRatified {
			ratified = true
		}
	}
	if !ratified {
		t.Fatal("the revised amendment wasn't ratified")
	}
}

// A draft refused for its evidence isn't a selected model, so its post keeps
// the previous proposal, and the next revision still compares with it
// rather than publishing without the comparison it needs (#648).
func TestRefusedRevisionKeepsItsBaseline(t *testing.T) {
	tc, _ := reader603Tools(t)
	r := newRig(t)
	r.f.Formalizer = formalize.Formalizer{Toolchain: tc, Backend: comparison648Agent{run: func(_ context.Context, j synth.Job) (synth.Usage, error) {
		reader603Workspace(t, j.Workspace)
		return synth.Usage{}, nil
	}}}
	r.gh.open(648, "gitdek", "Compare revisions", "A reader chart.\n/invariant solve")
	r.poll()
	first := r.expect(648, KindProposal, LabelProposal)
	// Each revision returns a draft without the comparison it needs.
	baselines := 0
	r.f.Formalizer = reader603Formalizer{run: func(_ context.Context, req formalize.Request, _ string) (*formalize.Result, error) {
		if req.CheckContext().Baseline != nil {
			baselines++
		}
		return &formalize.Result{Proposal: first.Marker.Proposal}, nil
	}}
	r.gh.say(648, "gitdek", "Revise.\n/invariant revise")
	r.poll()
	stuck := r.expect(648, KindStuck, LabelHumanReview)
	if stuck.Marker.Proposal == nil || stuck.Marker.Proposal.Hash != first.Marker.Proposal.Hash {
		t.Fatal("the refusal dropped the previous proposal")
	}
	r.gh.say(648, "gitdek", "Revise again.\n/invariant revise")
	r.poll()
	r.expect(648, KindStuck, LabelHumanReview)
	if baselines != 2 {
		t.Fatalf("%d of 2 revisions compared with the selected baseline", baselines)
	}
}

// When a writer points an amendment issue at a directory with no project,
// the revision drafts a new project there instead of amending the old one
// (#648).
func TestProjectLineMovedToANewDirectoryDraftsANewProject(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	old, _ := reader603Checked(t, ws, tc)
	r := newRig(t)
	dir := "examples/reader-lifecycle"
	work := t.TempDir()
	git(t, work, "clone", "--quiet", r.origin, ".")
	if err := old.Write(filepath.Join(work, dir), "original", &project.Ratification{Issue: 571, By: "gitdek", Proposal: old.Hash}); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "-A")
	git(t, work, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "seed")
	git(t, work, "push", "--quiet", "origin", "HEAD:main")
	r.f.Formalizer = formalize.Formalizer{Toolchain: tc, Backend: comparison648Agent{run: func(_ context.Context, j synth.Job) (synth.Usage, error) {
		reader603Workspace(t, j.Workspace)
		return synth.Usage{}, nil
	}}}
	r.gh.open(648, "gitdek", "Amend the reader", "Project: "+dir+"\n/invariant solve")
	r.poll()
	if r.expect(648, KindProposal, LabelProposal).Marker.Proposal.Target == nil {
		t.Fatal("the first draft didn't amend the project")
	}
	r.gh.issues[648].Body = "Project: examples/reader-new\n/invariant solve"
	r.gh.say(648, "gitdek", "Make it a new project instead.\n/invariant revise")
	if err := r.f.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	last := r.expect(648, KindProposal, LabelProposal).Marker.Proposal
	if last.Target != nil || last.Dir != "examples/reader-new" {
		t.Fatalf("the revision amended %v instead of drafting a new project", last.Target)
	}
}
