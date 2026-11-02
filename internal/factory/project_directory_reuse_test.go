package factory

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// Identical amendments can target different projects. The saved proposal
// settles which one is ratified, even if the issue is edited again later.
func TestAmendmentBranchReuseHonorsSavedTarget(t *testing.T) {
	r := newRig(t)
	r.form.forks, r.form.amend = nil, dropCanFill
	r.build.stop = true
	oldDir, newDir := "examples/03-old-buffer", "factory/another-buffer"
	seed(t, r, oldDir, 1)
	base := seed(t, r, newDir, 2)
	issueBody := func(dir string) string {
		return "Stop promising the buffer can fill.\n\nProject: " + dir + "\n\n/invariant solve"
	}
	r.gh.open(5, "gitdek", "Amend a bounded buffer", issueBody(oldDir))
	r.poll()
	first := r.expect(5, KindProposal, LabelProposal)
	hash := first.Marker.Proposal.Hash
	r.gh.say(5, "gitdek", "/invariant ratify "+hash)
	r.poll()
	stopped := r.expect(5, KindFailed, LabelHumanReview)
	if stopped.Marker.Failure != FailStopped || stopped.Marker.PR != 0 || stopped.Marker.Project != oldDir {
		t.Fatalf("fixture did not stop at the first target: %+v", stopped.Marker)
	}
	oldBranch := stopped.Marker.Branch
	oldHead := git(t, r.origin, "rev-parse", oldBranch)

	r.gh.issues[5].Body = issueBody(newDir)
	r.gh.say(5, "gitdek", "/invariant revise")
	r.poll()
	revised := r.expect(5, KindProposal, LabelProposal).Marker.Proposal
	if revised.Hash != hash || revised.Target == nil || revised.Target.Dir != newDir {
		t.Fatalf("revision did not retain the statements at the new target: %+v", revised)
	}
	// The current issue text must not override the destination already
	// saved in the proposal the person is about to ratify.
	r.gh.issues[5].Body = issueBody(oldDir)
	r.build.stop = false
	ratify := r.gh.say(5, "gitdek", "/invariant ratify "+hash)
	r.poll()
	built := r.expect(5, KindPR, LabelPR)
	branch := built.Marker.Branch
	if built.Marker.Project != newDir || branch == oldBranch {
		t.Fatalf("changed amendment target reused the old destination: %+v", built.Marker)
	}
	if len(r.build.built) != 2 || !strings.HasSuffix(filepath.ToSlash(r.build.built[1]), "/"+newDir) ||
		len(r.build.amended) != 2 || !r.build.amended[1] {
		t.Fatalf("builder did not amend the revised target: paths %v, amendments %v", r.build.built, r.build.amended)
	}
	if head := git(t, r.origin, "rev-parse", oldBranch); head != oldHead {
		t.Fatalf("moving the amendment changed its earlier branch: %s -> %s", oldHead, head)
	}
	if changed := git(t, r.origin, "diff", "--name-only", "main", branch, "--", oldDir); changed != "" {
		t.Fatalf("fresh branch retained the abandoned amendment:\n%s", changed)
	}
	var lock project.Lock
	if err := json.Unmarshal([]byte(git(t, r.origin, "show", branch+":"+newDir+"/.invariant/ratified.lock")), &lock); err != nil {
		t.Fatal(err)
	}
	if lock.Ratified == nil || lock.Ratified.Proposal != hash || lock.Ratified.Comment != ratify.URL ||
		lock.Ratified.Amends != base.Ratified.Proposal || lock.Ratified.Previous != "#2" ||
		project.ProposalHash(lock.Bounds, lock.Statements) != hash {
		t.Fatalf("new target does not hold the person's amendment: %+v", lock)
	}
	if code := git(t, r.origin, "show", branch+":"+newDir+"/buffer/buffer.go"); !strings.Contains(code, "// Amended.") {
		t.Fatalf("new target's existing code was not amended: %q", code)
	}
}
