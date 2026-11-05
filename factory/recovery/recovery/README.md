# Watcher recovery

This core authorizes recovery effects for ordinary Solve/Build work, replacement
builds, watched drafts, and decision follow-ups. Its ratified specification is
[WatcherRecovery.tla](../.invariant/specs/WatcherRecovery.tla); the statements and
verification bounds are recorded in `.invariant/ratified.lock`.

Issue #445 extends recovery under D-0069/D-0074 and the follow-up/protocol
amendments D-0203/D-0210/D-0212/D-0225. It preserves the ordinary retry and
replacement behavior. The revised model has one initial state: the ordinary
lane with an inactive follow-up **record**. `ChooseFollowup` is enabled only in
that untouched state. Every ratified invariant, property, witness, known bug,
and fairness assumption retains its definition.

The amendment is pinned by [the ratification on #445](https://github.com/gitdek/invariant/issues/445#issuecomment-6629773797)
to proposal `sha256:58e3be24aa3ec6c2c0ef6b1d996dfb5493dc03d4261b53c47c270889f895c0dd`.

A follow-up's source obligation, branch and PR identity persist across draft and
creation attempts, repairs, holds and reopening. Each operation has a separate
identity. `FollowControl` computes guarded transitions; `FollowJournal.Save`
retains the accepted snapshot and its effect ledger before an adapter dispatches
work. The caller serializes the transaction under the repository lease and
passes its current fence in `FollowSession`. This amendment defines recovery
rules; later plumbing supplies the real durable records and remote effects.
Journal extension copies the retained prefix into a fresh slice, so earlier
snapshots keep their contents and exactly one new snapshot is appended. A
rejected save leaves the journal unchanged.

A dispatched effect stays pending until its durable result or a current GitHub
observation reconciles it. A stopped run is not restarted. Failed drafting or
creation instead receives a fresh recorded operation. PR creation recovery
adopts the existing PR even if a person closed or merged it. Repair publication
changes the same PR's revision, clears prior CI and mergeability, and spends its
allowance only at authorization. Failure, no change and invalidation retain
usage and their actual result.

Two repairs are authorized per renewal period. Closure or exhaustion holds work
until an explicit writer retry/resume. Under decision F1, a request during an
unresolved repair waits for that run and its publication to reconcile; it is
then consumed and answered once and renews the allowance to two. Reopening is a
separate authorized effect on the existing PR, never replacement's close-and-new
PR transition.

Merge intent is durable before dispatch. Authorization alone is not evidence of
success: recovery records confirmed success, a definitive refusal or an unknown
outcome. External merges settle through observations without invented CI or a
second merge dispatch. Publication and merge guards check the current revision;
merge also requires CI and mergeability for that revision. Lease fencing and
operation identities reject stale watchers and older results.

`explore.go` supplies independent bounded environment choices at every effect:
agent results, lost responses, CI, external closure/merge/head changes, writer
requests and watcher crashes. It contains all checking bounds and audit history;
the core has caller-sized operation storage and a growing snapshot journal.
`MaxFollowOps` is declared as a constructor parameter in the verification
manifest. Exhausting that capacity refuses a new authorization without changing
the journal; it does not renew an allowance or discard an earlier operation.
The gate checks safety, progress under the existing fairness assumptions,
reachability of all witnesses, every known bug, exact executable conformance,
one-size-larger agreement, Gobra contracts, vet and tests. Human holds do not
assume a person will act.

Gobra proves `recovery.go`, the recovery core, and `followstate.go`, the
follow-up journal's state, the files that carry the `// +gobra` header. The
journal in `journal.go` and the follow-up logic in `followup.go` are plain Go,
and conformance checks them against the model in every state the explorer
reaches, since the explorer saves every follow-up transition through
`FollowJournal.Save`. Proving them in place ran past Gobra's 20-minute limit:
all of `followup.go` took more than 35 minutes, and `journal.go`'s quantified
slice contracts alone more than 25. So on 2026-11-05 @gitdek chose to prove the
core and check the follow-up logic by conformance. The ratified statements are
unchanged.
