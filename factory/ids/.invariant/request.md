# Prove that decision IDs are unique (#177, part 1)

Issue #192 in gitdek/invariant, opened by @gitdek.

Part 1 of #177's four parts, built by hand while the factory is paused (D-0131).

`invariant decisions decide` takes a new decision's ID for a project. Checkouts on one machine, each on a branch of its own, decide at once, and the machine's one store holds the IDs taken. What must be true:

- No two decisions share an ID, in any checkout, on any branch, merged or not.
- A decide holds the store's lock from start to finish. It takes one more than the highest ID the store has taken or the checkout holds.
- A decide can stop at any step. A stop may leave a gap in the numbers, but never lets another decide take an ID again.
- Branches merge into main, and take main in.

The proved core goes in `factory/ids`, and `invariant decisions decide` runs on it, as the watcher runs on `factory/protocol`.

The code is package `ids`: a Go core that holds the model's state and takes each of its steps, `Take`, `Reserve`, `Write`, `Finish`, `Stop`, `Merge` and `Pull`, each for a checkout. Every operation decides for itself whether it runs: where the model's action can't run, it refuses and changes nothing, and its Gobra contract says both outcomes. `Try` tries every step the model's Next names, for every checkout, in every state, and lets the code refuse, and `Abstract` gives a state as the model sees it (D-0090). `invariant decisions decide` takes each ID with the core's `NextID`, one more than the highest ID the store has taken or the checkout holds, and takes its steps in the core's order: take the ID, have the store record it, write the journal file, let the lock go.
