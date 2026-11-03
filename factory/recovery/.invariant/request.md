# Recover decision follow-ups across crashes and watchers

Issue #445 in gitdek/invariant, opened by @invariant-code-factory[bot].

Project: factory/recovery

After the follow-up and protocol amendments, extend recovery to authorize their effects for #421. D-0069 requires this core's permission. It currently knows ordinary Solve/Build runs, replacement builds and watched drafts, not automatic follow-up attempts, same-PR repair pushes or reopening.

Amend recovery for a follow-up identified by its source ratified-plan obligation and a stable branch/PR identity. Distinguish each recorded draft, creation and repair operation from that stable identity. Failed drafting and creation may start a fresh recorded attempt automatically under Workflow; a stopped or completed operation is never unknowingly run twice. Repairs publish new contents to the same PR, invalidate prior CI and consume exactly the allowance authorized by Workflow. A writer's explicit retry/resume is consumed once, renews the allowance to two and, for closed work, permits reopening the same PR. Closure or exhausted repairs remains a hold until such a request. Existing ordinary retry/replacement behavior stays intact.

Represent durable accepted Workflow snapshots and pending/completed effect identities so every accepted snapshot is kept before subsequent work is dispatched. Record agent attempts and publication/merge intent before their effects; reconcile durable results and GitHub observations after crashes or lost responses. Persist accepted merge authorization before dispatch and promote its result only through the amended core's outcome handling. After restart, reconcile success, an unknown response or a definitive refusal; authorization alone never proves a remote merge. Losing a PR-creation response must adopt the already created PR, including when a person has since closed or merged it, rather than creating another. A repair push with a lost response must not spend a second repair. An externally merged PR settles through the amended observation path without fabricated CI or another merge dispatch. Failed/no-change repairs and revision invalidation follow the amended core, retaining their attempt usage and real failure cause.

Preserve lease fencing, at-most-once operation starts, one PR for the source obligation across all attempts and holds, one merge, command-answer deduplication and rejection of stale results from older operations or watchers. Reopening is its own authorized effect; do not reuse replacement's close-and-new-PR transition. Current-head CI and mergeability observations must belong to the revision being acted on. Include progress under the existing fairness assumptions, with human holds respected.

Update the model, proved core, explorer and project README through ratification and run the complete verification gate. Witness automatic draft/create recovery, repair after CI failure, exhaustion then explicit renewal, closed-PR resumption, lost publication/merge responses, and two watchers with crashes. Counterexamples must expose duplicate creation/run/merge, a stale repair result, an unrecorded effect, lease loss, reused CI and resumption without a person. This issue adds recovery rules; later plumbing maps real records and effects onto them.

Basis: D-0069/D-0074, D-0203/D-0210/D-0212/D-0225, #417 and the preceding follow-up/protocol amendments. Preserve their run, identity, allowance and hold guarantees. The merger records this amendment and any root documentation correction required by D-0122.

◉ Opened by Invariant for step 3 of 6 of the plan of issues on #421, which @gitdek [ratified](https://github.com/gitdek/invariant/issues/421#issuecomment-6587654565).

<!-- invariant-plan #421 sha256:35403d5bf0549f22c8840b97420b89389648fabb0ba0fba8f24cd1ffc5dc23d2 step 3 -->

## Decided

- **F1. When a writer gives retry/resume while a follow-up repair or its publication is still unresolved, when does the request renew the allowance, and may that repair still publish?** A. The system keeps the request pending until the existing repair and any publication are reconciled, then consumes the request once and renews the allowance to two. (decided by @gitdek: https://github.com/gitdek/invariant/issues/445#issuecomment-6594853236)

## Discussion

**@gitdek:**

The draft's last two check runs failed on a parse error the check's message didn't show. SANY says, of the draft's `WatcherRecovery.tla`:

```
***Parse Error***
Item at line 320, col 5 to line 320, col 5 of module WatcherRecovery is not properly indented inside conjunction or disjunction list item at line 314, col 5 to line 320, col 5 of module WatcherRecovery
```

Fix that indentation first. Then the failures from the draft's second check run remain: the witnesses `RetryBuilt`, `Replaced`, `FailedAgain`, `StoppedAfterRetiring`, `RepairAfterCIFailure`, `ClosedFollowupResumed`, `UnknownMergeRecorded`, `PendingRenewalReconciled` and `FailedAndNoChangeRepairsRetained`, and the property `CommandsAnswered`. A check that says only "Parsing or semantic analysis failed" means SANY rejected the module: look at conjunction and disjunction indentation, and at operators used before they're defined.

**@gitdek:**

Before this is ratified: the follow-up half of the model is scripted. `FSelectScenario` picks one of `FollowCases`, and `CASE follow.scenario = …` then fixes each outcome: which run fails, which response is lost, whether GitHub merges or refuses. TLC checks the new invariants only along those 19 scripts, so a combination, such as a lost push in a follow-up someone later merges, is never explored. The existing half of the model leaves its environment free, with `\E ready \in BOOLEAN` and `\E c \in Cmds : Give(c)`, and the follow-up half should too.

Model the follow-up's environment as choices at each effect instead:

- An agent run's result is any of success, failed, stopped or no change.
- A creation's or publication's response can be lost.
- A merge can succeed, be refused or come back unknown.
- A person can close or merge the pull request, or move its head, at any point.

Bound how many of each can happen, as `MaxCrashes` bounds crashes, so TLC explores every combination within the bounds, and drop `FollowCases`. Keep a witness for each case the scenarios covered.

**@gitdek:**

Most of this gate failure isn't the Go code:

- **The build changed the ratified model.** The ratified `Init` picks the lane with `lane \in {"ordinary", "followup"}`, so it has two initial states. The build made `Init` ordinary only, and added a `ChooseFollowup` step that turns `follow` from the string `"inactive"` into the follow-up record, "so the executable model has one Init". `WatchedDraftsSettled` and `FollowAuthorizedWorkStarts` fail on that step. Checking fairness, TLC compares `follow` before and after it, a string with a record, and throws instead of answering.
- **`FollowKnownMergesSettled` failed because I stopped its TLC run.** That run deadlocked after its first state and sat idle for nearly two hours. #470 has the details and the fix.
- **Conformance failed inside TLC:** "unsuccessfully trying to load custom FPSet class: tlc2.tool.fp.MSBDiskFPSet", then a NullPointerException.
- **Gobra failed on the code.**

The ratified model's two initial states are fine for TLC, and its check passed with them. The real problem is that the executable model wants a single `Init`. A revise should give it one without mixing a string and a record in `follow`: for example, keep `follow` a record in both lanes, inactive in the ordinary lane. I'll ask for that revise once the watchers have settled on today's release, behind #452's next step.

**@gitdek:**

What to change, from my comment above: give the model one initial state, so the executable model needs no `Init` of its own, and keep `follow` the same kind of value in every state. For example, every behavior could start in the ordinary lane with `follow` a record whose phase says it's inactive, and a first step could choose the follow-up lane. Comparing `follow` before and after that step then compares two records. Keep every invariant, property, witness and known bug as ratified, with their meanings unchanged, and check the amended model with TLC as before.

**@gitdek:**

What to change is in my two comments above: one initial state, with `follow` the same kind of value in every state, and every ratified statement kept with its meaning.
