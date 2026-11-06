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

func TestComparisonBaselineSurvivesCompletedAnswers(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	old, _ := reader603Checked(t, ws, tc)
	for _, amend := range []bool{false, true} {
		name := "revision"
		if amend {
			name = "amendment"
		}
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			body, work, dir := "/invariant solve", "", "examples/reader-lifecycle"
			if amend {
				work = t.TempDir()
				git(t, work, "clone", "--quiet", r.origin, ".")
				if err := old.Write(filepath.Join(work, dir), "original", &project.Ratification{Issue: 571, By: "gitdek", Proposal: old.Hash}); err != nil {
					t.Fatal(err)
				}
				git(t, work, "add", "-A")
				git(t, work, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "seed baseline")
				git(t, work, "push", "--quiet", "origin", "HEAD:main")
				body = "Project: " + dir + "\n" + body
			}
			run := 0
			r.f.Formalizer = formalize.Formalizer{Toolchain: tc, Backend: comparison648Agent{run: func(_ context.Context, j synth.Job) (synth.Usage, error) {
				reader603Workspace(t, j.Workspace)
				if run == 1 {
					preview639Write(t, j.Workspace, preview639Draft(t, 2, nil))
				} else if run == 2 {
					reader603Write(t, filepath.Join(j.Workspace, "view.json"), strings.ReplaceAll(reader603View, "Working", "Selected working"))
				}
				run++
				return synth.Usage{}, nil
			}}}
			r.gh.open(648, "gitdek", "Keep the baseline through answers", body)
			r.poll()
			first := r.expect(648, KindProposal, LabelProposal)
			r.gh.say(648, "gitdek", "/invariant revise")
			r.poll()
			r.expect(648, KindForks, LabelAsking)
			r.gh.say(648, "gitdek", "/invariant choose F1 A")
			r.poll()
			if amend {
				reader603Write(t, filepath.Join(work, dir, ".invariant/view.json"), strings.ReplaceAll(reader603View, "Working", "Moved branch"))
				git(t, work, "add", "-A")
				git(t, work, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "move baseline branch")
				git(t, work, "push", "--quiet", "origin", "HEAD:main")
			}
			r.gh.say(648, "gitdek", "/invariant choose F2 B")
			r.poll()
			last := r.expect(648, KindProposal, LabelProposal)
			c := last.Marker.Proposal.Reader.Comparison
			if run != 3 || c == nil || c.Baseline.View != first.Marker.Proposal.View || c.Baseline.Model != old.ModuleText {
				t.Fatalf("answers replaced the selected baseline: %+v", c)
			}
			if amend && c.Baseline.Name != first.Marker.Proposal.Reader.Comparison.Baseline.Name {
				t.Fatal("answers replaced the captured ratification")
			}
		})
	}
}
