package factory

import (
	"encoding/json"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// An amendment names its destination in Target, and two projects may have
// identical statements. Recovering a branch must still honor that target.
func TestAmendmentBranchHonorsSavedDestination422(t *testing.T) {
	for _, moved := range []bool{false, true} {
		name := "same target"
		if moved {
			name = "different target"
		}
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.form.forks, r.form.amend = nil, dropCanFill
			r.build.stop = true
			const oldDir = "examples/03-buffer"
			const otherDir = "examples/04-buffer"
			seed(t, r, oldDir, 1)
			seed(t, r, otherDir, 2)
			r.gh.open(5, "gitdek", "Stop promising the buffer can fill", "Project: "+oldDir+"\n\n/invariant solve")
			r.poll()
			first := r.expect(5, KindProposal, LabelProposal)
			hash := first.Marker.Proposal.Hash
			r.gh.say(5, "gitdek", "/invariant ratify "+hash)
			r.poll()
			stopped := r.expect(5, KindFailed, LabelHumanReview)
			oldBranch := stopped.Marker.Branch
			oldHead := git(t, r.origin, "rev-parse", oldBranch)
			dir := oldDir
			if moved {
				dir = otherDir
			}
			r.gh.issues[5].Body = "Project: " + dir + "\n\n/invariant solve"
			r.gh.say(5, "gitdek", "/invariant revise")
			r.poll()
			revised := r.expect(5, KindProposal, LabelProposal).Marker.Proposal
			if revised.Hash != hash || revised.Target == nil || revised.Target.Dir != dir {
				t.Fatalf("revision did not keep the statements and name the target: %+v", revised)
			}
			if revised.Slug != first.Marker.Proposal.Slug {
				t.Fatal("both destinations must derive the same branch name to test reuse")
			}
			// Ratification uses the saved proposal, even if the issue's
			// Project line changes again without another revision.
			r.gh.issues[5].Body = "Project: examples/not-proposed\n\n/invariant solve"
			r.build.stop = false
			ratify := r.gh.say(5, "gitdek", "/invariant ratify "+hash)
			r.poll()
			built := r.expect(5, KindPR, LabelPR).Marker
			if built.Project != dir || (built.Branch == oldBranch) == moved {
				t.Fatalf("ratification chose branch %q, project %q; prior branch %q, moved %v", built.Branch, built.Project, oldBranch, moved)
			}
			if moved {
				if head := git(t, r.origin, "rev-parse", oldBranch); head != oldHead {
					t.Fatal("moving the target changed the prior branch")
				}
				if diff := git(t, r.origin, "diff", "main", built.Branch, "--", oldDir); diff != "" {
					t.Fatalf("the fresh branch carries changes to the abandoned target:\n%s", diff)
				}
			}
			var lock project.Lock
			if err := json.Unmarshal([]byte(git(t, r.origin, "show", built.Branch+":"+dir+"/.invariant/ratified.lock")), &lock); err != nil {
				t.Fatal(err)
			}
			if lock.Ratified == nil || lock.Ratified.Comment != ratify.URL || lock.Ratified.Proposal != hash || lock.Ratified.Amends != revised.Target.Amends {
				t.Fatalf("the destination lost the ratification: %+v", lock.Ratified)
			}
		})
	}
}
