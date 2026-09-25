# Bounded log shipping buffer, in Python

The statements @gitdek ratified on [#1](https://github.com/gitdek/invariant/issues/1), with the code written in Python and proved with Nagini. The lock is the same one the [Go version](../03-log-buffer) carries, and its proposal hash is the same. Only the language changed, and the language isn't part of what was ratified.

Invariant's synthesis wrote everything here from the ratified statements and the request alone, and passed on its first gate run: 13 turns.

- [`logbuffer/core.py`](logbuffer/core.py) is marked `# +nagini`. It holds the state and one function per TLA+ action, and each contract restates its action: `Requires` for the enabling condition, and `Ensures` for the effect and everything left unchanged. Nagini proves all 5.
- [`logbuffer/explore.py`](logbuffer/explore.py) is plain Python that lists a state's successors. Nagini doesn't check it, and the receipt says so.
- [`conformance.py`](conformance.py) explores the core completely and records a run for every step it can take. TLC checks every run, and the driver visits all 87 of the model's states.
- [`test_logbuffer.py`](test_logbuffer.py) runs with `unittest`.

The receipt says **proved** (D-0039). Nagini proves each function against its contract, in every state its precondition allows. Exhaustive conformance shows the functions, wired together, reach exactly the model's states.

Nagini runs in its pinned, linux/amd64-only sandbox (D-0031, D-0032). The code also runs as ordinary Python in `python:3.13-alpine`, with the standard library only.

```bash
go run ./cmd/invariant verify examples/03-log-buffer-py
```
