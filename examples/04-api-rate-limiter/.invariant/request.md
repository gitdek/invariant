# Rebuild the rate limiter's driver on the harness

Issue #47 in gitdek/invariant, opened by @gitdek.

The rate limiter's conformance driver records only its runs, so its receipt says its steps weren't checked (D-0082). Rebuild the driver on Invariant's harness (D-0086), so the gate can check that it tried every step:

- It tries every step the model's Next names, with every argument, in every state it reaches, and lets the code refuse what the model rules out. Only the environment's bounds stop a step: how many calls are made, and how far the clock runs.
- The capacity, and how many calls may wait, are sizes the code takes, so name them as parameters in the manifest. The driver tries a call when the queue is full, and sees the code refuse it.
- It keeps the bounds as constants named after the model's, so the gate counts one size larger too.

What must be true doesn't change. Keep every ratified statement exactly as it is, and change the code only where the new driver finds it doesn't do what the model says.

Project: examples/04-api-rate-limiter


_Posted for @gitdek by a coding agent, through the dashboard._

## Discussion

**@gitdek:**

_Posted for @gitdek by a coding agent, through the dashboard._
