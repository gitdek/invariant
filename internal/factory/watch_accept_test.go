package factory

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// A writer's retry that watches a modeled project's failed draft again is
// the recovery core's watch (#354): the factory answers it, and once CI's
// gate passes on the draft, marks it ready and merges it.

// watchCrashRun takes a modeled project's failed draft, whose gate passed,
// through a writer's retry to a merge, stopping the watcher at effect at,
// counted from the retry, and starting a fresh one in its place, on this
// machine or, with elsewhere, on another. It returns what the watcher
// stopped at, the rig, the draft and how many effects it counted. With at
// 0, it never stops.
func watchCrashRun(t *testing.T, at int, elsewhere bool) (string, *rig, int, int) {
	t.Helper()
	ctx := context.Background()
	r := newRig(t)
	r.build.pass = false
	failed := ratified(t, r)
	draft := failed.Marker.PR
	if failed.Marker.Kind != KindFailed || draft == 0 || !r.gh.prs[draft].Draft {
		t.Fatalf("a build that fails the gate should leave a draft: %+v", failed.Marker)
	}
	r.gh.ci(draft, "success")
	r.poll()
	c := &crasher{at: at}
	watcher := func(repo Repo, work string) *Factory {
		return &Factory{Repository: r.f.Repository, GitHub: crashGitHub{r.gh, c}, Repo: crashRepo{repo, c},
			Formalizer: r.f.Formalizer, Builder: r.f.Builder, Base: r.f.Base, Projects: r.f.Projects, Work: work, Check: r.f.Check, Log: t.Logf}
	}
	f := watcher(r.f.Repo, r.f.Work)
	r.gh.say(1, "gitdek", "/invariant retry")
	for i := 0; i < 20; i++ {
		err := f.Poll(ctx)
		switch {
		case err != nil && strings.Contains(err.Error(), errCrash.Error()) && c.at > 0:
			c.at = -1
			if !elsewhere {
				f = watcher(r.f.Repo, r.f.Work)
				continue
			}
			clone := r.f.Repo.(Clone)
			clone.Dir = t.TempDir() + "/clone"
			if err := clone.Ensure(ctx); err != nil {
				t.Fatal(err)
			}
			f = watcher(clone, t.TempDir())
			continue
		case err != nil:
			t.Fatalf("after stopping at %q: %v", c.what, err)
		}
		if r.gh.last(1).Marker.Kind == KindMerged {
			return c.what, r, draft, c.n
		}
	}
	t.Fatalf("after stopping at %q, the draft never merged: the last post is %q", c.what, r.gh.last(1).Marker.Kind)
	return "", nil, 0, 0
}

// A watcher can stop just before any effect of a retry that watches a
// failed draft again: the answer, the merge's record, marking the draft
// ready, the merge and its post. A fresh watcher, here or on another
// machine, still merges the draft once, with the retry answered once and no
// build or pull request of its own.
func TestAWatchSurvivesAStopAtEachEffect(t *testing.T) {
	_, _, _, effects := watchCrashRun(t, 0, false)
	for _, elsewhere := range []bool{false, true} {
		where := "the same machine"
		if elsewhere {
			where = "another machine"
		}
		for at := 1; at <= effects; at++ {
			t.Run(fmt.Sprintf("%s/effect %d", where, at), func(t *testing.T) {
				t.Parallel()
				what, r, draft, _ := watchCrashRun(t, at, elsewhere)
				t.Logf("stopped %s", what)
				answers := map[int64]int{}
				for _, p := range r.gh.posts(1) {
					for _, id := range p.Marker.ReplyTo {
						answers[id]++
					}
				}
				for _, c := range r.gh.comments[1] {
					if _, ours := DecodeMarker(c.Body); ours || !hasVerb(ParseCommands(c.Body), Retry) {
						continue
					}
					if answers[c.ID] != 1 {
						t.Errorf("the retry was answered %d times", answers[c.ID])
					}
				}
				last := r.gh.last(1)
				if len(r.gh.prs) != 1 || !reflect.DeepEqual(r.gh.merged, []int{draft}) || r.gh.prs[draft].Draft || last.Marker.Numbers == nil {
					t.Errorf("the draft must be marked ready and merged once, by the factory: %d pull requests, merged %v, last %+v", len(r.gh.prs), r.gh.merged, last.Marker)
				}
			})
		}
	}
}
