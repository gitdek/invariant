# Unique decision IDs

Built by hand for [#192](https://github.com/gitdek/invariant/issues/192): Prove that decision IDs are unique (#177, part 1)

@gitdek ratified these statements [on the issue](https://github.com/gitdek/invariant/issues/192#issuecomment-5899177695). They're pinned by hash in [`ratified.lock`](.invariant/ratified.lock), and their text is in [`.invariant/specs/DecisionIDs.tla`](.invariant/specs/DecisionIDs.tla).

| Statement | Kind | Says |
| :-- | :-- | :-- |
| `Spec` | spec | Checkouts on branches of their own decide at once, under the store's lock, and a decide can stop at any step. Branches merge into main and take main in. |
| `TypeOK` | invariant | The store holds IDs, each checkout and main hold decisions, the lock is a checkout's or no one's, and each decide is idle, has taken an ID, has had the store record it, or has written it. |
| `NoSharedID` | invariant | No two decisions share an ID, in any checkout, on any branch, merged or not. |
| `BothDecided` | witness | Both checkouts can write decisions. |
| `BothMerged` | witness | Main can hold decisions from both checkouts. |
| `GapAfterStop` | witness | A decide that stops after the store records its ID leaves a gap: an ID no decision has. |
| `WriteBeforeReserve` | bug | The journal file is written before the store records the ID, so a stop between them leaves an ID the store doesn't know, and another checkout takes it again. |
| `TakeWithoutLock` | bug | A decide takes its ID without the store's lock, so two checkouts take the same one. |
| `CheckoutOnly` | bug | A decide takes one more than the highest ID its checkout holds, and never asks the store. |
