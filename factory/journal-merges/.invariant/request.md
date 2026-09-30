# Prove that merges only grow main's journal (#177, part 3)

Issue #194 in gitdek/invariant, opened by @gitdek.

Part 3 of #177's four parts, built by hand while the factory is paused (D-0131).

Branches reach main through pull requests, and CI's decision check keeps main's journal whole. What must be true:

- A branch merges only when each of main's journal files is the start of the branch's file (D-0103).
- Merging never changes or removes a line main already has.
- A merge adds the branch's new files and lines, and no line a branch added is lost.
- When two branches add lines to the same decision, the second to merge takes main in first, and a line main already has stays where it is.

The proved core goes in `factory/journal-merges`, and `invariant decisions check` checks a branch's journal against main's with it.

The code is package `merges`: a Go core that holds main's and each branch's journal files, and takes each of the model's steps, `Add`, `Merge` and `TakeMainIn`, each for a branch, and `Add` for one of its files. Every operation decides for itself whether it runs: where the model's action can't run, it refuses and changes nothing, and its Gobra contract says both outcomes. `Try` tries every step the model's Next names, for every branch and file, in every state, and lets the code refuse, and `Abstract` gives a state as the model sees it (D-0090). `invariant decisions check` checks a branch's journal against main's with the core's `StartsWith`, the rule a merge must meet: main's file is the start of the branch's.

Write the Gobra proofs in the style of `factory/ids`, whose core the gate proved: fixed-capacity slices with quantified permissions, plain loops with invariants, and pure functions that don't recurse. Avoid recursive ghost functions over sequences, slicing sequences inside pure functions, and sequence conversions of slices: on the first try, Gobra crashed on them for one part and never finished for another. A proof that runs past 20 minutes fails the gate.

Don't quantify over sequences in a contract or a loop invariant, as `exists s seq[int]` does, and keep no ghost sequence. Say what taking main in keeps with quantifiers over integer positions alone: main's lines stay where they are, each line after them is one of the branch's lines that main doesn't have, and each of those is among them. Conformance checks the exact lines, in order, against the model. Give every slice a length bound, as `MaxCapacity` does, so no loop counter can overflow, and in a loop invariant, grant access to a slice before any line that reads it. The last build of this part crashed Gobra with existentials over sequences, then ran out of time with counters that could overflow and an invariant that read a slice before its access.
