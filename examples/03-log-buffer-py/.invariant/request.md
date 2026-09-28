# Rebuild the Python log buffer's driver on the harness

Issue #49 in gitdek/invariant, opened by @gitdek.

The Python log buffer's conformance driver records only its runs, so its receipt says its steps weren't checked (D-0082). Rebuild the driver on Invariant's harness (D-0086), so the gate can check that it tried every step:

- It tries every step the model's Next names, with every argument, in every state it reaches, and lets the code refuse what the model rules out. Only the environment's bound stops a step: how many lines each producer writes.
- The buffer's capacity is a size the code takes, so name it as a parameter in the manifest. The driver tries a write into a full buffer, and sees the core do what the model says.
- It keeps the bounds as constants named after the model's, so the gate counts one size larger too.
- The core stays proved by Nagini.

What must be true doesn't change. Keep every ratified statement exactly as it is, and change the code only where the new driver finds it doesn't do what the model says.

Project: examples/03-log-buffer-py


_Posted for @gitdek by a coding agent, through the dashboard._

## Discussion

**@gitdek:**

_Posted for @gitdek by a coding agent, through the dashboard._
