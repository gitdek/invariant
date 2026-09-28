# Rebuild the connection pool's explorer to try every step

Issue #64 in gitdek/invariant, opened by @gitdek.

The connection pool's explorer returns only the states it reaches, through `Successors`. It drops the steps the code refuses, and some steps it only tries where it has already checked that the model allows them. So no check can say it tried every step (D-0082). Rebuild it with `Try` and `Abstract` (D-0090), so the gate explores the code itself and checks every attempt:

- Every operation decides for itself whether it runs, as `Acquire` and `Release` already do. Where the model's action can't run, it refuses and changes nothing, and its Gobra contract says both outcomes.
- `Try` tries every step the model's Next names, for every client, in every state, and lets the code refuse.
- `Size` is the pool's own limit, so `Try` still tries an `Acquire` when every connection is out and sees the code refuse it. Name `Size` as a parameter in the manifest.

What must be true doesn't change. Keep every ratified statement exactly as it is.

Project: examples/05-connection-pool


_Posted for @gitdek by a coding agent, through the dashboard._

## Discussion

**@gitdek:**

_Posted for @gitdek by a coding agent, through the dashboard._
