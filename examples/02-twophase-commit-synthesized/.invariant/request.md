# Rebuild the synthesized two-phase commit's explorer to try every step

Issue #66 in gitdek/invariant, opened by @gitdek.

The synthesized Go two-phase commit's explorer returns only the states it reaches, through `Successors`, and calls each operation only where it has already checked that the model allows it. So no check can say the code refuses what the model rules out (D-0082). Rebuild it with `Try` and `Abstract` (D-0090), so the gate explores the code itself and checks every attempt:

- Every operation decides for itself whether it runs. Where the model's action can't run, it refuses and changes nothing, and its Gobra contract says both outcomes.
- `Try` tries every step the model's Next names, for every resource manager, in every state, and lets the code refuse. The model has no numeric bound, so no step goes untried anywhere.

What must be true doesn't change. Keep every ratified statement exactly as it is.

Project: examples/02-twophase-commit-synthesized


_Posted for @gitdek by a coding agent, through the dashboard._

## Discussion

**@gitdek:**

_Posted for @gitdek by a coding agent, through the dashboard._
