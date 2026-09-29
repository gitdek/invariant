package factory

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// A new repository has no projects directory yet: not examples, the
// watcher's default, and not invariant, which invariant init suggests. Its
// first ratified proposal still becomes a project, the first one in that
// directory (#101).
func TestTheFirstProjectGoesInANewProjectsDirectory(t *testing.T) {
	r := newRig(t)
	r.f.Projects = "invariant"
	r.form.forks = nil
	r.gh.open(1, "gitdek", "Add a bounded buffer", "A buffer.\n\n/invariant solve")
	r.poll()
	p := r.expect(1, KindProposal, LabelProposal)
	r.gh.say(1, "gitdek", "/invariant ratify "+strings.TrimPrefix(p.Marker.Proposal.Hash, "sha256:"))
	if err := r.f.Poll(context.Background()); err != nil {
		t.Fatalf("ratifying with no %s directory on the base branch: %v", r.f.Projects, err)
	}
	dir := "invariant/01-bounded-buffer"
	var placed string
	for _, post := range r.gh.posts(1) {
		if post.Marker.Kind == KindRatified {
			placed = post.Marker.Project
		}
	}
	if placed != dir {
		t.Fatalf("the ratified project is in %q; want %q", placed, dir)
	}
	git(t, r.origin, "cat-file", "-e", "invariant/issue-1-bounded-buffer:"+dir+"/.invariant/ratified.lock")
}

// Listing a directory that isn't there, at a ref that is, gives no
// directories and no error. Any other failure to list one is still an error
// (#101).
func TestDirsOfADirectoryThatIsntThereAreNone(t *testing.T) {
	_, clone := gitRepos(t)
	ctx := context.Background()
	for _, dir := range []string{"invariant", "projects/go"} {
		if dirs, err := clone.Dirs(ctx, "origin/main", dir); err != nil || len(dirs) != 0 {
			t.Errorf("Dirs(origin/main, %s) = %q, %v; want no directories, and no error", dir, dirs, err)
		}
	}
	if dirs, err := clone.Dirs(ctx, "origin/main", "examples"); err != nil || !reflect.DeepEqual(dirs, []string{"02-twophase-commit"}) {
		t.Errorf("Dirs(origin/main, examples) = %q, %v; want [02-twophase-commit]", dirs, err)
	}
	for _, c := range []struct{ ref, dir string }{
		{"origin/no-such-branch", "invariant"},
		{"origin/no-such-branch", "examples"},
		{"origin/main", "README.md"},
	} {
		if dirs, err := clone.Dirs(ctx, c.ref, c.dir); err == nil {
			t.Errorf("Dirs(%s, %s) = %q, and no error; want an error", c.ref, c.dir, dirs)
		}
	}
}
