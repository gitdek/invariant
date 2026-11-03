package factory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// A statement hash does not include a project's directory. Ratifying the
// same statements after a revise must honor the new Project: line, while
// an unchanged destination still recovers the original branch.
func TestRatifyingTheSameStatementsHonorsTheProjectDirectory(t *testing.T) {
	for _, tc := range []struct {
		name, before, after string
		advanceNumbering    bool
	}{
		{"numbered to named", "", "factory/decision-followup", false},
		{"named to named", "examples/old-buffer", "factory/decision-followup", false},
		{"same named directory", "factory/decision-followup", "factory/decision-followup", false},
		{"same numbered directory after base advances", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.form.forks = nil
			r.build.stop = true
			body := "A bounded buffer.\n\n/invariant solve"
			if tc.before != "" {
				body += "\n\nProject: " + tc.before
			}
			r.gh.open(1, "gitdek", "Add a bounded buffer", body)
			r.poll()
			first := r.expect(1, KindProposal, LabelProposal)
			hash := first.Marker.Proposal.Hash
			r.gh.say(1, "gitdek", "/invariant ratify "+hash)
			r.poll()
			stopped := r.expect(1, KindFailed, LabelHumanReview)
			if stopped.Marker.PR != 0 || stopped.Marker.Failure != FailStopped {
				t.Fatalf("fixture did not stop before opening a pull request: %+v", stopped.Marker)
			}
			oldBranch, oldDir := stopped.Marker.Branch, stopped.Marker.Project
			oldHead := git(t, r.origin, "rev-parse", oldBranch)
			wantDir := tc.after
			if wantDir == "" {
				wantDir = oldDir
			}
			if tc.advanceNumbering {
				// An unrelated project may merge while this issue waits.
				// Its number must not relocate this issue's allocated project.
				seed := t.TempDir()
				git(t, seed, "clone", "--quiet", "--branch", "main", r.origin, ".")
				added := filepath.Join(seed, "examples", "03-unrelated")
				if err := os.MkdirAll(added, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(added, "README.md"), []byte("Another project.\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				git(t, seed, "add", "examples/03-unrelated/README.md")
				git(t, seed, "-c", "user.name=Seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "Add an unrelated project")
				git(t, seed, "push", "--quiet", "origin", "main")
			}

			r.gh.issues[1].Body = "A bounded buffer.\n\n/invariant solve"
			if tc.after != "" {
				r.gh.issues[1].Body += "\n\nProject: " + tc.after
			}
			r.gh.say(1, "gitdek", "Keep the statements; use the project's current directory.\n\n/invariant revise")
			r.poll()
			revised := r.expect(1, KindProposal, LabelProposal)
			if p := revised.Marker.Proposal; p.Hash != hash || p.Dir != tc.after {
				t.Fatalf("revision must change only the destination: hash %q, directory %q", p.Hash, p.Dir)
			}
			r.build.stop = false
			ratify := r.gh.say(1, "gitdek", "/invariant ratify "+hash)
			r.poll()
			built := r.expect(1, KindPR, LabelPR)
			if built.Marker.Project != wantDir {
				t.Fatalf("ratification built in %q; want %q", built.Marker.Project, wantDir)
			}
			if len(r.build.built) != 2 || !strings.HasSuffix(filepath.ToSlash(r.build.built[1]), "/"+wantDir) {
				t.Fatalf("builder did not receive the revised project directory: %v", r.build.built)
			}
			branch := built.Marker.Branch
			if oldDir == wantDir {
				if branch != oldBranch {
					t.Fatalf("same proposal and destination abandoned %q for %q", oldBranch, branch)
				}
			} else {
				if branch == oldBranch {
					t.Fatal("a different destination reused the earlier branch")
				}
				if head := git(t, r.origin, "rev-parse", oldBranch); head != oldHead {
					t.Fatalf("moving the project changed the old branch: %s -> %s", oldHead, head)
				}
				if files := git(t, r.origin, "ls-tree", "-r", "--name-only", branch, "--", oldDir); files != "" {
					t.Fatalf("fresh branch retained the abandoned project at %s:\n%s", oldDir, files)
				}
			}
			git(t, r.origin, "cat-file", "-e", branch+":"+wantDir+"/buffer/buffer.go")
			var lock project.Lock
			if err := json.Unmarshal([]byte(git(t, r.origin, "show", branch+":"+wantDir+"/.invariant/ratified.lock")), &lock); err != nil {
				t.Fatal(err)
			}
			if lock.Ratified == nil || lock.Ratified.Proposal != hash || lock.Ratified.Comment != ratify.URL ||
				project.ProposalHash(lock.Bounds, lock.Statements) != hash {
				t.Fatalf("new destination does not hold the person's unchanged ratified statements: %+v", lock)
			}
			if mod := git(t, r.origin, "show", branch+":"+wantDir+"/go.mod"); !strings.Contains(mod, "module github.com/o/r/"+wantDir+"\n") {
				t.Fatalf("project module still names an old destination: %s", mod)
			}
		})
	}
}
