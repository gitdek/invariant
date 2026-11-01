# Journal merges

Built by hand for [#194](https://github.com/gitdek/invariant/issues/194): Prove that merges only grow main's journal (#177, part 3)

@gitdek ratified these statements [on the issue](https://github.com/gitdek/invariant/issues/194#issuecomment-5899379336). They're pinned by hash in [`ratified.lock`](.invariant/ratified.lock), and their text is in [`.invariant/specs/JournalMerges.tla`](.invariant/specs/JournalMerges.tla).

| Statement | Kind | Says |
| :-- | :-- | :-- |
| `Spec` | spec | Main and every branch start with empty journals. Branches add lines to their files, merge into main only when each of main's files is the start of theirs, and take main in when main has moved past them. |
| `TypeOK` | invariant | Main and each branch hold journal files, each a list of lines, and each branch has added a bounded number of lines. |
| `MainOnlyGrows` | invariant | Merging never changes or removes a line main already has: every file main ever held is the start of the file it holds now. |
| `NoLineLost` | invariant | No line is lost: every line a branch added is in its branch or in main. |
| `BothMergedOneFile` | witness | Both branches can add lines to the same file, and both can reach main. |
| `TookMainIn` | witness | A branch that main moved past can take main in, and then hold lines past main's. |
| `MergeUnchecked` | bug | A branch merges without the check, and its files replace main's, dropping main's lines it doesn't have. |
| `TakeMainInDropsOwn` | bug | Taking main in drops the branch's own lines past what it had of main, so they're lost. |
