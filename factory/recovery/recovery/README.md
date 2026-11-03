# Recovery core

Recovery authorizes effects while preserving the durable facts needed by a
replacement watcher. Ordinary commands retain their existing solve/build,
replacement retry and watched-draft behavior. Follow-ups use a stable source
ratified-plan obligation, branch and PR identity; each draft, creation, repair,
reopening or merge has a separate operation identity.

`NewFollowRepo(source, branch, pr, operationCapacity, snapshotCapacity)` creates
the follow-up state. `FollowRepo.Step(action, input)` applies one named action
using a `FollowInput` containing the lease and external observations, returning
the resulting state and whether the action was authorized. Action names match
the model's `F...` operators. The caller owns persistence and dispatch; a
successful transition supplies permission and durable state to record.
`Repo.AcquireFenced` grants each follow-up lease a fresh monotonic fence;
ordinary `Repo.Acquire` preserves its existing lease behavior.

An accepted Workflow snapshot is retained before its authorized effect is
dispatched. The effect ledger records intent, pending, completed and cancelled
effects separately. A watcher checks its lease and fence before acting;
successful results and publications also belong to the current operation and
revision. A stopped operation is reconciled, and any permitted retry receives
a fresh identity.

Draft and creation failures retry automatically. Once created, the same PR is
used for repairs and reopening. Each authorized repair consumes one of the
two allowances in its renewal period, including failed, stopped, no-change or
invalidated repairs. A lost publication response is reconciled without another
publication or another charge. Published revisions invalidate prior CI and
mergeability observations.

Closed or exhausted work waits for an explicit writer retry/resume. A request
received during an unresolved repair or its publication stays pending until
that operation is reconciled. It is then consumed once, renews the allowance to
two, and receives one answer. Resuming closed work authorizes reopening the
existing PR.

Merge authorization is durable intent, not evidence that a merge happened.
Merge outcomes are reconciled as success, refusal or unresolved uncertainty.
Observing an externally merged PR settles it without inventing a CI result or
dispatching another merge. Recovery also adopts a created PR whose response
was lost even if a person has since closed or merged it.

## Executable model

`Init`, `Try` and `Abstract` expose the state graph in the TLA+ model's
vocabulary. Initialization produces one ordinary state; `ChooseFollowup` may
select the independent follow-up lane before any ordinary work. The lanes
avoid an irrelevant Cartesian product while retaining all behavior of each
protocol. Outcomes are choices at the effect that produces them, so combinations
and orderings are explored rather than selected from fixed scenarios.

The ratified checking bounds are two watchers, two ordinary crashes, one
follow-up crash, three exceptional follow-up events, and seven follow-up
operation identities. Individual bounds additionally permit one draft failure,
creation failure, publication refusal, lost creation response, lost repair
publication response, lost merge response, closure, head movement, CI failure,
unknown merge and refused merge. Both each individual bound and the total event
budget apply. Successful responses, reconciliation, writer requests and
successful CI do not consume that event budget. A crash-induced stopped result
is counted once through the crash.

Seven operation slots reserve the successful draft, creation and final merge,
one extra operation per exceptional event, and one reopening per closure:
`3 + MaxFollowEvents + MaxCloses`. These are finite exploration bounds, not
Workflow repair allowances or a production lifetime limit. Real integration
must continue allocating fresh durable operation identities as work is
authorized.

Progress assumes weak fairness for watcher acquisition, lease expiry, watcher
actions, ordinary run completion and dropping stale runs, as recorded in the
model. It does not require a person, CI or GitHub to supply a missing external
event. Human holds and an unknown remote merge can therefore remain unsettled
until such an event occurs.

## Verification

Run `go test ./...` for the executable checks and the complete Invariant gate
for model safety, temporal properties, witness reachability, known-bug
counterexamples, core proofs and model/code conformance. The authority for this
amendment is [.invariant/ratified.lock](../.invariant/ratified.lock), ratified for
[issue #445](https://github.com/gitdek/invariant/issues/445#issuecomment-6598920767).
