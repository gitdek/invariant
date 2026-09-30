# Store journal

Built by hand for [#195](https://github.com/gitdek/invariant/issues/195): Prove that the store's journal only grows (#177, part 4)

@gitdek ratified these statements [on the issue](https://github.com/gitdek/invariant/issues/195#issuecomment-5899380865). They're pinned by hash in [`ratified.lock`](.invariant/ratified.lock), and their text is in [`.invariant/specs/StoreJournal.tla`](.invariant/specs/StoreJournal.tla).

| Statement | Kind | Says |
| :-- | :-- | :-- |
| `Spec` | spec | Checkouts start with no lines, and the store's journal starts empty. Checkouts write lines, which the store journals, take each other's lines in through main, and can be abandoned; the store's project can be rebuilt from any checkout. |
| `TypeOK` | invariant | The store's journal and each checkout hold lines, each checkout has written a bounded number, and some checkouts may be abandoned. |
| `StoreOnlyGrows` | invariant | The store's journal only grows: every line it ever held, it holds now. |
| `KeepsEveryLine` | invariant | The store keeps every line any checkout wrote, including the lines of branches that were never merged. |
| `KeptAbandoned` | witness | The store can keep the lines of a branch that was abandoned. |
| `RebuiltFromOther` | witness | Both checkouts can write lines, and one can take in the other's, as a merge through main brings them. |
| `RebuildReplaces` | bug | A rebuild replaces the store's journal with what one checkout holds, dropping the lines only other branches have. |
| `AbandonDrops` | bug | Abandoning a branch drops its lines from the store. |
| `WriteNotJournaled` | bug | A checkout writes a line the store never journals. |
