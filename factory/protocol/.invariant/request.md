# Rebuild the factory's protocol's explorer to try every step

Issue #69 in gitdek/invariant, opened by @gitdek.

The factory's protocol's explorer returns only the states it reaches, through `Successors`, and calls each operation only where it has already checked that the model allows it. So no check can say the code refuses what the model rules out (D-0082). Rebuild it with `Try` and `Abstract` (D-0090), so the gate explores the code itself and checks every attempt:

- Every operation decides for itself whether it runs. Where the model's action can't run, it refuses and changes nothing, and its Gobra contract says both outcomes.
- `Try` tries every step the model's Next names, for every actor, question, proposal, head and lock, in every state, and lets the code refuse. The model has no numeric bound, so no step goes untried anywhere.
- The watcher checks each of its own steps with `Successors(State) []State`, so keep it, with the same meaning: every state one step of Next reaches from s. Keep `State` and every constant the watcher uses: the `Kind*`, `Gate*`, `Fail*` and `By*` values, `H1`, `H2`, `P1`, `P2`, `NoP`, `NoHead`, `NoActor`, `Nobody` and `ScopeMany`.

What must be true doesn't change. Keep every ratified statement exactly as it is.

Project: factory/protocol


_Posted for @gitdek by a coding agent, through the dashboard._

## Discussion

**@gitdek:**

_Posted for @gitdek by a coding agent, through the dashboard._
