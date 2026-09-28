# Rebuild the TypeScript two-phase commit's driver on the harness

Issue #50 in gitdek/invariant, opened by @gitdek.

The TypeScript two-phase commit's conformance driver samples runs at random, so no check can say it tried every step in every state (D-0082). Rebuild it on Invariant's harness (D-0086, D-0088), so it explores every state the code can reach and the gate can check that it tried every step there:

- It tries every step the model's Next names, for every resource manager, in every state it reaches, and lets the code refuse what the model rules out. The model has no numeric bound, so no step goes untried anywhere.

What must be true doesn't change. Keep every ratified statement exactly as it is, and change the code only where the new driver finds it doesn't do what the model says.

Project: examples/02-twophase-commit-ts


_Posted for @gitdek by a coding agent, through the dashboard._

## Discussion

**@gitdek:**

_Posted for @gitdek by a coding agent, through the dashboard._
