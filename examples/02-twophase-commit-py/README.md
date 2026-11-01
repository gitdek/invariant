# Example: two-phase commit in Python

The ratified two-phase commit from [the Go example](../02-twophase-commit), implemented as ordinary Python. [`twophase/transaction.py`](twophase/transaction.py) is a small `Transaction` library, written the way you'd write it in any codebase. It keeps each participant's and the coordinator's state in [`twophase/core.py`](twophase/core.py), a core marked `# +nagini`, and carries the messages itself.

Nagini proves the core's 8 functions against their contracts, so the receipt says **proved**. The library around it is **tested against the model**:

1. The driver, [`conformance.py`](conformance.py), explores the library with Invariant's harness. In every state it reaches, it makes the `Transaction` again and tries every step the model's `Next` names, for every resource manager: `prepare`, give up, record a vote, commit, abort and learn. It records each attempt in the spec's vocabulary, refusals included. That mapping is `abstract`, in [`twophase/explore.py`](twophase/explore.py).
2. TLC checks the recorded runs against the ratified model. Every run must start in a state `Init` allows. Every step that changes the state must be a `Next` step. A refused operation changes nothing, which is always allowed.
3. The driver reaches all 288 of the model's states and tries all 4,896 steps there, so the code and the model reach exactly the same states.

The statements, their pins and the design checks are the same as the Go example's. The sandbox is `python:3.13-alpine`, standard library only, with no network. The build step is `compileall` and `unittest`.

Plant the early-commit bug, a coordinator that commits after a single vote, and the gate fails at that exact step. The integration tests do exactly this.

```bash
go run ./cmd/invariant verify examples/02-twophase-commit-py
```
