# Rebuild the Go log buffer's explorer to try every step

Issue #67 in gitdek/invariant, opened by @gitdek.

The Go log buffer's explorer returns only the states it reaches, through `Successors`, and calls each operation only where it has already checked that the model allows it. So no check can say the code refuses what the model rules out (D-0082). Rebuild it with `Try` and `Abstract` (D-0090), so the gate explores the code itself and checks every attempt:

- Every operation decides for itself whether it runs. Where the model's action can't run, it refuses and changes nothing, and its Gobra contract says both outcomes.
- `Try` tries every step the model's Next names, for every producer, in every state, and lets the code refuse. Only `MaxLines`, the environment's bound, may leave a producer's write untried.
- `Capacity` is the buffer's own limit, so `Try` still tries a write into a full buffer and sees the code refuse it. Name `Capacity` as a parameter in the manifest.

What must be true doesn't change. Keep every ratified statement exactly as it is.

Project: examples/03-log-buffer


_Posted for @gitdek by a coding agent, through the dashboard._

## Discussion

**@gitdek:**

_Posted for @gitdek by a coding agent, through the dashboard._
