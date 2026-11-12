---
id: D-0457
title: Retire the decision follow-up machinery
date: 2026-11-13
door: one-way
status: ratified
ratified_by: "@gitdek"
proposed_by: agent
source: "#810, filed on 2026-11-11 once D-0421 made a ratified plan its own decision record. @gitdek on 2026-11-13: \"ratify 810\"."
---

# D-0457 · Retire the decision follow-up machinery

**Goal.** Nothing in the repository is proved, checked and paid for in every CI run that nothing uses. `factory/protocol` goes back to the statements D-0209 ratifies, and `factory/decision-followup` is removed.

## What it was for

Until 2026-11-11 a merged plan owed a decision: whoever merged it restated in `decisions/` what the plan had already said. #421's plan was to have the factory do that itself, with a second pull request after each merge, a follow-up.

- **`factory/decision-followup`** (#417 as #420, amended by #423 as #425; D-0288) is a proved core for the follow-up's own work: drafting, creation, CI, at most two repairs and the merge. 1,321 lines of Go and a model of 1,084.
- **#428's amendment to `factory/protocol`** (merged as #439; D-0293) taught the protocol to authorize a follow-up. The model grew from 518 lines and 38 statements to 1,122 and 70, and the core gained `followup.go` and about 550 lines of explorer.
- **#445**, the amendment that recovers a follow-up across crashes, was parked in a draft by D-0357, and the plan's later steps with it. @gitdek had stopped the plan after step 3 to put plumbing on any repository first (D-0297).

The watcher never took a follow-up step: the wiring was in the steps that were parked.

## Why it goes

D-0421, by @gitdek's call on 2026-11-11, makes a ratified plan its own decision record. Its file in `.invariant/plans` holds what a person ratified, and CI checks that ratification on every change. So no merged plan owes a decision, and there is nothing for a follow-up to record.

What keeping it cost:

- The amendment made `factory/protocol` CI's longest job: 6.9 minutes on the commit before #439, 20.1 on #439's merge and 22.1 on 2026-11-11. It set the wall clock of every pull request's CI, where the next longest job took 13 minutes.
- `factory/decision-followup` had a job of its own, about 6 minutes in every run.
- Every reader of the protocol's model read 32 statements about a step nothing takes.

## Options considered

- **A. Restore and remove (chosen).** `factory/protocol`'s model, lock, core, explorer, tests and request go back to the proposal D-0209 ratifies, `sha256:7a940fa942df2918af704e9c28d94506ad372aca53072029a85d97f517b45786`, and `followup.go` and its test are removed. Nothing else had changed in the project since #439. `factory/decision-followup` is removed. Nothing is formalized anew: the lock is one a person already ratified, for exactly that proposal.
- **B. Keep both, unused.** It costs about 20 minutes of every pull request's CI and keeps a model that says more than the factory does.
- **C. Keep the proved core, restore only the protocol.** The core would be proved and checked with nothing to authorize its steps.
- **D. Skip the protocol's gate in CI when nothing in it changed.** It hides the cost and leaves the model larger than the factory.

## What changes

- `factory/protocol`: 38 statements, as D-0209 ratifies them. On the restored core the gate passes in about six and a half minutes: 38 of 38 pins match, TLC checks 25,206 states and 632,656 one size larger, Gobra proves 22 of 22 functions, and conformance reaches all 25,206 states in 3,150,750 steps. The watcher's adapter is unchanged: it didn't change in #439 either.
- In all, 2,617 lines of Go and 1,774 of TLA+ are removed.
- `factory/decision-followup` is gone, with its CI job.
- D-0288 and D-0293 are superseded by this decision.
- #551, #445's draft, was closed on 2026-11-11.

## What would reopen it

A decision that merged work owes a record a ratified plan doesn't hold, for example if plans stopped being checked in CI, or if a repository adopted Invariant without `.invariant/plans`. The work is in the history: #420, #425 and #439 hold the cores as they were proved, and #428's proposal is still the one its issue ratified.
