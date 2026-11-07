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

func TestComparison701FactoryRetainsRefusedView(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	old, _ := reader603Checked(t, ws, tc)
	r := newRig(t)
	dir, work := "examples/reader-701", t.TempDir()
	git(t, work, "clone", "--quiet", r.origin, ".")
	if err := old.Write(filepath.Join(work, dir), "original", &project.Ratification{Issue: 571, By: "gitdek", Proposal: old.Hash}); err != nil {
		t.Fatal(err)
	}
	badView := strings.Replace(reader603View, `"label":"Continue"`, `"label":"Continue","hide":true`, 1)
	reader603Write(t, filepath.Join(work, dir, ".invariant/view.json"), badView)
	commit := func(message string) {
		git(t, work, "add", "-A")
		git(t, work, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", message)
		git(t, work, "push", "--quiet", "origin", "HEAD:main")
	}
	commit("seed captured invalid view")
	r.f.Formalizer = formalize.Formalizer{Toolchain: tc, Backend: comparison648Agent{run: func(_ context.Context, j synth.Job) (synth.Usage, error) {
		reader603Workspace(t, j.Workspace)
		reader603Write(t, filepath.Join(j.Workspace, "baseline/view.json"), reader603View)
		return synth.Usage{}, nil
	}}}
	r.gh.open(701, "gitdek", "Amend reader with fixed bad view", "Project: "+dir+"\n/invariant solve")
	r.poll()
	assertRefused := func(post Post) *formalize.Proposal {
		for _, want := range []string{"unreachable_chart", dir + "/.invariant/view.json", "separate change", "new amendment issue", "revis"} {
			if !strings.Contains(post.Comment.Body, want) {
				t.Errorf("factory refusal omits %q: %s", want, post.Comment.Body)
			}
		}
		if strings.Contains(post.Comment.Body, "repair baseline/view.json") {
			t.Error("factory suggests repairing the fixed capture")
		}
		decoded, ok := DecodeMarker(post.Comment.Body)
		if !ok || decoded.Proposal == nil || decoded.Proposal.Target == nil || decoded.Proposal.Target.Baseline == nil || decoded.Proposal.Target.Baseline.View != badView {
			t.Fatal("saved refused-amendment marker lost its captured view")
		}
		return decoded.Proposal
	}
	first := assertRefused(r.expect(701, KindStuck, LabelHumanReview))
	// Only view.json changes on the base branch; lock and statement hash stay fixed.
	reader603Write(t, filepath.Join(work, dir, ".invariant/view.json"), reader603View)
	commit("repair saved project view separately")
	r.gh.say(701, "gitdek", "/invariant revise")
	r.poll()
	second := assertRefused(r.expect(701, KindStuck, LabelHumanReview))
	if second.Target.Baseline.Commit != first.Target.Baseline.Commit || second.Target.Amends != first.Target.Amends {
		t.Fatal("view-only fix moved the existing amendment baseline")
	}
	r.gh.open(702, "gitdek", "Amend reader after saved view repair", "Project: "+dir+"\n/invariant solve")
	r.poll()
	fresh := r.expect(702, KindProposal, LabelProposal).Marker.Proposal
	if fresh.Target == nil || fresh.Target.Baseline == nil || fresh.Target.Baseline.View != reader603View || fresh.Target.Baseline.Commit == first.Target.Baseline.Commit || fresh.Reader == nil || fresh.Reader.Comparison == nil {
		t.Fatal("new amendment failed to capture corrected project")
	}
	if fresh.Reader.Comparison.Baseline.View != reader603View || fresh.Reader.Comparison.Baseline.Model != old.ModuleText {
		t.Fatal("new amendment comparison lost repaired view or original model")
	}
}
