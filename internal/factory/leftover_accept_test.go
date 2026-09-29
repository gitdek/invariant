package factory

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// These tests hold the factory to what its callers rely on (#170): once a
// call on its clone, Poll and Wait, or WorkPlan returns, nothing it started
// is still running, so nothing writes to a repository after its caller moves
// on. What outlived the factory's calls was git's own upkeep: git fetch, git
// commit and the receive-pack a push runs start git gc --auto, which git
// runs in the background by default. Here that upkeep is due in every
// repository after every command that starts it, as it's due now and then in
// a real one, so a command that leaves it running does so every time.

// quietFor is how long a repository must stay as it was once a call returns.
const quietFor = 300 * time.Millisecond

// upkeepDue makes git's upkeep due in the repository at dir after every
// command that starts it: git gc --auto repacks once the repository holds
// more than one pack, and a push or a fetch keeps what it brings as a pack
// of its own. The repository asks for its upkeep in the background, as git
// runs it by default.
func upkeepDue(t *testing.T, dir string) {
	t.Helper()
	for _, setting := range [][2]string{
		{"gc.autoPackLimit", "1"},
		{"receive.unpackLimit", "1"},
		{"fetch.unpackLimit", "1"},
		{"receive.autogc", "true"},
		{"maintenance.auto", "true"},
		{"gc.autoDetach", "true"},
		{"maintenance.autoDetach", "true"},
	} {
		git(t, dir, "config", setting[0], setting[1])
	}
	packUp(t, dir)
}

// packUp gives the repository at dir another pack while it holds fewer than
// two that gc --auto counts, neither kept nor cruft, so the next command
// that starts git's upkeep finds it due. Each pack it adds holds an object
// of its own that nothing refers to.
func packUp(t *testing.T, dir string) {
	t.Helper()
	packs := filepath.Join(objectsOf(dir), "pack")
	for i := 0; ; i++ {
		have, err := filepath.Glob(filepath.Join(packs, "*.pack"))
		if err != nil {
			t.Fatal(err)
		}
		counted := 0
		for _, pack := range have {
			_, keepErr := os.Stat(strings.TrimSuffix(pack, ".pack") + ".keep")
			_, cruftErr := os.Stat(strings.TrimSuffix(pack, ".pack") + ".mtimes")
			if keepErr != nil && cruftErr != nil {
				counted++
			}
		}
		if counted >= 2 {
			return
		}
		if i == 2 {
			t.Fatalf("%s holds %d packs that gc counts after two more; want two", dir, counted)
		}
		blob := gitInput(t, dir, fmt.Sprintf("upkeep %d %d\n", time.Now().UnixNano(), i), "hash-object", "-w", "--stdin")
		gitInput(t, dir, blob+"\n", "pack-objects", "-q", filepath.Join(packs, "pack"))
	}
}

// objectsOf is the object store of the repository at dir, bare or not.
func objectsOf(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return filepath.Join(dir, ".git", "objects")
	}
	return filepath.Join(dir, "objects")
}

// gitInput runs git in dir with input on its standard input, apart from any
// configuration outside the repository, as the tests' git runs, and returns
// what it printed.
func gitInput(t *testing.T, dir, input string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Stdin = dir, strings.NewReader(input)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			err = fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exit.Stderr)))
		}
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// snapshotOf is every file and directory under dir, by its path there, with
// its mode, size and when it last changed. A directory changes when an entry
// comes or goes in it, so even a lock that comes and goes shows. An entry
// that goes while it's read shows as unreadable.
func snapshotOf(dir string) map[string]string {
	seen := map[string]string{}
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		rel, _ := filepath.Rel(dir, path)
		if err == nil {
			var info fs.FileInfo
			if info, err = d.Info(); err == nil {
				seen[rel] = fmt.Sprintf("%v %d %s", info.Mode(), info.Size(), info.ModTime().Format(time.RFC3339Nano))
				return nil
			}
		}
		seen[rel] = "unreadable: " + err.Error()
		return nil
	})
	return seen
}

// stillAfter checks that the repositories at dirs stay as they are once what
// the test called has returned: nothing it started goes on writing to them.
func stillAfter(t *testing.T, what string, dirs ...string) {
	t.Helper()
	before := make([]map[string]string, len(dirs))
	for i, dir := range dirs {
		before[i] = snapshotOf(dir)
	}
	time.Sleep(quietFor)
	for i, dir := range dirs {
		if changed := changedBetween(before[i], snapshotOf(dir)); len(changed) > 0 {
			t.Errorf("once %s returned, something went on writing to %s: %s", what, dir, strings.Join(changed, ", "))
		}
	}
}

// changedBetween names what differs between two snapshots, five at most.
func changedBetween(before, after map[string]string) []string {
	var changed []string
	for p, was := range before {
		switch now, ok := after[p]; {
		case !ok:
			changed = append(changed, p+" went")
		case now != was:
			changed = append(changed, p+" changed")
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			changed = append(changed, p+" came")
		}
	}
	sort.Strings(changed)
	if len(changed) > 5 {
		changed = append(changed[:5], fmt.Sprintf("and %d more", len(changed)-5))
	}
	return changed
}

// Every call on the clone that starts git's upkeep, on the clone or on the
// repository it pushes to, has ended when it returns, upkeep and all: a
// fetch, a commit, a push of code, recording a run and its result, pushing a
// commit, taking the lease, and another watcher's clone reading a run it
// has to fetch.
func TestEveryCallOnTheCloneLeavesNothingRunning(t *testing.T) {
	ctx := context.Background()
	origin, a := gitRepos(t)
	b := Clone{Dir: filepath.Join(t.TempDir(), "clone"), Remote: origin, Name: a.Name, Email: a.Email}
	if err := b.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	repos := []string{origin, a.Dir, b.Dir}
	for _, dir := range repos {
		upkeepDue(t, dir)
	}
	const branch = "invariant/issue-1-buffer"
	wt, err := a.Worktree(ctx, branch, "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	defer a.RemoveWorktree(ctx, wt)
	result := t.TempDir()
	if err := os.WriteFile(filepath.Join(result, "result.json"), []byte(`{"proposal": "p"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var code, saved string
	calls := []struct {
		what string
		call func() error
	}{
		{"Fetch", func() error { return a.Fetch(ctx) }},
		{"Commit", func() (err error) {
			if err = os.WriteFile(filepath.Join(wt, "buffer.go"), []byte("package buffer\n"), 0o644); err != nil {
				return err
			}
			code, err = a.Commit(ctx, wt, ".", "Add a buffer")
			return err
		}},
		{"Push", func() error { return a.Push(ctx, wt, branch) }},
		{"Record", func() error { return a.Record(ctx, 1, "draft-issue", "a draft for #1") }},
		{"Finish", func() (err error) {
			if saved, err = a.Save(ctx, result, "invariant: the result of a draft for #1", ""); err != nil {
				return err
			}
			return a.Finish(ctx, 1, "draft-issue", saved)
		}},
		{"PushCommit", func() error {
			next, err := a.Save(ctx, result, "More of the buffer", code)
			if err != nil {
				return err
			}
			return a.PushCommit(ctx, next, branch)
		}},
		{"PushLease", func() error {
			_, err := a.PushLease(ctx, "", LeaseRecord{Holder: "watcher-a", Until: time.Now().Add(5 * time.Minute)})
			return err
		}},
		{"Run, on another watcher's clone", func() error {
			state, sha, err := b.Run(ctx, 1, "draft-issue")
			if err == nil && (state != RunDone || sha != saved) {
				err = fmt.Errorf("the run is %v at %s; want it done at %s", state, sha, saved)
			}
			return err
		}},
	}
	for _, c := range calls {
		for _, dir := range repos {
			packUp(t, dir)
		}
		if err := c.call(); err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		stillAfter(t, c.what, repos...)
	}
}

// With steps running at once, once Wait returns, every step's git has ended
// too: two issues drafted at once, built at once and merged at once, and
// then two more, leave the origin and the clone as they are after each poll
// and Wait.
func TestNothingWritesToARepositoryOnceWaitReturns(t *testing.T) {
	p := newParRig(t, 3)
	repos := []string{p.origin, p.clone.Dir}
	for _, dir := range repos {
		upkeepDue(t, dir)
	}
	settle := func(what string, pair [2]int) {
		t.Helper()
		for _, dir := range repos {
			packUp(t, dir)
		}
		p.settle()
		stillAfter(t, fmt.Sprintf("the poll that %s #%d and #%d, and Wait,", what, pair[0], pair[1]), repos...)
	}
	for _, pair := range [][2]int{{1, 2}, {3, 4}} {
		for _, n := range pair {
			p.open(n)
		}
		settle("drafts", pair)
		for _, n := range pair {
			proposal := p.last(n)
			if proposal.Marker.Kind != KindProposal {
				t.Fatalf("#%d's last post is %q; want its proposal", n, proposal.Marker.Kind)
			}
			p.say(n, "/invariant ratify "+strings.TrimPrefix(proposal.Marker.Proposal.Hash, "sha256:")[:hashChars])
		}
		settle("builds", pair)
		for _, n := range pair {
			pr := p.last(n)
			if pr.Marker.Kind != KindPR {
				t.Fatalf("#%d's last post is %q; want its pull request", n, pr.Marker.Kind)
			}
			p.ci(pr.Marker.PR)
		}
		settle("merges", pair)
		for _, n := range pair {
			if kind := p.last(n).Marker.Kind; kind != KindMerged {
				t.Errorf("#%d's last post is %q; want its merge", n, kind)
			}
		}
	}
}

// The plan of issues whose test failed its cleanup (#170), worked with git's
// upkeep due: WorkPlan opens its first issue, polls draft it, build it and
// merge it, WorkPlan opens the second, and polls draft, build and merge that.
// After each WorkPlan and each poll, nothing writes to the origin or the
// clone.
func TestNothingWritesToARepositoryAfterWorkPlanOrAPoll(t *testing.T) {
	r, hub, plan := planRig(t, logPipeline...)
	repos := []string{r.origin, r.f.Repo.(Clone).Dir}
	for _, dir := range repos {
		upkeepDue(t, dir)
	}
	then := func(what string, call func()) {
		t.Helper()
		for _, dir := range repos {
			packUp(t, dir)
		}
		call()
		stillAfter(t, what, repos...)
	}
	work := func() { workOn(t, r.f, plan) }
	then("WorkPlan", work)
	if len(hub.opened) != 1 {
		t.Fatalf("the plan opened %v at first; want its first issue alone", hub.opened)
	}
	first := hub.opened[0]
	then("the poll that drafts the plan's first issue", r.poll)
	proposal := r.gh.last(first)
	if proposal.Marker.Kind != KindProposal {
		t.Fatalf("#%d's latest post is %q; want its proposal", first, proposal.Marker.Kind)
	}
	r.gh.say(first, "gitdek", "/invariant ratify "+strings.TrimPrefix(proposal.Marker.Proposal.Hash, "sha256:")[:hashChars])
	then("the poll that builds it", r.poll)
	pr := r.gh.last(first)
	if pr.Marker.Kind != KindPR {
		t.Fatalf("#%d's latest post is %q; want its pull request", first, pr.Marker.Kind)
	}
	then("WorkPlan while it's open", work)
	r.gh.ci(pr.Marker.PR, "success")
	then("the poll that merges it", r.poll)
	if kind := lastKindOn(r, first); kind != KindMerged {
		t.Fatalf("#%d's latest post is %q; want its merge", first, kind)
	}
	then("WorkPlan once it merged", work)
	if len(hub.opened) != 2 {
		t.Fatalf("once its first issue merged, the plan has opened %v; want its first two", hub.opened)
	}
	then("the poll that drafts the plan's second issue", r.poll)
	second := hub.opened[1]
	proposal = r.gh.last(second)
	if proposal.Marker.Kind != KindProposal {
		t.Fatalf("#%d's latest post is %q; want its proposal", second, proposal.Marker.Kind)
	}
	r.gh.say(second, "gitdek", "/invariant ratify "+strings.TrimPrefix(proposal.Marker.Proposal.Hash, "sha256:")[:hashChars])
	then("the poll that builds it", r.poll)
	pr = r.gh.last(second)
	if pr.Marker.Kind != KindPR {
		t.Fatalf("#%d's latest post is %q; want its pull request", second, pr.Marker.Kind)
	}
	r.gh.ci(pr.Marker.PR, "success")
	then("the poll that merges it", r.poll)
	if kind := lastKindOn(r, second); kind != KindMerged {
		t.Errorf("#%d's latest post is %q; want its merge", second, kind)
	}
}
