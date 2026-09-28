# Rebuild the factory's recovery core's explorer to try every step

Issue #68 in gitdek/invariant, opened by @gitdek.

The factory's recovery core's explorer returns only the states it reaches, through `Successors`. It drops the steps the code refuses, and some steps it only tries where it has already checked that the model allows them. So no check can say it tried every step (D-0082). Rebuild it with `Try` and `Abstract` (D-0090), so the gate explores the code itself and checks every attempt:

- Every operation decides for itself whether it runs, as most already do. Where the model's action can't run, it refuses and changes nothing, and its Gobra contract says both outcomes.
- `Try` tries every step the model's Next names, for every watcher and command, in every state, and lets the code refuse. Only `MaxCrashes`, the environment's bound, may leave a crash untried.
- The watcher runs on this core: it uses `Repo`, `Watcher`, `NewRepo`, `NewWatcher`, their methods, and the constants `Solve`, `Ratify`, `Note`, `Build`, `Merge`, `NoRun`, `RunDone` and `RunRecorded`. Keep every exported name, and what it means.

What must be true doesn't change. Keep every ratified statement exactly as it is.

Project: factory/recovery


_Posted for @gitdek by a coding agent, through the dashboard._

## Discussion

**@gitdek:**

_Posted for @gitdek by a coding agent, through the dashboard._
