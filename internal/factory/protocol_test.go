package factory

import (
	"testing"

	"github.com/gitdek/invariant/factory/protocol/protocol"
	"github.com/gitdek/invariant/internal/formalize"
)

// The protocol lets the factory take the steps of an issue's life, and
// refuses each bug the ratified model names.
func TestTheProtocolDecides(t *testing.T) {
	const p, earlier, lock, moved, head, old = "sha256:p", "sha256:earlier", "sha256:lock", "sha256:moved", "c0ffee", "decade"
	proposed := Post{Marker: Marker{Kind: KindProposal, Proposal: &formalize.Proposal{Hash: p}}}
	amending := Post{Marker: Marker{Kind: KindProposal, Proposal: &formalize.Proposal{Hash: p, Target: &formalize.Target{Amends: lock}}}}
	ratified := Post{Marker: Marker{Kind: KindRatified, Proposal: &formalize.Proposal{Hash: p}, Hash: p}}
	open := Post{Marker: Marker{Kind: KindPR, Proposal: &formalize.Proposal{Hash: p}, Hash: p, PR: 7}}
	failed := Post{Marker: Marker{Kind: KindFailed, Proposal: &formalize.Proposal{Hash: p}, Hash: p, PR: 7}}
	f := &Factory{Self: "invariant-code-factory[bot]", writers: map[string]bool{"gitdek": true, "invariant-code-factory[bot]": true}}
	by := func(login string) Command { return Command{Verb: Ratify, By: login} }
	green := pullRequest{head: head, gate: protocol.GatePass, lock: p}

	type step struct {
		name     string
		from, to view
		allowed  bool
	}
	var steps []step
	add := func(name string, allowed bool, from, to view) {
		steps = append(steps, step{name, from, to, allowed})
	}
	solve := func(login string, m Marker) (view, view) {
		return f.draftStep(Thread{}, Command{Verb: Solve, By: login}, "", m)
	}
	proposal := Marker{Kind: KindProposal, Proposal: &formalize.Proposal{Hash: p}}

	from, to := solve("gitdek", proposal)
	add("a writer's solve gets a proposal", true, from, to)
	from, to = solve("gitdek", Marker{Kind: KindForks, Forks: make([]formalize.Fork, 3)})
	add("a writer's solve gets questions", true, from, to)
	from, to = solve("someone", proposal)
	add("ObeyAnyone: someone without write access directs the factory", false, from, to)
	from, to = solve("invariant-code-factory[bot]", proposal)
	add("ObeyAnyone: the factory directs itself", false, from, to)

	asked := Thread{Posts: []Post{{Marker: Marker{Kind: KindForks, Forks: make([]formalize.Fork, 2)}}}}
	from, to = f.draftStep(asked, Command{Verb: Choose, By: "gitdek"}, "", proposal)
	add("the last answer gets a proposal", true, from, to)

	// Revising an amendment after the project changed: four hashes, more
	// than the model has, and still the protocol's step.
	revised := Thread{Posts: []Post{amending}}
	from, to = f.draftStep(revised, Command{Verb: Revise, By: "gitdek"}, moved,
		Marker{Kind: KindProposal, Proposal: &formalize.Proposal{Hash: "sha256:again", Target: &formalize.Target{Amends: moved}}})
	add("a revise amends the lock as it is now", true, from, to)

	from, to = f.ratifyStep(Thread{Posts: []Post{proposed}}, proposed, by("gitdek"), p, "")
	add("a writer ratifies the current proposal", true, from, to)
	from, to = f.ratifyStep(Thread{Posts: []Post{proposed}}, proposed, by("gitdek"), earlier, "")
	add("RatifyEarlier: a ratify names an earlier proposal", false, from, to)
	from, to = f.ratifyStep(Thread{Posts: []Post{amending}}, amending, by("gitdek"), p, lock)
	add("an amendment is ratified over the lock it amends", true, from, to)
	from, to = f.ratifyStep(Thread{Posts: []Post{amending}}, amending, by("gitdek"), p, moved)
	add("RatifyOverMovedLock: the base branch's lock has moved", false, from, to)
	from, to = f.ratifyStep(Thread{Posts: []Post{proposed}}, proposed, by("someone"), p, "")
	add("someone without write access ratifies", false, from, to)

	from, to = buildStep(ratified, 0, pullRequest{head: head, gate: protocol.GatePending, lock: p})
	add("the build opens a pull request with the ratified lock", true, from, to)
	from, to = buildStep(ratified, 0, pullRequest{head: head, gate: protocol.GatePending, lock: earlier})
	add("the build opens a pull request with another lock", false, from, to)

	from, to = prStep(Thread{Posts: []Post{open}}, open, pullRequest{head: head, gate: protocol.GateFail, lock: p}, protocol.KindFailed, protocol.Nobody)
	to.failure = FailCI
	add("CI's gate fails", true, from, to)
	from, to = prStep(Thread{Posts: []Post{open}}, open, pullRequest{head: head, gate: protocol.GatePass, lock: earlier}, protocol.KindFailed, protocol.Nobody)
	to.failure = FailUnmergeable
	add("CI's gate passes, but the lock isn't the ratified one", true, from, to)
	from, to = prStep(Thread{Posts: []Post{open}}, open, green, protocol.KindFailed, protocol.Nobody)
	to.failure = FailUnmergeable
	add("the factory fails a pull request that can merge", false, from, to)
	from, to = f.retryStep(Thread{Posts: []Post{failed}}, Command{Verb: Retry, By: "gitdek"})
	add("a writer retries", true, from, to)
	from, to = prStep(Thread{Posts: []Post{open}}, open, green, protocol.KindMerged, protocol.ByFactory)
	add("the factory merges a green head with the ratified lock", true, from, to)
	from, to = prStep(Thread{Posts: []Post{open}}, open, pullRequest{head: head, gate: protocol.GatePending, lock: p}, protocol.KindMerged, protocol.ByFactory)
	add("the factory merges before CI's gate passes", false, from, to)
	from, to = prStep(Thread{Posts: []Post{open}}, open, pullRequest{head: head, gate: protocol.GatePass, lock: earlier}, protocol.KindMerged, protocol.ByFactory)
	add("MergeIgnoringLock: the lock isn't the ratified proposal", false, from, to)
	from, to = prStep(Thread{Posts: []Post{open}}, open, pullRequest{head: head, gate: protocol.GatePass, lock: p, scopeMany: true}, protocol.KindMerged, protocol.ByFactory)
	add("the pull request changes more than its project", false, from, to)
	from, to = prStep(Thread{Posts: []Post{open}}, open, green, protocol.KindMerged, protocol.ByFactory)
	to.mergedHead = old
	add("MergeOnAnyPass: the factory merges a head that isn't the current one", false, from, to)
	from, to = prStep(Thread{Posts: []Post{failed}}, failed, green, protocol.KindMerged, protocol.ByFactory)
	add("the factory merges a pull request that failed, without a retry", false, from, to)
	from, to = prStep(Thread{Posts: []Post{open}}, open, pullRequest{head: head}, protocol.KindMerged, protocol.ByOther)
	add("someone else merges", true, from, to)
	from, to = prStep(Thread{Posts: []Post{open}}, open, pullRequest{head: head}, protocol.KindClosed, protocol.Nobody)
	add("someone else closes", true, from, to)

	for _, s := range steps {
		if got := allows(s.from, s.to); got != s.allowed {
			t.Errorf("%s: allowed = %v, want %v\nfrom %+v\nto   %+v", s.name, got, s.allowed, s.from, s.to)
		}
	}
}

// A lock or head past the model's values never makes two different values
// the ones a step compares.
func TestRenamingKeepsTheComparisons(t *testing.T) {
	r := rename(view{proposal: "p", amends: "a", base: "b", prLock: "l", ratifiedBase: "rb"}, view{ratified: "p", head: "h", mergedHead: "m"})
	if r.locks["p"] != protocol.P1 || r.locks["a"] == r.locks["b"] || r.locks["a"] == r.locks["p"] || r.locks["b"] == r.locks["p"] {
		t.Errorf("the proposal, the lock it amends and the base branch's lock must stay apart: %v", r.locks)
	}
	if r.heads["h"] != protocol.H1 || r.heads["m"] == r.heads["h"] {
		t.Errorf("the head and another commit must stay apart: %v", r.heads)
	}
}

// Only a retry that builds a stopped build again counts stops afresh; a
// retry of a failed pull request doesn't, as the protocol has it.
func TestStopsCountAfreshOnlyOnARebuild(t *testing.T) {
	retry := Command{Verb: Retry, Comment: 10}
	stopped := Post{Marker: Marker{Kind: KindFailed, Failure: FailStopped}}
	th := Thread{Commands: []Command{retry}, Posts: []Post{stopped, stopped}}
	if got := th.Stops(); got != 2 {
		t.Fatalf("two stops: %d", got)
	}
	watchedAgain := Post{Marker: Marker{Kind: KindPR, ReplyTo: []int64{10}, PR: 7}}
	if got := (Thread{Commands: th.Commands, Posts: append(th.Posts, watchedAgain)}).Stops(); got != 2 {
		t.Errorf("a retry of a pull request counted stops afresh: %d", got)
	}
	builtAgain := Post{Marker: Marker{Kind: KindRatified, ReplyTo: []int64{10}}}
	if got := (Thread{Commands: th.Commands, Posts: append(th.Posts, builtAgain)}).Stops(); got != 0 {
		t.Errorf("a rebuild didn't count stops afresh: %d", got)
	}
}
