# Example: two-phase commit in Python

The ratified two-phase commit from [the Go example](../02-twophase-commit), implemented as ordinary Python. [`twophase/transaction.py`](twophase/transaction.py) is a small `Transaction` library, written the way you'd write it in any codebase. Nothing in it was written for a verifier.

It isn't proved. It's **tested against the model**:

1. The driver, [`conformance.py`](conformance.py), creates transactions and calls their operations at random: `prepare`, give up, record a vote, commit, abort and learn. After each call it records the transaction's state in the spec's vocabulary. That mapping, the `abstract` function, is the one part a person writes for Invariant.
2. TLC checks the recorded runs against the ratified model. Every run must start in a state `Init` allows. Every step that changes the state must be a `Next` step. A refused operation changes nothing, which is always allowed.
3. The runs use a fixed seed: 1,000 runs of 40 steps. So the receipt's numbers reproduce.

The statements, their pins and the design checks are the same as the Go example's. The sandbox is `python:3.13-alpine`, standard library only, with no network. The build step is `compileall` and `unittest`.

Plant the early-commit bug, a coordinator that commits after a single vote, and the gate fails at that exact step. The integration tests do exactly this.

```bash
go run ./cmd/invariant verify examples/02-twophase-commit-py
```
