# Example: two-phase commit in Python, proved

The ratified two-phase commit from [the Go example](../02-twophase-commit), written as a Python core for [Nagini](https://github.com/marcoeilers/nagini). It's **proved**, and it's tested against the model as well.

- [`twophase/core.py`](twophase/core.py) is marked `# +nagini`. It holds the state and one function for `Init` and for each of the seven actions. Each function's contract restates its action. `Requires` is the action's enabling condition. `Ensures` is its effect, including everything the action leaves unchanged. Nagini proves all 8 contracts and every list index, and checks that each function touches only what its permissions allow.
- [`twophase/explore.py`](twophase/explore.py) is plain Python that lists a state's successors. Nagini doesn't check it, and the receipt says so.
- [`conformance.py`](conformance.py) explores the core completely, breadth first, and records a run for every step the code can take. TLC checks that each step is one the model allows. The runs visit all 288 of the model's states.
- [`test_core.py`](test_core.py) runs with `unittest`.

Together these say two things. The proof says each function meets its contract, in every state its precondition allows. Conformance says the functions, wired together, reach exactly the model's states and take only the model's steps. Nobody has to read the contracts. People ratified the TLA+ statements, and the gate ties everything back to them.

## A bug only the proof can see

A proof covers states that no run reaches. The integration tests plant a `tm_commit` that sends the Commit message only if no Abort was sent:

```python
s.commit_msg = not s.abort_msg
```

Every run gets the same result as the correct code, because the coordinator can't have aborted before it commits. The tests pass, and conformance finds no step outside the model. But the precondition doesn't rule an abort out, so the contract doesn't hold, and Nagini rejects it:

```
core.py:78:12: Postcondition of tm_commit might not hold. Assertion s.commit_msg might not hold.
```

## The sandbox

Nagini runs with no network, in an image Invariant builds from [its own recipe](../../internal/toolchain/nagini.Dockerfile). Every input is pinned. The base is pinned by digest. The Java runtime is copied from the Temurin image TLC runs in. Every Python package is pinned by the hash of its exact wheel. The image is linux/amd64 only, because Nagini pins a different Z3 on ARM. One image keeps receipts identical between a laptop and CI, and Apple silicon runs it under emulation. The first run builds it, which downloads about 260 MB.

The core also runs as ordinary Python. A no-op stand-in for Nagini's contract library takes the real one's place, so the tests and the driver run in `python:3.13-alpine`, like any other Python project.

```bash
go run ./cmd/invariant verify examples/02-twophase-commit-py-proved
```
