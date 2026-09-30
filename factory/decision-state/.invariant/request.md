# Prove how a decision's state follows from its journal (#177, part 2)

Issue #193 in gitdek/invariant, opened by @gitdek.

Part 2 of #177's four parts, built by hand while the factory is paused (D-0131).

A decision's state is its journal's lines, folded in order. `invariant decisions` folds them whenever it reads a journal, and refuses a line that breaks these rules. What must be true:

- A journal's first line records its decision, and no later line does.
- Only a person ratifies, and a ratify line names a person.
- An agent never decides a one-way door: it proposes one, until a person ratifies it.
- A decision that's ratified or superseded isn't ratified again.
- Every checkout that holds the same lines agrees on each decision's state.

The proved core goes in `factory/decision-state`, and `invariant decisions` folds journals with it.

The code is package `state`: a Go core that holds a decision's journal and state, and takes each of the model's steps, `Decide`, `Ratify` and `Supersede`. A writer is a person or an agent. Every operation decides for itself whether it runs: where the model's action can't run, it refuses and changes nothing, and its Gobra contract says both outcomes. `Try` tries every step the model's Next names, for every writer, door and status, in every state, and lets the code refuse, and `Abstract` gives a state as the model sees it (D-0090). `invariant decisions` folds each journal with the core: each line is one of its steps, and a line the core refuses is refused.

Write the Gobra proofs in the style of `factory/ids`, whose core the gate proved: fixed-capacity slices with quantified permissions, plain loops with invariants, and pure functions that don't recurse. Avoid recursive ghost functions over sequences, slicing sequences inside pure functions, and sequence conversions of slices: on the first try, Gobra crashed on them for one part and never finished for another. A proof that runs past 20 minutes fails the gate.
