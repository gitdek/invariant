package factory

import "testing"

func TestAmendmentBranchReuseChecksItsTargetDirectory(t *testing.T) {
	for _, move := range []bool{false, true} {
		name := "same target"
		if move {
			name = "different target"
		}
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.form.forks, r.form.amend = nil, dropCanFill
			const oldDir, newDir = "examples/03-buffer", "examples/04-buffer"
			seed(t, r, oldDir, 1)
			seed(t, r, newDir, 2)
			r.build.stop = true
			r.gh.open(5, "gitdek", "Amend the buffer", "Project: "+oldDir+"\n\n/invariant solve")
			r.poll()
			hash := r.expect(5, KindProposal, LabelProposal).Marker.Proposal.Hash
			r.gh.say(5, "gitdek", "/invariant ratify "+hash)
			r.poll()
			stopped := r.expect(5, KindFailed, LabelHumanReview)
			oldBranch := stopped.Marker.Branch
			oldHead := git(t, r.origin, "rev-parse", oldBranch)
			dir := oldDir
			if move {
				dir = newDir
			}
			r.gh.issues[5].Body = "Project: " + dir + "\n\n/invariant solve"
			r.gh.say(5, "gitdek", "/invariant revise")
			r.poll()
			p := r.expect(5, KindProposal, LabelProposal).Marker.Proposal
			if p.Hash != hash || p.Target == nil || p.Target.Dir != dir {
				t.Fatalf("revision = %+v; want unchanged hash and target %s", p, dir)
			}
			r.build.stop = false
			r.gh.say(5, "gitdek", "/invariant ratify "+hash)
			r.poll()
			built := r.expect(5, KindPR, LabelPR).Marker
			if built.Project != dir || (built.Branch != oldBranch) != move {
				t.Fatalf("built in %s on %s; move = %v", built.Project, built.Branch, move)
			}
			if move {
				if git(t, r.origin, "rev-parse", oldBranch) != oldHead {
					t.Fatal("moving the amendment changed the old branch")
				}
				if diff := git(t, r.origin, "diff", "main..."+built.Branch, "--", oldDir); diff != "" {
					t.Fatalf("fresh branch changed the old target:\n%s", diff)
				}
			}
		})
	}
}
