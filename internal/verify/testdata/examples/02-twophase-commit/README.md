# Example: two-phase commit

In two-phase commit, a transaction manager commits only after every resource manager has prepared. Until it prepares, any resource manager may abort. This is slice 1's proving ground: a hand-built project that the gate checks end to end.

## What was ratified

The statements live in [`.invariant/ratified.lock`](.invariant/ratified.lock). Each one is pinned by hash to its text in [`TwoPhase.tla`](.invariant/specs/TwoPhase.tla).

| Statement | Kind | Says |
| :-- | :-- | :-- |
| `TypeOK` | invariant | Every variable always holds a value of the expected type. |
| `TCConsistent` | invariant | No resource manager commits while another aborts. |
| `AllCommitted` | witness | Every resource manager can end up committed. |
| `AllAborted` | witness | Every resource manager can end up aborted. |
| `EarlyCommit` | bug | The coordinator commits once any resource manager has prepared, instead of waiting for all of them. It must violate `TCConsistent`. |
| `Spec` | spec | The system starts in `Init`, and every step is a `Next` step. |

TLC checks them with three resource managers, `RM = {r1, r2, r3}`. Each pin covers the statement and everything it depends on, so `TypeOK`'s pin covers `Messages` too. The model itself, meaning `Init`, the actions and `Next`, is the factory's to write.

## What the gate checks

1. All six statements still match their pins.
2. TLC explores every reachable state of the model, 288 in all. `TypeOK` and `TCConsistent` hold in every one, and none of them deadlocks.
3. Both witnesses are reachable, which shows the invariants aren't holding only because the model does nothing.
4. With `EarlyCommit` added to the model as an extra action, TLC reports a `TCConsistent` violation in five steps:
   - r1 prepares.
   - The coordinator records r1's vote.
   - r2 aborts on its own.
   - The buggy coordinator commits anyway, on r1's vote alone.
   - r1 commits, while r2 has aborted.
5. Agreement: exploring the Go code from `Init()` through `Successors()` reaches exactly the 288 states TLC found, at the same depth.
6. Gobra verifies all ten functions in [`twophase.go`](twophase/twophase.go). Each step function's contract restates one TLA+ action. Gobra also checks that every array index is in range and that no integer operation overflows. `Successors`, in [`explore.go`](twophase/explore.go), is plain Go. Gobra can't verify `append`, so the agreement check covers it instead.
7. `go vet` and `go test` pass in a sandbox with no network.

## Run it

Run these from the repository root, with Docker running:

```bash
go run ./cmd/invariant verify -out out examples/02-twophase-commit
```

```bash
go run ./cmd/invariant trace out/traces/early-commit.json
```
