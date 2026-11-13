---
id: D-0470
title: What a bug's statement says of the code is part of what a person ratifies
date: 2026-11-13
door: one-way
status: proposed
proposed_by: agent
source: "#857's ratified plan, step 2 of #789, which asks whoever merges it to record the classification and compatibility decisions"
---

# D-0470 · What a bug's statement says of the code is part of what a person ratifies

**Goal.** A known bug is a statement the model has to catch. Until now it said nothing about the code: a bug the issue reports in code that exists read the same as a mistake someone might make one day. [#789](https://github.com/gitdek/invariant/issues/789) wants the first kind shown against the code, by replaying TLC's counterexample, and that starts with the statement saying which kind it is. [#857's plan](../.invariant/plans/issue-857.json) adds that, and makes it part of what a person ratifies.

## What it is

- **A bug statement may carry `code_behavior`.** It's `current` when the code has the bug today, as the issue, unchanged code or tests report, and `hypothetical` when nothing says it does. No other kind of statement carries it.
- **It's in the ratified identity.** The field enters the proposal hash with the rest of the statement. Adding it, changing it and taking it away are each an amendment that needs its own ratification, and the scope and ratification checks refuse a lock that differs by it from the proposal a person ratified.
- **A lock from before it is left exactly as it is.** A statement without the field keeps its JSON and its hash. Nothing infers a classification for it and nothing migrates it. Its bugs read "not replayed: not classified", and the gate asks no replay of them.
- **A new proposal classifies every bug.** So does a preview. An amendment classifies each bug it adds or changes, and keeps a bug its lock leaves unclassified exactly as the lock has it ([D-0472](journal/D-0472.jsonl)).

## Why it's a one-way door

Every lock ratified with a classification from here on rests on how the field is hashed. Changing that later changes those locks' identities, and each would need a person to ratify it again.

## Compatibility

A gate from before this change reads a lock that classifies a bug without the classification. The proposal hash it computes is the one the statements would have without the field, which isn't the one a person ratified, so its ratification check fails. A repository's CI pins the gate it runs: it moves that pin before its factory drafts with this rule.

## Options considered

| Option | What it gives | What it costs |
| :-- | :-- | :-- |
| **A. An optional field on the bug statement, hashed when present** (the plan) | What the statement says of the code is ratified with it, and no existing lock changes | Two kinds of lock for good: classified, and from before |
| B. The classification beside the lock, in the manifest or a file of its own | No change to what's hashed | It could be changed without a ratification, and a `current` bug made `hypothetical` drops the replay the gate owes it |
| C. A required field, with every lock migrated | One kind of lock | Every project's ratification is void at once, and a migration has to guess what each old bug meant |
| D. Infer it when the gate runs, from the issue or the code | Nothing to ratify | The gate decides what it has to show, from text nobody pinned |

## What would reopen it

- A third kind of bug, such as one fixed in the code and kept so that it stays fixed.
- A project whose code is in more than one language, where a bug is current in one and not another.

## Questions for @gitdek, with a recommendation

1. **Keep A?** Recommend yes. It's what the plan ratified on #857 states and what's built. This record is here because a one-way door gets one, and it stays `proposed` until you ratify it.
